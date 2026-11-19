package artifactfs

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestOwnerKernelRemovedEpochs(t *testing.T) {
	budget, base := ownerTestBudget(t, true)
	for _, name := range []string{"removed-and-retired", "group-only", "group-replaced", "tampered-parent", "unbound", "old-host-bound"} {
		t.Run(name, func(t *testing.T) {
			ctx := ownerTestContext(t)
			parent := filepath.Join(base, name)
			mustOwnerRemoval(t, os.Mkdir(parent, 0700))
			redelegate := func() {
				mustOwnerRemoval(t, os.WriteFile(filepath.Join(parent, "cgroup.subtree_control"), []byte("+cpu +memory +pids"), 0600))
			}
			redelegate()
			cfg := ownerConfigAt(t, t.TempDir(), budget, parent)
			cfg.MaxEpochs = 2
			if name != "unbound" {
				cfg.Host = testOwnerHost(t)
			}
			o, err := NewOwner(ctx, cfg)
			mustOwnerRemoval(t, err)
			code := sealed(t, o.s.code, KindCode, ids.New())
			runtime := sealed(t, o.s.runtime, KindRuntime, "node")
			mustOwnerRemoval(t, o.Close(ctx))
			first := o.s.disk.Epochs[0]
			group := filepath.Join(parent, "epoch-"+first.Reference.Epoch)
			// Hold the original parent descriptor while comparing the replacement.
			pin, err := os.Open(parent)
			mustOwnerRemoval(t, err)
			defer pin.Close()
			old, err := pin.Stat()
			mustOwnerRemoval(t, err)
			epochFile := filepath.Join(o.s.epochRoot(first.ID), "epoch.json")
			switch name {
			case "group-only", "group-replaced":
				ownerRemoveGroups(t, group)
				if name == "group-replaced" {
					mustOwnerRemoval(t, os.Mkdir(group, 0700))
				}
			default:
				if name == "old-host-bound" {
					o.s.disk.Epochs[0].Reference.ParentInode, o.s.disk.Epochs[0].Reference.ParentDevice = 0, 0
					w, err := openOwnerWitness(cfg)
					mustOwnerRemoval(t, err)
					mustOwnerRemoval(t, w.sign(&o.s.disk))
					mustOwnerRemoval(t, w.file.Close())
					raw, err := json.Marshal(o.s.disk)
					mustOwnerRemoval(t, err)
					mustOwnerRemoval(t, os.WriteFile(filepath.Join(cfg.MetadataRoot, "owner.json"), raw, 0600))
					o, err = OpenOwnerRecovery(ctx, cfg)
					mustOwnerRemoval(t, err) // An old reference still supports exact fencing.
					mustOwnerRemoval(t, o.Close(ctx))
				}
				ownerRemoveGroups(t, parent)
				mustOwnerRemoval(t, os.Mkdir(parent, 0700))
				redelegate()
				current, err := os.Stat(parent)
				mustOwnerRemoval(t, err)
				if os.SameFile(old, current) {
					t.Fatal("kernel did not replace the parent identity")
				}
				if name == "tampered-parent" {
					raw, err := os.ReadFile(epochFile)
					mustOwnerRemoval(t, err)
					var r struct {
						Parent struct{ Device, Inode uint64 }
					}
					mustOwnerRemoval(t, json.Unmarshal(raw, &r))
					var fields map[string]json.RawMessage
					mustOwnerRemoval(t, json.Unmarshal(raw, &fields))
					r.Parent.Inode++
					changed, err := json.Marshal(map[string]uint64{"device": r.Parent.Device, "inode": r.Parent.Inode})
					mustOwnerRemoval(t, err)
					raw = bytes.Replace(raw, fields["parent"], changed, 1)
					mustOwnerRemoval(t, os.WriteFile(epochFile, raw, 0600))
				}
			}
			got, err := OpenOwnerRecovery(ctx, cfg)
			if name != "removed-and-retired" {
				if err == nil || got != nil {
					t.Fatal("ambiguous epoch recovery accepted", err)
				}
				return
			}
			mustOwnerRemoval(t, err)
			assertRetiredBindings(t, ctx, got, code, runtime)
			if got.s.disk.Epochs[1].Reference.ParentInode == first.Reference.ParentInode {
				t.Fatal("fresh epoch was not bound to the replacement parent")
			}
			mustOwnerRemoval(t, got.Close(ctx))
			// A replacement at the retired epoch's path must not be deleted.
			mustOwnerRemoval(t, os.Mkdir(group, 0700))
			got, err = OpenOwnerRecovery(ctx, cfg)
			mustOwnerRemoval(t, err)
			assertRetiredBindings(t, ctx, got, code, runtime)
			_, err = os.Stat(epochFile)
			if !os.IsNotExist(err) {
				t.Fatalf("retired metadata remains: %v", err)
			}
			_, err = os.Stat(group)
			mustOwnerRemoval(t, err)
			mustOwnerRemoval(t, got.Close(ctx))
		})
	}
}

func mustOwnerRemoval(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
