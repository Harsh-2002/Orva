package executionlogs

import (
	"bytes"
	"encoding/json"
	"regexp"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

const magicPrefix = "__ORVA_LOG_JSON__"

var nsjailNoise = regexp.MustCompile(`^\[[WI]\]\[[^\]]*\]\[\d+\] \w+\(\):\d+ `)

// Persist keeps raw and structured output available on non-proxy dispatch paths.
func Persist(db *database.Database, stderr []byte, fnID, execID, traceID, spanID string) {
	stderr = Process(db, stderr, fnID, execID, traceID, spanID)
	if len(stderr) > 0 {
		db.AsyncInsertExecutionLog(&database.ExecutionLog{
			ExecutionID: execID, FunctionID: fnID, Stderr: string(stderr),
		})
	}
}

// Process extracts SDK structured logs into the telemetry writer and returns
// stderr with those lines and nsjail's routine startup noise removed.
func Process(db *database.Database, stderr []byte, fnID, execID, traceID, spanID string) []byte {
	if len(stderr) == 0 {
		return stderr
	}
	clean, entries := Extract(stderr, fnID, execID, traceID, spanID)
	if db != nil {
		for _, entry := range entries {
			db.AsyncInsertLogEntry(entry)
		}
	}
	return stripNsjailNoise(clean)
}

// Extract separates valid SDK log frames while leaving malformed frames intact.
func Extract(stderr []byte, fnID, execID, traceID, spanID string) ([]byte, []*database.LogEntry) {
	if len(stderr) == 0 || !bytes.Contains(stderr, []byte(magicPrefix)) {
		return stderr, nil
	}
	var keep bytes.Buffer
	keep.Grow(len(stderr))
	var entries []*database.LogEntry
	for _, line := range bytes.SplitAfter(stderr, []byte{'\n'}) {
		probe := line
		if len(probe) > 0 && probe[len(probe)-1] == '\n' {
			probe = probe[:len(probe)-1]
		}
		if !bytes.HasPrefix(probe, []byte(magicPrefix)) {
			keep.Write(line)
			continue
		}
		var rec struct {
			TS      string          `json:"ts"`
			Level   string          `json:"level"`
			Message string          `json:"message"`
			Fields  json.RawMessage `json:"fields,omitempty"`
			SpanID  string          `json:"span_id,omitempty"`
		}
		if err := json.Unmarshal(probe[len(magicPrefix):], &rec); err != nil {
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
			FunctionID: fnID, ExecutionID: execID, TraceID: traceID,
			SpanID: effectiveSpan, TS: ts, Level: rec.Level, Message: rec.Message,
		}
		if len(rec.Fields) > 0 {
			entry.Fields = string(rec.Fields)
		}
		entries = append(entries, entry)
	}
	return keep.Bytes(), entries
}

func stripNsjailNoise(stderr []byte) []byte {
	if len(stderr) == 0 || !bytes.HasPrefix(bytes.TrimLeft(stderr, "\n"), []byte("[")) {
		return stderr
	}
	lines := bytes.Split(stderr, []byte("\n"))
	kept := lines[:0]
	for _, line := range lines {
		if !nsjailNoise.Match(line) {
			kept = append(kept, line)
		}
	}
	out := bytes.Join(kept, []byte("\n"))
	if len(bytes.TrimSpace(out)) == 0 {
		return nil
	}
	return out
}

// StripNsjailNoise removes only routine nsjail startup messages.
func StripNsjailNoise(stderr []byte) []byte { return stripNsjailNoise(stderr) }
