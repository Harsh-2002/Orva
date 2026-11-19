package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/servingmode"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func enrollmentMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func enrollmentFixture(t *testing.T, wal bool) (string, string, string, string) {
	t.Helper()
	t.Setenv(servingmode.Env, servingmode.Legacy)
	dir := t.TempDir()
	lock, err := servingmode.AcquireLegacy(dir)
	enrollmentMust(t, err)
	enrollmentMust(t, lock.Close())
	db, err := database.New(filepath.Join(dir, "orva.db"))
	enrollmentMust(t, err)
	enrollmentMust(t, db.Migrate())
	if wal {
		t.Cleanup(func() { enrollmentMust(t, db.Close()) })
	} else {
		defer func() { enrollmentMust(t, db.Close()) }()
	}
	first, second, code := ids.New(), ids.New(), strings.Repeat("a", 64)
	for i, id := range []string{first, second} {
		entry := "handler.js"
		if i == 1 {
			entry = "handler.ts"
		}
		_, err := db.WriteDB().Exec(`INSERT INTO functions(id,name,runtime,entrypoint,status,version,code_hash,sdk)
			 VALUES(?,?,'node',?,'active',1,?,?)`, id, entry, entry, code, i == 1)
		enrollmentMust(t, err)
		deploy := ids.New()
		_, err = db.WriteDB().Exec(`INSERT INTO deployments(id,function_id,version,status) VALUES(?,?,1,'succeeded')`, deploy, id)
		enrollmentMust(t, err)
		_, err = db.WriteDB().Exec(`UPDATE functions SET active_deployment_id=? WHERE id=?`, deploy, id)
		enrollmentMust(t, err)
		source := filepath.Join(dir, "functions", id, "versions", code)
		enrollmentMust(t, os.MkdirAll(source, 0755))
		enrollmentMust(t, os.WriteFile(filepath.Join(source, entry), []byte("exports.handler = () => ({body:'ok'})\n"), 0644))
		if i == 1 {
			for _, marker := range []string{"package.json", "tsconfig.json"} {
				enrollmentMust(t, os.WriteFile(filepath.Join(source, marker), []byte("{}"), 0644))
			}
		}
	}
	for _, creator := range []any{first, nil} {
		_, err := db.WriteDB().Exec(`INSERT INTO jobs(id,function_id,payload,status,scheduled_at,enqueued_by_function_id)
			 VALUES(?,?,'{}','pending',CURRENT_TIMESTAMP,?)`, ids.New(), first, creator)
		enrollmentMust(t, err)
	}
	_, err = db.WriteDB().Exec(`INSERT INTO cron_schedules(id,function_id,cron_expr) VALUES(?,?,'* * * * *')`, ids.New(), first)
	enrollmentMust(t, err)
	return dir, first, second, code
}

func enrollmentTreeHash(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%q:%v:%d:%v\n", path, info.Mode(), info.Size(), info.ModTime())
		if !d.IsDir() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			h.Write(raw)
		}
		return nil
	})
	enrollmentMust(t, err)
	return fmt.Sprintf("%x", h.Sum(nil))
}

func TestManagedEnrollDryRun(t *testing.T) {
	dir, first, second, hash := enrollmentFixture(t, true)
	planFile := filepath.Join(t.TempDir(), "plan.json")
	before := enrollmentTreeHash(t, dir)
	legacy, err := servingmode.AcquireLegacy(dir)
	enrollmentMust(t, err)
	if err := planManagedEnrollment(t.Context(), dir, planFile, func([]byte) error { return nil }); err == nil {
		t.Fatal("dry run ignored legacy daemon lock")
	}
	enrollmentMust(t, legacy.Close())
	var buf bytes.Buffer
	cmd := newManagedCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"enroll", "--data-dir", dir, "--plan", planFile})
	enrollmentMust(t, cmd.ExecuteContext(t.Context()))
	raw, err := os.ReadFile(planFile)
	enrollmentMust(t, err)
	if !bytes.Equal(raw, buf.Bytes()) || before != enrollmentTreeHash(t, dir) {
		t.Fatal("dry run changed data directory or emitted different plans")
	}
	enrollmentMust(t, planManagedEnrollment(t.Context(), dir, "", func(next []byte) error {
		if !bytes.Equal(raw, next) {
			t.Fatal("unchanged data produced a different plan")
		}
		return nil
	}))
	var plan enrollmentPlan
	enrollmentMust(t, json.Unmarshal(raw, &plan))
	if plan.Work != (enrollmentWork{FunctionJobs: 1, UnknownJobs: 1, UnknownCrons: 1}) || !enrollmentDigest(plan.Database) || len(plan.Functions) != 2 {
		t.Fatal("unexpected legacy plan totals")
	}
	for _, f := range plan.Functions {
		if !enrollmentDigest(f.Revision) || f.CodeHash != hash {
			t.Fatal("missing revision identity")
		}
		switch f.ID {
		case first:
			if !f.Enrollable || f.SDK || f.TypeScript || len(f.Dependencies) != 0 {
				t.Fatalf("unexpected plain function report: %+v", f)
			}
		case second:
			if f.Enrollable || !f.SDK || !f.TypeScript || len(f.Dependencies) != 1 || !strings.Contains(f.Reason, "TypeScript") {
				t.Fatalf("unexpected TS function report: %+v", f)
			}
		default:
			t.Fatal("unexpected function")
		}
	}
	for _, planFile := range []string{filepath.Join(dir, "forbidden.json")} {
		if err := planManagedEnrollment(t.Context(), dir, planFile, func([]byte) error { return nil }); err == nil {
			t.Fatal("dry run accepted a plan inside the live tree")
		}
	}
	if before != enrollmentTreeHash(t, dir) {
		t.Fatal("refused dry run changed data directory")
	}
}
