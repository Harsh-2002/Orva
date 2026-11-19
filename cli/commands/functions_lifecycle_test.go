package commands

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestWorkloadLifecycleCLI(t *testing.T) {
	const owner = "019aaa00-0000-7000-8000-000000000001"
	const raw = `{"generation":"019aaa00-0000-7000-8000-000000000002", "policy_revision":"9007199254740993","authority_epoch":"2","confirm":true}`
	t.Setenv("NO_PROXY", "127.0.0.1")
	var calls atomic.Int32
	var redirect atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(r.Body)
		op := strings.TrimPrefix(r.URL.Path, "/api/v1/functions/"+owner+"/")
		if err != nil || r.Method != "POST" || (op != "pause" && op != "resume") || (string(body) != raw && string(body) != `{}`) {
			t.Errorf("unexpected outgoing request: %s %s %q", r.Method, r.URL.Path, body)
		}
		if string(body) == `{}` {
			http.Error(w, `{"error":{"message":"server requires confirm:true"}}`, http.StatusBadRequest)
			return
		}
		if redirect.Load() && op == "resume" {
			http.Redirect(w, r, "/api/v1/functions/"+owner+"/pause", http.StatusTemporaryRedirect)
			return
		}
		if op == "pause" {
			_, _ = io.WriteString(w, `{"function_status":"inactive","operator_paused":true}`)
			return
		}
		_, _ = io.WriteString(w, `{"function_status":"inactive","operator_paused":false,"installation_required":true,"review_required":true}`)
	}))
	defer srv.Close()
	t.Setenv("ORVA_ENDPOINT", srv.URL)
	t.Setenv("ORVA_API_KEY", "orva_fixture")
	run := func(op, id, path, input string, yes bool) (string, error) {
		t.Helper()
		root := NewRoot()
		out := &bytes.Buffer{}
		root.SetOut(out)
		root.SetIn(strings.NewReader(input))
		args := []string{"--quiet"}
		if yes {
			args = append(args, "--yes")
		}
		root.SetArgs(append(args, "functions", op, id, "--request", path))
		err := root.Execute()
		return out.String(), err
	}
	if _, err := run("pause", owner, "-", raw, false); err == nil || calls.Load() != 0 {
		t.Fatal("noninteractive mutation bypassed confirmation", err)
	}
	for _, input := range []string{"{", strings.Repeat(" ", workloadLifecycleLimit) + raw} {
		if _, err := run("resume", owner, "-", input, true); err == nil || calls.Load() != 0 {
			t.Fatal("invalid request reached server", err)
		}
	}
	if _, err := run("pause", "name", "-", raw, true); err == nil || calls.Load() != 0 {
		t.Fatal("name was accepted instead of exact UUID", err)
	}
	if _, err := run("pause", owner, "-", `{}`, true); err == nil || calls.Load() != 1 {
		t.Fatal("--yes supplied body confirmation or retried failure", err)
	}
	out, err := run("pause", owner, "-", raw, true)
	if err != nil || !strings.Contains(out, `"operator_paused": true`) || calls.Load() != 2 {
		t.Fatal("pause stdin request failed", err)
	}
	file := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	out, err = run("resume", owner, file, "", true)
	if err != nil || !strings.Contains(out, `"installation_required": true`) ||
		!strings.Contains(out, `"review_required": true`) || calls.Load() != 3 {
		t.Fatal("resume file request failed", err)
	}
	redirect.Store(true)
	if _, err := run("resume", owner, file, "", true); err == nil || calls.Load() != 4 {
		t.Fatal("redirect retargeted the reviewed operation", err)
	}
}
