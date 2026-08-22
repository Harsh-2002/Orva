package proxy

import (
	"bytes"
	"encoding/json"
	"regexp"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

// orvaLogMagicPrefix is the line marker the runtime SDK emits on stderr
// for every orva.log.* call. Any stderr line beginning with this prefix
// is JSON-decoded into a LogEntry and queued for the structured-logs
// table; non-magic lines remain in the existing stderr blob.
const orvaLogMagicPrefix = "__ORVA_LOG_JSON__"

// extractStructuredLogs scans stderr line-by-line, separating magic lines
// from regular stderr. Magic lines are queued into execution_log_entries
// via the async writer; the returned []byte is the original blob with
// the magic lines stripped so they don't appear twice in the dashboard.
//
// `execID`, `traceID`, `spanID` describe the parent execution; they ride
// onto every entry that doesn't already carry its own. Callers pass them
// after Forward returns so the writer reuses the trace context that was
// already set on the request env.
func extractStructuredLogs(db *database.Database, stderr []byte, execID, traceID, spanID string) []byte {
	if len(stderr) == 0 || !bytes.Contains(stderr, []byte(orvaLogMagicPrefix)) {
		return stderr
	}
	var keep bytes.Buffer
	keep.Grow(len(stderr))

	// Split on \n but preserve a trailing partial line (no terminator).
	// bytes.Split allocates one slice per line; for stderr blobs of a few
	// KiB this is fine — the alternative scanner is more code for no win.
	for i, line := range bytes.SplitAfter(stderr, []byte{'\n'}) {
		_ = i
		// Strip trailing newline for the prefix check; keep_writing
		// preserves the original line termination.
		var probe []byte
		if len(line) > 0 && line[len(line)-1] == '\n' {
			probe = line[:len(line)-1]
		} else {
			probe = line
		}
		if !bytes.HasPrefix(probe, []byte(orvaLogMagicPrefix)) {
			keep.Write(line)
			continue
		}
		payload := probe[len(orvaLogMagicPrefix):]
		var rec struct {
			TS      string          `json:"ts"`
			Level   string          `json:"level"`
			Message string          `json:"message"`
			Fields  json.RawMessage `json:"fields,omitempty"`
			SpanID  string          `json:"span_id,omitempty"`
		}
		if err := json.Unmarshal(payload, &rec); err != nil {
			// Malformed magic line — preserve the raw text in stderr so the
			// operator can see what went wrong instead of silently dropping.
			keep.Write(line)
			continue
		}
		ts, _ := time.Parse(time.RFC3339Nano, rec.TS)
		if ts.IsZero() {
			ts = time.Now().UTC()
		}
		effectiveSpan := rec.SpanID
		if effectiveSpan == "" {
			effectiveSpan = spanID
		}
		entry := &database.LogEntry{
			ExecutionID: execID,
			TraceID:     traceID,
			SpanID:      effectiveSpan,
			TS:          ts,
			Level:       rec.Level,
			Message:     rec.Message,
		}
		if len(rec.Fields) > 0 {
			entry.Fields = string(rec.Fields)
		}
		db.AsyncInsertLogEntry(entry)
	}
	return keep.Bytes()
}

// nsjailNoise matches nsjail's own log lines, which use a fixed
// `[LEVEL][timestamp][pid] func():line message` shape.
//
// nsjail writes these to the sandbox's stderr, so on a cold start they land in
// whichever invocation happened to spawn the worker and are attributed to the
// operator's handler. Two of them appear on every spawn:
//
//	[W][...] logParams():318 Process will be UID/EUID=0 in the global user namespace...
//	[W][...] logParams():328 Process will be GID/EGID=0 in the global user namespace...
//
// They describe the sandbox's own configuration, are identical every time, and
// read alarmingly ("root-level access to files") to someone debugging their
// function. They also went into the dashboard's "suggest a fix" prompt as if
// they were the handler's output.
//
// Only W and I are dropped. E and F are kept: when a spawn genuinely fails,
// nsjail's error line is the diagnosis, and swallowing it would leave a bare
// WORKER_CRASHED with nothing to go on.
var nsjailNoise = regexp.MustCompile(`^\[[WI]\]\[[^\]]*\]\[\d+\] \w+\(\):\d+ `)

// stripNsjailNoise removes nsjail's own warning/info lines from a stderr blob.
func stripNsjailNoise(stderr []byte) []byte {
	if len(stderr) == 0 || !bytes.HasPrefix(bytes.TrimLeft(stderr, "\n"), []byte("[")) {
		return stderr
	}
	lines := bytes.Split(stderr, []byte("\n"))
	kept := lines[:0]
	for _, ln := range lines {
		if nsjailNoise.Match(ln) {
			continue
		}
		kept = append(kept, ln)
	}
	out := bytes.Join(kept, []byte("\n"))
	// A blob that was nothing but noise becomes genuinely empty, not a run of
	// blank lines the dashboard would render as an empty <pre>.
	if len(bytes.TrimSpace(out)) == 0 {
		return nil
	}
	return out
}
