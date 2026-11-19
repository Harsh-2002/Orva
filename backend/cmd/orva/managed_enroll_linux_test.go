package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/servingmode"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestManagedEnrollProjectsAuthoredFiles(t *testing.T) {
	for _, runtime := range []string{"node", "python"} {
		t.Run(runtime, func(t *testing.T) {
			data, scratch := t.TempDir(), t.TempDir()
			f := enrollmentFunction{ID: ids.New(), Runtime: runtime, CodeHash: strings.Repeat("b", 64), deployment: ids.New(), status: "active"}
			dir := filepath.Join(data, "functions", f.ID)
			version := filepath.Join(dir, "versions", f.CodeHash)
			write := func(name, body string) {
				p := filepath.Join(version, name)
				enrollmentMust(t, os.MkdirAll(filepath.Dir(p), 0755))
				enrollmentMust(t, os.WriteFile(p, []byte(body), 0644))
			}
			var want []string
			if runtime == "node" {
				f.Entrypoint, f.runEntrypoint = "handler.ts", "out/handler.js"
				write("package.json", `{"devDependencies":{"typescript":"5.9.3"}}`)
				write("tsconfig.json", `{"compilerOptions":{"outDir":"out"}}`)
				write("out/handler.js", "compiled")
				write("node_modules/typescript/bin/tsc", "installed")
				enrollmentMust(t, os.MkdirAll(filepath.Join(version, "node_modules/.bin"), 0755))
				enrollmentMust(t, os.Symlink("../typescript/bin/tsc", filepath.Join(version, "node_modules/.bin/tsc")))
				want = []string{"handler.ts", "package.json", "tsconfig.json"}
			} else {
				f.Entrypoint = "handler.py"
				write("requirements.txt", "example==1.0")
				write("example/__init__.py", "installed")
				digest := sha256.Sum256([]byte("installed"))
				write("example-1.0.dist-info/RECORD", "example/__init__.py,sha256="+base64.RawURLEncoding.EncodeToString(digest[:])+
					",9\nexample-1.0.dist-info/RECORD,,\n")
				want = []string{"handler.py", "requirements.txt"}
			}
			write(f.Entrypoint, "source")
			write(".orva-ready", f.CodeHash)
			enrollmentMust(t, os.Symlink(filepath.Join("versions", f.CodeHash), filepath.Join(dir, "current")))
			enrollmentMust(t, f.inspect(t.Context(), data, scratch))
			if !f.Enrollable {
				t.Fatal(f.Reason)
			}
			archive, err := os.Open(f.archive(scratch))
			enrollmentMust(t, err)
			defer archive.Close()
			gz, err := gzip.NewReader(archive)
			enrollmentMust(t, err)
			defer gz.Close()
			tr := tar.NewReader(gz)
			var got []string
			for {
				h, err := tr.Next()
				if err == io.EOF {
					break
				}
				enrollmentMust(t, err)
				got = append(got, h.Name)
			}
			if strings.Join(got, ",") != strings.Join(want, ",") {
				t.Fatalf("archive paths = %v, want %v", got, want)
			}
		})
	}
}

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
		enrollmentMust(t, os.Symlink(filepath.Join("versions", code), filepath.Join(dir, "functions", id, "current")))
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
		if info.Mode()&os.ModeSymlink != 0 {
			raw, err := os.Readlink(path)
			if err != nil {
				return err
			}
			fmt.Fprintln(h, raw)
		} else if !d.IsDir() {
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
			if f.Enrollable || !f.SDK || !f.TypeScript || len(f.Dependencies) != 1 || !strings.Contains(f.Reason, "typescript not in package.json") {
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

func TestManagedEnrollApplyGate(t *testing.T) {
	dir, first, excluded, hash := enrollmentFixture(t, false)
	path := filepath.Join(t.TempDir(), "plan.json")
	enrollmentMust(t, planManagedEnrollment(t.Context(), dir, path, func([]byte) error { return nil }))
	assertGate := func(exclude []string, want string) {
		t.Helper()
		before := enrollmentTreeHash(t, dir)
		err := withEnrollmentPlan(t.Context(), dir, func(_ string, _ *servingmode.Lock, _ string, p *enrollmentPlan) error {
			skipped, err := validateEnrollmentApply(path, p, exclude)
			if err == nil && !skipped[excluded] {
				t.Fatal("explicit exclusion was lost")
			}
			return err
		})
		if (want == "" && err != nil) || (want != "" && (err == nil || !strings.Contains(err.Error(), want))) {
			t.Fatalf("gate error = %v, want %q", err, want)
		}
		if before != enrollmentTreeHash(t, dir) {
			t.Fatal("apply gate changed legacy data directory")
		}
	}
	assertGate(nil, "explicit --exclude")
	assertGate([]string{ids.New()}, "known function UUID")
	assertGate([]string{excluded, excluded}, "at most once")
	assertGate([]string{excluded}, "")
	cmd := newManagedCmd()
	cmd.SetArgs([]string{"enroll", "--data-dir", dir, "--apply", "--plan", path})
	if err := cmd.ExecuteContext(t.Context()); err == nil || !strings.Contains(err.Error(), "--confirm") {
		t.Fatalf("missing confirmation: %v", err)
	}
	db, err := database.New(filepath.Join(dir, "orva.db"))
	enrollmentMust(t, err)
	_, err = db.WriteDB().Exec(`UPDATE functions SET description='changed' WHERE id=?`, first)
	enrollmentMust(t, err)
	enrollmentMust(t, db.Close())
	assertGate([]string{excluded}, "plan differs")
	path = filepath.Join(t.TempDir(), "source-plan.json")
	enrollmentMust(t, planManagedEnrollment(t.Context(), dir, path, func([]byte) error { return nil }))
	source := filepath.Join(dir, "functions", first, "versions", hash, "handler.js")
	enrollmentMust(t, os.WriteFile(source, []byte("exports.handler = () => ({body:'changed'})\n"), 0644))
	assertGate([]string{excluded}, "plan differs")
}

func TestManagedEnrollDatabaseAtomic(t *testing.T) {
	dir, first, second, hash := enrollmentFixture(t, false)
	db, err := database.New(filepath.Join(dir, "orva.db"))
	enrollmentMust(t, err)
	defer func() { enrollmentMust(t, db.Close()) }()
	f, err := db.GetFunction(first)
	enrollmentMust(t, err)
	other, err := db.GetFunction(second)
	enrollmentMust(t, err)
	gen, digest := ids.New(), strings.Repeat("b", 64)
	plan := database.LegacyEnrollment{
		Owner: ids.New(), Operation: ids.New(), PlanDigest: digest, Runtimes: map[string]string{"node": gen},
		Functions: []database.LegacyEnrollmentFunction{
			{ID: first, Generation: ids.New(), LegacyHash: hash, Revision: digest,
				DeploymentID: f.ActiveDeploymentID, ApprovedArtifact: digest,
				Build: database.WorkloadBuildOutput{CodeGeneration: ids.New(), CodeDigest: digest,
					RuntimeGeneration: gen, RuntimeDigest: digest, CodeHash: hash, Entrypoint: f.Entrypoint}},
			{ID: second, Generation: ids.New(), LegacyHash: hash, Revision: digest, LegacySDK: false, Excluded: true},
		},
	}
	if err := db.CommitLegacyEnrollment(t.Context(), plan); err == nil {
		t.Fatal("late row conflict committed")
	}
	fn, err := db.GetFunction(first)
	enrollmentMust(t, err)
	if fn.SDK {
		t.Fatal("earlier mutation survived rollback")
	}
	var tables int
	enrollmentMust(t, db.ReadDB().QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name GLOB 'workload_*'`).Scan(&tables))
	if tables != 0 {
		t.Fatal("partial managed schema survived rollback")
	}
	plan.Functions[1].LegacySDK = true
	enrollmentMust(t, db.CommitLegacyEnrollment(t.Context(), plan))
	enrollmentMust(t, database.RequireManagedOwnerBinding(t.Context(), filepath.Join(dir, "orva.db"), plan.Owner))
	fn, err = db.GetFunction(first)
	enrollmentMust(t, err)
	if !fn.SDK || fn.Status != "active" {
		t.Fatal("enrolled current deployment was not configured for managed transport")
	}
	fn, err = db.GetFunction(second)
	enrollmentMust(t, err)
	if fn.Status != "inactive" || fn.ActiveDeploymentID != other.ActiveDeploymentID {
		t.Fatal("excluded deployment was lost or left serveable")
	}
	for id, sdk := range map[string]bool{first: false, second: true} {
		policy, err := db.GetIAMPolicy(t.Context(), id)
		enrollmentMust(t, err)
		if policy.PolicyJSON != database.RecoveryEmptyPolicy || policy.ReviewRequired != sdk || policy.Quarantined != sdk {
			t.Fatalf("unexpected policy state for legacy sdk=%t", sdk)
		}
		audit, err := db.ListIAMPolicyAudit(t.Context(), id)
		enrollmentMust(t, err)
		if len(audit) != 1 || audit[0].Actor.Kind != "system" || audit[0].Actor.ID != plan.Operation {
			t.Fatal("missing enrollment audit")
		}
	}
	for query, want := range map[string]int{
		`SELECT count(*) FROM jobs WHERE origin_kind='function' AND authority_state='suspended'`:                   1,
		`SELECT count(*) FROM jobs WHERE origin_kind='legacy_unknown' AND authority_state='quarantined'`:           1,
		`SELECT count(*) FROM cron_schedules WHERE origin_kind='legacy_unknown' AND authority_state='quarantined'`: 1,
		`SELECT count(*) FROM workload_enrollment_functions WHERE excluded=1 AND note LIKE '%review required%'`:    1,
		`SELECT count(*) FROM workload_deployment_capture`:                                                         1,
	} {
		var n int
		enrollmentMust(t, db.ReadDB().QueryRow(query).Scan(&n))
		if n != want {
			t.Fatalf("%s: got %d want %d", query, n, want)
		}
	}
	runtimes, err := db.LegacyEnrollmentRuntimes(t.Context())
	enrollmentMust(t, err)
	if len(runtimes) != 1 || runtimes["node"] != gen {
		t.Fatal("missing enrolled runtime")
	}
}

func TestManagedEnrollFaultAfterIntent(t *testing.T) {
	dir, first, second, hash := enrollmentFixture(t, false)
	err := withEnrollmentPlan(t.Context(), dir, func(root string, lock *servingmode.Lock, scratch string, p *enrollmentPlan) error {
		return applyManagedEnrollment(t.Context(), root, lock, scratch, p, map[string]bool{first: true, second: true}, func(stage string) error {
			if stage != "intent" {
				t.Fatalf("unexpected stage %s", stage)
			}
			return fmt.Errorf("injected after committed intent")
		})
	})
	if err == nil || !strings.Contains(err.Error(), "injected after committed intent") {
		t.Fatalf("apply error = %v", err)
	}
	if fd, err := servingmode.AcquireLegacy(dir); err == nil {
		fd.Close()
		t.Fatal("legacy reopened an interrupted apply")
	}
	managed, err := servingmode.Acquire(dir)
	enrollmentMust(t, err)
	defer managed.Close()
	if _, fd, err := managed.OpenData(); err == nil {
		fd.Close()
		t.Fatal("managed opened an interrupted apply")
	}
	for _, path := range []string{"orva.db", filepath.Join("functions", first, "versions", hash, "handler.js")} {
		if st, err := os.Stat(filepath.Join(dir, path)); err != nil || !st.Mode().IsRegular() {
			t.Fatalf("preserved %s: %v", path, err)
		}
	}
}
