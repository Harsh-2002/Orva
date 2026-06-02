// Package ai is the service layer for Orva's in-product AI chat assistant. It
// wires together: the SQLite store (database), the at-rest key cipher
// (secrets), the in-process tool registry (mcp.BuildAgentRegistry), the
// embedded LLM gateway (llm, Bifrost), and the agentic loop (agent). Handlers
// call Manager; Manager owns all the policy.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/ai/agent"
	"github.com/Harsh-2002/Orva/backend/internal/ai/llm"
	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/mcp"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
)

// Sink is the SSE event sink the handler implements over the HTTP response.
type Sink = agent.Sink

// Principal identifies the chat caller and scopes which tools the agent may
// use. Perms mirrors the MCP/REST permission model.
type Principal struct {
	ID    string
	Label string
	Perms auth.PermSet
}

// Manager is the long-lived AI service, constructed once in server.New().
type Manager struct {
	db      *database.Database
	secrets *secrets.Manager
	deps    mcp.Deps // tool registry dependencies (same struct the MCP server uses)

	mu     sync.Mutex
	client *llm.Client
	dirty  bool // rebuild the gateway on next use (provider configs changed)

	// convMu guards convBusy, the set of conversations with a turn in flight.
	// One turn per conversation: overlapping turns (double-send, or chat while
	// an approval is being decided) are rejected rather than interleaved, which
	// would corrupt message ordering.
	convMu   sync.Mutex
	convBusy map[string]bool
}

// ErrConversationBusy is returned when a turn is already running for a
// conversation. Streaming entry points surface it as an SSE error; the JSON
// delete path maps it to 409 Conflict.
var ErrConversationBusy = errors.New("a turn is already in progress for this conversation")

// tryLockConv marks a conversation busy, returning false if it already is.
func (m *Manager) tryLockConv(id string) bool {
	m.convMu.Lock()
	defer m.convMu.Unlock()
	if m.convBusy[id] {
		return false
	}
	if m.convBusy == nil {
		m.convBusy = map[string]bool{}
	}
	m.convBusy[id] = true
	return true
}

func (m *Manager) unlockConv(id string) {
	m.convMu.Lock()
	delete(m.convBusy, id)
	m.convMu.Unlock()
}

// New constructs the Manager. The LLM gateway is built lazily on first chat
// (and rebuilt when provider configs change), so startup never depends on a
// provider being configured.
func New(db *database.Database, sec *secrets.Manager, deps mcp.Deps) *Manager {
	return &Manager{db: db, secrets: sec, deps: deps, dirty: true}
}

// Close releases the LLM gateway pools.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client != nil {
		m.client.Close()
		m.client = nil
	}
}

// ─── llm.KeyResolver (credentials come from ai_provider_configs, decrypted) ──

// Providers lists providers that currently have an enabled key.
func (m *Manager) Providers() []string {
	cfgs, err := m.db.ListProviderConfigs()
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, c := range cfgs {
		if c.Enabled && c.APIKeyEncrypted != "" && !seen[c.Provider] {
			seen[c.Provider] = true
			out = append(out, c.Provider)
		}
	}
	return out
}

// Resolve returns the decrypted API key and optional base-URL for a provider.
func (m *Manager) Resolve(provider string) (apiKey, baseURL string, err error) {
	cfg, err := m.db.GetEnabledProviderConfig(strings.ToLower(provider))
	if err != nil {
		return "", "", fmt.Errorf("provider %q not configured", provider)
	}
	if cfg.APIKeyEncrypted == "" {
		// Local providers (e.g. Ollama) may legitimately have no key.
		return "", normalizeBaseURL(cfg.BaseURL), nil
	}
	key, err := m.secrets.DecryptValue(cfg.APIKeyEncrypted)
	if err != nil {
		return "", "", fmt.Errorf("decrypt key for %q: %w", provider, err)
	}
	return key, normalizeBaseURL(cfg.BaseURL), nil
}

func (m *Manager) getClient() (*llm.Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client != nil && !m.dirty {
		return m.client, nil
	}
	if m.client != nil {
		m.client.Close()
		m.client = nil
	}
	c, err := llm.New(m)
	if err != nil {
		return nil, err
	}
	m.client = c
	m.dirty = false
	return c, nil
}

