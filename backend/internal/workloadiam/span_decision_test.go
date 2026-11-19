package workloadiam

import (
	"testing"
	"time"
)

func TestSpanDecisionGateAndOwnedValue(t *testing.T) {
	e := compiled(t, withStatements(statement(Execution, activeExecution(), SpansEmit)))
	a, err := NewAuthority(0, []*Evaluator{e})
	if err != nil {
		t.Fatal(err)
	}
	c := executionContext()
	r := request(OpSpansEmit, activeExecution())
	r.ExecutionID = c.Lease.ExecutionID
	entered, release := make(chan struct{}), make(chan struct{})
	admitted := make(chan error, 1)
	var captured SpanDecision
	go func() {
		admitted <- a.EmitSpanObserved(func() Context { return c }, r, func(d SpanDecision) error {
			captured = d
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	nextMeta := e.Metadata()
	nextMeta.Revision++
	next, err := Compile(nextMeta, withStatements())
	if err != nil {
		t.Fatal(err)
	}
	revoked := make(chan error, 1)
	go func() { revoked <- a.Publish(e.Metadata().Revision, next, func() error { return nil }) }()
	select {
	case <-revoked:
		t.Fatal("revoke crossed decision handoff")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	if err := <-admitted; err != nil {
		t.Fatal(err)
	}
	if err := <-revoked; err != nil {
		t.Fatal(err)
	}
	if captured.OwnerID() != owner || captured.Revision() != e.Metadata().Revision {
		t.Fatal("captured wrong deciding evaluator")
	}
	copy := captured
	captured = SpanDecision{}
	if copy.Revision() != e.Metadata().Revision {
		t.Fatal("snapshot aliased caller value")
	}
	called := false
	if err := a.EmitSpanObserved(func() Context { return c }, r, func(SpanDecision) error { called = true; return nil }); err == nil || called {
		t.Fatal("revoked emission got provenance")
	}
	if err := a.EmitSpanObserved(func() Context { return c }, request(OpKVGet, key("x")), func(SpanDecision) error { return nil }); err == nil {
		t.Fatal("span seam authorized another operation")
	}
}
