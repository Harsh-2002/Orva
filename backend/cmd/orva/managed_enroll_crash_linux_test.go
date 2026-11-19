package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/servingmode"
)

// Two child re-execs make these real process deaths rather than injected errors:
// one runs the offline apply and dies at a named boundary, the other runs the
// real serve refusal path so the observed exit code is the operator-visible one.
// Same pattern as artifactfs.TestOwnerPreparedCrashHelper.
const (
	enrollKillStageEnv   = "ORVA_ENROLL_KILL_STAGE"
	enrollKillDataEnv    = "ORVA_ENROLL_KILL_DATA_DIR"
	enrollKillPlanEnv    = "ORVA_ENROLL_KILL_PLAN"
	enrollKillExcludeEnv = "ORVA_ENROLL_KILL_EXCLUDE"
	enrollKillExit       = 97
	enrollServeChildEnv  = "ORVA_ENROLL_SERVE_CHILD"
	enrollRuntimeEnv     = "ORVA_ENROLL_VM_ROOTFS"
)

func TestManagedEnrollCrashHelper(t *testing.T) {
	dir, child := os.LookupEnv(enrollKillDataEnv)
	if !child {
		return
	}
	os.Exit(enrollKillChild(dir, os.Getenv(enrollKillStageEnv),
		os.Getenv(enrollKillPlanEnv), os.Getenv(enrollKillExcludeEnv)))
}

func TestManagedEnrollServeExitHelper(t *testing.T) {
	if os.Getenv(enrollServeChildEnv) != "1" {
		return
	}
	os.Exit(runServe(newServeCmd(), nil))
}

// enrollKillChild runs the same apply body the enroll command runs and exits at
// the named durable boundary. os.Exit is deliberate: the lock close, the scratch
// cleanup and the deferred dispose of the build owner must not run.
func enrollKillChild(dir, stage, planPath, exclude string) int {
	var excluded []string
	if exclude != "" {
		excluded = strings.Split(exclude, ",")
	}
	ctx := context.Background()
	err := withEnrollmentPlan(ctx, dir, func(root string, lock *servingmode.Lock, scratch string, p *enrollmentPlan) error {
		skipped, err := validateEnrollmentApply(planPath, p, excluded)
		if err != nil {
			return err
		}
		return applyManagedEnrollment(ctx, root, lock, scratch, p, skipped, func(s string) error {
			if s == stage {
				os.Exit(enrollKillExit)
			}
			return nil
		})
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "enrollment kill child: %v\n", err)
		return 1
	}
	return 0
}

