//go:build linux

package artifactfs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/internal/ids"
)

const controlName = ".artifactfs-control"
const recordOverhead = 1024
const formatRecord = `{"version":1}`

type persistentRecord struct {
	Version    int      `json:"version"`
	Generation string   `json:"generation"`
	Kind       Kind     `json:"kind"`
	Owner      string   `json:"owner"`
	Digest     string   `json:"digest"`
	Reserved   int64    `json:"reserved"`
	Phase      string   `json:"phase"`
	Manifest   Manifest `json:"manifest"`
}

// Hooks are package-private fault seams, never caller-supplied authority.
type persistentOps struct {
	sync   func(*os.File) error
	write  func(*os.File, []byte) (int, error)
	rename func(string, string) error
	remove func(string) error
}

type persistentState struct {
	ioMu          sync.Mutex
	control       string
	recovered     bool
	manifestBytes int
	ops           persistentOps
	recovery      []RecoveryEntry
	pending       map[string]bool
}

// RecoveryEntry is a detached inspection record, not a path or approval token.
type RecoveryEntry struct {
	Identity          Identity
	Phase             string
	ReservedBytes     int64
	Manifest          Manifest
	StagingPresent    bool
	GenerationPresent bool
	PendingMetadata   bool
}

func newPersistentState(root string) *persistentState {
	return &persistentState{
		control: filepath.Join(root, controlName),
		pending: make(map[string]bool),
		ops: persistentOps{
			sync: (*os.File).Sync, write: (*os.File).Write,
			rename: os.Rename, remove: os.Remove,
		},
	}
}

// NewPersistent initializes only an empty private root. New retains its stricter
// memory-only contract; neither constructor adopts existing trees.
func NewPersistent(root string, limits Limits) (*Store, error) {
	s, err := New(root, limits)
	if err != nil {
		return nil, err
	}
	s.persistent = newPersistentState(root)
	if err = os.Mkdir(s.persistent.control, 0700); err == nil {
		err = s.syncDirectory(root)
	}
	if err == nil {
		err = s.atomicMetadata("format.json", []byte(formatRecord))
	}
	if err != nil {
		_ = s.lock.Close()
		return nil, ErrIO
	}
	return s, nil
}

func (s *Store) syncDirectory(path string) error {
	f, err := openNoFollow(path, true)
	if err != nil {
		return ErrIO
	}
	defer f.Close()
	if err = s.persistent.ops.sync(f); err != nil {
		return ErrIO
	}
	return nil
}

func (s *Store) atomicMetadata(name string, raw []byte) error {
	p := s.persistent
	p.ioMu.Lock()
	defer p.ioMu.Unlock()
	temp := filepath.Join(p.control, name+".tmp")
	if p.pending[temp] {
		if err := p.ops.remove(temp); err != nil && !errors.Is(err, os.ErrNotExist) {
			return ErrIO
		}
		if err := s.syncDirectory(p.control); err != nil {
			return ErrIO
		}
		delete(p.pending, temp)
	}
	fd, err := syscall.Open(temp, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return ErrIO
	}
	p.pending[temp] = true
	f := os.NewFile(uintptr(fd), temp)
	n, err := p.ops.write(f, raw)
	if err == nil && n != len(raw) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = p.ops.sync(f)
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = p.ops.rename(temp, filepath.Join(p.control, name))
	}
	if err != nil {
		// A failed cleanup leaves recognizable but untrusted pending metadata.
		if e := p.ops.remove(temp); e == nil {
			if e = s.syncDirectory(p.control); e == nil {
				delete(p.pending, temp)
			}
		}
		return ErrIO
	}
	delete(p.pending, temp)
	return s.syncDirectory(p.control)
}

func (s *Store) saveRecord(r *record, phase string) error {
	s.mu.Lock()
	r.durable.Phase = phase
	d := r.durable
	s.mu.Unlock()
	raw, err := json.Marshal(d)
	if err != nil || len(raw) > s.limits.ManifestBytes+recordOverhead {
		return ErrCapacity
	}
	return s.atomicMetadata(d.Generation+".json", raw)
}

