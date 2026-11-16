package handlers

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
)

func TestIAMInvokeCoreJail(t *testing.T) {
	if !strings.Contains(strings.Join(os.Args, " "), "--iam-invoke-core") {
		return
	}
	b := []byte(`{"type":"ready"}`)
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(b)))
	_, _ = os.Stdout.Write(append(header[:], b...))
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func TestIAMInvokeCoreDenialBeforeBindingAndDispatch(t *testing.T) {
	db := newTestDB(t)
	const id = "019a8dd0-0000-7000-8000-000000000002"
	fn := &database.Function{ID: id, Name: "iam-core", Runtime: "node", Entrypoint: "handler.js", TimeoutMS: 30000, MemoryMB: 64, CPUs: .5, EnvVars: map[string]string{}, NetworkMode: "none", Status: "active"}
	if err := db.InsertFunction(fn); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{
		"node/usr/local/bin/node": "", "node/opt/orva/adapter.js": "//\n",
		"jail": fmt.Sprintf("#!/bin/sh\nexec %q -test.run=^TestIAMInvokeCoreJail$ -- --iam-invoke-core \"$@\"\n", exe),
	} {
		p := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pm := pool.NewManager(pool.ManagerConfig{DefaultMin: 0, DefaultIdleTTL: time.Minute, DefaultMaxUses: 1000, ReapInterval: time.Hour},
		pool.SandboxTemplate{NsjailBin: filepath.Join(root, "jail"), RootfsDir: root, DataDir: root, DefaultMaxPids: 32}, db, registry.New(db), sandbox.NewLimiter(4))
	t.Cleanup(func() { _ = pm.Shutdown(context.Background()) })
	// A nil SDKAuth would panic if the hook ran after execution binding.
	h := &InternalInvokeHandler{DB: db, Pool: pm}
	denied := &InvokeError{Status: 403, Opts: respond.ErrorOpts{Code: "IAM_DENIED", Message: "SDK operation denied"}}
	var actual *sandbox.Worker
	for range 2 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		result, ierr := h.RunInvokeWithAdmission(ctx, F2FCaller{FunctionID: id}, fn, []byte(`{}`), "", func(w *sandbox.Worker) *InvokeError {
			if actual != nil && actual != w {
				t.Error("healthy denied worker was not reused")
			}
			actual = w
			s, ok := w.WorkloadState()
			if !ok || !s.Ready || !s.Alive || s.Identity.FunctionID != id || w.Served.Load() != 0 {
				t.Error("hook did not observe an acquired, undispatched worker")
			}
			return denied
		})
		cancel()
		if result != nil || ierr != denied {
			t.Fatalf("denial changed: %s %v", result, ierr)
		}
	}
	if actual == nil {
		t.Fatal("hook was not called")
	}
	if s, _ := actual.WorkloadState(); !s.Alive || actual.Served.Load() != 0 {
		t.Fatal("denial dispatched or poisoned the worker")
	}
	for _, s := range pm.Stats() {
		if s.Busy != 0 {
			t.Fatal("worker lease leaked")
		}
	}
	var count int
	if err := db.ReadDB().QueryRow(`SELECT COUNT(*) FROM executions WHERE function_id=?`, id).Scan(&count); err != nil || count != 0 {
		t.Fatalf("denied call recorded execution: %d %v", count, err)
	}
	// With every reservation returned, the full critical capacity is available.
	var leases []*database.ExecutionLease
	defer func() {
		for _, lease := range leases {
			lease.Cancel()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for range db.WriterStats().CriticalCap {
		lease, err := db.ReserveExecution(ctx)
		if err != nil {
			t.Fatalf("storage reservation leaked: %v", err)
		}
		leases = append(leases, lease)
	}
}
