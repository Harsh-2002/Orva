package commands

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHistoryCLIOriginalRawAndBounds(t *testing.T) {
	raw := `{"limit":1,"limit":2}`
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		if string(body) != raw || r.URL.Path != "/api/v1/work-history/list" {
			t.Error("CLI normalized original request")
		}
		_, _ = io.WriteString(w, `{"revision":"9007199254740993"}`)
	}))
	defer srv.Close()
	run := func(args []string, input string) (string, error) {
		root := NewRoot()
		out := &bytes.Buffer{}
		root.SetOut(out)
		root.SetIn(strings.NewReader(input))
		root.SetArgs(append([]string{"--endpoint", srv.URL, "--api-key", "orva_fixture", "--quiet"}, args...))
		err := root.Execute()
		return out.String(), err
	}
	out, err := run([]string{"history", "list", "--request", "-"}, raw)
	if err != nil || !strings.Contains(out, `"9007199254740993"`) || calls != 1 {
		t.Fatal("JSON output/raw dispatch failed", err)
	}
	for _, input := range []string{"{", strings.Repeat(" ", historyRequestLimit) + raw, "{\"\xff\":1}"} {
		if _, err := run([]string{"history", "list", "--request", "-"}, input); err == nil {
			t.Fatal("invalid request accepted")
		}
	}
	if _, err := run([]string{"history", "purge", "--request", "-"}, `{}`); err == nil {
		t.Fatal("noninteractive purge skipped confirmation")
	}
	if calls != 1 {
		t.Fatal("invalid/unconfirmed request reached server")
	}
}

func TestHistoryCLIFlagsFilesAndHelp(t *testing.T) {
	root := NewRoot()
	for _, op := range []string{"list", "get", "capacity", "purge", "reissue"} {
		cmd, _, err := root.Find([]string{"history", op})
		if err != nil || cmd.Name() != op || cmd.RunE == nil || cmd.ValidArgsFunction == nil {
			t.Fatalf("missing history command/completion: %s", op)
		}
		flag := cmd.Flags().Lookup("request")
		if flag == nil || len(flag.Annotations["cobra_annotation_bash_completion_filename_extensions"]) == 0 {
			t.Fatalf("missing request filename completion: %s", op)
		}
		if required := len(flag.Annotations["cobra_annotation_bash_completion_one_required_flag"]) > 0; required != (op == "get" || op == "purge" || op == "reissue") {
			t.Fatalf("incorrect required request flag: %s", op)
		}
		if !strings.Contains(cmd.Long, "Output is always JSON") || !strings.Contains(cmd.Long, "reviewed:true") {
			t.Fatalf("history help lost safety/output contract: %s", op)
		}
	}
	body := `{"selected":[{"kind":"job","id":"019a8000-0000-7000-8000-000000000001"}],"confirm":true}`
	file := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(file, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if string(raw) != body {
			t.Error("file request changed")
		}
		_, _ = io.WriteString(w, `{"purged":true}`)
	}))
	defer server.Close()
	// Package-level commands retain inherited flag pointers across test roots.
	if err := root.PersistentFlags().Set("endpoint", server.URL); err != nil {
		t.Fatal(err)
	}
	if err := root.PersistentFlags().Set("api-key", "orva_fixture"); err != nil {
		t.Fatal(err)
	}
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetArgs([]string{"--endpoint", server.URL, "--api-key", "orva_fixture", "--quiet", "--yes", "history", "purge", "--request", file})
	if err := root.Execute(); err != nil || !strings.Contains(out.String(), `"purged":true`) {
		t.Fatal("confirmed file request failed", err)
	}
}