func (s *Store) beginPersistent(ctx context.Context, reserved int64) (*Staging, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.closed || s.persistent.recovered {
		s.mu.Unlock()
		return nil, ErrClosed
	}
	if reserved <= 0 || reserved > s.limits.Bytes {
		s.mu.Unlock()
		return nil, ErrInvalid
	}
	if len(s.records) >= s.limits.Generations || s.stages >= s.limits.Staging || reserved > s.limits.Bytes-s.bytes {
		s.mu.Unlock()
		return nil, ErrCapacity
	}
	id := ids.New()
	r := &record{
		identity: Identity{Generation: id}, path: filepath.Join(s.root, "staging-"+id),
		reserved: reserved, phase: verifying, isStage: true,
		durable: persistentRecord{Version: 1, Generation: id, Reserved: reserved,
			Manifest: Manifest{Entries: []Entry{}}},
	}
	s.records[id], s.bytes, s.stages = r, s.bytes+reserved, s.stages+1
	s.mu.Unlock()
	st := &Staging{s, r}
	err := s.saveRecord(r, "staging")
	if err == nil {
		err = os.Mkdir(r.path, 0700)
		if err == nil {
			s.mu.Lock()
			r.treeOwned = true
			s.mu.Unlock()
			err = s.syncDirectory(s.root)
		}
	}
	s.mu.Lock()
	if err != nil {
		r.phase = failed
	} else {
		r.phase = staging
	}
	closed := s.closed
	s.mu.Unlock()
	if err != nil {
		return st, ErrIO
	}
	if err = ctx.Err(); err != nil {
		_ = st.Abort()
		return st, err
	}
	if closed {
		_ = st.Abort()
		return st, ErrClosed
	}
	return st, nil
}

func (st *Staging) sealPersistent(ctx context.Context, kind Kind, owner string, manifest Manifest, expected string) (string, error) {
	s, r := st.store, st.rec
	s.mu.Lock()
	if s.closed || s.persistent.recovered {
		s.mu.Unlock()
		return "", ErrClosed
	}
	if r.phase != staging {
		s.mu.Unlock()
		return "", ErrInvalid
	}
	r.phase = verifying
	s.mu.Unlock()
	err := ctx.Err()
	var owned Manifest
	var digest string
	if err == nil {
		if !validOwner(kind, owner) {
			err = ErrInvalid
		} else {
			owned, digest, err = canonical(manifest, s.limits)
		}
	}
	if err == nil && (!validDigest(expected) || expected != digest) {
		err = ErrIntegrity
	}
	if err == nil {
		raw, _ := json.Marshal(owned)
		s.mu.Lock()
		if len(raw) > s.limits.ManifestBytes-s.persistent.manifestBytes {
			err = ErrCapacity
		} else {
			r.manifestBytes = len(raw)
			s.persistent.manifestBytes += len(raw)
		}
		s.mu.Unlock()
	}
	if err == nil {
		err = verify(ctx, r.path, kind, owned, s.limits, r.reserved)
	}
	if err == nil {
		// Failed verification must not make unverified claims durable during Abort.
		s.mu.Lock()
		r.durable.Kind, r.durable.Owner, r.durable.Digest, r.durable.Manifest = kind, owner, digest, owned
		s.mu.Unlock()
	}
	if err == nil {
		err = s.syncTree(ctx, r.path, owned)
	}
	if err == nil {
		// Detect replacements during sync without changing any authored metadata.
		err = verify(ctx, r.path, kind, owned, s.limits, r.reserved)
	}
	if err == nil {
		err = s.publicationContext(ctx)
	}
	if err == nil {
		err = s.saveRecord(r, "publish_intent")
	}
	destination := filepath.Join(s.root, "generation-"+r.identity.Generation)
	renamed := false
	if err == nil {
		err = s.publicationContext(ctx)
	}
	if err == nil {
		if _, e := os.Lstat(destination); !errors.Is(e, os.ErrNotExist) {
			err = ErrIntegrity
		} else if e = s.persistent.ops.rename(r.path, destination); e != nil {
			err = ErrIO
		} else {
			renamed = true
			s.mu.Lock()
			r.path, r.isStage = destination, false
			s.stages--
			r.identity.Kind, r.identity.Owner, r.identity.Digest = kind, owner, digest
			s.mu.Unlock()
			err = s.syncDirectory(s.root)
		}
	}
	if err == nil {
		err = s.publicationContext(ctx)
	}
	if err == nil {
		err = s.saveRecord(r, "committed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		err = ctx.Err()
		if err == nil && s.closed {
			err = ErrClosed
		}
	}
	if err != nil {
		if renamed {
			r.phase = retired
			return r.identity.Generation, err
		}
		r.phase = failed
		return "", err
	}
	r.phase = active
	return r.identity.Generation, nil
}

func (s *Store) publicationContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	return nil
}

