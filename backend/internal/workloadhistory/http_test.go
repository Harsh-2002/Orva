package workloadhistory

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
)

func TestHistoryHTTPStrictAndReadBoundary(t *testing.T) {
	f := setup(t)
	if err := f.reg.Delete(f.ctx, f.target); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	(&HTTPAdapter{Service: f.s, Authenticate: func(r *http.Request) (*auth.Principal, error) {
		return &auth.Principal{Kind: "api_key", ID: f.ref.ID}, nil
	}}).Register(mux)
	call := func(path string, raw []byte, want int) {
		t.Helper()
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/work-history/"+path, bytes.NewReader(raw)))
		if rec.Code != want || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("HTTP %s: got %d want %d", path, rec.Code, want)
		}
	}
	call("list", []byte(`{"limit":100}`), 200)
	for _, raw := range [][]byte{
		[]byte(`{"limit":10,"limit":100}`), []byte(`{"limit":100,"actor":{"kind":"api_key","id":"forged"}}`),
		[]byte(`{"limit":100} {}`), append([]byte(`{"target_id":"`), 255),
	} {
		call("list", raw, 400)
	}
	f.exec(t, `UPDATE api_keys SET permissions='["write"]' WHERE id=?`, f.ref.ID)
	call("list", []byte(`{"limit":100}`), 403)
	f.exec(t, `UPDATE api_keys SET permissions='["read"]' WHERE id=?`, f.ref.ID)
	call("list", []byte(`{"limit":100}`), 200)
	raw, err := json.Marshal(map[string]any{"selected": []database.HistorySelector{{Kind: "job", ID: f.job.ID}}, "confirm": true})
	if err != nil {
		t.Fatal(err)
	}
	call("purge", raw, 403)
}
