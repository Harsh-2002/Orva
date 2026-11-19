//go:build linux

package workloadconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
)

const MaxRequestBytes = 1 << 20

// HTTPAdapter is optional; the serving router never registers it.
type HTTPAdapter struct {
	Service      *Service
	Authenticate func(*http.Request) (*auth.Principal, error)
}

func (h *HTTPAdapter) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/functions/{id}/configuration", h)
	mux.Handle("PUT /api/v1/functions/{id}/configuration/environment", h)
	mux.Handle("POST /api/v1/functions/{id}/configuration/install", h)
	mux.Handle("PUT /api/v1/functions/{id}/configuration/settings", h)
	mux.Handle("PUT /api/v1/functions/{id}/configuration/secrets", h)
	mux.Handle("DELETE /api/v1/functions/{id}/configuration/secrets", h)
}

func (h *HTTPAdapter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h == nil || h.Service == nil || h.Authenticate == nil {
		configurationHTTPError(w, ErrUnavailable)
		return
	}
	p, err := h.Authenticate(r)
	if err != nil || p == nil || p.Kind == auth.KindChannel {
		respond.Error(w, 401, "UNAUTHORIZED", "Operator authentication required", "")
		return
	}
	ctx := auth.WithOperator(r.Context(), p)
	_, ok := auth.OperatorFromContext(ctx)
	if !ok {
		configurationHTTPError(w, database.ErrOperatorDenied)
		return
	}
	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/configuration") {
		out, err := h.Service.Execute(ctx, "inspect", r.PathValue("id"), []byte(`{}`))
		if err != nil {
			configurationHTTPError(w, err)
			return
		}
		respond.JSON(w, 200, out)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxRequestBytes))
	if err != nil {
		configurationHTTPError(w, database.ErrWorkloadArtifactInvalid)
		return
	}
	operation := ""
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/install"):
		operation = "install"
	case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/environment"):
		operation = "environment"
	case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/settings"):
		operation = "settings"
	case r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/secrets"):
		operation = "secret_upsert"
	case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/secrets"):
		operation = "secret_delete"
	default:
		w.WriteHeader(405)
		return
	}
	out, err := h.Service.Execute(ctx, operation, r.PathValue("id"), raw)
	if err != nil {
		configurationHTTPError(w, err)
		return
	}
	respond.JSON(w, 200, out)
}

type secretRequest struct{ key, value string }

func decodeExtendedRequest(raw []byte, settingsMode, remove bool) (database.WorkloadConfigurationExpected, string, map[string]string, *database.WorkloadExecutionSettings, *secretRequest, error) {
	invalid := func() (database.WorkloadConfigurationExpected, string, map[string]string, *database.WorkloadExecutionSettings, *secretRequest, error) {
		return database.WorkloadConfigurationExpected{}, "", nil, nil, nil, database.ErrWorkloadArtifactInvalid
	}
	if len(raw) > MaxRequestBytes || !utf8.Valid(raw) || !validEscapes(raw) {
		return invalid()
	}
	root, err := object(raw)
	count := 3
	if settingsMode {
		count = 4
	}
	if err != nil || len(root) != count || root["expected"] == nil || root["current_artifact"] == nil {
		return invalid()
	}
	envelope := map[string]json.RawMessage{"expected": root["expected"], "current_artifact": root["current_artifact"], "environment": json.RawMessage(`{}`)}
	if settingsMode {
		if root["environment"] == nil || root["settings"] == nil {
			return invalid()
		}
		envelope["environment"] = root["environment"]
	} else if root["secret"] == nil {
		return invalid()
	}
	base, _ := json.Marshal(envelope)
	expected, current, env, err := decodeRequest(base, false)
	if err != nil {
		return invalid()
	}
	if settingsMode {
		fields, err := object(root["settings"])
		allowed := map[string]bool{"timeout_ms": true, "memory_mb": true, "cpus": true, "network_mode": true, "sdk": true, "max_concurrency": true, "concurrency_policy": true, "auth_mode": true, "rate_limit_per_min": true}
		if err != nil || len(fields) != len(allowed) {
			return invalid()
		}
		for key, raw := range fields {
			if !allowed[key] || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return invalid()
			}
		}
		var settings database.WorkloadExecutionSettings
		if json.Unmarshal(root["settings"], &settings) != nil {
			return invalid()
		}
		return expected, current, env, &settings, nil, nil
	}
	fields, err := object(root["secret"])
	want := 2
	if remove {
		want = 1
	}
	if err != nil || len(fields) != want || fields["key"] == nil {
		return invalid()
	}
	secret := &secretRequest{}
	if bytes.Equal(bytes.TrimSpace(fields["key"]), []byte("null")) || json.Unmarshal(fields["key"], &secret.key) != nil {
		return invalid()
	}
	if !remove {
		if fields["value"] == nil || bytes.Equal(bytes.TrimSpace(fields["value"]), []byte("null")) || json.Unmarshal(fields["value"], &secret.value) != nil {
			return invalid()
		}
	}
	return expected, current, env, nil, secret, nil
}