func (m *Manager) invalidateClient() {
	m.mu.Lock()
	m.dirty = true
	m.mu.Unlock()
}

// ─── chat ────────────────────────────────────────────────────────────────────

// ChatParams is one inbound chat message plus optional per-message overrides.
type ChatParams struct {
	ConversationID string
	Content        string
	Provider       string
	Model          string
	Thinking       string
}

// Chat runs one user turn through the agentic loop, streaming events to sink.
// It creates the conversation if ConversationID is empty and returns the
// conversation id used.
func (m *Manager) Chat(ctx context.Context, sink Sink, p Principal, params ChatParams) (string, error) {
	settings := m.resolvedSettings()
	cfg := agent.Config{
		Provider:       firstNonEmpty(params.Provider, settings.Provider),
		Model:          firstNonEmpty(params.Model, settings.Model),
		Thinking:       firstNonEmpty(params.Thinking, settings.ThinkingLevel),
		System:         settings.SystemPrompt,
		ApprovalPolicy: settings.ApprovalPolicy,
		MaxIterations:  settings.MaxToolIterations,
	}

	convID := params.ConversationID
	if convID == "" {
		c := &database.AIConversation{
			UserID:   p.ID,
			Title:    titleFrom(params.Content),
			Provider: cfg.Provider,
			Model:    cfg.Model,
		}
		if err := m.db.CreateConversation(c); err != nil {
			return "", err
		}
		convID = c.ID
		_ = sink.Send("conversation", map[string]any{"id": c.ID, "title": c.Title})
	}

	if !m.tryLockConv(convID) {
		_ = sink.Send("error", map[string]any{"message": ErrConversationBusy.Error()})
		return convID, ErrConversationBusy
	}
	defer m.unlockConv(convID)

	runner, err := m.buildRunner(p.Perms, cfg)
	if err != nil {
		_ = sink.Send("error", map[string]any{"message": err.Error()})
		return convID, err
	}
	return convID, runner.Run(ctx, sink, convID, params.Content, p.ID)
}

// Resume continues a paused turn after an approve/reject decision.
func (m *Manager) Resume(ctx context.Context, sink Sink, p Principal, convID, toolCallID string, approved bool) error {
	if !m.tryLockConv(convID) {
		_ = sink.Send("error", map[string]any{"message": ErrConversationBusy.Error()})
		return ErrConversationBusy
	}
	defer m.unlockConv(convID)
	settings := m.resolvedSettings()
	cfg := agent.Config{
		Provider:       settings.Provider,
		Model:          settings.Model,
		Thinking:       settings.ThinkingLevel,
		System:         settings.SystemPrompt,
		ApprovalPolicy: settings.ApprovalPolicy,
		MaxIterations:  settings.MaxToolIterations,
	}
	// Prefer the conversation's snapshotted provider/model so a resume uses the
	// same model that started the turn.
	if c, err := m.db.GetConversation(convID); err == nil {
		if c.Provider != "" {
			cfg.Provider = c.Provider
		}
		if c.Model != "" {
			cfg.Model = c.Model
		}
	}
	runner, err := m.buildRunner(p.Perms, cfg)
	if err != nil {
		_ = sink.Send("error", map[string]any{"message": err.Error()})
		return err
	}
	return runner.Resume(ctx, sink, convID, toolCallID, approved, p.ID)
}

// ChatOverrides are optional per-request provider/model/thinking overrides,
// shared by regenerate and edit-and-resend so a re-run honours the model the
// user currently has selected.
type ChatOverrides struct {
	Provider string
	Model    string
	Thinking string
}

