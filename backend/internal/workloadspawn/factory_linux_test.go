//go:build linux

package workloadspawn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func factoryLimits() FactoryLimits {
	return FactoryLimits{MaxPreparedPlans: 4, MaxPreparedBytes: 16 << 20, MaxSnapshotBytes: 4 << 20}
}

func fixtureFactory(t *testing.T, f *fixture, limits FactoryLimits) *Factory {
	t.Helper()
	factory, err := NewFactory(t.Context(), f.prepared, f.store, f.codeID, f.store, f.runtimeID, limits)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(factory.Close)
	f.paths.Release()
	return factory
}

func factoryPlan(t *testing.T, f *Factory) *SpawnPlan {
	t.Helper()
	plan, err := f.Prepare(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(plan.Close)
	return plan
}

func TestFactoryFrozenInputsAndResources(t *testing.T) {
	for _, sdk := range []bool{false, true} {
		t.Run(fmt.Sprint(sdk), func(t *testing.T) {
			f := newFixture(t, "node")
			f.prepared.Function.SDK = sdk
			f.prepared.Function.MaxConcurrency = 3
			f.prepared.Function.RateLimitPerMin = 17
			f.prepared.Function.ConcurrencyPolicy = database.ConcurrencyPolicyReject
			f.prepared.Function.EnvVars["ORVA_MEMORY_MB"] = "1"
			f.prepared.Secrets["ORVA_TIMEOUT_MS"] = "999"
			f.prepared.Secrets["orva_sdk_proto"] = "untrusted"
			factory := fixtureFactory(t, f, factoryLimits())
			copy := *factory
			facts := factory.Resources()
			want := ResourceFacts{MemoryMB: 96, CPUs: 0.5, Timeout: 1234 * time.Millisecond,
				MaxConcurrency: 3, RateLimitPerMin: 17, ConcurrencyPolicy: database.ConcurrencyPolicyReject,
				NetworkMode: "none", SDK: sdk}
			if facts != want || factory.Owner() != f.prepared.Function.ID {
				t.Fatal("resource/owner capture mismatch")
			}
			first := factoryPlan(t, factory)
			originalArtifact, generation := first.ArtifactRevision(), first.Generation()
			f.prepared.Function.EnvVars["CONFIG"] = "new-generation"
			f.prepared.Function.EnvVars["EXTRA"] = "new-generation"
			f.prepared.Secrets["OVERLAY"] = "new-generation"
			f.prepared.Secrets["EXTRA_SECRET"] = "new-generation"
			f.prepared.Function.ID, f.prepared.Function.Name = ids.New(), "changed"
			f.prepared.Function.Runtime, f.prepared.Function.MemoryMB = "unsupported", 1
			f.prepared.Function.CPUs, f.prepared.Function.TimeoutMS = 99, 99
			f.prepared.Function.SDK, f.prepared.Function.NetworkMode = !sdk, "egress"
			f.prepared.Function.MaxConcurrency = 999
			f.prepared.Artifact.Generation, f.prepared.Artifact.Revision = ids.New(), 999
			second := factoryPlan(t, &copy)
			if second.ArtifactRevision() != originalArtifact || factory.ArtifactRevision() != originalArtifact ||
				second.Generation() != generation || factory.Generation() != generation || factory.Resources() != want {
				t.Fatal("caller mutation mixed generation/configuration")
			}
			code, runtime := factory.Identities()
			actualCode, actualRuntime := second.state.input.paths.Identities()
			if code != actualCode || runtime != actualRuntime || code.Generation != f.codeID || runtime.Generation != f.runtimeID {
				t.Fatal("factory changed concrete artifacts")
			}
			for _, plan := range []*SpawnPlan{first, second} {
				fn := plan.state.input.fn
				if fn.MemoryMB != facts.MemoryMB || fn.CPUs != facts.CPUs || time.Duration(fn.TimeoutMS)*time.Millisecond != facts.Timeout ||
					fn.MaxConcurrency != facts.MaxConcurrency || fn.RateLimitPerMin != facts.RateLimitPerMin ||
					fn.ConcurrencyPolicy != facts.ConcurrencyPolicy || fn.NetworkMode != facts.NetworkMode || fn.SDK != facts.SDK {
					t.Fatal("resource descriptor diverged from spawn function")
				}
				worker, err := plan.Spawn(t.Context(), sandbox.ExecConfig{
					NsjailBin: fakeJail(t), MemoryMB: 1, MaxCPUs: 99, Timeout: time.Hour,
					NetworkMode: "egress", FunctionID: "forged", ArtifactRevision: "forged",
					SDKHandler: func(context.Context, sandbox.SDKCaller, string, json.RawMessage) (any, *sandbox.SDKError) {
						return map[string]string{"value": "fixture"}, nil
					},
				})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = worker.Kill() })
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				if err := worker.AwaitReady(ctx); err != nil {
					t.Fatal(err)
				}
				result, err := worker.DispatchEx(ctx, []byte(`{}`))
				if err != nil {
					t.Fatal(err)
				}
				var reply jailReply
				if err := json.Unmarshal([]byte(result.Body), &reply); err != nil {
					t.Fatal(err)
				}
				for key, want := range map[string]string{"CONFIG": "original", "OVERLAY": dummySecret,
					"ORVA_FUNCTION_ID": factory.Owner(), "ORVA_FUNCTION_NAME": "frozen-name",
					"ORVA_MEMORY_MB": strconv.FormatInt(facts.MemoryMB, 10), "ORVA_TIMEOUT_MS": "1234"} {
					if reply.Env[key] != want {
						t.Error("wrong frozen environment at", key)
					}
				}
				if reply.Env["EXTRA"] != "" || reply.Env["EXTRA_SECRET"] != "" ||
					(reply.Env["ORVA_SDK_PROTO"] == "1") != facts.SDK || strings.Contains(strings.Join(reply.Args, "\n"), "--user_net") {
					t.Error("new input/template data leaked into frozen worker")
				}
				state, ok := worker.WorkloadState()
				if !ok || state.Identity.ArtifactRevision != originalArtifact || state.Identity.FunctionID != factory.Owner() {
					t.Error("worker lost factory identity")
				}
				if err := worker.Kill(); err != nil {
					t.Fatal(err)
				}
			}
			if usage := factory.Usage(); usage.Plans != 0 || usage.Bytes != 0 {
				t.Fatal("spawn failed to return prepared capacity")
			}
			for _, format := range []string{"%v", "%+v", "%#v"} {
				if strings.Contains(fmt.Sprintf(format, factory), dummySecret) {
					t.Fatal("factory formatting exposed secret")
				}
			}
			raw, err := json.Marshal(factory)
			if err != nil || strings.Contains(string(raw), dummySecret) {
				t.Fatal("factory JSON exposed secret")
			}
		})
	}
}

