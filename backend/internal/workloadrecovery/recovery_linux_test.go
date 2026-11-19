//go:build linux

package workloadrecovery

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/backup"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
	"golang.org/x/sys/unix"
)

type fixture struct {
	dir        string
	owners     []string
	jobs       []string
	crons      []string
	deployment string
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func openDB(t *testing.T, dir string) *database.Database {
	t.Helper()
	db, err := database.New(filepath.Join(dir, "orva.db"))
	must(t, err)
	must(t, db.Migrate())
	return db
}

func seed(t *testing.T, active bool) (fixture, []byte) {
	t.Helper()
	f := fixture{dir: t.TempDir(), owners: []string{ids.New(), ids.New()}, deployment: ids.New()}
	db := openDB(t, f.dir)
	defer db.Close()
	for i, id := range f.owners {
		must(t, db.InsertFunction(&database.Function{ID: id, Name: "recovery-" + id, Runtime: "node",
			Entrypoint: "handler.js", Status: "active", SDK: i == 1, CodeHash: strings.Repeat("a", 64)}))
		path := filepath.Join(f.dir, "functions", id, "versions", strings.Repeat("a", 64))
		must(t, os.MkdirAll(path, 0700))
		must(t, os.WriteFile(filepath.Join(path, "handler.js"), []byte("export default () => 'retained'"), 0600))
		must(t, db.KVPut(id, "retained", []byte(`"retained"`), nil))
		j, c := ids.New(), ids.New()
		f.jobs = append(f.jobs, j)
		f.crons = append(f.crons, c)
		_, err := db.WriteDB().Exec(`INSERT INTO jobs(id,function_id,payload,status,scheduled_at,enqueued_by_function_id) VALUES(?,?,'{}','pending',CURRENT_TIMESTAMP,?)`,
			j, id, []string{id, ""}[i])
		must(t, err)
		_, err = db.WriteDB().Exec(`INSERT INTO cron_schedules(id,function_id,name,cron_expr,next_run_at) VALUES(?,?,'retained','* * * * *',CURRENT_TIMESTAMP)`, c, id)
		must(t, err)
	}
	_, err := db.WriteDB().Exec(`INSERT INTO deployments(id,function_id,version,status) VALUES(?,?,1,'queued')`, f.deployment, f.owners[0])
	must(t, err)
	if active {
		_, err := db.ApplyRecoveryForFixture(t.Context(), "")
		must(t, err)
		for i, id := range f.owners {
			r, err := db.GetIAMPolicy(t.Context(), id)
			must(t, err)
			policy := `{"schema_version":1,"allow":[{"actions":["kv.get"],"resource":{"own_kv":{"key":"retained"}},"context":"execution"}],"inbound":[{"caller_id":"` + f.owners[1-i] + `","triggers":["f2f","job"]}]}`
			_, err = db.SaveIAMPolicy(t.Context(), database.IAMPolicyChange{OwnerID: id, ExpectedRevision: r.Revision,
				ExpectedApprovedArtifact: r.ApprovedArtifact, ApprovedArtifact: "retained-approval", PolicyJSON: []byte(policy),
				Actor: database.IAMActorReference{Kind: "system", ID: ids.New()}})
			must(t, err)
			_, err = db.WriteDB().Exec(`UPDATE workload_iam_inline SET policy_json=? WHERE function_id=?`, policy, id)
			must(t, err)
			pid := ids.New()
			_, err = db.WriteDB().Exec(`INSERT INTO workload_iam_managed VALUES(?,?,1,1,1,0)`, pid, "retained-"+pid)
			must(t, err)
			_, err = db.WriteDB().Exec(`INSERT INTO workload_iam_managed_versions VALUES(?,1,?,'system',?,CURRENT_TIMESTAMP)`, pid, policy, ids.New())
			must(t, err)
			_, err = db.WriteDB().Exec(`INSERT INTO workload_iam_attachments VALUES(?,?,1,1)`, id, pid)
			must(t, err)
			origin := []string{"function", "operator"}[i]
			_, err = db.WriteDB().Exec(`UPDATE jobs SET authority_state='active',origin_kind=?,creator_id=?,operator_kind='api_key',operator_id=?,
			 admission_caller_revision=2,admission_target_revision=2 WHERE id=?`, origin, id, ids.New(), f.jobs[i])
			must(t, err)
			_, err = db.WriteDB().Exec(`UPDATE cron_schedules SET authority_state='active',origin_kind=?,creator_id=?,operator_kind='api_key',operator_id=?,
			 admission_caller_revision=2,admission_target_revision=2 WHERE id=?`, origin, id, ids.New(), f.crons[i])
			must(t, err)
		}
	}
	must(t, db.InitHistorySchemaForFixture(t.Context()))
	_, err = db.WriteDB().Exec(`INSERT INTO work_history VALUES('job',?,?,'retained','{}',2,0,CURRENT_TIMESTAMP);
	 UPDATE work_history_capacity SET rows_used=1,bytes_used=2`, ids.New(), f.owners[0])
	must(t, err)
	snap := filepath.Join(f.dir, "snapshot.db")
	must(t, backup.SnapshotDB(db.WriteDB(), snap))
	var archive bytes.Buffer
	must(t, backup.ArchiveTo(&archive, f.dir, snap))
	return f, archive.Bytes()
}

func coordinator(t *testing.T, dir string) *Coordinator {
	t.Helper()
	c, err := Open(dir, Host{Quiesce: func(context.Context) error { return nil }, AdmissionClosed: func() bool { return true }})
	must(t, err)
	t.Cleanup(func() { must(t, c.Close()) })
	return c
}

func start(t *testing.T, c *Coordinator) (*database.Database, database.RecoveryResult, error) {
	t.Helper()
	return c.Start(t.Context(), func(_ context.Context, db *database.Database) error {
		var n int
		if err := db.ReadDB().QueryRow(`SELECT count(*) FROM workload_iam_policies WHERE quarantined=1 OR policy_json=?`, database.RecoveryEmptyPolicy).Scan(&n); err != nil {
			return err
		}
		if n != 2 {
			return errors.New("reconstruct ran before quarantine")
		}
		return nil
	}, func(context.Context, *database.Database) error { return nil })
}

func assertRetained(t *testing.T, db *database.Database, f fixture, dir string, active bool) {
	t.Helper()
	for i, id := range f.owners {
		p, err := db.GetIAMPolicy(t.Context(), id)
		must(t, err)
		if !p.Quarantined {
			t.Fatal("restored policy not quarantined")
		}
		e, err := p.AdmissionEvaluator()
		must(t, err)
		key := "retained"
		if e.Allows(workloadiam.Context{Principal: workloadiam.Principal{FunctionID: id, WorkerID: "worker", ArtifactRevision: p.ApprovedArtifact},
			Phase: workloadiam.Execution, Ready: true, Lease: workloadiam.Lease{ID: "dispatch", WorkerID: "worker", ExecutionID: ids.New(), Active: true}},
			workloadiam.Request{Operation: workloadiam.OpKVGet, FunctionID: id, Resource: workloadiam.Resource{OwnKV: &workloadiam.KVResource{Key: &key}}}) {
			t.Fatal("retained grant active after restore")
		}
		if active {
			if p.PolicyJSON == database.RecoveryEmptyPolicy || p.ApprovedArtifact != "retained-approval" {
				t.Fatal("lost retained authority")
			}
			var attached int
			must(t, db.ReadDB().QueryRow(`SELECT count(*) FROM workload_iam_attachments WHERE function_id=? AND enabled=1`, id).Scan(&attached))
			if attached != 1 {
				t.Fatal("lost pinned source")
			}
		} else if p.PolicyJSON != database.RecoveryEmptyPolicy || p.ReviewRequired != (i == 1) {
			t.Fatal("legacy boolean expanded or review lost")
		}
		var raw string
		must(t, db.ReadDB().QueryRow(`SELECT value FROM kv_store WHERE function_id=? AND key='retained'`, id).Scan(&raw))
		if raw != `"retained"` {
			t.Fatal("KV changed")
		}
		code, err := os.ReadFile(filepath.Join(dir, "functions", id, "versions", strings.Repeat("a", 64), "handler.js"))
		must(t, err)
		if string(code) != "export default () => 'retained'" {
			t.Fatal("source changed")
		}
		audit, err := db.ListIAMPolicyAudit(t.Context(), id)
		must(t, err)
		if len(audit) == 0 || len(audit) > database.IAMAuditRetention {
			t.Fatal("missing/beyond-bound audit")
		}
	}
	for _, table := range []string{"jobs", "cron_schedules"} {
		var n int
		must(t, db.ReadDB().QueryRow(`SELECT count(*) FROM `+table+` WHERE authority_state='quarantined' AND admission_caller_revision=0 AND admission_target_revision=0`).Scan(&n))
		if n != 2 {
			t.Fatalf("%s: not all function and operator work quarantined", table)
		}
	}
	for _, table := range []string{"async_work_audit", "async_cron_audit"} {
		var n int
		must(t, db.ReadDB().QueryRow(`SELECT count(*) FROM `+table+` WHERE action='restore_quarantine'`).Scan(&n))
		if n != 2 {
			t.Fatal("missing per-work quarantine audit")
		}
	}
	var n int
	must(t, db.ReadDB().QueryRow(`SELECT count(*) FROM work_history WHERE metadata='{}'`).Scan(&n))
	if n != 1 {
		t.Fatal("history changed")
	}
	var status string
	must(t, db.ReadDB().QueryRow(`SELECT status FROM deployments WHERE id=?`, f.deployment).Scan(&status))
	if status != "failed" {
		t.Fatal("stale queued build provenance was not failed")
	}
}

func TestRecoveryArchiveThroughQuarantine(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "pre-IAM", true: "retained-IAM"}[active], func(t *testing.T) {
			f, archive := seed(t, active)
			dir := t.TempDir()
			c := coordinator(t, dir)
			op, err := c.Restore(t.Context(), bytes.NewReader(archive))
			must(t, err)
			db, r, err := start(t, c)
			must(t, err)
			defer db.Close()
			if r.Epoch != 1 || r.Operation != op {
				t.Fatal("wrong recovery epoch")
			}
			assertRetained(t, db, f, dir, active)
			if _, err := os.Lstat(filepath.Join(dir, backup.RecoveryMarkerName)); !os.IsNotExist(err) {
				t.Fatal("committed marker not removed")
			}
		})
	}
}