// runTurn runs one turn over a conversation's EXISTING history plus `content`
// (which runner.Run appends as the user message), streaming to sink. Callers
// truncate the conversation first.
func (m *Manager) runTurn(ctx context.Context, sink Sink, p Principal, convID, content string, ov ChatOverrides) error {
	settings := m.resolvedSettings()
	cfg := agent.Config{
		Provider:       firstNonEmpty(ov.Provider, settings.Provider),
		Model:          firstNonEmpty(ov.Model, settings.Model),
		Thinking:       firstNonEmpty(ov.Thinking, settings.ThinkingLevel),
		System:         settings.SystemPrompt,
		ApprovalPolicy: settings.ApprovalPolicy,
		MaxIterations:  settings.MaxToolIterations,
	}
	runner, err := m.buildRunner(p.Perms, cfg)
	if err != nil {
		_ = sink.Send("error", map[string]any{"message": err.Error()})
		return err
	}
	return runner.Run(ctx, sink, convID, content, p.ID)
}

// RegenerateLast drops the last assistant turn and re-runs the last user message
// for a fresh answer.
func (m *Manager) RegenerateLast(ctx context.Context, sink Sink, p Principal, convID string, ov ChatOverrides) error {
	if !m.tryLockConv(convID) {
		_ = sink.Send("error", map[string]any{"message": ErrConversationBusy.Error()})
		return ErrConversationBusy
	}
	defer m.unlockConv(convID)
	msgs, err := m.db.ListMessages(convID, 0)
	if err != nil {
		_ = sink.Send("error", map[string]any{"message": err.Error()})
		return err
	}
	var last *database.AIMessage
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			last = msgs[i]
			break
		}
	}
	if last == nil {
		err := fmt.Errorf("nothing to regenerate")
		_ = sink.Send("error", map[string]any{"message": err.Error()})
		return err
	}
	if err := m.db.DeleteMessagesFromSeq(convID, last.Seq); err != nil {
		_ = sink.Send("error", map[string]any{"message": err.Error()})
		return err
	}
	return m.runTurn(ctx, sink, p, convID, last.Content, ov)
}

// EditAndResend rewrites a user message, drops it and everything after, and
// re-runs the turn with the new content.
func (m *Manager) EditAndResend(ctx context.Context, sink Sink, p Principal, convID, messageID, content string, ov ChatOverrides) error {
	if !m.tryLockConv(convID) {
		_ = sink.Send("error", map[string]any{"message": ErrConversationBusy.Error()})
		return ErrConversationBusy
	}
	defer m.unlockConv(convID)
	msg, err := m.db.GetMessage(messageID)
	if err != nil || msg.ConversationID != convID {
		e := fmt.Errorf("message not found")
		_ = sink.Send("error", map[string]any{"message": e.Error()})
		return e
	}
	if msg.Role != "user" {
		e := fmt.Errorf("only user messages can be edited")
		_ = sink.Send("error", map[string]any{"message": e.Error()})
		return e
	}
	if err := m.db.DeleteMessagesFromSeq(convID, msg.Seq); err != nil {
		_ = sink.Send("error", map[string]any{"message": err.Error()})
		return err
	}
	return m.runTurn(ctx, sink, p, convID, content, ov)
}

// DeleteMessageFrom truncates a conversation from a message onward (that message
// and everything after it), keeping the remaining history coherent. No re-run.
func (m *Manager) DeleteMessageFrom(convID, messageID string) error {
	if !m.tryLockConv(convID) {
		return ErrConversationBusy
	}
	defer m.unlockConv(convID)
	msg, err := m.db.GetMessage(messageID)
	if err != nil || msg.ConversationID != convID {
		return fmt.Errorf("message not found")
	}
	return m.db.DeleteMessagesFromSeq(convID, msg.Seq)
}

// buildRunner assembles a principal-scoped agent runner: the tool registry is
// gated to the caller's perms, the gateway is the shared client, and dispatch
// runs tools in-process.
func (m *Manager) buildRunner(perms auth.PermSet, cfg agent.Config) (*agent.Runner, error) {
	client, err := m.getClient()
	if err != nil {
		return nil, fmt.Errorf("ai gateway unavailable: %w", err)
	}
	reg := mcp.BuildAgentRegistry(m.deps, perms)
	tools := make([]agent.Tool, 0)
	for _, t := range reg.Tools() {
		tools = append(tools, agent.Tool{
			Def:         llm.ToolDef{Name: t.Name, Description: t.Description, Schema: t.Schema},
			Group:       t.Group,
			Perm:        t.Perm,
			ReadOnly:    t.ReadOnly,
			Destructive: t.Destructive,
		})
	}
	dispatch := func(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
		out, err := reg.Dispatch(ctx, name, args)
		if err != nil {
			return nil, err
		}
		b, mErr := json.Marshal(out)
		if mErr != nil {
			return nil, mErr
		}
		return b, nil
	}
	return agent.New(client, tools, dispatch, m.db, cfg), nil
}

