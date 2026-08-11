package handlers

import (
	"bytes"
	"encoding/json"
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
	req := httptest.NewRequest(http.MethodPost, "/api/v1/_internal/crons", bytes.NewBufferString(`{"name":"nightly","schedule":"0 3 * * *"}`))
	req.Header.Set("X-Orva-Internal-Token", auth.Mint("fn-cron-owner"))
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
