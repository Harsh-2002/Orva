// Command migration-rehearsal builds a PRE-UUIDv7 Orva data directory:
// legacy prefix-typed ids in the database, and function code on disk under
// those legacy ids.
//
// It exists because the UUIDv7 migration's dangerous failure mode is not a
// startup error. If the ids move and the directories do not, every function
// fails to spawn and the build GC then removes the trees as orphans --
// minutes after a boot that looked successful. Unit tests cover the pieces;
// only a real boot against a real legacy data dir exercises the whole path.
//
// The schema is NOT created here. The caller boots the real orva binary
// once against the target data dir first, so the schema under test is the
// one the shipped binary produces rather than one this file constructed.
// This program then rewinds that database to a pre-migration state.
//
// Usage (see test/migration-rehearsal.sh, which drives all of it):
//
//	orva serve            # once, to lay down the schema; then stop it
//	go run ./test/migration-rehearsal -data <dir>          # rewind + seed
//	orva serve            # migration runs on this boot; then stop it
//	go run ./test/migration-rehearsal -data <dir> -verify
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

// pendingRenamesKey mirrors database.PendingFnRenamesKey. Duplicated as a
// literal because Go's internal-package rule puts backend/internal out of
// reach from test/ -- and reaching for it would defeat the point anyway:
// this harness should observe the data dir the way an operator would, not
// through the code under test.
const pendingRenamesKey = "uuidv7_pending_fn_renames"