// ─── settings ────────────────────────────────────────────────────────────────

// defaultSystemPrompt is a structured, token-efficient operator-agent prompt.
// Design choices (prompt-engineering best practice):
//   - Role + operating rules are explicit and concise (high signal, low tokens).
//   - The ~70 KB full reference is NOT inlined every request (that would blow up
//     cost and context); instead a condensed ESSENTIALS section covers the
//     high-frequency facts, and the agent is told to call get_orva_docs for the
//     complete, always-current reference on any detail. The docs the tool
//     returns are the single-source docs/reference.md, embedded at build time —
//     so they are always up to date with the running build.
//   - The whole prompt is stable across requests, so providers with prompt
//     caching (Anthropic/OpenAI) cache it and charge a fraction after the first
//     call (see cache hints in internal/ai/llm).
const defaultSystemPrompt = `# Role
You are Orva's built-in operator assistant, embedded in the Orva dashboard. Orva
is a self-hosted serverless platform (Functions-as-a-Service): users write
JavaScript/TypeScript (Node 22/24) or Python (3.13/3.14) handlers; Orva builds
them and runs them in nsjail sandboxes, exposed over HTTP. You operate THIS
instance on the user's behalf through tools.

# How to work
- Act, don't narrate. Prefer doing the task with tools over describing how the
  user could do it. Take the next concrete step.
- Inspect before you mutate. Read current state (list_*/get_*) before changing it.
- Don't guess Orva's APIs, handler contract, SDK, config, or limits. When you
  need authoritative detail beyond the essentials below, call get_orva_docs — it
  returns the complete, current Orva reference. Avoid re-fetching the same topic
  in a turn; consulting it again for a different area is fine.
- Writes are gated by the approval policy (default all_writes). Reads and invokes
  never pause. Under all_writes every write pauses for operator approval;
  destructive_only pauses only destructive-marked tools; auto runs everything.
  Just request the write — the platform enforces the gate. A gated call pauses
  the turn (it shows as awaiting approval) and resumes with the result once
  approved; never assume a write ran until you see its result.
- Destructive tools (delete_*, rollback_function, bulk_delete_executions,
  kv_delete, system_vacuum) need an explicit confirm=true argument — a separate
  guard the tool enforces in code even when approval is auto. Set confirm=true
  only when the user clearly asked for that destructive action, and never retry a
  failed destructive call without fresh user intent.
- Close the loop on failures: if an invocation or build fails, read the full
  build/execution log + stderr, diagnose ALL issues at once (syntax, deps,
  logic), fix them together, and redeploy once — don't redeploy after each single
  fix, and don't stop at the first error.
- Spend steps wisely. Each turn has a bounded tool budget (about 25 calls). Batch
  work (deploy then invoke once; fix everything from one log) rather than
  one-tool-per-item loops. If you run low, finish the highest-value step and tell
  the user what remains.

# Handling queries
- "How do I…/what does Orva support…" → answer from the essentials; for anything
  deeper or version-specific, get_orva_docs first, then answer precisely.
- "Build/deploy/fix X" → create_function → deploy_function_inline (the handler
  source, wait=true) → invoke_function to verify → on failure read the full log,
  fix all issues together, redeploy once.
- "Why is X failing/slow" → list_executions / get_execution_logs / list_traces /
  get_function_baseline, then explain and offer a fix.
- Only ask the user a question when a choice genuinely can't be inferred.

# Orva essentials (quick reference; use get_orva_docs for the full spec)
- Runtimes: node22, node24, python313, python314 (TypeScript runs on the node
  runtime). Entrypoint defaults: handler.js (JS/TS) or handler.py (Python).
- Handler contract: the handler receives an event (method, path, headers, body,
  query) and returns a response (statusCode, headers, body). Exact shape per
  runtime is in get_orva_docs.
- Ship a function: create_function (name, runtime, limits) → deploy_function_inline
  (code [+ dependencies = package.json/requirements.txt], wait=true) →
  invoke_function (method REQUIRED) → get_execution_logs on failure.
- Networking: network_mode is "none" (loopback only — the default) or "egress"
  (outbound HTTPS + the in-sandbox SDK). Set egress at create time if the handler
  uses the orva SDK (kv/invoke/jobs) or any external HTTPS. Flipping it drains the
  warm pool, so the next invoke is a (normal) cold start. ENETUNREACH/ECONNREFUSED
  on an SDK/HTTP handler means it's still on "none" — switch to egress and retry.
- In-sandbox SDK (egress): per-function KV (orva.kv), function-to-function invoke
  (orva.invoke), and background jobs (orva.jobs).
- set_secret/delete_secret are idempotent but drain the function's warm pool (next
  invoke is a cold start); don't loop secret writes in production.
- Also available: secrets (encrypted env injection), custom routes, cron
  schedules, background jobs with retries, system-event webhooks, signed inbound
  webhooks, saved request fixtures, egress firewall rules, causal traces, and
  system health/metrics/storage. One tool per capability.

# Output
- First line = the user-facing outcome (for example: Deployed hello v5; invoke
  returned 200). Then optional brief detail or next steps. Keep prose tight.
- Wrap tool names, ids, config keys, and short values in inline code. Use fenced
  code blocks, always with a language label (python, bash, json), for handlers,
  command output, or JSON. Tool outputs (logs, JSON) auto-render in collapsible
  cards and large code auto-collapses, so don't re-print raw tool output or
  apologize for length. Default to prose and bullet lists — including for
  multi-attribute entity listings (functions, jobs, executions, secrets, cron):
  give each item ONE bullet with its details inline (e.g. "hello: node24, active,
  egress"). Do NOT render a routine listing as a Markdown table. Use a table ONLY
  when the user explicitly asks to compare items side by side.
- Surface execution/trace/job ids in prose only when the user is debugging a
  specific run; otherwise they already show in tool cards — keep summaries clean.
- Never surface secret/key/token values — not even prefixes or hashes. The secret
  and key tools never return plaintext; the only way a secret reaches you is if
  user code logged it. If a value appears in logs, stderr, or KV, redact it and
  describe the symptom (for example: invalid auth header), not the value.`

