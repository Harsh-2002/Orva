package sdkhost

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
)

const (
	maxInvokeBody      = 1 << 20
	maxStreamChunk     = 256 << 10
	maxStreamsPerChan  = 8
	maxStreamTombstone = 1024
)

// WithInvoker supplies the F2F core (DB, pool, metrics, SDKAuth, MaxCallDepth)
// that backs invoke and invoke.stream.*. Without one those ops reply 501.
func WithInvoker(inv *handlers.InternalInvokeHandler) Option {
	return func(h *Host) { h.invoker.Store(inv) }
}

// SetInvoker is WithInvoker for callers that must build the pool first.
func (h *Host) SetInvoker(inv *handlers.InternalInvokeHandler) { h.invoker.Store(inv) }

type invokeArgs struct {
	Name string          `json:"name"`
	Body json.RawMessage `json:"body"`
}

func serr(status int, code, msg string) *sandbox.SDKError {
	return &sandbox.SDKError{Status: status, Code: code, Message: msg}
}

func fromInvokeErr(e *handlers.InvokeError) *sandbox.SDKError {
	return serr(e.Status, e.Opts.Code, e.Opts.Message)
}

// prepared is a validated F2F call.
type prepared struct {
	inv    *handlers.InternalInvokeHandler
	fn     *database.Function
	caller handlers.F2FCaller
	body   []byte
	parent context.Context
}

// prepare mirrors the HTTP handlers' check order: name, depth, callee, body.
// Identity (trace, span, depth) comes only from caller.Exec.
func (h *Host) prepare(ctx context.Context, caller sandbox.SDKCaller, args json.RawMessage) (*prepared, *sandbox.SDKError) {
	inv := h.invoker.Load()
	if inv == nil {
		return nil, serr(501, "NOT_IMPLEMENTED", "operation not implemented on the SDK channel yet")
	}
	if caller.Exec == nil {
		return nil, serr(403, "SDK_SCOPE_VIOLATION", "SDK request is not associated with an active caller execution")
	}
	if e := revoked(ctx, caller.Exec); e != nil {
		return nil, e
	}
	var a invokeArgs
	if e := decodeArgs(args, sandbox.MaxSDKFrame, &a); e != nil {
		return nil, e
	}
	if a.Name == "" {
		return nil, serr(400, "VALIDATION", "function name is required")
	}
	if ie := inv.CheckDepth(caller.Exec.Depth); ie != nil {
		return nil, fromInvokeErr(ie)
	}
	fn, ie := inv.ResolveCallee(a.Name)
	if ie != nil {
		return nil, fromInvokeErr(ie)
	}
	body := []byte(a.Body)
	if len(body) == 0 {
		body = []byte("{}")
	}
	if len(body) > maxInvokeBody {
		return nil, serr(400, "INVALID_BODY", "failed to read body")
	}
	parent := caller.Exec.Ctx
	if parent == nil {
		parent = ctx
	}
	return &prepared{inv: inv, fn: fn, body: body, parent: parent, caller: handlers.F2FCaller{
		FunctionID: caller.FunctionID, TraceID: caller.Exec.TraceID, SpanID: caller.Exec.SpanID, Depth: caller.Exec.Depth,
	}}, nil
}

func (h *Host) invoke(ctx context.Context, caller sandbox.SDKCaller, args json.RawMessage) (any, *sandbox.SDKError) {
	p, e := h.prepare(ctx, caller, args)
	if e != nil {
		return nil, e
	}
	resp, ie := p.inv.RunInvoke(p.parent, p.caller, p.fn, p.body, "")
	if ie != nil {
		return nil, fromInvokeErr(ie)
	}
	return json.RawMessage(resp), nil
}

// ---- streams ----

type chStream struct {
	s     *handlers.F2FStream
	lease string

	mu      sync.Mutex
	pending []byte
	ended   bool
}

// streamTable is the per-channel stream state held in the SDKSession.
type streamTable struct {
	mu      sync.Mutex
	closed  bool
	opening int
	m       map[string]*chStream
	// tomb remembers closed handles (handle -> lease) so close stays idempotent
	// while a handle from another lease is still NOT_FOUND.
	tomb map[string]string
}

func newStreamTable() sandbox.SDKSessionState {
	return &streamTable{m: map[string]*chStream{}, tomb: map[string]string{}}
}

func (t *streamTable) LeaseRevoked(lease string) {
	t.mu.Lock()
	var gone []*chStream
	for k, cs := range t.m {
		if cs.lease == lease {
			gone = append(gone, cs)
			delete(t.m, k)
		}
	}
	for k, l := range t.tomb {
		if l == lease {
			delete(t.tomb, k)
		}
	}
	t.mu.Unlock()
	for _, cs := range gone {
		cs.s.Finish(context.Canceled)
	}
}

func (t *streamTable) Closed() {
	t.mu.Lock()
	t.closed = true
	gone := t.m
	t.m = map[string]*chStream{}
	t.tomb = map[string]string{}
	t.mu.Unlock()
	for _, cs := range gone {
		cs.s.Finish(context.Canceled)
	}
}

// retire removes a handle, leaving a tombstone, and reports the stream.
func (t *streamTable) retire(handle string) *chStream {
	t.mu.Lock()
	defer t.mu.Unlock()
	cs := t.m[handle]
	if cs == nil {
		return nil
	}
	delete(t.m, handle)
	if len(t.tomb) < maxStreamTombstone {
		t.tomb[handle] = cs.lease
	}
	return cs
}

