package builder_test

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/mcp"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/server/handlers"
)

func TestQueueRetainsAcceptedConfig(t *testing.T) {
	for _, api := range []string{"REST", "MCP"} {
		for _, status := range []string{"active", "created"} {
			for _, outcome := range []string{"success", "inactive", "failure", "changed-entrypoint"} {
				t.Run(api+"/"+status+"/"+outcome, func(t *testing.T) {
					wantSuccess := outcome == "success" || outcome == "inactive"
					dir := t.TempDir()
					db, err := database.New(filepath.Join(dir, "orva.db"))
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = db.Close() })
					if err := db.Migrate(); err != nil {
						t.Fatal(err)
					}
					reg := registry.New(db)
					fn := &database.Function{
						ID: "019df200-7b00-7e00-9c00-aab1cd2e3f41", Name: "queued-config", Runtime: "node",
						Entrypoint: "handler.js", Status: status, SDK: true, NetworkMode: "none",
						MemoryMB: 64, CPUs: 1, TimeoutMS: 1000, CodeHash: strings.Repeat("a", 64),
					}
					if err := reg.Set(fn); err != nil {
						t.Fatal(err)
					}
					dep := &database.Deployment{
						ID: "019df200-7b00-7e00-9c00-aab1cd2e3f42", FunctionID: fn.ID, Version: 1, Status: "queued",
					}
					if err := db.InsertDeployment(dep); err != nil {
						t.Fatal(err)
					}
					archive := queueConfigArchive(t, dir)
					rootfs := filepath.Join(dir, "rootfs")
					if err := os.MkdirAll(filepath.Join(rootfs, "node/usr/local/bin"), 0o755); err != nil {
						t.Fatal(err)
					}
					for path, contents := range map[string]string{
						filepath.Join(rootfs, "node/usr/local/bin/node"): "unused",
						filepath.Join(dir, "syntax-check-stub"):          "#!/bin/sh\nexit 0\n",
					} {
						if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
							t.Fatal(err)
						}
					}
					entered, release := make(chan struct{}), make(chan struct{})
					var releaseOnce sync.Once
					unblock := func() { releaseOnce.Do(func() { close(release) }) }
					// Only syntax execution is stubbed; extraction, queue publication and API mutations are real.
					bld := &builder.Builder{
						DataDir: dir, RootfsDir: rootfs, NsjailBin: filepath.Join(dir, "syntax-check-stub"),
						BuildFunc: func(context.Context, string, string, string) (int64, error) {
							close(entered)
							<-release
							if outcome == "failure" {
								return 0, errors.New("gated build failure")
							}
							return 0, nil
						},
					}
					q := builder.NewQueue(bld, db, reg)
					var buildLock sync.Mutex
					q.FnLock = func(string) *sync.Mutex { return &buildLock }
					q.Start()
					t.Cleanup(func() {
						unblock()
						ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
						defer cancel()
						q.Shutdown(ctx)
					})
					done := make(chan bool, 1)
					if err := q.Submit(builder.BuildJob{
						DeploymentID: dep.ID, FunctionID: fn.ID, TarballPath: archive,
						OnComplete: func(_ string, ok bool) { done <- ok },
					}); err != nil {
						t.Fatal(err)
					}
					select {
					case <-entered:
					case ok := <-done:
						t.Fatalf("build exited before gate: %v", ok)
					case <-time.After(10 * time.Second):
						t.Fatal("build did not reach gate")
					}

					drains := 0
					h := &handlers.FunctionHandler{
						DB: db, Registry: reg, FnLock: q.FnLock, PoolDrain: func(string) { drains++ },
					}
					tools := mcp.BuildAgentRegistry(mcp.Deps{DB: db, Registry: reg}, map[string]bool{"write": true})
					update := func(body string) error {
						if api == "MCP" {
							args := fmt.Sprintf(`{"function_id":%q,%s`, fn.ID, body[1:])
							_, err := tools.Dispatch(context.Background(), "update_function", json.RawMessage(args))
							return err
						}
						rec := httptest.NewRecorder()
						h.Update(rec, httptest.NewRequest(http.MethodPut, "/api/v1/functions/"+fn.ID, strings.NewReader(body)))
						if rec.Code != http.StatusOK {
							return fmt.Errorf("update %d: %s", rec.Code, rec.Body.String())
						}
						return nil
					}
					updated := make(chan error, 1)
					go func() {
						updated <- update(`{"sdk":false,"env_vars":{"LATEST":"retained"},"auth_mode":"platform_key","network_mode":"egress","memory_mb":128}`)
					}()
					select {
					case err := <-updated:
						if err != nil {
							t.Fatal(err)
						}
					case <-time.After(5 * time.Second):
						unblock()
						<-updated
						t.Fatal("config update blocked behind build")
					}
					if api == "REST" && drains != 1 {
						t.Fatalf("accepted revocation did not drain: %d", drains)
					}
					accepted, err := db.GetFunction(fn.ID)
					if err != nil || accepted.SDK {
						t.Fatalf("revoke was not persisted: %+v (%v)", accepted, err)
					}
					if outcome == "changed-entrypoint" {
						if err := update(`{"entrypoint":"other.js"}`); err != nil {
							t.Fatal(err)
						}
					} else if outcome == "inactive" {
						if err := update(`{"status":"inactive"}`); err != nil {
							t.Fatal(err)
						}
					}
					unblock()
					select {
					case ok := <-done:
						if ok != wantSuccess {
							t.Errorf("build success=%v, outcome=%s", ok, outcome)
						}
					case <-time.After(10 * time.Second):
						t.Fatal("build did not finish")
					}
					// A failed job used to call OnComplete before its last stale config write.
					q.Shutdown(context.Background())
					stored, err := db.GetFunction(fn.ID)
					if err != nil {
						t.Fatal(err)
					}
					cached, err := reg.Get(fn.ID)
					if err != nil {
						t.Fatal(err)
					}
					for name, got := range map[string]*database.Function{"DB": stored, "registry": cached} {
						if got.SDK || got.EnvVars["LATEST"] != "retained" || got.AuthMode != "platform_key" || got.NetworkMode != "egress" || got.MemoryMB != 128 {
							t.Errorf("%s lost accepted config: %+v", name, got)
						}
						if !wantSuccess && (got.CodeHash != fn.CodeHash || got.ActiveDeploymentID != "") {
							t.Errorf("%s activated failed/obsolete build: %+v", name, got)
						}
						if outcome == "changed-entrypoint" && got.Entrypoint != "other.js" {
							t.Errorf("%s overwrote changed build input: %+v", name, got)
						}
						wantStatus := "active"
						if outcome == "inactive" {
							wantStatus = "inactive"
						} else if !wantSuccess && status == "created" {
							wantStatus = "error"
						}
						if got.Status != wantStatus {
							t.Errorf("%s status=%s, want %s", name, got.Status, wantStatus)
						}
					}
					finished, err := db.GetDeployment(dep.ID)
					if err != nil {
						t.Fatal(err)
					}
					if wantSuccess {
						if finished.Status != "succeeded" || finished.Snapshot == nil || !finished.Snapshot.Equal(database.SnapshotFromFunction(stored)) || finished.Snapshot.SDK {
							t.Errorf("deployment snapshot lost accepted config: %+v", finished)
						}
					} else if finished.Status != "failed" || finished.Snapshot != nil {
						t.Errorf("failed build published a snapshot: %+v", finished)
					}
					current := filepath.Join(dir, "functions", fn.ID, "current")
					if !wantSuccess {
						if _, err := os.Lstat(current); !errors.Is(err, os.ErrNotExist) {
							t.Errorf("obsolete/failed build activated current: %v", err)
						}
					} else if target, err := os.Readlink(current); err != nil || !strings.HasSuffix(target, stored.CodeHash) {
						t.Errorf("current=%s (%v), hash=%s", target, err, stored.CodeHash)
					}
				})
			}
		}
	}
}

func queueConfigArchive(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "source.tar.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	code := "exports.handler = () => ({statusCode:200, body:'test'});\n"
	if err := tw.WriteHeader(&tar.Header{Name: "handler.js", Mode: 0o644, Size: int64(len(code))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(code)); err != nil {
		t.Fatal(err)
	}
	for _, close := range []func() error{tw.Close, gz.Close, f.Close} {
		if err := close(); err != nil {
			t.Fatal(err)
		}
	}
	return path
}