// resolvedSettings returns the stored settings with built-in defaults filled
// in for any missing field.
func (m *Manager) resolvedSettings() database.AISettings {
	s, ok, err := m.db.GetSettings("default")
	if err != nil {
		// Don't mask DB corruption behind silent defaults — log, then fall back.
		slog.Warn("ai: load settings failed; using defaults", "error", err)
	}
	if s == nil {
		s = &database.AISettings{ID: "default"}
	}
	if !ok || s.Provider == "" {
		s.Provider = "anthropic"
	}
	if s.Model == "" {
		s.Model = "claude-opus-4-8"
	}
	if s.ThinkingLevel == "" {
		s.ThinkingLevel = "standard"
	}
	if s.ApprovalPolicy == "" {
		s.ApprovalPolicy = "all_writes"
	}
	if s.MaxToolIterations <= 0 {
		s.MaxToolIterations = 25
	}
	if strings.TrimSpace(s.SystemPrompt) == "" {
		s.SystemPrompt = defaultSystemPrompt
	}
	return *s
}

// Settings returns the resolved settings for the API (with defaults applied).
func (m *Manager) Settings() database.AISettings { return m.resolvedSettings() }

// SaveSettings persists settings, validating enums.
func (m *Manager) SaveSettings(in database.AISettings) (database.AISettings, error) {
	in.ID = "default"
	if !validThinking(in.ThinkingLevel) {
		return database.AISettings{}, fmt.Errorf("invalid thinking_level %q", in.ThinkingLevel)
	}
	if !validApproval(in.ApprovalPolicy) {
		return database.AISettings{}, fmt.Errorf("invalid approval_policy %q", in.ApprovalPolicy)
	}
	if err := m.db.UpsertSettings(&in); err != nil {
		return database.AISettings{}, err
	}
	return m.resolvedSettings(), nil
}

