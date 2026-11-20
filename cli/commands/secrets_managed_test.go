package commands

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	secretsOwner    = "019aaa00-0000-7000-8000-000000000001"
	secretsArtifact = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	secretsValue    = "never-print-this-value"
)

const secretsExpected = `{"function_version":"3","artifact_revision":"4","artifact_epoch":"1","generation":"019aaa00-0000-7000-8000-0000000000a1","policy_revision":"5","authority_epoch":"2","approved_artifact":"iam:unbound"}`
const secretsSuccessor = `{"function_version":"4","artifact_revision":"5","artifact_epoch":"1","generation":"019aaa00-0000-7000-8000-0000000000a2","policy_revision":"6","authority_epoch":"3","approved_artifact":"iam:unbound"}`

type secretsServer struct {
	mu       sync.Mutex
	calls    []string
	bodies   map[string]string
	managed  bool
	artifact string
	status   map[string]int // path suffix -> forced status
	reply    string         // overrides the write reply
	srv      *httptest.Server
}

func newSecretsServer(t *testing.T, managed bool) *secretsServer {
	t.Helper()
	t.Setenv("NO_PROXY", "127.0.0.1")
	f := &secretsServer{managed: managed, artifact: secretsArtifact, bodies: map[string]string{}, status: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		key := r.Method + " " + r.URL.Path
		f.mu.Lock()
		f.calls = append(f.calls, key)
		f.bodies[key] = string(body)
		forced := 0
		for suffix, code := range f.status {
			if strings.HasSuffix(r.URL.Path, suffix) {
				forced = code
			}
		}
		f.mu.Unlock()
		base := "/api/v1/functions/" + secretsOwner
		switch {
		case r.URL.Path == "/api/v1/iam/capabilities":
			if !f.managed {
				http.NotFound(w, r)
				return
			}
			_, _ = io.WriteString(w, `{"managed":true}`)
		case forced != 0:
			w.WriteHeader(forced)
			_, _ = io.WriteString(w, `{"error":{"code":"IAM_CONFLICT","message":"changed"}}`)
		case r.Method == "GET" && r.URL.Path == base+"/configuration":
			_, _ = io.WriteString(w, `{"expected":`+secretsExpected+`,"review_required":false,"current_artifact":"`+f.artifact+`"}`)
		case r.URL.Path == base+"/configuration/secrets":
			reply := f.reply
			if reply == "" {
				reply = secretsSuccessor[:len(secretsSuccessor)-1] + `,"review_required":true}`
			}
			_, _ = io.WriteString(w, reply)
		case r.Method == "POST" && r.URL.Path == base+"/configuration/install":
			_, _ = io.WriteString(w, `{"current_artifact":"`+secretsArtifact+`","review_required":true}`)
		case r.URL.Path == base+"/secrets" || strings.HasPrefix(r.URL.Path, base+"/secrets/"):
			_, _ = io.WriteString(w, `{"ok":true}`)
		default:
			t.Errorf("unexpected request %s", key)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	t.Setenv("ORVA_ENDPOINT", f.srv.URL)
	t.Setenv("ORVA_API_KEY", "orva_fixture")
	return f
}

func (f *secretsServer) count(call string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c == call {
			n++
		}
	}
	return n
}

// runSecrets runs the real root command and returns everything it wrote to
// stdout and stderr. Commands are package-level values, so flag state is reset
// before and after to keep one run from leaking into the next.
func runSecrets(t *testing.T, args ...string) (string, error) {
	t.Helper()
	reset := func() {
		for _, c := range []*cobra.Command{secretsSetCmd, secretsDeleteCmd} {
			c.Flags().VisitAll(func(f *pflag.Flag) { _ = f.Value.Set(f.DefValue); f.Changed = false })
		}
	}
	reset()
	t.Cleanup(reset)
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	realErr := os.Stderr
	os.Stderr = w
	collected := make(chan string)
	go func() { b, _ := io.ReadAll(r); collected <- string(b) }()

	root := NewRoot()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(out)
	root.SetIn(strings.NewReader(""))
	root.SetArgs(append([]string{"secrets"}, args...))
	execErr := root.Execute()

	os.Stderr = realErr
	_ = w.Close()
	return out.String() + <-collected, execErr
}

func jsonKeys(t *testing.T, raw string) []string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("not an object: %q", raw)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func sameJSON(t *testing.T, a, b string) bool {
	t.Helper()
	var x, y any
	if json.Unmarshal([]byte(a), &x) != nil || json.Unmarshal([]byte(b), &y) != nil {
		return false
	}
	ax, _ := json.Marshal(x)
	by, _ := json.Marshal(y)
	return bytes.Equal(ax, by)
}

func TestManagedSecretSetUsesConfigurationRouteWithExactRevisions(t *testing.T) {
	f := newSecretsServer(t, true)
	const put = "PUT /api/v1/functions/" + secretsOwner + "/configuration/secrets"
	const install = "POST /api/v1/functions/" + secretsOwner + "/configuration/install"

	out, err := runSecrets(t, "set", secretsOwner, "API_KEY", "--value", secretsValue, "--install")
	if err != nil {
		t.Fatalf("managed set failed: %v\n%s", err, out)
	}
	if strings.Contains(out, secretsValue) {
		t.Fatal("the secret value was printed")
	}
	if f.count("POST /api/v1/functions/"+secretsOwner+"/secrets") != 0 {
		t.Fatal("managed server received the legacy secret route")
	}
	var write struct {
		Expected        json.RawMessage   `json:"expected"`
		CurrentArtifact string            `json:"current_artifact"`
		Secret          map[string]string `json:"secret"`
	}
	if err := json.Unmarshal([]byte(f.bodies[put]), &write); err != nil {
		t.Fatal(err)
	}
	if len(jsonKeys(t, f.bodies[put])) != 3 || !sameJSON(t, string(write.Expected), secretsExpected) ||
		write.CurrentArtifact != secretsArtifact || write.Secret["key"] != "API_KEY" || write.Secret["value"] != secretsValue || len(write.Secret) != 2 {
		t.Fatalf("write body does not carry the exact current revisions: %s", f.bodies[put])
	}
	var in struct {
		Expected json.RawMessage `json:"expected"`
	}
	if err := json.Unmarshal([]byte(f.bodies[install]), &in); err != nil || len(jsonKeys(t, f.bodies[install])) != 1 || !sameJSON(t, string(in.Expected), secretsSuccessor) {
		t.Fatalf("install did not use the fresh revisions from the write reply: %s", f.bodies[install])
	}
	if !strings.Contains(out, "does not approve") && !strings.Contains(out, "need review") {
		t.Fatalf("missing the permissions-review reminder: %s", out)
	}
}

func TestManagedSecretSetWithoutInstallLeavesInstallExplicit(t *testing.T) {
	f := newSecretsServer(t, true)
	out, err := runSecrets(t, "set", secretsOwner, "API_KEY", "--value", secretsValue)
	if err != nil {
		t.Fatalf("set failed: %v\n%s", err, out)
	}
	if f.count("POST /api/v1/functions/"+secretsOwner+"/configuration/install") != 0 {
		t.Fatal("installed without --install")
	}
	if !strings.Contains(out, "--install") {
		t.Fatalf("did not explain how to install: %s", out)
	}
}

func TestManagedSecretDeleteSendsOnlyTheKey(t *testing.T) {
	f := newSecretsServer(t, true)
	const del = "DELETE /api/v1/functions/" + secretsOwner + "/configuration/secrets"
	out, err := runSecrets(t, "--yes", "delete", secretsOwner, "API_KEY")
	if err != nil {
		t.Fatalf("delete failed: %v\n%s", err, out)
	}
	var write struct {
		Secret map[string]string `json:"secret"`
	}
	if err := json.Unmarshal([]byte(f.bodies[del]), &write); err != nil || len(write.Secret) != 1 || write.Secret["key"] != "API_KEY" || len(jsonKeys(t, f.bodies[del])) != 3 {
		t.Fatalf("delete body wrong: %s", f.bodies[del])
	}
	if f.count("DELETE /api/v1/functions/"+secretsOwner+"/secrets/API_KEY") != 0 {
		t.Fatal("managed server received the legacy delete route")
	}
}

func TestManagedSecretConflictIsReportedAndNeverRetried(t *testing.T) {
	f := newSecretsServer(t, true)
	f.status["/configuration/secrets"] = http.StatusConflict
	const put = "PUT /api/v1/functions/" + secretsOwner + "/configuration/secrets"
	out, err := runSecrets(t, "set", secretsOwner, "API_KEY", "--value", secretsValue, "--install")
	if err == nil || !strings.Contains(err.Error(), "nothing was sent again") {
		t.Fatalf("conflict was not reported clearly: %v", err)
	}
	if f.count(put) != 1 || f.count("GET /api/v1/functions/"+secretsOwner+"/configuration") != 1 {
		t.Fatalf("conflict was retried or re-read: %v", f.calls)
	}
	if f.count("POST /api/v1/functions/"+secretsOwner+"/configuration/install") != 0 {
		t.Fatal("installed after a failed write")
	}
	if strings.Contains(out+err.Error(), secretsValue) {
		t.Fatal("the secret value leaked into the error")
	}
}

func TestManagedSecretNeedsAnInstalledBuild(t *testing.T) {
	f := newSecretsServer(t, true)
	f.artifact = ""
	_, err := runSecrets(t, "set", secretsOwner, "API_KEY", "--value", secretsValue)
	if err == nil || !strings.Contains(err.Error(), "no installed build") {
		t.Fatalf("expected a clear refusal: %v", err)
	}
	if f.count("PUT /api/v1/functions/"+secretsOwner+"/configuration/secrets") != 0 {
		t.Fatal("wrote a secret without a current artifact")
	}
}

func TestManagedSecretUnverifiedReplyDoesNotInstall(t *testing.T) {
	for name, reply := range map[string]string{
		"not review required": strings.Replace(secretsSuccessor[:len(secretsSuccessor)-1]+`,"review_required":true}`, `"review_required":true`, `"review_required":false`, 1),
		"missing field":       `{"function_version":"4","review_required":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := newSecretsServer(t, true)
			f.reply = reply
			_, err := runSecrets(t, "set", secretsOwner, "API_KEY", "--value", secretsValue, "--install")
			if err == nil || !strings.Contains(err.Error(), "unverified") {
				t.Fatalf("expected an unverified-reply error: %v", err)
			}
			if f.count("POST /api/v1/functions/"+secretsOwner+"/configuration/install") != 0 {
				t.Fatal("installed from an unverified reply")
			}
		})
	}
}

func TestLegacySecretsAreUnchanged(t *testing.T) {
	f := newSecretsServer(t, false)
	if out, err := runSecrets(t, "set", secretsOwner, "API_KEY", "--value", secretsValue); err != nil {
		t.Fatalf("legacy set failed: %v\n%s", err, out)
	}
	if f.count("POST /api/v1/functions/"+secretsOwner+"/secrets") != 1 || f.count("GET /api/v1/functions/"+secretsOwner+"/configuration") != 0 {
		t.Fatalf("legacy set did not use the legacy route alone: %v", f.calls)
	}
	if out, err := runSecrets(t, "--yes", "delete", secretsOwner, "API_KEY"); err != nil {
		t.Fatalf("legacy delete failed: %v\n%s", err, out)
	}
	if f.count("DELETE /api/v1/functions/"+secretsOwner+"/secrets/API_KEY") != 1 {
		t.Fatalf("legacy delete did not use the legacy route: %v", f.calls)
	}
	before := len(f.calls)
	if _, err := runSecrets(t, "set", secretsOwner, "API_KEY", "--value", secretsValue, "--install"); err == nil || !strings.Contains(err.Error(), "managed") {
		t.Fatalf("--install was accepted on a legacy server: %v", err)
	}
	for _, c := range f.calls[before:] {
		if strings.Contains(c, "/secrets") {
			t.Fatalf("--install on a legacy server still changed secrets: %s", c)
		}
	}
}
