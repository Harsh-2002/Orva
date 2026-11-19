package sandbox

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// sdkChannelFD is the descriptor number the sandboxed process sees. Pipes occupy
// 0-2 and cmd.ExtraFiles[0] becomes 3; nsjail closes everything else unless told
// otherwise (--pass_fd), so the number is part of the contract with the adapters.
const sdkChannelFD = 3

// sdkWriteTimeout bounds one reply write. A sandbox that stops reading its end
// would otherwise wedge the writer (and every other reply) forever.
const sdkWriteTimeout = 5 * time.Second

// sdkOps is the CLOSED set of operations in protocol v1. Anything else is
// answered with 400 UNKNOWN_OP rather than reaching the handler.
var sdkOps = map[string]struct{}{
	"kv.get": {}, "kv.put": {}, "kv.delete": {}, "kv.list": {}, "kv.batch": {},
	"kv.incr": {}, "kv.cas": {}, "invoke": {}, "invoke.stream.open": {},
	"invoke.stream.next": {}, "invoke.stream.close": {}, "jobs.enqueue": {},
	"crons.upsert": {}, "spans.emit": {},
}

// sdkOpNeedsExec lists the operations that act on behalf of a live execution.
// The others (kv.*) are allowed without one, so a frame carrying a lease that
// has since been revoked (a timer, a background thread, work after the response)
// is served as module scope rather than refused.
var sdkOpNeedsExec = map[string]bool{
	"invoke": true, "invoke.stream.open": true, "invoke.stream.next": true,
	"invoke.stream.close": true, "jobs.enqueue": true, "crons.upsert": true,
	"spans.emit": true,
}

// maxSDKPendingReplies bounds replies that are computed but not yet written.
// The in-flight slot is freed before the reply is written, so without this a
// client that never reads could pipeline requests until the write deadline
// killed it, each handler holding its payload meanwhile. At the limit the read
// loop stops reading, which is the backpressure.
const maxSDKPendingReplies = 2 * MaxSDKInflight

// errSDKProtocol marks a framing/JSON violation: the connection is closed and
// the worker is not reused.
var errSDKProtocol = errors.New("sdk protocol violation")

// errSDKFrameTooLarge marks a reply the host refused to encode. It is a
// host-side bug, not a peer protocol violation: the peer never sees a frame.
var errSDKFrameTooLarge = errors.New("sdk frame payload exceeds the protocol limit")

// sdkFrameHeaderLen is the big-endian length prefix every frame starts with.
const sdkFrameHeaderLen = 4

// newSDKPair returns the host end of a per-worker AF_UNIX socketpair and the
// file the child inherits. Both are CLOEXEC on the host so a concurrent spawn
// cannot leak another worker's end into its own sandbox; os/exec clears CLOEXEC
// on ExtraFiles for the one child that is meant to have it.
func newSDKPair() (host net.Conn, child *os.File, err error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("sdk socketpair: %w", err)
	}
	hostFile := os.NewFile(uintptr(fds[0]), "sdk-host")
	child = os.NewFile(uintptr(fds[1]), "sdk-child")
	host, err = net.FileConn(hostFile) // dups the descriptor
	_ = hostFile.Close()
	if err != nil {
		_ = child.Close()
		return nil, nil, fmt.Errorf("sdk conn: %w", err)
	}
	return host, child, nil
}

// sdkFrameLen validates one declared wire length and is the only place a frame
// length becomes an allocation size. Zero is invalid and anything above
// MaxSDKFrame is refused here, before the value is converted or added to, so a
// hostile header can neither make the host allocate nor wrap a 32-bit int.
func sdkFrameLen(n uint32) (int, error) {
	if n == 0 || n > MaxSDKFrame {
		return 0, fmt.Errorf("%w: frame length %d out of range", errSDKProtocol, n)
	}
	return int(n), nil
}

