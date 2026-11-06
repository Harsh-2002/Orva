package llm

import "testing"

// CI's govulncheck gate allows GO-2026-6911 (mark3labs/mcp-go, reached only
// through Bifrost's MCP client) on the premise that Orva never builds one.
// If this fails, that premise is gone: remove the exception, do not edit this.
func TestGatewayHasNoMCPManager(t *testing.T) {
	c, err := New(staticKeyResolver{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	if c.bf.MCPManager != nil {
		t.Fatal("gateway built an MCP manager; the govulncheck exception for GO-2026-6911 no longer holds")
	}
}
