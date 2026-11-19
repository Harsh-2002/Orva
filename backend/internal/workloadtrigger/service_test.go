package workloadtrigger

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func triggerSchema(t *testing.T, conn *sql.DB, file string) {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), filepath.Join("../database", file), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(f, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			text, _ := strconv.Unquote(lit.Value)
			if strings.Contains(text, "CREATE TABLE IF NOT EXISTS") {
				if _, err := conn.Exec(text); err != nil {
					t.Fatal(err)
				}
			}
		}
		return true
	})
}

type triggerFixture struct {
	s           *Service
	db          *database.Database
	sql         *sql.DB
	owner, next string
	ref         database.OperatorCredentialReference
	ctx         context.Context
}

func triggerSetup(t *testing.T) triggerFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.db")
	db, err := database.New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err = db.Migrate(); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetMaxOpenConns(1)
	triggerSchema(t, conn, "workload_iam_schema.go")
	triggerSchema(t, conn, "workload_exposure_schema.go")
	owners := []string{ids.New(), ids.New()}
	evals := []*workloadiam.Evaluator{}
	for _, owner := range owners {
		if err = db.InsertFunction(&database.Function{ID: owner, Name: owner, Runtime: "node", Status: "active", Entrypoint: "handler.js", Version: 1, MemoryMB: 128, TimeoutMS: 1000, EnvVars: map[string]string{}}); err != nil {
			t.Fatal(err)
		}
		p, e := db.NewIAMPolicy(t.Context(), owner, false, database.IAMActorReference{Kind: "system", ID: ids.New()})
		if e != nil {
			t.Fatal(e)
		}
		eval, e := p.AdmissionEvaluator()
		if e != nil {
			t.Fatal(e)
		}
		evals = append(evals, eval)
	}
	triggerSchema(t, conn, "workload_managed_schema.go")
	a, err := workloadiam.NewAuthority(8, evals)
	if err != nil {
		t.Fatal(err)
	}
	reg := registry.New(db)
	s, err := New(db, reg, a)
	if err != nil {
		t.Fatal(err)
	}
	id := ids.New()
	if err = db.InsertAPIKey(&database.APIKey{ID: id, Name: "fixture", KeyHash: "fixture-key-hash", Prefix: "fixture", Permissions: `["admin","write","read"]`}); err != nil {
		t.Fatal(err)
	}
	p := &auth.Principal{Kind: auth.KindAPIKey, ID: id}
	return triggerFixture{s: s, db: db, sql: conn, owner: owners[0], next: owners[1], ref: database.OperatorCredentialReference{Kind: "api_key", ID: id}, ctx: auth.WithOperator(t.Context(), p)}
}
func (f triggerFixture) expected(owners ...string) map[string]database.ExposureOwnerExpected {
	out := map[string]database.ExposureOwnerExpected{}
	for _, id := range owners {
		out[id] = database.ExposureOwnerExpected{PolicyRevision: 1, AuthorityEpoch: 1}
	}
	return out
}
func triggerBody(t *testing.T, v any) []byte {
	t.Helper()
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestExposureServiceCommitEventsOutsideLocks(t *testing.T) {
	f := triggerSetup(t)
	called := 0
	f.s.reg.PublishEvent = func(_ string, _ any) {
		called++
		lock := f.s.reg.MutationLock(f.owner)
		if !lock.TryLock() {
			t.Error("event under owner lock")
			return
		}
		lock.Unlock()
		if err := f.s.authority.ChangeHostState(func() {}); err != nil {
			t.Error(err)
		}
		if _, err := f.s.Execute(f.ctx, "inspect", triggerBody(t, map[string]string{"kind": "route", "key": "/callback", "function_id": f.owner})); err != nil {
			t.Error("reentrant inspection failed", err)
		}
	}
	_, err := f.s.Apply(f.ctx, f.ref, database.ExposureMutation{Operation: "route_set", Key: "/callback", FunctionID: f.owner, Expected: f.expected(f.owner)})
	if err != nil || called != 1 {
		t.Fatal("committed callback missing", err)
	}
	_, err = f.sql.Exec(`CREATE TABLE exposure_commit_guard(owner TEXT REFERENCES functions(id) DEFERRABLE INITIALLY DEFERRED);
CREATE TRIGGER exposure_commit_fail AFTER INSERT ON workload_exposure_audit BEGIN INSERT INTO exposure_commit_guard VALUES('absent'); END`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.s.Apply(f.ctx, f.ref, database.ExposureMutation{Operation: "route_set", Key: "/callback", FunctionID: f.next, Version: 1, Expected: f.expected(f.owner, f.next)})
	if err == nil || called != 1 {
		t.Fatal("failed COMMIT had event effect", err)
	}
	r, e := f.db.GetRoute("/callback")
	if e != nil || r.FunctionID != f.owner {
		t.Fatal("failed COMMIT changed route")
	}
	var audits int
	if err = f.sql.QueryRow(`SELECT count(*) FROM workload_exposure_audit`).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("failed COMMIT changed audit")
	}
}

func TestExposureServiceSortedLocksWaitAndMappingConflict(t *testing.T) {
	f := triggerSetup(t)
	if err := f.db.UpsertRoute("/waiting", f.owner, "GET"); err != nil {
		t.Fatal(err)
	}
	c := database.ExposureMutation{Operation: "route_set", Key: "/waiting", FunctionID: f.next, Expected: f.expected(f.owner, f.next)}
	plan, err := f.db.PrepareExposure(f.ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	low, high := f.owner, f.next
	if high < low {
		low, high = high, low
	}
	lock := f.s.reg.MutationLock(low)
	lock.Lock()
	released := false
	defer func() {
		if !released {
			lock.Unlock()
		}
	}()
	// Holding low must leave high free while the service waits for its first lock.
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(f.ctx, 3*time.Second)
	defer cancel()
	go func() {
		_, err := f.s.applyPrepared(ctx, f.ref, c, plan)
		done <- err
	}()
	if !f.s.reg.MutationLock(high).TryLock() {
		t.Fatal("high owner locked before low")
	}
	f.s.reg.MutationLock(high).Unlock()
	if err := f.db.UpsertRoute("/waiting", f.next, "POST"); err != nil {
		t.Fatal(err)
	}
	lock.Unlock()
	released = true
	select {
	case err := <-done:
		if !errors.Is(err, database.ErrExposureConflict) {
			t.Fatal("old mapping silently adopted", err)
		}
	case <-ctx.Done():
		t.Fatal("owner wait deadlocked")
	}
	r, err := f.db.GetRoute("/waiting")
	if err != nil || r.FunctionID != f.next || r.Methods != "POST" {
		t.Fatal("racing mapping changed")
	}
}

func TestExposureStrictHTTPAndExecute(t *testing.T) {
	f := triggerSetup(t)
	base := map[string]any{"path": "/strict", "function_id": f.owner, "version": "0", "expected": f.expected(f.owner)}
	valid := string(triggerBody(t, base))
	bad := []string{
		`{"path":"/a","path":"/b"}`,
		strings.TrimSuffix(valid, "}") + `,"actor":{"kind":"system"}}`,
		strings.TrimSuffix(valid, "}") + `,"artifact":"` + strings.Repeat("a", 64) + `"}`,
		strings.TrimSuffix(valid, "}") + `,"operator":{"admin":true}}`,
		strings.Replace(valid, `"version":"0"`, `"version":0`, 1),
		strings.Replace(valid, `"version":"0"`, `"version":"00"`, 1),
		strings.Replace(valid, `"path":"/strict"`, `"path":"/\ud800"`, 1),
		strings.Replace(valid, `"path":"/strict"`, "\"path\":\"/\xff\"", 1),
		strings.Repeat(" ", MaxRequestBytes) + valid,
		valid + `{}`,
	}
	for i, body := range bad {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			if _, err := f.s.Execute(f.ctx, "route_set", []byte(body)); err == nil {
				t.Fatal("strict execute accepted invalid body")
			}
			req := httptest.NewRequest("POST", "/api/v1/routes", bytes.NewBufferString(body)).WithContext(f.ctx)
			w := httptest.NewRecorder()
			f.s.HTTP(w, req, "route_set", "", "")
			if w.Code != http.StatusBadRequest {
				t.Fatal("strict HTTP not rejected", w.Code)
			}
		})
	}
	for _, ctx := range []context.Context{context.Background(), auth.WithOperator(f.ctx, &auth.Principal{Kind: auth.KindChannel, ID: ids.New()})} {
		if _, err := f.s.Execute(ctx, "route_set", []byte(valid)); !errors.Is(err, database.ErrOperatorDenied) {
			t.Fatal("missing reference accepted", err)
		}
	}
	if _, err := f.s.Execute(f.ctx, "route_set", []byte(valid)); err != nil {
		t.Fatal(err)
	}
}

