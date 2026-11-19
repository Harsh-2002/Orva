//go:build linux

package builder

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/workloadfence"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestManagedOutputProfilesExclusive(t *testing.T) {
	for _, cfg := range []OutputConfig{
		{},
		{ParentDomain: &workloadfence.Domain{}, Budget: &workloadfence.Budget{}},
		{ParentDomain: &workloadfence.Domain{}, Parent: "/not-authority"},
		{Budget: &workloadfence.Budget{}},
		{Parent: "/not-authority"},
	} {
		if owner, err := NewOutputOwner(cfg); err == nil || owner != nil {
			t.Fatal("ambiguous or incomplete profile accepted")
		}
	}
}

func TestManagedOutputNestedKernel(t *testing.T) {
	budget, parent := outputTestBudget(t)
	for _, mode := range []string{"normal", "fence", "cancel", "quarantine", "closed", "child-failure"} {
		t.Run(mode, func(t *testing.T) {
			meta := t.TempDir()
			if err := os.Chmod(meta, 0700); err != nil {
				t.Fatal(err)
			}
			root, err := workloadfence.CreateUnderBudget(budget, meta, parent)
			if err != nil {
				t.Fatal(err)
			}
			jails, err := root.PrepareJails(t.Context(), "cpu", "memory", "pids")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				proof, err := root.Fence(ctx)
				if err != nil {
					t.Error(err)
				} else if err := root.Check(ctx, proof); err != nil {
					t.Error(err)
				}
				if err := root.Close(); err != nil {
					t.Error(err)
				}
				if err := removeOutputGroups(filepath.Dir(jails.Path()), 0); err != nil {
					t.Error(err)
				}
			})
			s, _, l := outputTestStore(t)
			rs, rid, _ := builderRuntime(t, artifactfs.KindRuntime, "node", "node fixture")
			cfg := outputTestConfig(t, budget, parent, s, l)
			cfg.ParentDomain, cfg.Budget, cfg.Parent = root, nil, ""
			owner, err := NewOutputOwner(cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := owner.Close(); err != nil {
					t.Error(err)
				}
			})
			b := New(WithManagedOutput(owner), WithRuntimeBinding(func(context.Context, RuntimeRequest) (RuntimeBinding, error) {
				return RuntimeBinding{Runtime: sandbox.BuildRuntime{Store: rs, Generation: rid}}, nil
			}))
			b.NsjailBin, b.RootfsDir = outputTestJail(t), "/not-used"
			event := filepath.Join(t.TempDir(), "event")
			t.Setenv("OUTPUT_EVENT", event)
			childMode := "normal"
			if mode == "fence" || mode == "cancel" || mode == "quarantine" {
				childMode = "cancel"
			}
			t.Setenv("OUTPUT_CHILD_MODE", childMode)
			fn := &database.Function{ID: ids.New(), Runtime: "node", Entrypoint: "handler.js"}
			archive := createTestArchive(t, map[string]string{"handler.js": "exports.handler = () => 42"})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "closed" {
				if _, err := root.Fence(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "child-failure" {
				// The failure precedes creation of any child ownership.
				if err := os.Chmod(cfg.MetadataRoot, 0500); err != nil {
					t.Fatal(err)
				}
				// Root ignores file permissions; remove the still-open empty root.
				if err := os.Remove(cfg.MetadataRoot); err != nil {
					t.Fatal(err)
				}
			}
			var result *BuildResult
			var buildErr error
			if mode == "fence" || mode == "cancel" || mode == "quarantine" {
				done := make(chan struct{})
				go func() { result, buildErr = b.Build(ctx, fn, archive); close(done) }()
				awaitOutputFile(t, event)
				membership := awaitOutputFile(t, event+".membership")
				if !strings.Contains(membership, strings.TrimPrefix(jails.Path(), "/sys/fs/cgroup")+"/epoch-") {
					t.Fatal("build not within instance", membership)
				}
				dirs, err := os.ReadDir(cfg.MetadataRoot)
				if err != nil || len(dirs) != 1 {
					t.Fatal("missing exclusive child metadata", err)
				}
				childMetadata := filepath.Join(cfg.MetadataRoot, dirs[0].Name())
				data, err := os.ReadFile(filepath.Join(childMetadata, "epoch.json"))
				var ref struct {
					Lineage struct{ Parent, Instance string }
				}
				if err != nil || json.Unmarshal(data, &ref) != nil {
					t.Fatal("invalid child metadata", err)
				}
				rootRef, rootErr := root.Reference(t.Context())
				if rootErr != nil || ref.Lineage.Parent != rootRef.Epoch || ref.Lineage.Instance != rootRef.Epoch {
					t.Fatalf("typed build association: %+v %v %v", ref, err, rootErr)
				}
				if _, err := b.Build(ctx, fn, archive); !errors.Is(err, ErrOutputCapacity) {
					t.Fatal("active child lost reserved capacity", err)
				}
				var saved, record string
				if mode == "quarantine" {
					record = filepath.Join(childMetadata, "epoch.json")
					saved = record + ".saved"
					if err := os.Rename(record, saved); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "fence" {
					proof, err := root.Fence(t.Context())
					if err != nil || root.Check(t.Context(), proof) != nil {
						t.Fatal("root fence", err)
					}
				} else {
					cancel()
				}
				<-done
				if mode == "quarantine" {
					u := owner.Usage()
					if u.Jobs != 1 || u.Pending != 1 || u.ReservedBytes != cfg.ReservedBytes {
						t.Fatal("quarantined child lost reservation", u)
					}
					if err := root.Close(); !errors.Is(err, workloadfence.ErrBusy) {
						t.Fatal("quarantined child lost parent pin", err)
					}
					if err := os.Rename(saved, record); err != nil {
						t.Fatal(err)
					}
					if err := owner.RetryCleanup(t.Context()); err != nil {
						t.Fatal(err)
					}
				}
				outputChildDead(t, event)
			} else {
				result, buildErr = b.Build(ctx, fn, archive)
			}
			if mode == "normal" {
				if buildErr != nil || result == nil || result.CodeArtifact.Generation == "" {
					t.Fatal("typed managed Build/Seal", buildErr)
				}
				if err := s.Retire(result.CodeArtifact.Generation); err != nil {
					t.Fatal(err)
				}
				if err := s.Reclaim(result.CodeArtifact.Generation); err != nil {
					t.Fatal(err)
				}
			} else if buildErr == nil || result != nil {
				t.Fatal("failed/fenced build succeeded", buildErr)
			}
			if u := owner.Usage(); u.Jobs != 0 || u.Pending != 0 || u.ReservedBytes != 0 {
				t.Fatal("typed cleanup leaked quota", u)
			}
			if mode == "fence" {
				if r, err := b.Build(t.Context(), fn, archive); err == nil || r != nil {
					t.Fatal("retained owner revived root-fenced builds")
				}
			}
		})
	}
}