func TestFactoryFingerprintBindings(t *testing.T) {
	for _, change := range []string{"same", "cosmetic version", "code", "runtime", "generation", "revision", "epoch"} {
		t.Run(change, func(t *testing.T) {
			f := newFixture(t, "node")
			base := fixtureFactory(t, f, factoryLimits())
			prepared := *f.prepared
			prepared.Function = f.prepared.Function.Clone()
			code, runtime := f.codeID, f.runtimeID
			switch change {
			case "cosmetic version":
				prepared.Function.Version++
			case "code":
				code = f.extraGeneration(t, artifactfs.KindCode, prepared.Function.ID, "node", "different code")
			case "runtime":
				runtime = f.extraGeneration(t, artifactfs.KindRuntime, "node", "node", "different runtime")
			case "generation":
				prepared.Artifact.Generation = ids.New()
			case "revision":
				prepared.Artifact.Revision++
			case "epoch":
				prepared.Artifact.Epoch++
			}
			other, err := NewFactory(t.Context(), &prepared, f.store, code, f.store, runtime, factoryLimits())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(other.Close)
			same := change == "same" || change == "cosmetic version"
			if (base.ArtifactRevision() == other.ArtifactRevision()) != same {
				t.Fatal("factory changed v2 fingerprint semantics")
			}
			for _, factory := range []*Factory{base, other} {
				plan := factoryPlan(t, factory)
				if plan.ArtifactRevision() != factory.ArtifactRevision() || plan.Generation() != factory.Generation() {
					t.Fatal("issued plan fingerprint changed")
				}
			}
		})
	}
}

