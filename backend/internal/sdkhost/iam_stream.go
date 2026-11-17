package sdkhost

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync/atomic"

	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

type iamStream struct {
	call       *workloadiam.Admission
	worker     *sandbox.Worker
	targetID   string
	stopCancel func() bool
	nextBusy   atomic.Bool
}

// Storage is reserved before admission; only the bounded copy is under the gate.
func (h *Host) handoffIAMStream(ctx context.Context, caller sandbox.SDKCaller, cs *chStream, frame []byte) ([]byte, error) {
	if caller.Exec == nil || cs.iam == nil || len(frame) > workloadiam.MaxHandoffBytes {
		return nil, workloadiam.ErrDenied
	}
	exec := *caller.Exec
	reply := make([]byte, len(frame))
	err := cs.iam.call.HandoffFrameObserved(func() (workloadiam.Context, workloadiam.TargetState) {
		return h.iamInvokeContext(ctx, caller, exec), h.iamInvokeTarget(cs.iam.targetID, cs.iam.worker)
	}, frame, func(b []byte) error {
		copy(reply, b)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reply, nil
}

func (h *Host) handoffIAMStreamError(ctx context.Context, caller sandbox.SDKCaller, cs *chStream, sdkErr *sandbox.SDKError) *sandbox.SDKError {
	wire, err := json.Marshal(sdkErr)
	if err != nil || len(wire) > workloadiam.MaxHandoffBytes {
		return iamDenied()
	}
	reply, err := h.handoffIAMStream(ctx, caller, cs, wire)
	if err != nil {
		return iamDenied()
	}
	var admitted sandbox.SDKError
	if err := json.Unmarshal(reply, &admitted); err != nil {
		return iamDenied()
	}
	return &admitted
}

func (h *Host) streamOpenIAM(ctx context.Context, caller sandbox.SDKCaller, args json.RawMessage) (any, *sandbox.SDKError) {
	if caller.Exec != nil {
		exec := *caller.Exec
		caller.Exec = &exec
	}
	p, call, e := h.beginIAMCall(ctx, caller, args, workloadiam.OpStreamOpen)
	if e != nil {
		return nil, e
	}
	stopOpen := context.AfterFunc(ctx, call.Finish)
	defer stopOpen()
	owned := false
	defer func() {
		if !owned {
			call.Finish()
		}
	}()
	t, e := sessionTable(ctx)
	if e != nil {
		return nil, e
	}
	t.mu.Lock()
	if t.closed || len(t.m)+t.opening >= maxStreamsPerChan {
		closed := t.closed
		t.mu.Unlock()
		if closed {
			return nil, iamDenied()
		}
		return nil, serr(429, "TOO_MANY_STREAMS", "too many open streams on this channel")
	}
	t.opening++
	if t.waiting == nil {
		t.waiting = make(map[*workloadiam.Admission]string)
	}
	t.waiting[call] = caller.Exec.Lease
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.opening--
		delete(t.waiting, call)
		t.mu.Unlock()
	}()

	state := &iamStream{call: call, targetID: p.fn.ID}
	admitted := false
	s, ie := p.inv.OpenStreamWithAdmission(call.Context(), p.caller, p.fn, p.body, "", func(worker *sandbox.Worker) *handlers.InvokeError {
		err := h.admitIAMInvoke(ctx, caller, *caller.Exec, call, state.targetID, worker)
		if err == nil {
			state.worker, admitted = worker, true
		}
		return err
	})
	if ie != nil {
		if !admitted {
			return nil, iamDenied()
		}
		return nil, h.handoffIAMStreamError(ctx, caller, &chStream{iam: state}, fromInvokeErr(ie))
	}
	owned = true
	return h.publishIAMStream(ctx, caller, t, s, state)
}

// Publish only after acquired-worker admission and the response-head wait.
func (h *Host) publishIAMStream(ctx context.Context, caller sandbox.SDKCaller, t *streamTable, s *handlers.F2FStream, state *iamStream) (any, *sandbox.SDKError) {
	call := state.call
	cs := &chStream{s: s, lease: caller.Exec.Lease, iam: state}
	if !s.Streaming {
		cs.pending, cs.ended = []byte(s.Body), true
	}
	handle := newHandle()
	headers := s.Headers
	if headers == nil {
		headers = map[string]string{}
	}
	head, err := json.Marshal(map[string]any{"handle": handle, "status": s.StatusCode, "headers": headers})
	if err != nil || len(head) > workloadiam.MaxHandoffBytes {
		call.Finish()
		s.Finish(context.Canceled)
		return nil, iamDenied()
	}
	state.stopCancel = context.AfterFunc(call.Context(), func() {
		t.retire(handle)
		call.Finish()
		s.Finish(context.Canceled)
	})
	t.mu.Lock()
	if t.closed || call.Context().Err() != nil {
		t.mu.Unlock()
		cs.finish(context.Canceled)
		return nil, iamDenied()
	}
	t.m[handle] = cs
	t.mu.Unlock()
	reply, err := h.handoffIAMStream(ctx, caller, cs, head)
	if err != nil {
		t.retire(handle)
		cs.finish(context.Canceled)
		return nil, iamDenied()
	}
	return json.RawMessage(reply), nil
}

func (h *Host) streamNextIAM(ctx context.Context, caller sandbox.SDKCaller, args json.RawMessage) (any, *sandbox.SDKError) {
	if h.iam == nil || caller.Exec == nil {
		return nil, iamDenied()
	}
	exec := *caller.Exec
	caller.Exec = &exec
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
	cs := t.lookup(a.Handle, exec.Lease)
	if cs == nil || cs.iam == nil {
		return nil, iamDenied()
	}
	// Do not carry session/stream locks into IAM, frame reads or cancellation.
	if !cs.iam.nextBusy.CompareAndSwap(false, true) {
		return nil, iamDenied()
	}
	defer cs.iam.nextBusy.Store(false)
	stop := context.AfterFunc(ctx, cs.iam.call.Finish)
	defer stop()
	failClosed := func() (any, *sandbox.SDKError) {
		t.retire(a.Handle)
		cs.finish(context.Canceled)
		return nil, iamDenied()
	}
	// Deny stale callers before waiting for a frame, then recheck at handoff.
	if _, err := h.handoffIAMStream(ctx, caller, cs, nil); err != nil {
		return failClosed()
	}
	var frameErr error
	for len(cs.pending) == 0 && !cs.ended {
		kind, data, err := cs.s.NextFrame()
		if err != nil {
			frameErr = err
			break
		}
		if kind == "end" {
			cs.ended = true
		} else if len(data) > 0 {
			cs.pending = data
		}
	}
	if frameErr != nil {
		msg := frameErr.Error()
		if errors.Is(frameErr, context.DeadlineExceeded) {
			msg = "function timed out"
		}
		sdkErr := h.handoffIAMStreamError(ctx, caller, cs, serr(502, "INVOKE_FAILED", msg))
		t.retire(a.Handle)
		cs.finish(nil)
		return nil, sdkErr
	}
	n := min(len(cs.pending), maxStreamChunk)
	reply, err := h.handoffIAMStream(ctx, caller, cs, cs.pending[:n])
	if err != nil {
		return failClosed()
	}
	if n > 0 {
		cs.pending = cs.pending[n:]
		return map[string]any{"chunk_b64": base64.StdEncoding.EncodeToString(reply), "done": false}, nil
	}
	t.retire(a.Handle)
	cs.finish(nil)
	return map[string]any{"chunk_b64": "", "done": true}, nil
}
