package database

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

func TestAdmittedSpanNonblockingOwnedQueue(t *testing.T) {
	db := &Database{writer: newAsyncWriter(nil)}
	span := UserSpan{ID: "span", FunctionID: "fn", ExecutionID: "exec", TraceID: "trace", ParentSpanID: "parent", Name: "original", Attributes: `{"synthetic":true}`}
	if !db.TryInsertAdmittedUserSpan(span, workloadiam.SpanDecision{}) {
		t.Fatal("queue rejected")
	}
	span.Name = "mutated"
	job := <-db.writer.telemetry
	if job.functionID != "fn" || job.args[5] != "original" || job.args[8] != `{"synthetic":true}` {
		t.Fatal("queue retained mutable envelope")
	}
	db.writer.telemetryBytes.Add(-int64(job.bytes))
	db.writer.enqueueMu.Lock()
	if db.TryInsertAdmittedUserSpan(span, workloadiam.SpanDecision{}) {
		t.Fatal("blocked writer fence accepted")
	}
	db.writer.enqueueMu.Unlock()
	for range cap(db.writer.telemetry) {
		if !db.TryInsertAdmittedUserSpan(span, workloadiam.SpanDecision{}) {
			t.Fatal("early saturation")
		}
	}
	before := db.WriterStats()
	if db.TryInsertAdmittedUserSpan(span, workloadiam.SpanDecision{}) {
		t.Fatal("full queue accepted")
	}
	after := db.WriterStats()
	if after.TelemetryBytes != before.TelemetryBytes || after.DroppedTelemetry != before.DroppedTelemetry+1 {
		t.Fatal("full queue leaked bytes")
	}
	for len(db.writer.telemetry) > 0 {
		j := <-db.writer.telemetry
		db.writer.telemetryBytes.Add(-int64(j.bytes))
	}
	db.writer.telemetryBytes.Store(maxTelemetryQueueBytes)
	if db.TryInsertAdmittedUserSpan(UserSpan{Attributes: strings.Repeat("a", 65536)}, workloadiam.SpanDecision{}) {
		t.Fatal("byte saturation accepted")
	}
	if db.writer.telemetryBytes.Load() != maxTelemetryQueueBytes {
		t.Fatal("byte refusal leaked reservation")
	}
	db.writer.telemetryBytes.Store(0)
	close(db.writer.stopRequested)
	if db.TryInsertAdmittedUserSpan(span, workloadiam.SpanDecision{}) || db.WriterStats().TelemetryBytes != 0 {
		t.Fatal("shutdown accepted/leaked")
	}
}

func TestAdmittedSpanActualWriterBlockedRevoke(t *testing.T) {
	db := newTestDB(t)
	const owner = "019a8dd0-0000-7000-8000-000000000001"
	const execution = "019a8dd0-0000-7000-8000-000000000003"
	seedFunctionRow(t, db, owner)
	compile := func(rev uint64, grant bool) *workloadiam.Evaluator {
		t.Helper()
		p := workloadiam.Policy{SchemaVersion: 1, Allow: []workloadiam.Statement{}, Inbound: []workloadiam.Acceptance{}}
		if grant {
			p.Allow = append(p.Allow, workloadiam.Statement{Context: workloadiam.Execution, Actions: []workloadiam.Action{workloadiam.SpansEmit}, Resource: workloadiam.Resource{OwnExecution: &workloadiam.ExecutionResource{}}})
		}
		e, err := workloadiam.Compile(workloadiam.Metadata{OwnerID: owner, Revision: rev, ApprovedArtifact: "artifact"}, p)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	a, err := workloadiam.NewAuthority(8, []*workloadiam.Evaluator{compile(1, true)})
	if err != nil {
		t.Fatal(err)
	}
	c := workloadiam.Context{Principal: workloadiam.Principal{FunctionID: owner, WorkerID: "worker", ArtifactRevision: "artifact"},
		Phase: workloadiam.Execution, Ready: true, Lease: workloadiam.Lease{ID: "lease", WorkerID: "worker", ExecutionID: execution, Active: true}}
	r := workloadiam.Request{Operation: workloadiam.OpSpansEmit, FunctionID: owner, ExecutionID: execution, Resource: workloadiam.Resource{OwnExecution: &workloadiam.ExecutionResource{}}}
	// Hold the real sole SQLite write connection, not a fake writer callback.
	connection, err := db.write.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	waits := db.write.Stats().WaitCount
	span := UserSpan{ID: "blocked-span", FunctionID: owner, ExecutionID: execution, TraceID: "host-trace", ParentSpanID: "host-parent", Name: "before-revoke", Attributes: `{"owned":true}`, StartedAt: time.Now(), Status: "ok"}
	done := make(chan error, 1)
	go func() {
		done <- a.EmitSpanObserved(func() workloadiam.Context { return c }, r, func(decision workloadiam.SpanDecision) error {
			if !db.TryInsertAdmittedUserSpan(span, decision) {
				return context.Canceled
			}
			return nil
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("admission waited on SQLite")
	}
	span.Name, span.Attributes = "after", `{"owned":false}`
	if !waitFor(t, time.Second, func() bool { return db.write.Stats().WaitCount > waits }) {
		t.Fatal("actual writer did not wait for the occupied SQLite connection")
	}
	go func() { done <- a.Publish(1, compile(2, false), func() error { return nil }) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("writer blocked revoke")
	}
	called := false
	if err := a.DoObserved(func() workloadiam.Context { return c }, r, func() error { called = true; return nil }); err == nil || called {
		t.Fatal("post-revoke emission admitted")
	}
	if db.WriterStats().TelemetryBytes == 0 {
		t.Fatal("writer was not holding the admitted envelope")
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	if !waitFor(t, 5*time.Second, func() bool { return db.WriterStats().TelemetryBytes == 0 }) {
		t.Fatal("writer did not release envelope bytes")
	}
	rows, err := db.ListUserSpansByExecution(execution)
	if err != nil || len(rows) != 1 || rows[0].Name != "before-revoke" || rows[0].TraceID != "host-trace" || rows[0].ParentSpanID != "host-parent" || rows[0].Attributes != `{"owned":true}` {
		t.Fatal("post-revoke writer reinterpreted accepted envelope", err)
	}
}

func TestAdmittedSpanDeletionFence(t *testing.T) {
	db := newTestDB(t)
	seedFunctionRow(t, db, "admitted-span-owner")
	db.writer.stop(5 * time.Second)
	db.writer = newAsyncWriter(db)
	if !db.TryInsertAdmittedUserSpan(UserSpan{ID: "admitted", FunctionID: "admitted-span-owner",
		ExecutionID: "original-execution", TraceID: "original-trace", ParentSpanID: "original-parent", Name: "synthetic", StartedAt: time.Now(), Status: "ok"}, workloadiam.SpanDecision{}) {
		t.Fatal("envelope refused")
	}
	if err := db.DeleteFunction(context.Background(), "admitted-span-owner"); err != nil {
		t.Fatal(err)
	}
	db.writer.start()
	if !waitFor(t, 5*time.Second, func() bool {
		stats := db.WriterStats()
		return stats.DeletedWrites == 1 && stats.TelemetryBytes == 0
	}) {
		t.Fatal("deleted envelope did not shed/reservation was retained", db.WriterStats())
	}
	if countQuery(t, db, `SELECT COUNT(*) FROM user_spans WHERE execution_id=?`, "original-execution") != 0 {
		t.Fatal("deleted telemetry resurrected")
	}
}
