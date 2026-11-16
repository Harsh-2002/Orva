//go:build linux

package workloadfence

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

// Budget pins this process's exact current non-root cgroup before a trusted
// boot owner moves the daemon into a leaf. It is not an IAM or recovery proof.
type Budget struct{ s *budgetState }

type budgetState struct {
	mu       sync.Mutex
	pid      int
	boot     string
	path     string
	mountID  uint64
	objects  [4]*os.File // mount, budget, cgroup namespace, mount namespace
	ids      [4]objectID
	refs     int
	closed   bool
	disposed bool
}

// CaptureBudget accepts no path: only the exact current non-root membership,
// verified cgroup-v2 mount and current kernel namespaces can mint this handle.
func CaptureBudget() (*Budget, error) {
	path, err := currentBudgetPath()
	if err != nil {
		return nil, err
	}
	b := &budgetState{pid: os.Getpid(), path: path}
	ok := false
	defer func() {
		if !ok {
			b.dispose()
		}
	}()
	b.boot, err = bootID()
	if err != nil {
		return nil, err
	}
	b.objects[0], err = openDir("/sys/fs/cgroup")
	if err != nil {
		return nil, err
	}
	var fs syscall.Statfs_t
	if err := syscall.Fstatfs(int(b.objects[0].Fd()), &fs); err != nil || fs.Type != cgroup2Magic {
		return nil, ErrInvalid
	}
	b.objects[1], err = openCgroup(path)
	if err != nil {
		return nil, err
	}
	for i, name := range []string{"/proc/self/ns/cgroup", "/proc/self/ns/mnt"} {
		b.objects[i+2], err = os.Open(name)
		if err != nil {
			return nil, err
		}
	}
	for i, f := range b.objects {
		b.ids[i], err = identity(f)
		if err != nil {
			return nil, err
		}
	}
	b.mountID, err = descriptorMountID(b.objects[0])
	if err != nil {
		return nil, err
	}
	if err := b.check(""); err != nil {
		return nil, err
	}
	ok = true
	return &Budget{s: b}, nil
}

// CreateUnderBudget requires a package-minted live capture and a strict child of
// its exact budget. Current membership must still be inside that pinned budget.
// The caller, not this package, prepares delegation and moves only its daemon.
func CreateUnderBudget(budget *Budget, root, parent string) (*Domain, error) {
	if budget == nil || budget.s == nil {
		return nil, ErrInvalid
	}
	return create(root, parent, budget.s)
}

// Close prevents new domains through every copy. Existing domains retain pins
// and their independent entry/fence lifetime; ErrBusy is not a process fence.
func (b *Budget) Close() error {
	if b == nil || b.s == nil {
		return ErrInvalid
	}
	s := b.s
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.refs != 0 {
		return ErrBusy
	}
	s.dispose()
	return nil
}

func (b *budgetState) retain(parent string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.disposed {
		return ErrClosed
	}
	if b.refs >= 1024 {
		return ErrBusy
	}
	if err := b.check(parent); err != nil {
		return err
	}
	b.refs++
	return nil
}

func (b *budgetState) release() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refs--
	if b.refs == 0 && b.closed {
		b.dispose()
	}
}

func (b *budgetState) verify(parent string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.disposed {
		return ErrClosed
	}
	return b.check(parent)
}

func (b *budgetState) dispose() {
	if b.disposed {
		return
	}
	b.disposed = true
	for _, f := range b.objects {
		if f != nil {
			f.Close()
		}
	}
}

func (b *budgetState) check(parent string) error {
	if b.pid != os.Getpid() || b.disposed {
		return ErrIntegrity
	}
	boot, err := bootID()
	if err != nil {
		return err
	}
	if boot != b.boot {
		return ErrIntegrity
	}
	current, err := currentBudgetPath()
	if err != nil {
		return err
	}
	if current != b.path && !strictChild(b.path, current) {
		return ErrIntegrity
	}
	if parent != "" && !strictChild(b.path, parent) {
		return ErrInvalid
	}
	if parent != "" {
		f, err := openCgroup(parent)
		if err != nil {
			return err
		}
		mount, err := descriptorMountID(f)
		f.Close()
		if err != nil {
			return err
		}
		if mount != b.mountID {
			return ErrIntegrity
		}
	}
	paths := [4]string{"/sys/fs/cgroup", b.path, "/proc/self/ns/cgroup", "/proc/self/ns/mnt"}
	for i, path := range paths {
		var f *os.File
		if i == 0 {
			// The mount root need not be delegated to a non-root owner.
			f, err = openDir(path)
		} else if i == 1 {
			f, err = openCgroup(path)
		} else {
			f, err = os.Open(path)
		}
		if err != nil {
			return err
		}
		actual, statErr := identity(f)
		mount := uint64(0)
		if i < 2 && statErr == nil {
			mount, statErr = descriptorMountID(f)
		}
		f.Close()
		if statErr != nil {
			return statErr
		}
		pinned, err := identity(b.objects[i])
		if err != nil {
			return err
		}
		if actual != b.ids[i] || actual != pinned || (i < 2 && mount != b.mountID) {
			return ErrIntegrity
		}
	}
	return nil
}

func strictChild(parent, path string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return false
	}
	rel, err := filepath.Rel(parent, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, "../")
}

func currentBudgetPath() (string, error) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", err
	}
	var path string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) == 3 && parts[0] == "0" && parts[1] == "" {
			if path != "" || parts[2] == "/" || !filepath.IsAbs(parts[2]) || filepath.Clean(parts[2]) != parts[2] {
				return "", ErrInvalid
			}
			path = "/sys/fs/cgroup" + parts[2]
		}
	}
	if path == "" {
		return "", ErrInvalid
	}
	return path, nil
}

func descriptorMountID(f *os.File) (uint64, error) {
	info, err := os.Open("/proc/self/fdinfo/" + strconv.FormatUint(uint64(f.Fd()), 10))
	if err != nil {
		return 0, err
	}
	defer info.Close()
	data, err := io.ReadAll(io.LimitReader(info, 4097))
	if err != nil || len(data) > 4096 {
		return 0, ErrInvalid
	}
	var id uint64
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "mnt_id:" {
			if id != 0 {
				return 0, ErrInvalid
			}
			id, err = strconv.ParseUint(fields[1], 10, 64)
			if err != nil || id == 0 {
				return 0, ErrInvalid
			}
		}
	}
	if id == 0 {
		return 0, errors.New("workload fence: kernel mount identity unavailable")
	}
	return id, nil
}
