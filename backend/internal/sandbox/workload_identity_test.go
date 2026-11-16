package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/internal/ids"
)

func workloadState(t *testing.T, c SDKCaller, phase WorkloadPhase) WorkloadState {
	t.Helper()
	s, ok := c.WorkloadState()
	if !ok || s.Phase != phase {
		t.Fatalf("state = %+v, %v; want %s", s, ok, phase)
	}
	return s
}

func workloadCaller(w *Worker, st *dispatchState, lease string) SDKCaller {
	return SDKCaller{workload: &sdkWorkloadSource{worker: w, original: st, lease: lease, ctx: context.Background()}}
}

func TestWorkloadObservation(t *testing.T) {
	for _, c := range []SDKCaller{{}, {FunctionID: ids.New(), Exec: &ExecInfo{Lease: "forged"}}, workloadCaller(&Worker{}, nil, "")} {
		if s, ok := c.WorkloadState(); ok || s != (WorkloadState{}) {
			t.Fatalf("unattached caller fabricated state: %+v", s)
		}
	}
	if _, ok := (*Worker)(nil).WorkloadState(); ok {
		t.Fatal("nil worker fabricated state")
	}
	w := &Worker{sdkEnabled: true, identity: &WorkloadIdentity{FunctionID: ids.New(), WorkerID: ids.New()}}
	module := workloadCaller(w, nil, "")
	workloadState(t, module, WorkloadBootstrap)
	workloadState(t, workloadCaller(w, nil, "forged"), WorkloadStaleLease)
	w.beginLease(WithExecution(context.Background(), ExecInfo{ExecutionID: "first", Lease: "same"}))
	first := w.disp.Load()
	c := workloadCaller(w, first, first.info.Lease)
	if s := workloadState(t, c, WorkloadExecution); s.ExecutionID != "first" || s.Identity.ArtifactRevision != "" {
		t.Fatalf("wrong original identity: %+v", s)
	}
	// Even a dispatch before readiness cannot upgrade an original module caller.
	workloadState(t, module, WorkloadBootstrap)
	w.ready.Store(true)
	workloadState(t, module, WorkloadUnbound)
	first.cancel()
	workloadState(t, c, WorkloadStaleLease)
	w.revokeLease()
	workloadState(t, c, WorkloadStaleLease)
	// Pointer identity prevents reacquisition even if a host supplies the same lease.
	w.beginLease(WithExecution(context.Background(), ExecInfo{ExecutionID: "second", Lease: "same"}))
	workloadState(t, c, WorkloadStaleLease)
	workloadState(t, module, WorkloadUnbound)
	current := workloadCaller(w, w.disp.Load(), "same")
	workloadState(t, current, WorkloadExecution)
	ctx, cancel := context.WithCancel(context.Background())
	closed := workloadCaller(w, nil, "")
	closed.workload.ctx = ctx
	cancel()
	workloadState(t, closed, WorkloadInactive)
	w.markDead()
	s := workloadState(t, current, WorkloadInactive)
	if !s.Ready || s.Alive || s.Identity != *w.identity {
		t.Fatalf("identity/readiness lost at death: %+v", s)
	}
}

func TestWorkloadReadinessValidation(t *testing.T) {
	w := &Worker{sdkEnabled: true, identity: &WorkloadIdentity{WorkerID: ids.New()}, frames: make(chan frameRead, 1)}
	c := workloadCaller(w, nil, "")
	for _, payload := range []string{`{`, `{"type":"response","ready":true}`, `{"type":"ready"}`} {
		w.frames <- frameRead{payload: []byte(payload)}
		err := w.AwaitReady(context.Background())
		if payload != `{"type":"ready"}` {
			if err == nil {
				t.Fatal("invalid handshake accepted")
			}
			workloadState(t, c, WorkloadBootstrap)
		} else if err != nil {
			t.Fatal(err)
		}
	}
	workloadState(t, c, WorkloadUnbound)
	w.frames <- frameRead{payload: []byte(`{"type":"error"}`)}
	if err := w.AwaitReady(context.Background()); err == nil {
		t.Fatal("invalid second handshake accepted")
	}
	workloadState(t, c, WorkloadUnbound)
}

