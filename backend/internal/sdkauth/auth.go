// Package sdkauth issues and verifies process-scoped credentials for sandbox
// workers. A credential binds the worker to one immutable function ID; the
// process-random signing key makes every credential expire on server restart.
package sdkauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"time"
)

const tokenVersion = "v1"

var ErrInvalidToken = errors.New("invalid SDK credential")

type Authenticator struct {
	key    []byte
	active sync.Map // execution ID -> activeExecution while a worker is dispatching
}

type activeExecution struct {
	functionID string
	startedAt  time.Time
	traceID    string
	spanID     string
}

func (a *Authenticator) BindExecution(executionID, functionID, traceID, spanID string, startedAt time.Time) func() {
	if a == nil || executionID == "" || functionID == "" {
		return func() {}
	}
	a.active.Store(executionID, activeExecution{
		functionID: functionID, startedAt: startedAt, traceID: traceID, spanID: spanID,
	})
	return func() { a.active.Delete(executionID) }
}

func (a *Authenticator) OwnsExecution(executionID, functionID string) bool {
	if a == nil {
		return false
	}
	execution, ok := a.active.Load(executionID)
	return ok && execution.(activeExecution).functionID == functionID
}

func (a *Authenticator) ExecutionStart(executionID, functionID string) (time.Time, bool) {
	if a == nil {
		return time.Time{}, false
	}
	value, ok := a.active.Load(executionID)
	if !ok {
		return time.Time{}, false
	}
	execution := value.(activeExecution)
	return execution.startedAt, execution.functionID == functionID
}

func (a *Authenticator) TraceContext(executionID, functionID string) (traceID, spanID string, startedAt time.Time, ok bool) {
	if a == nil {
		return "", "", time.Time{}, false
	}
	value, found := a.active.Load(executionID)
	if !found {
		return "", "", time.Time{}, false
	}
	execution := value.(activeExecution)
	if execution.functionID != functionID {
		return "", "", time.Time{}, false
	}
	return execution.traceID, execution.spanID, execution.startedAt, true
}

func New(key []byte) *Authenticator {
	return &Authenticator{key: append([]byte(nil), key...)}
}

func (a *Authenticator) Mint(functionID string) string {
	if a == nil || len(a.key) == 0 || functionID == "" {
		return ""
	}
	encodedID := base64.RawURLEncoding.EncodeToString([]byte(functionID))
	payload := tokenVersion + "." + encodedID
	mac := hmac.New(sha256.New, a.key)
	_, _ = mac.Write([]byte(payload))
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (a *Authenticator) Verify(token string) (string, error) {
	if a == nil || len(a.key) == 0 {
		return "", ErrInvalidToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != tokenVersion {
		return "", ErrInvalidToken
	}
	payload := parts[0] + "." + parts[1]
	gotMAC, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", ErrInvalidToken
	}
	mac := hmac.New(sha256.New, a.key)
	_, _ = mac.Write([]byte(payload))
	if !hmac.Equal(gotMAC, mac.Sum(nil)) {
		return "", ErrInvalidToken
	}
	rawID, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(rawID) == 0 {
		return "", ErrInvalidToken
	}
	return string(rawID), nil
}
