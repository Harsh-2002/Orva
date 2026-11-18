package database

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func pausedResumeFixture(t *testing.T, installed bool) (*Database, OperatorCredentialReference, WorkloadPauseRequest, WorkloadBuildOutput) {
	t.Helper()
	ctx := t.Context()
	db, ref, owner := pauseFixture(t, "api_key")
	deployment := captureDeployment(t, db, ref, owner)
	output := deploymentOutput()
	if err := db.RecordWorkloadBuildOutput(ctx, deployment.ID, output); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PublishWorkloadDeployment(ctx, deployment.ID, output); err != nil {
		t.Fatal(err)
	}
	if installed {
		plan, err := db.PrepareWorkloadDeploymentInstallation(ctx, deployment.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.ActivateWorkloadDeploymentInstallation(ctx, ref, plan); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := db.PauseWorkloadFunctionAsOperator(ctx, ref, pauseRequest(t, db, owner)); err != nil {
		t.Fatal(err)
	}
	return db, ref, pauseRequest(t, db, owner), output
}

func resumePlanIsReadOnly(t *testing.T, db *Database, owner string) {
	t.Helper()
	ctx := context.Background()
	read := func() (WorkloadOperatorPause, IAMSourceSnapshot, []*Deployment) {
		t.Helper()
		deployments, err := db.ListDeploymentsForFunction(owner, 10)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.read.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		pause, present, err := workloadPauseRecordTx(ctx, tx, owner)
		if err != nil || !present {
			t.Fatalf("expected durable pause: %v", err)
		}
		sources, err := readSources(ctx, tx, owner)
		if err != nil {
			t.Fatal(err)
		}
		return *pause, sources, deployments
	}
	before := artifactTestSnapshot(t, db, owner)
	policy, audit := environmentPublishPolicy(t, db)
	pause, sources, deployments := read()
	changes := func() int64 {
		t.Helper()
		var n int64
		if err := db.write.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	beforeChanges := changes()
	t.Cleanup(func() {
		if changes() != beforeChanges {
			t.Error("resume planning issued a write")
		}
		artifactTestUnchanged(t, db, before)
		iamTestUnchanged(t, db, policy, audit)
		afterPause, afterSources, afterDeployments := read()
		if !reflect.DeepEqual(pause, afterPause) || !reflect.DeepEqual(sources, afterSources) ||
			!reflect.DeepEqual(deployments, afterDeployments) {
			t.Error("resume planning changed pause, sources, or deployment history")
		}
	})
}

func TestWorkloadOperatorResumePlan(t *testing.T) {
	for _, stage := range []string{"review_required", "installed"} {
		t.Run(stage, func(t *testing.T) {
			db, ref, req, output := pausedResumeFixture(t, stage == "installed")
			resumePlanIsReadOnly(t, db, req.OwnerID)
			snapshot := artifactTestSnapshot(t, db, req.OwnerID)
			plan, err := db.PrepareWorkloadResume(t.Context(), ref, req)
			if err != nil {
				t.Fatal(err)
			}
			if plan.pause.OwnerID != req.OwnerID || plan.pause.ArtifactGeneration != req.ExpectedGeneration ||
				plan.pause.PolicyRevision != req.ExpectedPolicyRevision || plan.pause.AuthorityEpoch != req.ExpectedAuthorityEpoch ||
				plan.artifact != snapshot.Artifact || plan.policy.Revision != req.ExpectedPolicyRevision ||
				plan.policy.AuthorityEpoch != req.ExpectedAuthorityEpoch || plan.deploymentID != snapshot.Function.ActiveDeploymentID ||
				plan.Output() != output || plan.snapshotDigest != deploymentSnapshotDigest(snapshot) || plan.sourceDigest == "" {
				t.Fatal("plan did not bind the observed pause, config, authority and sealed output")
			}
			if _, err := db.PrepareWorkloadDeploymentInstallation(t.Context(), plan.deploymentID); !errors.Is(err, ErrWorkloadArtifactConflict) {
				t.Fatalf("plan unblocked installation: %v", err)
			}
		})
	}
}

func TestWorkloadOperatorResumePlanStaleOrUnauthorized(t *testing.T) {
	db, ref, current, _ := pausedResumeFixture(t, false)
	for _, field := range []string{"generation", "policy_revision", "authority_epoch"} {
		t.Run(field, func(t *testing.T) {
			resumePlanIsReadOnly(t, db, current.OwnerID)
			req := current
			switch field {
			case "generation":
				req.ExpectedGeneration = ids.New()
			case "policy_revision":
				req.ExpectedPolicyRevision--
			case "authority_epoch":
				req.ExpectedAuthorityEpoch--
			}
			if plan, err := db.PrepareWorkloadResume(t.Context(), ref, req); plan != nil || !errors.Is(err, ErrWorkloadArtifactConflict) {
				t.Fatalf("stale %s accepted: %v", field, err)
			}
		})
	}
	t.Run("write_only", func(t *testing.T) {
		operatorExec(t, db, `UPDATE api_keys SET permissions='["write"]' WHERE id=?`, ref.ID)
		resumePlanIsReadOnly(t, db, current.OwnerID)
		if plan, err := db.PrepareWorkloadResume(t.Context(), ref, current); plan != nil || !errors.Is(err, ErrOperatorDenied) {
			t.Fatalf("current WRITE accepted: %v", err)
		}
	})
}

func TestWorkloadOperatorResumePlanBindsCurrentAuthorityAfterPause(t *testing.T) {
	db, ref, atPause, _ := pausedResumeFixture(t, true)
	operatorExec(t, db, `UPDATE workload_iam_policies SET revision=revision+1,
		authority_epoch=authority_epoch+1,quarantined=1 WHERE function_id=?`, atPause.OwnerID)
	current := pauseRequest(t, db, atPause.OwnerID)
	resumePlanIsReadOnly(t, db, current.OwnerID)

	if plan, err := db.PrepareWorkloadResume(t.Context(), ref, atPause); plan != nil || !errors.Is(err, ErrWorkloadArtifactConflict) {
		t.Fatalf("pause-time authority passed as current: %v", err)
	}
	plan, err := db.PrepareWorkloadResume(t.Context(), ref, current)
	if err != nil {
		t.Fatal(err)
	}
	if plan.pause.PolicyRevision != atPause.ExpectedPolicyRevision || plan.pause.AuthorityEpoch != atPause.ExpectedAuthorityEpoch ||
		plan.policy.Revision != current.ExpectedPolicyRevision || plan.policy.AuthorityEpoch != current.ExpectedAuthorityEpoch {
		t.Fatal("plan conflated pause-time intent with current authority")
	}
}

func TestWorkloadOperatorResumePlanRequiresPause(t *testing.T) {
	for _, mode := range []string{"unprovisioned", "no_pause", "missing_table"} {
		t.Run(mode, func(t *testing.T) {
			db, ref, owner := deploymentFixture(t, "api_key")
			publishedDeployment(t, db, ref, owner)
			req := pauseRequest(t, db, owner)
			want := ErrWorkloadPauseUnavailable
			if mode != "unprovisioned" {
				if err := db.InitWorkloadOperatorPauseSchema(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "no_pause":
				want = ErrWorkloadArtifactConflict
			case "missing_table":
				operatorExec(t, db, `DROP TABLE workload_operator_pause`)
			}
			if plan, err := db.PrepareWorkloadResume(t.Context(), ref, req); plan != nil || !errors.Is(err, want) {
				t.Fatalf("resume plan without explicit durable pause: %v", err)
			}
		})
	}
}

func TestWorkloadOperatorResumePlanRejectsUnavailableAssociation(t *testing.T) {
	for _, tc := range []struct {
		name, query string
	}{
		{"no_active_deployment", `UPDATE functions SET active_deployment_id='' WHERE id=?`},
		{"missing_capture", `DELETE FROM workload_deployment_capture WHERE function_id=?`},
		{"stale_generation", `UPDATE workload_deployment_capture SET published_generation='11111111-1111-4111-8111-111111111111' WHERE function_id=?`},
		{"not_succeeded", `UPDATE deployments SET status='failed' WHERE function_id=?`},
		{"stale_code_hash", `UPDATE deployments SET code_hash='' WHERE function_id=?`},
		{"stale_entrypoint", `UPDATE functions SET run_entrypoint='other.js' WHERE id=?`},
		{"unrecorded_output", `UPDATE workload_deployment_capture SET output='' WHERE function_id=?`},
		{"malformed_output", `UPDATE workload_deployment_capture SET output='{"CodeHash":"bad"}' WHERE function_id=?`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, ref, req, _ := pausedResumeFixture(t, true)
			operatorExec(t, db, tc.query, req.OwnerID)
			resumePlanIsReadOnly(t, db, req.OwnerID)
			if plan, err := db.PrepareWorkloadResume(t.Context(), ref, req); plan != nil || !errors.Is(err, ErrWorkloadResumeAssociation) {
				t.Fatalf("invalid sealed association accepted or misclassified: %v", err)
			}
		})
	}
	for _, table := range []string{"deployments", "workload_deployment_capture"} {
		t.Run("foreign_"+table+"_owner", func(t *testing.T) {
			db, ref, req, _ := pausedResumeFixture(t, true)
			iamTestFunction(t, db, iamTestOther, true)
			operatorExec(t, db, `UPDATE `+table+` SET function_id=? WHERE function_id=?`, iamTestOther, req.OwnerID)
			resumePlanIsReadOnly(t, db, req.OwnerID)
			if plan, err := db.PrepareWorkloadResume(t.Context(), ref, req); plan != nil || !errors.Is(err, ErrWorkloadResumeAssociation) {
				t.Fatalf("foreign %s accepted: %v", table, err)
			}
		})
	}
}

func TestWorkloadOperatorResumeCommit(t *testing.T) {
	for _, mode := range []string{"original", "promotion_pending", "promotion_installed"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			var db *Database
			var ref OperatorCredentialReference
			var req WorkloadPauseRequest
			var output WorkloadBuildOutput
			if mode == "original" {
				db, ref, req, output = pausedResumeFixture(t, true)
			} else {
				d, r, owner, target, o := rollbackFixture(t)
				db, ref, output = d, r, o
				p, err := db.PrepareWorkloadRollback(ctx, ref, owner, target)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.CommitWorkloadRollback(ctx, ref, p, output); err != nil {
					t.Fatal(err)
				}
				if mode == "promotion_installed" {
					p, err := db.PrepareWorkloadDeploymentInstallation(ctx, target)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = db.ActivateWorkloadDeploymentInstallation(ctx, ref, p); err != nil {
						t.Fatal(err)
					}
				}
				if _, _, err := db.PauseWorkloadFunctionAsOperator(ctx, ref, pauseRequest(t, db, owner)); err != nil {
					t.Fatal(err)
				}
				req = pauseRequest(t, db, owner)
			}
			owner := req.OwnerID
			operatorExec(t, db, `UPDATE workload_iam_policies SET quarantined=1 WHERE function_id=?`, owner)
			if err := db.UpsertSecret(owner, "KEEP", "fixture ciphertext"); err != nil {
				t.Fatal(err)
			}
			before := artifactTestSnapshot(t, db, owner)
			policy, audit := environmentPublishPolicy(t, db)
			history, err := db.ListDeploymentsForFunction(owner, 10)
			if err != nil {
				t.Fatal(err)
			}
			p, err := db.PrepareWorkloadResume(ctx, ref, req)
			if err != nil {
				t.Fatal(err)
			}
			out, err := db.CommitWorkloadResume(ctx, ref, p, output)
			if err != nil {
				t.Fatal(err)
			}
			after := artifactTestSnapshot(t, db, owner)
			wantFn := before.Function.Clone()
			wantFn.Version++
			wantFn.UpdatedAt = out.Function.UpdatedAt
			if out.Artifact.Generation == before.Artifact.Generation || out.Artifact.Revision != before.Artifact.Revision+1 ||
				!reflect.DeepEqual(after.Function, wantFn) || !reflect.DeepEqual(after.Secrets, before.Secrets) {
				t.Fatal("resume did not rotate in place while preserving paused config and secrets")
			}
			if got, entries := environmentPublishPolicy(t, db); !got.ReviewRequired || !got.Quarantined ||
				got.ApprovedArtifact != policy.ApprovedArtifact || got.PolicyJSON != policy.PolicyJSON ||
				got.Revision != policy.Revision+1 || got.AuthorityEpoch != policy.AuthorityEpoch+1 ||
				len(entries) != len(audit)+1 || entries[0].Actor.ID != ref.ID || entries[0].Revision != got.Revision {
				t.Fatal("resume did not commit the audited review-required successor")
			}
			if paused, err := db.WorkloadOperatorPaused(ctx, owner); err != nil || paused {
				t.Fatalf("pause not consumed: %v", err)
			}
			id := before.Function.ActiveDeploymentID
			install, err := db.PrepareWorkloadDeploymentInstallation(ctx, id)
			if err != nil || install.Output() != output || install.Snapshot().Artifact.Generation != out.Artifact.Generation {
				t.Fatalf("no exact successor install plan: %v", err)
			}
			if _, err := db.CaptureWorkloadDeployment(ctx, ref, owner); err != nil {
				t.Fatalf("resume stranded inactive deployment capture: %v", err)
			}
			if _, err := db.ActivateWorkloadDeploymentInstallation(ctx, ref, install); err != nil {
				t.Fatal(err)
			}
			afterHistory, err := db.ListDeploymentsForFunction(owner, 10)
			if err != nil || !reflect.DeepEqual(afterHistory[1:], history) {
				t.Fatalf("resume or install changed immutable history: %v", err)
			}
		})
	}
}

func TestWorkloadOperatorResumeCommitCAS(t *testing.T) {
	for _, tc := range []struct {
		name, query string
		want        error
	}{
		{"config", `UPDATE functions SET timeout_ms=timeout_ms+1`, ErrWorkloadArtifactConflict},
		{"secret", `UPDATE function_secrets SET value_encrypted='different'`, ErrWorkloadArtifactConflict},
		{"pause", `UPDATE workload_operator_pause SET paused_at='2026-01-01T00:00:00Z'`, ErrWorkloadArtifactConflict},
		{"policy", `UPDATE workload_iam_policies SET quarantined=1`, ErrIAMConflict},
		{"association", `UPDATE workload_deployment_capture SET output=json_set(output,'$.CodeGeneration','` + ids.New() + `')`, ErrWorkloadArtifactConflict},
		{"credential", `UPDATE api_keys SET permissions='["write"]'`, ErrOperatorDenied},
		{"commit_failure", `CREATE TABLE resume_guard(owner TEXT REFERENCES functions(id) DEFERRABLE INITIALLY DEFERRED);
			CREATE TRIGGER reject_resume AFTER DELETE ON workload_operator_pause
			BEGIN INSERT INTO resume_guard VALUES('missing-owner'); END`, ErrWorkloadArtifactStorage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()
			db, ref, req, output := pausedResumeFixture(t, true)
			if err := db.UpsertSecret(req.OwnerID, "KEEP", "fixture ciphertext"); err != nil {
				t.Fatal(err)
			}
			p, err := db.PrepareWorkloadResume(ctx, ref, req)
			if err != nil {
				t.Fatal(err)
			}
			operatorExec(t, db, tc.query)
			before := artifactTestSnapshot(t, db, req.OwnerID)
			policy, audit := environmentPublishPolicy(t, db)
			if out, err := db.CommitWorkloadResume(ctx, ref, p, output); out != nil || !errors.Is(err, tc.want) {
				t.Fatalf("resume committed or returned wrong failure: %v", err)
			}
			artifactTestUnchanged(t, db, before)
			iamTestUnchanged(t, db, policy, audit)
			if paused, err := db.WorkloadOperatorPaused(ctx, req.OwnerID); err != nil || !paused {
				t.Fatalf("failed resume consumed pause: %v", err)
			}
			if n := iamTestCount(t, db, "workload_deployment_promotion"); n != 0 {
				t.Fatal("failed resume recorded a promotion")
			}
		})
	}
}

