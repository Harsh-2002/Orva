package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// agent_registry.go gives Orva's in-product AI agent an IN-PROCESS view of
// the exact same tools the external MCP server exposes — without any MCP
// transport, JSON-RPC, or HTTP. The agent calls the tool implementations
// directly as Go functions.
//
// Single source of truth: every tool is declared once via regAddTool, which
// (a) registers it with the external *mcpsdk.Server (unchanged behaviour,
// for claude.ai / ChatGPT connectors) AND (b) records an AgentTool in the
// in-process Registry the chat agent dispatches against. Same name, same
// LLM-tuned description, same JSON schema (inferred from the same input
// struct + jsonschema tags), same permission gating, same destructive
// hints — so the two fronts can never drift.

// regCtx carries everything a register*Tools function needs. It is built two
// ways:
//   - server.go (external MCP): srv set, reg nil → tools registered with the SDK.
//   - BuildAgentRegistry (internal agent): srv nil, reg set → tools recorded
//     for in-process dispatch only.
type regCtx struct {
	srv   *mcpsdk.Server
	deps  Deps
	perms permSet
	reg   *Registry
	group string // current domain label applied to tools added next (e.g. "functions")
}

// serverRegCtx builds a regCtx for the external MCP server path.
func serverRegCtx(s *mcpsdk.Server, deps Deps, perms permSet) *regCtx {
	return &regCtx{srv: s, deps: deps, perms: perms}
}

// AgentTool is one tool the in-process agent can call. Schema is the JSON
// schema for the tool's arguments (inferred from the input struct); Invoke
// runs the tool implementation directly and returns its typed output.
type AgentTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Group       string          `json:"group"`
	Perm        string          `json:"perm"` // read | write | invoke | admin
	ReadOnly    bool            `json:"read_only"`
	Destructive bool            `json:"destructive"`
	Schema      json.RawMessage `json:"schema"` // JSON Schema for arguments

	invoke func(ctx context.Context, args json.RawMessage) (any, error)
}

// Registry is the agent's tool catalog. It is built per principal so it only
// ever contains tools that principal's permissions allow — the same guarantee
// the MCP server gives.
type Registry struct {
	order []string
	byName map[string]*AgentTool
}

func newRegistry() *Registry { return &Registry{byName: map[string]*AgentTool{}} }

func (r *Registry) add(t *AgentTool) {
	if _, dup := r.byName[t.Name]; dup {
		return
	}
	r.byName[t.Name] = t
	r.order = append(r.order, t.Name)
}

// Tools returns the catalog in stable (sorted) order.
func (r *Registry) Tools() []*AgentTool {
	out := make([]*AgentTool, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.byName[n])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns the tool by name, or nil.
func (r *Registry) Get(name string) *AgentTool { return r.byName[name] }

// Dispatch runs a tool by name with raw JSON arguments and returns its typed
// output. The output is whatever the underlying impl returns (the agent loop
// JSON-encodes it back to the model). Returns an error for unknown tools or
// failed invocations.
func (r *Registry) Dispatch(ctx context.Context, name string, args json.RawMessage) (any, error) {
	t := r.byName[name]
	if t == nil {
		return nil, fmt.Errorf("unknown tool %q", name)
	}
	return t.invoke(ctx, args)
}

// regAddTool declares one tool. It is generic over the input/output types so
// the JSON schema is inferred from In and the handler is type-checked, exactly
// like mcpsdk.AddTool. Pass the existing handler closure verbatim — no need to
// extract inline closures.
func regAddTool[In, Out any](
	rc *regCtx,
	perm string,
	def *mcpsdk.Tool,
	h mcpsdk.ToolHandlerFor[In, Out],
) {
	if !rc.perms.Has(perm) {
		return // permission-gated: invisible to this principal on both fronts
	}

	// External MCP server path — byte-for-byte the old behaviour.
	if rc.srv != nil {
		mcpsdk.AddTool(rc.srv, def, h)
	}

	// In-process agent registry path.
	if rc.reg != nil {
		var schemaJSON json.RawMessage
		if s, err := jsonschema.For[In](nil); err == nil && s != nil {
			if b, err := json.Marshal(s); err == nil {
				schemaJSON = b
			}
		}
		readOnly := def.Annotations != nil && def.Annotations.ReadOnlyHint
		destructive := def.Annotations != nil &&
			def.Annotations.DestructiveHint != nil && *def.Annotations.DestructiveHint
		rc.reg.add(&AgentTool{
			Name:        def.Name,
			Description: def.Description,
			Group:       rc.group,
			Perm:        perm,
			ReadOnly:    readOnly,
			Destructive: destructive,
			Schema:      schemaJSON,
			invoke: func(ctx context.Context, args json.RawMessage) (any, error) {
				var in In
				if len(args) > 0 {
					if err := json.Unmarshal(args, &in); err != nil {
						return nil, fmt.Errorf("invalid arguments for %s: %w", def.Name, err)
					}
				}
				_, out, err := h(ctx, nil, in)
				if err != nil {
					return nil, err
				}
				return out, nil
			},
		})
	}
}

// BuildAgentRegistry assembles the in-process tool catalog for a principal.
// Only register*Tools families that have been migrated to regAddTool appear
// here; each call is gated to the principal's perms inside regAddTool, so a
// read-only key yields a read-only catalog.
//
// As more tool files are migrated to regAddTool, add their register call here.
func BuildAgentRegistry(deps Deps, perms permSet) *Registry {
	reg := newRegistry()
	rc := &regCtx{deps: deps, perms: perms, reg: reg}

	// Every operator-tool family, gated to this principal's perms inside
	// regAddTool. This mirrors the external MCP server's registration in
	// server.go — same tools, same descriptions, same schemas.
	registerSystemTools(rc)
	registerDocsTools(rc)
	registerFunctionTools(rc)
	registerDeployTools(rc)
	registerInvokeTools(rc)
	registerSecretTools(rc)
	registerRouteTools(rc)
	registerKeyTools(rc)
	registerFirewallTools(rc)
	registerPoolTools(rc)
	registerCronTools(rc)
	registerKVTools(rc)
	registerJobTools(rc)
	registerWebhookTools(rc)
	registerInboundWebhookTools(rc)
	registerFixtureTools(rc)
	registerTraceTools(rc)

	return reg
}
