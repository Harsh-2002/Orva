//go:build linux

package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestWorkloadLifecycleREST(t *testing.T) {
	owner := ids.New()
	p := &auth.Principal{Kind: auth.KindAPIKey, ID: ids.New()}
	valid := `{"generation":"` + ids.New() + `","policy_revision":"1","authority_epoch":"2","confirm":true}`
	send := func(mux *http.ServeMux, id, op, body string, want int, slug string) {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/functions/"+id+"/"+op, strings.NewReader(body)))
		if rec.Code != want || !strings.Contains(rec.Body.String(), slug) {
			t.Fatalf("%s status=%d body=%s; want %d %s", op, rec.Code, rec.Body, want, slug)
		}
		if want != 404 && rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("lifecycle response was cacheable")
		}
	}
	for _, op := range []string{"pause", "resume"} {
		send(http.NewServeMux(), owner, op, valid, 404, "")
		mux := http.NewServeMux()
		h := &WorkloadLifecycleHandler{Publication: &builder.PublicationQueue{}, Authenticate: func(*http.Request) (*auth.Principal, error) { return p, nil }}
		h.Register(mux)
		send(mux, owner, op, valid, 503, `"UNAVAILABLE"`)
		send(mux, "name", op, valid, 400, `"VALIDATION"`)
		for _, raw := range []string{`{}`, valid + `{}`, strings.Repeat(" ", builder.MaxLifecycleRequestBytes) + valid,
			strings.Replace(valid, `true`, `false`, 1), strings.Replace(valid, `true`, `false,"confirm":true`, 1),
			strings.Replace(valid, `"2"`, `2`, 1), strings.Replace(valid, `true`, `true,"function_id":"`+owner+`"`, 1)} {
			send(mux, owner, op, raw, 400, `"VALIDATION"`)
		}
		p.Kind = auth.KindChannel
		send(mux, owner, op, valid, 401, `"UNAUTHORIZED"`)
		p.Kind = auth.KindAPIKey
		id := p.ID
		p.ID = "untrusted"
		send(mux, owner, op, valid, 403, `"FORBIDDEN"`)
		p.ID = id
		h.Publication = nil
		send(mux, owner, op, valid, 503, `"UNAVAILABLE"`)
	}
}