func validThinking(v string) bool {
	switch v {
	case "", "off", "standard", "deep":
		return true
	}
	return false
}

func validApproval(v string) bool {
	switch v {
	case "", "all_writes", "destructive_only", "auto":
		return true
	}
	return false
}

// ─── providers ───────────────────────────────────────────────────────────────

// ProviderView is the API projection of a provider config — the key is never
// returned, only whether one is set.
type ProviderView struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	Label    string `json:"label"`
	HasKey   bool   `json:"has_key"`
	BaseURL  string `json:"base_url,omitempty"`
	Enabled  bool   `json:"enabled"`
}

func toProviderView(c *database.AIProviderConfig) ProviderView {
	return ProviderView{
		ID: c.ID, Provider: c.Provider, Label: c.Label,
		HasKey: c.APIKeyEncrypted != "", BaseURL: c.BaseURL, Enabled: c.Enabled,
	}
}

// ListProviders returns all configured providers (keys redacted).
func (m *Manager) ListProviders() ([]ProviderView, error) {
	cfgs, err := m.db.ListProviderConfigs()
	if err != nil {
		return nil, err
	}
	out := make([]ProviderView, 0, len(cfgs))
	for _, c := range cfgs {
		out = append(out, toProviderView(c))
	}
	return out, nil
}

// ProviderInput is the write payload for a provider config. APIKey is plaintext
// and only used on this request; an empty APIKey on update leaves the stored
// key untouched.
type ProviderInput struct {
	Provider    string `json:"provider"`
	Label       string `json:"label"`
	APIKey      string `json:"api_key"`
	BaseURL     string `json:"base_url"`
	ExtraConfig string `json:"extra_config"`
	Enabled     *bool  `json:"enabled"`
}

// SaveProvider creates or updates a provider config, encrypting the key.
func (m *Manager) SaveProvider(in ProviderInput) (ProviderView, error) {
	provider := strings.ToLower(strings.TrimSpace(in.Provider))
	if provider == "" {
		return ProviderView{}, fmt.Errorf("provider is required")
	}
	cfg := &database.AIProviderConfig{
		Provider:    provider,
		Label:       in.Label,
		BaseURL:     normalizeBaseURL(in.BaseURL),
		ExtraConfig: in.ExtraConfig,
		Enabled:     in.Enabled == nil || *in.Enabled,
	}
	if strings.TrimSpace(in.APIKey) != "" {
		enc, err := m.secrets.EncryptValue(in.APIKey)
		if err != nil {
			return ProviderView{}, err
		}
		cfg.APIKeyEncrypted = enc
	}
	saved, err := m.db.UpsertProviderConfig(cfg)
	if err != nil {
		return ProviderView{}, err
	}
	m.invalidateClient() // pick up the new/changed credentials
	return toProviderView(saved), nil
}

// DeleteProvider removes a provider config.
func (m *Manager) DeleteProvider(id string) error {
	if err := m.db.DeleteProviderConfig(id); err != nil {
		return err
	}
	m.invalidateClient()
	return nil
}

// ─── model catalog ───────────────────────────────────────────────────────────

// ModelInfo is one selectable model.
type ModelInfo struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Provider string `json:"provider"`
}

