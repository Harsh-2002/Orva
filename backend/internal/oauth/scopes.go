package oauth

import (
	"sort"
	"strings"
)

// Scope constants. The first four map 1:1 onto the api_keys.permissions
// strings — same enforcement, same semantics, just delivered through a
// different bearer-token type. The OIDC scopes are accepted purely for
// ChatGPT compatibility (it requests them during discovery) and have no
// functional effect.
const (
	ScopeRead   = "read"
	ScopeInvoke = "invoke"
	ScopeWrite  = "write"
	ScopeAdmin  = "admin"

	// OIDC scopes — accepted, recorded, otherwise ignored. ChatGPT
	// includes them in /authorize and our discovery JSON advertises
	// them so the request validates.
	ScopeOpenID  = "openid"
	ScopeEmail   = "email"
	ScopeProfile = "profile"
)

// SupportedScopes is the canonical list returned by the AS metadata
// endpoint and validated against on /authorize. Adding a new scope means
// extending this list AND updating the scope→permission map below.
func SupportedScopes() []string {
	return []string{
		ScopeRead, ScopeInvoke, ScopeWrite, ScopeAdmin,
		ScopeOpenID, ScopeEmail, ScopeProfile,
	}
}

// DefaultGrantedScope is what we hand out when a client registers
// without specifying scopes (which both claude.ai and ChatGPT do).
//
// Full-power default by design. Without RBAC, splitting permissions
// between operator API keys (full) and OAuth-issued tokens (read+invoke
// only) was artificial — and ChatGPT users hit "no create_function
// tool exposed" because write-gated tools never registered against a
// limited-scope token. The protection is the explicit consent screen
// (which collapses to a single "Full administrative control" line for
// admin scope) plus per-token revocation in Settings → Connected
// applications.
const DefaultGrantedScope = ScopeRead + " " + ScopeInvoke + " " + ScopeWrite + " " + ScopeAdmin

// ParseScope splits an RFC 6749 §3.3 space-separated scope string,
// dropping empties so accidental double spaces don't produce blank
// scopes. Order is preserved; callers that need set semantics should
// call NormaliseScope.
func ParseScope(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Fields(s)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// NormaliseScope returns a deduplicated, sorted, space-joined scope
// string. We persist the normalised form so two requests asking for
// "read invoke" and "invoke read" hash to the same row in audit logs.
func NormaliseScope(scopes []string) string {
	seen := make(map[string]struct{}, len(scopes))
	uniq := make([]string, 0, len(scopes))
	for _, s := range scopes {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			uniq = append(uniq, s)
		}
	}
	sort.Strings(uniq)
	return strings.Join(uniq, " ")
}

// IntersectScope returns scopes present in BOTH inputs, normalised. Used
// at /authorize when the user's underlying API-key permission set might
// be narrower than what the client requested — the issued token never
// exceeds either side.
func IntersectScope(requested, allowed []string) []string {
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, s := range allowed {
		allowedSet[s] = struct{}{}
	}
	out := make([]string, 0, len(requested))
	for _, s := range requested {
		if _, ok := allowedSet[s]; ok {
			out = append(out, s)
		}
	}
	return out
}

// ScopeToPermissions translates an OAuth scope string into the api_keys
// permission strings the rest of the codebase already understands.
// OIDC scopes drop out — they have no permission consequences.
func ScopeToPermissions(scope string) []string {
	out := make([]string, 0, 4)
	for _, s := range ParseScope(scope) {
		switch s {
		case ScopeRead, ScopeInvoke, ScopeWrite, ScopeAdmin:
			out = append(out, s)
		case ScopeOpenID, ScopeEmail, ScopeProfile:
			// OIDC compatibility scopes — recorded on the token but
			// ignored functionally. Don't grant anything.
		}
	}
	return out
}

// IsValidScope returns true if every space-separated entry is a
// recognised scope. /authorize rejects unknown scopes with
// invalid_scope per RFC 6749 §4.1.2.1.
func IsValidScope(scope string) bool {
	supported := SupportedScopes()
	supportedSet := make(map[string]struct{}, len(supported))
	for _, s := range supported {
		supportedSet[s] = struct{}{}
	}
	for _, s := range ParseScope(scope) {
		if _, ok := supportedSet[s]; !ok {
			return false
		}
	}
	return true
}

// HumanScopeBullets renders each granted scope as a one-line
// description for the consent screen. Order is fixed so the screen
// reads the same regardless of input order.
func HumanScopeBullets(scope string) []string {
	parsed := ParseScope(scope)
	set := make(map[string]struct{}, len(parsed))
	for _, s := range parsed {
		set[s] = struct{}{}
	}
	var bullets []string
	if _, ok := set[ScopeAdmin]; ok {
		bullets = append(bullets, "Full administrative control over your Orva instance (read, invoke, write, admin).")
		return bullets
	}
	if _, ok := set[ScopeRead]; ok {
		bullets = append(bullets, "Read functions, executions, traces, baselines, KV, and docs.")
	}
	if _, ok := set[ScopeInvoke]; ok {
		bullets = append(bullets, "Invoke deployed functions on your behalf.")
	}
	if _, ok := set[ScopeWrite]; ok {
		bullets = append(bullets, "Create, update, and delete functions, secrets, routes, schedules, jobs.")
	}
	// OIDC scopes — quietly omitted from the bullets to keep the
	// consent screen focused on what the application can actually DO
	// to your Orva. Their presence in the request is still recorded.
	return bullets
}