// readSDKFrame reads one length-prefixed frame. The declared length goes through
// sdkFrameLen before any allocation.
func readSDKFrame(r io.Reader) ([]byte, error) {
	var hdr [sdkFrameHeaderLen]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n, err := sdkFrameLen(binary.BigEndian.Uint32(hdr[:]))
	if err != nil {
		return nil, err
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// encodeSDKFrame prefixes payload with its length. The payload is checked against
// MaxSDKFrame first, which both matches what the peer accepts and bounds the
// header arithmetic below; without it, 4+len(payload) could wrap on a 32-bit int
// and allocate a buffer shorter than the copy. An over-limit payload is never
// encoded: the peer would reject the frame, so it is reported instead.
func encodeSDKFrame(payload []byte) ([]byte, error) {
	if len(payload) > MaxSDKFrame {
		return nil, fmt.Errorf("%w: %d bytes", errSDKFrameTooLarge, len(payload))
	}
	out := make([]byte, sdkFrameHeaderLen+len(payload))
	binary.BigEndian.PutUint32(out, uint32(len(payload)))
	copy(out[sdkFrameHeaderLen:], payload)
	return out, nil
}

// sdkRequest is one decoded client frame.
type sdkRequest struct {
	ID    uint64          `json:"id"`
	Op    string          `json:"op"`
	Lease string          `json:"lease"`
	Args  json.RawMessage `json:"a"`
}

// decodeSDKRequest strictly decodes a frame. Every failure wraps errSDKProtocol.
func decodeSDKRequest(frame []byte) (sdkRequest, error) {
	var req sdkRequest
	trimmed := bytes.TrimLeft(frame, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return req, fmt.Errorf("%w: frame is not a JSON object", errSDKProtocol)
	}
	dec := json.NewDecoder(bytes.NewReader(frame))
	if err := dec.Decode(&req); err != nil {
		return sdkRequest{}, fmt.Errorf("%w: %v", errSDKProtocol, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return sdkRequest{}, fmt.Errorf("%w: trailing data after object", errSDKProtocol)
	}
	if req.ID < 1 {
		return sdkRequest{}, fmt.Errorf("%w: missing or invalid id", errSDKProtocol)
	}
	if a := bytes.TrimSpace(req.Args); len(a) == 0 || bytes.Equal(a, []byte("null")) {
		req.Args = json.RawMessage("{}")
	} else if a[0] != '{' {
		return sdkRequest{}, fmt.Errorf("%w: args is not an object", errSDKProtocol)
	}
	return req, nil
}

// dispatchState is the host's record of the dispatch running on a worker. It is
// published before the request frame is written and revoked on every exit path.
type dispatchState struct {
	info   ExecInfo
	ctx    context.Context
	cancel context.CancelFunc
}

func newLease() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("sandbox: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b[:])
}

// newDispatchState builds the state for one dispatch from the ctx the caller
// passed (which may carry an ExecInfo from WithExecution).
func newDispatchState(ctx context.Context) *dispatchState {
	info, _ := ExecutionFrom(ctx)
	if info.Lease == "" {
		info.Lease = newLease()
	}
	base := info.Ctx
	if base == nil {
		base = ctx
	}
	cctx, cancel := context.WithCancel(base)
	info.Ctx = cctx
	return &dispatchState{info: info, ctx: cctx, cancel: cancel}
}

// sdkServer serves one worker's channel. It is separate from Worker so the
// protocol is testable over a plain net.Conn.
type sdkServer struct {
	conn       net.Conn
	functionID string
	handler    SDKHandler
	state      func() *dispatchState
	worker     *Worker // optional; standalone protocol servers have no workload authority
	// violation is called after the conn is closed for a protocol violation;
	// the worker must not be reused. kill is called when a reply cannot be
	// written within writeTimeout.
	violation    func()
	kill         func()
	writeTimeout time.Duration

	session SDKSession

	wmu      sync.Mutex
	inflight atomic.Int32
	replySem chan struct{}
	closeMu  sync.Once
	connCtx  context.Context
	cancel   context.CancelFunc
}

func (s *sdkServer) closeConn() {
	s.closeMu.Do(func() {
		s.cancel()
		_ = s.conn.Close()
		s.session.close()
	})
}

// serve runs the read loop until the connection ends. Each op runs in its own
// goroutine because invoke blocks.
func (s *sdkServer) serve() {
	s.connCtx, s.cancel = context.WithCancel(context.Background())
	if s.writeTimeout == 0 {
		s.writeTimeout = sdkWriteTimeout
	}
	s.replySem = make(chan struct{}, maxSDKPendingReplies)
	sdkOpenChannels.Add(1)
	defer sdkOpenChannels.Add(-1)
	defer s.closeConn()

	for {
		frame, err := readSDKFrame(s.conn)
		if err == nil {
			var req sdkRequest
			if req, err = decodeSDKRequest(frame); err == nil {
				s.dispatch(req)
				continue
			}
		}
		if errors.Is(err, errSDKProtocol) {
			slog.Warn("sdk channel protocol violation", "function_id", s.functionID, "error", err)
			sdkProtocolViolations.Add(1)
			s.closeConn()
			if s.violation != nil {
				s.violation()
			}
		}
		return
	}
}

func (s *sdkServer) dispatch(req sdkRequest) {
	if _, ok := sdkOps[req.Op]; !ok {
		sdkRecord("unknown", 400, 0)
		s.reply(req.ID, nil, &SDKError{Status: 400, Code: "UNKNOWN_OP", Message: "unknown op"})
		return
	}
	caller := SDKCaller{FunctionID: s.functionID}
	var original *dispatchState
	ctx := s.connCtx
	// ctx is replaced below by the lease ctx; the session rides on whichever
	// one the op ends up with.
	if req.Lease != "" {
		st := s.state()
		switch {
		case st != nil && st.info.Lease == req.Lease:
			original = st
			info := st.info
			caller.Exec = &info
			ctx = st.ctx
		case sdkOpNeedsExec[req.Op]:
			sdkRecord(req.Op, 403, 0)
			s.reply(req.ID, nil, &SDKError{Status: 403, Code: "SDK_SCOPE_VIOLATION",
				Message: "lease is not valid for a running execution"})
			return
		}
		// Otherwise: a stale lease on an op that needs no execution is served
		// as module scope (Exec stays nil).
	}
	if s.worker != nil {
		caller.workload = &sdkWorkloadSource{worker: s.worker, original: original, lease: req.Lease, ctx: s.connCtx}
	}
	if s.inflight.Add(1) > MaxSDKInflight {
		s.inflight.Add(-1)
		sdkRecord(req.Op, 429, 0)
		s.reply(req.ID, nil, &SDKError{Status: 429, Code: "TOO_MANY_INFLIGHT",
			Message: "too many unanswered SDK requests"})
		return
	}
	select {
	case s.replySem <- struct{}{}:
	case <-s.connCtx.Done():
		s.inflight.Add(-1)
		return
	}
	ctx = context.WithValue(ctx, sessionCtxKey{}, &s.session)
	go func() {
		defer func() { <-s.replySem }()
		start := time.Now()
		res, serr := s.call(ctx, caller, req)
		status := 200
		if serr != nil {
			status = serr.Status
		}
		sdkRecord(req.Op, status, time.Since(start))
		// Free the slot BEFORE the reply is written: a client that stays within
		// the limit sends its next request the instant it reads this reply, and
		// would otherwise be counted as the 33rd and refused.
		s.inflight.Add(-1)
		s.reply(req.ID, res, serr)
	}()
}

func (s *sdkServer) call(ctx context.Context, caller SDKCaller, req sdkRequest) (res any, serr *SDKError) {
	defer func() {
		if p := recover(); p != nil {
			slog.Error("sdk handler panic", "function_id", s.functionID, "op", req.Op, "panic", p)
			res, serr = nil, &SDKError{Status: 500, Code: "INTERNAL", Message: "internal error"}
		}
	}()
	return s.handler(ctx, caller, req.Op, req.Args)
}

type sdkOKReply struct {
	ID uint64 `json:"id"`
	OK bool   `json:"ok"`
	R  any    `json:"r"`
}

type sdkErrReply struct {
	ID uint64    `json:"id"`
	OK bool      `json:"ok"`
	E  *SDKError `json:"e"`
}

func (s *sdkServer) reply(id uint64, res any, serr *SDKError) {
	var payload []byte
	var err error
	if serr == nil {
		if res == nil {
			res = struct{}{}
		}
		payload, err = json.Marshal(sdkOKReply{ID: id, OK: true, R: res})
		if err != nil {
			serr = &SDKError{Status: 500, Code: "INTERNAL", Message: "result is not encodable"}
		} else if len(payload) > MaxSDKFrame {
			serr = &SDKError{Status: 500, Code: "RESULT_TOO_LARGE", Message: "result exceeds the SDK frame limit"}
		}
	}
	if serr != nil {
		payload, _ = json.Marshal(sdkErrReply{ID: id, OK: false, E: serr})
	}
	s.write(payload)
}

func (s *sdkServer) write(payload []byte) {
	buf, err := encodeSDKFrame(payload)
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if err == nil {
		_ = s.conn.SetWriteDeadline(time.Now().Add(s.writeTimeout))
		_, err = s.conn.Write(buf)
	}
	if err != nil {
		// The sandbox stopped reading (or the conn is gone, or the payload could
		// not be encoded at all). A partially written frame would desync the
		// stream, so the only safe move is to drop the connection and the worker.
		select {
		case <-s.connCtx.Done():
		default:
			slog.Warn("sdk channel write failed; killing worker", "function_id", s.functionID, "error", err)
			sdkWriteFailures.Add(1)
			s.closeConn()
			if s.kill != nil {
				s.kill()
			}
		}
	}
}