func TestFactoryLimitsAndRelease(t *testing.T) {
	for _, byBytes := range []bool{false, true} {
		t.Run(fmt.Sprint(byBytes), func(t *testing.T) {
			f := newFixture(t, "node")
			limits := factoryLimits()
			cost, err := snapshotCost(f.prepared, limits.MaxSnapshotBytes)
			if err != nil {
				t.Fatal(err)
			}
			if byBytes {
				limits.MaxPreparedBytes = cost
			} else {
				limits.MaxPreparedPlans = 1
			}
			factory := fixtureFactory(t, f, limits)
			plan := factoryPlan(t, factory)
			if usage := factory.Usage(); usage != (PreparedUsage{Plans: 1, Bytes: cost, PlanBytes: cost}) {
				t.Fatal("reservation not held")
			}
			if p, err := factory.Prepare(t.Context()); p != nil || !errors.Is(err, ErrFactoryCapacity) {
				t.Fatal("prepared quota bypassed")
			}
			copied := *plan
			var wg sync.WaitGroup
			for range 16 {
				wg.Add(1)
				go func() { defer wg.Done(); copied.Close(); plan.Close() }()
			}
			wg.Wait()
			if usage := factory.Usage(); usage.Plans != 0 || usage.Bytes != 0 {
				t.Fatal("copied Close returned reservation incorrectly")
			}
			plan = factoryPlan(t, factory)
			if _, err := plan.Spawn(t.Context(), sandbox.ExecConfig{NsjailBin: "/does-not-exist"}); !errors.Is(err, ErrSpawn) {
				t.Fatal(err)
			}
			plan.Close()
			if usage := factory.Usage(); usage.Plans != 0 || usage.Bytes != 0 {
				t.Fatal("failed spawn leaked capacity")
			}
			factoryPlan(t, factory).Close()
		})
	}
}

// Err pauses after a chosen check; no production hook or altered spawn API.
type factoryPauseContext struct {
	context.Context
	checks          atomic.Int32
	at              int32
	entered, resume chan struct{}
	err             error
}

func (c *factoryPauseContext) Err() error {
	if c.checks.Add(1) == c.at {
		close(c.entered)
		<-c.resume
	}
	return c.err
}

func pauseFactoryContext(at int32) *factoryPauseContext {
	return &factoryPauseContext{Context: context.Background(), at: at, entered: make(chan struct{}), resume: make(chan struct{})}
}

func waitFactoryPause(t *testing.T, ctx *factoryPauseContext) {
	t.Helper()
	select {
	case <-ctx.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("operation did not reach pause")
	}
}

func TestFactoryStartingReservation(t *testing.T) {
	f := newFixture(t, "node")
	limits := factoryLimits()
	limits.MaxPreparedPlans = 1
	factory := fixtureFactory(t, f, limits)
	plan := factoryPlan(t, factory)
	ctx := pauseFactoryContext(1)
	var once sync.Once
	resume := func() { once.Do(func() { close(ctx.resume) }) }
	t.Cleanup(resume)
	template := sandbox.ExecConfig{NsjailBin: fakeJail(t)}
	type outcome struct {
		worker *sandbox.Worker
		err    error
	}
	done := make(chan outcome, 1)
	go func() { w, err := plan.Spawn(ctx, template); done <- outcome{w, err} }()
	waitFactoryPause(t, ctx)
	plan.Close()
	if usage := factory.Usage(); usage.Plans != 1 || usage.Bytes != usage.PlanBytes {
		t.Fatal("Take/Close released starting capacity")
	}
	if p, err := factory.Prepare(t.Context()); p != nil || !errors.Is(err, ErrFactoryCapacity) {
		t.Fatal("blocked Spawn permitted unbounded preparation")
	}
	resume()
	result := <-done
	if result.err != nil {
		t.Fatal(result.err)
	}
	t.Cleanup(func() { _ = result.worker.Kill() })
	if usage := factory.Usage(); usage.Plans != 0 || usage.Bytes != 0 {
		t.Fatal("returning Spawn retained reservation")
	}
	// Live workers require a separate future pool budget.
	factoryPlan(t, factory).Close()
	if err := result.worker.Kill(); err != nil {
		t.Fatal(err)
	}
}

