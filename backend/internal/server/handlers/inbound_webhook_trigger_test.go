package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/internal/database"
)

// signRequest is a small helper that produces the right header value for
// each format so tests can confirm the verifier accepts known-good
// signatures and rejects everything else.
func TestVerifyInboundSignature(t *testing.T) {
	secret := "shhh-it-is-a-secret"
	body := []byte(`{"hello":"world"}`)

	t.Run("hmac_sha256_hex_ok", func(t *testing.T) {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		sig := hex.EncodeToString(mac.Sum(nil))

		req := httptest.NewRequest(http.MethodPost, "/webhook/iwh_x", nil)
		req.Header.Set("X-Orva-Signature", sig)
		hook := &database.InboundWebhook{
			Secret: secret, SignatureFormat: "hmac_sha256_hex",
			SignatureHeader: "X-Orva-Signature",
		}
		if err := verifyInboundSignature(req, body, hook); err != nil {
			t.Fatalf("expected ok, got %v", err)
		}
	})

	t.Run("hmac_sha256_hex_tamper", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/webhook/iwh_x", nil)
		req.Header.Set("X-Orva-Signature", "deadbeef")
		hook := &database.InboundWebhook{
			Secret: secret, SignatureFormat: "hmac_sha256_hex",
			SignatureHeader: "X-Orva-Signature",
		}
		if err := verifyInboundSignature(req, body, hook); err == nil {
			t.Fatal("expected mismatch error")
		}
	})

	t.Run("hmac_sha256_base64_ok", func(t *testing.T) {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))

		req := httptest.NewRequest(http.MethodPost, "/webhook/iwh_x", nil)
		req.Header.Set("X-Orva-Signature", sig)
		hook := &database.InboundWebhook{
			Secret: secret, SignatureFormat: "hmac_sha256_base64",
			SignatureHeader: "X-Orva-Signature",
		}
		if err := verifyInboundSignature(req, body, hook); err != nil {
			t.Fatalf("expected ok, got %v", err)
		}
	})

	t.Run("github_ok", func(t *testing.T) {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

		req := httptest.NewRequest(http.MethodPost, "/webhook/iwh_x", nil)
		req.Header.Set("X-Hub-Signature-256", sig)
		hook := &database.InboundWebhook{
			Secret: secret, SignatureFormat: "github",
			SignatureHeader: "X-Hub-Signature-256",
		}
		if err := verifyInboundSignature(req, body, hook); err != nil {
			t.Fatalf("expected ok, got %v", err)
		}
	})

	t.Run("github_missing_prefix", func(t *testing.T) {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(body)
		sig := hex.EncodeToString(mac.Sum(nil)) // missing "sha256=" prefix

		req := httptest.NewRequest(http.MethodPost, "/webhook/iwh_x", nil)
		req.Header.Set("X-Hub-Signature-256", sig)
		hook := &database.InboundWebhook{
			Secret: secret, SignatureFormat: "github",
			SignatureHeader: "X-Hub-Signature-256",
		}
		if err := verifyInboundSignature(req, body, hook); err == nil {
			t.Fatal("expected error for missing sha256= prefix")
		}
	})

	t.Run("stripe_ok", func(t *testing.T) {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(ts))
		mac.Write([]byte("."))
		mac.Write(body)
		sig := "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))

		req := httptest.NewRequest(http.MethodPost, "/webhook/iwh_x", nil)
		req.Header.Set("Stripe-Signature", sig)
		hook := &database.InboundWebhook{
			Secret: secret, SignatureFormat: "stripe",
			SignatureHeader: "Stripe-Signature",
		}
		if err := verifyInboundSignature(req, body, hook); err != nil {
			t.Fatalf("expected ok, got %v", err)
		}
	})

	t.Run("stripe_replay_blocked", func(t *testing.T) {
		// Timestamp 10 minutes in the past — outside the 5-minute window.
		ts := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(ts))
		mac.Write([]byte("."))
		mac.Write(body)
		sig := "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))

		req := httptest.NewRequest(http.MethodPost, "/webhook/iwh_x", nil)
		req.Header.Set("Stripe-Signature", sig)
		hook := &database.InboundWebhook{
			Secret: secret, SignatureFormat: "stripe",
			SignatureHeader: "Stripe-Signature",
		}
		if err := verifyInboundSignature(req, body, hook); err == nil {
			t.Fatal("expected replay-window error")
		}
	})

	t.Run("slack_ok", func(t *testing.T) {
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte("v0:"))
		mac.Write([]byte(ts))
		mac.Write([]byte(":"))
		mac.Write(body)
		sig := "v0=" + hex.EncodeToString(mac.Sum(nil))

		req := httptest.NewRequest(http.MethodPost, "/webhook/iwh_x", nil)
		req.Header.Set("X-Slack-Signature", sig)
		req.Header.Set("X-Slack-Request-Timestamp", ts)
		hook := &database.InboundWebhook{
			Secret: secret, SignatureFormat: "slack",
			SignatureHeader: "X-Slack-Signature",
		}
		if err := verifyInboundSignature(req, body, hook); err != nil {
			t.Fatalf("expected ok, got %v", err)
		}
	})

	t.Run("slack_old_ts_rejected", func(t *testing.T) {
		ts := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte("v0:"))
		mac.Write([]byte(ts))
		mac.Write([]byte(":"))
		mac.Write(body)
		sig := "v0=" + hex.EncodeToString(mac.Sum(nil))

		req := httptest.NewRequest(http.MethodPost, "/webhook/iwh_x", nil)
		req.Header.Set("X-Slack-Signature", sig)
		req.Header.Set("X-Slack-Request-Timestamp", ts)
		hook := &database.InboundWebhook{
			Secret: secret, SignatureFormat: "slack",
			SignatureHeader: "X-Slack-Signature",
		}
		if err := verifyInboundSignature(req, body, hook); err == nil {
			t.Fatal("expected slack timestamp window rejection")
		}
	})
}