func TestRecoveryCrashBoundaries(t *testing.T) {
	for _, point := range []string{"marker_staging", "staged", "marker_installing", "installed", "marker_ready", "before_quarantine", "committed", "before_remove", "removed", "removal_synced"} {
		t.Run(point, func(t *testing.T) {
			_, archive := seed(t, false)
			c := coordinator(t, t.TempDir())
			injected := errors.New("injected crash boundary")
			fired := false
			c.fault = func(s string) error {
				if s == point {
					fired = true
					return injected
				}
				return nil
			}
			op, err := c.Restore(t.Context(), bytes.NewReader(archive))
			if err == nil {
				db, _, e := start(t, c)
				if db != nil {
					db.Close()
				}
				err = e
			}
			if !fired || !errors.Is(err, injected) {
				t.Fatal("boundary not exercised")
			}
			c.fault = nil
			m, err := c.readMarker()
			must(t, err)
			if m != nil && m.State != "ready" {
				called := false
				_, _, err = c.Start(t.Context(), func(context.Context, *database.Database) error { called = true; return nil }, func(context.Context, *database.Database) error { called = true; return nil })
				if !errors.Is(err, ErrIncomplete) || called {
					t.Fatal("incomplete install admitted")
				}
				same, e := c.Restore(t.Context(), bytes.NewReader(archive))
				must(t, e)
				if same != op {
					t.Fatal("retry invented operation")
				}
			}
			db, r, err := start(t, c)
			must(t, err)
			defer db.Close()
			if r.Epoch != 1 || r.Operation != op {
				t.Fatal("recovery replay changed epoch")
			}
		})
	}
}