func TestWorkloadOperatorResumeAdmissionRetainsMetadataButDeniesStanding(t *testing.T) {
	db, _, owner := deploymentFixture(t, "api_key")
	record, _ := environmentPublishPolicy(t, db)
	for _, tc := range []struct {
		name               string
		review, quarantine bool
		want               error
	}{
		{"approved", false, false, nil},
		{"review_required", true, false, workloadiam.ErrDenied},
		{"quarantined", false, true, workloadiam.ErrDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := *record
			p.ReviewRequired, p.Quarantined = tc.review, tc.quarantine
			e, err := p.AdmissionEvaluator()
			if err != nil || e.Metadata() != p.Metadata {
				t.Fatalf("admission evaluator lost inspection metadata: %v", err)
			}
			a, err := workloadiam.NewAuthority(1, []*workloadiam.Evaluator{e})
			if err != nil {
				t.Fatal(err)
			}
			origin := workloadiam.AsyncOrigin{Kind: "operator", OperatorKind: "api_key", OperatorID: owner,
				TargetID: owner, Trigger: workloadiam.Job, ExpectedTargetRevision: p.Revision}
			observe := func() workloadiam.StandingState {
				return workloadiam.StandingState{TargetArtifact: p.ApprovedArtifact}
			}
			called := false
			err = a.DoStandingObserved(origin, observe, func(workloadiam.AsyncRevisions) error { called = true; return nil })
			if !errors.Is(err, tc.want) || called != (tc.want == nil) {
				t.Fatalf("wrong standing decision: effect=%v, err=%v", called, err)
			}
		})
	}
}