func TestFactoryCancelledStartingReservation(t *testing.T) {
	f := newFixture(t, "node")
	factory := fixtureFactory(t, f, factoryLimits())
	plan := factoryPlan(t, factory)
	ctx := pauseFactoryContext(1)
	var once sync.Once
	resume := func() { once.Do(func() { close(ctx.resume) }) }
	t.Cleanup(resume)
	done := make(chan error, 1)
	go func() {
		worker, err := plan.Spawn(ctx, sandbox.ExecConfig{NsjailBin: "/must-not-start"})
		if worker != nil {
			_ = worker.Kill()
			t.Error("cancelled plan started a worker")
		}
		done <- err
	}()
	waitFactoryPause(t, ctx)
	factory.Close()
	plan.Close()
	if usage := factory.Usage(); usage.Plans != 1 {
		t.Fatal("close released a starting reservation")
	}
	ctx.err = context.Canceled
	resume()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if usage := factory.Usage(); usage.Plans != 0 || usage.Bytes != 0 {
		t.Fatal("cancelled Spawn leaked prepared quota")
	}
	f.reclaim(t)
}

func TestFactoryRetireDuringPrepare(t *testing.T) {
	f := newFixture(t, "node")
	factory := fixtureFactory(t, f, factoryLimits())
	ctx := pauseFactoryContext(2)
	var once sync.Once
	resume := func() { once.Do(func() { close(ctx.resume) }) }
	t.Cleanup(resume)
	done := make(chan error, 1)
	go func() {
		plan, err := factory.Prepare(ctx)
		if plan != nil {
			plan.Close()
			t.Error("preparation completed after losing retirement boundary")
		}
		done <- err
	}()
	waitFactoryPause(t, ctx)
	if usage := factory.Usage(); usage.Plans != 1 {
		t.Fatal("preparation not reserved before acquire/clone")
	}
	copy := *factory
	copy.Retire()
	if !factory.Retired() {
		t.Fatal("retirement not shared")
	}
	if p, err := factory.Prepare(t.Context()); p != nil || !errors.Is(err, ErrFactoryRetired) {
		t.Fatal("retired factory admitted preparation")
	}
	resume()
	if err := <-done; !errors.Is(err, ErrFactoryRetired) {
		t.Fatal(err)
	}
	if usage := factory.Usage(); usage.Plans != 0 || usage.Bytes != 0 {
		t.Fatal("lost preparation leaked capacity")
	}
	f.reclaim(t)
}

func TestFactoryRetireKeepsIssuedAndWorkerLeases(t *testing.T) {
	f := newFixture(t, "node")
	factory := fixtureFactory(t, f, factoryLimits())
	plan := factoryPlan(t, factory)
	factory.Close()
	factory.Close()
	if usage := factory.Usage(); usage.Plans != 1 {
		t.Fatal("factory closed caller-owned plan")
	}
	reaped, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(resume) }) }
	t.Cleanup(unblock)
	worker, err := plan.Spawn(t.Context(), sandbox.ExecConfig{
		NsjailBin: fakeJail(t), OnExit: func() { close(reaped); <-resume },
	})
	if err != nil {
		t.Fatal("issued plan lost ownership on retirement:", err)
	}
	t.Cleanup(func() { unblock(); _ = worker.Kill() })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := worker.AwaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{f.codeID, f.runtimeID} {
		if err := f.store.Retire(id); err != nil {
			t.Fatal(err)
		}
		if err := f.store.Reclaim(id); !errors.Is(err, artifactfs.ErrBusy) {
			t.Fatal("factory/plan released worker lease")
		}
	}
	if usage := factory.Usage(); usage.Plans != 0 || usage.Bytes != 0 {
		t.Fatal("active worker charged as prepared plan")
	}
	if _, err := worker.DispatchEx(ctx, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := worker.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reaped:
	case <-ctx.Done():
		t.Fatal("no post-Wait callback")
	}
	for _, id := range []string{f.codeID, f.runtimeID} {
		if err := f.store.Reclaim(id); !errors.Is(err, artifactfs.ErrBusy) {
			t.Fatal("factory lease escaped post-Wait lifetime")
		}
	}
	unblock()
	f.reclaim(t)
}