func TestRecoveryMalformedAndLinkedMarkersDenyBeforeOpen(t *testing.T) {
	for _, kind := range []string{"json", "oversize", "symlink", "hardlink", "fifo", "unknown", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, backup.RecoveryMarkerName)
			c := coordinator(t, dir)
			switch kind {
			case "json":
				must(t, os.WriteFile(path, []byte("{"), 0600))
			case "oversize":
				must(t, os.WriteFile(path, bytes.Repeat([]byte("x"), 257), 0600))
			case "symlink":
				must(t, os.Symlink(filepath.Join(t.TempDir(), "outside"), path))
			case "hardlink":
				other := filepath.Join(dir, "other")
				must(t, os.WriteFile(other, []byte("{}"), 0600))
				must(t, os.Link(other, path))
			case "fifo":
				must(t, unix.Mkfifo(path, 0600))
			case "unknown":
				must(t, os.WriteFile(path, []byte(`{"version":1,"operation":"`+ids.New()+`","state":"ready","approve":true}`), 0600))
			case "duplicate":
				must(t, os.WriteFile(path, []byte(`{"version":1,"version":1,"operation":"`+ids.New()+`","state":"ready"}`), 0600))
			}
			called := false
			_, _, err := c.Start(t.Context(), func(context.Context, *database.Database) error { called = true; return nil }, func(context.Context, *database.Database) error { called = true; return nil })
			if err == nil || called {
				t.Fatal("unsafe marker admitted")
			}
			if _, err := os.Stat(filepath.Join(dir, "orva.db")); !os.IsNotExist(err) {
				t.Fatal("DB opened before marker validation")
			}
		})
	}
}