func (s *Store) syncTree(ctx context.Context, root string, m Manifest) error {
	// Reverse canonical order puts children before their directories.
	for i := len(m.Entries) - 1; i >= 0; i-- {
		if err := ctx.Err(); err != nil {
			return err
		}
		e := m.Entries[i]
		if e.Type == TypeSymlink {
			continue
		}
		f, err := openNoFollow(filepath.Join(root, e.Path), e.Type == TypeDirectory)
		if err != nil {
			return err
		}
		err = s.persistent.ops.sync(f)
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			return ErrIO
		}
	}
	return nil
}

func (s *Store) retirePersistent(id string) error {
	s.mu.Lock()
	if s.persistent.recovered {
		s.mu.Unlock()
		return ErrRetired
	}
	r := s.records[id]
	if r == nil {
		s.mu.Unlock()
		return ErrInvalid
	}
	if r.phase == deleting {
		s.mu.Unlock()
		return ErrBusy
	}
	if r.phase != active && r.phase != retired {
		s.mu.Unlock()
		return ErrInvalid
	}
	// Deny new leases before I/O; a failed durable retirement remains denied.
	r.phase = verifying
	s.mu.Unlock()
	err := s.saveRecord(r, "retired")
	s.mu.Lock()
	r.phase = retired
	s.mu.Unlock()
	return err
}

func (s *Store) removePersistent(r *record, abort bool) error {
	s.mu.Lock()
	if s.persistent.recovered {
		s.mu.Unlock()
		return ErrRetired
	}
	if r.phase == deleted {
		s.mu.Unlock()
		return nil
	}
	if r.phase == verifying || r.phase == deleting || r.refs != 0 {
		s.mu.Unlock()
		return ErrBusy
	}
	if (abort && (!r.isStage || (r.phase != staging && r.phase != failed))) || (!abort && (r.isStage || r.phase != retired)) {
		s.mu.Unlock()
		return ErrInvalid
	}
	r.phase = deleting
	path := r.path
	owned := r.treeOwned
	s.mu.Unlock()
	err := s.saveRecord(r, "delete_intent")
	if err == nil {
		if owned {
			err = s.removeAll(path)
		} else if _, e := os.Lstat(path); !errors.Is(e, os.ErrNotExist) {
			// A failed Mkdir never grants ownership of an unexpected collision.
			err = ErrIntegrity
		}
	}
	if err == nil {
		err = s.syncDirectory(s.root)
	}
	if err == nil {
		err = s.removeMetadata(r.identity.Generation)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		if r.isStage {
			r.phase = failed
		} else {
			r.phase = retired
		}
		return ErrIO
	}
	r.phase = deleted
	delete(s.records, r.identity.Generation)
	s.bytes -= r.reserved
	s.persistent.manifestBytes -= r.manifestBytes
	if r.isStage {
		s.stages--
	}
	return nil
}

