package database

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

func TestSpanDecisionConcurrentOffers(t *testing.T) {
	for _, large := range []bool{false, true} {
		t.Run(fmt.Sprint("large=", large), func(t *testing.T) {
			a, c, r, _ := spanDecisionFixture(t)
			db := newTestDB(t)
			seedFunctionRow(t, db, r.FunctionID)
			db.writer.stop(5 * time.Second)
			db.writer = newAsyncWriter(db)
			started := false
			t.Cleanup(func() {
				if !started {
					db.writer.start()
				}
			})
			attrs := `{"synthetic":true}`
			if large {
				attrs = `{"synthetic":"` + strings.Repeat("x", 63000) + `"}`
			}
			var accepted atomic.Int64
			var invalid atomic.Bool
			start := make(chan struct{})
			var wg sync.WaitGroup
			const producers, perProducer = 32, 64
			for g := range producers {
				wg.Add(1)
				go func(g int) {
					defer wg.Done()
					<-start
					for i := range perProducer {
						s := UserSpan{ID: fmt.Sprintf("p-%d-%d", g, i), FunctionID: r.FunctionID, ExecutionID: r.ExecutionID,
							TraceID: "host-trace", ParentSpanID: "host-parent", Name: "original", Attributes: attrs, StartedAt: time.Now().UTC(), Status: "ok"}
						err := a.EmitSpanObserved(func() workloadiam.Context { return c }, r, func(d workloadiam.SpanDecision) error {
							if db.TryInsertAdmittedUserSpan(s, d) {
								accepted.Add(1)
							}
							return nil
						})
						if err != nil {
							invalid.Store(true)
						}
						s.Name, s.Attributes = "changed", "changed"
						n := db.WriterStats().TelemetryBytes
						if n < 0 || n > maxTelemetryQueueBytes {
							invalid.Store(true)
						}
					}
				}(g)
			}
			close(start)
			wg.Wait()
			n := accepted.Load()
			stats := db.WriterStats()
			if invalid.Load() || n <= 0 || n >= producers*perProducer || stats.DroppedTelemetry != uint64(producers*perProducer)-uint64(n) {
				t.Fatal("concurrent offers/counters did not reconcile")
			}
			if !large && n != int64(cap(db.writer.telemetry)) {
				t.Fatal("small jobs did not hit queue cap")
			}
			if large && n >= int64(cap(db.writer.telemetry)) {
				t.Fatal("large jobs did not hit byte cap")
			}
			jobs := make([]writeJob, 0, int(n))
			var bytes int64
			for range n {
				j := <-db.writer.telemetry
				if j.spanDecision.OwnerID() != r.FunctionID || j.spanDecision.Revision() != 1 {
					t.Fatal("concurrent job lost deciding snapshot")
				}
				if j.bytes != jobBytes(j.sql, j.args)+len(j.functionID)+32+len(r.FunctionID) {
					t.Fatal("provenance accounting differs")
				}
				bytes += int64(j.bytes)
				jobs = append(jobs, j)
			}
			if bytes != stats.TelemetryBytes {
				t.Fatal("reservation differs from retained provenance jobs")
			}
			for _, j := range jobs {
				db.writer.telemetry <- j
			}
			db.writer.start()
			started = true
			if !waitFor(t, 10*time.Second, func() bool { return db.WriterStats().TelemetryBytes == 0 }) {
				t.Fatal("concurrent jobs did not drain")
			}
			rows, err := db.ListUserSpansByExecution(r.ExecutionID)
			if err != nil || len(rows) != int(n) {
				t.Fatal("concurrent row count differs")
			}
			for _, s := range rows {
				if s.Name != "original" || s.Attributes != attrs || s.TraceID != "host-trace" || s.ParentSpanID != "host-parent" {
					t.Fatal("concurrent attribution changed")
				}
			}
			if db.WriterStats().DroppedTelemetry != stats.DroppedTelemetry {
				t.Fatal("healthy drain shed jobs")
			}
			t.Logf("producers=%d offers=%d accepted=%d shed=%d provenance_accounted_bytes=%d final_bytes=0", producers, producers*perProducer, n, stats.DroppedTelemetry, bytes)
		})
	}
}

