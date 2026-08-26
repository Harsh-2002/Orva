// Package sdkauth issues and verifies credentials for sandbox workers.
//
// A credential names one immutable function ID and one specific worker
// process. The process-random signing key expires every credential on server
// restart; the per-spawn nonce expires each one when its own worker dies.
//
// The nonce is what makes redeploy a real remediation. Without it a credential
// was a pure function of (signing key, function ID), so a copy exfiltrated by a
// compromised dependency kept working against the replacement code -- the
// operator removed the dependency, redeployed, and the stolen token was
// re-minted byte-identical. Only a full restart cleared it. Now a redeploy
// retires the function's warm workers, each worker's nonce dies with its
// process, and the copy stops working.
package sdkauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"time"
)

// v2 added the nonce. There is no v1 compatibility to keep: the signing key is
// process-random, so no credential survives the restart that deploys a new
// binary anyway.
const tokenVersion = "v2"

var ErrInvalidToken = errors.New("invalid SDK credential")

type Authenticator struct {
	key    []byte
	active sync.Map // execution ID -> activeExecution while a worker is dispatching
	live   sync.Map // nonce -> struct{} while the worker that received it is alive
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

// Mint issues a credential for one worker process and returns a release
// function that invalidates it. The caller must call release when that worker
// dies, and on every path where the spawn it was minted for does not happen.
//
// Failing to release leaks one map entry and degrades to the pre-nonce
// behaviour -- the credential stays valid until the process restarts. Releasing
// too early would 401 a live worker, which is why the release is wired to the
// reaper that observes the process actually exiting rather than to any code
// path that merely intends to stop it.
func (a *Authenticator) Mint(functionID string) (string, func()) {
	noop := func() {}
	if a == nil || len(a.key) == 0 || functionID == "" {
		return "", noop
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", noop
	}
	encodedNonce := base64.RawURLEncoding.EncodeToString(nonce)
	encodedID := base64.RawURLEncoding.EncodeToString([]byte(functionID))
	payload := tokenVersion + "." + encodedID + "." + encodedNonce
	mac := hmac.New(sha256.New, a.key)
	_, _ = mac.Write([]byte(payload))

	a.live.Store(encodedNonce, struct{}{})
	release := func() { a.live.Delete(encodedNonce) }
	return payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), release
}

func (a *Authenticator) Verify(token string) (string, error) {
	if a == nil || len(a.key) == 0 {
		return "", ErrInvalidToken
	}
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != tokenVersion {
		return "", ErrInvalidToken
	}
	payload := parts[0] + "." + parts[1] + "." + parts[2]
	gotMAC, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return "", ErrInvalidToken
	}
	mac := hmac.New(sha256.New, a.key)
	_, _ = mac.Write([]byte(payload))
	if !hmac.Equal(gotMAC, mac.Sum(nil)) {
		return "", ErrInvalidToken
	}
	// A valid signature is not enough: the worker this was minted for must
	// still be alive. This is the check that makes an exfiltrated copy stop
	// working when the function is redeployed.
	if _, alive := a.live.Load(parts[2]); !alive {
		return "", ErrInvalidToken
	}
	rawID, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(rawID) == 0 {
		return "", ErrInvalidToken
	}
	return string(rawID), nil
}