func TestFactoryPartialAcquireAndConstructorRollback(t *testing.T) {
	for _, constructor := range []bool{false, true} {
		t.Run(fmt.Sprint(constructor), func(t *testing.T) {
			f := newFixture(t, "node")
			if constructor {
				f.paths.Release()
				factory, err := NewFactory(t.Context(), f.prepared, f.store, f.codeID, f.store, ids.New(), factoryLimits())
				if err == nil || factory != nil {
					t.Fatal("unknown runtime acquired")
				}
			} else {
				factory := fixtureFactory(t, f, factoryLimits())
				if err := f.store.Retire(f.runtimeID); err != nil {
					t.Fatal(err)
				}
				if p, err := factory.Prepare(t.Context()); p != nil || !errors.Is(err, artifactfs.ErrRetired) {
					t.Fatal("retired runtime acquired")
				}
				if usage := factory.Usage(); usage.Plans != 0 || usage.Bytes != 0 {
					t.Fatal("partial acquire leaked prepared quota")
				}
				factory.Close()
			}
			f.reclaim(t)
		})
	}
}

func TestFactoryCancelledReservationRollback(t *testing.T) {
	f := newFixture(t, "node")
	factory := fixtureFactory(t, f, factoryLimits())
	ctx := pauseFactoryContext(2)
	var once sync.Once
	resume := func() { once.Do(func() { close(ctx.resume) }) }
	t.Cleanup(resume)
	done := make(chan error, 1)
	go func() {
		plan, err := factory.Prepare(ctx)
		if plan != nil {
			plan.Close()
			t.Error("cancelled preparation returned a plan")
		}
		done <- err
	}()
	waitFactoryPause(t, ctx)
	if usage := factory.Usage(); usage.Plans != 1 {
		t.Fatal("cancelled preparation was not reserved")
	}
	ctx.err = context.Canceled
	resume()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if usage := factory.Usage(); usage.Plans != 0 || usage.Bytes != 0 {
		t.Fatal("cancelled preparation leaked quota")
	}
	factoryPlan(t, factory).Close()
}

func TestFactoryConstructorBoundsAndStrictValidation(t *testing.T) {
	f := newFixture(t, "node")
	for _, which := range []string{"zero limits", "snapshot bytes", "prepared bytes", "bad owner", "bad version", "bad environment", "nil context", "cancelled"} {
		t.Run(which, func(t *testing.T) {
			p := *f.prepared
			p.Function = f.prepared.Function.Clone()
			limits := factoryLimits()
			ctx := t.Context()
			switch which {
			case "zero limits":
				limits.MaxPreparedPlans = 0
			case "snapshot bytes":
				limits.MaxSnapshotBytes = 4096
			case "prepared bytes":
				limits.MaxPreparedBytes = 1
			case "bad owner":
				p.Artifact.OwnerID = ids.New()
			case "bad version":
				p.Function.Version = 0
			case "bad environment":
				p.Function.EnvVars["bad=name"] = "invalid"
			case "nil context":
				ctx = nil
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			}
			factory, err := NewFactory(ctx, &p, f.store, f.codeID, f.store, f.runtimeID, limits)
			if factory != nil || err == nil {
				t.Fatal("invalid factory accepted")
			}
			if which == "snapshot bytes" || which == "prepared bytes" {
				if !errors.Is(err, ErrFactoryCapacity) {
					t.Fatal(err)
				}
			}
		})
	}
	f.paths.Release()
	f.reclaim(t)
}

