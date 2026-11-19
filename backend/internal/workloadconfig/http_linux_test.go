//go:build linux

package workloadconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
)

const wireExpected = `{"function_version":"1","artifact_revision":"1","artifact_epoch":"1","generation":"019a9872-0000-7000-8000-000000000001","policy_revision":"1","authority_epoch":"1","approved_artifact":"unbound"}`

func TestConfigurationWireStrict(t *testing.T) {
	valid := `{"expected":` + wireExpected + `,"current_artifact":"` + strings.Repeat("a", 64) + `","environment":{"CONFIG":"😀"}}`
	if _, _, _, err := decodeRequest([]byte(valid), false); err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{
		"unknown":               []byte(strings.Replace(valid, `"environment":`, `"actor":"author","environment":`, 1)),
		"root-duplicate":        []byte(strings.Replace(valid, `"expected":`, `"expected":{},"expected":`, 1)),
		"expected-duplicate":    []byte(strings.Replace(valid, `"function_version":"1"`, `"function_version":"1","function_version":"1"`, 1)),
		"env-duplicate":         []byte(strings.Replace(valid, `"CONFIG":"😀"`, `"CONFIG":"a","CONFIG":"b"`, 1)),
		"unknown-expected":      []byte(strings.Replace(valid, `"authority_epoch"`, `"permissions"`, 1)),
		"numeric-revision":      []byte(strings.Replace(valid, `"policy_revision":"1"`, `"policy_revision":1`, 1)),
		"noncanonical-revision": []byte(strings.Replace(valid, `"policy_revision":"1"`, `"policy_revision":"01"`, 1)),
		"null":                  []byte(strings.Replace(valid, `"CONFIG":"😀"`, `"CONFIG":null`, 1)),
		"surrogate":             []byte(strings.Replace(valid, `😀`, `\ud800`, 1)),
		"utf8":                  bytes.Replace([]byte(valid), []byte("😀"), []byte{0xff}, 1),
		"trailing":              []byte(valid + ` {}`),
		"oversize":              bytes.Repeat([]byte(" "), MaxRequestBytes+1),
		"install-extra":         []byte(valid),
	} {
		t.Run(name, func(t *testing.T) {
			_, _, _, err := decodeRequest(raw, name == "install-extra")
			if !errors.Is(err, database.ErrWorkloadArtifactInvalid) {
				t.Fatal("malformed request accepted")
			}
		})
	}
	if _, _, _, err := decodeRequest([]byte(`{"expected":`+wireExpected+`}`), true); err != nil {
		t.Fatal(err)
	}
}

func TestConfigurationHTTPDoesNotEchoBodyOrTrustPrincipalPermissions(t *testing.T) {
	h := &HTTPAdapter{Service: &Service{}, Authenticate: func(*http.Request) (*auth.Principal, error) {
		return &auth.Principal{Kind: auth.KindAPIKey, ID: "019a9872-0000-7000-8000-000000000001", Perms: auth.PermSet{"admin": true}}, nil
	}}
	r := httptest.NewRequest("PUT", "/configuration/environment", strings.NewReader(`{"environment":"secret-canary","actor":"forged"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 400 || strings.Contains(w.Body.String(), "canary") || strings.Contains(w.Body.String(), "forged") {
		t.Fatal("unsafe error body")
	}
	var body any
	if json.Unmarshal(w.Body.Bytes(), &body) != nil {
		t.Fatal("non-JSON error")
	}
	h.Authenticate = func(*http.Request) (*auth.Principal, error) { return &auth.Principal{Kind: auth.KindChannel}, nil }
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal("channel accepted")
	}
}

func TestConfigurationExtendedWireStrict(t *testing.T) {
	prefix := `{"expected":` + wireExpected + `,"current_artifact":"` + strings.Repeat("a", 64) + `",`
	settings := `{"timeout_ms":1000,"memory_mb":128,"cpus":1,"sdk":true,"network_mode":"none","max_concurrency":1,"concurrency_policy":"queue","auth_mode":"none","rate_limit_per_min":0}`
	for _, tt := range []struct {
		raw              string
		settings, remove bool
	}{
		{prefix + `"secret":{"key":"K","value":"V"}}`, false, false},
		{prefix + `"secret":{"key":"K"}}`, false, true},
		{prefix + `"settings":` + settings + `,"environment":{}}`, true, false},
	} {
		if _, _, _, _, _, err := decodeExtendedRequest([]byte(tt.raw), tt.settings, tt.remove); err != nil {
			t.Fatal("valid extended request rejected")
		}
		for _, bad := range []string{
			strings.Replace(tt.raw, `"current_artifact":`, `"actor":"forged","current_artifact":`, 1),
			strings.Replace(tt.raw, `"expected":`, `"expected":{},"expected":`, 1),
			strings.Replace(tt.raw, `"key":"K"`, `"key":"K","key":"K"`, 1),
			strings.Replace(tt.raw, `"sdk":true`, `"sdk":null`, 1),
			strings.Replace(tt.raw, `"cpus":1`, `"cpus":1,"cpus":2`, 1),
			strings.Replace(tt.raw, `"value":"V"`, `"value":"\ud800"`, 1),
		} {
			if bad == tt.raw {
				continue
			}
			if _, _, _, _, _, err := decodeExtendedRequest([]byte(bad), tt.settings, tt.remove); err == nil {
				t.Fatal("invalid extended request accepted")
			}
		}
	}
}
