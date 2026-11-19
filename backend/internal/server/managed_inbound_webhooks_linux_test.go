//go:build linux

package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadtrigger"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestManagedInboundWebhookHTTP(t *testing.T) {
	dataDir := t.TempDir()
	db, err := database.New(filepath.Join(dataDir, "orva.db"))
	managedMust(t, err)
	t.Cleanup(func() { managedMust(t, db.Close()) })
	managedMust(t, db.Migrate())
	owner := ids.New()
	managedMust(t, db.InsertFunction(&database.Function{ID: owner, Name: "hooked", Runtime: "node", Status: "active", AuthMode: "none"}))
	_, err = db.ApplyRecoveryForFixture(t.Context(), "")
	managedMust(t, err)
	managedMust(t, db.InitManagedServingSchemas(t.Context()))
	record, err := db.GetIAMPolicy(t.Context(), owner)
	managedMust(t, err)
	evaluator, err := record.AdmissionEvaluator()
	managedMust(t, err)
	authority, err := workloadiam.NewAuthority(16, []*workloadiam.Evaluator{evaluator})
	managedMust(t, err)
	reg := registry.New(db)
	runtime := newManagedRuntime(db, reg, authority, nil, pool.SandboxTemplate{DataDir: dataDir}, sandbox.NewLimiter(1))
	t.Cleanup(func() { managedMust(t, runtime.pool.Shutdown(context.Background())) })
	triggers, err := workloadtrigger.New(db, reg, authority)
	managedMust(t, err)
	mux := http.NewServeMux()
	mux.Handle("/webhook/", &handlers.InboundTriggerHandler{DB: db, Registry: reg, Pool: runtime.pool,
		Metrics: runtime.invoke.Metrics, SDKAuth: runtime.invoke.Proxy.SDKAuth, Managed: true})
	router := bodySizeMiddleware(managedMaxBodyBytes, mountManagedCustomRoutes(mux, triggers, managedOperator(db), runtime.invoke))

	key := func(name, permissions string) (string, string) {
		id, token := ids.New(), "orva_inbound_fixture_"+name
		hash := sha256.Sum256([]byte(token))
		managedMust(t, db.InsertAPIKey(&database.APIKey{ID: id, Name: name, Prefix: name, KeyHash: fmt.Sprintf("%x", hash), Permissions: permissions}))
		return id, token
	}
	_, read := key("read", `["read"]`)
	writerID, write := key("write", `["write"]`)
	call := func(method, path, token, body string, headers map[string]string, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("X-Orva-API-Key", token)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s: got %d; want %d: %s", method, path, w.Code, want, w.Body)
		}
		return w
	}
	sign := func(secret, body string) map[string]string {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(body))
		return map[string]string{"X-Orva-Signature": hex.EncodeToString(mac.Sum(nil))}
	}
	expect := fmt.Sprintf(`{"%s":{"policy_revision":"1","authority_epoch":"1"}}`, owner)
	collection := "/api/v1/functions/" + owner + "/inbound-webhooks"

	// Unauthenticated, read-only, name-addressed and malformed management is denied.
	create := `{"name":"deploy hook","version":"0","expected":` + expect + `}`
	call("POST", collection, "", create, nil, 403)
	call("POST", collection, read, create, nil, 403)
	call("POST", "/api/v1/functions/hooked/inbound-webhooks", write, create, nil, 400)
	call("POST", collection, write, `{"name":"x","name":"y","version":"0","expected":`+expect+`}`, nil, 400)
	call("POST", collection, write, `{"name":"x","version":"1","expected":`+expect+`}`, nil, 409)
	call("POST", collection, write, `{"name":"x","function_id":"`+owner+`","version":"0","expected":`+expect+`}`, nil, 400)

	w := call("POST", collection, write, create, nil, 201)
	var created struct {
		Webhook struct {
			ID string `json:"id"`
		} `json:"inbound_webhook"`
		Secret     string `json:"secret"`
		TriggerURL string `json:"trigger_url"`
	}
	managedMust(t, json.Unmarshal(w.Body.Bytes(), &created))
	id := created.Webhook.ID
	if len(created.Secret) != 64 || created.TriggerURL != "/webhook/"+id || !ids.IsUUID(id) {
		t.Fatalf("unexpected create response shape: %s", w.Body)
	}
	secret := created.Secret

	// Reads never return the secret and report publication.
	for _, path := range []string{collection, collection + "/" + id, "/api/v1/exposures?kind=inbound&key=" + id + "&function_id=" + owner} {
		w = call("GET", path, read, "", nil, 200)
		if strings.Contains(w.Body.String(), secret) || !strings.Contains(w.Body.String(), `"published":true`) && !strings.Contains(w.Body.String(), `"inbound_published":true`) {
			t.Fatalf("unexpected read for %s: %s", path, w.Body)
		}
	}
	call("GET", collection, "", "", nil, 403)
	call("GET", collection+"/"+ids.New(), read, "", nil, 503)

	// Serving: published, signature-gated, then reaches only the managed pool.
	body := `{"event":"push"}`
	hook := "/webhook/" + id
	call("GET", hook, "", "", nil, 405)
	call("POST", hook, "", body, nil, 401)
	call("POST", hook, "", body, sign(strings.Repeat("0", 64), body), 401)
	call("POST", hook, "", body+" ", sign(secret, body), 401)
	call("POST", "/webhook/"+id+"/extra", "", body, sign(secret, body), 404)
	call("POST", "/webhook/not-a-uuid", "", body, sign(secret, body), 404)
	w = call("POST", hook, "", body, sign(secret, body), 503)
	if !strings.Contains(w.Body.String(), pool.ErrNoFunction.Error()) {
		t.Fatalf("signed webhook did not dispatch through the managed pool: %s", w.Body)
	}
	if strings.Contains(w.Body.String(), secret) {
		t.Fatal("response leaked the signature secret")
	}

	// Legacy rows never dispatch, whatever their signature.
	legacy := &database.InboundWebhook{FunctionID: owner, Name: "legacy", Secret: database.NewInboundWebhookSecret(), Active: true}
	managedMust(t, db.InsertInboundWebhook(legacy))
	call("POST", "/webhook/"+legacy.ID, "", body, sign(legacy.Secret, body), 404)
	// A raw edit to a managed row also unpublishes it until a managed republication.
	if _, err := db.WriteDB().Exec(`UPDATE inbound_webhooks SET name='raw' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	call("POST", hook, "", body, sign(secret, body), 404)
	w = call("GET", collection+"/"+id, read, "", nil, 200)
	if !strings.Contains(w.Body.String(), `"published":false`) || !strings.Contains(w.Body.String(), `"version":"1"`) {
		t.Fatalf("raw edit did not show an unpublished mapping at the same version: %s", w.Body)
	}
	update := `{"version":"1","expected":` + expect + `}`
	call("PUT", collection+"/"+id, read, update, nil, 403)
	call("PUT", collection+"/"+id, write, `{"version":"0","expected":`+expect+`}`, nil, 409)
	call("PUT", collection+"/"+id, write, `{"signature_format":"nope","version":"1","expected":`+expect+`}`, nil, 400)
	call("PUT", collection+"/"+id, write, update, nil, 200)
	call("POST", hook, "", body, sign(secret, body), 503)

	// Disabling is published too: it is dispatched as inactive, never as live.
	call("PUT", collection+"/"+id, write, `{"active":false,"version":"2","expected":`+expect+`}`, nil, 200)
	call("POST", hook, "", body, sign(secret, body), 410)

	// Deletion requires confirmation and then 404s with a tombstoned CAS counter.
	call("DELETE", collection+"/"+id, write, `{"version":"3","expected":`+expect+`}`, nil, 400)
	call("DELETE", collection+"/"+id, write, `{"confirm":true,"version":"2","expected":`+expect+`}`, nil, 409)
	call("DELETE", collection+"/"+id, write, `{"confirm":true,"version":"3","expected":`+expect+`}`, nil, 200)
	call("POST", hook, "", body, sign(secret, body), 404)

	// Revocation is rechecked at the mutation.
	managedMust(t, db.DeleteAPIKey(writerID))
	call("POST", collection, write, create, nil, 403)
}