func TestRecoveryArchiveCannotSupplyMarker(t *testing.T) {
	_, archive := seed(t, false)
	for _, name := range []string{backup.RecoveryMarkerName, "functions/" + ids.New() + "/" + backup.RecoveryMarkerName} {
		t.Run(name, func(t *testing.T) {
			c := coordinator(t, t.TempDir())
			var b bytes.Buffer
			gz := gzip.NewWriter(&b)
			tw := tar.NewWriter(gz)
			must(t, tw.WriteHeader(&tar.Header{Name: name, Mode: 0600, Size: 2}))
			_, err := tw.Write([]byte("{}"))
			must(t, err)
			must(t, tw.Close())
			must(t, gz.Close())
			op, err := c.Restore(t.Context(), bytes.NewReader(b.Bytes()))
			if !errors.Is(err, backup.ErrBadArchive) {
				t.Fatal("archive control path accepted")
			}
			m, err := c.readMarker()
			must(t, err)
			if m == nil || m.Operation != op || m.State != "staging" {
				t.Fatal("archive changed host marker")
			}
			_, err = c.Restore(t.Context(), bytes.NewReader(archive))
			must(t, err)
		})
	}
}

func TestRecoveryFsyncFailures(t *testing.T) {
	_, archive := seed(t, false)
	for _, point := range []string{"marker_file_staging", "marker_dir_staging", "marker_file_installing", "marker_dir_installing",
		"installed_file_orva.db", "installed_dir", "marker_file_ready", "marker_dir_ready", "quarantine_dir", "removal_dir"} {
		t.Run(point, func(t *testing.T) {
			c := coordinator(t, t.TempDir())
			fired := false
			c.sync = func(p string, action func() error) error {
				if p == point {
					fired = true
					return unix.EIO
				}
				return action()
			}
			_, err := c.Restore(t.Context(), bytes.NewReader(archive))
			called := false
			if err == nil {
				_, _, err = c.Start(t.Context(), func(context.Context, *database.Database) error { called = true; return nil },
					func(context.Context, *database.Database) error { called = true; return nil })
			}
			if !fired || !errors.Is(err, unix.EIO) || called {
				t.Fatal("fsync failure did not close admission")
			}
			c.sync = nil
			m, err := c.readMarker()
			must(t, err)
			if m == nil && point == "marker_file_staging" {
				_, err = c.Restore(t.Context(), bytes.NewReader(archive))
				must(t, err)
			} else if m != nil && m.State != "ready" {
				_, err = c.Restore(t.Context(), bytes.NewReader(archive))
				must(t, err)
			}
			db, r, err := start(t, c)
			must(t, err)
			defer db.Close()
			if r.Epoch != 1 {
				t.Fatal("fsync retry repeated quarantine")
			}
		})
	}
}

