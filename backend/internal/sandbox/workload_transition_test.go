package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type transitionSnapshot struct {
	ready, dead bool
	dispatch    *dispatchState
}

func transitionState(w *Worker) transitionSnapshot {
	return transitionSnapshot{w.ready.Load(), w.dead.Load(), w.disp.Load()}
}

type transitionSession struct {
	revoke func(string)
}

func (s transitionSession) LeaseRevoked(lease string) { s.revoke(lease) }
func (s transitionSession) Closed()                   {}

type transitionCloser struct{ close func() }

func (c transitionCloser) Write(p []byte) (int, error) { return len(p), nil }
func (c transitionCloser) Close() error                { c.close(); return nil }

func TestWorkloadTransitionAtomicLifecycle(t *testing.T) {
	w := &Worker{sdkEnabled: true, identity: &WorkloadIdentity{WorkerID: "host-worker"}, frames: make(chan frameRead, 1)}
	var changes [][2]transitionSnapshot
	w.workloadTransition = func(change func()) {
		before := transitionState(w)
		change()
		changes = append(changes, [2]transitionSnapshot{before, transitionState(w)})
	}
	for _, frame := range []string{`{`, `{"type":"response"}`} {
		w.frames <- frameRead{payload: []byte(frame)}
		if w.AwaitReady(context.Background()) == nil {
			t.Fatal("invalid readiness accepted")
		}
	}
	if len(changes) != 0 {
		t.Fatal("invalid readiness entered hook")
	}
	w.frames <- frameRead{payload: []byte(`{"type":"ready"}`)}
	if err := w.AwaitReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := WithExecution(context.Background(), ExecInfo{ExecutionID: "original", Lease: "same-lease"})
	w.beginLease(ctx)
	first := w.disp.Load()
	caller := workloadCaller(w, first, first.info.Lease)
	workloadState(t, caller, WorkloadExecution)
	w.beginLease(ctx)
	second := w.disp.Load()
	workloadState(t, caller, WorkloadStaleLease)
	w.revokeLease()
	w.beginLease(ctx)
	third := w.disp.Load()
	workloadState(t, caller, WorkloadStaleLease)
	w.markDead()
	workloadState(t, caller, WorkloadInactive)
	want := [][2]transitionSnapshot{
		{{}, {ready: true}},
		{{ready: true}, {ready: true, dispatch: first}},
		{{ready: true, dispatch: first}, {ready: true, dispatch: second}},
		{{ready: true, dispatch: second}, {ready: true}},
		{{ready: true}, {ready: true, dispatch: third}},
		{{ready: true, dispatch: third}, {ready: true, dead: true}},
	}
	if len(changes) != len(want) {
		t.Fatalf("transitions = %d, want %d", len(changes), len(want))
	}
	for i := range want {
		if changes[i] != want[i] {
			t.Errorf("transition %d = %+v, want %+v", i, changes[i], want[i])
		}
	}
}

func TestWorkloadTransitionCleanupOutsideHook(t *testing.T) {
	for _, end := range []string{"replace", "revoke", "markDead", "reap"} {
		t.Run(end, func(t *testing.T) {
			w := &Worker{sdkEnabled: true, identity: &WorkloadIdentity{WorkerID: "host-worker"}}
			var active atomic.Bool
			var gate sync.Mutex
			w.workloadTransition = func(change func()) {
				if active.Swap(true) {
					t.Error("nested transition hook")
					change()
					return
				}
				gate.Lock()
				defer gate.Unlock()
				defer active.Store(false)
				previous := w.disp.Load()
				change()
				if previous != nil && previous.ctx.Err() != nil {
					t.Error("dispatch cancellation ran inside hook")
				}
			}
			w.beginLease(context.Background())
			st := w.disp.Load()
			caller := workloadCaller(w, st, st.info.Lease)
			reenter := func() {
				if active.Load() {
					t.Error("cleanup called under transition hook")
					return
				}
				w.transitionWorkload(func() {})
				if state, _ := caller.WorkloadState(); state.Phase == WorkloadExecution {
					t.Error("original caller still active during cleanup")
				}
			}
			cancel := st.cancel
			var cancelled, revoked, closed int
			st.cancel = func() { reenter(); cancelled++; cancel() }
			session := &SDKSession{}
			session.State(func() SDKSessionState {
				return transitionSession{revoke: func(lease string) {
					reenter()
					if lease != st.info.Lease || st.ctx.Err() == nil {
						t.Error("session teardown lost lease or preceded cancellation")
					}
					revoked++
				}}
			})
			w.sdkSess.Store(session)
			w.stdin = transitionCloser{close: func() { reenter(); closed++ }}
			switch end {
			case "replace":
				w.beginLease(context.Background())
			case "revoke":
				w.revokeLease()
			case "markDead":
				w.markDead()
			case "reap":
				w.invalidateWorkload()
			}
			if cancelled != 1 || revoked != 1 || (closed == 1) != (end == "markDead") {
				t.Fatalf("cleanup counts: cancel=%d revoke=%d close=%d", cancelled, revoked, closed)
			}
			if current := w.disp.Load(); current != nil {
				current.cancel()
			}
		})
	}
}

