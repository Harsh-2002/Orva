package handlers

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/workloadasync"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestCronControlHTTPAdminCASAndNoAdoption(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "cron.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, init := range []func() error{db.Migrate, func() error { return db.InitCronSchemaForFixture(t.Context()) }} {
		if err := init(); err != nil {
			t.Fatal(err)
		}
	}
	id := ids.New()
	if err := db.InsertFunction(&database.Function{ID: id, Name: "cron-http", Status: "active", Runtime: "node", Entrypoint: "handler.js"}); err != nil {
		t.Fatal(err)
	}
	name := "daily"
	e, err := workloadiam.Compile(workloadiam.Metadata{OwnerID: id, Revision: 1, ApprovedArtifact: "artifact"}, workloadiam.Policy{SchemaVersion: 1,
		Allow:   []workloadiam.Statement{{Context: workloadiam.Execution, Actions: []workloadiam.Action{workloadiam.CronsUpsert}, Resource: workloadiam.Resource{OwnCron: &workloadiam.CronResource{Name: &name, TargetID: id}}}},
		Inbound: []workloadiam.Acceptance{{CallerID: id, Triggers: []workloadiam.Trigger{workloadiam.Cron}}}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := workloadiam.NewAuthority(8, []*workloadiam.Evaluator{e})
	if err != nil {
		t.Fatal(err)
	}
	service := &workloadasync.Service{DB: db, Authority: a, CurrentArtifact: func(string) string { return "artifact" }}
	h := &CronHandler{DB: db, Registry: registry.New(db), CronEnabled: true, CronIAM: service}
	keys := map[string]string{}
	for role, perms := range map[string]string{"admin": `["admin"]`, "reader": `["read"]`, "writer": `["write"]`} {
		keys[role] = ids.New()
		if err := db.InsertAPIKey(&database.APIKey{ID: keys[role], Name: role, KeyHash: role, Prefix: "fixture", Permissions: perms}); err != nil {
			t.Fatal(err)
		}
	}
	call := func(role, action, sid string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "/", bytes.NewReader(raw))
		r.SetPathValue("fn_id", id)
		r.SetPathValue("id", sid)
		r = r.WithContext(auth.WithOperator(r.Context(), &auth.Principal{Kind: "api_key", ID: keys[role]}))
		w := httptest.NewRecorder()
		switch action {
		case "create":
			h.Create(w, r)
		case "update":
			h.Update(w, r)
		case "resume":
			h.Resume(w, r)
		case "list":
			h.ListAll(w, r)
		}
		return w
	}
	for _, role := range []string{"reader", "writer"} {
		if w := call(role, "create", "", map[string]any{"cron_expr": "@daily"}); w.Code != 403 {
			t.Fatal(role, w.Code)
		}
	}
	if w := call("admin", "create", "", map[string]any{"cron_expr": "@daily", "origin_kind": "function"}); w.Code != 400 {
		t.Fatal("author origin accepted", w.Code)
	}
	w := call("admin", "create", "", map[string]any{"cron_expr": "@daily"})
	if w.Code != 201 {
		t.Fatal("admin-only create", w.Code)
	}
	var operator database.AsyncCron
	if err := json.Unmarshal(w.Body.Bytes(), &operator); err != nil {
		t.Fatal(err)
	}
	if operator.OriginKind != "operator" || operator.OperatorID != keys["admin"] || operator.CreatorID != "" {
		t.Fatal("wrong host origin")
	}
	c := &database.AsyncCron{CronSchedule: database.CronSchedule{FunctionID: id, Name: name, CronExpr: "@daily", Timezone: "UTC", Enabled: true, Payload: "{}"},
		AsyncProvenance: database.AsyncProvenance{OriginKind: "function", CreatorID: id}}
	if err := db.SaveAsyncCron(t.Context(), c, nil, workloadiam.AsyncRevisions{Creator: 1, Target: 1}, "upsert"); err != nil {
		t.Fatal(err)
	}
	if err := db.SuspendAsyncCron(t.Context(), c, "revoked"); err != nil {
		t.Fatal(err)
	}
	c, err = db.GetAsyncCron(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	expect := map[string]any{"expected_config_revision": strconv.FormatUint(c.ConfigRevision, 10), "expected_authority_generation": strconv.FormatUint(c.AuthorityGeneration, 10)}
	for _, role := range []string{"reader", "writer"} {
		if w := call(role, "resume", c.ID, expect); w.Code != 403 {
			t.Fatal("non-admin resume", role, w.Code)
		}
	}
	w = call("admin", "resume", c.ID, expect)
	if w.Code != 200 {
		t.Fatal("admin-only resume", w.Code)
	}
	var resumed database.AsyncCron
	if err := json.Unmarshal(w.Body.Bytes(), &resumed); err != nil {
		t.Fatal(err)
	}
	if resumed.OriginKind != "function" || resumed.CreatorID != id || resumed.OperatorID != "" || resumed.CreationCallerRevision != 1 {
		t.Fatal("hidden adoption")
	}
	if w := call("admin", "resume", c.ID, expect); w.Code != 409 {
		t.Fatal("stale CAS not 409", w.Code)
	}
	if err := db.DeleteAPIKey(keys["admin"]); err != nil {
		t.Fatal(err)
	}
	expect["expected_config_revision"] = strconv.FormatUint(resumed.ConfigRevision, 10)
	expect["expected_authority_generation"] = strconv.FormatUint(resumed.AuthorityGeneration, 10)
	if w := call("admin", "resume", c.ID, expect); w.Code != 403 {
		t.Fatal("revoked credential was accepted", w.Code)
	}
}
