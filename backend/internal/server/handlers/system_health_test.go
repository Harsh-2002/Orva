package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

func newTestDB(t *testing.T) *database.Database {
	t.Helper()
	db, err := database.New(filepath.Join(t.TempDir(), "health.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func callHealth(t *testing.T, h *SystemHandler) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/v1/system/health", nil)
	w := httptest.NewRecorder()
	h.Health(w, req)
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v (raw: %s)", err, w.Body.String())
	}
	return w.Code, body
}

// TestHealthNsjailAbsentStays200 is the CI-contract guard: even when the
// configured nsjail binary does not exist, Health() must report the sandbox as
// "unavailable" while keeping HTTP 200 + status "healthy". A hard-fail on
// missing nsjail would red the e2e ready-poll (which boots the server before a
// sandbox is guaranteed) and contradict Orva's "starts without nsjail" design.
func TestHealthNsjailAbsentStays200(t *testing.T) {
	h := &SystemHandler{
		DB:        newTestDB(t),
		StartTime: time.Now(),
		NsjailBin: "/definitely/not/a/real/nsjail/path",
	}

	code, body := callHealth(t, h)
	if code != http.StatusOK {
		t.Fatalf("nsjail absent: got status %d, want 200 (body: %v)", code, body)
	}
	if body["status"] != "healthy" {
		t.Errorf("status = %v, want healthy", body["status"])
	}
	sb, _ := body["sandbox"].(map[string]any)
	if sb["runtime"] != "unavailable" {
		t.Errorf("sandbox.runtime = %v, want unavailable", sb["runtime"])
	}
}

// TestHealthDBDownReturns503 confirms the hard gate at the handler level.
func TestHealthDBDownReturns503(t *testing.T) {
	db := newTestDB(t)
	if err := db.ReadDB().Close(); err != nil {
		t.Fatalf("close read pool: %v", err)
	}
	h := &SystemHandler{DB: db, StartTime: time.Now()}

	code, body := callHealth(t, h)
	if code != http.StatusServiceUnavailable {
		t.Fatalf("dead DB: got status %d, want 503 (body: %v)", code, body)
	}
	if body["status"] != "degraded" {
		t.Errorf("status = %v, want degraded", body["status"])
	}
}
