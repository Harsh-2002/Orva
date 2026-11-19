//go:build linux

package artifactfs

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/Harsh-2002/Orva/backend/internal/workloadfence"
)

const ownerRetireFile = "owner.retire"

type ownerRetirement struct {
	Version   int              `json:"version"`
	Before    string           `json:"before"`
	After     string           `json:"after"`
	Epoch     ownerEpochRecord `json:"epoch"`
	EpochHash string           `json:"epoch_hash"`
	MAC       string           `json:"mac"`
}

type ownerRetirementReplay struct {
	marker    ownerRetirement
	successor ownerRecord
	promote   bool
	staged    bool
}

func (w *ownerWitness) retirementMAC(p ownerRetirement) (string, error) {
	key, err := w.key()
	if err != nil || !hmac.Equal(key[:], w.token[:]) {
		return "", ErrIntegrity
	}
	p.MAC = ""
	raw, _ := json.Marshal(p)
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write([]byte("orva:artifactfs:owner-retire:v1\x00"))
	_, _ = mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func (s *ownerState) readOwnerRetirement(ctx context.Context) (*ownerRetirementReplay, error) {
	root := s.cfg.MetadataRoot
	path := filepath.Join(root, ownerRetireFile)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, ErrIO
	}
	raw, err := privateMetadata(path, false, 2048)
	if err != nil {
		return nil, err
	}
	var p ownerRetirement
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, ErrIntegrity
	}
	canonical, _ := json.Marshal(p)
	if !bytes.Equal(raw, canonical) || p.Version != 1 || !ownerHex(p.Before, 64) ||
		!ownerHex(p.After, 64) || !ownerHex(p.EpochHash, 64) || !ownerHex(p.MAC, 64) || p.Before == p.After {
		return nil, ErrIntegrity
	}
	mac, err := s.witness.retirementMAC(p)
	if err != nil || !hmac.Equal([]byte(p.MAC), []byte(mac)) {
		return nil, ErrIntegrity
	}

	r := &ownerRetirementReplay{marker: p, successor: s.disk}
	names := []string{"owner.json", "epochs", ownerRetireFile}
	retired := ""
	switch ownerRecordDigest(s.disk) {
	case p.Before:
		if len(s.disk.Epochs) != s.disk.MaxEpochs || s.disk.Epochs[0] != p.Epoch {
			return nil, ErrIntegrity
		}
		r.promote = true
		r.successor.Epochs = s.disk.Epochs[1:]
		if err := s.witness.sign(&r.successor); err != nil || ownerRecordDigest(r.successor) != p.After {
			return nil, ErrIntegrity
		}
		tmp := filepath.Join(root, "owner.json.tmp")
		if _, err := os.Lstat(tmp); err == nil {
			staged, err := readOwnerRecord(tmp)
			if err != nil || ownerRecordDigest(staged) != p.After {
				return nil, ErrIntegrity
			}
			names, r.staged = append(names, "owner.json.tmp"), true
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, ErrIO
		}
	case p.After:
		if len(s.disk.Epochs) != s.disk.MaxEpochs-1 {
			return nil, ErrIntegrity
		}
		before := s.disk
		before.Epochs = append([]ownerEpochRecord{p.Epoch}, s.disk.Epochs...)
		if err := validateOwnerEpochs(before.Epochs); err != nil {
			return nil, err
		}
		if _, err := lastOwnerBootRun(before.Epochs); err != nil {
			return nil, err
		}
		if err := s.witness.sign(&before); err != nil || ownerRecordDigest(before) != p.Before {
			return nil, ErrIntegrity
		}
		retired = p.Epoch.ID
	default:
		return nil, ErrIntegrity
	}
	if err := s.validateOwnerLayoutWithRetired(ctx, retired, names...); err != nil {
		return nil, err
	}
	if err := s.withRetiredMetadata(ctx, p, false, func(_ *os.File, hasRecord bool) error {
		if r.promote && !hasRecord {
			return ErrIntegrity
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return r, nil
}

func (s *ownerState) retireOwnerEpoch(ctx context.Context, r *ownerRetirementReplay, old []*workloadfence.Domain,
	proofs []*workloadfence.Proof, prior []ownerEpochRecord) (bool, error) {
	if err := s.checkRecoveryBindings(ctx, old, proofs, prior); err != nil {
		return false, err
	}
	if r == nil {
		if len(s.disk.Epochs) != s.disk.MaxEpochs || s.witness == nil {
			return false, ErrIntegrity
		}
		p := ownerRetirement{Version: 1, Before: ownerRecordDigest(s.disk), Epoch: s.disk.Epochs[0]}
		raw, err := privateMetadata(filepath.Join(s.epochRoot(p.Epoch.ID), "epoch.json"), false, 4096)
		if err != nil {
			return false, err
		}
		hash := sha256.Sum256(raw)
		p.EpochHash = hex.EncodeToString(hash[:])
		after := s.disk
		after.Epochs = after.Epochs[1:]
		if err := s.witness.sign(&after); err != nil {
			return false, err
		}
		p.After = ownerRecordDigest(after)
		if p.MAC, err = s.witness.retirementMAC(p); err != nil {
			return false, err
		}
		raw, _ = json.Marshal(p)
		if err := s.writeFile(ownerRetireFile, raw); err != nil {
			return false, err
		}
		r = &ownerRetirementReplay{marker: p, successor: after, promote: true}
	} else if err := s.syncOwnerFile(ownerRetireFile); err != nil {
		return false, err
	}
	if err := s.syncDir(s.cfg.MetadataRoot); err != nil {
		return false, err
	}
	if r.promote {
		if r.staged {
			if err := s.syncOwnerFile("owner.json.tmp"); err != nil {
				return false, err
			}
		} else if err := s.stageOwnerRecord(r.successor); err != nil {
			return false, err
		}
		if err := s.promoteOwnerRecord(); err != nil {
			return false, err
		}
		s.disk = r.successor
	}
	disk, err := readOwnerRecord(filepath.Join(s.cfg.MetadataRoot, "owner.json"))
	if err != nil || ownerRecordDigest(disk) != r.marker.After {
		return false, ErrIntegrity
	}
	committed, err := s.readOwnerRetirement(ctx)
	if err != nil {
		return false, err
	}
	if committed == nil || committed.promote || committed.marker != r.marker {
		return false, ErrIntegrity
	}
	held := r.promote && len(prior) == 0
	if err := s.cleanRetiredEpoch(ctx, r.marker, held); err != nil {
		return false, err
	}
	if held {
		if err := old[0].Close(); err != nil {
			return false, err
		}
	}
	return r.promote, s.syncDir(s.cfg.MetadataRoot)
}

func (s *ownerState) syncOwnerFile(name string) error {
	f, err := openNoFollow(filepath.Join(s.cfg.MetadataRoot, name), false)
	if err != nil {
		return ErrIO
	}
	defer f.Close()
	if err := s.ops.sync(f); err != nil {
		return ErrIO
	}
	return nil
}

func (s *ownerState) withRetiredMetadata(ctx context.Context, p ownerRetirement, lock bool,
	f func(*os.File, bool) error) error {
	root := s.epochRoot(p.Epoch.ID)
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return f(nil, false)
	} else if err != nil {
		return ErrIO
	}
	dir, err := openNoFollow(root, true)
	if err != nil {
		return err
	}
	defer dir.Close()
	if _, err := readPrivateMetadata(dir, true, 0); err != nil {
		return err
	}
	if lock {
		if err := syscall.Flock(int(dir.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			return ErrBusy
		}
	}
	ref := p.Epoch.Reference
	id, err := ownerIdentity(dir)
	if err != nil || id != (ownerObject{ref.MetadataDevice, ref.MetadataInode}) {
		return ErrIntegrity
	}
	names, err := boundedNames(ctx, root, 1)
	if err != nil {
		return err
	}
	if len(names) == 0 {
		return f(dir, false)
	}
	if names[0] != "epoch.json" {
		return ErrIntegrity
	}
	raw, err := privateMetadata(filepath.Join(root, "epoch.json"), false, 4096)
	if err != nil {
		return err
	}
	hash := sha256.Sum256(raw)
	if hex.EncodeToString(hash[:]) != p.EpochHash {
		return ErrIntegrity
	}
	return f(dir, true)
}

func (s *ownerState) cleanRetiredEpoch(ctx context.Context, p ownerRetirement, held bool) error {
	root := s.epochRoot(p.Epoch.ID)
	if err := s.withRetiredMetadata(ctx, p, !held, func(dir *os.File, hasRecord bool) error {
		// Keep epoch.json until cgroup cleanup is finished so replay can verify a surviving group.
		if err := workloadfence.RemoveRetiredRootUnderBudget(ctx, s.cfg.Budget, root, s.cfg.Parent, p.Epoch.Reference); err != nil {
			return err
		}
		if dir == nil {
			return nil
		}
		if hasRecord {
			if err := s.ops.remove(filepath.Join(root, "epoch.json")); err != nil {
				return ErrIO
			}
		}
		if err := s.ops.sync(dir); err != nil {
			return ErrIO
		}
		if err := s.ops.remove(root); err != nil {
			return ErrIO
		}
		return nil
	}); err != nil {
		return err
	}
	if err := s.syncDir(filepath.Dir(root)); err != nil {
		return err
	}
	if err := s.ops.remove(filepath.Join(s.cfg.MetadataRoot, ownerRetireFile)); err != nil {
		return ErrIO
	}
	return nil
}
