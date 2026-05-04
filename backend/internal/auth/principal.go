// Package auth defines the unified caller identity used across Orva's
// auth surfaces. Three flavors of bearer tokens reach `/mcp` and the
// REST API today:
//
//   - orva_<hex>      → operator API key, gates by permission set
//   - orva_chn_<hex>  → agent channel, exposes a fixed function set
//                       as MCP tools; not accepted by /api/v1/*
//   - orva_oat_<hex>  → OAuth-issued access token (claude.ai web,
//                       ChatGPT web, etc.); operator-equivalent power
//                       gated by scope→permission mapping
//
// Each resolver builds a *Principal so downstream middleware (activity
// logging, MCP tool registration) doesn't have to care which path the
// caller came in through. Replaces the older "synthesise a fake
// *database.APIKey for OAuth grants" hack — that synth shape leaked
// "everyone is an api_key" into the activity log, which broke channel
// attribution before this refactor.
package auth

import "time"

// Kind values for Principal.Kind. Use the constants instead of bare
// strings so a typo in middleware doesn't silently bypass an actor-
// type filter.
const (
	KindAPIKey    = "api_key"
	KindOAuth     = "oauth"
	KindChannel = "channel"
)

// Principal is the resolved caller identity. Every auth path returns
// one of these. Fields populated depend on Kind.
type Principal struct {
	// Kind is one of KindAPIKey / KindOAuth / KindChannel.
	Kind string

	// ID is the storage row id (uuidv7) of the underlying credential —
	// API key id, OAuth token storage id, or agent channel id.
	// Stamped on activity rows so operators can trace who did what.
	ID string

	// Label is a human-friendly name. API key name, OAuth client name,
	// or channel name. Surfaced in the dashboard's Activity feed.
	Label string

	// Perms is the permission set for KindAPIKey and KindOAuth.
	// Empty for KindChannel — channels have no Orva-management
	// permissions; their access is bounded by the function set in
	// Channel.FunctionIDs.
	Perms PermSet

	// Channel carries the per-token function bundle. Non-nil iff
	// Kind == KindChannel. The MCP layer uses FunctionIDs to
	// register one tool per function and skips every other register*
	// call.
	Channel *ChannelRef

	// Expires is when the credential becomes unusable (nil = never).
	// API keys, channel tokens, and OAuth access tokens all have
	// expiry semantics; the resolver checks this before returning, so
	// callers don't need to re-check — but it's available for
	// telemetry / "expires in" rendering.
	Expires *time.Time
}

// PermSet is a string-keyed bitset for Orva permissions: read, invoke,
// write, admin. Duplicates the legacy mcp.permSet shape so the MCP
// layer can transition without a flood of conversions.
type PermSet map[string]bool

// Has reports whether the permission set includes `name`.
func (p PermSet) Has(name string) bool { return p != nil && p[name] }

// ChannelRef is the scoped data a channel token gives the MCP
// server: which functions to register as tools, plus the channel's
// own identity (used for the activity log + the per-channel
// system prompt).
type ChannelRef struct {
	ID           string
	Name         string
	Description  string
	Instructions string   // optional per-channel serverInstructions override
	FunctionIDs  []string // populated from channel_functions junction
}
