package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGetFunctionResolvesByName — GET /functions/{id} was the only function
// endpoint that would not resolve a name. The dashboard addresses functions
// by name in nine places while listing them with the default LIMIT of 20, so
// opening any function outside the twenty most recent reported
// "Function not found". The CLI had already worked around it.
func TestGetFunctionResolvesByName(t *testing.T) {
	const fnID = "019df200-7b00-7e00-9c00-aab1cd2e3f42"
	h, _ := newFnDiskHandler(t, fnID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/functions/victim", nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("lookup by name returned %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), fnID) {
		t.Errorf("response did not carry the resolved function: %s", rec.Body.String())
	}
}

func TestGetFunctionStillResolvesByID(t *testing.T) {
	const fnID = "019df200-7b00-7e00-9c00-aab1cd2e3f42"
	h, _ := newFnDiskHandler(t, fnID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/functions/"+fnID, nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("lookup by id returned %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetFunctionUnknownStillNotFound(t *testing.T) {
	h, _ := newFnDiskHandler(t, "019df200-7b00-7e00-9c00-aab1cd2e3f42")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/functions/no-such-thing", nil)
	rec := httptest.NewRecorder()
	h.Get(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown name returned %d, want 404", rec.Code)
	}
}