func (s *Store) removeMetadata(id string) error {
	p := s.persistent
	p.ioMu.Lock()
	defer p.ioMu.Unlock()
	for _, suffix := range []string{".json.tmp", ".json"} {
		if err := p.ops.remove(filepath.Join(p.control, id+suffix)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return ErrIO
		}
	}
	return s.syncDirectory(p.control)
}

func generationID(id string) bool {
	return validOwner(KindCode, id) && id[14] == '7'
}

// strictJSON bounds allocation before decoding structs, rejects duplicate keys
// and null at every depth; byte-for-byte reencoding below rejects normalization.
func strictJSON(raw []byte, limits Limits) error {
	if !utf8.Valid(raw) {
		return ErrIntegrity
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	budget := 64 + 24*limits.Entries
	var value func(int) error
	value = func(depth int) error {
		budget--
		if budget < 0 || depth > 5 {
			return ErrCapacity
		}
		token, err := d.Token()
		if err != nil || token == nil {
			return ErrIntegrity
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := make(map[string]bool)
			for d.More() {
				key, err := d.Token()
				name, ok := key.(string)
				if err != nil || !ok || seen[name] || len(seen) >= 16 {
					return ErrIntegrity
				}
				seen[name] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			n := 0
			for d.More() {
				n++
				if n > limits.Entries {
					return ErrCapacity
				}
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		default:
			return ErrIntegrity
		}
		end, err := d.Token()
		if err != nil || (delim == '{' && end != json.Delim('}')) || (delim == '[' && end != json.Delim(']')) {
			return ErrIntegrity
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return ErrIntegrity
	}
	return nil
}

func decodeRecord(raw []byte, id string, limits Limits) (persistentRecord, int, error) {
	var d persistentRecord
	if len(raw) > limits.ManifestBytes+recordOverhead {
		return d, 0, ErrCapacity
	}
	if err := strictJSON(raw, limits); err != nil {
		return d, 0, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&d); err != nil {
		return d, 0, ErrIntegrity
	}
	if d.Version != 1 || d.Generation != id || !generationID(id) || d.Reserved <= 0 || d.Reserved > limits.Bytes {
		return d, 0, ErrIntegrity
	}
	switch d.Phase {
	case "staging", "publish_intent", "committed", "retired", "delete_intent":
	default:
		return d, 0, ErrIntegrity
	}
	manifestBytes := 0
	if d.Kind == "" {
		if (d.Phase != "staging" && d.Phase != "delete_intent") || d.Owner != "" || d.Digest != "" ||
			d.Manifest.Version != 0 || d.Manifest.Entries == nil || len(d.Manifest.Entries) != 0 {
			return d, 0, ErrIntegrity
		}
	} else {
		if d.Phase == "staging" || !validOwner(d.Kind, d.Owner) || !validDigest(d.Digest) {
			return d, 0, ErrIntegrity
		}
		m, digest, err := canonical(d.Manifest, limits)
		if err != nil || digest != d.Digest {
			return d, 0, ErrIntegrity
		}
		var total int64
		for _, e := range m.Entries {
			if e.Size > d.Reserved-total {
				return d, 0, ErrCapacity
			}
			total += e.Size
		}
		d.Manifest = m
		encoded, _ := json.Marshal(m)
		manifestBytes = len(encoded)
	}
	encoded, err := json.Marshal(d)
	if err != nil || !bytes.Equal(raw, encoded) {
		return d, 0, ErrIntegrity
	}
	return d, manifestBytes, nil
}

func privateMetadata(path string, directory bool, max int) ([]byte, error) {
	f, err := openNoFollow(path, directory)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, ErrIO
	}
	st := info.Sys().(*syscall.Stat_t)
	mode := uint32(0600)
	if directory {
		mode = 0700
	}
	if st.Uid != uint32(os.Geteuid()) || st.Mode&07777 != mode ||
		(!directory && (!info.Mode().IsRegular() || st.Nlink != 1)) {
		return nil, ErrIntegrity
	}
	if err := noXattrs(path); err != nil {
		return nil, err
	}
	if directory {
		return nil, nil
	}
	if st.Size < 0 || st.Size > int64(max) {
		return nil, ErrCapacity
	}
	raw, err := io.ReadAll(io.LimitReader(f, int64(max)+1))
	if err != nil {
		return nil, ErrIO
	}
	after, err := f.Stat()
	if err != nil || len(raw) != int(st.Size) || metadata(after.Sys().(*syscall.Stat_t)) != metadata(st) {
		return nil, ErrIntegrity
	}
	return raw, nil
}

func boundedNames(ctx context.Context, path string, max int) ([]string, error) {
	f, err := openNoFollow(path, true)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var names []string
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		batch, err := f.Readdirnames(128)
		if len(batch) > max-len(names) {
			return nil, ErrCapacity
		}
		names = append(names, batch...)
		if errors.Is(err, io.EOF) {
			return names, nil
		}
		if err != nil {
			return nil, ErrIO
		}
	}
}

// OpenRecovering verifies a recognized store under flock, but grants no worker
// lifecycle proof. Every recovered record remains inspection-only.
func OpenRecovering(ctx context.Context, root string, limits Limits) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s, err := openStore(root, limits, false)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = s.lock.Close()
		}
	}()
	s.persistent = newPersistentState(root)
	s.persistent.recovered = true
	if _, err := privateMetadata(s.persistent.control, true, 0); err != nil {
		return nil, err
	}
	raw, err := privateMetadata(filepath.Join(s.persistent.control, "format.json"), false, 128)
	if err != nil || string(raw) != formatRecord {
		return nil, ErrIntegrity
	}
	names, err := boundedNames(ctx, s.persistent.control, 2*limits.Generations+1)
	if err != nil {
		return nil, err
	}
	type pending struct {
		primary, temp           *persistentRecord
		primaryBytes, tempBytes int
	}
	all := make(map[string]*pending)
	metadataBytes := int64(0)
	maxMetadata := int64(limits.ManifestBytes) + int64(recordOverhead)*int64(limits.Generations)
	for _, name := range names {
		if name == "format.json" {
			continue
		}
		temporary := strings.HasSuffix(name, ".json.tmp")
		suffix := ".json"
		if temporary {
			suffix += ".tmp"
		}
		id := strings.TrimSuffix(name, suffix)
		if !generationID(id) || name != id+suffix {
			return nil, ErrIntegrity
		}
		raw, err := privateMetadata(filepath.Join(s.persistent.control, name), false, limits.ManifestBytes+recordOverhead)
		if err != nil {
			return nil, err
		}
		// At most one bounded temporary record per generation is recognized.
		metadataBytes += int64(len(raw))
		if metadataBytes > 2*maxMetadata {
			return nil, ErrCapacity
		}
		d, size, err := decodeRecord(raw, id, limits)
		if err != nil {
			return nil, err
		}
		p := all[id]
		if p == nil {
			if len(all) >= limits.Generations {
				return nil, ErrCapacity
			}
			p = &pending{}
			all[id] = p
		}
		if temporary {
			p.temp, p.tempBytes = &d, size
		} else {
			p.primary, p.primaryBytes = &d, size
		}
	}
	rootNames, err := boundedNames(ctx, root, 2*limits.Generations+1)
	if err != nil {
		return nil, err
	}
	stages, generations := make(map[string]bool), make(map[string]bool)
	for _, name := range rootNames {
		if name == controlName {
			continue
		}
		prefix := "generation-"
		isStage := strings.HasPrefix(name, "staging-")
		if isStage {
			prefix = "staging-"
		}
		id := strings.TrimPrefix(name, prefix)
		if !generationID(id) || name != prefix+id || all[id] == nil {
			return nil, ErrIntegrity
		}
		info, err := os.Lstat(filepath.Join(root, name))
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, ErrIntegrity
		}
		if err := noXattrs(filepath.Join(root, name)); err != nil {
			return nil, err
		}
		if isStage {
			stages[id] = true
		} else {
			generations[id] = true
		}
	}
	for id, p := range all {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		d, size := p.primary, p.primaryBytes
		if d == nil {
			d, size = p.temp, p.tempBytes
		}
		if p.primary != nil && p.temp != nil {
			if !compatiblePending(*p.primary, *p.temp) {
				return nil, ErrIntegrity
			}
			if p.tempBytes > size {
				size = p.tempBytes
			}
		}
		if d.Reserved > limits.Bytes-s.bytes || size > limits.ManifestBytes-s.persistent.manifestBytes {
			return nil, ErrCapacity
		}
		if stages[id] && generations[id] {
			return nil, ErrIntegrity
		}
		switch d.Phase {
		case "staging":
			if generations[id] {
				return nil, ErrIntegrity
			}
		case "committed", "retired":
			if !generations[id] || stages[id] {
				return nil, ErrIntegrity
			}
			// Registration is deferred until every committed tree has verified.
			if err := verify(ctx, filepath.Join(root, "generation-"+id), d.Kind, d.Manifest, limits, d.Reserved); err != nil {
				return nil, err
			}
		case "publish_intent":
			if !stages[id] && !generations[id] {
				return nil, ErrIntegrity
			}
		case "delete_intent":
			// Partial deletion is retained, never walked or resumed here.
		}
		stage := d.Kind == "" || stages[id]
		if stage {
			s.stages++
			if s.stages > limits.Staging {
				return nil, ErrCapacity
			}
		}
		identity := Identity{Generation: id, Kind: d.Kind, Owner: d.Owner, Digest: d.Digest}
		s.bytes += d.Reserved
		s.persistent.manifestBytes += size
		s.persistent.recovery = append(s.persistent.recovery, RecoveryEntry{
			Identity: identity, Phase: d.Phase, ReservedBytes: d.Reserved, Manifest: d.Manifest,
			StagingPresent: stages[id], GenerationPresent: generations[id], PendingMetadata: p.temp != nil,
		})
	}
	for _, e := range s.persistent.recovery {
		s.records[e.Identity.Generation] = &record{identity: e.Identity, reserved: e.ReservedBytes, phase: retired}
	}
	sort.Slice(s.persistent.recovery, func(i, j int) bool {
		return s.persistent.recovery[i].Identity.Generation < s.persistent.recovery[j].Identity.Generation
	})
	ok = true
	return s, nil
}

