package proxy

import "testing"

// nsjail writes its own log lines to the sandbox's stderr. On a cold start they
// land in whichever invocation spawned the worker, so an operator debugging
// their handler saw two lines about "root-level access to files" that had
// nothing to do with their code -- and the dashboard's suggest-a-fix prompt was
// fed them as if they were the handler's output.
//
// Captured verbatim from a real cold start.
const realColdStart = `[W][2026-08-22T20:46:24+0000][793973] logParams():318 Process will be UID/EUID=0 in the global user namespace, and will have user root-level access to files
[W][2026-08-22T20:46:24+0000][793973] logParams():328 Process will be GID/EGID=0 in the global user namespace, and will have group root-level access to files
DIAGNOSTIC LINE ON STDERR
Handler error: Error: boom from handler
    at exports.handler (/code/handler.js:1:83)`

func TestStripNsjailNoiseKeepsTheHandlersOutput(t *testing.T) {
	got := string(stripNsjailNoise([]byte(realColdStart)))

	for _, want := range []string{
		"DIAGNOSTIC LINE ON STDERR",
		"Handler error: Error: boom from handler",
		"at exports.handler (/code/handler.js:1:83)",
	} {
		if !contains(got, want) {
			t.Errorf("dropped a line the operator needs: %q\ngot:\n%s", want, got)
		}
	}
	if contains(got, "logParams()") || contains(got, "root-level access") {
		t.Errorf("nsjail's own warnings survived:\n%s", got)
	}
}

// A spawn that genuinely fails is diagnosed by nsjail's error lines. Dropping
// those would leave a bare WORKER_CRASHED with nothing to go on -- which is
// exactly the state that made a cgroup misconfiguration hard to place.
func TestStripNsjailNoiseKeepsErrorsAndFatals(t *testing.T) {
	failed := `[W][2026-08-22T20:46:24+0000][793973] logParams():318 Process will be UID/EUID=0
[E][2026-08-22T20:46:24+0000][793973] initParent():482 Couldn't initialize user namespace for pid=793974
[F][2026-08-22T20:46:24+0000][1] runChild():551 Launching child process failed`

	got := string(stripNsjailNoise([]byte(failed)))
	if !contains(got, "Couldn't initialize user namespace") {
		t.Errorf("dropped nsjail's [E] diagnosis:\n%s", got)
	}
	if !contains(got, "Launching child process failed") {
		t.Errorf("dropped nsjail's [F] diagnosis:\n%s", got)
	}
	if contains(got, "logParams()") {
		t.Errorf("kept the [W] noise:\n%s", got)
	}
}

// A blob that was nothing but noise must come back genuinely empty, so the
// dashboard renders "no stderr captured" rather than an empty box, and the
// suggest-a-fix button stays disabled.
func TestStripNsjailNoiseEmptiesANoiseOnlyBlob(t *testing.T) {
	only := `[W][2026-08-22T20:46:24+0000][793973] logParams():318 Process will be UID/EUID=0
[W][2026-08-22T20:46:24+0000][793973] logParams():328 Process will be GID/EGID=0
`
	if got := stripNsjailNoise([]byte(only)); len(got) != 0 {
		t.Errorf("want empty, got %d bytes: %q", len(got), got)
	}
}

// Handler output that merely starts with a bracket must not be mistaken for it.
func TestStripNsjailNoiseLeavesHandlerOutputAlone(t *testing.T) {
	for _, s := range []string{
		"[INFO] my app started\n[WARN] cache miss",
		`{"level":"error","msg":"boom"}`,
		"[W] not nsjail, no timestamp or pid",
		"",
	} {
		if got := string(stripNsjailNoise([]byte(s))); got != s {
			t.Errorf("rewrote handler output\n in: %q\nout: %q", s, got)
		}
	}
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