func spanDecisionFixture(t *testing.T) (*workloadiam.Authority, workloadiam.Context, workloadiam.Request, func(uint64, bool) *workloadiam.Evaluator) {
	t.Helper()
	const owner = "019a8dd0-0000-7000-8000-000000000001"
	const execution = "019a8dd0-0000-7000-8000-000000000003"
	compile := func(rev uint64, grant bool) *workloadiam.Evaluator {
		t.Helper()
		p := workloadiam.Policy{SchemaVersion: 1, Allow: []workloadiam.Statement{}, Inbound: []workloadiam.Acceptance{}}
		if grant {
			p.Allow = append(p.Allow, workloadiam.Statement{Context: workloadiam.Execution, Actions: []workloadiam.Action{workloadiam.SpansEmit},
				Resource: workloadiam.Resource{OwnExecution: &workloadiam.ExecutionResource{}}})
		}
		e, err := workloadiam.Compile(workloadiam.Metadata{OwnerID: owner, Revision: rev, ApprovedArtifact: "artifact"}, p)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	a, err := workloadiam.NewAuthority(0, []*workloadiam.Evaluator{compile(1, true)})
	if err != nil {
		t.Fatal(err)
	}
	c := workloadiam.Context{Principal: workloadiam.Principal{FunctionID: owner, WorkerID: "worker", ArtifactRevision: "artifact"},
		Phase: workloadiam.Execution, Ready: true, Lease: workloadiam.Lease{ID: "synthetic", WorkerID: "worker", ExecutionID: execution, Active: true}}
	r := workloadiam.Request{Operation: workloadiam.OpSpansEmit, FunctionID: owner, ExecutionID: execution, Resource: workloadiam.Resource{OwnExecution: &workloadiam.ExecutionResource{}}}
	return a, c, r, compile
}

func TestSpanDecisionRevisionJobs(t *testing.T) {
	a, c, r, compile := spanDecisionFixture(t)
	db := &Database{writer: newAsyncWriter(nil)}
	s := UserSpan{ID: "same-envelope", FunctionID: r.FunctionID, ExecutionID: r.ExecutionID, TraceID: "host-trace",
		ParentSpanID: "host-parent", Name: "immutable", StartedAt: time.Unix(1700000000, 0).UTC(), Status: "ok"}
	offer := func() error {
		return a.EmitSpanObserved(func() workloadiam.Context { return c }, r, func(d workloadiam.SpanDecision) error {
			if !db.TryInsertAdmittedUserSpan(s, d) {
				t.Fatal("queue refused")
			}
			return nil
		})
	}
	if err := offer(); err != nil {
		t.Fatal(err)
	}
	first := <-db.writer.telemetry
	if err := a.Publish(1, compile(2, false), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := offer(); err == nil {
		t.Fatal("revoke admitted")
	}
	if err := a.Publish(2, compile(3, true), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := offer(); err != nil {
		t.Fatal(err)
	}
	third := <-db.writer.telemetry
	first.enqueuedAt = 0
	third.enqueuedAt = 0
	if reflect.DeepEqual(first, third) {
		t.Fatal("REVISION_PROVENANCE_MISSING: revision 1 and revision 3 jobs identical")
	}
	if first.spanDecision.OwnerID() != r.FunctionID || first.spanDecision.Revision() != 1 || third.spanDecision.Revision() != 3 {
		t.Fatal("wrong deciding provenance")
	}
	third.spanDecision = first.spanDecision
	if !reflect.DeepEqual(first, third) {
		t.Fatal("otherwise identical host jobs differ beyond deciding revision")
	}
	db.writer.telemetryBytes.Add(-int64(first.bytes + third.bytes))
	if db.WriterStats().TelemetryBytes != 0 {
		t.Fatal("reservation leak")
	}
}

func TestSpanDecisionRealRetryAndShed(t *testing.T) {
	a, c, r, compile := spanDecisionFixture(t)
	db := newTestDB(t)
	seedFunctionRow(t, db, r.FunctionID)
	db.writer.stop(5 * time.Second)
	db.writer = newAsyncWriter(db)
	t.Cleanup(db.writer.start)
	s := UserSpan{ID: "retry-decision", FunctionID: r.FunctionID, ExecutionID: r.ExecutionID, TraceID: "host-trace",
		ParentSpanID: "host-parent", Name: "original", StartedAt: time.Now().UTC(), Status: "ok"}
	if err := a.EmitSpanObserved(func() workloadiam.Context { return c }, r, func(d workloadiam.SpanDecision) error {
		if !db.TryInsertAdmittedUserSpan(s, d) {
			t.Fatal("offer rejected")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	job := <-db.writer.telemetry
	wantBytes := db.WriterStats().TelemetryBytes
	if job.spanDecision.Revision() != 1 || wantBytes != int64(job.bytes) {
		t.Fatal("initial job provenance/bytes missing")
	}
	conn, err := db.write.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	waits := db.write.Stats().WaitCount
	done := make(chan []writeJob, 1)
	batch := []writeJob{job}
	go func() { done <- db.writer.commit(batch, writeTelemetry) }()
	if !waitFor(t, time.Second, func() bool { return db.write.Stats().WaitCount > waits }) {
		t.Fatal("real writer not waiting")
	}
	published := make(chan error, 1)
	go func() { published <- a.Publish(1, compile(2, false), func() error { return nil }) }()
	select {
	case err := <-published:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("writer blocked revoke")
	}
	if err := a.Publish(2, compile(3, true), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	var retry []writeJob
	select {
	case retry = <-done:
	case <-time.After(txBudget + 3*time.Second):
		t.Fatal("real writer timeout did not return retry")
	}
	if len(retry) != 1 || retry[0].spanDecision.Revision() != 1 || db.WriterStats().TelemetryBytes != wantBytes {
		t.Fatal("retry rebound decision or reservation")
	}
	retry = retainRetryBatch(batch, retry)
	if retry[0].spanDecision != job.spanDecision || !reflect.DeepEqual(retry[0].args, job.args) {
		t.Fatal("retry copy changed immutable job")
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if next := db.writer.commit(retry, writeTelemetry); len(next) != 0 {
		t.Fatal("retry did not persist")
	}
	db.writer.telemetryBytes.Add(-wantBytes)
	if retry[0].spanDecision.Revision() != 1 {
		t.Fatal("persistence reinterpreted revision")
	}
	rows, err := db.ListUserSpansByExecution(r.ExecutionID)
	if err != nil || len(rows) != 1 || rows[0].TraceID != s.TraceID || rows[0].Name != s.Name {
		t.Fatal("retry attribution changed")
	}
	// Deletion sheds the original decision, without an IAM callback.
	db.writer.telemetryBytes.Add(wantBytes)
	if err := db.DeleteFunction(t.Context(), r.FunctionID); err != nil {
		t.Fatal(err)
	}
	if next := db.writer.commit(retry, writeTelemetry); len(next) != 0 {
		t.Fatal("deleted job retained")
	}
	db.writer.telemetryBytes.Add(-wantBytes)
	if retry[0].spanDecision.Revision() != 1 || db.WriterStats().DeletedWrites != 1 || db.WriterStats().TelemetryBytes != 0 {
		t.Fatal("shed changed provenance/accounting")
	}
	t.Log("revision 1 retained across actual SQLite timeout, revoke 2, regrant 3, retry copy, persistence and deletion shed")
}
