//go:build linux

package artifactfs

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/workloadfence"
	"github.com/Harsh-2002/Orva/internal/ids"
)

const ownerRecordLimit = 64 << 10

// OwnerConfig is trusted boot-owner provisioning, never function input.
type OwnerConfig struct {
	MetadataRoot, CodeRoot, RuntimeRoot, Parent string
	CodeLimits, RuntimeLimits                   Limits
	Budget                                      *workloadfence.Budget
	MaxEpochs                                   int
}

type ownerObject struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

type ownerStoreRecord struct {
	Root     string      `json:"root"`
	Identity ownerObject `json:"identity"`
	Limits   Limits      `json:"limits"`
}

type ownerEpochRecord struct {
	ID        string                       `json:"id"`
	Reference workloadfence.EpochReference `json:"reference"`
}

type ownerRecord struct {
	Version   int                `json:"version"`
	ID        string             `json:"id"`
	Root      ownerObject        `json:"root"`
	Code      ownerStoreRecord   `json:"code"`
	Runtime   ownerStoreRecord   `json:"runtime"`
	Parent    string             `json:"parent"`
	MaxEpochs int                `json:"max_epochs"`
	Epochs    []ownerEpochRecord `json:"epochs"`
}

// Owner copies share one durable association and shutdown lifetime.
type Owner struct{ s *ownerState }

type ownerState struct {
	mu              sync.Mutex
	cfg             OwnerConfig
	meta            *Store
	code, runtime   *Store
	domain          *workloadfence.Domain
	disk            ownerRecord
	quarantine      []OwnerQuarantine
	closing, closed bool
	domainReleased  bool
	ops             persistentOps
	closeFile       func(*os.File) error
}

// OwnerBindings are trusted internal references. The host must use Domain for
// every build/worker and must close the Owner, not its individual stores/domain.
type OwnerBindings struct {
	Code, Runtime *Store
	Domain        *workloadfence.Domain
}

// OwnerQuarantine labels detached inspection data with its bound store role.
type OwnerQuarantine struct {
	Store Kind
	RecoveryEntry
}

func NewOwner(ctx context.Context, cfg OwnerConfig) (*Owner, error) {
	return openLifecycleOwner(ctx, cfg, false, defaultOwnerOps())
}

// OpenOwnerRecovery is same-boot committed-asset recovery, not policy recovery.
// Unknown/incomplete owner transitions deny; no reboot or legacy adoption exists.
func OpenOwnerRecovery(ctx context.Context, cfg OwnerConfig) (*Owner, error) {
	return openLifecycleOwner(ctx, cfg, true, defaultOwnerOps())
}

func defaultOwnerOps() persistentOps {
	return persistentOps{sync: (*os.File).Sync, write: (*os.File).Write, rename: os.Rename, remove: os.Remove}
}

func ownerMetadataLimits() Limits {
	return Limits{Generations: 1, Leases: 1, Staging: 1, Entries: 512,
		PathBytes: 4096, LinkBytes: 4096, ManifestBytes: ownerRecordLimit, Bytes: ownerRecordLimit, FileBytes: ownerRecordLimit}
}

