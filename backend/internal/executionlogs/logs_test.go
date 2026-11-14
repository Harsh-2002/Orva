package executionlogs

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

func TestExtract(t *testing.T) {
	for _, tc := range []struct {
		name, input, raw, span string
		count                  int
	}{
		{"plain", "hello\n", "hello\n", "", 0},
		{"malformed", magicPrefix + "{broken}\n", magicPrefix + "{broken}\n", "", 0},
		{"mixed", "before\n" + magicPrefix + `{"ts":"2026-11-14T08:00:00Z","level":"info","message":"saved","fields":{"n":2}}` + "\nafter", "before\nafter", "sp-parent", 1},
		{"explicit span", magicPrefix + `{"level":"warn","message":"child","span_id":"sp-child"}`, "", "sp-child", 1},
		{"invalid timestamp", magicPrefix + `{"ts":"bad","level":"error","message":"failure"}` + "\n", "", "sp-parent", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, entries := Extract([]byte(tc.input), "fn", "exec", "tr-test", "sp-parent")
			if string(raw) != tc.raw || len(entries) != tc.count {
				t.Fatalf("raw=%q entries=%d, want %q/%d", raw, len(entries), tc.raw, tc.count)
			}
			for _, entry := range entries {
				if entry.FunctionID != "fn" || entry.ExecutionID != "exec" || entry.TraceID != "tr-test" || entry.SpanID != tc.span || entry.TS.IsZero() {
					t.Fatalf("incorrect correlation: %+v", entry)
				}
			}
		})
	}
}

func TestPersistAcrossInvocationTriggers(t *testing.T) {
	for _, trigger := range []string{"http", "f2f", "cron", "job", "inbound", "replay"} {
		t.Run(trigger, func(t *testing.T) {
			db, err := database.New(filepath.Join(t.TempDir(), "logs.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err := db.Migrate(); err != nil {
				t.Fatal(err)
			}
			fn := &database.Function{ID: "fn-" + trigger, Name: "log-" + trigger, Runtime: "node", Entrypoint: "handler.js", Status: "active", NetworkMode: "none", AuthMode: "none"}
			if err := db.InsertFunction(fn); err != nil {
				t.Fatal(err)
			}
			if err := db.AsyncInsertExecutionFinal(&database.Execution{
				ID: "exec-" + trigger, FunctionID: fn.ID, Status: "success", Trigger: trigger,
				TraceID: "tr-test", SpanID: "sp-test", StartedAt: time.Now(),
			}, 1, 200, "", 0); err != nil {
				t.Fatal(err)
			}
			stderr := "[W][2026-11-14][123] logParams():318 routine warning\nconsole output\n" + magicPrefix + `{"level":"info","message":"structured","fields":{"attempt":2}}` + "\n" + magicPrefix + "{bad}\n"
			Persist(db, []byte(stderr), fn.ID, "exec-"+trigger, "tr-test", "sp-test")
			deadline := time.Now().Add(3 * time.Second)
			for {
				logs, err := db.ListLogEntriesByExecution("exec-" + trigger)
				if err != nil {
					t.Fatal(err)
				}
				raw, rawErr := db.GetExecutionLog("exec-" + trigger)
				if len(logs) == 1 && rawErr == nil && raw != nil {
					entry := logs[0]
					if entry.TraceID != "tr-test" || entry.SpanID != "sp-test" || entry.Fields != `{"attempt":2}` {
						t.Fatalf("wrong log metadata: %+v", entry)
					}
					if !strings.Contains(raw.Stderr, "console output") || !strings.Contains(raw.Stderr, "{bad}") || strings.Contains(raw.Stderr, "routine warning") || strings.Contains(raw.Stderr, `"structured"`) {
						t.Fatalf("wrong raw stderr: %q", raw.Stderr)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("logs not persisted: structured=%d rawErr=%v", len(logs), rawErr)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}