func compatiblePending(old, next persistentRecord) bool {
	if old.Generation != next.Generation || old.Reserved != next.Reserved {
		return false
	}
	if old.Kind != "" {
		a, b := old, next
		a.Phase, b.Phase = "", ""
		ar, _ := json.Marshal(a)
		br, _ := json.Marshal(b)
		if !bytes.Equal(ar, br) {
			return false
		}
	}
	switch old.Phase {
	case "staging":
		return next.Phase == "publish_intent" || next.Phase == "delete_intent"
	case "publish_intent":
		return next.Phase == "committed" || next.Phase == "delete_intent"
	case "committed":
		return next.Phase == "retired" || next.Phase == "delete_intent"
	case "retired":
		return next.Phase == "retired" || next.Phase == "delete_intent"
	case "delete_intent":
		return next.Phase == "delete_intent"
	}
	return false
}

// SnapshotRecovery returns copies only. A released process lock is not proof
// that workers from its previous lifetime have stopped using these trees.
func (s *Store) SnapshotRecovery() []RecoveryEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.persistent == nil || !s.persistent.recovered {
		return nil
	}
	out := append([]RecoveryEntry(nil), s.persistent.recovery...)
	for i := range out {
		out[i].Manifest.Entries = append([]Entry{}, out[i].Manifest.Entries...)
	}
	return out
}
