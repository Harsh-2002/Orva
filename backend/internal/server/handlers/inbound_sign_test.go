package handlers

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

// The dashboard's webhook test asked the operator to paste back the plaintext
// secret -- one the server already stores and never needs told -- and then
// signed in the browser. A browser could only produce 2 of the 5 formats, so
// testing a Stripe, Slack or base64 trigger was refused outright.
//
// Signing now happens where the secret and the format table already live. The
// property that matters: for EVERY format the server can verify, the headers it
// produces must actually verify. Anything less and the test tells an operator
// their trigger is broken when it is fine.
func TestEveryVerifiableFormatCanBeSigned(t *testing.T) {
	const secret = "s3cr3t-inbound-key"
	body := []byte(`{"event":"ping","n":1}`)
	now := time.Now().Unix()

	formats := []string{
		"hmac_sha256_hex",
		"hmac_sha256_base64",
		"github",
		"stripe",
		"slack",
	}

	for _, format := range formats {
		t.Run(format, func(t *testing.T) {
			hook := &database.InboundWebhook{
				Secret:          secret,
				SignatureFormat: format,
				SignatureHeader: database.DefaultSignatureHeader(format),
			}

			headers, err := signInboundTest(hook, body, now)
			if err != nil {
				t.Fatalf("signInboundTest(%s): %v", format, err)
			}
			if len(headers) == 0 {
				t.Fatalf("%s produced no headers", format)
			}

			req := httptest.NewRequest("POST", "/webhook/x", strings.NewReader(string(body)))
			for k, v := range headers {
				req.Header.Set(k, v)
			}

			if err := verifyInboundSignature(req, body, hook); err != nil {
				t.Errorf("%s: headers produced by signInboundTest did not verify: %v (headers=%v)",
					format, err, headers)
			}
		})
	}
}

// A signature is only meaningful if it fails on a body it did not sign.
func TestSignedHeadersRejectATamperedBody(t *testing.T) {
	const secret = "s3cr3t-inbound-key"
	signed := []byte(`{"amount":100}`)
	tampered := []byte(`{"amount":999999}`)

	for _, format := range []string{"hmac_sha256_hex", "hmac_sha256_base64", "github", "stripe", "slack"} {
		t.Run(format, func(t *testing.T) {
			hook := &database.InboundWebhook{
				Secret:          secret,
				SignatureFormat: format,
				SignatureHeader: database.DefaultSignatureHeader(format),
			}
			headers, err := signInboundTest(hook, signed, time.Now().Unix())
			if err != nil {
				t.Fatalf("signInboundTest: %v", err)
			}
			req := httptest.NewRequest("POST", "/webhook/x", strings.NewReader(string(tampered)))
			for k, v := range headers {
				req.Header.Set(k, v)
			}
			if err := verifyInboundSignature(req, tampered, hook); err == nil {
				t.Errorf("%s: a signature for a different body verified", format)
			}
		})
	}
}

// An unknown format must be refused rather than silently signed with a default.
func TestSigningRefusesAnUnknownFormat(t *testing.T) {
	hook := &database.InboundWebhook{Secret: "x", SignatureFormat: "not-a-format"}
	if _, err := signInboundTest(hook, []byte("{}"), time.Now().Unix()); err == nil {
		t.Error("an unknown signature_format was signed instead of refused")
	}
}
