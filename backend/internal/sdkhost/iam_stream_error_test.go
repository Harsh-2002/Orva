package sdkhost

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/sdkauth"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

func vouchErrorEnv(t *testing.T) *invEnv {
	t.Helper()
	db := newDB(t)
	t.Cleanup(func() { _ = db.Close() })
	for _, f := range []*database.Function{{ID: iamOwner, Name: "iam-0"}, {ID: iamOther, Name: "iam-1"}} {
		f.Runtime, f.Entrypoint, f.MemoryMB, f.CPUs, f.NetworkMode, f.TimeoutMS, f.Status = "node", "handler.js", 64, 0.5, "none", 30000, "active"
		f.EnvVars = map[string]string{}
		if err := db.InsertFunction(f); err != nil {
			t.Fatal(err)
		}
	}
	tmp := t.TempDir()
	root := filepath.Join(tmp, "rootfs")
	for _, d := range []string{"node/usr/local/bin", "node/opt/orva"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"node/usr/local/bin/node", "node/opt/orva/adapter.js"} {
		if err := os.WriteFile(filepath.Join(root, p), []byte("// fixture\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	adapter := strings.Replace(fakeAdapter, `    elif m in ("stream", "slowstream", "big"):`, `    elif m == "streamerror":
        wr({"type":"response_start","statusCode":200})
        wr({"type":"error","message":cmd.get("message","terminal-marker")})
    elif m in ("stream", "slowstream", "big"):`, 1)
	if adapter == fakeAdapter {
		t.Fatal("scratch adapter insertion failed")
	}
	jail := filepath.Join(tmp, "fake-nsjail")
	if err := os.WriteFile(jail, []byte(adapter), 0755); err != nil {
		t.Fatal(err)
	}
	pm := pool.NewManager(pool.ManagerConfig{DefaultMin: 0, DefaultIdleTTL: time.Minute, DefaultMaxUses: 1000, ReapInterval: time.Hour}, pool.SandboxTemplate{NsjailBin: jail, RootfsDir: root, DataDir: tmp, DefaultMaxPids: 32}, db, registry.New(db), sandbox.NewLimiter(50))
	t.Cleanup(func() { _ = pm.Shutdown(context.Background()) })
	auth := sdkauth.New()
	inv := &handlers.InternalInvokeHandler{DB: db, Pool: pm, SDKAuth: auth}
	return &invEnv{db: db, pm: pm, auth: auth, inv: inv, host: New(db, WithInvoker(inv))}
}

func TestVouchStreamErrorHandoffBound(t *testing.T) {
	f := newIAMStreamFixture(t, 1)
	f.e = vouchErrorEnv(t)
	f.h.SetInvoker(f.e.inv)
	msg := strings.Repeat("E", workloadiam.MaxHandoffBytes+1024)
	body, _ := json.Marshal(map[string]string{"mode": "streamerror", "message": msg})
	handle, _ := f.open(t, string(body))
	r, e := f.h.Handle(f.c.ctx, f.c.caller, "invoke.stream.next", json.RawMessage(`{"handle":"`+handle+`"}`))
	if r != nil {
		t.Fatal("error returned response data")
	}
	if e == nil {
		t.Fatal("adapter error not surfaced")
	}
	wire, _ := json.Marshal(e)
	t.Logf("decoded handoff limit=%d; returned error message bytes=%d; encoded SDKError bytes=%d; status=%d code=%s", workloadiam.MaxHandoffBytes, len(e.Message), len(wire), e.Status, e.Code)
	if len(e.Message) > workloadiam.MaxHandoffBytes {
		t.Fatalf("error message bypassed bounded handoff: got %d > %d bytes after nil-frame authorization", len(e.Message), workloadiam.MaxHandoffBytes)
	}
}

func streamErrorAtBound(t *testing.T, unit string) string {
	t.Helper()
	base, err := json.Marshal(serr(502, "INVOKE_FAILED", ""))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(unit)
	if err != nil || len(encoded) <= 2 {
		t.Fatalf("invalid test unit %q: %v", unit, err)
	}
	available, step := workloadiam.MaxHandoffBytes-len(base), len(encoded)-2
	return strings.Repeat(unit, (available-1)/step) + strings.Repeat("a", (available-1)%step) + "a"
}

func TestIAMStreamErrorEnvelopeBoundsAndOwnedCopy(t *testing.T) {
	f := newIAMStreamFixture(t, 1)
	call := f.begin(t)
	cs := &chStream{iam: &iamStream{call: call, worker: f.target, targetID: iamOther}}
	for _, unit := range []string{"a", "\"", "\\", "\n", "<>&", "雪", "😀", "\u2028"} {
		t.Run(unit, func(t *testing.T) {
			msg := streamErrorAtBound(t, unit)
			original := serr(502, "INVOKE_FAILED", msg)
			wire, _ := json.Marshal(original)
			if len(wire) != workloadiam.MaxHandoffBytes {
				t.Fatalf("fixture size %d", len(wire))
			}
			got := f.h.handoffIAMStreamError(f.c.ctx, f.c.caller, cs, original)
			if got == original || got.Status != 502 || got.Code != original.Code || got.Message != msg {
				t.Fatal("bounded error was not reconstructed from its owned copy")
			}
			original.Message = "changed after handoff"
			if got.Message != msg {
				t.Fatal("error retained original storage")
			}
			below := f.h.handoffIAMStreamError(f.c.ctx, f.c.caller, cs, serr(502, "INVOKE_FAILED", msg[:len(msg)-1]))
			belowWire, _ := json.Marshal(below)
			if below.Code != "INVOKE_FAILED" || len(belowWire) != workloadiam.MaxHandoffBytes-1 {
				t.Fatal("one-byte-below-bound envelope rejected or changed")
			}
			over := f.h.handoffIAMStreamError(f.c.ctx, f.c.caller, cs, serr(502, "INVOKE_FAILED", msg+"a"))
			if over.Code != "IAM_DENIED" || over.Message != "SDK operation denied" {
				t.Fatal("oversized serialized envelope disclosed dynamic error")
			}
		})
	}
	original := serr(502, "INVOKE_FAILED", "\xff")
	got := f.h.handoffIAMStreamError(f.c.ctx, f.c.caller, cs, original)
	if got.Message != "\ufffd" {
		t.Fatalf("returned original invalid UTF-8 instead of admitted JSON copy: %q", got.Message)
	}
}

func TestIAMStreamNextErrorEscapingAndFreshness(t *testing.T) {
	f := newIAMStreamFixture(t, 1)
	f.e = vouchErrorEnv(t)
	f.h.SetInvoker(f.e.inv)
	for _, msg := range []string{"bounded error", strings.Repeat("\"", workloadiam.MaxHandoffBytes/2), strings.Repeat("雪", workloadiam.MaxHandoffBytes/3)} {
		body, _ := json.Marshal(map[string]string{"mode": "streamerror", "message": msg})
		handle, _ := f.open(t, string(body))
		r, e := f.h.Handle(f.c.ctx, f.c.caller, "invoke.stream.next", json.RawMessage(`{"handle":"`+handle+`"}`))
		if r != nil || e == nil {
			t.Fatal("terminal error missing")
		}
		wire, _ := json.Marshal(e)
		if len(wire) > workloadiam.MaxHandoffBytes {
			t.Fatalf("error envelope exceeded bound: %d", len(wire))
		}
		if msg == "bounded error" && (e.Code != "INVOKE_FAILED" || !strings.Contains(e.Message, msg)) {
			t.Fatal("bounded dynamic error was lost")
		}
		if msg != "bounded error" && e.Code != "IAM_DENIED" {
			t.Fatal("overflowing escaped/Unicode error was disclosed")
		}
		f.e.waitBusy(t, 0)
	}
	// The immutable envelope is prepared before the fresh observed handoff.
	call := f.begin(t)
	cs := &chStream{iam: &iamStream{call: call, worker: f.target, targetID: iamOther}}
	f.revoke(t)
	e := f.h.handoffIAMStreamError(f.c.ctx, f.c.caller, cs, serr(502, "INVOKE_FAILED", "private"))
	if e.Code != "IAM_DENIED" || e.Message != "SDK operation denied" {
		t.Fatal("revoked error handoff disclosed dynamic error")
	}
}

func TestIAMStreamOpenErrorEnvelope(t *testing.T) {
	f := newIAMStreamFixture(t, 1)
	call := f.begin(t)
	cs := &chStream{iam: &iamStream{call: call, worker: f.target, targetID: iamOther}}
	fn, ie := f.e.inv.ResolveCallee("iam-1")
	if ie != nil {
		t.Fatal(ie)
	}
	s, ie := f.e.inv.OpenStreamWithAdmission(call.Context(), handlers.F2FCaller{FunctionID: iamOwner}, fn,
		[]byte(`{"mode":"fail"}`), "", func(*sandbox.Worker) *handlers.InvokeError { return nil })
	if s != nil || ie == nil {
		t.Fatal("fixture did not produce an open error")
	}
	got := f.h.handoffIAMStreamError(f.c.ctx, f.c.caller, cs, fromInvokeErr(ie))
	if got.Status != ie.Status || got.Code != ie.Opts.Code || got.Message != ie.Opts.Message {
		t.Fatal("bounded open error changed")
	}
	oversized := &handlers.InvokeError{Status: 502, Opts: respond.ErrorOpts{
		Code: "INVOKE_FAILED", Message: streamErrorAtBound(t, "\n") + "\n",
	}}
	got = f.h.handoffIAMStreamError(f.c.ctx, f.c.caller, cs, fromInvokeErr(oversized))
	if got.Code != "IAM_DENIED" || got.Message != "SDK operation denied" {
		t.Fatal("oversized open error disclosed")
	}
	f.e.waitBusy(t, 0)
}
