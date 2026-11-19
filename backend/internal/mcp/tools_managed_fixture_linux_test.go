//go:build linux

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/proxy"
	"github.com/Harsh-2002/Orva/backend/internal/sdkauth"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func withManagedFixtureProxy(t *testing.T, f *iamSurfaceFixture, deps Deps) Deps {
	t.Helper()
	deps.ManagedAuthority = f.gate
	deps.PoolMgr = pool.NewManager(pool.ManagerConfig{ManagedAuthority: f.gate}, pool.SandboxTemplate{}, f.db, f.registry, nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := deps.PoolMgr.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	deps.Proxy = &proxy.Proxy{DB: f.db, Pool: deps.PoolMgr, SDKAuth: sdkauth.New(), Config: proxy.ProxyConfig{MaxBodyBytes: 1024}}
	return deps
}

func TestManagedFixtureRequiresBoundProxy(t *testing.T) {
	f := iamSurfaceSetup(t)
	deps := withManagedFixtureProxy(t, f, Deps{ManagedServer: true, DB: f.db, Registry: f.registry})
	otherGate, err := workloadiam.NewAuthority(8, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(*Deps, *proxy.Proxy)
	}{
		{"no database", func(d *Deps, _ *proxy.Proxy) { d.DB = nil }},
		{"no registry", func(d *Deps, _ *proxy.Proxy) { d.Registry = nil }},
		{"no authority", func(d *Deps, _ *proxy.Proxy) { d.ManagedAuthority = nil }},
		{"other authority", func(d *Deps, _ *proxy.Proxy) { d.ManagedAuthority = otherGate }},
		{"no proxy", func(d *Deps, _ *proxy.Proxy) { d.Proxy = nil }},
		{"other pool", func(_ *Deps, p *proxy.Proxy) { p.Pool = new(pool.Manager) }},
		{"legacy pool", func(d *Deps, p *proxy.Proxy) { d.PoolMgr = new(pool.Manager); p.Pool = d.PoolMgr }},
		{"no pool", func(d *Deps, p *proxy.Proxy) { d.PoolMgr = nil; p.Pool = nil }},
		{"no proxy database", func(_ *Deps, p *proxy.Proxy) { p.DB = nil }},
		{"no SDK auth", func(_ *Deps, p *proxy.Proxy) { p.SDKAuth = nil }},
		{"no body cap", func(_ *Deps, p *proxy.Proxy) { p.Config.MaxBodyBytes = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := deps
			d.Proxy = &proxy.Proxy{Pool: deps.PoolMgr, DB: f.db, SDKAuth: deps.Proxy.SDKAuth, Config: deps.Proxy.Config}
			tc.edit(&d, d.Proxy)
			if BuildAgentRegistry(d, permSet{permInvoke: true}).Get("test_function_with_fixture") != nil {
				t.Fatal("fixture tool registered without a bound managed proxy")
			}
			_, _, err := testFunctionWithFixture(t.Context(), d, TestFunctionWithFixtureInput{})
			if err == nil || err.Error() != "managed fixture invocation is unavailable" {
				t.Fatalf("dispatch failed to refuse missing managed dependencies: %v", err)
			}
		})
	}
}

func TestManagedFixtureDispatchRefusals(t *testing.T) {
	f := iamSurfaceSetup(t)
	deps := withManagedFixtureProxy(t, f, Deps{ManagedServer: true, DB: f.db, Registry: f.registry})
	const toolName = "test_function_with_fixture"
	reg := BuildAgentRegistry(deps, permSet{permInvoke: true})
	tool := reg.Get(toolName)
	if tool == nil || tool.Perm != permInvoke || tool.ReadOnly || tool.Destructive || reg.Get("invoke_function") != nil {
		t.Fatal("managed fixture tool changed the invoke tier/Chat metadata or exposed generic invoke")
	}
	for _, perm := range []string{permRead, permWrite, permAdmin} {
		if BuildAgentRegistry(deps, permSet{perm: true}).Get(toolName) != nil {
			t.Fatalf("fixture invocation leaked to %s-only key", perm)
		}
	}
	keyID := ids.New()
	seedStatelessAPIKey(t, f.db, "orva_fixture_invoke_test", keyID, "invoke", `["invoke"]`)
	invokeCtx := auth.WithOperator(t.Context(), &auth.Principal{Kind: auth.KindAPIKey, ID: keyID})
	fix := &database.Fixture{FunctionID: f.owners[0], Name: "retained", Method: "POST", Path: "/", Body: []byte("fixture body")}
	if err := f.db.InsertFixture(fix); err != nil {
		t.Fatal(err)
	}
	args := func(owner string, override FixtureOverride) json.RawMessage {
		t.Helper()
		raw, err := json.Marshal(TestFunctionWithFixtureInput{FunctionID: owner, FixtureName: fix.Name, Override: override})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	ownerArgs := args(fix.FunctionID, FixtureOverride{})
	for _, ctx := range []context.Context{t.Context(), f.ctx} {
		if _, err := reg.Dispatch(ctx, toolName, ownerArgs); !errors.Is(err, database.ErrOperatorDenied) {
			t.Fatalf("absent or under-scoped operator was not denied: %v", err)
		}
	}
	for _, tc := range []struct {
		name, owner, want string
		override          FixtureOverride
	}{
		{"different owner", f.owners[1], "fixture not found", FixtureOverride{}},
		{"body cap", fix.FunctionID, "request body exceeds", FixtureOverride{Body: strings.Repeat("a", 1025)}},
		{"uninstalled owner", fix.FunctionID, pool.ErrNoFunction.Error(), FixtureOverride{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := reg.Dispatch(invokeCtx, toolName, args(tc.owner, tc.override)); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wanted %q refusal, got %v", tc.want, err)
			}
		})
	}
	if err := f.db.DeleteAPIKey(keyID); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Dispatch(invokeCtx, toolName, ownerArgs); !errors.Is(err, database.ErrOperatorDenied) {
		t.Fatalf("registered tool accepted revoked operator: %v", err)
	}
}
