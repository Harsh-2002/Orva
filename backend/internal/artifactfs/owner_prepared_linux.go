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

	"github.com/Harsh-2002/Orva/backend/internal/workloadfence"
)

const ownerPreparedFile = "owner.prepared"

type ownerPreparedRecord struct {
	Version int    `json:"version"`
	Before  string `json:"before"`
	After   string `json:"after"`
	MAC     string `json:"mac"`
}

type preparedOwnerReplay struct {
	promote bool
	intent  bool
}

func ownerRecordDigest(d ownerRecord) string {
	raw, _ := json.Marshal(d)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (w *ownerWitness) preparedMAC(p ownerPreparedRecord) (string, error) {
	key, err := w.key()
	if err != nil {
		return "", err
	}
	if !hmac.Equal(key[:], w.token[:]) {
		return "", ErrIntegrity
	}
	p.MAC = ""
	raw, _ := json.Marshal(p)
	mac := hmac.New(sha256.New, key[:])
	_, _ = mac.Write([]byte("orva:artifactfs:owner-prepared:v1\x00"))
	_, _ = mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func (s *ownerState) prepareOwnerTransition(before ownerRecord) error {
	if err := s.witness.verify(before); err != nil {
		return err
	}
	if err := s.validateOwnerAppend(before, s.disk); err != nil {
		return err
	}
	p := ownerPreparedRecord{Version: 1, Before: ownerRecordDigest(before), After: ownerRecordDigest(s.disk)}
	var err error
	if p.MAC, err = s.witness.preparedMAC(p); err != nil {
		return err
	}
	raw, _ := json.Marshal(p)
	if err := s.writeFile(ownerPreparedFile, raw); err != nil {
		return err
	}
	return s.syncDir(s.cfg.MetadataRoot)
}

func (s *ownerState) validateOwnerAppend(before, after ownerRecord) error {
	if before.Version != 2 || after.Version != 2 || len(after.Epochs) != len(before.Epochs)+1 ||
		len(before.Epochs) == 0 || after.Epochs[len(before.Epochs)].Reference.Boot != before.Epochs[len(before.Epochs)-1].Reference.Boot {
		return ErrIntegrity
	}
	truncated := after
	truncated.Epochs = after.Epochs[:len(before.Epochs)]
	if err := s.witness.sign(&truncated); err != nil {
		return err
	}
	if ownerRecordDigest(truncated) != ownerRecordDigest(before) {
		return ErrIntegrity
	}
	return s.witness.verify(after)
}

func (s *ownerState) readPreparedOwner(ctx context.Context) (*preparedOwnerReplay, error) {
	root := s.cfg.MetadataRoot
	path := filepath.Join(root, ownerPreparedFile)
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, ErrIO
	}
	raw, err := privateMetadata(path, false, 512)
	if err != nil {
		return nil, err
	}
	var p ownerPreparedRecord
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, ErrIntegrity
	}
	canonical, _ := json.Marshal(p)
	if !bytes.Equal(raw, canonical) || p.Version != 1 || !ownerHex(p.Before, 64) ||
		!ownerHex(p.After, 64) || !ownerHex(p.MAC, 64) || p.Before == p.After {
		return nil, ErrIntegrity
	}
	mac, err := s.witness.preparedMAC(p)
	if err != nil || !hmac.Equal([]byte(p.MAC), []byte(mac)) {
		return nil, ErrIntegrity
	}

	replay := &preparedOwnerReplay{}
	entries := []string{"owner.json", "epochs", ownerPreparedFile}
	intent := filepath.Join(root, "owner.intent")
	if _, err := os.Lstat(intent); err == nil {
		content, err := privateMetadata(intent, false, 32)
		if err != nil || string(content) != `{"version":1}` {
			return nil, ErrIntegrity
		}
		replay.intent = true
		entries = append(entries, "owner.intent")
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, ErrIO
	}

	switch ownerRecordDigest(s.disk) {
	case p.Before:
		if !replay.intent {
			return nil, ErrIntegrity
		}
		after, err := readOwnerRecord(filepath.Join(root, "owner.json.tmp"))
		if err != nil || ownerRecordDigest(after) != p.After {
			return nil, ErrIntegrity
		}
		if err := s.validateOwnerAppend(s.disk, after); err != nil {
			return nil, err
		}
		s.disk = after
		entries = append(entries, "owner.json.tmp")
		replay.promote = true
	case p.After:
		if len(s.disk.Epochs) < 2 {
			return nil, ErrIntegrity
		}
		before := s.disk
		before.Epochs = before.Epochs[:len(before.Epochs)-1]
		if err := s.witness.sign(&before); err != nil {
			return nil, err
		}
		if ownerRecordDigest(before) != p.Before {
			return nil, ErrIntegrity
		}
		if err := s.validateOwnerAppend(before, s.disk); err != nil {
			return nil, err
		}
	default:
		return nil, ErrIntegrity
	}
	if err := s.validateOwnerLayout(ctx, entries...); err != nil {
		return nil, err
	}
	return replay, nil
}

func (s *ownerState) reconcilePreparedOwner(ctx context.Context, replay *preparedOwnerReplay,
	old []*workloadfence.Domain, proofs []*workloadfence.Proof, prior []ownerEpochRecord) error {
	marker, err := openNoFollow(filepath.Join(s.cfg.MetadataRoot, ownerPreparedFile), false)
	if err != nil {
		return ErrIO
	}
	err = s.ops.sync(marker)
	_ = marker.Close()
	if err != nil {
		return ErrIO
	}
	if err := s.syncDir(s.cfg.MetadataRoot); err != nil {
		return err
	}
	if replay.promote {
		if err := s.promoteOwnerRecord(); err != nil {
			return err
		}
	}
	if err := s.checkRecoveryBindings(ctx, old, proofs, prior); err != nil {
		return err
	}
	if err := s.checkPreparedCommitted(ctx); err != nil {
		return err
	}
	return s.finishOwnerCommit(true, replay.intent)
}

func (s *ownerState) checkPreparedCommitted(ctx context.Context) error {
	committed, err := readOwnerRecord(filepath.Join(s.cfg.MetadataRoot, "owner.json"))
	if err != nil || ownerRecordDigest(committed) != ownerRecordDigest(s.disk) {
		return ErrIntegrity
	}
	replay, err := s.readPreparedOwner(ctx)
	if err != nil || replay == nil || replay.promote {
		return ErrIntegrity
	}
	return ctx.Err()
}
