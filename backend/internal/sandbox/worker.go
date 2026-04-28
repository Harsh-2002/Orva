package sandbox

import (
	"context"
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

	// mu serializes Dispatch calls defensively. The pool contract is that
	// only one goroutine holds a Worker at a time, so the lock is just a
	// safety net — but cheap enough to keep in.
	mu sync.Mutex

	dead atomic.Bool

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
		Cmd:     cmd,
		stdin:   stdinPipe,
		stdout:  stdoutPipe,
		stderr:  stderrPipe,
		Spawned: time.Now(),
		errBuf:  newRingBuffer(64 * 1024),
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
func (w *Worker) Dispatch(ctx context.Context, eventJSON []byte) ([]byte, []byte, error) {
	if w.dead.Load() {
		return nil, nil, errors.New("worker dead")
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.dead.Load() {
		return nil, nil, errors.New("worker dead")
	}

	errBefore := w.errBuf.Len()

	// Compose the request frame: {"type":"request","event":<raw>}.
	reqFrame, _ := json.Marshal(frameRequest{Type: "request", Event: json.RawMessage(eventJSON)})

	// Wire write must complete or we bail.
	writeErrCh := make(chan error, 1)
	go func() {
		writeErrCh <- writeFrame(w.stdin, reqFrame)
	}()

	// Read response on another goroutine so ctx cancel can break us out.
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
			// stdin write failure means the adapter side closed the pipe —
			// classify as worker-exit, not a generic transport error.
			return nil, w.errBuf.Snapshot(errBefore),
				fmt.Errorf("%w: write frame: %v", ErrWorkerExited, err)
		}
	case <-ctx.Done():
		w.markDead()
		return nil, w.errBuf.Snapshot(errBefore), ctx.Err()
	}

	select {
	case payload := <-respCh:
		var resp frameResponse
		if err := json.Unmarshal(payload, &resp); err != nil {
			w.markDead()
			return nil, w.errBuf.Snapshot(errBefore),
				fmt.Errorf("%w: invalid response frame: %v", ErrWorkerExited, err)
		}
		if resp.Type == "error" {
			if resp.Fatal {
				w.markDead()
				return nil, w.errBuf.Snapshot(errBefore),
					fmt.Errorf("%w: adapter fatal: %s", ErrWorkerExited, resp.Message)
			}
			// Non-fatal adapter error: handler threw but VM is healthy. Do
			// NOT mark dead; do NOT classify as ErrWorkerExited.
			return nil, w.errBuf.Snapshot(errBefore), fmt.Errorf("adapter: %s", resp.Message)
		}
		if resp.Type != "response" {
			w.markDead()
			return nil, w.errBuf.Snapshot(errBefore),
				fmt.Errorf("%w: unexpected frame type %q", ErrWorkerExited, resp.Type)
		}
		// Repack as Orva envelope for proxy.go which expects the historical
		// shape {"statusCode","headers","body"} in a JSON blob.
		envelope, _ := json.Marshal(map[string]any{
			"statusCode": resp.StatusCode,
			"headers":    resp.Headers,
			"body":       resp.Body,
		})
		w.Served.Add(1)
		return envelope, w.errBuf.Snapshot(errBefore), nil
	case err := <-readErrCh:
		w.markDead()
		// stdout read failure / EOF is the canonical worker-died signal.
		return nil, w.errBuf.Snapshot(errBefore),
			fmt.Errorf("%w: read frame: %v", ErrWorkerExited, err)
	case <-ctx.Done():
		w.markDead()
		return nil, w.errBuf.Snapshot(errBefore), ctx.Err()
	}
}

// Quit sends a clean shutdown frame, waits grace, then SIGTERMs, then SIGKILLs.
func (w *Worker) Quit(grace time.Duration) error {
	if w.dead.Load() {
		return w.Kill()
	}
	quitFrame, _ := json.Marshal(map[string]string{"type": "quit"})
	_ = writeFrame(w.stdin, quitFrame)
	_ = w.stdin.Close()

	done := make(chan error, 1)
	go func() { done <- w.Cmd.Wait() }()

	select {
	case <-done:
		w.markDead()
		return nil
	case <-time.After(grace):
	}

	// Grace expired — escalate.
	if w.Cmd.Process != nil {
		_ = w.Cmd.Process.Signal(syscall.SIGTERM)
	}
	select {
	case <-done:
	case <-time.After(grace):
		_ = w.Kill()
	}
	w.markDead()
	return nil
}

// Kill is an immediate SIGKILL of the worker's process group.
func (w *Worker) Kill() error {
	w.markDead()
	if w.Cmd == nil || w.Cmd.Process == nil {
		return nil
	}
	// Signal the whole process group (Setpgid=true at Spawn).
	_ = syscall.Kill(-w.Cmd.Process.Pid, syscall.SIGKILL)
	_ = w.Cmd.Process.Kill()
	go w.Cmd.Wait() // reap without blocking caller
	return nil
}

// IsDead reports whether the worker has been marked unusable.
func (w *Worker) IsDead() bool { return w.dead.Load() }

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