func TestWorkloadChannelLessObservationIsNotAuthority(t *testing.T) {
	w := &Worker{identity: &WorkloadIdentity{FunctionID: ids.New(), WorkerID: ids.New(), ArtifactRevision: "reviewed"}}
	for _, ready := range []bool{false, true} {
		w.ready.Store(ready)
		if frame := w.beginLease(WithExecution(context.Background(), ExecInfo{ExecutionID: ids.New(), Lease: "not-issued"})); frame != nil {
			t.Fatal("channel-less worker received a lease frame")
		}
		s, ok := w.WorkloadState()
		if !ok || s.Phase != WorkloadNoChannel || s.Ready != ready || !s.Alive || s.ExecutionID != "" || s.Lease != "" || s.Identity != *w.identity {
			t.Fatalf("channel-less worker claimed authority: %+v, attached=%v", s, ok)
		}
		w.revokeLease()
		workloadState(t, workloadCaller(w, nil, ""), WorkloadNoChannel)
	}
	w.markDead()
	workloadState(t, workloadCaller(w, nil, ""), WorkloadInactive)
}

func TestWorkloadConcurrentObservation(t *testing.T) {
	w := &Worker{sdkEnabled: true, identity: &WorkloadIdentity{WorkerID: ids.New()}}
	module := workloadCaller(w, nil, "")
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			for range 500 {
				s, ok := module.WorkloadState()
				if !ok || s.Phase == WorkloadExecution || s.Lease != "" || s.ExecutionID != "" {
					t.Error("module caller upgraded")
				}
				_, _ = w.WorkloadState()
			}
		})
	}
	for range 500 {
		w.beginLease(context.Background())
		w.ready.Store(true)
		w.revokeLease()
	}
	wg.Wait()
}

func TestWorkloadChannelLegacyCompatibility(t *testing.T) {
	for _, attached := range []bool{false, true} {
		t.Run(fmt.Sprint(attached), func(t *testing.T) {
			w := &Worker{sdkEnabled: true, identity: &WorkloadIdentity{FunctionID: ids.New(), WorkerID: ids.New()}}
			w.beginLease(WithExecution(context.Background(), ExecInfo{ExecutionID: "original"}))
			defer w.revokeLease()
			lease := w.disp.Load().info.Lease
			host, client := net.Pipe()
			callers := make(chan SDKCaller, 1)
			srv := &sdkServer{conn: host, functionID: w.identity.FunctionID, state: w.disp.Load,
				handler: func(_ context.Context, c SDKCaller, _ string, _ json.RawMessage) (any, *SDKError) {
					callers <- c
					return nil, nil
				},
			}
			if attached {
				srv.worker = w
			}
			done := make(chan struct{})
			go func() { srv.serve(); close(done) }()
			defer func() { _ = client.Close(); <-done }()
			for i, supplied := range []string{"", "forged", lease} {
				sendReq(t, client, uint64(i+1), "kv.get", supplied, "")
				if r := readReply(t, client); !r.OK {
					t.Fatalf("legacy KV behavior changed: %+v", r)
				}
				c := <-callers
				if (c.Exec != nil) != (supplied == lease) {
					t.Fatal("legacy execution attribution changed")
				}
				if !attached {
					if _, ok := c.WorkloadState(); ok {
						t.Fatal("standalone server fabricated authority")
					}
					continue
				}
				phase := []WorkloadPhase{WorkloadBootstrap, WorkloadStaleLease, WorkloadExecution}[i]
				workloadState(t, c, phase)
				w.ready.Store(true)
				if supplied == "" {
					workloadState(t, c, WorkloadUnbound)
				}
			}
		})
	}
}

// This subprocess replaces nsjail, not its isolation guarantees.
func TestWorkloadFakeJail(t *testing.T) {
	if !strings.Contains(strings.Join(os.Args, " "), "--workload-fake-jail") {
		return
	}
	channel := os.Getenv("ORVA_SDK_PROTO") == "1"
	sdk := os.NewFile(3, "sdk")
	id := 0
	call := func(lease string) {
		id++
		frame := fmt.Sprintf(`{"id":%d,"op":"kv.get","lease":%q,"function_id":"forged","worker_id":"forged","artifact_revision":"forged","phase":"execution","a":{"artifact_revision":"forged"}}`, id, lease)
		if err := writeFrame(sdk, []byte(frame)); err != nil {
			os.Exit(10)
		}
		if _, err := readFrame(sdk); err != nil {
			os.Exit(11)
		}
	}
	if channel {
		call("")
	}
	if err := writeFrame(os.Stdout, []byte(`{"type":"ready"}`)); err != nil {
		os.Exit(12)
	}
	lease := ""
	for {
		b, err := readFrame(os.Stdin)
		if err != nil {
			os.Exit(0)
		}
		var f struct{ Type, Lease string }
		if json.Unmarshal(b, &f) != nil {
			os.Exit(13)
		}
		switch f.Type {
		case "lease":
			lease = f.Lease
		case "request":
			if channel {
				call(lease)
			}
			if writeFrame(os.Stdout, []byte(`{"type":"response","statusCode":200}`)) != nil {
				os.Exit(14)
			}
		case "quit":
			os.Exit(0)
		}
	}
}

