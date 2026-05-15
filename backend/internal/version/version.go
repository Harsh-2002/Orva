// Package version is the single source of truth for Orva's release
// identity. Both the long-running daemon (system/health endpoint, MCP
// initialize response, structured logs) and the CLI surface these
// values, so keeping them in one place avoids the recurring drift where
// one binary ships "0.4.0" and another still claims "0.1.0".
//
// All three variables are stamped at link time via `-X` ldflags from
// Makefile, Dockerfile, and the release workflow. They MUST stay as
// `var` (not `const`) so the linker can override them. The import path
// the linker sees is `github.com/Harsh-2002/Orva/backend/internal/version`.
//
// Defaults are deliberately obvious ("dev" / "unknown") so an unstamped
// binary can't pretend to be a real release.
package version

// Version is the release identity — a vYYYY.MM.DD git tag in production
// or `git describe`'s output in dev (e.g., "v2026.05.15-3-g1be3399-dirty").
var Version = "dev"

// Commit is the short git SHA at build time (e.g. "1be3399").
var Commit = "unknown"

// BuildTime is the RFC3339 UTC timestamp when this binary was linked
// (e.g. "2026-05-15T14:20:34Z"). Wall-clock at build moment, not commit
// time, so operators can tell "is this image the one CI just produced?"
// at a glance.
var BuildTime = "unknown"
