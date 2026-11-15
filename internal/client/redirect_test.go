package client

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The API key is a custom header, which Go does not strip on a cross-host
// redirect; CheckRedirect does. The NoTimeout client is a separate http.Client
// and used to lose it.
func TestAPIKeyNotForwardedAcrossHostsOnRedirect(t *testing.T) {
	for _, noTimeout := range []bool{false, true} {
		var gotKey string
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotKey = r.Header.Get("X-Orva-API-Key")
		}))
		redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.URL+"/elsewhere", http.StatusFound)
		}))

		resp, err := NewClient(redirector.URL, "secret-key").Send(Request{Path: "/x", NoTimeout: noTimeout})
		if err != nil {
			t.Fatalf("noTimeout=%v: %v", noTimeout, err)
		}
		resp.Body.Close()
		if gotKey != "" {
			t.Errorf("noTimeout=%v: the API key reached another host on a redirect", noTimeout)
		}
		target.Close()
		redirector.Close()
	}
}
