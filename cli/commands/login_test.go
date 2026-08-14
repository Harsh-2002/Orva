package commands

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// loginTestServer mirrors the real auth shape closely enough to catch a probe
// aimed at the wrong endpoint:
//   - /api/v1/auth/* bypasses authMiddleware entirely, and /auth/me then
//     demands a session_token cookie the CLI never has → always 401.
//   - /api/v1/runtimes sits behind the middleware and is satisfiable by a key.
func loginTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/auth/me", func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("session_token"); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/api/v1/runtimes", func(w http.ResponseWriter, r *http.Request) {
		switch r.Header.Get("X-Orva-API-Key") {
		case "orva_good":
			w.WriteHeader(http.StatusOK)
		case "orva_writeonly":
			w.WriteHeader(http.StatusForbidden)
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func runLoginCmd(t *testing.T, endpoint, key string) error {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("ORVA_CONFIG", cfg)
	cmd := loginCmd
	if err := cmd.Flags().Set("endpoint", endpoint); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("api-key", key); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Flags().Set("test", "true"); err != nil {
		t.Fatal(err)
	}
	return runLogin(cmd, nil)
}

// The regression: probing /api/v1/auth/me made --test reject every valid API
// key, because that route bypasses the auth middleware and wants a browser
// session cookie. A good key must verify and be saved.
func TestLoginTestAcceptsValidKey(t *testing.T) {
	srv := loginTestServer(t)
	if err := runLoginCmd(t, srv.URL, "orva_good"); err != nil {
		t.Fatalf("--test rejected a valid API key: %v", err)
	}
	if _, err := os.Stat(os.Getenv("ORVA_CONFIG")); err != nil {
		t.Fatalf("config not written for a valid key: %v", err)
	}
}

func TestLoginTestRejectsBadKey(t *testing.T) {
	srv := loginTestServer(t)
	if err := runLoginCmd(t, srv.URL, "orva_bogus"); err == nil {
		t.Fatal("--test accepted an invalid API key")
	}
	if _, err := os.Stat(os.Getenv("ORVA_CONFIG")); err == nil {
		t.Fatal("config written despite rejected credentials")
	}
}

// A key without "read" is still a genuine credential — the server
// authenticated it and answered 403. Saving it is correct.
func TestLoginTestAcceptsKeyLackingRead(t *testing.T) {
	srv := loginTestServer(t)
	if err := runLoginCmd(t, srv.URL, "orva_writeonly"); err != nil {
		t.Fatalf("--test rejected an authenticated key that lacks read: %v", err)
	}
	if _, err := os.Stat(os.Getenv("ORVA_CONFIG")); err != nil {
		t.Fatalf("config not written for a write-only key: %v", err)
	}
}

// A CLI newer than the server must not turn a missing endpoint into a refusal
// to save an otherwise good config.
func TestLoginTestToleratesOlderServer(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	if err := runLoginCmd(t, srv.URL, "orva_good"); err != nil {
		t.Fatalf("--test failed against a server without /api/v1/runtimes: %v", err)
	}
	if _, err := os.Stat(os.Getenv("ORVA_CONFIG")); err != nil {
		t.Fatalf("config not written against an older server: %v", err)
	}
}
