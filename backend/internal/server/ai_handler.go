package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/ai"
	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers/respond"
	"github.com/Harsh-2002/Orva/backend/internal/urlhint"
)

// AIHandler serves /api/v1/ai/* — the in-product AI chat assistant. Chat and
// tool-approval stream Server-Sent Events; everything else is JSON. It lives in
// package server (not handlers) because it needs ActorFromContext for the
// caller's identity + permissions, which scope the agent's tool catalog.
type AIHandler struct {
	Manager *ai.Manager
	DB      *database.Database
}

// ─── identity ────────────────────────────────────────────────────────────────

// principal resolves the caller into an ai.Principal. Web sessions are the
// operator and get full access; API keys are scoped to their permission set.
func (h *AIHandler) principal(r *http.Request) ai.Principal {
	a := ActorFromContext(r.Context())
	if a == nil {
		return ai.Principal{Perms: auth.PermSet{}}
	}
	p := ai.Principal{ID: a.ID, Label: a.Label}
	if a.Type == "api_key" {
		ps := auth.PermSet{}
		if k, err := h.DB.GetAPIKeyByID(a.ID); err == nil {
			for _, perm := range k.PermissionsList() {
				ps[perm] = true
			}
		}
		p.Perms = ps
		return p
	}
	// session / internal operator → full access (the dashboard is the operator console).
	p.Perms = auth.PermSet{"read": true, "write": true, "invoke": true, "admin": true}
	return p
}

// turnCtx is the request context with the instance's per-request base URL
// attached, so the agent's tools and system prompt produce real invoke URLs
// for this host (same source of truth the external MCP server uses).
func turnCtx(r *http.Request) context.Context {
	return ai.WithBaseURL(r.Context(), urlhint.BaseURL(r))
}

// ─── SSE sink ────────────────────────────────────────────────────────────────

type sseSink struct {
	w  http.ResponseWriter
	f  http.Flusher
	mu sync.Mutex
}

func (s *sseSink) Send(event string, data any) error {
	b, err := json.Marshal(data)
	if err != nil {
		b = []byte(`{}`)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := fmt.Fprintf(s.w, "event: %s\ndata: %s\n\n", event, b); err != nil {
		return err
	}
	s.f.Flush()
	return nil
}

// Ping writes an SSE comment frame to keep the connection alive during long
// pre-token model "thinking" gaps. Comment frames (lines not starting with
// event:/data:) are ignored by the client parser, so they're invisible to the UI.
func (s *sseSink) Ping() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := fmt.Fprint(s.w, ": ping\n\n"); err == nil {
		s.f.Flush()
	}
}

// heartbeat pings the sink every 15s until the returned stop() is called.
// Safe to run concurrently with Send (both take the sink mutex).
//
// stop() WAITS for the goroutine to exit. It used to only close(done) and
// return, so a ping already blocked on the sink mutex would win the race,
// wake up after the handler had returned, and write to a ResponseWriter the
// server had recycled for another request.
func heartbeat(sink *sseSink) (stop func()) {
	done := make(chan struct{})
	exited := make(chan struct{})
	go func() {
		defer close(exited)
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				sink.Ping()
			}
		}
	}()
	return func() {
		close(done)
		<-exited
	}
}

func startSSE(w http.ResponseWriter) (*sseSink, bool) {
	f, ok := w.(http.Flusher)
	if !ok {
		return nil, false
	}
	// Clear the server's WriteTimeout for this response. SSE chat turns are
	// long-lived (deep thinking + several tool calls + analysis routinely exceed
	// the 60s default), and the write deadline would otherwise cancel the
	// in-flight model stream mid-turn ("context canceled"), making the chat
	// appear to freeze. Liveness is still bounded by the 15s heartbeat and by
	// request-context cancellation when the client disconnects.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	f.Flush()
	return &sseSink{w: w, f: f}, true
}

// ─── chat (SSE) ──────────────────────────────────────────────────────────────