func TestWorkloadTransitionProtocolCompatibility(t *testing.T) {
	for _, sdk := range []bool{false, true} {
		for _, hooked := range []bool{false, true} {
			t.Run(fmt.Sprintf("sdk=%v/hook=%v", sdk, hooked), func(t *testing.T) {
				h := newWorkerHarness(t, sdk)
				h.w.identity = &WorkloadIdentity{WorkerID: "host-worker"}
				var transitions atomic.Int32
				if hooked {
					h.w.workloadTransition = func(change func()) { change(); transitions.Add(1) }
				}
				ctx, _ := testCtx(t)
				done := make(chan error, 1)
				go func() { _, _, err := h.w.Dispatch(ctx, []byte(`{}`)); done <- err }()
				n := 1
				if sdk {
					n++
				}
				types, lease := h.adapterFrames(t, n)
				if types[n-1] != "request" || (sdk && (types[0] != "lease" || lease == "")) {
					t.Fatalf("unexpected protocol: %v, lease %q", types, lease)
				}
				if !sdk {
					state, _ := h.w.WorkloadState()
					if h.w.disp.Load() != nil || lease != "" || state.Phase != WorkloadNoChannel {
						t.Fatal("channel-less worker acquired lease authority")
					}
				}
				h.reply(t, `{"type":"response","statusCode":200,"body":"unchanged"}`)
				if err := <-done; err != nil {
					t.Fatal(err)
				}
				want := int32(0)
				if hooked {
					want = int32(n)
				}
				if transitions.Load() != want || h.w.disp.Load() != nil || h.w.Served.Load() != 1 {
					t.Fatal("protocol completion or lifecycle changed")
				}
			})
		}
	}
}

func TestWorkloadTransitionSpawnReaping(t *testing.T) {
	for _, markDead := range []bool{false, true} {
		t.Run(fmt.Sprintf("markDead=%v", markDead), func(t *testing.T) {
			root := t.TempDir()
			for _, dir := range []string{"node/usr/local/bin", "node/opt/orva"} {
				if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			for path, content := range map[string]string{
				"node/usr/local/bin/node": "", "node/opt/orva/adapter.js": "// orva-sdk-proto:1",
				"fake-jail": fmt.Sprintf("#!/bin/sh\nexec %q -test.run=^TestWorkloadFakeJail$ -- --workload-fake-jail \"$@\"\n", exe),
			} {
				if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			var active atomic.Bool
			var transitions atomic.Int32
			exited := make(chan struct{})
			cfg := ExecConfig{Language: Node, RootfsDir: root, CodeDir: root, NsjailBin: filepath.Join(root, "fake-jail"),
				SDKHandler: func(context.Context, SDKCaller, string, json.RawMessage) (any, *SDKError) { return nil, nil },
				WorkloadTransitionHook: func(change func()) {
					if active.Swap(true) {
						t.Error("nested transition during death/reaping")
					}
					change()
					transitions.Add(1)
					active.Store(false)
				},
				OnExit: func() {
					if active.Load() {
						t.Error("OnExit called under hook")
					}
					close(exited)
				},
			}
			ctx, _ := testCtx(t)
			w, err := Spawn(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = w.Kill()
				select {
				case <-w.waitDone:
				case <-time.After(5 * time.Second):
					t.Error("fake jail cleanup did not reap process")
				}
			})
			cfg.WorkloadTransitionHook = func(func()) { t.Error("Spawn did not capture hook") }
			if err := w.AwaitReady(ctx); err != nil {
				t.Fatal(err)
			}
			w.beginLease(ctx)
			st := w.disp.Load()
			if markDead {
				w.markDead()
			} else {
				_ = w.stdin.Close()
			}
			select {
			case <-exited:
			case <-ctx.Done():
				t.Fatal("fake jail not reaped")
			}
			<-w.waitDone
			want := int32(3)
			if markDead {
				want++
			}
			if transitions.Load() != want || !w.dead.Load() || w.disp.Load() != nil || st.ctx.Err() == nil || w.Cmd.ProcessState == nil || !w.Cmd.ProcessState.Exited() {
				t.Fatalf("reaper state: transitions=%d dead=%v dispatch=%p ctx=%v process=%v", transitions.Load(), w.dead.Load(), w.disp.Load(), st.ctx.Err(), w.Cmd.ProcessState)
			}
		})
	}
}
