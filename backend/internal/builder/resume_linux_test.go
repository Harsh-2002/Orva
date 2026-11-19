//go:build linux

package builder

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/auth"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/secrets"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/backend/internal/workloadpolicy"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestPublicationQueueResume(t *testing.T) {
	for _, mode := range []string{"commit_failure_then_resume", "retired_before_verify", "retired_while_pinned"} {
		t.Run(mode, func(t *testing.T) {
			s, _, owner := publicationQueueFixture(t)
			db := s.queue.db
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			admin := ids.New()
			must(db.InsertAPIKey(&database.APIKey{ID: admin, Name: "admin", KeyHash: "resume-fixture",
				Prefix: "resume-fixture", Permissions: `["admin","read","write"]`}))
			writer := ids.New()
			must(db.InsertAPIKey(&database.APIKey{ID: writer, Name: "writer", KeyHash: "resume-writer",
				Prefix: "resume-writer", Permissions: `["write"]`}))
			writerCtx := auth.WithOperator(t.Context(), &auth.Principal{Kind: auth.KindAPIKey, ID: writer})
			ctx := auth.WithOperator(t.Context(), &auth.Principal{Kind: auth.KindAPIKey, ID: admin})
			ref, _ := auth.OperatorFromContext(ctx)
			must(db.InitWorkloadOperatorPauseSchema(ctx))
			store, _, limits := outputTestStore(t)
			s.code, s.runtime = store, store
			s.secrets = secrets.New(db, t.TempDir())
			seal := publicationFixtureSealer(t, store, limits)
			code := seal(artifactfs.KindCode, owner, nil, "handler.js", 0644)
			runtime := seal(artifactfs.KindRuntime, "node", []string{"usr", "usr/local", "usr/local/bin", "opt", "opt/orva"}, "usr/local/bin/node", 0755)
			paths, err := artifactfs.PreparePaths(store, code, store, runtime)
			must(err)
			c, r := paths.Identities()
			paths.Release()
			p, err := db.CaptureWorkloadDeployment(ctx, ref, owner)
			must(err)
			output := database.WorkloadBuildOutput{CodeGeneration: code, CodeDigest: c.Digest, RuntimeGeneration: runtime,
				RuntimeDigest: r.Digest, CodeHash: strings.Repeat("b", 64), Entrypoint: "handler.js"}
			must(db.RecordWorkloadBuildOutput(ctx, p.ID, output))
			published, err := db.PublishWorkloadDeployment(ctx, p.ID, output)
			must(err)
			e, err := published.Policy.AdmissionEvaluator()
			must(err)
			s.authority, err = workloadiam.NewAuthority(8, []*workloadiam.Evaluator{e})
			must(err)
			s.manager = pool.NewManager(pool.ManagerConfig{ManagedAuthority: s.authority}, pool.SandboxTemplate{}, db, s.queue.reg, nil)
			t.Cleanup(func() { must(s.manager.Shutdown(context.Background())) })
			policies, err := workloadpolicy.NewManaged(db, s.queue.reg, s.authority, func(id string) workloadpolicy.ArtifactState {
				current := s.manager.CurrentArtifact(id)
				return workloadpolicy.ArtifactState{OwnerID: id, Artifact: current, Known: current != ""}
			})
			must(err)
			old, err := s.Install(ctx, p.ID)
			must(err)
			oldFactory := s.manager.CurrentFactory(owner)
			body := []byte(`{"schema_version":1,"allow":[{"actions":["kv.get"],"resource":{"own_kv":{"key":"probe"}},"context":"execution"}],"inbound":[]}`)
			policy, err := policies.Replace(ctx, ref, owner, workloadpolicy.Expected{
				Revision: published.Policy.Revision, ApprovedArtifact: published.Policy.ApprovedArtifact, CurrentArtifact: old,
			}, body)
			must(err)
			review := func(artifact string) {
				t.Helper()
				policy, err = policies.Review(ctx, ref, owner, workloadpolicy.Expected{
					Revision: policy.Revision, ApprovedArtifact: policy.ApprovedArtifact, CurrentArtifact: artifact,
				})
				must(err)
			}
			review(old)
			standing := func() error {
				return s.authority.DoStandingObserved(workloadiam.AsyncOrigin{Kind: "operator", OperatorKind: "api_key",
					OperatorID: admin, TargetID: owner, Trigger: workloadiam.Job}, func() workloadiam.StandingState {
					return workloadiam.StandingState{TargetArtifact: s.manager.CurrentArtifact(owner)}
				}, func(workloadiam.AsyncRevisions) error { return nil })
			}
			kv := func() error {
				key := "probe"
				return s.authority.DoObserved(func() workloadiam.Context {
					return workloadiam.Context{Principal: workloadiam.Principal{FunctionID: owner, WorkerID: "fixture-worker",
						ArtifactRevision: s.manager.CurrentArtifact(owner)}, Phase: workloadiam.Execution, Ready: true,
						Lease: workloadiam.Lease{ID: "fixture-lease", WorkerID: "fixture-worker", ExecutionID: owner, Active: true}}
				}, workloadiam.Request{Operation: workloadiam.OpKVGet, FunctionID: owner,
					Resource: workloadiam.Resource{OwnKV: &workloadiam.KVResource{Key: &key}}}, func() error { return nil })
			}
			denied := func() {
				t.Helper()
				if err := standing(); !errors.Is(err, workloadiam.ErrDenied) {
					t.Fatalf("standing work not denied: %v", err)
				}
				if err := kv(); !errors.Is(err, workloadiam.ErrDenied) {
					t.Fatalf("protected operation not denied: %v", err)
				}
			}
			must(standing())
			must(kv())
			req := database.WorkloadPauseRequest{OwnerID: owner, ExpectedGeneration: published.Artifact.Generation,
				ExpectedPolicyRevision: policy.Revision, ExpectedAuthorityEpoch: policy.AuthorityEpoch}
			if out, err := s.ExecuteLifecycle(writerCtx, req, false); out != (LifecycleAcknowledgement{}) || !errors.Is(err, database.ErrOperatorDenied) {
				t.Fatalf("WRITE-only pause of a privileged owner: %+v, %v", out, err)
			}
			must(standing())
			must(kv())
			if out, err := s.ExecuteLifecycle(ctx, req, false); err != nil ||
				out != (LifecycleAcknowledgement{FunctionID: owner, FunctionStatus: "inactive", OperatorPaused: true}) {
				t.Fatalf("pause acknowledgement: %+v, %v", out, err)
			}
			inspected, err := db.InspectWorkloadConfiguration(ctx, ref, owner)
			must(err)
			if inspected.Expected.Generation != req.ExpectedGeneration || inspected.Expected.PolicyRevision != req.ExpectedPolicyRevision ||
				inspected.Expected.AuthorityEpoch <= req.ExpectedAuthorityEpoch {
				t.Fatalf("paused configuration CAS: %+v", inspected.Expected)
			}
			req.ExpectedAuthorityEpoch = inspected.Expected.AuthorityEpoch
			cached, err := s.queue.reg.Get(owner)
			must(err)
			before, err := db.GetWorkloadArtifactSnapshot(ctx, owner)
			must(err)
			assertPaused := func() {
				t.Helper()
				paused, err := db.WorkloadOperatorPaused(ctx, owner)
				must(err)
				after, err := db.GetWorkloadArtifactSnapshot(ctx, owner)
				must(err)
				fn, err := s.queue.reg.Get(owner)
				must(err)
				if !paused || !oldFactory.Retired() || s.manager.CurrentFactory(owner) != nil ||
					cached.Status != "inactive" || !reflect.DeepEqual(fn, cached) || !reflect.DeepEqual(after, before) {
					t.Fatal("failed resume changed pause, cache, durable state, or Factory")
				}
				must(s.authority.ChangeHostState(func() {
					if s.authority.PublicationRevisionUnderGate(owner) != req.ExpectedPolicyRevision {
						t.Error("failed resume changed the installed evaluator")
					}
				}))
				denied()
			}
			assertPaused()
			if mode == "retired_before_verify" {
				must(store.Retire(code))
				if err := s.Resume(ctx, req); !errors.Is(err, ErrResumePairUnavailable) {
					t.Fatalf("retired code did not deny resume: %v", err)
				}
				assertPaused()
				return
			}
			if mode == "retired_while_pinned" {
				lock := s.queue.reg.MutationLock(owner)
				lock.Lock()
				result := make(chan error, 1)
				go func() { result <- s.Resume(ctx, req) }()
				waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				ticker := time.NewTicker(time.Millisecond)
				defer ticker.Stop()
				waitErr := func() error {
					for {
						select {
						case <-waitCtx.Done():
							return waitCtx.Err()
						case <-ticker.C:
							// Reclaim cannot delete an active tree; ErrBusy shows a pin.
							c, r := store.Reclaim(code), store.Reclaim(runtime)
							if errors.Is(c, artifactfs.ErrBusy) && errors.Is(r, artifactfs.ErrBusy) {
								return nil
							}
						}
					}
				}()
				var retireErr, reclaimErr error
				if waitErr == nil {
					retireErr = store.Retire(code)
					reclaimErr = store.Reclaim(code)
				}
				lock.Unlock()
				resumeErr := <-result
				must(waitErr)
				must(retireErr)
				must(resumeErr)
				if !errors.Is(reclaimErr, artifactfs.ErrBusy) {
					t.Fatalf("held resume lease did not pin retired tree: %v", reclaimErr)
				}
				if _, err := s.Install(ctx, p.ID); !errors.Is(err, ErrPublicationUnavailable) {
					t.Fatalf("fresh install unexpectedly resolved the retired pair: %v", err)
				}
				if s.manager.CurrentFactory(owner) != nil {
					t.Fatal("unavailable pair produced a Factory")
				}
				denied()
				return
			}
			conn, err := sql.Open("sqlite", db.Path())
			must(err)
			defer conn.Close()
			_, err = conn.Exec(`CREATE TABLE resume_host_guard(owner TEXT REFERENCES functions(id) DEFERRABLE INITIALLY DEFERRED);
				CREATE TRIGGER reject_resume AFTER DELETE ON workload_operator_pause
				BEGIN INSERT INTO resume_host_guard VALUES('missing-owner'); END`)
			must(err)
			if err := s.Resume(ctx, req); !errors.Is(err, database.ErrWorkloadArtifactStorage) {
				t.Fatalf("failed COMMIT did not deny resume: %v", err)
			}
			assertPaused()
			_, err = conn.Exec(`DROP TRIGGER reject_resume`)
			must(err)
			if out, err := s.ExecuteLifecycle(writerCtx, req, true); out != (LifecycleAcknowledgement{}) || !errors.Is(err, database.ErrOperatorDenied) {
				t.Fatalf("WRITE-only resume: %+v, %v", out, err)
			}
			assertPaused()
			if out, err := s.ExecuteLifecycle(ctx, req, true); err != nil ||
				out != (LifecycleAcknowledgement{FunctionID: owner, FunctionStatus: "inactive", InstallationRequired: true, ReviewRequired: true}) {
				t.Fatalf("resume acknowledgement: %+v, %v", out, err)
			}
			next, err := db.GetWorkloadArtifactSnapshot(ctx, owner)
			must(err)
			policy, err = db.GetIAMPolicy(ctx, owner)
			must(err)
			fn, err := s.queue.reg.Get(owner)
			must(err)
			if next.Artifact.Generation == before.Artifact.Generation || fn.Status != "inactive" ||
				fn.ActiveDeploymentID != p.ID || !policy.ReviewRequired || policy.ApprovedArtifact != old ||
				s.manager.CurrentFactory(owner) != nil {
				t.Fatal("resume did not publish inactive exact successor")
			}
			denied()
			current, err := s.Install(ctx, p.ID)
			must(err)
			if current == old || s.manager.CurrentFactory(owner).Generation().Token != next.Artifact.Generation {
				t.Fatal("installation reused old authority")
			}
			denied()
			policy, err = policies.Replace(ctx, ref, owner, workloadpolicy.Expected{
				Revision: policy.Revision, ApprovedArtifact: policy.ApprovedArtifact, CurrentArtifact: current,
			}, body)
			must(err)
			if !policy.ReviewRequired || policy.ApprovedArtifact != current {
				t.Fatal("inline edit did not exercise current artifact match with review still required")
			}
			denied()
			review(current)
			must(standing())
			must(kv())
		})
	}
}
