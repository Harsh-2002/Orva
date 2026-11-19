//go:build linux

package artifactfs

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

// OwnerHostConfig opts into host binding at creation. The witness is
// pre-provisioned and never copied, backed up, restored, or rotated.
type OwnerHostConfig struct {
	WitnessPath string
	BackupRoots []string // Complete, explicit list; use a non-nil empty list if none.
}

type ownerWitness struct {
	file  *os.File
	id    ownerObject
	token [32]byte
}

type ownerHostBinding struct {
	Identity ownerObject `json:"identity"`
	MAC      string      `json:"mac"`
}

// CheckProvisionedWitness checks an offline operation's pre-existing witness
// without creating stores or changing the witness.
func CheckProvisionedWitness(host *OwnerHostConfig, dataDir string) error {
	w, err := openOwnerWitness(OwnerConfig{Host: host, MetadataRoot: dataDir, CodeRoot: dataDir, RuntimeRoot: dataDir})
	if err != nil {
		return err
	}
	defer clear(w.token[:])
	return w.file.Close()
}

func ownerHostPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path &&
		len(path) <= 4096 && utf8.ValidString(path) && !strings.ContainsRune(path, 0)
}

func validateWitnessPlacement(path string, roots []string) error {
	if !ownerHostPath(path) || roots == nil {
		return ErrInvalid
	}
	parent := filepath.Dir(path)
	ancestors := make(map[ownerObject]bool)
	for dir := parent; ; dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() {
			return ErrIntegrity
		}
		st := info.Sys().(*syscall.Stat_t)
		ancestors[ownerObject{uint64(st.Dev), st.Ino}] = true
		if dir == "/" {
			break
		}
	}
	for _, root := range roots {
		if !ownerHostPath(root) || root == path {
			return ErrInvalid
		}
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil || resolved != root {
			return ErrInvalid
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return ErrInvalid
		}
		st := info.Sys().(*syscall.Stat_t)
		if ancestors[ownerObject{uint64(st.Dev), st.Ino}] {
			return ErrInvalid
		}
	}
	return nil
}

// ProvisionOwnerWitness creates a private, non-replaceable witness. Parent
// directories above the dedicated witness directory must already exist.
func ProvisionOwnerWitness(path string, roots []string) error {
	if !ownerHostPath(path) || roots == nil || len(roots) > 64 {
		return ErrInvalid
	}
	parent := filepath.Dir(path)
	// Check the existing ancestry before creating anything.
	if err := validateWitnessPlacement(parent, roots); err != nil {
		return err
	}
	created := false
	if err := os.Mkdir(parent, 0700); err == nil {
		created = true
	} else if !errors.Is(err, os.ErrExist) {
		return err
	}
	if err := validateWitnessPlacement(path, roots); err != nil {
		return err
	}
	dir, err := openNoFollow(parent, true)
	if err != nil {
		return err
	}
	defer dir.Close()
	if created {
		if err := dir.Chmod(0700); err != nil {
			return err
		}
	}
	if _, err := readPrivateMetadata(dir, true, 0); err != nil {
		return err
	}
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		return err
	}
	defer clear(key[:])
	var raw [65]byte
	hex.Encode(raw[:64], key[:])
	raw[64] = '\n'
	defer clear(raw[:])
	name := filepath.Base(path)
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return &os.PathError{Op: "create witness", Path: path, Err: err}
	}
	f := os.NewFile(uintptr(fd), name)
	err = f.Chmod(0600)
	if err == nil {
		_, err = f.Write(raw[:])
	}
	if err == nil {
		err = f.Sync()
	}
	if err := errors.Join(err, f.Close()); err != nil {
		return err
	}
	if err := dir.Sync(); err != nil {
		return err
	}
	if created {
		up, err := openNoFollow(filepath.Dir(parent), true)
		if err != nil {
			return err
		}
		return errors.Join(up.Sync(), up.Close())
	}
	return nil
}

