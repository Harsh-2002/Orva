package mcp

// Permission scopes mirror the REST API's middleware_auth.go scopes.
// Each tool requires exactly one — registration helpers consult the
// caller's permSet to decide visibility.
const (
	permRead   = "read"
	permWrite  = "write"
	permInvoke = "invoke"
	permAdmin  = "admin"
)

// gatedAdd is a small helper used by every tool registration block.
// If the caller's permission set includes `need`, fn is called to
// register the tool. Otherwise it's silently skipped — the agent
// never even sees the tool name in its catalog.
func gatedAdd(perms permSet, need string, fn func()) {
	if perms.Has(need) {
		fn()
	}
}