// openDB opens the data dir's SQLite file directly.
func openDB(dataDir string) *sql.DB {
	path := filepath.Join(dataDir, "orva.db")
	if _, err := os.Stat(path); err != nil {
		fatal("no database at %s -- boot orva against this data dir first: %v", path, err)
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(10000)&_pragma=foreign_keys(ON)")
	if err != nil {
		fatal("open db: %v", err)
	}
	return db
}

// The fixture. Legacy ids in the shape Orva used before UUIDv7, one function
// bound to a channel (the binding that used to abort the migration), and one
// with none.
const (
	legacyFnBound   = "fn_boundtochannel"
	legacyFnPlain   = "fn_plainfunction"
	legacyChannel   = "chn_rehearsal01"
	legacyExecution = "exec_rehearsal1"
	legacyDeploy    = "dep_rehearsal01"
	legacyKey       = "key_rehearsal1"
	codeHash        = "abc123def456"
)

type expectation struct {
	Functions map[string]string `json:"functions"` // legacy id -> handler source
}

func main() {
	dataDir := flag.String("data", "", "data directory to build or verify (required)")
	verify := flag.Bool("verify", false, "verify a migrated data dir instead of seeding one")
	flag.Parse()

	if *dataDir == "" {
		fatal("-data is required")
	}
	if *verify {
		runVerify(*dataDir)
		return
	}
	runSeed(*dataDir)
}

func runSeed(dataDir string) {
	db := openDB(dataDir)
	defer db.Close()

	// Rewind: drop the completion marker so the next boot runs the
	// migration, then seed rows that predate it. This works because
	// migrations are additive and the rewrite skips ids that are already
	// UUIDs -- the same fixture shape the unit tests use.
	exec := func(q string, args ...any) {
		if _, err := db.Exec(q, args...); err != nil {
			fatal("seed (%s): %v", firstLine(q), err)
		}
	}
	exec(`DELETE FROM system_config WHERE key = 'uuidv7_migration_done'`)

	exec(`INSERT INTO functions (id, name, runtime, entrypoint, status) VALUES (?,?,?,?,?)`,
		legacyFnBound, "bound-fn", "node", "handler.js", "active")
	exec(`INSERT INTO functions (id, name, runtime, entrypoint, status) VALUES (?,?,?,?,?)`,
		legacyFnPlain, "plain-fn", "node", "handler.js", "active")

	// The channel binding: a hard FK to functions(id) that the migration's
	// hand-maintained child list used to miss, aborting the whole thing.
	exec(`INSERT INTO channels (id, name, token_hash, token_prefix) VALUES (?,?,?,?)`,
		legacyChannel, "rehearsal", "fakehash-rehearsal", "orva_chn_")
	exec(`INSERT INTO channel_functions (channel_id, function_id) VALUES (?,?)`,
		legacyChannel, legacyFnBound)

	// Soft references: no declared FK, so foreign_key_check is blind to them.
	exec(`INSERT INTO executions (id, function_id, status, started_at, parent_function_id)
	      VALUES (?,?, 'success', datetime('now'), ?)`,
		legacyExecution, legacyFnBound, legacyFnPlain)
	exec(`INSERT INTO user_spans (id, trace_id, parent_span_id, execution_id, name, started_at, duration_ms)
	      VALUES ('span_rehearsal','trace_r','parent_r',?, 'work', datetime('now'), 5)`,
		legacyExecution)
	exec(`INSERT INTO jobs (id, function_id, payload, status, scheduled_at, enqueued_by_function_id)
	      VALUES ('job_rehearsal1', ?, '{}', 'pending', datetime('now'), ?)`,
		legacyFnPlain, legacyFnBound)

	exec(`INSERT INTO deployments (id, function_id, version, status, phase) VALUES (?,?,?,?,?)`,
		legacyDeploy, legacyFnBound, 1, "succeeded", "complete")
	exec(`INSERT INTO api_keys (id, key_hash, name, permissions) VALUES (?,?,?,?)`,
		legacyKey, "rehearsal-hash", "rehearsal-key", `["read"]`)

	// Function code on disk, under the LEGACY ids, in the real layout:
	// versions/<hash>/ with `current` symlinked at it.
	want := expectation{Functions: map[string]string{}}
	for _, fn := range []string{legacyFnBound, legacyFnPlain} {
		src := fmt.Sprintf("module.exports = async () => ({fn: %q});\n", fn)
		versionDir := filepath.Join(dataDir, "functions", fn, "versions", codeHash)
		if err := os.MkdirAll(versionDir, 0o755); err != nil {
			fatal("create %s: %v", versionDir, err)
		}
		if err := os.WriteFile(filepath.Join(versionDir, "handler.js"), []byte(src), 0o644); err != nil {
			fatal("write handler: %v", err)
		}
		link := filepath.Join(dataDir, "functions", fn, "current")
		if err := os.Symlink(filepath.Join("versions", codeHash), link); err != nil {
			fatal("symlink current: %v", err)
		}
		want.Functions[fn] = src
	}

	blob, _ := json.MarshalIndent(want, "", "  ")
	if err := os.WriteFile(filepath.Join(dataDir, "rehearsal-expectation.json"), blob, 0o644); err != nil {
		fatal("write expectation: %v", err)
	}

	fmt.Printf("seeded pre-UUIDv7 data dir: %s\n", dataDir)
	fmt.Printf("  functions on disk (legacy ids): %s, %s\n", legacyFnBound, legacyFnPlain)
	fmt.Printf("  channel binding:                %s -> %s\n", legacyChannel, legacyFnBound)
	fmt.Printf("  soft refs seeded:               executions.parent_function_id, user_spans.execution_id, jobs.enqueued_by_function_id\n")
}

func runVerify(dataDir string) {
	blob, err := os.ReadFile(filepath.Join(dataDir, "rehearsal-expectation.json"))
	if err != nil {
		fatal("read expectation (did you seed this dir?): %v", err)
	}
	var want expectation
	if err := json.Unmarshal(blob, &want); err != nil {
		fatal("parse expectation: %v", err)
	}

	db := openDB(dataDir)
	defer db.Close()

	var failures []string
	fail := func(format string, a ...any) { failures = append(failures, fmt.Sprintf(format, a...)) }
	ok := func(format string, a ...any) { fmt.Printf("  ok    %s\n", fmt.Sprintf(format, a...)) }

	// 1. The migration ran to completion.
	var marker string
	_ = db.QueryRow(
		`SELECT value FROM system_config WHERE key = 'uuidv7_migration_done'`).Scan(&marker)
	if marker != "true" {
		fail("migration marker not set -- the migration did not commit")
	} else {
		ok("migration committed")
	}

	// 2. The rename record was cleared, which is what unblocks the build GC.
	var pending string
	_ = db.QueryRow(`SELECT value FROM system_config WHERE key = ?`,
		pendingRenamesKey).Scan(&pending)
	if strings.TrimSpace(pending) != "" {
		fail("pending directory renames outstanding -- the GC is still blocked, " +
			"and the ids on disk do not match the database")
	} else {
		ok("directory renames reconciled")
	}

	// 3. Every function id is a UUID now, and its code moved with it.
	newIDs := map[string]string{} // name -> new id
	rows, err := db.Query(`SELECT id, name FROM functions ORDER BY name`)
	if err != nil {
		fatal("query functions: %v", err)
	}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			fatal("scan: %v", err)
		}
		newIDs[name] = id
		if strings.HasPrefix(id, "fn_") {
			fail("function %q still has a legacy id: %s", name, id)
		}
	}
	rows.Close()

	legacyToName := map[string]string{legacyFnBound: "bound-fn", legacyFnPlain: "plain-fn"}
	for legacyID, src := range want.Functions {
		name := legacyToName[legacyID]
		newID := newIDs[name]
		if newID == "" {
			fail("function %q disappeared from the database", name)
			continue
		}
		// The whole point: the handler must be reachable at the NEW id.
		moved := filepath.Join(dataDir, "functions", newID, "versions", codeHash, "handler.js")
		got, err := os.ReadFile(moved)
		if err != nil {
			fail("%s: source not reachable at the new id (%v)", name, err)
			continue
		}
		if string(got) != src {
			fail("%s: handler source changed during the migration", name)
			continue
		}
		// `current` must still resolve, or a deployed function cannot spawn.
		cur := filepath.Join(dataDir, "functions", newID, "current", "handler.js")
		if _, err := os.Stat(cur); err != nil {
			fail("%s: `current` symlink does not resolve after the rename (%v)", name, err)
			continue
		}
		if _, err := os.Stat(filepath.Join(dataDir, "functions", legacyID)); !os.IsNotExist(err) {
			fail("%s: legacy directory %s still present", name, legacyID)
			continue
		}
		ok("%s: code followed its id (%s -> %s), `current` resolves", name, legacyID, newID)
	}

	// 4. The channel binding -- the FK whose absence used to abort the commit.
	var boundTo string
	if err := db.QueryRow(`SELECT function_id FROM channel_functions`).Scan(&boundTo); err != nil {
		fail("channel binding lost: %v", err)
	} else if boundTo != newIDs["bound-fn"] {
		fail("channel_functions.function_id = %s, want %s", boundTo, newIDs["bound-fn"])
	} else {
		ok("channel binding follows the rewritten function id")
	}

	// 5. Soft references: invisible to foreign_key_check, so they are the
	//    ones that rot silently.
	softRefs := []struct{ label, query, want string }{
		{"executions.parent_function_id",
			`SELECT COALESCE(parent_function_id,'') FROM executions`, newIDs["plain-fn"]},
		{"jobs.enqueued_by_function_id",
			`SELECT COALESCE(enqueued_by_function_id,'') FROM jobs`, newIDs["bound-fn"]},
	}
	for _, sr := range softRefs {
		var got string
		if err := db.QueryRow(sr.query).Scan(&got); err != nil {
			fail("%s: %v", sr.label, err)
			continue
		}
		if got != sr.want {
			fail("%s = %q, want %q (soft reference not rewritten)", sr.label, got, sr.want)
			continue
		}
		ok("%s rewritten", sr.label)
	}
	var spanExec string
	if err := db.QueryRow(`SELECT execution_id FROM user_spans`).Scan(&spanExec); err != nil {
		fail("user_spans: %v", err)
	} else if strings.HasPrefix(spanExec, "exec_") {
		fail("user_spans.execution_id still legacy (%s) -- the span is now unreachable "+
			"and PurgeOldExecutions can never reclaim it", spanExec)
	} else {
		ok("user_spans.execution_id rewritten")
	}

	if len(failures) > 0 {
		fmt.Fprintf(os.Stderr, "\nFAILED (%d):\n", len(failures))
		sort.Strings(failures)
		for _, f := range failures {
			fmt.Fprintf(os.Stderr, "  x %s\n", f)
		}
		os.Exit(1)
	}
	fmt.Printf("\nrehearsal passed: ids moved, code followed, references intact\n")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "migration-rehearsal: "+format+"\n", a...)
	os.Exit(1)
}