// enrollmentKillFixture is a stopped legacy instance with the keys an operator
// would already have on disk.
func enrollmentKillFixture(t *testing.T) (string, []string) {
	t.Helper()
	dir, first, second, _ := enrollmentFixture(t, false)
	for _, name := range []string{".master.key", ".admin-key"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("legacy"+name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, []string{first, second}
}

// enrollmentPayload fingerprints the legacy source tree and key files by
// content, mode and symlink target — never mtime, so a rename-free interruption
// cannot present itself as a change.
func enrollmentPayload(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(filepath.Join(dir, "functions"), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%q:%v:%d\n", path, info.Mode(), info.Size())
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			fmt.Fprintln(h, target)
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
	for _, name := range []string{".master.key", ".admin-key"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		enrollmentMust(t, err)
		fmt.Fprintf(h, "%q:%s\n", name, raw)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// enrollmentLegacyRows reads the stopped legacy database without writing it.
// Identity and code hash only: the commit boundary legitimately rewrites status.
func enrollmentLegacyRows(t *testing.T, dir string) (rows []string, digest string) {
	t.Helper()
	path := filepath.Join(dir, "orva.db")
	raw, err := os.ReadFile(path)
	enrollmentMust(t, err)
	conn, err := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	enrollmentMust(t, err)
	defer conn.Close()
	cur, err := conn.Query(`SELECT id, COALESCE(code_hash,'') FROM functions ORDER BY id`)
	enrollmentMust(t, err)
	defer cur.Close()
	for cur.Next() {
		var id, hash string
		enrollmentMust(t, cur.Scan(&id, &hash))
		rows = append(rows, id+":"+hash)
	}
	enrollmentMust(t, cur.Err())
	return rows, fmt.Sprintf("%x", sha256.Sum256(raw))
}

// enrollmentKernelBudget returns the cgroup the launch wrapper must enter: owner
// capture refuses a process that is not already inside the delegated budget, so
// a run that forgets the wrapper fails loudly instead of passing against
// rlimit-only limits.
func enrollmentKernelBudget(t *testing.T) string {
	t.Helper()
	budget := os.Getenv("ORVA_MANAGED_BOOT_BUDGET")
	if os.Getenv("ORVA_OWNER_KERNEL_TEST") != "disposable-vm" || budget == "" {
		t.Skip("requires ORVA_OWNER_KERNEL_TEST=disposable-vm and ORVA_MANAGED_BOOT_BUDGET from an owned disposable VM")
	}
	return budget
}

// enrollmentStubNsjail accepts the only path the product looks at. A stub is
// sufficient: the boundary this gates precedes every build spawn.
func enrollmentStubNsjail(t *testing.T) {
	t.Helper()
	st, err := os.Stat("/usr/local/bin/nsjail")
	if err != nil || !st.Mode().IsRegular() || st.Mode()&0111 == 0 {
		t.Skip("requires an executable at /usr/local/bin/nsjail (a stub is enough: the kill precedes any build)")
	}
}

// enrollmentFakeRuntime is a syntactically valid Node runtime tree: enough for
// preflight, sealing and the post-seal check, and never executed.
func enrollmentFakeRuntime(t *testing.T, dir string) {
	t.Helper()
	root := filepath.Join(dir, "rootfs", "node")
	files := map[string]struct {
		mode    fs.FileMode
		content string
	}{
		filepath.Join(root, "usr", "local", "bin", "node"): {0755, "#!/bin/sh\nexit 1\n"},
		filepath.Join(root, "opt", "orva", "adapter.js"):   {0644, "// orva-sdk-proto:1\n"},
	}
	for path, f := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(f.content), f.mode); err != nil {
			t.Fatal(err)
		}
	}
}

// enrollmentProvisionedRuntime stages a runtime tree that can actually build, so
// the per-function seal boundary is reachable rather than merely declared.
func enrollmentProvisionedRuntime(t *testing.T, dir string) {
	t.Helper()
	source := os.Getenv(enrollRuntimeEnv)
	if source == "" {
		t.Skip("requires " + enrollRuntimeEnv + " naming a provisioned Node runtime tree; a function build needs a working interpreter")
	}
	enrollmentStubNsjail(t)
	if err := os.CopyFS(filepath.Join(dir, "rootfs", "node"), os.DirFS(source)); err != nil {
		t.Fatalf("stage provisioned runtime: %v", err)
	}
}

// TestManagedEnrollProcessKill stops the real offline apply at each
// artifact-production boundary, then asks the real server process whether the
// interrupted layout is serveable. Nothing may be deleted, no serving mode may
// be published early, and the exit code must be the layout refusal.
func TestManagedEnrollProcessKill(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		// stage is the boundary the child dies at; "" is the completion control.
		stage string
		// gate is the environment a case needs: "" host, "runtime" a sealed
		// runtime, "provisioned" a runtime that can build. All of them are
		// kernel-gated because owner capture precedes every boundary here.
		gate string
		// excludeAll leaves every function inactive, which keeps the boundary
		// reachable with no interpreter and no build at all.
		excludeAll bool
		// dbWrites reports that the boundary itself legitimately commits to the
		// legacy database.
		dbWrites bool
	}{
		{name: "intent", stage: "intent", excludeAll: true},
		{name: "owner_provisioned", stage: "owner_provisioned", gate: "kernel", excludeAll: true},
		{name: "runtime_sealed", stage: "runtime_sealed", gate: "runtime"},
		{name: "database_committed", stage: "database_committed", gate: "kernel", excludeAll: true, dbWrites: true},
		{name: "code_sealed", stage: "code_sealed", gate: "provisioned"},
		{name: "complete", stage: "", gate: "kernel", excludeAll: true, dbWrites: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, ids := enrollmentKillFixture(t)
			excluded := ids[1:] // the TypeScript fixture can only be excluded
			if tc.excludeAll {
				excluded = ids
			}
			planPath := filepath.Join(t.TempDir(), "plan.json")
			enrollmentMust(t, planManagedEnrollment(t.Context(), dir, planPath, func([]byte) error { return nil }))

			env := append(os.Environ(),
				enrollKillDataEnv+"="+dir, enrollKillPlanEnv+"="+planPath,
				enrollKillExcludeEnv+"="+strings.Join(excluded, ","), enrollKillStageEnv+"="+tc.stage,
				"TMPDIR="+t.TempDir())
			launch := []string{bin, "-test.run=^TestManagedEnrollCrashHelper$", "-test.timeout=4m"}
			if tc.gate != "" {
				budget := enrollmentKernelBudget(t)
				witness := filepath.Join(t.TempDir(), "witness")
				enrollmentMust(t, artifactfs.ProvisionOwnerWitness(witness, managedBackupRoots(dir)))
				env = append(env,
					"ORVA_MANAGED_BOOT_BUDGET="+budget,
					managedParentEnv+"="+filepath.Join(budget, "orva.workers"),
					managedWitnessEnv+"="+witness)
				// Owner capture requires the process to be inside the delegated
				// budget, and the external launcher — not the daemon — moves it.
				launch = append([]string{"/bin/sh", "-ec",
					`printf '%s' "$$" > "$1/cgroup.procs"; shift; exec "$@"`, "sh", budget}, launch...)
			}
			switch tc.gate {
			case "runtime":
				enrollmentStubNsjail(t)
				enrollmentFakeRuntime(t, dir)
			case "provisioned":
				enrollmentProvisionedRuntime(t, dir)
			}

			var payload, dbDigest string
			var functions []string
			if tc.stage != "" {
				payload = enrollmentPayload(t, dir)
				functions, dbDigest = enrollmentLegacyRows(t, dir)
			}

			cmd := exec.Command(launch[0], launch[1:]...)
			cmd.Env, cmd.Dir = env, t.TempDir()
			out, err := cmd.CombinedOutput()
			var status *exec.ExitError
			if tc.stage == "" {
				if err != nil {
					t.Fatalf("uninterrupted apply failed: %v\n%s", err, out)
				}
			} else if !errors.As(err, &status) || status.ExitCode() != enrollKillExit {
				t.Fatalf("apply did not stop at %q: %v\n%s", tc.stage, err, out)
			}

			if _, err := os.Lstat(filepath.Join(dir, servingmode.MarkerName)); (err == nil) != (tc.stage == "") {
				t.Fatalf("serving mode published=%t at %q", err == nil, tc.stage)
			}
			if tc.stage == "" {
				// Completion control: the apply must end in a layout the managed
				// path serves, otherwise the boundary cases prove nothing.
				t.Setenv(servingmode.Env, servingmode.Managed)
				lock, err := servingmode.Acquire(dir)
				enrollmentMust(t, err)
				defer lock.Close()
				record, root, err := lock.OpenData()
				if err != nil || record.Mode != servingmode.Managed || record.Owner == "" {
					t.Fatalf("published layout is not serveable: %+v %v", record, err)
				}
				root.Close()
				return
			}

			if enrollmentPayload(t, dir) != payload {
				t.Fatal("interrupted apply changed the legacy source tree or keys")
			}
			// The literal mirrors servingmode's unexported enrollIntent: every case
			// here stops before publication, so the intent is the guard that must
			// still be on disk.
			if _, err := os.Lstat(filepath.Join(dir, ".orva-enrollment-intent")); err != nil {
				t.Fatalf("enrollment intent missing at %q: %v", tc.stage, err)
			}
			afterFunctions, afterDigest := enrollmentLegacyRows(t, dir)
			if strings.Join(afterFunctions, "\n") != strings.Join(functions, "\n") {
				t.Fatal("interrupted apply lost or rewrote legacy function identities")
			}
			if !tc.dbWrites && afterDigest != dbDigest {
				t.Fatal("interrupted apply wrote the legacy database before its commit boundary")
			}

			// Both serving paths must refuse. runServe takes the legacy path while
			// no marker exists, and the intent — or, once it is cleared, the orva.db
			// tombstone — closes it. The managed path shares this Acquire/OpenData
			// gate, so one process-level check plus one library check covers both.
			serve := exec.Command(bin, "-test.run=^TestManagedEnrollServeExitHelper$", "-test.timeout=2m")
			serve.Env = append(append([]string{}, env...), enrollServeChildEnv+"=1", "ORVA_DATA_DIR="+dir)
			serve.Dir = t.TempDir()
			serveOut, serveErr := serve.CombinedOutput()
			if !errors.As(serveErr, &status) || status.ExitCode() != exitConfig {
				t.Fatalf("serve exited %v, want %d\n%s", serveErr, exitConfig, serveOut)
			}
			if !strings.Contains(string(serveOut), "refusing legacy startup") {
				t.Fatalf("serve did not report a layout refusal:\n%s", serveOut)
			}
			lock, err := servingmode.Acquire(dir)
			enrollmentMust(t, err)
			defer lock.Close()
			if record, root, err := lock.OpenData(); err == nil {
				root.Close()
				t.Fatalf("managed serving opened an interrupted apply: %+v", record)
			}
		})
	}
}
