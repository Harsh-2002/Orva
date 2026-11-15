package pool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
)

func TestSDKSpawnFailsClosedWithoutEgressPolicy(t *testing.T) {
	m, reg := egressTestManager(t)
	tmpl := fakeSandboxTemplate(t)
	tmpl.EgressPolicy = func() (string, string, error) { return "", "", errTestNoPolicy }
	m.tmpl = tmpl
	fn := registerFn(t, reg, "sdk-egress", "egress")
	fn.SDK = true
	if err := reg.Set(fn); err != nil {
		t.Fatal(err)
	}
	p, err := m.getOrCreatePool(fn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.spawnFn(context.Background()); !errors.Is(err, errTestNoPolicy) {
		t.Fatalf("spawn without policy = %v", err)
	}
}

func TestSDKSpawnRequiresChannelHost(t *testing.T) {
	m, reg := egressTestManager(t)
	m.tmpl = fakeSandboxTemplate(t)
	fn := registerFn(t, reg, "sdk-no-host", "none")
	fn.SDK = true
	if err := reg.Set(fn); err != nil {
		t.Fatal(err)
	}
	p, err := m.getOrCreatePool(fn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.spawnFn(context.Background()); err == nil {
		t.Fatal("SDK-enabled worker started without a channel host")
	}
}

func TestRetiredWorkerCancelsItsChannelWithoutInterruptingBusyWorker(t *testing.T) {
	for _, idle := range []bool{false, true} {
		t.Run(map[bool]string{false: "busy", true: "idle"}[idle], func(t *testing.T) {
			m, reg := egressTestManager(t)
			tmpl := fakeSandboxTemplate(t)
			if err := os.WriteFile(filepath.Join(tmpl.RootfsDir, "node/opt/orva/adapter.js"), []byte("// orva-sdk-proto:1\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			const shim = `#!/usr/bin/env python3
import json, os, struct, sys
request = json.dumps({"id": 1, "op": "kv.get", "a": {"key": "hold"}}).encode()
os.write(3, struct.pack(">I", len(request)) + request)
ready = b'{"type":"ready"}'
sys.stdout.buffer.write(struct.pack(">I", len(ready)) + ready)
sys.stdout.buffer.flush()
sys.stdin.buffer.read()
`
			if err := os.WriteFile(tmpl.NsjailBin, []byte(shim), 0o755); err != nil {
				t.Fatal(err)
			}
			started := make(chan context.Context, 1)
			ended := make(chan error, 1)
			tmpl.SDKHandler = func(ctx context.Context, caller sandbox.SDKCaller, op string, _ json.RawMessage) (any, *sandbox.SDKError) {
				started <- ctx
				<-ctx.Done()
				ended <- ctx.Err()
				return nil, nil
			}
			m.tmpl = tmpl
			fn := registerFn(t, reg, "channel-lifetime", "none")
			fn.SDK = true
			if err := reg.Set(fn); err != nil {
				t.Fatal(err)
			}
			acq, err := m.Acquire(context.Background(), fn.ID)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = acq.Worker.Kill() })
			var channelCtx context.Context
			select {
			case channelCtx = <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("worker did not reach its private channel host")
			}
			if idle {
				m.Release(acq, nil)
			}
			m.Drain(fn.ID)
			if !idle {
				if channelCtx.Err() != nil || acq.Worker.IsDead() {
					t.Fatal("retiring the busy generation interrupted its live channel")
				}
				m.Release(acq, nil)
			}
			select {
			case err := <-ended:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("channel cancellation = %v", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("retired worker's SDK handler outlived its channel")
			}
		})
	}
}