func TestExposureServiceConcurrentReentry(t *testing.T) {
	f := triggerSetup(t)
	inside := false
	f.s.reg.PublishEvent = func(_ string, _ any) {
		if inside {
			return
		}
		inside = true
		defer func() { inside = false }()
		lock := f.s.reg.MutationLock(f.owner)
		if !lock.TryLock() {
			t.Error("owner lock held")
			return
		}
		lock.Unlock()
		if err := f.s.authority.MutateOwners([]workloadiam.Metadata{{OwnerID: f.owner, Revision: 1, ApprovedArtifact: database.IAMUnboundArtifact}}, func() error { return nil }); err != nil {
			t.Error("gate held or authority changed", err)
		}
		_, err := f.s.Apply(f.ctx, f.ref, database.ExposureMutation{Operation: "route_set", Key: "/nested", FunctionID: f.owner, Expected: f.expected(f.owner)})
		if err != nil {
			t.Error("reentrant mutation", err)
		}
	}
	if _, err := f.s.Apply(f.ctx, f.ref, database.ExposureMutation{Operation: "route_set", Key: "/reentry", FunctionID: f.owner, Expected: f.expected(f.owner)}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.GetRoute("/nested"); err != nil {
		t.Fatal("reentrant mutation absent", err)
	}
}

func TestSelectorsRequireCanonicalUUIDs(t *testing.T) {
	owner := ids.New()
	upper := strings.ToUpper(owner)
	for _, raw := range []string{
		`{"kind":"inbound","function_id":"` + upper + `"}`,
		`{"kind":"inbound","function_id":"` + strings.ReplaceAll(owner, "-", "") + `"}`,
		`{"kind":"route","key":"/x","function_id":"` + upper + `"}`,
	} {
		if _, _, _, err := decodeInspect([]byte(raw)); !errors.Is(err, database.ErrExposureInvalid) {
			t.Fatalf("%s: non-canonical selector accepted: %v", raw, err)
		}
	}
	if _, _, _, err := decodeInspect([]byte(`{"kind":"inbound","function_id":"` + owner + `"}`)); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"n","function_id":"` + owner + `","version":"0","expected":{"` + upper + `":{"policy_revision":"1","authority_epoch":"1"}}}`
	if _, err := Decode("inbound_create", []byte(body)); !errors.Is(err, database.ErrExposureInvalid) {
		t.Fatalf("non-canonical expected owner accepted: %v", err)
	}
}