func (t *streamTable) lookup(handle, lease string) *chStream {
	t.mu.Lock()
	defer t.mu.Unlock()
	if cs := t.m[handle]; cs != nil && cs.lease == lease {
		return cs
	}
	return nil
}

func sessionTable(ctx context.Context) (*streamTable, *sandbox.SDKError) {
	sess := sandbox.SDKSessionFrom(ctx)
	if sess == nil {
		return nil, serr(500, "INTERNAL", "no SDK channel session")
	}
	st := sess.State(newStreamTable)
	if st == nil {
		return nil, serr(403, "SDK_SCOPE_VIOLATION", "channel is closed")
	}
	return st.(*streamTable), nil
}

func newHandle() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("sdkhost: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

func (h *Host) streamOpen(ctx context.Context, caller sandbox.SDKCaller, args json.RawMessage) (any, *sandbox.SDKError) {
	p, e := h.prepare(ctx, caller, args)
	if e != nil {
		return nil, e
	}
	t, e := sessionTable(ctx)
	if e != nil {
		return nil, e
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return nil, serr(403, "SDK_SCOPE_VIOLATION", "channel is closed")
	}
	if len(t.m)+t.opening >= maxStreamsPerChan {
		t.mu.Unlock()
		return nil, serr(429, "TOO_MANY_STREAMS", "too many open streams on this channel")
	}
	t.opening++
	t.mu.Unlock()

	s, ie := p.inv.OpenStream(p.parent, p.caller, p.fn, p.body, "")
	t.mu.Lock()
	t.opening--
	if ie != nil {
		t.mu.Unlock()
		return nil, fromInvokeErr(ie)
	}
	cs := &chStream{s: s, lease: caller.Exec.Lease}
	if !s.Streaming {
		cs.pending, cs.ended = []byte(s.Body), true
	}
	handle := newHandle()
	// A lease revoked while the callee was starting has already run
	// LeaseRevoked, which could not see this stream.
	if t.closed || p.parent.Err() != nil {
		t.mu.Unlock()
		s.Finish(context.Canceled)
		return nil, serr(499, "CLIENT_DISCONNECTED", "caller execution ended")
	}
	t.m[handle] = cs
	t.mu.Unlock()
	hdr := s.Headers
	if hdr == nil {
		hdr = map[string]string{}
	}
	return map[string]any{"handle": handle, "status": s.StatusCode, "headers": hdr}, nil
}

type handleArgs struct {
	Handle string `json:"handle"`
}

func (h *Host) streamNext(ctx context.Context, caller sandbox.SDKCaller, args json.RawMessage) (any, *sandbox.SDKError) {
	if caller.Exec == nil {
		return nil, serr(403, "SDK_SCOPE_VIOLATION", "SDK request is not associated with an active caller execution")
	}
	var a handleArgs
	if e := decodeArgs(args, limitSmall, &a); e != nil {
		return nil, e
	}
	if a.Handle == "" {
		return nil, serr(400, "VALIDATION", "handle is required")
	}
	t, e := sessionTable(ctx)
	if e != nil {
		return nil, e
	}
	cs := t.lookup(a.Handle, caller.Exec.Lease)
	if cs == nil {
		return nil, serr(404, "NOT_FOUND", "stream not found")
	}
	cs.mu.Lock()
	defer cs.mu.Unlock()
	for len(cs.pending) == 0 && !cs.ended {
		kind, data, err := cs.s.NextFrame()
		if err != nil {
			t.retire(a.Handle)
			cs.s.Finish(nil)
			msg := err.Error()
			if errors.Is(err, context.DeadlineExceeded) {
				msg = "function timed out"
			}
			return nil, serr(http.StatusBadGateway, "INVOKE_FAILED", msg)
		}
		if kind == "end" {
			cs.ended = true
		} else if len(data) > 0 { // empty chunks are adapter heartbeats
			cs.pending = data
		}
	}
	if len(cs.pending) > 0 {
		n := min(len(cs.pending), maxStreamChunk)
		chunk := cs.pending[:n]
		cs.pending = cs.pending[n:]
		return map[string]any{"chunk_b64": base64.StdEncoding.EncodeToString(chunk), "done": false}, nil
	}
	t.retire(a.Handle)
	cs.s.Finish(nil)
	return map[string]any{"chunk_b64": "", "done": true}, nil
}

func (h *Host) streamClose(ctx context.Context, caller sandbox.SDKCaller, args json.RawMessage) (any, *sandbox.SDKError) {
	if caller.Exec == nil {
		return nil, serr(403, "SDK_SCOPE_VIOLATION", "SDK request is not associated with an active caller execution")
	}
	var a handleArgs
	if e := decodeArgs(args, limitSmall, &a); e != nil {
		return nil, e
	}
	if a.Handle == "" {
		return nil, serr(400, "VALIDATION", "handle is required")
	}
	t, e := sessionTable(ctx)
	if e != nil {
		return nil, e
	}
	if cs := t.lookup(a.Handle, caller.Exec.Lease); cs != nil {
		t.retire(a.Handle)
		cs.s.Finish(context.Canceled)
		return struct{}{}, nil
	}
	t.mu.Lock()
	l, seen := t.tomb[a.Handle]
	t.mu.Unlock()
	if seen && l == caller.Exec.Lease {
		return struct{}{}, nil
	}
	return nil, serr(404, "NOT_FOUND", "stream not found")
}