func TestWorkloadSpawnAndChannel(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"node/usr/local/bin", "node/opt/orva"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, text := range map[string]string{"node/usr/local/bin/node": "", "node/opt/orva/adapter.js": "// orva-sdk-proto:1"} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(text), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	jail := filepath.Join(root, "fake-jail")
	if err := os.WriteFile(jail, []byte(fmt.Sprintf("#!/bin/sh\nexec %q -test.run=^TestWorkloadFakeJail$ -- --workload-fake-jail \"$@\"\n", exe)), 0o755); err != nil {
		t.Fatal(err)
	}
	type captured struct {
		caller  SDKCaller
		release chan struct{}
	}
	calls := make(chan captured, 4)
	cfg := ExecConfig{FunctionID: ids.New(), ArtifactRevision: "trusted-artifact", Language: Node, RootfsDir: root, CodeDir: root, NsjailBin: jail,
		Env: map[string]string{"ORVA_FUNCTION_ID": "forged", "ARTIFACT_REVISION": "forged"},
		SDKHandler: func(ctx context.Context, c SDKCaller, _ string, _ json.RawMessage) (any, *SDKError) {
			release := make(chan struct{})
			calls <- captured{c, release}
			if c.Exec != nil {
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			return nil, nil
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	spawn := func(cfg ExecConfig) *Worker {
		t.Helper()
		w, err := Spawn(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = w.Kill()
			select {
			case <-w.waitDone:
			case <-time.After(5 * time.Second):
				t.Error("fake jail not reaped")
			}
		})
		return w
	}
	nextCall := func() captured {
		t.Helper()
		select {
		case got := <-calls:
			return got
		case <-ctx.Done():
			t.Fatal("no SDK host call")
			return captured{}
		}
	}
	w := spawn(cfg)
	module := nextCall().caller
	s := workloadState(t, module, WorkloadBootstrap)
	if s.Identity.FunctionID != cfg.FunctionID || s.Identity.ArtifactRevision != cfg.ArtifactRevision || !ids.IsUUID(s.Identity.WorkerID) {
		t.Fatalf("untrusted spawn identity: %+v", s)
	}
	for _, arg := range append(append([]string{}, w.Cmd.Args...), w.Cmd.Env...) {
		if strings.Contains(arg, s.Identity.WorkerID) || strings.Contains(arg, cfg.ArtifactRevision) {
			t.Fatal("host identity leaked to child")
		}
	}
	if err := w.AwaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	workloadState(t, module, WorkloadUnbound)
	for i := range 2 {
		done := make(chan error, 1)
		go func() {
			_, _, err := w.Dispatch(WithExecution(ctx, ExecInfo{ExecutionID: fmt.Sprint(i)}), []byte(`{}`))
			done <- err
		}()
		got := nextCall()
		active := workloadState(t, got.caller, WorkloadExecution)
		if active.Identity != s.Identity || active.ExecutionID != fmt.Sprint(i) || active.Lease == "" {
			t.Fatalf("dispatch identity changed: %+v", active)
		}
		workloadState(t, module, WorkloadUnbound)
		got.caller.FunctionID = "forged"
		*got.caller.Exec = ExecInfo{Lease: "forged", ExecutionID: "forged"}
		if after := workloadState(t, got.caller, WorkloadExecution); after != active {
			t.Fatal("exported caller fields changed host capture")
		}
		close(got.release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		workloadState(t, got.caller, WorkloadStaleLease)
	}
	cfg.SDKHandler, cfg.ArtifactRevision = nil, ""
	other := spawn(cfg)
	if err := other.AwaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	second, ok := other.WorkloadState()
	if !ok || second.Identity.WorkerID == s.Identity.WorkerID || second.Identity.FunctionID != s.Identity.FunctionID || second.Identity.ArtifactRevision != "" || len(other.Cmd.ExtraFiles) != 0 {
		t.Fatalf("channel-less identity/FD contract: %+v", second)
	}
	second.Identity.ArtifactRevision = "mutated-copy"
	if again, _ := other.WorkloadState(); again.Identity.ArtifactRevision != "" {
		t.Fatal("snapshot aliases host identity")
	}
	_ = w.Kill()
	if final, _ := w.WorkloadState(); !final.Ready || final.Alive || final.Identity != s.Identity {
		t.Fatalf("identity/readiness changed at kill: %+v", final)
	}
}
