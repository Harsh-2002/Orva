package database

import (
	"errors"
	"testing"

	"github.com/Harsh-2002/Orva/internal/ids"
)

func pauseFixture(t *testing.T, kind string) (*Database, OperatorCredentialReference, string) {
	t.Helper()
	db, ref, owner := deploymentFixture(t, kind)
	if err := db.InitWorkloadOperatorPauseSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	return db, ref, owner
}

func pauseRequest(t *testing.T, db *Database, owner string) WorkloadPauseRequest {
	t.Helper()
	snapshot, err := db.GetWorkloadArtifactSnapshot(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := db.GetIAMPolicy(t.Context(), owner)
	if err != nil {
		t.Fatal(err)
	}
	return WorkloadPauseRequest{OwnerID: owner, ExpectedGeneration: snapshot.Artifact.Generation,
		ExpectedPolicyRevision: policy.Revision, ExpectedAuthorityEpoch: policy.AuthorityEpoch}
}

func publishedDeployment(t *testing.T, db *Database, ref OperatorCredentialReference, owner string) *WorkloadDeploymentPlan {
	t.Helper()
	p := captureDeployment(t, db, ref, owner)
	output := deploymentOutput()
	if err := db.RecordWorkloadBuildOutput(t.Context(), p.ID, output); err != nil {
		t.Fatal(err)
	}
	if _, err := db.PublishWorkloadDeployment(t.Context(), p.ID, output); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOperatorPauseDistinguishesPendingReviewFromPauseIntent(t *testing.T) {
	db, ref, owner := pauseFixture(t, "api_key")
	publishedDeployment(t, db, ref, owner)
	fn, err := db.GetFunction(owner)
	if err != nil || fn.Status != "inactive" {
		t.Fatal("publication did not leave a staged inactive function", err)
	}
	if paused, err := db.WorkloadOperatorPaused(t.Context(), owner); err != nil || paused {
		t.Fatal("pending review recorded as operator pause", err)
	}
	// The staged inactive state is publication's review state, not operator intent.
	if _, err := db.CaptureWorkloadDeployment(t.Context(), ref, owner); err != nil {
		t.Fatalf("pending review was treated as operator pause: %v", err)
	}

	// An inactive function without pause-mode evidence is still refused.
	other, otherRef, otherOwner := deploymentFixture(t, "api_key")
	operatorExec(t, other, `UPDATE functions SET status='inactive' WHERE id=?`, otherOwner)
	if _, err := other.CaptureWorkloadDeployment(t.Context(), otherRef, otherOwner); !errors.Is(err, ErrWorkloadArtifactConflict) {
		t.Fatalf("inactive without evidence captured: %v", err)
	}

	// Explicit intent denies capture even when the serving status says otherwise.
	if _, _, err := db.PauseWorkloadFunctionAsOperator(t.Context(), ref, pauseRequest(t, db, owner)); err != nil {
		t.Fatal(err)
	}
	operatorExec(t, db, `UPDATE functions SET status='active' WHERE id=?`, owner)
	if _, err := db.CaptureWorkloadDeployment(t.Context(), ref, owner); !errors.Is(err, ErrWorkloadArtifactConflict) {
		t.Fatalf("pause intent bypassed by active status: %v", err)
	}
}

func TestOperatorPauseInvalidatesCapturedPublication(t *testing.T) {
	db, ref, owner := pauseFixture(t, "api_key")
	p := captureDeployment(t, db, ref, owner)
	output := deploymentOutput()
	if err := db.RecordWorkloadBuildOutput(t.Context(), p.ID, output); err != nil {
		t.Fatal(err)
	}
	before := artifactTestSnapshot(t, db, owner)
	policyBefore, _ := db.GetIAMPolicy(t.Context(), owner)
	pause, fn, err := db.PauseWorkloadFunctionAsOperator(t.Context(), ref, pauseRequest(t, db, owner))
	if err != nil {
		t.Fatal(err)
	}
	after := artifactTestSnapshot(t, db, owner)
	policyAfter, _ := db.GetIAMPolicy(t.Context(), owner)
	if pause.OwnerID != owner || pause.ArtifactGeneration != before.Artifact.Generation ||
		pause.PolicyRevision != policyBefore.Revision || pause.AuthorityEpoch != policyBefore.AuthorityEpoch+1 ||
		pause.Actor.Kind != ref.Kind || pause.Actor.ID != ref.ID || pause.PausedAt.IsZero() {
		t.Fatal("pause record does not pin the exact reviewed authority")
	}
	if fn.Status != "inactive" || fn.Version != before.Function.Version+1 ||
		after.Function.Status != "inactive" || after.Artifact.Generation != before.Artifact.Generation ||
		policyAfter.Revision != policyBefore.Revision || policyAfter.AuthorityEpoch != policyBefore.AuthorityEpoch+1 ||
		policyAfter.PolicyJSON != policyBefore.PolicyJSON || policyAfter.ApprovedArtifact != policyBefore.ApprovedArtifact {
		t.Fatal("pause changed more than intent, status and authority epoch")
	}
	if paused, err := db.WorkloadOperatorPaused(t.Context(), owner); err != nil || !paused {
		t.Fatal("pause intent not durable", err)
	}
	if _, err := db.PublishWorkloadDeployment(t.Context(), p.ID, output); !errors.Is(err, ErrWorkloadArtifactConflict) {
		t.Fatalf("captured build published after pause: %v", err)
	}
	dep, _ := db.GetDeployment(p.ID)
	if dep.Status == "succeeded" {
		t.Fatal("paused owner published")
	}
	if _, err := db.CaptureWorkloadDeployment(t.Context(), ref, owner); !errors.Is(err, ErrWorkloadArtifactConflict) {
		t.Fatalf("paused owner captured: %v", err)
	}
}

func TestOperatorPauseAfterPrepareBlocksActivation(t *testing.T) {
	for _, stage := range []string{"activate", "prepare"} {
		t.Run(stage, func(t *testing.T) {
			db, ref, owner := pauseFixture(t, "api_key")
			p := publishedDeployment(t, db, ref, owner)
			install, err := db.PrepareWorkloadDeploymentInstallation(t.Context(), p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := db.PauseWorkloadFunctionAsOperator(t.Context(), ref, pauseRequest(t, db, owner)); err != nil {
				t.Fatal(err)
			}
			if stage == "prepare" {
				if _, err := db.PrepareWorkloadDeploymentInstallation(t.Context(), p.ID); !errors.Is(err, ErrWorkloadArtifactConflict) {
					t.Fatalf("paused owner prepared: %v", err)
				}
				return
			}
			if err := db.CheckWorkloadDeploymentInstallation(t.Context(), ref, install); !errors.Is(err, ErrWorkloadArtifactConflict) {
				t.Fatalf("paused owner passed installation check: %v", err)
			}
			if _, err := db.ActivateWorkloadDeploymentInstallation(t.Context(), ref, install); !errors.Is(err, ErrWorkloadArtifactConflict) {
				t.Fatalf("paused owner activated: %v", err)
			}
			fn, err := db.GetFunction(owner)
			if err != nil || fn.Status != "inactive" {
				t.Fatal("pause implicitly resumed by activation", err)
			}
			if paused, err := db.WorkloadOperatorPaused(t.Context(), owner); err != nil || !paused {
				t.Fatal("activation cleared pause intent", err)
			}
		})
	}
}

func TestOperatorPauseCASAuthorizationAndUnavailable(t *testing.T) {
	t.Run("stale", func(t *testing.T) {
		db, ref, owner := pauseFixture(t, "api_key")
		publishedDeployment(t, db, ref, owner)
		base := pauseRequest(t, db, owner)
		for name, mutate := range map[string]func(*WorkloadPauseRequest){
			"generation": func(r *WorkloadPauseRequest) { r.ExpectedGeneration = ids.New() },
			"revision":   func(r *WorkloadPauseRequest) { r.ExpectedPolicyRevision++ },
			"epoch":      func(r *WorkloadPauseRequest) { r.ExpectedAuthorityEpoch++ },
		} {
			t.Run(name, func(t *testing.T) {
				before := artifactTestSnapshot(t, db, owner)
				policy, _ := db.GetIAMPolicy(t.Context(), owner)
				req := base
				mutate(&req)
				if _, _, err := db.PauseWorkloadFunctionAsOperator(t.Context(), ref, req); !errors.Is(err, ErrWorkloadArtifactConflict) && !errors.Is(err, ErrIAMConflict) {
					t.Fatalf("stale %s accepted: %v", name, err)
				}
				artifactTestUnchanged(t, db, before)
				after, _ := db.GetIAMPolicy(t.Context(), owner)
				if after.Revision != policy.Revision || after.AuthorityEpoch != policy.AuthorityEpoch ||
					after.ReviewRequired != policy.ReviewRequired {
					t.Fatal("stale pause changed authority")
				}
				if paused, err := db.WorkloadOperatorPaused(t.Context(), owner); err != nil || paused {
					t.Fatal("stale pause recorded intent", err)
				}
			})
		}
	})

	t.Run("admin", func(t *testing.T) {
		db, _, owner := pauseFixture(t, "api_key")
		operatorExec(t, db, `UPDATE workload_iam_policies SET policy_json=?,review_required=0,quarantined=0 WHERE function_id=?`, iamTestEmpty, owner)
		operatorExec(t, db, `UPDATE workload_iam_inline SET policy_json=? WHERE function_id=?`, iamTestEmpty, owner)
		writer := ids.New()
		if err := db.InsertAPIKey(&APIKey{ID: writer, KeyHash: "private-writer-hash", Prefix: "private-writer",
			Permissions: `["write"]`}); err != nil {
			t.Fatal(err)
		}
		req := pauseRequest(t, db, owner)
		if _, _, err := db.PauseWorkloadFunctionAsOperator(t.Context(), OperatorCredentialReference{Kind: "api_key", ID: writer}, req); err != nil {
			t.Fatalf("review-free owner rejected writer: %v", err)
		}
		// A protected owner requires live ADMIN; the writer must not resume.
		other, otherRef, otherOwner := pauseFixture(t, "api_key")
		operatorExec(t, other, `UPDATE workload_iam_policies SET review_required=1 WHERE function_id=?`, otherOwner)
		if err := other.InsertAPIKey(&APIKey{ID: writer, KeyHash: "private-writer-hash", Prefix: "private-writer",
			Permissions: `["write"]`}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := other.PauseWorkloadFunctionAsOperator(t.Context(), OperatorCredentialReference{Kind: "api_key", ID: writer},
			pauseRequest(t, other, otherOwner)); !errors.Is(err, ErrOperatorDenied) {
			t.Fatalf("protected owner paused by WRITE: %v", err)
		}
		if paused, err := other.WorkloadOperatorPaused(t.Context(), otherOwner); err != nil || paused {
			t.Fatal("denied pause changed intent", err)
		}
		if _, _, err := other.PauseWorkloadFunctionAsOperator(t.Context(), otherRef, pauseRequest(t, other, otherOwner)); err != nil {
			t.Fatalf("protected owner rejected live ADMIN: %v", err)
		}
	})

	t.Run("unavailable", func(t *testing.T) {
		db, ref, owner := deploymentFixture(t, "api_key")
		before := artifactTestSnapshot(t, db, owner)
		policy, _ := db.GetIAMPolicy(t.Context(), owner)
		req := WorkloadPauseRequest{OwnerID: owner, ExpectedGeneration: before.Artifact.Generation,
			ExpectedPolicyRevision: policy.Revision, ExpectedAuthorityEpoch: policy.AuthorityEpoch}
		if _, _, err := db.PauseWorkloadFunctionAsOperator(t.Context(), ref, req); !errors.Is(err, ErrWorkloadPauseUnavailable) {
			t.Fatalf("pause mode silently approximated: %v", err)
		}
		artifactTestUnchanged(t, db, before)
		if paused, err := db.WorkloadOperatorPaused(t.Context(), owner); err != nil || paused {
			t.Fatal("unavailable mode recorded intent", err)
		}
	})
}
