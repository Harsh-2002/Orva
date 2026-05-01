package sandbox

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// ErrWorkerExited signals that the worker process died (stdout EOF, fatal
// frame, or stdin write failure). The pool kills the worker; the proxy
// surfaces this as a 502 WORKER_CRASHED — distinct from a ctx timeout or
// a benign per-request handler exception that stays inside the worker.
var ErrWorkerExited = errors.New("worker exited unexpectedly")

// Worker is a long-lived nsjail+adapter process held by the pool. It
// serializes one request at a time — callers hold exclusive access for the
// duration of a Dispatch. Stdout is the length-prefixed protocol stream;
// stderr is drained in the background into a ring buffer.
type Worker struct {
	Cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	stderr  io.ReadCloser
	Spawned time.Time
	Served  atomic.Int64

	// CgroupPath is resolved asynchronously after Spawn (nsjail names the
	// cgroup after its jailed child's PID, which isn't known until after fork).
	// AcquireUsec and AcquireAt are stamped by the pool before handing the
	// worker to a caller and read at release for per-function EWMA metrics.
	cgroupPathMu sync.Mutex
	CgroupPath   string
	AcquireUsec  int64
	AcquireAt    time.Time

	// mu serializes Dispatch calls defensively. The pool contract is that
	// only one goroutine holds a Worker at a time, so the lock is just a
	// safety net — but cheap enough to keep in.
	mu sync.Mutex

	dead atomic.Bool

	// waitDone is closed exactly once when the single background
	// cmd.Wait() goroutine (started in Spawn) has reaped the underlying
	// process. Quit() / Kill() use this to synchronize their grace timers
	// without ever calling cmd.Wait() themselves — that prevents the
	// "Wait was already called" race we used to hit when a Quit grace
	// expired and the escalation path called Kill() concurrently.
	//
	// Reaping in Spawn guarantees that no matter how the worker dies
	// (Quit, Kill, markDead-then-pool-kill, child crashes on its own),
	// nsjail does NOT linger as a zombie waiting for orvad to wait() on it.
	// See v0.4 zombie-nsjail bug: streaming-disconnect closed stdin via
	// markDead but the kill that followed sometimes spawned a duplicate
	// Wait() goroutine which errored out without reaping.
	waitDone chan struct{}

	errBuf *ringBuffer
}

// Frame types on the wire. See runtimes/*/adapter.{js,py} for the other end.
type frameRequest struct {
	Type  string          `json:"type"`
	Event json.RawMessage `json:"event"`
}