func configurationHTTPError(w http.ResponseWriter, err error) {
	status, code, message := 503, "IAM_UNAVAILABLE", "Configuration publication unavailable"
	switch {
	case errors.Is(err, database.ErrOperatorDenied):
		status, code, message = 403, "FORBIDDEN", "Current operator authority required"
	case errors.Is(err, database.ErrWorkloadArtifactConflict), errors.Is(err, database.ErrIAMConflict):
		status, code, message = 409, "IAM_CONFLICT", "Configuration or authority changed; inspect again"
	case errors.Is(err, database.ErrWorkloadArtifactInvalid), errors.Is(err, database.ErrIAMInvalidPolicy):
		status, code, message = 400, "VALIDATION", "Invalid configuration request"
	}
	respond.Error(w, status, code, message, "")
}

func object(raw []byte) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	if t, err := d.Token(); err != nil || t != json.Delim('{') {
		return nil, database.ErrWorkloadArtifactInvalid
	}
	out := map[string]json.RawMessage{}
	for d.More() {
		t, err := d.Token()
		key, ok := t.(string)
		if err != nil || !ok || out[key] != nil || len(out) >= 1024 {
			return nil, database.ErrWorkloadArtifactInvalid
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return nil, database.ErrWorkloadArtifactInvalid
		}
		out[key] = value
	}
	if t, err := d.Token(); err != nil || t != json.Delim('}') {
		return nil, database.ErrWorkloadArtifactInvalid
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, database.ErrWorkloadArtifactInvalid
	}
	return out, nil
}

func decodeRequest(raw []byte, install bool) (database.WorkloadConfigurationExpected, string, map[string]string, error) {
	var expected database.WorkloadConfigurationExpected
	invalid := func() (database.WorkloadConfigurationExpected, string, map[string]string, error) {
		return expected, "", nil, database.ErrWorkloadArtifactInvalid
	}
	if len(raw) == 0 || len(raw) > MaxRequestBytes || !utf8.Valid(raw) || !validEscapes(raw) {
		return invalid()
	}
	root, err := object(raw)
	if err != nil {
		return invalid()
	}
	count := 3
	if install {
		count = 1
	}
	if len(root) != count || root["expected"] == nil {
		return invalid()
	}
	fields, err := object(root["expected"])
	if err != nil || len(fields) != 7 {
		return invalid()
	}
	values := map[string]string{}
	for key, value := range fields {
		var text string
		if string(value) == "null" || json.Unmarshal(value, &text) != nil {
			return invalid()
		}
		values[key] = text
	}
	number := func(key string) (uint64, error) {
		text := values[key]
		n, err := strconv.ParseUint(text, 10, 64)
		if err != nil || n == 0 || n > math.MaxInt64 || strconv.FormatUint(n, 10) != text {
			return 0, database.ErrWorkloadArtifactInvalid
		}
		return n, nil
	}
	v, err := number("function_version")
	if err != nil {
		return invalid()
	}
	if v > math.MaxInt32 {
		return invalid()
	}
	expected.FunctionVersion = int(v)
	for key, dest := range map[string]*uint64{
		"artifact_revision": &expected.ArtifactRevision, "artifact_epoch": &expected.ArtifactEpoch,
		"policy_revision": &expected.PolicyRevision, "authority_epoch": &expected.AuthorityEpoch,
	} {
		*dest, err = number(key)
		if err != nil {
			return invalid()
		}
	}
	expected.Generation, expected.ApprovedArtifact = values["generation"], values["approved_artifact"]
	if expected.Generation == "" || expected.ApprovedArtifact == "" {
		return invalid()
	}
	env := map[string]string{}
	current := ""
	if !install {
		if root["environment"] == nil || root["current_artifact"] == nil ||
			json.Unmarshal(root["current_artifact"], &current) != nil || len(current) != 64 {
			return invalid()
		}
		items, err := object(root["environment"])
		if err != nil {
			return invalid()
		}
		for key, raw := range items {
			var value string
			if string(raw) == "null" || json.Unmarshal(raw, &value) != nil {
				return invalid()
			}
			env[key] = value
		}
	}
	return expected, current, env, nil
}

// JSON's replacement of unpaired UTF-16 must not change an authored secret/env.
func validEscapes(data []byte) bool {
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return false
		}
		if data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return false
		}
		unit, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if unit >= 0xDC00 && unit <= 0xDFFF {
			return false
		}
		if unit < 0xD800 || unit > 0xDBFF {
			continue
		}
		if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return false
		}
		low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
		if err != nil || low < 0xDC00 || low > 0xDFFF {
			return false
		}
		i += 6
	}
	return true
}
