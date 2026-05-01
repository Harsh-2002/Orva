package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/Harsh-2002/Orva/internal/database"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// FixtureView is the public per-fixture shape the MCP surface returns.
// Headers is decoded from the stored JSON so agents don't have to
// re-parse a string. Body is exposed as a UTF-8 string (the same shape
// the editor stores) — agents pass arbitrary JSON / form bodies and the
// server treats them as opaque bytes.
type FixtureView struct {
	ID         string            `json:"id"`
	FunctionID string            `json:"function_id"`
	Name       string            `json:"name"`
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	CreatedAt  string            `json:"created_at"`
	UpdatedAt  string            `json:"updated_at"`
}

func toFixtureView(f *database.Fixture) FixtureView {
	hdrs := map[string]string{}
	if f.HeadersJSON != "" {
		_ = json.Unmarshal([]byte(f.HeadersJSON), &hdrs)
	}
	return FixtureView{
		ID:         f.ID,
		FunctionID: f.FunctionID,
		Name:       f.Name,
		Method:     f.Method,
		Path:       f.Path,
		Headers:    hdrs,
		Body:       string(f.Body),
		CreatedAt:  f.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		UpdatedAt:  f.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

// ─── list_fixtures ─────────────────────────────────────────────────

type ListFixturesInput struct {
	FunctionID string `json:"function_id" jsonschema:"function id (fn_...) or name"`
}
type ListFixturesOutput struct {
	Fixtures []FixtureView `json:"fixtures"`
}

// ─── save_fixture ──────────────────────────────────────────────────

type SaveFixtureInput struct {
	FunctionID string            `json:"function_id" jsonschema:"function id (fn_...) or name"`
	Name       string            `json:"name"        jsonschema:"unique-per-function name; upsert on conflict"`
	Method     string            `json:"method,omitempty"  jsonschema:"HTTP method, default POST"`
	Path       string            `json:"path,omitempty"    jsonschema:"sub-path passed to the handler, default /"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"    jsonschema:"raw request body (JSON should be pre-stringified)"`
}
type SaveFixtureOutput struct {
	Fixture FixtureView `json:"fixture"`
}

// ─── delete_fixture ────────────────────────────────────────────────

type DeleteFixtureInput struct {
	FunctionID string `json:"function_id"`
	Name       string `json:"name"`
}
type DeleteFixtureOutput struct {
	Status string `json:"status"`
	Name   string `json:"name"`
}

// validFixtureMethods mirrors the REST handler's allowlist; we keep the
// MCP surface honest by rejecting verbs that wouldn't survive a roundtrip
// through the editor's <select>.
var validFixtureMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true,
	"DELETE": true, "HEAD": true, "OPTIONS": true,
}

const mcpFixtureBodyCap = 64 * 1024

// normaliseFixtureInput uppercases method, defaults path to /, trims name,
// and bounds the body. Returns an error suitable to surface back to the agent.
func normaliseFixtureInput(in *SaveFixtureInput) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return errors.New("name is required")
	}
	if len(in.Name) > 128 {
		return errors.New("name must be <= 128 chars")
	}
	in.Method = strings.ToUpper(strings.TrimSpace(in.Method))
	if in.Method == "" {
		in.Method = "POST"
	}
	if !validFixtureMethods[in.Method] {
		return errors.New("method must be one of GET/POST/PUT/PATCH/DELETE/HEAD/OPTIONS")
	}
	in.Path = strings.TrimSpace(in.Path)
	if in.Path == "" {
		in.Path = "/"
	}
	if !strings.HasPrefix(in.Path, "/") {
		in.Path = "/" + in.Path
	}
	if len(in.Body) > mcpFixtureBodyCap {
		return errors.New("body exceeds 64 KB cap")
	}
	if in.Headers == nil {
		in.Headers = map[string]string{}
	}
	return nil
}

func registerFixtureTools(s *mcpsdk.Server, deps Deps, perms permSet) {
	gatedAdd(perms, permRead, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "list_fixtures",
				Description: "List saved request fixtures for one function. Fixtures are reusable Postman-style presets (method, path, headers, body) used to invoke the function from the editor's Test pane or via test_function_with_fixture.",
				Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in ListFixturesInput) (*mcpsdk.CallToolResult, ListFixturesOutput, error) {
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, ListFixturesOutput{}, err
				}
				rows, err := deps.DB.ListFixtures(fn.ID)
				if err != nil {
					return nil, ListFixturesOutput{}, err
				}
				out := ListFixturesOutput{Fixtures: make([]FixtureView, 0, len(rows))}
				for _, f := range rows {
					out.Fixtures = append(out.Fixtures, toFixtureView(f))
				}
				return nil, out, nil
			},
		)
	})

	gatedAdd(perms, permWrite, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "save_fixture",
				Description: "Create or update a saved request fixture for a function. Idempotent on (function_id, name) — re-calling with the same name overwrites method/path/headers/body. Use list_fixtures to see what's already saved before creating dupes.",
				Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: true, OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in SaveFixtureInput) (*mcpsdk.CallToolResult, SaveFixtureOutput, error) {
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, SaveFixtureOutput{}, err
				}
				if err := normaliseFixtureInput(&in); err != nil {
					return nil, SaveFixtureOutput{}, err
				}
				headersJSON, _ := json.Marshal(in.Headers)
				row := &database.Fixture{
					FunctionID:  fn.ID,
					Name:        in.Name,
					Method:      in.Method,
					Path:        in.Path,
					HeadersJSON: string(headersJSON),
					Body:        []byte(in.Body),
				}
				saved, err := deps.DB.UpsertFixture(row)
				if err != nil {
					return nil, SaveFixtureOutput{}, err
				}
				return nil, SaveFixtureOutput{Fixture: toFixtureView(saved)}, nil
			},
		)
	})

	gatedAdd(perms, permWrite, func() {
		mcpsdk.AddTool(s,
			&mcpsdk.Tool{
				Name:        "delete_fixture",
				Description: "Remove a saved fixture by (function_id, name). Idempotent — returns ok even if the fixture didn't exist.",
				Annotations: &mcpsdk.ToolAnnotations{IdempotentHint: true, DestructiveHint: ptrTrue(), OpenWorldHint: ptrFalse()},
			},
			func(_ context.Context, _ *mcpsdk.CallToolRequest, in DeleteFixtureInput) (*mcpsdk.CallToolResult, DeleteFixtureOutput, error) {
				fn, err := resolveFunction(deps, in.FunctionID)
				if err != nil {
					return nil, DeleteFixtureOutput{}, err
				}
				name := strings.TrimSpace(in.Name)
				if name == "" {
					return nil, DeleteFixtureOutput{}, errors.New("name is required")
				}
				if err := deps.DB.DeleteFixture(fn.ID, name); err != nil {
					return nil, DeleteFixtureOutput{}, err
				}
				return nil, DeleteFixtureOutput{Status: "deleted", Name: name}, nil
			},
		)
	})
}
