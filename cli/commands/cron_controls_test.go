package commands

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCronControlResumeUsesInspectedCASAndOwnerWithoutRead(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/api/v1/functions/019a8000-0000-7000-8000-000000000002/cron/schedule/resume" {
			t.Error("unexpected lookup or route", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var body map[string]string
		if json.Unmarshal(raw, &body) != nil || body["expected_config_revision"] != "9007199254740993" || body["expected_authority_generation"] != "7" {
			t.Error("CAS was rounded or replaced")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"schedule","authority_state":"active","config_revision":"9007199254740994"}`)
	}))
	defer server.Close()
	root := NewRoot()
	root.SetArgs([]string{"--endpoint", server.URL, "--api-key", "fixture-key", "--output", "json", "cron", "resume", "schedule", "--fn", "019a8000-0000-7000-8000-000000000002", "--config-revision", "9007199254740993", "--authority-generation", "7"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("admin-only path performed a read", calls)
	}
}