func TestRecoveryManualAndCallbackOrder(t *testing.T) {
	f, _ := seed(t, false)
	closed, quiesced := false, false
	live := openDB(t, f.dir)
	c, err := Open(f.dir, Host{
		AdmissionClosed: func() bool { return closed },
		Quiesce: func(context.Context) error {
			quiesced = true
			closed = true
			if live != nil {
				err := live.Close()
				live = nil
				return err
			}
			return nil
		}})
	must(t, err)
	defer c.Close()
	op, err := c.PrepareManual(t.Context())
	must(t, err)
	if !closed || !quiesced || live != nil {
		t.Fatal("manual marker preceded quiescence")
	}
	called := false
	_, _, err = c.Start(t.Context(), func(context.Context, *database.Database) error { called = true; return nil },
		func(context.Context, *database.Database) error { called = true; return nil })
	if !errors.Is(err, ErrIncomplete) || called {
		t.Fatal("unacknowledged manual install started")
	}
	must(t, c.CompleteManual(t.Context(), op))
	db, r, err := c.Start(t.Context(), func(ctx context.Context, db *database.Database) error {
		if !closed {
			return ErrClosed
		}
		// DB callbacks must not run under the database lifecycle lock/transaction.
		_, err := db.ApplyRecoveryForFixture(ctx, "")
		return err
	}, func(_ context.Context, db *database.Database) error {
		if !closed {
			return ErrClosed
		}
		var n int
		must(t, db.ReadDB().QueryRow(`SELECT count(*) FROM jobs WHERE authority_state='quarantined'`).Scan(&n))
		if n != 2 {
			t.Fatal("start preceded quarantine")
		}
		called = true
		return nil
	})
	must(t, err)
	must(t, db.Close())
	if !called || r.Operation != op || r.Epoch != 1 {
		t.Fatal("manual operation was not committed")
	}
	denied := coordinator(t, t.TempDir())
	denied.host.AdmissionClosed = func() bool { return false }
	if _, err := denied.Restore(t.Context(), bytes.NewReader(nil)); !errors.Is(err, ErrClosed) {
		t.Fatal("open-admission restore")
	}
	if m, err := denied.readMarker(); err != nil || m != nil {
		t.Fatal("wrote marker before closed admission")
	}
}

func TestRecoveryFailedCommitRetainsMarkerAndAllWork(t *testing.T) {
	_, archive := seed(t, true)
	c := coordinator(t, t.TempDir())
	op, err := c.Restore(t.Context(), bytes.NewReader(archive))
	must(t, err)
	db := openDB(t, c.dir)
	_, err = db.WriteDB().Exec(`CREATE TABLE fail_parent(id INTEGER PRIMARY KEY);
	 CREATE TABLE fail_child(id INTEGER REFERENCES fail_parent(id) DEFERRABLE INITIALLY DEFERRED);
	 CREATE TRIGGER fail_commit AFTER UPDATE ON workload_recovery BEGIN INSERT INTO fail_child VALUES(1); END;`)
	must(t, err)
	must(t, db.Close())
	called := false
	_, _, err = c.Start(t.Context(), func(context.Context, *database.Database) error { called = true; return nil },
		func(context.Context, *database.Database) error { called = true; return nil })
	if err == nil || called {
		t.Fatal("COMMIT failure started callbacks")
	}
	m, err := c.readMarker()
	must(t, err)
	if m == nil || m.Operation != op || m.State != "ready" {
		t.Fatal("COMMIT failure lost marker")
	}
	db = openDB(t, c.dir)
	for _, table := range []string{"workload_iam_policies", "jobs", "cron_schedules"} {
		var n int
		predicate := "quarantined=0"
		if table != "workload_iam_policies" {
			predicate = "authority_state='active'"
		}
		must(t, db.ReadDB().QueryRow(`SELECT count(*) FROM `+table+` WHERE `+predicate).Scan(&n))
		if n != 2 {
			t.Fatal("failed COMMIT partially changed authority")
		}
	}
	for _, table := range []string{"async_work_audit", "async_cron_audit"} {
		var n int
		must(t, db.ReadDB().QueryRow(`SELECT count(*) FROM `+table+` WHERE action='restore_quarantine'`).Scan(&n))
		if n != 0 {
			t.Fatal("failed COMMIT left work audit")
		}
	}
	_, err = db.WriteDB().Exec(`DROP TRIGGER fail_commit`)
	must(t, err)
	must(t, db.Close())
	db, r, err := start(t, c)
	must(t, err)
	defer db.Close()
	if r.Epoch != 1 || r.Operation != op {
		t.Fatal("commit retry changed operation")
	}
}

