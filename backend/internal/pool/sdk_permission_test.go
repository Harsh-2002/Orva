package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/sdkauth"
)

func TestSDKPermissionSpawnGate(t *testing.T) {
	for _, network := range []string{"none", "egress"} {
		for _, enabled := range []bool{false, true} {
			for _, marker := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/sdk=%v/marker=%v", network, enabled, marker), func(t *testing.T) {
					m, reg := egressTestManager(t)
					tmpl := fakeSandboxTemplate(t)
					if network == "egress" {
						policyPath := filepath.Join(t.TempDir(), "egress-sdk-test.cfg")
						if err := os.WriteFile(policyPath, []byte(`mount_proc: true
user_net {
  backend: NSTUN
  ip4: "10.0.0.2"
  gw4: "10.0.0.1"
  ip6: "fd00::2"
  gw6: "fd00::1"
  ns_iface: "eth0"
  rule4 {
    direction: GUEST_TO_HOST
    action: REJECT
    proto: ANY
    dst_ip: "169.254.169.254/32"
    redirect_ip: "0.0.0.0"
  }
}
`), 0o600); err != nil {
							t.Fatal(err)
						}
						tmpl.EgressPolicy = func() (string, string, error) { return policyPath, "sdk-test", nil }
					}
					if marker {
						adapter := filepath.Join(tmpl.RootfsDir, "node/opt/orva/adapter.js")
						if err := os.WriteFile(adapter, []byte("// orva-sdk-proto:1\n"), 0o644); err != nil {
							t.Fatal(err)
						}
					}
					capture := filepath.Join(t.TempDir(), "config")
					request := `{"id":1,"op":"kv.get","a":{"key":"test"}}`
					script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$@" > '%s'
printf 'token=%%s\nbase=%%s\nfd=%%s\nproto=%%s\n' "$ORVA_INTERNAL_TOKEN" "$ORVA_API_BASE" "$ORVA_SDK_FD" "$ORVA_SDK_PROTO" >> '%s'
if [ "$ORVA_SDK_FD" = 3 ]; then
  printf '\000\000\000\%03o%s' >&3
fi
printf '\000\000\000\020{"type":"ready"}'
cat >/dev/null
`, capture, capture, len(request), request)
					if err := os.WriteFile(tmpl.NsjailBin, []byte(script), 0o755); err != nil {
						t.Fatal(err)
					}
					var minted, released atomic.Int32
					tmpl.SDKToken = func(string) (string, func()) {
						minted.Add(1)
						return "test-token", func() { released.Add(1) }
					}
					called := make(chan sandbox.SDKCaller, 1)
					tmpl.SDKHandler = func(ctx context.Context, caller sandbox.SDKCaller, op string, args json.RawMessage) (any, *sandbox.SDKError) {
						called <- caller
						return nil, nil
					}
					tmpl.APIBaseURL = "http://test-control-plane"
					injected := map[string]string{
						"ORVA_INTERNAL_TOKEN": "fake-token", "ORVA_API_BASE": "fake-base",
						"ORVA_SDK_FD": "3", "ORVA_SDK_PROTO": "1", "orva_sdk_fd": "3",
						"ORVA_INTERNAL_TOKEN=fake-token": "ignored", "ORVA_API_BASE=fake-base": "ignored",
						"ORVA_SDK_FD=3": "ignored", "ORVA_SDK_PROTO=1": "ignored",
					}
					tmpl.SecretsLookup = func(string) map[string]string { return injected }
					m.tmpl = tmpl
					fn := registerFn(t, reg, "sdk-gate", network)
					fn.SDK, fn.EnvVars = enabled, injected
					if err := reg.Set(fn); err != nil {
						t.Fatal(err)
					}
					p, err := m.getOrCreatePool(fn.ID)
					if err != nil {
						t.Fatal(err)
					}
					w, err := p.spawnFn(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = w.Kill() })
					data, err := os.ReadFile(capture)
					if err != nil {
						t.Fatal(err)
					}
					config := string(data)
					if strings.Contains(config, "fake-") || strings.Contains(config, "orva_sdk_fd") {
						t.Fatalf("user injection survived: %s", config)
					}
					if !enabled {
						for _, reserved := range []string{"ORVA_INTERNAL_TOKEN", "ORVA_API_BASE", "ORVA_SDK_FD", "ORVA_SDK_PROTO", "--pass_fd"} {
							if strings.Contains(config, reserved) {
								t.Errorf("disabled SDK exposes %s", reserved)
							}
						}
						if minted.Load() != 0 {
							t.Fatal("disabled SDK minted a credential")
						}
					} else if minted.Load() != 1 || !strings.Contains(config, "token=test-token") || !strings.Contains(config, "base=http://test-control-plane") {
						t.Fatalf("enabled SDK lost HTTP fallback: %s", config)
					}
					if enabled && marker {
						if !strings.Contains(config, "--pass_fd\n3\n") {
							t.Fatal("SDK channel descriptor not passed")
						}
						select {
						case caller := <-called:
							if caller.FunctionID != fn.ID {
								t.Fatalf("host caller identity: %+v", caller)
							}
						case <-time.After(2 * time.Second):
							t.Fatal("enabled SDK did not reach host handler")
						}
					} else if strings.Contains(config, "--pass_fd") {
						t.Fatal("disabled or legacy worker got a private channel")
					}
					_ = w.Kill()
					deadline := time.Now().Add(2 * time.Second)
					for released.Load() != minted.Load() && time.Now().Before(deadline) {
						time.Sleep(time.Millisecond)
					}
					if released.Load() != minted.Load() {
						t.Fatal("reaper did not revoke worker credential")
					}
				})
			}
		}
	}
}

func TestSDKPermissionReservedEnvironment(t *testing.T) {
	env := map[string]string{"ORVA_SIGNING_SECRET": "keep", "ORVA_API_BASE_URL": "keep", "CUSTOM": "keep"}
	for _, name := range []string{"ORVA_INTERNAL_TOKEN", "ORVA_API_BASE", "ORVA_SDK_FD", "ORVA_SDK_PROTO"} {
		for _, spelling := range []string{name, strings.ToLower(name), "Orva_" + name[5:], name + "=injected"} {
			env[spelling] = "remove"
		}
	}
	sanitizeSDKEnv(env)
	if len(env) != 3 {
		t.Fatalf("reserved names survived: %v", env)
	}
	for name, value := range env {
		if value != "keep" {
			t.Errorf("unexpected environment entry %s", name)
		}
	}
}

func TestSDKPermissionToggleRetiresBusyAndIdleCredentials(t *testing.T) {
	m, reg := egressTestManager(t)
	tmpl := fakeSandboxTemplate(t)
	adapter := filepath.Join(tmpl.RootfsDir, "node/opt/orva/adapter.js")
	if err := os.WriteFile(adapter, []byte("// orva-sdk-proto:1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	auth := sdkauth.New([]byte("test-sdk-permission-key"))
	tmpl.SDKToken = auth.Mint
	tmpl.SDKHandler = func(context.Context, sandbox.SDKCaller, string, json.RawMessage) (any, *sandbox.SDKError) {
		return nil, nil
	}
	m.tmpl = tmpl
	fn := registerFn(t, reg, "toggle-sdk", "none")
	fn.SDK = true
	if err := reg.Set(fn); err != nil {
		t.Fatal(err)
	}
	tokenOf := func(w *sandbox.Worker) string {
		for _, entry := range w.Cmd.Env {
			if token, ok := strings.CutPrefix(entry, "ORVA_INTERNAL_TOKEN="); ok {
				return token
			}
		}
		return ""
	}
	waitRevoked := func(w *sandbox.Worker, token string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := auth.Verify(token); err != nil && w.IsDead() {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("retired worker retained channel process or valid HTTP credential")
	}
	for _, nextSDK := range []bool{false, true, false} {
		busy, err := m.Acquire(context.Background(), fn.ID)
		if err != nil {
			t.Fatal(err)
		}
		idle, err := m.Acquire(context.Background(), fn.ID)
		if err != nil {
			m.Release(busy, err)
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = busy.Worker.Kill(); _ = idle.Worker.Kill() })
		old := busy.pool
		m.Release(idle, nil)
		if len(old.idle) != 1 || old.busy.Load() != 1 {
			t.Fatal("test requires one busy and one idle worker")
		}
		busyToken, idleToken := tokenOf(busy.Worker), tokenOf(idle.Worker)
		if fn.SDK {
			if _, err := auth.Verify(busyToken); err != nil {
				t.Fatalf("live worker lacks valid SDK credential: %v", err)
			}
		} else if busyToken != "" || idleToken != "" {
			t.Fatal("SDK-disabled generation has credentials")
		}
		fn.SDK = nextSDK
		if err := reg.Set(fn); err != nil {
			t.Fatal(err)
		}
		m.Drain(fn.ID)
		if !old.closing.Load() || len(old.idle) != 0 || !idle.Worker.IsDead() {
			t.Fatal("toggle did not evict idle generation")
		}
		if _, exists := m.pools.Load(fn.ID); exists {
			t.Fatal("retired generation remains available for new invocations")
		}
		waitRevoked(idle.Worker, idleToken)
		if busy.Worker.IsDead() {
			t.Fatal("hard drain interrupted the already-running invocation")
		}
		replacement, err := m.Acquire(context.Background(), fn.ID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = replacement.Worker.Kill() })
		if replacement.pool == old {
			t.Fatal("replacement reused old generation")
		}
		hasChannel := strings.Contains(strings.Join(replacement.Worker.Cmd.Args, " "), "--pass_fd 3")
		if hasChannel != nextSDK || (tokenOf(replacement.Worker) != "") != nextSDK {
			t.Fatalf("replacement authority does not match sdk=%v", nextSDK)
		}
		m.Release(busy, nil)
		waitRevoked(busy.Worker, busyToken)
		if old.busy.Load() != 0 || len(old.idle) != 0 {
			t.Fatal("busy worker returned to retired generation")
		}
		m.Release(replacement, nil)
	}
	m.Drain(fn.ID)
}
