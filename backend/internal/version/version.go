// Package version is the single source of truth for Orva's release
// version string. Both the long-running daemon (system/health endpoint,
// MCP initialize response, structured logs) and the CLI surface this
// value, so keeping it in one place avoids the recurring drift where
// one binary ships "0.4.0" and another still claims "0.1.0".
//
// At release time we bump this constant and let `go build` re-stamp
// every binary in the workspace. The variable form (rather than const)
// lets distributors override the embedded value with
// `-ldflags '-X github.com/Harsh-2002/Orva/internal/version.Version=0.4.1+downstream'`
// if they need to mark a custom build without forking the source.
package version

// Version is Orva's semantic version string. Bump on every tagged
// release; see CHANGELOG.md and the v0.x ship notes in the release PR.
var Version = "0.4.0"
