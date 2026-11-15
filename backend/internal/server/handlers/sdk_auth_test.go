package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sdkauth"
)

func insertSDKTestFunction(t *testing.T, db *database.Database, id, name string) {
	t.Helper()
	if err := db.InsertFunction(&database.Function{
		ID: id, Name: name, Runtime: "node", Entrypoint: "handler.js", TimeoutMS: 30000,
		MemoryMB: 64, CPUs: 0.5, EnvVars: map[string]string{}, NetworkMode: "none", Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
}

func TestJobCoreAttributionUsesBoundCaller(t *testing.T) {
	db := newTestDB(t)
	insertSDKTestFunction(t, db, "fn-job-caller", "job-caller")
	insertSDKTestFunction(t, db, "fn-job-target", "job-target")
	auth := sdkauth.New()
	defer auth.BindExecution("exec-job", "fn-job-caller", "trace-bound", "span-bound", time.Now(), 0)()
	traceID, spanID, _, active := auth.TraceContext("exec-job", "fn-job-caller")
	if !active {
		t.Fatal("caller execution not bound")
	}
	job, err := EnqueueJob(db, "fn-job-caller", traceID, spanID, &EnqueueJobRequest{
		FunctionName: "job-target", Payload: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if job.EnqueuedByFunctionID != "fn-job-caller" || job.TraceID != "trace-bound" || job.ParentSpanID != "span-bound" {
		t.Fatal("job lost bound caller attribution")
	}
}

func TestUserSpanCoreUsesBoundTraceAndExecutionStart(t *testing.T) {
	db := newTestDB(t)
	insertSDKTestFunction(t, db, "fn-owner", "owner")
	auth := sdkauth.New()
	started := time.Now().UTC().Add(-time.Second)
	defer auth.BindExecution("exec-owned", "fn-owner", "trace", "span", started, 0)()
	traceID, parentID, executionStart, owned := auth.TraceContext("exec-owned", "fn-owner")
	if !owned {
		t.Fatal("caller execution not bound")
	}
	_, serr := CreateUserSpan(db, "fn-owner", "exec-owned", traceID, parentID, executionStart,
		&UserSpanRequest{Name: "work", StartedAt: started.Add(25 * time.Millisecond), DurationMS: 1})
	if serr != nil {
		t.Fatal(serr)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		rows, err := db.ListUserSpansByExecution("exec-owned")
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 1 {
			row := rows[0]
			if row.TraceID != "trace" || row.ParentSpanID != "span" || row.OffsetMS != 25 {
				t.Fatalf("span lost host-bound attribution: %+v", row)
			}
			var fnID string
			if err := db.ReadDB().QueryRow("SELECT function_id FROM user_spans WHERE execution_id = ?", "exec-owned").Scan(&fnID); err != nil {
				t.Fatal(err)
			}
			if fnID != "fn-owner" {
				t.Fatalf("span function_id = %q", fnID)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("span was not recorded")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCronCoreUsesHostFunctionScope(t *testing.T) {
	db := newTestDB(t)
	insertSDKTestFunction(t, db, "fn-cron-owner", "cron-owner")
	insertSDKTestFunction(t, db, "fn-cron-other", "cron-other")
	if _, err := UpsertCron(db, "fn-cron-owner", &UpsertCronRequest{Name: "nightly", Schedule: "0 3 * * *"}); err != nil {
		t.Fatal(err)
	}
	owner, err := db.ListCronSchedulesForFunction("fn-cron-owner")
	if err != nil || len(owner) != 1 {
		t.Fatalf("owner schedules=%d err=%v", len(owner), err)
	}
	other, err := db.ListCronSchedulesForFunction("fn-cron-other")
	if err != nil || len(other) != 0 {
		t.Fatalf("other schedules=%d err=%v", len(other), err)
	}
}

func TestSDKScheduleCountIsCapped(t *testing.T) {
	db := newTestDB(t)
	insertSDKTestFunction(t, db, "fn-cron-many", "cron-many")
	upsert := func(name string) *StatusErr {
		_, err := UpsertCron(db, "fn-cron-many", &UpsertCronRequest{Name: name, Schedule: "* * * * *"})
		return err
	}
	for i := 0; i < maxSDKSchedulesPerFunction; i++ {
		if err := upsert(fmt.Sprintf("job-%d", i)); err != nil {
			t.Fatalf("below cap: %v", err)
		}
	}
	if err := upsert("one-too-many"); err == nil || err.Status != http.StatusBadRequest || err.Code != "VALIDATION" {
		t.Fatalf("26th schedule = %+v", err)
	}
	if err := upsert("job-0"); err != nil {
		t.Fatalf("update at cap: %v", err)
	}
	rows, err := db.ListCronSchedulesForFunction("fn-cron-many")
	if err != nil || len(rows) != maxSDKSchedulesPerFunction {
		t.Fatalf("schedules=%d err=%v", len(rows), err)
	}
}

func TestTheScheduleCapIgnoresOperatorCreatedRows(t *testing.T) {
	db := newTestDB(t)
	insertSDKTestFunction(t, db, "fn-cron-mixed", "cron-mixed")
	for i := 0; i < maxSDKSchedulesPerFunction+5; i++ {
		if err := db.InsertCronSchedule(&database.CronSchedule{
			ID: fmt.Sprintf("cron_op_%d", i), FunctionID: "fn-cron-mixed", CronExpr: "0 4 * * *",
			Timezone: "UTC", Enabled: true, Payload: "{}",
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := UpsertCron(db, "fn-cron-mixed", &UpsertCronRequest{Name: "first-declared", Schedule: "0 3 * * *"}); err != nil {
		t.Fatalf("operator schedules prevented first SDK schedule: %v", err)
	}
}
