package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

// Creating a channel whose name is taken used to return 500 INTERNAL. A UNIQUE
// violation is the caller's mistake: it tells an operator the server broke, and
// tells retry logic the request is worth repeating, when repeating it can only
// fail the same way. Every sibling create (functions, fixtures, firewall rules)
// already returns 409.
func TestDuplicateChannelNameIsAConflictNotAServerError(t *testing.T) {
	db := newTestDB(t)
	fn := &database.Function{
		ID: "01a02b52-e3dd-7421-b015-981c667d2039", Name: "seed-fn", Runtime: "node",
		Entrypoint: "handler.js", Status: "active", EnvVars: map[string]string{},
		TimeoutMS: 30000, MemoryMB: 128, CPUs: 0.5,
	}
	if err := db.InsertFunction(fn); err != nil {
		t.Fatalf("insert function: %v", err)
	}
	h := &ChannelHandler{DB: db}

	body := `{"name":"dupe","function_ids":["` + fn.ID + `"]}`
	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/channels", strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.Create(rec, req)
		return rec
	}

	if first := post(); first.Code < 200 || first.Code >= 300 {
		t.Fatalf("first create returned %d, want 2xx: %s", first.Code, first.Body.String())
	}

	second := post()
	if second.Code == http.StatusInternalServerError {
		t.Fatalf("a duplicate name returned 500 INTERNAL; a name collision is a client error: %s",
			second.Body.String())
	}
	if second.Code != http.StatusConflict {
		t.Errorf("duplicate name returned %d, want 409: %s", second.Code, second.Body.String())
	}
	if !strings.Contains(second.Body.String(), "already exists") {
		t.Errorf("the 409 body does not say the name is taken: %s", second.Body.String())
	}
}
