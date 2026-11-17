package workloadpolicy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
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
