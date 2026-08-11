package mcp

import (
	"context"
	"encoding/json"
	"sync"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// encodedToolSchemas holds the expensive, immutable JSON representation of a
// registered tool's schemas. The SDK stores inferred schemas as reflection-rich
// *jsonschema.Schema values; encoding all of them on every tools/list response
// dominated the stateless hot path even after Server reuse.
type encodedToolSchemas struct {
	input  json.RawMessage
	output json.RawMessage
}

var encodedCatalogSchemas sync.Map // map[*mcpsdk.Tool]encodedToolSchemas

// catalogEncodingMiddleware swaps schema values for pre-encoded JSON on a
// shallow copy of each Tool. Handler validation keeps using the SDK's resolved
// schemas, and the registered Tool is never mutated, so concurrent requests are
// race-free. The cache is bounded by the tools in the bounded server variants.
func catalogEncodingMiddleware() mcpsdk.Middleware {
	return func(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
		return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil {
				return result, err
			}
			listed, ok := result.(*mcpsdk.ListToolsResult)
			if !ok {
				return result, nil
			}
			for i, tool := range listed.Tools {
				if tool == nil {
					continue
				}
				encoded := schemasForTool(tool)
				clone := *tool
				if encoded.input != nil {
					clone.InputSchema = encoded.input
				}
				if encoded.output != nil {
					clone.OutputSchema = encoded.output
				}
				listed.Tools[i] = &clone
			}
			return result, nil
		}
	}
}

func schemasForTool(tool *mcpsdk.Tool) encodedToolSchemas {
	if cached, ok := encodedCatalogSchemas.Load(tool); ok {
		return cached.(encodedToolSchemas)
	}
	var encoded encodedToolSchemas
	if raw, err := json.Marshal(tool.InputSchema); err == nil {
		encoded.input = raw
	}
	if tool.OutputSchema != nil {
		if raw, err := json.Marshal(tool.OutputSchema); err == nil {
			encoded.output = raw
		}
	}
	actual, _ := encodedCatalogSchemas.LoadOrStore(tool, encoded)
	return actual.(encodedToolSchemas)
}
