package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
)

func TestFunctionsSDKPayload(t *testing.T) {
	const id = "019df200-7b00-7e00-9c00-aab1cd2e3f40"
	root := NewRoot()
	for _, tc := range []struct {
		name string
		args []string
		want any
	}{
		{"create default", []string{"create", "--name", "counter", "--runtime", "node"}, false},
		{"create enable", []string{"create", "--name", "counter", "--runtime", "node", "--sdk"}, true},
		{"create disable", []string{"create", "--name", "counter", "--runtime", "node", "--sdk=false"}, false},
		{"update preserve", []string{"update", id, "--description", "counter"}, nil},
		{"update enable", []string{"update", id, "--sdk=true"}, true},
		{"update disable", []string{"update", id, "--sdk=false"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ORVA_CONFIG", filepath.Join(t.TempDir(), "config.yaml"))
			requests := make(chan map[string]any, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				method, path := http.MethodPost, "/api/v1/functions"
				if tc.args[0] == "update" {
					method, path = http.MethodPut, path+"/"+id
				}
				if r.Method != method || r.URL.Path != path {
					t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.Path, method, path)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Errorf("decode request: %v", err)
				}
				requests <- body
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"` + id + `","name":"counter","runtime":"node","status":"active"}`))
			}))
			defer srv.Close()

			// Commands are package globals; isolate flag values and Changed bits.
			for _, cmd := range []*pflag.FlagSet{functionsCreateCmd.Flags(), functionsUpdateCmd.Flags()} {
				cmd.VisitAll(func(f *pflag.Flag) {
					value, changed := f.Value.String(), f.Changed
					t.Cleanup(func() { _ = f.Value.Set(value); f.Changed = changed })
					if err := f.Value.Set(f.DefValue); err != nil {
						t.Fatal(err)
					}
					f.Changed = false
				})
			}
			root.SetArgs(append([]string{"--endpoint", srv.URL, "--api-key", "test-key", "--quiet", "functions"}, tc.args...))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			select {
			case body := <-requests:
				got, present := body["sdk"]
				if got != tc.want || present != (tc.want != nil) {
					t.Errorf("sdk = %#v (present %v), want %#v", got, present, tc.want)
				}
				if _, present := body["network_mode"]; present {
					t.Error("SDK flag unexpectedly changed network_mode")
				}
			default:
				t.Fatal("no request received")
			}
		})
	}
}