func TestFactoryOwnedStringCoverage(t *testing.T) {
	fn := &database.Function{EnvVars: map[string]string{"key": "value"}}
	value := reflect.ValueOf(fn).Elem()
	want := map[string]bool{}
	for i := range value.NumField() {
		switch value.Field(i).Kind() {
		case reflect.Map:
			if value.Type().Field(i).Name != "EnvVars" || value.Field(i).Type() != reflect.TypeFor[map[string]string]() {
				t.Fatal("new function reference field needs explicit factory ownership")
			}
		case reflect.Pointer, reflect.Slice, reflect.Interface, reflect.Chan, reflect.Func:
			t.Fatal("new function reference field needs explicit factory ownership")
		}
		if value.Field(i).Kind() == reflect.String {
			name := value.Type().Field(i).Name
			value.Field(i).SetString(strings.Repeat(name, 2))
			want[strings.Repeat(name, 2)] = true
		}
	}
	for _, text := range functionStrings(fn) {
		if !want[text] {
			t.Fatal("function string accounting duplicated/omitted a field")
		}
		delete(want, text)
	}
	if len(want) != 0 {
		t.Fatal("new function strings missing from accounting/ownership")
	}
	owned := ownFunction(fn)
	originalText, ownedText := functionStrings(fn), functionStrings(owned)
	for i, text := range originalText {
		if ownedText[i] != text || unsafe.StringData(ownedText[i]) == unsafe.StringData(text) {
			t.Fatal("factory retained caller string backing storage")
		}
	}
	for key, val := range owned.EnvVars {
		if unsafe.StringData(key) == unsafe.StringData("key") || unsafe.StringData(val) == unsafe.StringData("value") {
			t.Fatal("factory retained caller map string backing")
		}
	}
}

func TestFactoryConcurrentPrepareRetireConsume(t *testing.T) {
	for iteration := range 8 {
		t.Run(strconv.Itoa(iteration), func(t *testing.T) {
			f := newFixture(t, "node")
			limits := factoryLimits()
			limits.MaxPreparedPlans = 2
			factory := fixtureFactory(t, f, limits)
			template := sandbox.ExecConfig{NsjailBin: fakeJail(t)}
			start := make(chan struct{})
			var wg sync.WaitGroup
			for i := range 12 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					if i%4 == 0 {
						factory.Retire()
						return
					}
					plan, err := factory.Prepare(t.Context())
					if err != nil {
						if !errors.Is(err, ErrFactoryRetired) && !errors.Is(err, ErrFactoryCapacity) && !errors.Is(err, artifactfs.ErrCapacity) {
							t.Error(err)
						}
						return
					}
					copied := *plan
					closed := make(chan struct{})
					go func() { copied.Close(); close(closed) }()
					worker, err := plan.Spawn(t.Context(), template)
					<-closed
					if err == nil {
						_ = worker.Kill()
					} else if !errors.Is(err, ErrUsed) {
						t.Error(err)
					}
					plan.Close()
				}()
			}
			close(start)
			wg.Wait()
			factory.Close()
			if usage := factory.Usage(); usage.Plans != 0 || usage.Bytes != 0 {
				t.Fatal("retire/prepare/consume race leaked quota")
			}
			f.reclaim(t)
		})
	}
}

func TestFactoryRetiredObservationAndZeroValues(t *testing.T) {
	f := newFixture(t, "node")
	factory := fixtureFactory(t, f, factoryLimits())
	factory.state.mu.Lock()
	done := make(chan bool, 1)
	go func() { done <- factory.Retired() }()
	select {
	case retired := <-done:
		factory.state.mu.Unlock()
		if retired {
			t.Fatal("active factory observed retired")
		}
	case <-time.After(time.Second):
		factory.state.mu.Unlock()
		t.Fatal("retired observer acquired factory mutex")
	}
	var absent *Factory
	for _, factory := range []*Factory{absent, {}} {
		factory.Close()
		factory.Retire()
		if !factory.Retired() || factory.Resources() != (ResourceFacts{}) || factory.Generation() != (Generation{}) ||
			factory.Owner() != "" || factory.ArtifactRevision() != "" || factory.Usage() != (PreparedUsage{}) {
			t.Fatal("empty factory acquired authority")
		}
		if p, err := factory.Prepare(t.Context()); p != nil || !errors.Is(err, ErrFactoryRetired) {
			t.Fatal("empty factory prepared")
		}
	}
}