func validateOwnerConfig(ctx context.Context, cfg OwnerConfig) error {
	if ctx == nil || cfg.Budget == nil || !cfg.CodeLimits.valid() || !cfg.RuntimeLimits.valid() ||
		cfg.MaxEpochs < 2 || cfg.MaxEpochs > 64 {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	roots := []string{cfg.MetadataRoot, cfg.CodeRoot, cfg.RuntimeRoot}
	for i, root := range roots {
		if !filepath.IsAbs(root) || filepath.Clean(root) != root || len(root) > 4096 ||
			!utf8.ValidString(root) || strings.ContainsRune(root, 0) {
			return ErrInvalid
		}
		for j, other := range roots {
			if i != j && (root == other || strings.HasPrefix(root, other+"/")) {
				return ErrInvalid
			}
		}
		if _, err := privateMetadata(root, true, 0); err != nil {
			return err
		}
	}
	if !filepath.IsAbs(cfg.Parent) || filepath.Clean(cfg.Parent) != cfg.Parent || len(cfg.Parent) > 4096 ||
		!utf8.ValidString(cfg.Parent) || strings.ContainsRune(cfg.Parent, 0) {
		return ErrInvalid
	}
	return nil
}

func openLifecycleOwner(ctx context.Context, cfg OwnerConfig, recovering bool, ops persistentOps) (*Owner, error) {
	if err := validateOwnerConfig(ctx, cfg); err != nil {
		return nil, err
	}
	s := &ownerState{cfg: cfg, ops: ops, closeFile: (*os.File).Close}
	var err error
	s.meta, err = openStore(cfg.MetadataRoot, ownerMetadataLimits(), !recovering)
	if err != nil {
		return nil, err
	}
	ok := false
	var old []*workloadfence.Domain
	var proofs []*workloadfence.Proof
	defer func() {
		for _, d := range old {
			_ = d.Close()
		}
		if !ok {
			if s.domain != nil {
				_ = s.domain.Close()
			}
			for _, store := range []*Store{s.runtime, s.code, s.meta} {
				if store != nil && store.lock != nil {
					_ = store.lock.Close()
					store.lock = nil
				}
			}
		}
	}()
	if recovering {
		s.disk, err = readOwnerRecord(filepath.Join(cfg.MetadataRoot, "owner.json"))
		if err != nil {
			return nil, err
		}
		if err := s.validateLayout(ctx); err != nil {
			return nil, err
		}
		s.code, err = openStore(cfg.CodeRoot, cfg.CodeLimits, false)
		if err != nil {
			return nil, err
		}
		s.runtime, err = openStore(cfg.RuntimeRoot, cfg.RuntimeLimits, false)
		if err != nil {
			return nil, err
		}
		if err := s.matchStoreRoots(); err != nil {
			return nil, err
		}
		if len(s.disk.Epochs) >= cfg.MaxEpochs {
			return nil, ErrCapacity
		}
		for _, epoch := range s.disk.Epochs {
			d, err := workloadfence.OpenRecoveringUnderBudget(cfg.Budget, s.epochRoot(epoch.ID), cfg.Parent)
			if err != nil {
				return nil, err
			}
			old = append(old, d)
			ref, err := d.Reference(ctx)
			if err != nil {
				return nil, err
			}
			if ref != epoch.Reference {
				return nil, ErrIntegrity
			}
		}
	} else {
		s.disk = ownerRecord{Version: 1, ID: ids.New(), Parent: cfg.Parent, MaxEpochs: cfg.MaxEpochs}
		s.disk.Root, err = ownerIdentity(s.meta.lock)
		if err != nil {
			return nil, err
		}
		s.code, err = openStore(cfg.CodeRoot, cfg.CodeLimits, true)
		if err != nil {
			return nil, err
		}
		s.runtime, err = openStore(cfg.RuntimeRoot, cfg.RuntimeLimits, true)
		if err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.writeIntent(); err != nil {
		return nil, err
	}
	if recovering {
		// No recovered tree is activated or removed before exact old-root proof.
		for _, d := range old {
			proof, err := d.Fence(ctx)
			if err != nil {
				return nil, err
			}
			if err := d.Check(ctx, proof); err != nil {
				return nil, err
			}
			proofs = append(proofs, proof)
		}
		if s.code, err = recoverLockedStore(ctx, s.code); err != nil {
			return nil, err
		}
		if s.runtime, err = recoverLockedStore(ctx, s.runtime); err != nil {
			return nil, err
		}
		if err := validateRecoveredRole(s.code, KindCode); err != nil {
			return nil, err
		}
		if err := validateRecoveredRole(s.runtime, KindRuntime); err != nil {
			return nil, err
		}
	} else {
		if err := os.Mkdir(filepath.Join(cfg.MetadataRoot, "epochs"), 0700); err != nil {
			return nil, ErrIO
		}
		if err := s.syncDir(cfg.MetadataRoot); err != nil {
			return nil, err
		}
		s.code, err = initializePersistent(s.code)
		if err != nil {
			return nil, err
		}
		s.runtime, err = initializePersistent(s.runtime)
		if err != nil {
			return nil, err
		}
		if s.disk.Code, err = ownerStoreIdentity(s.code); err != nil {
			return nil, err
		}
		if s.disk.Runtime, err = ownerStoreIdentity(s.runtime); err != nil {
			return nil, err
		}
	}
	epoch := ownerEpochRecord{ID: ids.New()}
	if err := os.Mkdir(s.epochRoot(epoch.ID), 0700); err != nil {
		return nil, ErrIO
	}
	if err := s.syncDir(filepath.Join(cfg.MetadataRoot, "epochs")); err != nil {
		return nil, err
	}
	s.domain, err = workloadfence.CreateUnderBudget(cfg.Budget, s.epochRoot(epoch.ID), cfg.Parent)
	if err != nil {
		return nil, err
	}
	if _, err := s.domain.PrepareJails(ctx, "cpu", "memory", "pids"); err != nil {
		return nil, err
	}
	epoch.Reference, err = s.domain.Reference(ctx)
	if err != nil {
		return nil, err
	}
	s.disk.Epochs = append(s.disk.Epochs, epoch)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.commit(); err != nil {
		return nil, err
	}
	for i, d := range old {
		if err := d.Check(ctx, proofs[i]); err != nil {
			return nil, err
		}
	}
	if err := s.matchStoreRoots(); err != nil {
		return nil, err
	}
	current, err := s.domain.Reference(ctx)
	if err != nil {
		return nil, err
	}
	if current != epoch.Reference {
		return nil, ErrIntegrity
	}
	if err := s.finishCommit(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, pair := range []struct {
		store *Store
		kind  Kind
	}{{s.code, KindCode}, {s.runtime, KindRuntime}} {
		if recovering {
			s.activateCommitted(pair.store, pair.kind)
		}
		pair.store.binding, pair.store.boundKind = s, pair.kind
	}
	ok = true
	return &Owner{s}, nil
}

func ownerIdentity(f *os.File) (ownerObject, error) {
	info, err := f.Stat()
	if err != nil {
		return ownerObject{}, ErrIO
	}
	st := info.Sys().(*syscall.Stat_t)
	return ownerObject{uint64(st.Dev), st.Ino}, nil
}

func ownerStoreIdentity(s *Store) (ownerStoreRecord, error) {
	id, err := ownerIdentity(s.lock)
	if err != nil {
		return ownerStoreRecord{}, err
	}
	if _, err := privateMetadata(s.root, true, 0); err != nil {
		return ownerStoreRecord{}, err
	}
	current, err := openNoFollow(s.root, true)
	if err != nil {
		return ownerStoreRecord{}, err
	}
	now, err := ownerIdentity(current)
	_ = current.Close()
	if err != nil || now != id {
		return ownerStoreRecord{}, ErrIntegrity
	}
	return ownerStoreRecord{s.root, id, s.limits}, nil
}

func (s *ownerState) matchStoreRoots() error {
	code, err := ownerStoreIdentity(s.code)
	if err != nil || code != s.disk.Code {
		return ErrIntegrity
	}
	runtime, err := ownerStoreIdentity(s.runtime)
	if err != nil || runtime != s.disk.Runtime {
		return ErrIntegrity
	}
	return nil
}

func (s *ownerState) epochRoot(id string) string {
	return filepath.Join(s.cfg.MetadataRoot, "epochs", "epoch-"+id)
}

func readOwnerRecord(path string) (ownerRecord, error) {
	var disk ownerRecord
	raw, err := privateMetadata(path, false, ownerRecordLimit)
	if err != nil {
		return disk, err
	}
	if err := strictJSON(raw, ownerMetadataLimits()); err != nil {
		return disk, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&disk); err != nil {
		return disk, ErrIntegrity
	}
	encoded, err := json.Marshal(disk)
	if err != nil || !bytes.Equal(encoded, raw) || disk.Version != 1 || !generationID(disk.ID) ||
		disk.Root.Inode == 0 || disk.MaxEpochs < 2 || disk.MaxEpochs > 64 ||
		len(disk.Epochs) == 0 || len(disk.Epochs) > disk.MaxEpochs {
		return disk, ErrIntegrity
	}
	seen := make(map[string]bool)
	epochs := make(map[string]bool)
	for _, epoch := range disk.Epochs {
		ref := epoch.Reference
		if !generationID(epoch.ID) || seen[epoch.ID] || epochs[ref.Epoch] || ref.Version != 1 || ref.Depth != 0 ||
			ref.ParentEpoch != "" || ref.InstanceEpoch != ref.Epoch ||
			!ownerHex(ref.Epoch, 32) || !ownerBoot(ref.Boot) ||
			ref.MetadataInode == 0 || ref.GroupInode == 0 {
			return disk, ErrIntegrity
		}
		seen[epoch.ID] = true
		epochs[ref.Epoch] = true
	}
	return disk, nil
}

func ownerHex(value string, size int) bool {
	if len(value) != size || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func ownerBoot(value string) bool {
	return len(value) == 36 && value[8] == '-' && value[13] == '-' &&
		value[18] == '-' && value[23] == '-' &&
		ownerHex(strings.ReplaceAll(value, "-", ""), 32)
}

func (s *ownerState) validateLayout(ctx context.Context) error {
	root, err := ownerStoreIdentity(s.meta)
	d := s.disk
	if err != nil || root.Identity != d.Root || d.Parent != s.cfg.Parent || d.MaxEpochs != s.cfg.MaxEpochs ||
		d.Code.Root != s.cfg.CodeRoot || d.Runtime.Root != s.cfg.RuntimeRoot ||
		d.Code.Limits != s.cfg.CodeLimits || d.Runtime.Limits != s.cfg.RuntimeLimits ||
		d.Code.Identity == d.Runtime.Identity {
		return ErrIntegrity
	}
	names, err := boundedNames(ctx, s.cfg.MetadataRoot, 2)
	if err != nil {
		return err
	}
	if len(names) != 2 {
		return ErrIntegrity
	}
	for _, name := range names {
		if name != "owner.json" && name != "epochs" {
			return ErrIntegrity
		}
	}
	dir := filepath.Join(s.cfg.MetadataRoot, "epochs")
	if _, err := privateMetadata(dir, true, 0); err != nil {
		return err
	}
	names, err = boundedNames(ctx, dir, s.cfg.MaxEpochs)
	if err != nil {
		return err
	}
	if len(names) != len(d.Epochs) {
		return ErrIntegrity
	}
	known := make(map[string]bool)
	for _, e := range d.Epochs {
		known["epoch-"+e.ID] = true
		path := s.epochRoot(e.ID)
		if _, err := privateMetadata(path, true, 0); err != nil {
			return err
		}
		files, err := boundedNames(ctx, path, 1)
		if err != nil {
			return err
		}
		if len(files) != 1 || files[0] != "epoch.json" {
			return ErrIntegrity
		}
	}
	for _, name := range names {
		if !known[name] {
			return ErrIntegrity
		}
	}
	return nil
}

func (s *ownerState) syncDir(path string) error {
	f, err := openNoFollow(path, true)
	if err != nil {
		return ErrIO
	}
	defer f.Close()
	if err := s.ops.sync(f); err != nil {
		return ErrIO
	}
	return nil
}

func (s *ownerState) writeFile(name string, raw []byte) error {
	path := filepath.Join(s.cfg.MetadataRoot, name)
	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return ErrIO
	}
	f := os.NewFile(uintptr(fd), path)
	n, err := s.ops.write(f, raw)
	if err == nil && n != len(raw) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = s.ops.sync(f)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return ErrIO
	}
	return nil
}

func (s *ownerState) writeIntent() error {
	if err := s.writeFile("owner.intent", []byte(`{"version":1}`)); err != nil {
		return err
	}
	return s.syncDir(s.cfg.MetadataRoot)
}

func (s *ownerState) commit() error {
	raw, err := json.Marshal(s.disk)
	if err != nil || len(raw) > ownerRecordLimit {
		return ErrCapacity
	}
	if err := s.writeFile("owner.json.tmp", raw); err != nil {
		return err
	}
	if err := s.ops.rename(filepath.Join(s.cfg.MetadataRoot, "owner.json.tmp"), filepath.Join(s.cfg.MetadataRoot, "owner.json")); err != nil {
		return ErrIO
	}
	if err := s.syncDir(s.cfg.MetadataRoot); err != nil {
		return err
	}
	return nil
}

func (s *ownerState) finishCommit() error {
	if err := s.ops.remove(filepath.Join(s.cfg.MetadataRoot, "owner.intent")); err != nil {
		return ErrIO
	}
	if err := s.syncDir(s.cfg.MetadataRoot); err != nil {
		// Preserve a denial marker if the final unlink barrier did not succeed.
		_ = s.writeIntent()
		return err
	}
	return nil
}

func validateRecoveredRole(store *Store, kind Kind) error {
	for _, e := range store.persistent.recovery {
		if e.Identity.Kind != "" && e.Identity.Kind != kind {
			return ErrIntegrity
		}
	}
	return nil
}

func (s *ownerState) activateCommitted(store *Store, kind Kind) {
	for _, e := range store.persistent.recovery {
		r := store.records[e.Identity.Generation]
		r.quarantined = true
		if e.Phase == "committed" && !e.PendingMetadata {
			r.phase, r.path, r.treeOwned = active, filepath.Join(store.root, "generation-"+e.Identity.Generation), true
			r.quarantined = false
			r.durable = persistentRecord{1, e.Identity.Generation, e.Identity.Kind, e.Identity.Owner,
				e.Identity.Digest, e.ReservedBytes, "committed", e.Manifest}
			raw, _ := json.Marshal(e.Manifest)
			r.manifestBytes = len(raw)
		} else {
			s.quarantine = append(s.quarantine, OwnerQuarantine{kind, e})
		}
	}
	// Only this private, fully proven and durably rebound path changes the mode.
	store.persistent.recovered = false
}

func (o *Owner) Bindings(ctx context.Context) (OwnerBindings, error) {
	if o == nil || o.s == nil || ctx == nil {
		return OwnerBindings{}, ErrInvalid
	}
	s := o.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closing || s.closed {
		return OwnerBindings{}, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return OwnerBindings{}, err
	}
	current, err := readOwnerRecord(filepath.Join(s.cfg.MetadataRoot, "owner.json"))
	if err != nil {
		return OwnerBindings{}, err
	}
	expected, _ := json.Marshal(s.disk)
	actual, _ := json.Marshal(current)
	if !bytes.Equal(expected, actual) {
		return OwnerBindings{}, ErrIntegrity
	}
	if err := s.validateLayout(ctx); err != nil {
		return OwnerBindings{}, err
	}
	if err := s.matchStoreRoots(); err != nil {
		return OwnerBindings{}, err
	}
	ref, err := s.domain.Reference(ctx)
	if err != nil {
		return OwnerBindings{}, err
	}
	if ref != s.disk.Epochs[len(s.disk.Epochs)-1].Reference {
		return OwnerBindings{}, ErrIntegrity
	}
	return OwnerBindings{s.code, s.runtime, s.domain}, nil
}

func (o *Owner) Quarantined() []OwnerQuarantine {
	if o == nil || o.s == nil {
		return nil
	}
	o.s.mu.Lock()
	defer o.s.mu.Unlock()
	out := append([]OwnerQuarantine(nil), o.s.quarantine...)
	for i := range out {
		out[i].Manifest.Entries = append([]Entry{}, out[i].Manifest.Entries...)
	}
	return out
}

// Close denies new store entry, fences the root and retains all locks while
// commands/leases/child ownership drain. Retry after their explicit cleanup.
func (o *Owner) Close(ctx context.Context) error {
	if o == nil || o.s == nil || ctx == nil {
		return ErrInvalid
	}
	s := o.s
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closing = true
	for _, store := range []*Store{s.code, s.runtime} {
		store.mu.Lock()
		store.closed = true
		store.mu.Unlock()
	}
	if !s.domainReleased {
		proof, err := s.domain.Fence(ctx)
		if err != nil {
			return err
		}
		if err := s.domain.Check(ctx, proof); err != nil {
			return err
		}
		s.code.mu.Lock()
		s.runtime.mu.Lock()
		busy := false
		for _, store := range []*Store{s.code, s.runtime} {
			busy = busy || store.leases != 0
			for _, r := range store.records {
				busy = busy || r.phase == verifying || r.phase == deleting || (r.isStage && !r.quarantined)
			}
		}
		if !busy {
			s.code.detached, s.runtime.detached = true, true
		}
		s.runtime.mu.Unlock()
		s.code.mu.Unlock()
		if busy {
			return ErrBusy
		}
		if err := s.domain.Close(); err != nil {
			for _, store := range []*Store{s.code, s.runtime} {
				store.mu.Lock()
				store.detached = false
				store.mu.Unlock()
			}
			return err
		}
		s.domainReleased = true
	}
	for _, store := range []*Store{s.code, s.runtime, s.meta} {
		if err := ctx.Err(); err != nil {
			return err
		}
		if store.lock != nil {
			if err := s.closeFile(store.lock); err != nil && !errors.Is(err, os.ErrClosed) {
				return ErrIO
			}
			store.lock = nil
		}
	}
	s.closed = true
	return nil
}
