package sdkhost

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

const iamOwner = "019a8dd0-0000-7000-8000-000000000001"
const iamOther = "019a8dd0-0000-7000-8000-000000000002"
const iamExecution = "019a8dd0-0000-7000-8000-000000000003"

func iamStatement(phase workloadiam.Phase, prefix string, actions ...workloadiam.Action) workloadiam.Statement {
	return workloadiam.Statement{Context: phase, Actions: actions, Resource: workloadiam.Resource{OwnKV: &workloadiam.KVResource{Prefix: &prefix}}}
}

func iamEvaluator(t *testing.T, revision uint64, artifact string, statements ...workloadiam.Statement) *workloadiam.Evaluator {
	t.Helper()
	e, err := workloadiam.Compile(workloadiam.Metadata{OwnerID: iamOwner, Revision: revision, ApprovedArtifact: artifact}, workloadiam.Policy{
		SchemaVersion: 1, Allow: append([]workloadiam.Statement{}, statements...), Inbound: []workloadiam.Acceptance{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func iamAuthority(t *testing.T, statements ...workloadiam.Statement) *workloadiam.Authority {
	t.Helper()
	a, err := workloadiam.NewAuthority(0, []*workloadiam.Evaluator{iamEvaluator(t, 1, "reviewed", statements...)})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func iamDB(t *testing.T) *database.Database {
	t.Helper()
	db := newDB(t)
	t.Cleanup(func() { _ = db.Close() })
	for i, id := range []string{iamOwner, iamOther} {
		if err := db.InsertFunction(&database.Function{ID: id, Name: fmt.Sprintf("iam-%d", i), Runtime: "node", Entrypoint: "handler.js", TimeoutMS: 30000, MemoryMB: 64, CPUs: .5, EnvVars: map[string]string{}, NetworkMode: "none", Status: "active"}); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func iamCall(t *testing.T, h *Host, ctx context.Context, caller sandbox.SDKCaller, op, args string, status int) any {
	t.Helper()
	r, e := h.Handle(ctx, caller, op, json.RawMessage(args))
	got := 200
	if e != nil {
		got = e.Status
	}
	if got != status {
		t.Fatalf("%s: result=%v error=%v want status %d", op, r, e, status)
	}
	if status == 403 && (r != nil || e.Code != "IAM_DENIED" || e.Message != "SDK operation denied") {
		t.Fatalf("non-generic denial: %v %v", r, e)
	}
	return r
}

func TestIAMExplicitOptInAndMetrics(t *testing.T) {
	db := iamDB(t)
	ctx := context.Background()
	c := sandbox.SDKCaller{FunctionID: iamOwner}
	legacy := New(db)
	iamCall(t, legacy, ctx, c, "kv.put", `{"key":"A%","value":1}`, 200)
	iamCall(t, legacy, ctx, c, "kv.batch", `{"ops":[]}`, 200)
	page := iamCall(t, legacy, ctx, c, "kv.list", `{"prefix":"a","limit":1}`, 200).(map[string]any)
	if len(page["keys"].([]listEntry)) != 1 {
		t.Fatal("legacy LIKE/case behavior changed")
	}
	cm := &captureMetrics{}
	h := New(db, WithIAM(nil), WithMetrics(cm))
	for _, op := range []string{"kv.get", "kv.put", "kv.delete", "kv.list", "kv.incr", "kv.cas", "kv.batch", "invoke", "invoke.stream.open", "invoke.stream.next", "jobs.enqueue", "crons.upsert", "spans.emit"} {
		iamCall(t, h, ctx, c, op, `{}`, 403)
	}
	if len(cm.seen) != 13 {
		t.Fatalf("metrics counted %d operations", len(cm.seen))
	}
	for _, stat := range h.Stats() {
		if stat.Count > 1 || stat.Errors != stat.Count {
			t.Fatalf("double counted or missing error: %+v", stat)
		}
	}
	a := iamAuthority(t, iamStatement(workloadiam.Execution, "", workloadiam.KVGet, workloadiam.KVPut))
	h = New(db, WithIAM(a))
	c.Exec = &sandbox.ExecInfo{ExecutionID: iamExecution, Lease: "forged"}
	iamCall(t, h, ctx, c, "kv.get", `{"key":"A%"}`, 403)
	iamCall(t, h, ctx, c, "kv.put", `{"key":"A%","value":2}`, 403)
	iamCall(t, h, ctx, sandbox.SDKCaller{}, "kv.get", `{"key":"A%"}`, 403)
	entry, err := db.KVGet(iamOwner, "A%")
	if err != nil || string(entry.Value) != "1" {
		t.Fatalf("denied write changed data: %v %v", entry, err)
	}
}

func iamWriteFrame(w io.Writer, b []byte) error {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(b)))
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(b)
	return err
}

func iamReadFrame(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > sandbox.MaxSDKFrame {
		return nil, fmt.Errorf("invalid test frame size")
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}

// Runs only as Spawn's fake jail, using its real inherited private socketpair.
func TestIAMFakeJail(t *testing.T) {
	if !strings.Contains(strings.Join(os.Args, " "), "--iam-fake-jail") {
		return
	}
	sdk := os.NewFile(3, "sdk")
	id := 0
	call := func(lease string) {
		id++
		b := []byte(fmt.Sprintf(`{"id":%d,"op":"kv.get","lease":%q,"a":{"key":"config/k"}}`, id, lease))
		if iamWriteFrame(sdk, b) != nil {
			os.Exit(10)
		}
		if _, err := iamReadFrame(sdk); err != nil {
			os.Exit(11)
		}
	}
	call("")
	if iamWriteFrame(os.Stdout, []byte(`{"type":"ready"}`)) != nil {
		os.Exit(12)
	}
	lease := ""
	for {
		b, err := iamReadFrame(os.Stdin)
		if err != nil {
			os.Exit(0)
		}
		var f struct{ Type, Lease string }
		if json.Unmarshal(b, &f) != nil {
			os.Exit(13)
		}
		switch f.Type {
		case "lease":
			lease = f.Lease
		case "request":
			call(lease)
			if iamWriteFrame(os.Stdout, []byte(`{"type":"response","statusCode":200}`)) != nil {
				os.Exit(14)
			}
		case "quit":
			os.Exit(0)
		}
	}
}

type iamCaptured struct {
	ctx     context.Context
	caller  sandbox.SDKCaller
	release func()
}

type iamWorker struct {
	w     *sandbox.Worker
	ctx   context.Context
	calls chan iamCaptured
}

func spawnIAMWorker(t *testing.T, h *Host, a *workloadiam.Authority) (*iamWorker, iamCaptured) {
	t.Helper()
	root := t.TempDir()
	for _, dir := range []string{"node/usr/local/bin", "node/opt/orva"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for path, text := range map[string]string{
		"node/usr/local/bin/node": "", "node/opt/orva/adapter.js": "// orva-sdk-proto:1",
		"fake-jail": fmt.Sprintf("#!/bin/sh\nexec %q -test.run=^TestIAMFakeJail$ -- --iam-fake-jail \"$@\"\n", exe),
	} {
		if err := os.WriteFile(filepath.Join(root, path), []byte(text), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	f := &iamWorker{ctx: ctx, calls: make(chan iamCaptured, 4)}
	exited := make(chan struct{})
	f.w, err = sandbox.Spawn(ctx, sandbox.ExecConfig{
		FunctionID: iamOwner, ArtifactRevision: "reviewed", Language: sandbox.Node, RootfsDir: root, CodeDir: root, NsjailBin: filepath.Join(root, "fake-jail"),
		WorkloadTransitionHook: func(change func()) {
			if err := a.ChangeHostState(change); err != nil {
				t.Error(err)
			}
		},
		OnExit: func() { close(exited) },
		SDKHandler: func(ctx context.Context, c sandbox.SDKCaller, op string, args json.RawMessage) (any, *sandbox.SDKError) {
			release := make(chan struct{})
			var once sync.Once
			f.calls <- iamCaptured{ctx, c, func() { once.Do(func() { close(release) }) }}
			select {
			case <-release:
			case <-ctx.Done():
			}
			return h.Handle(ctx, c, op, args)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = f.w.Kill()
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			t.Error("fake jail was not reaped")
		}
	})
	return f, f.next(t)
}

func (f *iamWorker) next(t *testing.T) iamCaptured {
	t.Helper()
	select {
	case c := <-f.calls:
		t.Cleanup(c.release)
		return c
	case <-f.ctx.Done():
		t.Fatal("no SDK caller from Spawn", f.ctx.Err())
		return iamCaptured{}
	}
}

func (f *iamWorker) ready(t *testing.T, boot iamCaptured) {
	t.Helper()
	boot.release()
	if err := f.w.AwaitReady(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func (f *iamWorker) execution(t *testing.T) (iamCaptured, func()) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		_, _, err := f.w.Dispatch(sandbox.WithExecution(f.ctx, sandbox.ExecInfo{ExecutionID: iamExecution}), []byte(`{}`))
		done <- err
	}()
	c := f.next(t)
	return c, func() {
		c.release()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-f.ctx.Done():
			t.Fatal("dispatch did not complete")
		}
	}
}

func TestIAMTrustedWorkerKVAndLifecycle(t *testing.T) {
	db := iamDB(t)
	all := iamStatement(workloadiam.Execution, "config/", workloadiam.KVGet, workloadiam.KVPut, workloadiam.KVDelete, workloadiam.KVList, workloadiam.KVIncr, workloadiam.KVCAS)
	bootGrant := iamStatement(workloadiam.Bootstrap, "config/", workloadiam.KVGet, workloadiam.KVList)
	a := iamAuthority(t, all, bootGrant)
	h := New(db, WithIAM(a))
	if err := db.KVPut(iamOwner, "config/k", []byte(`1`), nil); err != nil {
		t.Fatal(err)
	}
	if err := db.KVPut(iamOther, "config/only-other", []byte(`99`), nil); err != nil {
		t.Fatal(err)
	}
	f, boot := spawnIAMWorker(t, h, a)
	call := func(c iamCaptured, op, args string, status int) any {
		return iamCall(t, h, c.ctx, c.caller, op, args, status)
	}
	call(boot, "kv.get", `{"key":"config/k"}`, 200)
	call(boot, "kv.list", `{"prefix":"config/"}`, 200)
	call(boot, "kv.batch", `{"ops":[{"op":"get","key":"config/k"}]}`, 200)
	call(boot, "kv.get", `{"key":"elsewhere"}`, 403)
	call(boot, "kv.put", `{"key":"config/k","value":2}`, 403)
	call(boot, "kv.batch", `{"ops":[{"op":"get","key":"config/k"},{"op":"put","key":"config/k","value":2}]}`, 403)
	f.ready(t, boot)
	call(boot, "kv.get", `{"key":"config/k"}`, 403)
	c, finish := f.execution(t)
	call(boot, "kv.get", `{"key":"config/k"}`, 403)
	call(c, "kv.get", `{"key":"config/k"}`, 200)
	call(c, "kv.get", `{"key":"config/only-other"}`, 404)
	call(c, "kv.put", `{"key":"config/new","value":3}`, 200)
	call(c, "kv.delete", `{"key":"config/new"}`, 200)
	call(c, "kv.incr", `{"key":"config/k","delta":2}`, 200)
	call(c, "kv.cas", `{"key":"config/k","expected":3,"new":4}`, 200)
	call(c, "kv.batch", `{"ops":[{"op":"put","key":"config/batch","value":1},{"op":"get","key":"config/batch"},{"op":"delete","key":"config/batch"}]}`, 200)
	call(c, "kv.batch", `{"ops":[]}`, 403)
	call(c, "kv.batch", `{"ops":[{"op":"put","key":"config/no-partial","value":1},{"op":"get","key":"forbidden"}]}`, 403)
	if _, err := db.KVGet(iamOwner, "config/no-partial"); err != database.ErrKVNotFound {
		t.Fatalf("partial batch mutation: %v", err)
	}
	for _, op := range []string{"invoke", "invoke.stream.open", "jobs.enqueue", "crons.upsert", "spans.emit"} {
		call(c, op, `{}`, 403)
		call(c, op, "\xff", 403)
	}
	call(c, "invoke.stream.next", `{"handle":"unowned"}`, 403)
	call(c, "invoke.stream.next", `{}`, 400)
	call(c, "invoke.stream.next", "\xff", 400)
	call(c, "kv.get", `{"key":"config/k","function_id":"`+iamOther+`"}`, 400)
	wrong := c.caller
	wrong.FunctionID = iamOther
	iamCall(t, h, c.ctx, wrong, "kv.get", `{"key":"config/k"}`, 403)
	forged := c.caller
	forged.Exec = &sandbox.ExecInfo{ExecutionID: "forged", Lease: "forged"}
	iamCall(t, h, c.ctx, forged, "kv.get", `{"key":"config/k"}`, 200)
	cancelled, cancel := context.WithCancel(c.ctx)
	cancel()
	iamCall(t, h, cancelled, c.caller, "kv.put", `{"key":"config/k","value":88}`, 403)
	revision := uint64(1)
	publish := func(artifact string, statements ...workloadiam.Statement) {
		t.Helper()
		if err := a.Publish(revision, iamEvaluator(t, revision+1, artifact, statements...), func() error { return nil }); err != nil {
			t.Fatal(err)
		}
		revision++
	}
	publish("reviewed", iamStatement(workloadiam.Execution, "config/", workloadiam.KVIncr, workloadiam.KVCAS))
	call(c, "kv.incr", `{"key":"config/k"}`, 403)
	call(c, "kv.cas", `{"key":"config/k","expected":0,"new":5}`, 403)
	publish("reviewed", iamStatement(workloadiam.Execution, "config/", workloadiam.KVIncr, workloadiam.KVCAS), iamStatement(workloadiam.Execution, "elsewhere/", workloadiam.KVGet))
	call(c, "kv.incr", `{"key":"config/k"}`, 403)
	call(c, "kv.cas", `{"key":"config/k","expected":0,"new":5}`, 403)
	publish("different-artifact", all)
	call(c, "kv.get", `{"key":"config/k"}`, 403)
	publish("reviewed")
	call(c, "kv.get", `{"key":"config/k"}`, 403)
	// Revocation (even a nil authority) cannot prevent owned cleanup, but
	// changing the lease or channel still cannot close somebody else's handle.
	table, tableErr := sessionTable(c.ctx)
	if tableErr != nil {
		t.Fatal(tableErr)
	}
	table.tomb["owned"] = c.caller.Exec.Lease
	cleanupHost := New(db, WithIAM(nil))
	iamCall(t, cleanupHost, c.ctx, c.caller, "invoke.stream.close", `{"handle":"owned"}`, 200)
	wrongLease := c.caller
	wrongLease.Exec = &sandbox.ExecInfo{Lease: "other-lease"}
	iamCall(t, cleanupHost, c.ctx, wrongLease, "invoke.stream.close", `{"handle":"owned"}`, 404)
	otherSession := sandbox.WithSDKSession(c.ctx, &sandbox.SDKSession{})
	iamCall(t, cleanupHost, otherSession, c.caller, "invoke.stream.close", `{"handle":"owned"}`, 404)
	publish("reviewed", all, bootGrant)
	call(c, "kv.get", `{"key":"config/k"}`, 200)
	finish()
	call(c, "kv.get", `{"key":"config/k"}`, 403)
	call(boot, "kv.get", `{"key":"config/k"}`, 403)
	next, finishNext := f.execution(t)
	call(c, "kv.get", `{"key":"config/k"}`, 403)
	call(next, "kv.get", `{"key":"config/k"}`, 200)
	finishNext()
	_ = f.w.Kill()
	call(next, "kv.get", `{"key":"config/k"}`, 403)
}

func TestIAMLiteralListAndCursorBounds(t *testing.T) {
	db := iamDB(t)
	a := iamAuthority(t, iamStatement(workloadiam.Execution, "", workloadiam.KVList))
	h := New(db, WithIAM(a))
	f, boot := spawnIAMWorker(t, h, a)
	f.ready(t, boot)
	c, finish := f.execution(t)
	defer finish()
	keys := []string{"p%a", "p%b", "pXa", "p_a", "p_b", "pZa", "Aa", "aa", "é/1", "é/2", "é/1"}
	for _, key := range keys {
		if err := db.KVPut(iamOwner, key, []byte(`1`), nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.KVPut(iamOther, "p%other", []byte(`99`), nil); err != nil {
		t.Fatal(err)
	}
	list := func(prefix, cursor string, limit, status int) map[string]any {
		t.Helper()
		args := jb(map[string]any{"prefix": prefix, "cursor": cursor, "limit": limit})
		r := iamCall(t, h, c.ctx, c.caller, "kv.list", args, status)
		if r == nil {
			return nil
		}
		return r.(map[string]any)
	}
	for prefix, want := range map[string][]string{"p%": {"p%a", "p%b"}, "p_": {"p_a", "p_b"}, "A": {"Aa"}, "a": {"aa"}, "é/": {"é/1", "é/2"}} {
		var got []string
		cursor := ""
		for {
			page := list(prefix, cursor, 1, 200)
			for _, entry := range page["keys"].([]listEntry) {
				got = append(got, entry.Key)
			}
			next, _ := page["next_cursor"].(string)
			if next == "" {
				break
			}
			if next == cursor || len(got) > len(keys) {
				t.Fatal("cursor did not advance")
			}
			last, err := decodeIAMCursor(iamOwner, prefix, next)
			if err != nil || last != got[len(got)-1] {
				t.Fatalf("cursor not bound: %q %v", last, err)
			}
			cursor = next
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("prefix %q: %v want %v", prefix, got, want)
		}
	}
	cursor := list("p%", "", 1, 200)["next_cursor"].(string)
	list("p_", cursor, 1, 400)
	for _, bad := range []string{"p%a", "!!!", cursor[:2] + "\n" + cursor[2:], strings.Repeat("a", maxIAMCursorBytes+1), encodeIAMCursor(iamOther, "p%", "p%a"), encodeIAMCursor(iamOwner, "p%", "pXa"), base64.RawURLEncoding.EncodeToString([]byte("{\"v\":1,\"last\":\"\xff\"}")), base64.RawURLEncoding.EncodeToString([]byte(`{"v":2,"owner":"` + iamOwner + `","prefix":"p%","last":"p%a"}`))} {
		list("p%", bad, 1, 400)
	}
	iamCall(t, h, c.ctx, c.caller, "kv.list", "{\"prefix\":\"\xff\"}", 400)
	// Exercise both the sliced-query and reply-size early-return cursor paths.
	for i := range 250 {
		if err := db.KVPut(iamOwner, fmt.Sprintf("slice/%03d", i), []byte(`1`), nil); err != nil {
			t.Fatal(err)
		}
	}
	page := list("slice/", "", 200, 200)
	last, err := decodeIAMCursor(iamOwner, "slice/", page["next_cursor"].(string))
	if err != nil || last != "slice/199" || len(page["keys"].([]listEntry)) != 200 {
		t.Fatalf("sliced page: %q %v", last, err)
	}
	value, _ := json.Marshal(strings.Repeat("v", 60_000))
	for i := range 40 {
		if err := db.KVPut(iamOwner, fmt.Sprintf("big/%03d", i), value, nil); err != nil {
			t.Fatal(err)
		}
	}
	page = list("big/", "", 1000, 200)
	large := page["keys"].([]listEntry)
	last, err = decodeIAMCursor(iamOwner, "big/", page["next_cursor"].(string))
	if err != nil || last != large[len(large)-1].Key || len(large) >= 40 {
		t.Fatalf("reply-bound cursor: %q %v", last, err)
	}
	remaining := list("big/", page["next_cursor"].(string), 1000, 200)["keys"].([]listEntry)
	if len(large)+len(remaining) != 40 {
		t.Fatal("reply-size pagination lost keys")
	}
	if err := a.Publish(1, iamEvaluator(t, 2, "reviewed", iamStatement(workloadiam.Execution, "p%", workloadiam.KVList)), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	list("", "", 1, 403)
	list("p_", "", 1, 403)
	list("p%", cursor, 1, 200)
	exact := "p%a"
	if err := a.Publish(2, iamEvaluator(t, 3, "reviewed", workloadiam.Statement{Context: workloadiam.Execution, Actions: []workloadiam.Action{workloadiam.KVGet}, Resource: workloadiam.Resource{OwnKV: &workloadiam.KVResource{Key: &exact}}}), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	list("p%a", "", 1, 403)
}
