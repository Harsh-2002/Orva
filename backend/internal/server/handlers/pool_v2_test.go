package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
)

func poolV2Handler(t *testing.T) (*PoolConfigHandler, *database.Function) {
	t.Helper()
	db, err := database.New(filepath.Join(t.TempDir(), "pool-handler.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	reg := registry.New(db)
	fn := &database.Function{Name: "pool-handler", Runtime: "node", Entrypoint: "handler.js", MemoryMB: 64, CPUs: 1, TimeoutMS: 1000, Status: "active"}
	if err := reg.Set(fn); err != nil {
		t.Fatal(err)
	}
	return &PoolConfigHandler{DB: db, Registry: reg}, fn
}

func putPoolConfig(t *testing.T, h *PoolConfigHandler, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPut, "/api/v1/pool/config", bytes.NewReader(b))
	w := httptest.NewRecorder()
	h.Upsert(w, req)
	return w
}

func TestPoolConfigRejectsRemovedTargetConcurrency(t *testing.T) {
	h, fn := poolV2Handler(t)
	w := putPoolConfig(t, h, map[string]any{"function_id": fn.ID, "target_concurrency": 4})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Pool Controller v2") || !strings.Contains(w.Body.String(), "VALIDATION") {
		t.Fatalf("missing migration guidance: %s", w.Body.String())
	}
}

func TestPoolConfigScaleContract(t *testing.T) {
	h, fn := poolV2Handler(t)
	w := putPoolConfig(t, h, map[string]any{"function_id": fn.ID, "scale_to_zero": true})
	if w.Code != http.StatusOK {
		t.Fatalf("enable: %d %s", w.Code, w.Body.String())
	}
	var cfg database.PoolConfig
	if err := json.Unmarshal(w.Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.MinWarm != 0 || !cfg.ScaleToZero {
		t.Fatalf("enable result: %+v", cfg)
	}

	w = putPoolConfig(t, h, map[string]any{"function_id": fn.ID, "scale_to_zero": false})
	if w.Code != http.StatusOK {
		t.Fatalf("disable: %d %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.MinWarm != 1 || cfg.ScaleToZero {
		t.Fatalf("disable result: %+v", cfg)
	}

	w = putPoolConfig(t, h, map[string]any{"function_id": fn.ID, "scale_to_zero": true, "min_warm": 2})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("incompatible pair status=%d", w.Code)
	}

	w = putPoolConfig(t, h, map[string]any{"function_id": fn.ID, "min_warm": 0})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("active min zero status=%d", w.Code)
	}
}
