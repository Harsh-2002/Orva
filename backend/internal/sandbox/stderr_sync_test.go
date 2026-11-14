package sandbox

import (
	"testing"
	"time"
)

func syncedResult(sync, complete bool, before string) (*DispatchResult, *ringBuffer) {
	w := &Worker{errBuf: newRingBuffer(1024)}
	w.stderrSync.Store(sync)
	_, _ = w.errBuf.Write([]byte(before))
	return &DispatchResult{w: w, errBefore: w.errBuf.Len(), complete: complete}, w.errBuf
}

func TestStderrWaitsForBytesWrittenBeforeTheReply(t *testing.T) {
	res, buf := syncedResult(true, true, "previous request\n")
	_, _ = buf.Write([]byte("first\n"))
	go func() {
		time.Sleep(20 * time.Millisecond)
		_, _ = buf.Write([]byte("last"))
		_, _ = buf.Write(stderrSyncMarker)
	}()
	if got := string(res.Stderr()); got != "first\nlast" {
		t.Fatalf("stderr = %q, want the whole request output without the marker", got)
	}
}

func TestStderrStripsMarkerExactly(t *testing.T) {
	for _, tc := range []struct{ name, written, want string }{
		{"line terminated", "x\n", "x\n"},
		{"partial line", "partial", "partial"},
		{"nothing written", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, buf := syncedResult(true, true, "")
			_, _ = buf.Write([]byte(tc.written))
			_, _ = buf.Write(stderrSyncMarker)
			got := res.Stderr()
			if string(got) != tc.want || (tc.want == "" && got != nil) {
				t.Fatalf("stderr = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStderrNeverWaitsWithoutTheCapability(t *testing.T) {
	for name, res := range map[string]*DispatchResult{
		"older adapter":    func() *DispatchResult { r, _ := syncedResult(false, true, ""); return r }(),
		"aborted dispatch": func() *DispatchResult { r, _ := syncedResult(true, false, ""); return r }(),
	} {
		start := time.Now()
		_ = res.Stderr()
		if elapsed := time.Since(start); elapsed > stderrSyncWait/2 {
			t.Fatalf("%s waited %s for a marker that cannot arrive", name, elapsed)
		}
	}
}

func TestStderrWaitIsBounded(t *testing.T) {
	res, buf := syncedResult(true, true, "")
	_, _ = buf.Write([]byte("no marker"))
	start := time.Now()
	if got := string(res.Stderr()); got != "no marker" {
		t.Fatalf("stderr = %q", got)
	}
	if elapsed := time.Since(start); elapsed < stderrSyncWait || elapsed > 3*stderrSyncWait {
		t.Fatalf("waited %s, want about %s", elapsed, stderrSyncWait)
	}
}