func TestRecoveryLinkedDBPathsDeny(t *testing.T) {
	for _, name := range []string{"orva.db", "orva.db-wal", "orva.db-shm"} {
		t.Run(name, func(t *testing.T) {
			c := coordinator(t, t.TempDir())
			must(t, os.Symlink(filepath.Join(t.TempDir(), "outside"), filepath.Join(c.dir, name)))
			called := false
			_, _, err := c.Start(t.Context(), func(context.Context, *database.Database) error { called = true; return nil },
				func(context.Context, *database.Database) error { called = true; return nil })
			if err == nil || called {
				t.Fatal("linked SQLite path accepted")
			}
		})
	}
}

func TestRecoveryProcessCrashHelper(t *testing.T) {
	if os.Getenv("RECOVERY_TEST_CHILD") != "1" {
		return
	}
	c, err := Open(os.Getenv("RECOVERY_TEST_DIR"), Host{Quiesce: func(context.Context) error { return nil }, AdmissionClosed: func() bool { return true }})
	must(t, err)
	c.fault = func(point string) error {
		if point == os.Getenv("RECOVERY_TEST_POINT") {
			os.Exit(23)
		}
		return nil
	}
	_, err = c.Restore(t.Context(), os.Stdin)
	must(t, err)
	db, _, err := start(t, c)
	must(t, err)
	db.Close()
	c.Close()
	t.Fatal("crash point not reached")
}

func TestRecoveryProcessCrashRestart(t *testing.T) {
	_, archive := seed(t, false)
	for _, point := range []string{"marker_staging", "staged", "marker_installing", "installed", "marker_ready", "committed", "before_remove", "removed", "removal_synced"} {
		t.Run(point, func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRecoveryProcessCrashHelper$")
			child.Env = []string{"RECOVERY_TEST_CHILD=1", "RECOVERY_TEST_DIR=" + dir, "RECOVERY_TEST_POINT=" + point}
			child.Stdin = bytes.NewReader(archive)
			output, err := child.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 23 {
				t.Fatalf("child did not crash at %s: %v (%d output bytes)", point, err, len(output))
			}
			c := coordinator(t, dir)
			m, err := c.readMarker()
			must(t, err)
			if m != nil && m.State != "ready" {
				op, e := c.Restore(t.Context(), bytes.NewReader(archive))
				must(t, e)
				if op != m.Operation {
					t.Fatal("crash retry changed operation")
				}
			}
			db, r, err := start(t, c)
			must(t, err)
			if r.Epoch != 1 {
				t.Fatal("process crash repeated epoch")
			}
			for _, table := range []string{"async_work_audit", "async_cron_audit"} {
				var n int
				must(t, db.ReadDB().QueryRow(`SELECT count(*) FROM `+table+` WHERE action='restore_quarantine'`).Scan(&n))
				if n != 2 {
					t.Fatal("process crash duplicated work audits")
				}
			}
			must(t, db.Close())
		})
	}
}