func (h *AIHandler) ChatStream(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ConversationID string `json:"conversation_id"`
		Content        string `json:"content"`
		Provider       string `json:"provider"`
		Model          string `json:"model"`
		Thinking       string `json:"thinking"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body", RequestID(r.Context()))
		return
	}
	if body.Content == "" {
		respond.Error(w, http.StatusBadRequest, "BAD_REQUEST", "content is required", RequestID(r.Context()))
		return
	}
	sink, ok := startSSE(w)
	if !ok {
		respond.Error(w, http.StatusInternalServerError, "NO_STREAM", "streaming unsupported", RequestID(r.Context()))
		return
	}
	defer heartbeat(sink)()
	// Errors are streamed as SSE `error` frames by the ai layer (it owns the
	// sink); the returned error is logged for operator visibility.
	_, err := h.Manager.Chat(turnCtx(r), sink, h.principal(r), ai.ChatParams{
		ConversationID: body.ConversationID,
		Content:        body.Content,
		Provider:       body.Provider,
		Model:          body.Model,
		Thinking:       body.Thinking,
	})
	logAIError(r, "chat", err)
}

// logAIError surfaces a manager-returned error in the server log. Expected,
// non-actionable conditions (client disconnect, conversation-busy) are skipped
// so the log stays signal.
func logAIError(r *http.Request, op string, err error) {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, ai.ErrConversationBusy) {
		return
	}
	slog.Warn("ai "+op+" failed", "error", err, "request_id", RequestID(r.Context()))
}

// ─── tool approval (SSE — resumes the paused turn) ───────────────────────────

func (h *AIHandler) ApproveTool(w http.ResponseWriter, r *http.Request) { h.resume(w, r, true) }
func (h *AIHandler) RejectTool(w http.ResponseWriter, r *http.Request)  { h.resume(w, r, false) }

func (h *AIHandler) resume(w http.ResponseWriter, r *http.Request, approved bool) {
	id := r.PathValue("id")
	tc, err := h.Manager.GetToolCall(id)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "tool call not found", RequestID(r.Context()))
		return
	}
	sink, ok := startSSE(w)
	if !ok {
		respond.Error(w, http.StatusInternalServerError, "NO_STREAM", "streaming unsupported", RequestID(r.Context()))
		return
	}
	defer heartbeat(sink)()
	// As with chat, the ai layer streams any error as an SSE `error` frame.
	logAIError(r, "resume", h.Manager.Resume(turnCtx(r), sink, h.principal(r), tc.ConversationID, id, approved))
}

// ─── conversations (JSON) ────────────────────────────────────────────────────

func (h *AIHandler) ListConversations(w http.ResponseWriter, r *http.Request) {
	archived := r.URL.Query().Get("archived") == "true"
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	// Single-operator instance: list all conversations (see manager.go).
	convs, err := h.Manager.ListConversations("", archived, limit, offset)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "DB_ERROR", err.Error(), RequestID(r.Context()))
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"conversations": convs})
}

func (h *AIHandler) CreateConversation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title string `json:"title"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	c, err := h.Manager.CreateConversation(h.principal(r).ID, body.Title)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "DB_ERROR", err.Error(), RequestID(r.Context()))
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"conversation": c})
}

func (h *AIHandler) GetConversation(w http.ResponseWriter, r *http.Request) {
	detail, err := h.Manager.GetConversation(r.PathValue("id"))
	if err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", "conversation not found", RequestID(r.Context()))
		return
	}
	respond.JSON(w, http.StatusOK, detail)
}

func (h *AIHandler) PatchConversation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title    *string `json:"title"`
		Archived *bool   `json:"archived"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body", RequestID(r.Context()))
		return
	}
	c, err := h.Manager.UpdateConversation(r.PathValue("id"), body.Title, body.Archived)
	if err != nil {
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", err.Error(), RequestID(r.Context()))
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"conversation": c})
}

func (h *AIHandler) DeleteConversation(w http.ResponseWriter, r *http.Request) {
	if err := h.Manager.DeleteConversation(r.PathValue("id")); err != nil {
		respond.Error(w, http.StatusInternalServerError, "DB_ERROR", err.Error(), RequestID(r.Context()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AIHandler) DeleteAllConversations(w http.ResponseWriter, r *http.Request) {
	deleted, err := h.Manager.DeleteAllConversations()
	if errors.Is(err, ai.ErrConversationBusy) {
		respond.Error(w, http.StatusConflict, "CONVERSATION_BUSY", err.Error(), RequestID(r.Context()))
		return
	}
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "DB_ERROR", err.Error(), RequestID(r.Context()))
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"deleted": deleted})
}

func (h *AIHandler) ListMessages(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.Atoi(r.URL.Query().Get("since_seq"))
	msgs, err := h.Manager.ListMessages(r.PathValue("id"), since)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "DB_ERROR", err.Error(), RequestID(r.Context()))
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"messages": msgs})
}

// Regenerate (SSE) drops the last assistant turn and re-runs the last user
// message for a fresh answer.
func (h *AIHandler) Regenerate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
		Thinking string `json:"thinking"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	sink, ok := startSSE(w)
	if !ok {
		respond.Error(w, http.StatusInternalServerError, "NO_STREAM", "streaming unsupported", RequestID(r.Context()))
		return
	}
	defer heartbeat(sink)()
	logAIError(r, "regenerate", h.Manager.RegenerateLast(turnCtx(r), sink, h.principal(r), r.PathValue("id"),
		ai.ChatOverrides{Provider: body.Provider, Model: body.Model, Thinking: body.Thinking}))
}

