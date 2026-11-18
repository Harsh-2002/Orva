package workloadhistory

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
)

func TestHistoryWireStrictAndCapacity(t *testing.T) {
	f := setup(t)
	mux := http.NewServeMux()
	(&HTTPAdapter{Service: f.s, Authenticate: func(*http.Request) (*auth.Principal, error) {
		return &auth.Principal{Kind: f.ref.Kind, ID: f.ref.ID}, nil
	}}).Register(mux)
	bad := [][]byte{
		[]byte(`{"limit":1,"limit":2}`), []byte(`{"limit":1,"actor_id":"author"}`),
		[]byte(`{"Limit":1}`), []byte(`{"limit":1} {}`), []byte(`null`),
		[]byte("{\"limit\":1,\"bad\":\"\xff\"}"), []byte(`{"limit":1,"after":{"id":"x","kind":"job","kind":"cron"}}`),
		[]byte(strings.Repeat(" ", MaxRequestBytes) + `{"limit":1}`),
	}
	for i, raw := range bad {
		if _, err := f.s.Execute(f.ctx, "list", raw); err == nil {
			t.Fatalf("service accepted malformed request %d", i)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/work-history/list", bytes.NewReader(raw)))
		if rec.Code != 400 {
			t.Fatalf("HTTP accepted malformed request %d: %d", i, rec.Code)
		}
	}
	out, err := f.s.Execute(f.ctx, "capacity", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	capacity := out.(*database.HistoryCapacity)
	if capacity.MaxRows != 100000 || capacity.MaxBytes != 64<<20 || capacity.PurgeEligibilityDays != 90 || !capacity.CanManage {
		t.Fatal("incorrect capacity contract")
	}
	raw, _ := json.Marshal(capacity)
	if !bytes.Contains(raw, []byte(`"max_rows":"100000"`)) || !bytes.Contains(raw, []byte(`"rows_reserved":"0"`)) {
		t.Fatal("capacity counters lost decimal strings")
	}
	f.exec(t, `UPDATE api_keys SET permissions='["read"]' WHERE id=?`, f.ref.ID)
	out, err = f.s.Execute(f.ctx, "capacity", []byte(`{}`))
	if err != nil || out.(*database.HistoryCapacity).CanManage {
		t.Fatal("read-only capacity promoted management", err)
	}
	f.exec(t, `UPDATE api_keys SET permissions='["write"]' WHERE id=?`, f.ref.ID)
	if _, err = f.s.Execute(f.ctx, "capacity", []byte(`{}`)); err == nil {
		t.Fatal("capacity exposed without READ")
	}
}