type frameResponse struct {
	Type       string            `json:"type"`
	StatusCode int               `json:"statusCode,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	Fatal      bool              `json:"fatal,omitempty"`
	Message    string            `json:"message,omitempty"`
	// Streaming chunk frames carry base64-encoded bytes in Data so the
	// transport stays JSON-safe (raw bytes can include 0x00 / NUL which
	// upset some intermediaries). v0.4 C1 — see proxy.Forward + adapter.py
	// / adapter.js for the producer/consumer.
	Data string `json:"data,omitempty"`
}

// Spawn boots a fresh nsjail+adapter process. Caller owns the returned
// Worker and is responsible for eventually calling Quit or Kill.
func Spawn(ctx context.Context, cfg ExecConfig) (*Worker, error) {
	rootfs, entrypoint, err := resolveRuntime(cfg)
	if err != nil {
		return nil, err
	}
	args := buildArgs(cfg, rootfs, entrypoint)

	cmd := exec.Command(cfg.NsjailBin, args...)

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		stdinPipe.Close()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		stdinPipe.Close()
		stdoutPipe.Close()
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	// Put the worker in its own process group so Kill reliably tears down
	// nsjail + everything inside the sandbox.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		stdinPipe.Close()
		stdoutPipe.Close()
		stderrPipe.Close()
		return nil, fmt.Errorf("start nsjail: %w", err)
	}

	w := &Worker{
		Cmd:      cmd,
		stdin:    stdinPipe,
		stdout:   stdoutPipe,
		stderr:   stderrPipe,
		Spawned:  time.Now(),
		errBuf:   newRingBuffer(64 * 1024),
		waitDone: make(chan struct{}),
	}

	// Single, authoritative Wait() for this worker's lifetime. Whichever
	// path tears the process down (clean Quit, SIGKILL via Kill, child
	// crash, ctx cancel that closes stdin), this goroutine is the one
	// that reaps the kernel-side PID. Nothing else may call cmd.Wait().
	// On return we close waitDone so any blocked Quit/Kill grace-timer
	// can wake up.
	go func() {
		_ = cmd.Wait()
		close(w.waitDone)
	}()

	// Resolve the nsjail cgroup path asynchronously. nsjail names the cgroup
	// after its jailed child's PID (not its own), so we scan for it in a
	// background goroutine to avoid blocking Spawn. The pool reads CgroupPath
	// at acquire time; by then (first real request) the goroutine has long
	// finished. CgroupPath="" is safe — resource sampling is silently skipped.
	if mount := CgroupV2Mount(); mount != "" && cmd.Process != nil {
		nsjailPid := cmd.Process.Pid
		go func() {
			p := FindJailedCgroupPath(mount, nsjailPid)
			if p != "" {
				w.cgroupPathMu.Lock()
				w.CgroupPath = p
				w.cgroupPathMu.Unlock()
			}
		}()
	}

	// Background goroutine drains stderr into the ring buffer so the pipe
	// never blocks the worker and we can snapshot per-request stderr.
	go func() {
		io.Copy(w.errBuf, stderrPipe)
	}()

	return w, nil
}

// Dispatch writes a request frame and reads the response frame. Returns
// (respBody, stderrSnapshot, err). On context cancellation or protocol
// error the worker is marked dead and must not be reused.
//
// Backwards-compatible non-streaming wrapper around DispatchEx. For
// streaming responses (v0.4 C1) callers should prefer DispatchEx so they
// can drive chunk frames out to the HTTP client incrementally.
func (w *Worker) Dispatch(ctx context.Context, eventJSON []byte) ([]byte, []byte, error) {
	res, err := w.DispatchEx(ctx, eventJSON)
	if err != nil {
		var stderr []byte
		if res != nil {
			stderr = res.Stderr()
		}
		return nil, stderr, err
	}
	if res.Streaming {
		// Caller used the back-compat path on a streaming handler. Drain
		// chunks into a buffer so the existing single-write path still
		// works; this is the path tests + non-HTTP callers take.
		var buf []byte
		for {
			kind, data, err := res.NextFrame(ctx)
			if err != nil {
				return nil, res.Stderr(), err
			}
			if kind == "end" {
				break
			}
			if kind == "chunk" {
				buf = append(buf, data...)
			}
		}
		envelope, _ := json.Marshal(map[string]any{
			"statusCode": res.StatusCode,
			"headers":    res.Headers,
			"body":       string(buf),
		})
		return envelope, res.Stderr(), nil
	}
	envelope, _ := json.Marshal(map[string]any{
		"statusCode": res.StatusCode,
		"headers":    res.Headers,
		"body":       res.Body,
	})
	return envelope, res.Stderr(), nil
}

// DispatchResult is returned from DispatchEx. Two shapes:
//
//   - Streaming=false: StatusCode/Headers/Body fully populated; the call
//     completed with the historical single-frame "response" path. Callers
//     just write the envelope and move on.
//   - Streaming=true:  StatusCode/Headers populated from "response_start";
//     Body is empty. Caller MUST loop on NextFrame until kind == "end".
//     During the loop the Worker holds w.mu — Release should run only
//     after the loop terminates.
//
// Stderr() is safe to call at any point; it returns the bytes written to
// the worker's stderr ring buffer since dispatch began. Most callers wait
// until after the loop completes so they capture the full snapshot.
type DispatchResult struct {
	Streaming  bool
	StatusCode int
	Headers    map[string]string
	Body       string

	w         *Worker
	errBefore int64
	finished  bool
	// readErr is sticky — once the underlying pipe surfaces an error, all
	// subsequent NextFrame calls return it.
	readErr error
	// finishHook is called exactly once when the streaming loop reaches a
	// terminal state (end / error / ctx-cancel). Used by DispatchEx to
	// release the worker mutex it held across the stream.
	finishHook func()
}

// finalize runs the finishHook (if set) exactly once. Idempotent.
func (r *DispatchResult) finalize() {
	if r.finishHook != nil {
		hook := r.finishHook
		r.finishHook = nil
		hook()
	}
}

// NextFrame reads the next frame from a streaming response. Returns:
//
//	kind="chunk", data=<bytes>   — one chunk of body bytes
//	kind="end",   data=nil       — terminator; loop should exit
//	kind="",      err=...        — protocol error; worker is dead
//
// An empty chunk (data length 0) is a heartbeat from the adapter and is
// surfaced as kind="chunk", data=nil. Callers can choose to forward an
// empty Write+Flush (keeps proxies/LBs alive) or skip it.
func (r *DispatchResult) NextFrame(ctx context.Context) (kind string, data []byte, err error) {
	if r.finished {
		r.finalize()
		return "end", nil, nil
	}
	if r.readErr != nil {
		r.finalize()
		return "", nil, r.readErr
	}

	// Read the next frame from worker stdout, with ctx cancellation.
	type framePayload struct {
		payload []byte
		err     error
	}
	ch := make(chan framePayload, 1)
	go func() {
		p, err := readFrame(r.w.stdout)
		ch <- framePayload{p, err}
	}()
	select {
	case fp := <-ch:
		if fp.err != nil {
			r.w.markDead()
			r.readErr = fmt.Errorf("%w: read chunk frame: %v", ErrWorkerExited, fp.err)
			r.finalize()
			return "", nil, r.readErr
		}
		var f frameResponse
		if err := json.Unmarshal(fp.payload, &f); err != nil {
			r.w.markDead()
			r.readErr = fmt.Errorf("%w: invalid chunk frame: %v", ErrWorkerExited, err)
			r.finalize()
			return "", nil, r.readErr
		}
		switch f.Type {
		case "chunk":
			// Body bytes are base64 to keep the JSON transport binary-safe.
			if f.Data == "" {
				return "chunk", nil, nil
			}
			dec, err := base64.StdEncoding.DecodeString(f.Data)
			if err != nil {
				r.w.markDead()
				r.readErr = fmt.Errorf("%w: invalid chunk data: %v", ErrWorkerExited, err)
				r.finalize()
				return "", nil, r.readErr
			}
			return "chunk", dec, nil
		case "response_end":
			r.finished = true
			r.w.Served.Add(1)
			r.finalize()
			return "end", nil, nil
		case "error":
			// Adapter surfaced an error mid-stream. Always treat as fatal
			// because the response head was already written — there is no
			// clean way to convert this into a 5xx envelope at this point.
			r.w.markDead()
			r.readErr = fmt.Errorf("%w: adapter mid-stream error: %s", ErrWorkerExited, f.Message)
			r.finalize()
			return "", nil, r.readErr
		default:
			r.w.markDead()
			r.readErr = fmt.Errorf("%w: unexpected stream frame type %q", ErrWorkerExited, f.Type)
			r.finalize()
			return "", nil, r.readErr
		}
	case <-ctx.Done():
		r.w.markDead()
		r.readErr = ctx.Err()
		r.finalize()
		return "", nil, r.readErr
	}
}

// Stderr returns the bytes written to the worker's stderr since dispatch
// began. Callers typically defer this until after the streaming loop has
// drained so the snapshot covers the full handler invocation.
func (r *DispatchResult) Stderr() []byte {
	if r == nil || r.w == nil {
		return nil
	}
	return r.w.errBuf.Snapshot(r.errBefore)
}

// DispatchEx writes a request frame, reads the FIRST response frame, and
// dispatches by frame type:
//
//   - "response"        → DispatchResult{Streaming:false} populated end-to-end
//   - "response_start"  → DispatchResult{Streaming:true}; caller drives
//     NextFrame until kind=="end"
//
// On protocol errors the worker is marked dead. Worker.mu is held until
// the streaming caller finishes the loop (DispatchEx releases it inside
// NextFrame's terminal cases via finished=true; non-streaming returns
// release the lock immediately). This matches the contract that one
// Worker handles one request at a time.
//
// NOTE: holding w.mu across the streaming loop is fine because the pool
// guarantees a single goroutine owns each Worker, and Release happens
// after the proxy's Forward unwinds.
func (w *Worker) DispatchEx(ctx context.Context, eventJSON []byte) (*DispatchResult, error) {
	if w.dead.Load() {
		return nil, errors.New("worker dead")
	}
	w.mu.Lock()
	// We can't unconditionally defer Unlock here because streaming holds
	// the lock until NextFrame finishes. Both terminal paths (non-stream
	// + stream-end + error) take responsibility for unlocking.
	unlocked := false
	unlock := func() {
		if !unlocked {
			unlocked = true
			w.mu.Unlock()
		}
	}

	if w.dead.Load() {
		unlock()
		return nil, errors.New("worker dead")
	}

	errBefore := w.errBuf.Len()

	// Compose the request frame: {"type":"request","event":<raw>}.
	reqFrame, _ := json.Marshal(frameRequest{Type: "request", Event: json.RawMessage(eventJSON)})

	// Wire write must complete or we bail.
	writeErrCh := make(chan error, 1)
	go func() {
		writeErrCh <- writeFrame(w.stdin, reqFrame)
	}()

	// Read FIRST response frame on another goroutine so ctx cancel can
	// break us out.
	respCh := make(chan []byte, 1)
	readErrCh := make(chan error, 1)
	go func() {
		payload, err := readFrame(w.stdout)
		if err != nil {
			readErrCh <- err
			return
		}
		respCh <- payload
	}()

	select {
	case err := <-writeErrCh:
		if err != nil {
			w.markDead()
			unlock()
			return &DispatchResult{w: w, errBefore: errBefore},
				fmt.Errorf("%w: write frame: %v", ErrWorkerExited, err)
		}
	case <-ctx.Done():
		w.markDead()
		unlock()
		return &DispatchResult{w: w, errBefore: errBefore}, ctx.Err()
	}

	select {
	case payload := <-respCh:
		var resp frameResponse
		if err := json.Unmarshal(payload, &resp); err != nil {
			w.markDead()
			unlock()
			return &DispatchResult{w: w, errBefore: errBefore},
				fmt.Errorf("%w: invalid response frame: %v", ErrWorkerExited, err)
		}
		if resp.Type == "error" {
			if resp.Fatal {
				w.markDead()
				unlock()
				return &DispatchResult{w: w, errBefore: errBefore},
					fmt.Errorf("%w: adapter fatal: %s", ErrWorkerExited, resp.Message)
			}
			// Non-fatal adapter error: handler threw but VM is healthy. Do
			// NOT mark dead; do NOT classify as ErrWorkerExited.
			unlock()
			return &DispatchResult{w: w, errBefore: errBefore},
				fmt.Errorf("adapter: %s", resp.Message)
		}
		if resp.Type == "response" {
			// Non-streaming back-compat path: full response in one frame.
			w.Served.Add(1)
			unlock()
			return &DispatchResult{
				Streaming:  false,
				StatusCode: resp.StatusCode,
				Headers:    resp.Headers,
				Body:       resp.Body,
				w:          w,
				errBefore:  errBefore,
				finished:   true,
			}, nil
		}
		if resp.Type == "response_start" {
			// Streaming: keep the lock — the caller drives NextFrame and
			// unlocks via the wrapper below when finished.
			r := &DispatchResult{
				Streaming:  true,
				StatusCode: resp.StatusCode,
				Headers:    resp.Headers,
				w:          w,
				errBefore:  errBefore,
			}
			// Wrap NextFrame so the worker unlocks on terminal kinds. We
			// achieve this by replacing the helper closure ... but Go
			// doesn't let us mutate a method, so instead we install the
			// unlock as a finalizer triggered on every NextFrame return
			// path that flips finished=true OR sets readErr.
			//
			// Simpler: use a tiny goroutine-free finishHook that the
			// NextFrame body checks. We attach via a new field.
			r.finishHook = unlock
			return r, nil
		}
		w.markDead()
		unlock()
		return &DispatchResult{w: w, errBefore: errBefore},
			fmt.Errorf("%w: unexpected frame type %q", ErrWorkerExited, resp.Type)
	case err := <-readErrCh:
		w.markDead()
		unlock()
		return &DispatchResult{w: w, errBefore: errBefore},
			fmt.Errorf("%w: read frame: %v", ErrWorkerExited, err)
	case <-ctx.Done():
		w.markDead()
		unlock()
		return &DispatchResult{w: w, errBefore: errBefore}, ctx.Err()
	}
}

// Quit sends a clean shutdown frame, waits grace, then SIGTERMs, then SIGKILLs.
//
// All synchronization with the underlying process happens via w.waitDone,
// which the single Wait() goroutine launched in Spawn closes once the PID
// has been reaped. Quit MUST NOT call cmd.Wait() itself — doing so races
// with the Spawn-side Wait and yields "Wait was already called" errors,
// which would silently leave nsjail unreaped.
func (w *Worker) Quit(grace time.Duration) error {
	if w.dead.Load() {
		return w.Kill()
	}
	quitFrame, _ := json.Marshal(map[string]string{"type": "quit"})
	_ = writeFrame(w.stdin, quitFrame)
	_ = w.stdin.Close()

	select {
	case <-w.waitDone:
		w.markDead()
		return nil
	case <-time.After(grace):
	}

	// Grace expired — escalate.
	if w.Cmd.Process != nil {
		_ = w.Cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-w.waitDone:
	case <-time.After(grace):
		_ = w.Kill()
		// Wait briefly for the Spawn-side reaper to finish so the caller
		// returning from Quit can rely on the process being gone. Bounded
		// so a wedged kernel can't hang shutdown forever.
		select {
		case <-w.waitDone:
		case <-time.After(grace):
		}
	}
	w.markDead()
	return nil
}

// Kill is an immediate SIGKILL of the worker's process group.
//
// Reaping is owned by the single Wait() goroutine launched in Spawn — we
// must NOT call cmd.Wait() here. Calling Wait() twice on the same
// *exec.Cmd is a documented race that can leave nsjail unreaped (the
// loser sees "Wait was already called" and returns without reaping).
// Pre-fix this was the path that produced <defunct> nsjail entries after
// streaming-client disconnects: markDead → pool kill → duplicate Wait.
func (w *Worker) Kill() error {
	w.markDead()
	if w.Cmd == nil || w.Cmd.Process == nil {
		return nil
	}
	// Signal the whole process group (Setpgid=true at Spawn).
	_ = syscall.Kill(-w.Cmd.Process.Pid, syscall.SIGKILL)
	_ = w.Cmd.Process.Kill()
	// The Spawn-side reaper goroutine will pick up the SIGKILL exit and
	// close waitDone. Caller does not block on it — w.Kill returns as soon
	// as the signal is delivered.
	return nil
}

// IsDead reports whether the worker has been marked unusable.
func (w *Worker) IsDead() bool { return w.dead.Load() }

// GetCgroupPath returns the resolved cgroup path (thread-safe).
func (w *Worker) GetCgroupPath() string {
	w.cgroupPathMu.Lock()
	defer w.cgroupPathMu.Unlock()
	return w.CgroupPath
}

// IsExpired reports whether the worker should be retired based on age or
// use count. Either zero disables that check.
func (w *Worker) IsExpired(ttl time.Duration, maxUses int64) bool {
	if ttl > 0 && time.Since(w.Spawned) > ttl {
		return true
	}
	if maxUses > 0 && w.Served.Load() >= maxUses {
		return true
	}
	return false
}

func (w *Worker) markDead() {
	w.dead.Store(true)
	// Close stdin so a blocked read on the adapter side unblocks and exits.
	_ = w.stdin.Close()
}

// ── Framing helpers ────────────────────────────────────────────────────

func writeFrame(w io.Writer, payload []byte) error {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if _, err := w.Write(payload); err != nil {
		return err
	}
	return nil
}

func readFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 {
		return []byte("{}"), nil
	}
	if n > 64*1024*1024 { // sanity cap — 64 MB per frame
		return nil, fmt.Errorf("frame too large: %d bytes", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// ── Stderr ring buffer ────────────────────────────────────────────────

// ringBuffer is a fixed-size, write-only ring used to capture the last N
// bytes of stderr per worker. Reads are via Snapshot(from).
type ringBuffer struct {
	mu      sync.Mutex
	buf     []byte
	size    int
	written int64 // total bytes ever written
}

func newRingBuffer(size int) *ringBuffer {
	return &ringBuffer{buf: make([]byte, size), size: size}
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range p {
		r.buf[int(r.written)%r.size] = b
		r.written++
	}
	return len(p), nil
}

// Len returns the cumulative byte count (monotonically increasing).
func (r *ringBuffer) Len() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.written
}

// Snapshot returns the bytes written since the 'from' marker. If the
// buffer wrapped past that point, the oldest overwritten bytes are lost.
func (r *ringBuffer) Snapshot(from int64) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.written <= from {
		return nil
	}
	want := int(r.written - from)
	if want > r.size {
		want = r.size
	}
	start := int(r.written-int64(want)) % r.size
	out := make([]byte, want)
	if start+want <= r.size {
		copy(out, r.buf[start:start+want])
	} else {
		n := copy(out, r.buf[start:])
		copy(out[n:], r.buf[:want-n])
	}
	return out
}
