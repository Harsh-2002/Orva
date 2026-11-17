//go:build linux

package workloadfence

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// JailParent is an owned live-epoch binding, not durable approval. Pass it back
// to that Domain's Start for gate-held revalidation; its path alone grants none.
type JailParent struct {
	s           *state
	id          objectID
	path        string
	controllers uint8
}

// Path is only for trusted host argument construction. It is not a descriptor,
// a grant to enter cgroups directly, or authority to start outside Domain.Start.
func (p *JailParent) Path() string {
	if p == nil || p.s == nil {
		return ""
	}
	return p.path
}

// PrepareJails prepares only this live epoch's own jails subtree. Requested
// controllers are explicit cpu/memory/pids names; no names means lifetime only.
// Missing ancestor delegation fails rather than enabling anything outside it.
func (d *Domain) PrepareJails(ctx context.Context, controllers ...string) (*JailParent, error) {
	if d == nil || d.s == nil || ctx == nil {
		return nil, ErrInvalid
	}
	requested, err := controllerMask(controllers)
	if err != nil {
		return nil, err
	}
	s := d.s
	unlock, err := s.enterLineage(ctx, true)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if s.disposed || s.closed.Load() || s.recovering {
		return nil, ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.verifyLineage(); err != nil {
		return nil, err
	}
	id, err := s.verifyJails()
	if err != nil {
		s.closed.Store(true)
		return nil, err
	}
	want := s.controllers | requested
	if want != s.controllers {
		available, err := readControllers(s.path, "cgroup.controllers")
		if err != nil {
			return nil, err
		}
		if available&want != want {
			return nil, ErrUnsupported
		}
		// A partial or unverifiable kernel change poisons entry, never
		// silently downgrades an already-issued binding.
		if err := enableJailControllers(s.path, want); err != nil {
			s.closed.Store(true)
			return nil, err
		}
		if err := enableJailControllers(filepath.Join(s.path, "jails"), want); err != nil {
			s.closed.Store(true)
			return nil, err
		}
		s.controllers = want
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if s.closed.Load() {
		return nil, ErrClosed
	}
	if _, err := s.verifyJails(); err != nil {
		s.closed.Store(true)
		return nil, err
	}
	return &JailParent{s: s, id: id, path: filepath.Join(s.path, "jails"), controllers: requested}, nil
}

func controllerMask(names []string) (uint8, error) {
	if len(names) > 3 {
		return 0, ErrInvalid
	}
	var mask uint8
	for _, name := range names {
		bit := controllerBit(name)
		if bit == 0 || mask&bit != 0 {
			return 0, ErrInvalid
		}
		mask |= bit
	}
	return mask, nil
}

func controllerBit(name string) uint8 {
	switch name {
	case "cpu":
		return 1
	case "memory":
		return 2
	case "pids":
		return 4
	}
	return 0
}

func readControllers(path, file string) (uint8, error) {
	b, err := os.ReadFile(filepath.Join(path, file))
	if err != nil {
		return 0, err
	}
	var mask uint8
	for _, name := range strings.Fields(string(b)) {
		bit := controllerBit(name)
		// Other available controllers are not requested or modified.
		if bit != 0 {
			if mask&bit != 0 {
				return 0, ErrInvalid
			}
			mask |= bit
		} else if file == "cgroup.subtree_control" {
			// Available controllers are a superset; enabled controllers
			// outside package ownership are unexpected.
			return 0, ErrIntegrity
		}
	}
	return mask, nil
}

func enableJailControllers(path string, mask uint8) error {
	var names []string
	for _, name := range []string{"cpu", "memory", "pids"} {
		if mask&controllerBit(name) != 0 {
			names = append(names, "+"+name)
		}
	}
	if err := writeControl(path, "cgroup.subtree_control", strings.Join(names, " ")); err != nil {
		return err
	}
	actual, err := readControllers(path, "cgroup.subtree_control")
	if err != nil {
		return err
	}
	if actual != mask {
		return ErrIntegrity
	}
	return nil
}

func (s *state) checkJailParent(p *JailParent) error {
	if p == nil || p.s != s || p.path != filepath.Join(s.path, "jails") || p.controllers&s.controllers != p.controllers {
		return ErrInvalid
	}
	id, err := s.verifyJails()
	if err != nil {
		s.closed.Store(true)
		return err
	}
	if id != p.id {
		return ErrIntegrity
	}
	return nil
}

func (s *state) verifyJails() (objectID, error) {
	if s.jails == nil {
		return objectID{}, ErrInvalid
	}
	path := filepath.Join(s.path, "jails")
	current, err := openCgroup(path)
	if err != nil {
		return objectID{}, err
	}
	defer current.Close()
	id, err := identity(current)
	if err != nil {
		return objectID{}, err
	}
	pinned, err := identity(s.jails)
	if err != nil {
		return objectID{}, err
	}
	if pinned != id {
		return objectID{}, ErrIntegrity
	}
	for _, parent := range []string{s.path, path} {
		kind, err := os.ReadFile(filepath.Join(parent, "cgroup.type"))
		if err != nil {
			return objectID{}, err
		}
		if strings.TrimSpace(string(kind)) != "domain" {
			return objectID{}, ErrIntegrity
		}
		actual, err := readControllers(parent, "cgroup.subtree_control")
		if err != nil {
			return objectID{}, err
		}
		if actual != s.controllers {
			return objectID{}, ErrIntegrity
		}
	}
	if err := checkChildNames(s.path, false, nil); err != nil {
		return objectID{}, err
	}
	if err := checkChildNames(path, true, s.children); err != nil {
		return objectID{}, err
	}
	return id, nil
}

func checkChildNames(path string, jails bool, children map[*state]struct{}) error {
	dir, err := openDir(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	// Cgroup files are kernel-owned, but child enumeration still needs a bound.
	for count := 0; ; {
		entries, err := dir.ReadDir(128)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		count += len(entries)
		if count > 2048 {
			return ErrInvalid
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			known := false
			for child := range children {
				if child.path == filepath.Join(path, name) && child.groupCreated && child.group != nil {
					actual, err := openCgroup(child.path)
					if err != nil {
						return err
					}
					id, err := identity(actual)
					actual.Close()
					if err != nil || id != child.rec.Group {
						return ErrIntegrity
					}
					known = true
					break
				}
			}
			if known {
				continue
			}
			if !jails {
				if name != "jails" && name != "supervisors" {
					return ErrIntegrity
				}
				continue
			}
			number := strings.TrimPrefix(name, "NSJAIL.")
			pid, parseErr := strconv.ParseUint(number, 10, 32)
			if number == name || parseErr != nil || pid == 0 || strconv.FormatUint(pid, 10) != number {
				return ErrIntegrity
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
	}
}
