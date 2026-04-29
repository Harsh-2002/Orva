package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// orva:// resources are read-only URIs an agent can attach to its
// context (e.g. drag a function's source into Claude Desktop) without
// needing a tool call. URI templates are listed in the server's
// resource catalog so the agent can discover them.
//
// Five resource shapes:
//   orva://functions/{fn_id}/source        — handler.{py|js}
//   orva://functions/{fn_id}/dependencies  — requirements.txt or package.json
//   orva://deployments/{deployment_id}/logs  — full build log
//   orva://executions/{execution_id}/logs    — captured stderr
//   orva://system/metrics                    — JSON metrics snapshot

func registerResources(s *mcpsdk.Server, deps Deps, perms permSet) {
	if !perms.has(permRead) {
		return
	}

	// Static resource — system metrics. Lists in the catalog so the
	// agent can find it without guessing.
	s.AddResource(
		&mcpsdk.Resource{
			URI:         "orva://system/metrics",
			Name:        "system metrics",
			Description: "JSON snapshot of Orva's invocation/build/latency counters and per-function pool stats. Live values; refresh by re-reading.",
			MIMEType:    "application/json",
		},
		func(_ context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
			snap := buildSystemMetrics(deps)
			body, _ := json.Marshal(snap)
			return &mcpsdk.ReadResourceResult{
				Contents: []*mcpsdk.ResourceContents{
					{URI: req.Params.URI, MIMEType: "application/json", Text: string(body)},
				},
			}, nil
		},
	)

	// Templated resources — fn_id, deployment_id, execution_id slots.
	// Clients with template support (Claude Desktop, Cursor) can fetch
	// them on demand.
	s.AddResourceTemplate(
		&mcpsdk.ResourceTemplate{
			URITemplate: "orva://functions/{function_id}/source",
			Name:        "function source",
			Description: "The handler file (handler.py / handler.js) for a function's currently-active deployment.",
			MIMEType:    "text/plain",
		},
		func(_ context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
			fnID, ok := extractURIPart(req.Params.URI, "orva://functions/", "/source")
			if !ok {
				return nil, fmt.Errorf("invalid uri: %s", req.Params.URI)
			}
			fn, err := resolveFunction(deps, fnID)
			if err != nil {
				return nil, err
			}
			out, err := readFunctionSource(deps, fn)
			if err != nil {
				return nil, err
			}
			return &mcpsdk.ReadResourceResult{
				Contents: []*mcpsdk.ResourceContents{
					{URI: req.Params.URI, MIMEType: "text/plain", Text: out.Code},
				},
			}, nil
		},
	)

	s.AddResourceTemplate(
		&mcpsdk.ResourceTemplate{
			URITemplate: "orva://functions/{function_id}/dependencies",
			Name:        "function dependencies",
			Description: "requirements.txt or package.json from the function's currently-active deployment. Empty if the function has no third-party deps.",
			MIMEType:    "text/plain",
		},
		func(_ context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
			fnID, ok := extractURIPart(req.Params.URI, "orva://functions/", "/dependencies")
			if !ok {
				return nil, fmt.Errorf("invalid uri: %s", req.Params.URI)
			}
			fn, err := resolveFunction(deps, fnID)
			if err != nil {
				return nil, err
			}
			out, err := readFunctionSource(deps, fn)
			if err != nil {
				return nil, err
			}
			return &mcpsdk.ReadResourceResult{
				Contents: []*mcpsdk.ResourceContents{
					{URI: req.Params.URI, MIMEType: "text/plain", Text: out.Dependencies},
				},
			}, nil
		},
	)

	s.AddResourceTemplate(
		&mcpsdk.ResourceTemplate{
			URITemplate: "orva://deployments/{deployment_id}/logs",
			Name:        "deployment build logs",
			Description: "Full text build log for a deployment. Use the get_deployment_logs tool for incremental tail; this resource returns everything.",
			MIMEType:    "text/plain",
		},
		func(_ context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
			depID, ok := extractURIPart(req.Params.URI, "orva://deployments/", "/logs")
			if !ok {
				return nil, fmt.Errorf("invalid uri: %s", req.Params.URI)
			}
			lines, err := deps.DB.GetBuildLogs(depID, 0, 5000)
			if err != nil {
				return nil, err
			}
			var b strings.Builder
			for _, ln := range lines {
				b.WriteString(ln.Line)
				if !strings.HasSuffix(ln.Line, "\n") {
					b.WriteByte('\n')
				}
			}
			return &mcpsdk.ReadResourceResult{
				Contents: []*mcpsdk.ResourceContents{
					{URI: req.Params.URI, MIMEType: "text/plain", Text: b.String()},
				},
			}, nil
		},
	)

	s.AddResourceTemplate(
		&mcpsdk.ResourceTemplate{
			URITemplate: "orva://executions/{execution_id}/logs",
			Name:        "execution stderr",
			Description: "Captured stderr from one execution. Empty if the function logged nothing.",
			MIMEType:    "text/plain",
		},
		func(_ context.Context, req *mcpsdk.ReadResourceRequest) (*mcpsdk.ReadResourceResult, error) {
			execID, ok := extractURIPart(req.Params.URI, "orva://executions/", "/logs")
			if !ok {
				return nil, fmt.Errorf("invalid uri: %s", req.Params.URI)
			}
			log, err := deps.DB.GetExecutionLog(execID)
			text := ""
			if err == nil {
				text = log.Stderr
			}
			return &mcpsdk.ReadResourceResult{
				Contents: []*mcpsdk.ResourceContents{
					{URI: req.Params.URI, MIMEType: "text/plain", Text: text},
				},
			}, nil
		},
	)
}

// extractURIPart slices out the variable segment between two literal
// affixes. Returns the segment + true on a match, or "" + false.
func extractURIPart(uri, prefix, suffix string) (string, bool) {
	if !strings.HasPrefix(uri, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(uri, prefix)
	if !strings.HasSuffix(rest, suffix) {
		return "", false
	}
	return strings.TrimSuffix(rest, suffix), true
}
