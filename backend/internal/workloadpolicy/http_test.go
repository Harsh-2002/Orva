package workloadpolicy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestHTTPAdapterContract(t *testing.T) {
	f := newFixture(t, false)
	principal := &auth.Principal{Kind: auth.KindAPIKey, ID: f.ref.ID, Perms: auth.PermSet{"read": true, "admin": true}}
	adapter := &HTTPAdapter{Service: f.service, Authenticate: func(*http.Request) (*auth.Principal, error) { return principal, nil }}
	mux := http.NewServeMux()
	mux.Handle("/api/v1/functions/{id}/permissions", adapter)
	mux.Handle("/api/v1/functions/{id}/permissions/review", adapter)
	path := "/api/v1/functions/" + f.owner + "/permissions"
	request := func(method, path string, body []byte, status int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewReader(body)))
		if w.Code != status {
			t.Fatalf("%s: got %d want %d: %s", method, w.Code, status, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("credential-scoped snapshot was cacheable")
		}
		return w
	}
	w := request("GET", path, nil, 200)
	var snapshot policySnapshot
	if err := json.Unmarshal(w.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.Function.ID != f.owner || snapshot.Host.Revision != "2" || len(snapshot.History) == 0 ||
		!snapshot.Host.Capabilities.CanManage || snapshot.History[0].Actor.Kind != "system" {
		t.Fatal("DTO lost server metadata/audit")
	}
	body := []byte(`{"expected":{"revision":"2","current_artifact":"` + f.state.Artifact + `","approved_artifact":"` + f.stored.ApprovedArtifact + `"},"policy":` + emptyPolicy + `}`)
	request("PUT", path, body, 200)
	request("PUT", path, body, 409)
	request("PUT", path, []byte(`{"actor":"author","policy":{},"expected":{}}`), 400)
	request("POST", path+"/review", []byte(`{"expected":{},"policy":{}}`), 400)
	for _, perms := range []auth.PermSet{{"read": true}, {"write": true}, nil} {
		principal.Perms = perms
		request("PUT", path, body, 403)
	}
	principal.Perms = auth.PermSet{"read": true}
	w = request("GET", path, nil, 200)
	if json.Unmarshal(w.Body.Bytes(), &snapshot) != nil || snapshot.Host.Capabilities.CanManage {
		t.Fatal("read-only capability widened")
	}
	principal.Kind = auth.KindChannel
	request("GET", path, nil, 401)
	principal.Kind, principal.ID = auth.KindAPIKey, ""
	request("GET", path, nil, 403)
	principal = nil
	request("GET", path, nil, 401)
}

func TestHTTPVisibleTargetsAreReadScopedAndBounded(t *testing.T) {
	f := newFixture(t, false)
	p := &auth.Principal{Kind: auth.KindAPIKey, ID: f.ref.ID, Perms: auth.PermSet{"read": true}}
	target := FunctionTarget{ID: ids.New(), Label: "visible active function"}
	calls := 0
	rows := []FunctionTarget{target}
	var sourceErr error
	h := &HTTPAdapter{Service: f.service, Authenticate: func(*http.Request) (*auth.Principal, error) { return p, nil },
		Targets: func(ctx context.Context, got *auth.Principal) ([]FunctionTarget, error) {
			calls++
			if got != p || !got.Perms.Has("read") {
				t.Fatal("target source lost authenticated scope")
			}
			if _, ok := auth.OperatorFromContext(ctx); !ok {
				t.Fatal("target source lost trusted operator reference")
			}
			return rows, sourceErr
		}}
	get := func(want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "/permissions", nil)
		r.SetPathValue("id", f.owner)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("got %d want %d", w.Code, want)
		}
		return w
	}
	var snapshot policySnapshot
	if err := json.Unmarshal(get(200).Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Targets) != 1 || snapshot.Targets[0].FunctionTarget != target || snapshot.Targets[0].State != "resolved" || snapshot.Host.Capabilities.CanManage {
		t.Fatal("target picker metadata widened capability or lost identity")
	}
	p.Perms = auth.PermSet{"invoke": true}
	get(403)
	p.Kind = auth.KindChannel
	get(401)
	if calls != 1 {
		t.Fatal("targets enumerated without operator read")
	}
	p.Kind, p.Perms = auth.KindAPIKey, auth.PermSet{"read": true}
	for _, bad := range [][]FunctionTarget{{target, target}, {{ID: "invalid", Label: "hidden"}}, {{ID: target.ID}}, make([]FunctionTarget, 257)} {
		rows = bad
		get(503)
	}
	rows = []FunctionTarget{target}
	sourceErr = errors.New("private source details")
	w := get(503)
	if bytes.Contains(w.Body.Bytes(), []byte("private source details")) {
		t.Fatal("metadata source error leaked")
	}
	record, err := f.db.GetIAMPolicy(t.Context(), f.owner)
	if err != nil || record.Revision != f.stored.Revision {
		t.Fatal("metadata reads changed policy counters", err)
	}
}

func TestHTTPMutationRechecksCredentialAfterAuthentication(t *testing.T) {
	f := newFixture(t, false)
	adapter := &HTTPAdapter{Service: f.service, Authenticate: func(*http.Request) (*auth.Principal, error) {
		if err := f.db.DeleteAPIKey(f.ref.ID); err != nil {
			t.Fatal(err)
		}
		return &auth.Principal{Kind: auth.KindAPIKey, ID: f.ref.ID, Perms: auth.PermSet{"read": true, "admin": true}}, nil
	}}
	body := []byte(`{"expected":{"revision":"2","current_artifact":"` + f.state.Artifact + `","approved_artifact":"` + f.stored.ApprovedArtifact + `"},"policy":` + emptyPolicy + `}`)
	req := httptest.NewRequest("PUT", "/api/v1/functions/"+f.owner+"/permissions", bytes.NewReader(body))
	req.SetPathValue("id", f.owner)
	w := httptest.NewRecorder()
	adapter.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("revoked authenticated credential committed: %d", w.Code)
	}
	row, err := f.db.GetIAMPolicy(t.Context(), f.owner)
	if err != nil || row.Revision != f.stored.Revision {
		t.Fatal("credential refusal changed stored authority", err)
	}
}