func openOwnerWitness(cfg OwnerConfig) (*ownerWitness, error) {
	host := cfg.Host
	if host == nil || host.BackupRoots == nil || len(host.BackupRoots) > 64 {
		return nil, ErrInvalid
	}
	path := host.WitnessPath
	parent := filepath.Dir(path)
	roots := append([]string{cfg.MetadataRoot, cfg.CodeRoot, cfg.RuntimeRoot}, host.BackupRoots...)
	if err := validateWitnessPlacement(path, roots); err != nil {
		return nil, err
	}
	if _, err := privateMetadata(parent, true, 0); err != nil {
		return nil, err
	}
	f, err := openNoFollow(path, false)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			_ = f.Close()
		}
	}()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, ErrIO
	}
	id, err := ownerIdentity(f)
	if err != nil {
		return nil, err
	}
	w := &ownerWitness{file: f, id: id}
	w.token, err = w.key()
	if err != nil {
		return nil, err
	}
	ok = true
	return w, nil
}

func (w *ownerWitness) key() ([32]byte, error) {
	var key [32]byte
	if _, err := privateMetadata(filepath.Dir(w.file.Name()), true, 0); err != nil {
		return key, err
	}
	if _, err := w.file.Seek(0, 0); err != nil {
		return key, ErrIO
	}
	raw, err := readPrivateMetadata(w.file, false, 65)
	if err != nil {
		return key, err
	}
	if len(raw) != 65 || raw[64] != '\n' || !ownerHex(string(raw[:64]), 64) {
		return key, ErrIntegrity
	}
	f, err := openNoFollow(w.file.Name(), false)
	if err != nil {
		return key, err
	}
	defer f.Close()
	id, err := ownerIdentity(f)
	if err != nil || id != w.id {
		return key, ErrIntegrity
	}
	if _, err := hex.Decode(key[:], raw[:64]); err != nil || key == [32]byte{} {
		return key, ErrIntegrity
	}
	return key, nil
}

func (w *ownerWitness) sign(disk *ownerRecord) error {
	key, err := w.key()
	if err != nil {
		return err
	}
	if !hmac.Equal(key[:], w.token[:]) {
		return ErrIntegrity
	}
	copy := *disk
	copy.Host = &ownerHostBinding{Identity: w.id}
	raw, err := json.Marshal(copy)
	if err != nil {
		return ErrIntegrity
	}
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write([]byte("orva:artifactfs:owner:v2\x00"))
	_, _ = mac.Write(raw)
	copy.Host.MAC = hex.EncodeToString(mac.Sum(nil))
	disk.Host = copy.Host
	return nil
}

func (w *ownerWitness) verify(disk ownerRecord) error {
	if disk.Host == nil || disk.Host.Identity != w.id {
		return ErrIntegrity
	}
	want := disk.Host.MAC
	if err := w.sign(&disk); err != nil {
		return err
	}
	if !hmac.Equal([]byte(want), []byte(disk.Host.MAC)) {
		return ErrIntegrity
	}
	return nil
}

func lastOwnerBootRun(epochs []ownerEpochRecord) (int, error) {
	var last string
	start := 0
	seen := make(map[string]bool)
	if len(epochs) == 0 || len(epochs) > 64 {
		return 0, ErrIntegrity
	}
	for i, e := range epochs {
		boot := e.Reference.Boot
		if boot == last && i != 0 {
			continue
		}
		if !ownerBoot(boot) || boot == "00000000-0000-0000-0000-000000000000" || seen[boot] {
			return 0, ErrIntegrity
		}
		seen[boot], last, start = true, boot, i
	}
	return start, nil
}

func ownerNeedsColdRecovery(epochs []ownerEpochRecord, boot string) (bool, error) {
	start, err := lastOwnerBootRun(epochs)
	if err != nil || !ownerBoot(boot) || boot == "00000000-0000-0000-0000-000000000000" {
		return false, ErrIntegrity
	}
	if epochs[start].Reference.Boot == boot {
		return false, nil
	}
	for _, epoch := range epochs[:start] {
		if epoch.Reference.Boot == boot {
			return false, ErrIntegrity
		}
	}
	return true, nil
}
