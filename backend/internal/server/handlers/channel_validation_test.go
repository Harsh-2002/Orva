package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

const missingFunctionID = "00000000-0000-7000-8000-000000000000"

func seedChannelFunction(t *testing.T, db *database.Database, id, name string) {
	t.Helper()
	fn := &database.Function{
		ID: id, Name: name, Runtime: "node", Entrypoint: "handler.js", Status: "active",
		EnvVars: map[string]string{}, TimeoutMS: 30000, MemoryMB: 128, CPUs: 0.5,
	}
	if err := db.InsertFunction(fn); err != nil {
		t.Fatalf("insert function %s: %v", name, err)
	}
}

func postChannel(h *ChannelHandler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/channels", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.Create(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) (code, message string) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("response is not an error envelope: %v: %s", err, rec.Body.String())
	}
	return env.Error.Code, env.Error.Message
}

// A channel naming a function that does not exist is the caller's mistake.
// With a single id the collision check returns early, so the bad id used to
// reach the binding step and surface as 500 INTERNAL; with two ids the lookup
// error came back as a 400 carrying raw database text.
func TestChannelWithUnknownFunctionIsAClientError(t *testing.T) {
	db := newTestDB(t)
	const realID = "01a02b52-e3dd-7421-b015-981c667d2039"
	seedChannelFunction(t, db, realID, "real-fn")
	h := &ChannelHandler{DB: db}

	cases := []struct {
		name string
		ids  string
	}{
		{"single unknown id", `["` + missingFunctionID + `"]`},
		{"known and unknown", `["` + realID + `","` + missingFunctionID + `"]`},
		{"unknown first", `["` + missingFunctionID + `","` + realID + `"]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := postChannel(h, `{"name":"c-`+strings.ReplaceAll(c.name, " ", "-")+`","function_ids":`+c.ids+`}`)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			code, msg := errorCode(t, rec)
			if code != "INVALID_FUNCTION" {
				t.Errorf("code = %q, want INVALID_FUNCTION", code)
			}
			if !strings.Contains(msg, missingFunctionID) || !strings.Contains(msg, "not found") {
				t.Errorf("message must name the missing function, got %q", msg)
			}
			if strings.Contains(strings.ToLower(msg), "sql") {
				t.Errorf("message leaks database text: %q", msg)
			}
		})
	}

	rows, err := db.ListChannels()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("a rejected create left %d channel row(s) behind", len(rows))
	}
}

// Listing a function twice would collide on the junction table's primary key and
// come back as 500 "failed to bind functions" — a request mistake, not a fault.
func TestChannelWithDuplicateFunctionIDIsAClientError(t *testing.T) {
	db := newTestDB(t)
	const realID = "01a02b52-e3dd-7421-b015-981c667d2039"
	seedChannelFunction(t, db, realID, "real-fn")
	h := &ChannelHandler{DB: db}

	rec := postChannel(h, `{"name":"dup","function_ids":["`+realID+`","`+realID+`"]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	code, msg := errorCode(t, rec)
	if code != "VALIDATION" || !strings.Contains(msg, realID) || !strings.Contains(msg, "more than once") {
		t.Errorf("got code=%q message=%q, want VALIDATION naming the repeated id", code, msg)
	}
	if rows, err := db.ListChannels(); err != nil || len(rows) != 0 {
		t.Errorf("a rejected create left channel rows behind: %d err=%v", len(rows), err)
	}
}

func TestChannelWithKnownFunctionsStillCreates(t *testing.T) {
	db := newTestDB(t)
	seedChannelFunction(t, db, "01a02b52-e3dd-7421-b015-981c667d2039", "alpha-fn")
	seedChannelFunction(t, db, "01a02b52-e3dd-7421-b015-981c667d203a", "beta-fn")
	h := &ChannelHandler{DB: db}

	rec := postChannel(h, `{"name":"ok","function_ids":["01a02b52-e3dd-7421-b015-981c667d2039","01a02b52-e3dd-7421-b015-981c667d203a"]}`)
	if rec.Code < 200 || rec.Code >= 300 {
		t.Fatalf("valid create returned %d: %s", rec.Code, rec.Body.String())
	}
}

// Validating existence first must not hide a real collision.
func TestChannelToolNameCollisionIsStillReported(t *testing.T) {
	db := newTestDB(t)
	seedChannelFunction(t, db, "01a02b52-e3dd-7421-b015-981c667d2039", "stripe-charge")
	seedChannelFunction(t, db, "01a02b52-e3dd-7421-b015-981c667d203a", "stripe_charge")
	h := &ChannelHandler{DB: db}

	rec := postChannel(h, `{"name":"clash","function_ids":["01a02b52-e3dd-7421-b015-981c667d2039","01a02b52-e3dd-7421-b015-981c667d203a"]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if code, _ := errorCode(t, rec); code != "TOOL_NAME_COLLISION" {
		t.Errorf("code = %q, want TOOL_NAME_COLLISION", code)
	}
}

// The update path shares the validation and must leave the bundle untouched.
func TestSetChannelFunctionsRejectsUnknownFunction(t *testing.T) {
	db := newTestDB(t)
	const realID = "01a02b52-e3dd-7421-b015-981c667d2039"
	seedChannelFunction(t, db, realID, "real-fn")
	h := &ChannelHandler{DB: db}

	created := postChannel(h, `{"name":"bundle","function_ids":["`+realID+`"]}`)
	if created.Code < 200 || created.Code >= 300 {
		t.Fatalf("create returned %d: %s", created.Code, created.Body.String())
	}
	var out struct {
		Channel struct {
			ID string `json:"id"`
		} `json:"channel"`
		ID string `json:"id"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	chID := out.Channel.ID
	if chID == "" {
		chID = out.ID
	}
	if chID == "" {
		t.Fatalf("create response carries no channel id: %s", created.Body.String())
	}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/channels/"+chID+"/functions",
		strings.NewReader(`{"function_ids":["`+missingFunctionID+`"]}`))
	req.SetPathValue("id", chID)
	rec := httptest.NewRecorder()
	h.SetFunctions(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if code, msg := errorCode(t, rec); code != "INVALID_FUNCTION" || strings.Contains(strings.ToLower(msg), "sql") {
		t.Errorf("got code=%q message=%q, want INVALID_FUNCTION without database text", code, msg)
	}
	if n, err := db.CountChannelFunctions(chID); err != nil || n != 1 {
		t.Errorf("a rejected update changed the bundle: count=%d err=%v, want 1", n, err)
	}
}
