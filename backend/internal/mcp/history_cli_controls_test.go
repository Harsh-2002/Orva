package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHistoryControlsSlimCLIUsesStrictService(t *testing.T) {
	f := historyTransportSetup(t)
	bin := filepath.Join(t.TempDir(), "orva")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "./cli/cmd/orva")
	build.Dir = "../../.."
	build.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOPROXY=off", "GOSUMDB=off", "CGO_ENABLED=0", "GOMAXPROCS=2")
	if _, err := build.Output(); err != nil {
		t.Fatal("build slim CLI:", err)
	}
	call := func(key, op string, raw []byte, denied bool) []byte {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), bin, "history", op, "--request", "-", "--quiet", "--yes")
		cmd.Env = []string{"HOME=" + t.TempDir(), "ORVA_ENDPOINT=" + f.server.URL, "ORVA_API_KEY=" + key}
		cmd.Stdin = bytes.NewReader(raw)
		out, err := cmd.Output()
		if (err != nil) != denied {
			t.Fatalf("CLI %s denied=%v want=%v", op, err != nil, denied)
		}
		if !denied && !json.Valid(out) {
			t.Fatal("CLI stdout is not JSON")
		}
		return out
	}
	out := call(f.admin, "list", []byte(`{"limit":100}`), false)
	if !bytes.Contains(out, []byte(f.jobID)) || bytes.Contains(out, []byte("unavailable-old-body")) {
		t.Fatal("CLI archive inspection contract")
	}
	out = call(f.reader, "capacity", []byte(`{}`), false)
	if !bytes.Contains(out, []byte(`"max_rows":"100000"`)) {
		t.Fatal("CLI lost decimal-string capacity")
	}
	get := []byte(`{"kind":"job","id":"` + f.jobID + `"}`)
	call(f.reader, "get", get, false)
	purge := []byte(`{"selected":[` + string(get) + `],"confirm":true}`)
	for _, spec := range []struct {
		op  string
		raw []byte
	}{{"purge", purge}, {"reissue", f.reissue("job")}} {
		call(f.reader, spec.op, spec.raw, true)
	}
	for _, raw := range [][]byte{
		[]byte(`{"limit":1,"limit":2}`), []byte(`{"limit":1,"actor_id":"author"}`),
		[]byte(`{"limit":1} {}`), []byte("{\"limit\":1,\"x\":\"\xff\"}"),
		[]byte(strings.Repeat(" ", (1<<20)+8192) + `{"limit":1}`),
	} {
		call(f.admin, "list", raw, true)
	}
	out = call(f.admin, "reissue", f.reissue("job"), false)
	var result struct {
		ID     string `json:"new_id"`
		Origin string `json:"origin"`
	}
	if json.Unmarshal(out, &result) != nil || result.ID == "" || result.ID == f.jobID || result.Origin != "operator" {
		t.Fatal("CLI reissue identity")
	}
	if f.count(t, `SELECT count(*) FROM work_history WHERE kind='reissue' AND original_id=?`, result.ID) != 1 {
		t.Fatal("CLI reissue missing audit")
	}
	f.exec(t, `UPDATE work_history SET eligible_at=? WHERE kind='job'`, time.Now().UTC().Add(-time.Hour))
	call(f.admin, "purge", purge, false)
	if f.count(t, `SELECT count(*) FROM work_history WHERE kind='job'`) != 0 {
		t.Fatal("CLI purge did not use history service")
	}
}