// EditMessage (SSE) rewrites a user message, truncates from it, and re-runs.
func (h *AIHandler) EditMessage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content  string `json:"content"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
		Thinking string `json:"thinking"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Content == "" {
		respond.Error(w, http.StatusBadRequest, "BAD_REQUEST", "content is required", RequestID(r.Context()))
		return
	}
	sink, ok := startSSE(w)
	if !ok {
		respond.Error(w, http.StatusInternalServerError, "NO_STREAM", "streaming unsupported", RequestID(r.Context()))
		return
	}
	defer heartbeat(sink)()
	logAIError(r, "edit", h.Manager.EditAndResend(turnCtx(r), sink, h.principal(r), r.PathValue("id"), r.PathValue("mid"), body.Content,
		ai.ChatOverrides{Provider: body.Provider, Model: body.Model, Thinking: body.Thinking}))
}

// DeleteMessage truncates a conversation from a message onward (JSON, no re-run).
func (h *AIHandler) DeleteMessage(w http.ResponseWriter, r *http.Request) {
	if err := h.Manager.DeleteMessageFrom(r.PathValue("id"), r.PathValue("mid")); err != nil {
		if errors.Is(err, ai.ErrConversationBusy) {
			respond.Error(w, http.StatusConflict, "CONVERSATION_BUSY", err.Error(), RequestID(r.Context()))
			return
		}
		respond.Error(w, http.StatusNotFound, "NOT_FOUND", err.Error(), RequestID(r.Context()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── providers (JSON) ────────────────────────────────────────────────────────

func (h *AIHandler) ListProviders(w http.ResponseWriter, r *http.Request) {
	ps, err := h.Manager.ListProviders()
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "DB_ERROR", err.Error(), RequestID(r.Context()))
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"providers": ps})
}

func (h *AIHandler) SaveProvider(w http.ResponseWriter, r *http.Request) {
	var in ai.ProviderInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		respond.Error(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body", RequestID(r.Context()))
		return
	}
	v, err := h.Manager.SaveProvider(in)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), RequestID(r.Context()))
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"provider": v})
}

func (h *AIHandler) DeleteProvider(w http.ResponseWriter, r *http.Request) {
	if err := h.Manager.DeleteProvider(r.PathValue("id")); err != nil {
		respond.Error(w, http.StatusInternalServerError, "DB_ERROR", err.Error(), RequestID(r.Context()))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── models + settings (JSON) ────────────────────────────────────────────────

// ListProviderModels dynamically lists the models a configured provider exposes
// (queried live from its /v1/models endpoint). Returns 200 with {models, error?}
// — a non-empty error means the listing failed and the UI should let the user
// type a model id manually.
func (h *AIHandler) ListProviderModels(w http.ResponseWriter, r *http.Request) {
	models, err := h.Manager.ProviderModels(r.PathValue("id"))
	out := map[string]any{"models": models}
	if err != nil {
		out["models"] = []any{}
		out["error"] = err.Error()
	}
	respond.JSON(w, http.StatusOK, out)
}

func (h *AIHandler) GetSettings(w http.ResponseWriter, r *http.Request) {
	respond.JSON(w, http.StatusOK, map[string]any{"settings": h.Manager.Settings()})
}

func (h *AIHandler) PutSettings(w http.ResponseWriter, r *http.Request) {
	var in database.AISettings
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		respond.Error(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body", RequestID(r.Context()))
		return
	}
	s, err := h.Manager.SaveSettings(in)
	if err != nil {
		respond.Error(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), RequestID(r.Context()))
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"settings": s})
}

// PutSelection persists just the operator's active provider/model choice (not
// the full settings row), so the selection follows them across devices and each
// provider recalls its last model. Returns the resolved settings.
func (h *AIHandler) PutSelection(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ProviderID string `json:"provider_id"`
		Provider   string `json:"provider"`
		Model      string `json:"model"`
		Thinking   string `json:"thinking"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		respond.Error(w, http.StatusBadRequest, "BAD_REQUEST", "invalid JSON body", RequestID(r.Context()))
		return
	}
	s, err := h.Manager.SaveSelection(in.ProviderID, in.Provider, in.Model, in.Thinking)
	if err != nil {
		respond.Error(w, http.StatusInternalServerError, "DB_ERROR", err.Error(), RequestID(r.Context()))
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"settings": s})
}
