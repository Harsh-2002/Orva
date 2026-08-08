package handlers

import (
	"log/slog"
	"net/http"
	"sync"

	"github.com/Harsh-2002/Orva/backend/internal/version"
)

// sdkVersionSeen tracks the (function_id, sdk_version) tuples we've
// already logged in this process so we don't spam slog on every internal
// call. The first call from a given combo emits one INFO line; subsequent
// calls are silent. State resets on process restart, which is fine — the
// log line is purely operational signal, not durable record.
var (
	sdkVersionSeenMu sync.Mutex
	sdkVersionSeen   = map[string]struct{}{}
)

// observeSDKVersion peeks at X-Orva-SDK-Version on internal-token
// requests. First sighting of a (function_id, sdk_version) tuple logs an
// INFO; a version that differs from the SDK this server bundles
// (version.SDKVersion) logs a WARN so operators see genuine drift. Always
// safe to call — handlers that don't run inside a sandbox simply see an
// empty header and no-op.
//
// NOTE: this compares against version.SDKVersion (the bundled SDK's semver),
// NOT version.Version (the vYYYY.MM.DD release tag). Comparing against the
// release tag was a bug: the two value-spaces can never be equal, so it
// logged a false WARN for every function on its first internal call.
func observeSDKVersion(r *http.Request) {
	got := r.Header.Get("X-Orva-SDK-Version")
	if got == "" {
		return
	}
	fnID := r.Header.Get("X-Orva-Function-Id")
	if fnID == "" {
		// internal_invoke uses URL path; KV puts fn_id in the path too. We
		// only need a stable dedup key for the log line, so the empty
		// string is fine when there's no header.
		fnID = "anon"
	}
	key := fnID + "|" + got
	sdkVersionSeenMu.Lock()
	_, seen := sdkVersionSeen[key]
	if !seen {
		sdkVersionSeen[key] = struct{}{}
	}
	sdkVersionSeenMu.Unlock()
	if seen {
		return
	}
	if got == version.SDKVersion {
		slog.Info("sdk version observed", "function_id", fnID, "sdk_version", got)
		return
	}
	slog.Warn("sdk version differs from bundled SDK",
		"function_id", fnID,
		"sdk_version", got,
		"bundled_sdk_version", version.SDKVersion,
		"hint", "re-deploy the function to refresh the bundled SDK")
}