// ProviderModels lists the models a configured provider actually exposes, by
// querying its OpenAI-compatible /v1/models endpoint with the stored
// credentials — fully dynamic, never hardcoded. Works for any OpenAI-compatible
// gateway (incl. self-hosted custom endpoints) and Anthropic. Returns the
// models plus a non-nil error string the UI can surface if the listing failed
// (so the user can fall back to typing a model id).
func (m *Manager) ProviderModels(id string) ([]ModelInfo, error) {
	cfg, err := m.db.GetProviderConfig(id)
	if err != nil {
		return nil, err
	}
	key := ""
	if cfg.APIKeyEncrypted != "" {
		if k, derr := m.secrets.DecryptValue(cfg.APIKeyEncrypted); derr == nil {
			key = k
		}
	}
	root := normalizeBaseURL(cfg.BaseURL)
	if root == "" {
		root = defaultRoot(cfg.Provider)
	}
	if root == "" {
		return nil, fmt.Errorf("no base URL configured for provider %q", cfg.Provider)
	}

	req, err := http.NewRequest(http.MethodGet, root+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	if cfg.Provider == "anthropic" {
		req.Header.Set("x-api-key", key)
		req.Header.Set("anthropic-version", "2023-06-01")
	} else if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("reach %s: %w", root, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models endpoint returned %d", resp.StatusCode)
	}
	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse models response: %w", err)
	}
	out := make([]ModelInfo, 0, len(parsed.Data))
	for _, d := range parsed.Data {
		if d.ID != "" {
			out = append(out, ModelInfo{ID: d.ID, Label: d.ID, Provider: cfg.Provider})
		}
	}
	return out, nil
}

// normalizeBaseURL strips a trailing slash and a trailing "/v1" so the stored
// value is the provider ROOT. Bifrost (and our /v1/models probe) append the
// "/v1/…" path themselves, so a user can paste either "https://host" or the
// more natural "https://host/v1" and both work.
func normalizeBaseURL(u string) string {
	u = strings.TrimSpace(u)
	u = strings.TrimRight(u, "/")
	u = strings.TrimSuffix(u, "/v1")
	return strings.TrimRight(u, "/")
}

// defaultRoot is the provider ROOT (no /v1) used when no custom base URL is set.
func defaultRoot(provider string) string {
	switch provider {
	case "openai":
		return "https://api.openai.com"
	case "groq":
		return "https://api.groq.com/openai"
	case "anthropic":
		return "https://api.anthropic.com"
	case "mistral":
		return "https://api.mistral.ai"
	case "openrouter":
		return "https://openrouter.ai/api"
	case "xai":
		return "https://api.x.ai"
	case "cohere":
		return "https://api.cohere.ai/compatibility"
	default:
		return ""
	}
}

// ─── conversation passthrough (thin wrappers so the handler stays in one pkg) ─

func (m *Manager) ListConversations(userID string, includeArchived bool, limit, offset int) ([]*database.AIConversation, error) {
	return m.db.ListConversations(userID, includeArchived, limit, offset)
}

// ConversationDetail bundles a conversation with its messages + tool calls for
// rehydration on page load.
type ConversationDetail struct {
	Conversation *database.AIConversation `json:"conversation"`
	Messages     []*database.AIMessage    `json:"messages"`
	ToolCalls    []*database.AIToolCall   `json:"tool_calls"`
}

func (m *Manager) GetConversation(id string) (*ConversationDetail, error) {
	c, err := m.db.GetConversation(id)
	if err != nil {
		return nil, err
	}
	msgs, err := m.db.ListMessages(id, 0)
	if err != nil {
		return nil, err
	}
	calls, err := m.db.ListToolCalls(id)
	if err != nil {
		return nil, err
	}
	return &ConversationDetail{Conversation: c, Messages: msgs, ToolCalls: calls}, nil
}

func (m *Manager) CreateConversation(userID, title string) (*database.AIConversation, error) {
	c := &database.AIConversation{UserID: userID, Title: title}
	if err := m.db.CreateConversation(c); err != nil {
		return nil, err
	}
	return c, nil
}

func (m *Manager) UpdateConversation(id string, title *string, archived *bool) (*database.AIConversation, error) {
	return m.db.UpdateConversation(id, title, archived)
}

func (m *Manager) DeleteConversation(id string) error { return m.db.DeleteConversation(id) }

func (m *Manager) ListMessages(convID string, sinceSeq int) ([]*database.AIMessage, error) {
	return m.db.ListMessages(convID, sinceSeq)
}

func (m *Manager) GetToolCall(id string) (*database.AIToolCall, error) {
	return m.db.GetToolCall(id)
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func titleFrom(content string) string {
	t := strings.TrimSpace(strings.SplitN(content, "\n", 2)[0])
	if len(t) > 60 {
		t = t[:60] + "…"
	}
	if t == "" {
		t = "New conversation"
	}
	return t
}
