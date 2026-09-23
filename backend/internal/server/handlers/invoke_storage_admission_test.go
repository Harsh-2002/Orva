package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
)

func TestInvokeRejectsStoragePressureBeforeSandboxRuns(t *testing.T) {
	db := newTestDB(t)
	const fnID = "019df200-7b00-7e00-9c00-aab1cd2e3f40"
	if err := db.InsertFunction(&database.Function{
		ID: fnID, Name: "storage-pressure", Runtime: "node", Entrypoint: "handler.js",
		TimeoutMS: 30000, MemoryMB: 64, CPUs: 0.5, EnvVars: map[string]string{},
		NetworkMode: "none", Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	leases := make([]*database.ExecutionLease, 0, db.WriterStats().CriticalCap)
	for range db.WriterStats().CriticalCap {
		lease, err := db.ReserveExecution(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, lease)
	}
	t.Cleanup(func() {
		for _, lease := range leases {
			lease.Cancel()
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/fn/"+fnID, nil).WithContext(ctx)
	w := httptest.NewRecorder()
	(&InvokeHandler{Registry: registry.New(db), DB: db}).ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("storage admission = %d, retry=%q", w.Code, w.Header().Get("Retry-After"))
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error.Code != "STORAGE_BACKPRESSURE" {
		t.Fatalf("storage admission body = %s, err=%v", w.Body.String(), err)
	}
	// Proxy is deliberately nil: reaching sandbox execution would panic.
}
