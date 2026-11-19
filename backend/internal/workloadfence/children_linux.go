//go:build linux

package workloadfence

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxChildDepth  = 4
	maxDescendants = 1024
)

type lineage struct {
	Instance string   `json:"instance"`
	Root     objectID `json:"root"`
	Parent   string   `json:"parent"`
	Metadata objectID `json:"metadata"`
	Group    objectID `json:"group"`
	Depth    int      `json:"depth"`
}

func (l lineage) valid(epoch string) bool {
	for _, id := range []string{l.Instance, l.Parent} {
		if len(id) != 32 || strings.ToLower(id) != id || id == epoch {
			return false
		}
		if _, err := hex.DecodeString(id); err != nil {
			return false
		}
	}
	return l.Depth > 0 && l.Depth <= maxChildDepth &&
		l.Root.Inode != 0 && l.Metadata.Inode != 0 && l.Group.Inode != 0
}

func (s *state) instance() *state {
	for s.parentState != nil {
		s = s.parentState
	}
	return s
}

func (s *state) childLineage() lineage {
	root := s.instance()
	return lineage{root.rec.Epoch, root.rec.Root, s.rec.Epoch, s.rec.Root, s.rec.Group, s.depth + 1}
}

// Gates are always acquired root-to-leaf, including cleanup; no operation
// holding a child gate may independently enter an ancestor.
func (s *state) enterLineage(ctx context.Context, entry bool) (func(), error) {
	var chain [maxChildDepth + 1]*state
	n := 0
	for p := s; p != nil; p = p.parentState {
		if n == len(chain) {
			return nil, ErrInvalid
		}
		chain[n], n = p, n+1
	}
	held := n
	unlock := func() {
		for i := held; i < n; i++ {
			chain[i].leave()
		}
	}
	for i := n - 1; i >= 0; i-- {
		if err := chain[i].enter(ctx); err != nil {
			unlock()
			return nil, err
		}
		held = i
		p := chain[i]
		if entry && (p.closed.Load() || p.disposed || p.recovering || p.deleting) {
			unlock()
			return nil, ErrClosed
		}
	}
	if err := ctx.Err(); err != nil {
		unlock()
		return nil, err
	}
	return unlock, nil
}

func (s *state) verifyLineage() error {
	for p := s; p != nil; p = p.parentState {
		if p.disposed || p.reclaimed {
			return ErrClosed
		}
		if p.parentState != nil {
			if _, ok := p.parentState.children[p]; !ok {
				return ErrIntegrity
			}
		}
		if err := p.verifyIdentity(); err != nil {
			return err
		}
		if err := p.verifyRecord(); err != nil {
			return err
		}
	}
	return nil
}

// NewChild creates an exclusive epoch below this live domain's owned jails.
// All three resource controllers are required. A nonnil result with an error
// is quarantined ownership: retain it and its metadata; never discard it.
func (d *Domain) NewChild(ctx context.Context, metadataRoot string) (*Domain, error) {
	if d == nil || d.s == nil || ctx == nil {
		return nil, ErrInvalid
	}
	parent, err := d.PrepareJails(ctx, "cpu", "memory", "pids")
	if err != nil {
		return nil, err
	}
	s := d.s
	unlock, err := s.enterLineage(ctx, true)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := s.verifyLineage(); err != nil {
		return nil, err
	}
	if err := s.checkJailParent(parent); err != nil {
		return nil, err
	}
	if s.depth >= maxChildDepth || s.instance().descendants >= maxDescendants {
		return nil, ErrCapacity
	}
	child, err := openMetadata(metadataRoot, parent.path)
	if err != nil {
		return nil, err
	}
	child.parentState, child.depth = s, s.depth+1
	if s.children == nil {
		s.children = make(map[*state]struct{})
	}
	s.children[child] = struct{}{}
	s.instance().descendants++
	return initialize(child)
}

// EpochReference is association data only, never a start capability or proof.
type EpochReference struct {
	Version                       int
	Epoch, Boot                   string
	MetadataDevice, MetadataInode uint64
	GroupDevice, GroupInode       uint64
	InstanceEpoch, ParentEpoch    string
	Depth                         int
	ParentDevice                  uint64 `json:",omitempty"`
	ParentInode                   uint64 `json:",omitempty"`
}

// Matches accepts older references without a recorded parent identity for
// existing verification paths. Those references cannot prove removal.
func (r EpochReference) Matches(expected EpochReference) bool {
	if expected.ParentDevice == 0 && expected.ParentInode == 0 {
		r.ParentDevice, r.ParentInode = 0, 0
	}
	return r == expected
}

func (d *Domain) Reference(ctx context.Context) (EpochReference, error) {
	if d == nil || d.s == nil || ctx == nil {
		return EpochReference{}, ErrInvalid
	}
	s := d.s
	unlock, err := s.enterLineage(ctx, false)
	if err != nil {
		return EpochReference{}, err
	}
	defer unlock()
	if err := s.verifyLineage(); err != nil {
		return EpochReference{}, err
	}
	r := s.rec
	if r.Version == 1 {
		return rootEpochReference(r), nil
	}
	return EpochReference{Version: r.Version, Epoch: r.Epoch, Boot: r.Boot,
		MetadataDevice: r.Root.Device, MetadataInode: r.Root.Inode,
		GroupDevice: r.Group.Device, GroupInode: r.Group.Inode,
		InstanceEpoch: s.instance().rec.Epoch, ParentEpoch: r.Lineage.Parent, Depth: s.depth}, nil
}

// ReclaimChild removes only a direct, still-owned child's empty cgroup tree.
// It never removes durable metadata or grants artifact deletion/recovery.
func (d *Domain) ReclaimChild(ctx context.Context, child *Domain, proof *Proof) error {
	if d == nil || d.s == nil || child == nil || child.s == nil || ctx == nil ||
		child.s.parentState != d.s || proof == nil || proof.s != child.s {
		return ErrInvalid
	}
	s := child.s
	unlock, err := s.enterLineage(ctx, false)
	if err != nil {
		return err
	}
	defer unlock()
	if s.reclaimed {
		return nil
	}
	if len(s.children) != 0 {
		return ErrBusy
	}
	if !s.closed.Load() || !s.rec.Closed || proof.rec != s.rec {
		return ErrClosed
	}
	if err := s.verifyLineage(); err != nil {
		return err
	}
	empty, err := unpopulated(s.path)
	if err != nil {
		return err
	}
	if !empty {
		return ErrIntegrity
	}
	s.deleting = true
	count := 0
	if err := removeEmptyGroups(ctx, s.path, 0, &count, 0); err != nil {
		return err
	}
	s.reclaimed = true
	s.dispose()
	return nil
}

func removeEmptyGroups(ctx context.Context, path string, depth int, count *int, mount uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if depth > 16 || *count >= 2048 {
		return ErrCapacity
	}
	(*count)++
	dir, err := openCgroup(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	if mount != 0 {
		id, err := descriptorMountID(dir)
		if err != nil || id != mount {
			return ErrIntegrity
		}
	}
	for total := 0; ; {
		entries, err := dir.ReadDir(128)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		total += len(entries)
		if total > 2048 {
			return ErrCapacity
		}
		for _, entry := range entries {
			if entry.IsDir() {
				if err := removeEmptyGroups(ctx, filepath.Join(path, entry.Name()), depth+1, count, mount); err != nil {
					return err
				}
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}
	return os.Remove(path)
}
