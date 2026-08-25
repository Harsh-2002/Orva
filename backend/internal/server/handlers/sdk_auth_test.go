package handlers

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sdkauth"
)

func insertSDKTestFunction(t *testing.T, db *database.Database, id, name string) {
	t.Helper()
	err := db.InsertFunction(&database.Function{
		ID: id, Name: name, Runtime: "node", Entrypoint: "handler.js", TimeoutMS: 30000,
		MemoryMB: 64, CPUs: 0.5, EnvVars: map[string]string{}, NetworkMode: "none", Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestKVScopedCredentialRejectsCrossNamespaceAndCallerSpoof(t *testing.T) {
	db := newTestDB(t)
	insertSDKTestFunction(t, db, "fn-sdk-a", "sdk-a")
	insertSDKTestFunction(t, db, "fn-sdk-b", "sdk-b")
	auth := sdkauth.New([]byte("process-secret"))
	h := &KVHandler{DB: db, SDKAuth: auth}

	cross := httptest.NewRequest(http.MethodPut, "/api/v1/_kv/fn-sdk-b/key", bytes.NewBufferString(`{"value":1}`))
	cross.SetPathValue("fn_id", "fn-sdk-b")
	cross.SetPathValue("key", "key")
	cross.Header.Set("X-Orva-Internal-Token", auth.Mint("fn-sdk-a"))
	cross.Header.Set("X-Orva-Caller-Function", "fn-sdk-b")
	crossResult := httptest.NewRecorder()
	h.Put(crossResult, cross)
	if crossResult.Code != http.StatusForbidden {
		t.Fatalf("cross-namespace status=%d body=%s", crossResult.Code, crossResult.Body.String())
	}

	spoof := httptest.NewRequest(http.MethodPut, "/api/v1/_kv/fn-sdk-a/key", bytes.NewBufferString(`{"value":1}`))
	spoof.SetPathValue("fn_id", "fn-sdk-a")
	spoof.SetPathValue("key", "key")
	spoof.Header.Set("X-Orva-Internal-Token", auth.Mint("fn-sdk-a"))
	spoof.Header.Set("X-Orva-Caller-Function", "fn-sdk-b")
	spoofResult := httptest.NewRecorder()
	h.Put(spoofResult, spoof)
	if spoofResult.Code != http.StatusOK {
		t.Fatalf("signed caller rejected because of spoofable header: status=%d body=%s", spoofResult.Code, spoofResult.Body.String())
	}
	if _, err := db.KVGet("fn-sdk-a", "key"); err != nil {
		t.Fatalf("signed namespace was not written: %v", err)
	}
	if _, err := db.KVGet("fn-sdk-b", "key"); err != database.ErrKVNotFound {
		t.Fatalf("spoofed namespace was written: %v", err)
	}
}

func TestJobAttributionUsesSignedCaller(t *testing.T) {
	db := newTestDB(t)
	insertSDKTestFunction(t, db, "fn-job-caller", "job-caller")
	insertSDKTestFunction(t, db, "fn-job-target", "job-target")
	auth := sdkauth.New([]byte("process-secret"))
	release := auth.BindExecution("exec-job", "fn-job-caller", "trace-signed", "span-signed", time.Now())
	defer release()
	h := &JobsHandler{DB: db, SDKAuth: auth}
	body := bytes.NewBufferString(`{"function_name":"job-target","payload":{}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/jobs", body)
	req.Header.Set("X-Orva-Internal-Token", auth.Mint("fn-job-caller"))
	req.Header.Set("X-Orva-Caller-Function", "fn-spoof")
	req.Header.Set("X-Orva-Execution-Id", "exec-job")
	req.Header.Set("X-Orva-Trace-Id", "trace-spoof")
	req.Header.Set("X-Orva-Span-Id", "span-spoof")
	w := httptest.NewRecorder()
	h.Enqueue(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var job database.Job
	if err := json.NewDecoder(w.Body).Decode(&job); err != nil {
		t.Fatal(err)
	}
	if job.EnqueuedByFunctionID != "fn-job-caller" {
		t.Fatalf("enqueued_by=%q", job.EnqueuedByFunctionID)
	}
	if job.TraceID != "trace-signed" || job.ParentSpanID != "span-signed" {
		t.Fatalf("trace attribution=%q/%q", job.TraceID, job.ParentSpanID)
	}
}

func TestUserSpanMustBelongToSignedCaller(t *testing.T) {
	auth := sdkauth.New([]byte("process-secret"))
	release := auth.BindExecution("exec-owned", "fn-owner", "trace", "span", time.Now())
	defer release()
	h := &SpansHandler{DB: newTestDB(t), SDKAuth: auth}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/_internal/spans", bytes.NewBufferString(`{"name":"work","duration_ms":1}`))
	req.Header.Set("X-Orva-Internal-Token", auth.Mint("fn-other"))
	req.Header.Set("X-Orva-Trace-Id", "trace")
	req.Header.Set("X-Orva-Span-Id", "span")
	req.Header.Set("X-Orva-Execution-Id", "exec-owned")
	w := httptest.NewRecorder()
	h.Create(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestCronUpsertUsesSignedCallerScope(t *testing.T) {
	db := newTestDB(t)
	insertSDKTestFunction(t, db, "fn-cron-owner", "cron-owner")
	insertSDKTestFunction(t, db, "fn-cron-other", "cron-other")
	auth := sdkauth.New([]byte("process-secret"))
	h := &CronHandler{DB: db, SDKAuth: auth}
	release := auth.BindExecution("exec-1", "fn-cron-owner", "trace-1", "span-1", time.Now())
	defer release()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/_internal/crons", bytes.NewBufferString(`{"name":"nightly","schedule":"0 3 * * *"}`))
	req.Header.Set("X-Orva-Internal-Token", auth.Mint("fn-cron-owner"))
	req.Header.Set("X-Orva-Execution-Id", "exec-1")
	req.Header.Set("X-Orva-Function-Id", "fn-cron-other")
	w := httptest.NewRecorder()
	h.UpsertInternal(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	ownerRows, err := db.ListCronSchedulesForFunction("fn-cron-owner")
	if err != nil || len(ownerRows) != 1 {
		t.Fatalf("owner schedules=%d err=%v", len(ownerRows), err)
	}
	otherRows, err := db.ListCronSchedulesForFunction("fn-cron-other")
	if err != nil || len(otherRows) != 0 {
		t.Fatalf("other schedules=%d err=%v", len(otherRows), err)
	}
}

// A signed credential is not enough to plant a schedule. Every other use of a
// leaked ORVA_INTERNAL_TOKEN dies when orvad restarts and the process-random
// signing key is regenerated; a cron row does not, and nothing in the
// scheduler consults the function's auth_mode. So this one surface requires
// the caller to be inside a live execution of the function it is scheduling,
// which a copy of the token taken off the box cannot be.
//
// It also rules out module-scope crons.upsert, where the adapters leave
// ORVA_EXECUTION_ID unset -- undocumented, and already wrong, since it
// re-registered on every cold spawn.
func TestCronUpsertRequiresALiveExecution(t *testing.T) {
	db := newTestDB(t)
	insertSDKTestFunction(t, db, "fn-cron-owner", "cron-owner")
	auth := sdkauth.New([]byte("process-secret"))
	h := &CronHandler{DB: db, SDKAuth: auth}

	cases := []struct {
		name        string
		executionID string
		bind        string // function to bind exec-1 to, "" for no binding
	}{
		{"no execution header at all", "", ""},
		{"an execution id that is not live", "exec-stale", ""},
		{"a live execution belonging to another function", "exec-1", "fn-cron-other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.bind != "" {
				defer auth.BindExecution("exec-1", tc.bind, "t", "s", time.Now())()
			}
			req := httptest.NewRequest(http.MethodPost, "/api/v1/_internal/crons",
				bytes.NewBufferString(`{"name":"planted","schedule":"* * * * *"}`))
			req.Header.Set("X-Orva-Internal-Token", auth.Mint("fn-cron-owner"))
			if tc.executionID != "" {
				req.Header.Set("X-Orva-Execution-Id", tc.executionID)
			}
			w := httptest.NewRecorder()
			h.UpsertInternal(w, req)

			if w.Code != http.StatusForbidden {
				t.Errorf("status=%d, want 403: a token replayed outside a live execution must not plant a schedule; body=%s",
					w.Code, w.Body.String())
			}
			rows, err := db.ListCronSchedulesForFunction("fn-cron-owner")
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 0 {
				t.Errorf("a schedule was written anyway: %d rows", len(rows))
			}
		})
	}
}

// Upsert is keyed by (function, name), so distinct names multiply without
// bound, and nothing validates the interval -- "* * * * *" is accepted. A
// runaway loop, or one compromised execution, could fill the scheduler with
// once-a-minute work against its own function.
func TestSDKScheduleCountIsCapped(t *testing.T) {
	db := newTestDB(t)
	insertSDKTestFunction(t, db, "fn-cron-many", "cron-many")
	auth := sdkauth.New([]byte("process-secret"))
	h := &CronHandler{DB: db, SDKAuth: auth}
	defer auth.BindExecution("exec-1", "fn-cron-many", "t", "s", time.Now())()

	upsert := func(name string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/_internal/crons",
			bytes.NewBufferString(`{"name":"`+name+`","schedule":"* * * * *"}`))
		req.Header.Set("X-Orva-Internal-Token", auth.Mint("fn-cron-many"))
		req.Header.Set("X-Orva-Execution-Id", "exec-1")
		w := httptest.NewRecorder()
		h.UpsertInternal(w, req)
		return w.Code
	}

	for i := 0; i < maxSDKSchedulesPerFunction; i++ {
		if code := upsert(fmt.Sprintf("job-%d", i)); code != http.StatusOK {
			t.Fatalf("schedule %d was rejected with %d, below the cap", i, code)
		}
	}
	if code := upsert("one-too-many"); code != http.StatusBadRequest {
		t.Errorf("schedule %d returned %d, want 400: the cap is not enforced",
			maxSDKSchedulesPerFunction+1, code)
	}

	// Updating one that already exists is not a new schedule and must still
	// work at the cap -- otherwise a function pinned at the limit can no
	// longer re-declare its own jobs on deploy.
	if code := upsert("job-0"); code != http.StatusOK {
		t.Errorf("re-declaring an existing schedule at the cap returned %d, want 200", code)
	}

	rows, err := db.ListCronSchedulesForFunction("fn-cron-many")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != maxSDKSchedulesPerFunction {
		t.Errorf("stored %d schedules, want %d", len(rows), maxSDKSchedulesPerFunction)
	}
}

// The cap counts what the SDK declared, not what is on the Schedules page. An
// earlier version counted every row, so a function whose operator had already
// created 25 schedules by hand was refused its FIRST self-declared one --
// with a message saying it had declared too many, having declared none.
func TestTheScheduleCapIgnoresOperatorCreatedRows(t *testing.T) {
	db := newTestDB(t)
	insertSDKTestFunction(t, db, "fn-cron-mixed", "cron-mixed")
	auth := sdkauth.New([]byte("process-secret"))
	h := &CronHandler{DB: db, SDKAuth: auth}
	defer auth.BindExecution("exec-1", "fn-cron-mixed", "t", "s", time.Now())()

	// Operator schedules: created in the dashboard, so no name.
	for i := 0; i < maxSDKSchedulesPerFunction+5; i++ {
		if err := db.InsertCronSchedule(&database.CronSchedule{
			ID:         fmt.Sprintf("cron_op_%d", i),
			FunctionID: "fn-cron-mixed", CronExpr: "0 4 * * *",
			Timezone: "UTC", Enabled: true, Payload: "{}",
		}); err != nil {
			t.Fatal(err)
		}
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/_internal/crons",
		bytes.NewBufferString(`{"name":"first-declared","schedule":"0 3 * * *"}`))
	req.Header.Set("X-Orva-Internal-Token", auth.Mint("fn-cron-mixed"))
	req.Header.Set("X-Orva-Execution-Id", "exec-1")
	w := httptest.NewRecorder()
	h.UpsertInternal(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status=%d, want 200: this function has declared no schedules of its own; body=%s",
			w.Code, w.Body.String())
	}
}
