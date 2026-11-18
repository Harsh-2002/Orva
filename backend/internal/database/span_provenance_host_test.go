package database_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/sdkhost"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

func provenanceFrame(w io.Writer, b []byte) error {
	h := make([]byte, 4)
	binary.BigEndian.PutUint32(h, uint32(len(b)))
	if _, err := w.Write(h); err != nil {
		return err
	}
	_, err := w.Write(b)
	return err
}

func provenanceRead(r io.Reader) ([]byte, error) {
	h := make([]byte, 4)
	if _, err := io.ReadFull(r, h); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(h)
	if n > sandbox.MaxSDKFrame {
		return nil, fmt.Errorf("oversized fixture frame")
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}

// This unit fixture supplies a real host-owned dispatch, not runtime acceptance.
func TestSpanProvenanceFakeJail(t *testing.T) {
	if !strings.Contains(strings.Join(os.Args, " "), "--span-provenance-jail") {
		return
	}
	sdk := os.NewFile(3, "sdk")
	if provenanceFrame(os.Stdout, []byte(`{"type":"ready"}`)) != nil {
		os.Exit(10)
	}
	lease := ""
	for {
		b, err := provenanceRead(os.Stdin)
		if err != nil {
			os.Exit(0)
		}
		var f struct{ Type, Lease string }
		if json.Unmarshal(b, &f) != nil {
			os.Exit(11)
		}
		switch f.Type {
		case "lease":
			lease = f.Lease
		case "request":
			b := []byte(fmt.Sprintf(`{"id":1,"op":"kv.get","lease":%q,"a":{"key":"fixture"}}`, lease))
			if provenanceFrame(sdk, b) != nil {
				os.Exit(12)
			}
			if _, err := provenanceRead(sdk); err != nil {
				os.Exit(13)
			}
			if provenanceFrame(os.Stdout, []byte(`{"type":"response","statusCode":200}`)) != nil {
				os.Exit(14)
			}
		}
	}
}

func TestSpanProvenanceActualHostAdmission(t *testing.T) {
	const owner = "019a8dd0-0000-7000-8000-000000000001"
	const execution = "019a8dd0-0000-7000-8000-000000000003"
	db, err := database.New(filepath.Join(t.TempDir(), "fixture.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	take := database.PauseSpanOffersForTest(t, db)
	compile := func(rev uint64, grant bool) *workloadiam.Evaluator {
		t.Helper()
		p := workloadiam.Policy{SchemaVersion: 1, Allow: []workloadiam.Statement{}, Inbound: []workloadiam.Acceptance{}}
		if grant {
			p.Allow = append(p.Allow, workloadiam.Statement{Context: workloadiam.Execution,
				Actions: []workloadiam.Action{workloadiam.SpansEmit}, Resource: workloadiam.Resource{OwnExecution: &workloadiam.ExecutionResource{}}})
		}
		e, err := workloadiam.Compile(workloadiam.Metadata{OwnerID: owner, Revision: rev, ApprovedArtifact: "fixture-artifact"}, p)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	a, err := workloadiam.NewAuthority(0, []*workloadiam.Evaluator{compile(1, true)})
	if err != nil {
		t.Fatal(err)
	}
	h := sdkhost.New(db, sdkhost.WithIAM(a), sdkhost.WithIAMCurrentArtifact(func(string, string) bool { return true }))
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "node/usr/local/bin"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node/opt/orva"), 0755); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for path, text := range map[string]string{
		"node/usr/local/bin/node": "", "node/opt/orva/adapter.js": "// orva-sdk-proto:1",
		"jail": fmt.Sprintf("#!/bin/sh\nexec %q -test.run=^TestSpanProvenanceFakeJail$ -- --span-provenance-jail \"$@\"\n", exe),
	} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(text), 0755); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	callers := make(chan sandbox.SDKCaller, 1)
	release := make(chan struct{})
	w, err := sandbox.Spawn(ctx, sandbox.ExecConfig{FunctionID: owner, ArtifactRevision: "fixture-artifact", Language: sandbox.Node,
		RootfsDir: root, CodeDir: root, NsjailBin: filepath.Join(root, "jail"),
		WorkloadTransitionHook: func(change func()) {
			if err := a.ChangeHostState(change); err != nil {
				t.Error(err)
			}
		},
		SDKHandler: func(ctx context.Context, c sandbox.SDKCaller, _ string, _ json.RawMessage) (any, *sandbox.SDKError) {
			callers <- c
			select {
			case <-release:
			case <-ctx.Done():
			}
			return nil, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Kill()
	if err := w.AwaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := w.Dispatch(sandbox.WithExecution(ctx, sandbox.ExecInfo{ExecutionID: execution,
			TraceID: strings.Repeat("a", 32), SpanID: strings.Repeat("b", 16), StartedAt: time.Unix(1700000000, 0)}), []byte(`{}`))
		done <- err
	}()
	var caller sandbox.SDKCaller
	select {
	case caller = <-callers:
	case <-ctx.Done():
		t.Fatal("missing host dispatch")
	}
	defer func() {
		close(release)
		select {
		case err := <-done:
			if err != nil {
				t.Error("fixture dispatch failed")
			}
		case <-time.After(3 * time.Second):
			t.Error("fixture dispatch did not finish")
		}
	}()
	raw := json.RawMessage(`{"name":"same","started_at":"2023-11-14T22:13:20Z"}`)
	emit := func() {
		t.Helper()
		result, e := h.Handle(ctx, caller, "spans.emit", raw)
		if e != nil || result.(map[string]string)["status"] != "queued" {
			t.Fatal("host offer refused")
		}
	}
	emit()
	first := take()
	if first.Decision.OwnerID() != owner || first.Decision.Revision() != 1 {
		t.Fatal("first offer missing deciding owner/revision")
	}
	if err := a.Publish(1, compile(2, false), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, e := h.Handle(ctx, caller, "spans.emit", raw); e == nil || e.Status != 403 {
		t.Fatal("revoke did not deny")
	}
	if err := a.Publish(2, compile(3, true), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	emit()
	third := take()
	if third.Decision.OwnerID() != owner || third.Decision.Revision() != 3 || first.Decision.Revision() != 1 {
		t.Fatal("deciding snapshot rebound")
	}
	if reflect.DeepEqual(first.Decision, third.Decision) {
		t.Fatal("distinct decisions collapsed")
	}
	if third.Bytes < first.Bytes || first.Bytes < 32+len(owner) {
		t.Fatal("provenance was not accounted")
	}
	for _, bad := range []string{`{"name":"x","decision_revision":1}`, `{"name":"x","owner_id":"foreign"}`} {
		if _, e := h.Handle(ctx, caller, "spans.emit", json.RawMessage(bad)); e == nil || e.Status != 400 {
			t.Fatal("author provenance claim accepted")
		}
	}
	legacy := sdkhost.New(db)
	if _, e := legacy.Handle(ctx, caller, "spans.emit", raw); e != nil {
		t.Fatal("legacy span refused")
	}
	old := take()
	if old.Decision != (workloadiam.SpanDecision{}) {
		t.Fatal("legacy span acquired fake IAM provenance")
	}
	if first.Bytes != old.Bytes+32+len(owner) {
		t.Fatal("provenance reservation delta differs")
	}
	for _, i := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11} {
		if !reflect.DeepEqual(first.Args[i], old.Args[i]) {
			t.Fatal("legacy attribution changed")
		}
	}
	t.Log("actual SDK host admission revisions 1/3 are distinct; revoke denies; legacy remains no-IAM; author claims reject")
}
