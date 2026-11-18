package database

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/internal/ids"
)

func deploymentFixture(t *testing.T, kind string) (*Database, OperatorCredentialReference, string) {
	t.Helper()
	db, ref, c := operatorPublishSetup(t, kind)
	if err := db.initManagedIAMSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := db.InitWorkloadDeploymentSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	return db, ref, c.OwnerID
}

func deploymentOutput() WorkloadBuildOutput {
	return WorkloadBuildOutput{CodeGeneration: ids.New(), RuntimeGeneration: ids.New(),
		CodeDigest: strings.Repeat("a", 64), RuntimeDigest: strings.Repeat("b", 64), CodeHash: strings.Repeat("c", 64), Entrypoint: "handler.js"}
}

func captureDeployment(t *testing.T, db *Database, ref OperatorCredentialReference, owner string) *WorkloadDeploymentPlan {
	t.Helper()
	p, err := db.CaptureWorkloadDeployment(t.Context(), ref, owner)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDeploymentCaptureAndDenyPublication(t *testing.T) {
	for _, kind := range []string{"api_key", "session", "oauth"} {
		t.Run(kind, func(t *testing.T) {
			db, ref, owner := deploymentFixture(t, kind)
			before, err := db.GetWorkloadArtifactSnapshot(t.Context(), owner)
			if err != nil {
				t.Fatal(err)
			}
			src, _ := db.GetIAMSources(t.Context(), owner)
			p := captureDeployment(t, db, ref, owner)
			output := deploymentOutput()
			if err := db.RecordWorkloadBuildOutput(t.Context(), p.ID, output); err != nil {
				t.Fatal(err)
			}
			out, err := db.PublishWorkloadDeployment(t.Context(), p.ID, output)
			if err != nil {
				t.Fatal(err)
			}
			dep, _ := db.GetDeployment(p.ID)
			afterSrc, _ := db.GetIAMSources(t.Context(), owner)
			if out.Artifact.Generation == before.Artifact.Generation || out.Function.Version != before.Function.Version+1 ||
				!out.Policy.ReviewRequired || out.Policy.PolicyJSON != src.Record.PolicyJSON ||
				out.Policy.ApprovedArtifact != src.Record.ApprovedArtifact || afterSrc.InlineJSON != src.InlineJSON ||
				out.Policy.AuthorityEpoch != src.Record.AuthorityEpoch+1 || dep.Status != "succeeded" || dep.Phase != "review_required" ||
				out.Function.ActiveDeploymentID != p.ID || out.Function.EnvVars["CONFIG"] != before.Function.EnvVars["CONFIG"] {
				t.Fatal("not an atomic source-preserving deny publication")
			}
			if _, err := db.CheckWorkloadDeployment(t.Context(), p.ID); err == nil {
				t.Fatal("published work reused")
			}
			install, err := db.PrepareWorkloadDeploymentInstallation(t.Context(), p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := db.CheckWorkloadDeploymentInstallation(t.Context(), ref, install); err != nil {
				t.Fatal(err)
			}
			operatorExec(t, db, `UPDATE workload_iam_policies SET authority_epoch=authority_epoch+1`)
			if err := db.CheckWorkloadDeploymentInstallation(t.Context(), ref, install); err == nil {
				t.Fatal("stale install accepted")
			}
		})
	}
}

func TestDeploymentCredentialsRecheckedCaptureAndPublication(t *testing.T) {
	for kind, queries := range map[string][]string{
		"api_key": {`DELETE FROM api_keys`, `UPDATE api_keys SET expires_at='2000-01-01 00:00:00'`, `UPDATE api_keys SET permissions='["read"]'`},
		"session": {`DELETE FROM sessions`, `UPDATE sessions SET expires_at='2000-01-01 00:00:00'`, `DELETE FROM users`},
		"oauth": {`UPDATE oauth_access_tokens SET revoked_at=CURRENT_TIMESTAMP`, `UPDATE oauth_access_tokens SET access_expires_at='2000-01-01 00:00:00'`,
			`UPDATE oauth_clients SET revoked_at=CURRENT_TIMESTAMP`, `UPDATE oauth_access_tokens SET resource='https://other.invalid/mcp'`, `UPDATE oauth_clients SET scope='read'`},
	} {
		for _, query := range queries {
			for _, stage := range []string{"capture", "publication"} {
				t.Run(kind+"/"+stage+"/"+query, func(t *testing.T) {
					db, ref, owner := deploymentFixture(t, kind)
					var p *WorkloadDeploymentPlan
					output := deploymentOutput()
					if stage == "publication" {
						p = captureDeployment(t, db, ref, owner)
						if err := db.RecordWorkloadBuildOutput(t.Context(), p.ID, output); err != nil {
							t.Fatal(err)
						}
					}
					before := artifactTestSnapshot(t, db, owner)
					operatorExec(t, db, query)
					var err error
					if stage == "capture" {
						_, err = db.CaptureWorkloadDeployment(t.Context(), ref, owner)
					} else {
						_, err = db.PublishWorkloadDeployment(t.Context(), p.ID, output)
					}
					if !errors.Is(err, ErrOperatorDenied) {
						t.Fatalf("expected live denial: %v", err)
					}
					artifactTestUnchanged(t, db, before)
				})
			}
		}
	}
}

func TestDeploymentEpochSourcesConfigAndDeletionCAS(t *testing.T) {
	for _, change := range []string{"epoch", "regrant", "inline", "attachment", "function", "environment", "runtime", "entrypoint", "deleting", "missing-provenance"} {
		t.Run(change, func(t *testing.T) {
			db, ref, owner := deploymentFixture(t, "api_key")
			p := captureDeployment(t, db, ref, owner)
			output := deploymentOutput()
			if err := db.RecordWorkloadBuildOutput(t.Context(), p.ID, output); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "epoch":
				operatorExec(t, db, `UPDATE workload_iam_policies SET authority_epoch=authority_epoch+1`)
			case "regrant":
				operatorExec(t, db, `UPDATE workload_iam_policies SET authority_epoch=authority_epoch+2`)
			case "inline":
				operatorExec(t, db, `UPDATE workload_iam_inline SET policy_json=?`, managedEmpty)
			case "attachment":
				managedApply(t, db, ref, IAMManagedMutation{Operation: "create", Name: "new", PolicyJSON: []byte(managedReader)})
				policies, _ := db.ListIAMManagedPolicies(t.Context(), 0)
				managedApply(t, db, ref, IAMManagedMutation{Operation: "attach", PolicyID: policies[0].ID, Version: 1,
					DefinitionRevision: policies[0].DefinitionRevision, MembershipRevision: policies[0].MembershipRevision,
					Owners: managedExpected(t, db, []string{owner})})
			case "function":
				operatorExec(t, db, `UPDATE functions SET version=version+1`)
			case "environment":
				operatorExec(t, db, `UPDATE functions SET env_vars='{"NEW":"accepted"}'`)
			case "runtime":
				operatorExec(t, db, `UPDATE functions SET runtime='python'`)
			case "entrypoint":
				operatorExec(t, db, `UPDATE functions SET entrypoint='new.js'`)
			case "deleting":
				operatorExec(t, db, `UPDATE functions SET status='deleting'`)
			case "missing-provenance":
				operatorExec(t, db, `DELETE FROM workload_deployment_capture`)
			}
			if _, err := db.PublishWorkloadDeployment(t.Context(), p.ID, output); err == nil {
				t.Fatal("stale admin publication accepted")
			}
			dep, _ := db.GetDeployment(p.ID)
			if dep.Status == "succeeded" {
				t.Fatal("partial publication")
			}
		})
	}
}

func TestDeploymentEmptyWriterAndRetainedPrivilege(t *testing.T) {
	db, ref, owners := managedDB(t, 1)
	owner := owners[0]
	if err := db.initWorkloadArtifactSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := db.NewWorkloadArtifact(t.Context(), owner, IAMActorReference{Kind: "system", ID: ids.New()}); err != nil {
		t.Fatal(err)
	}
	if err := db.InitWorkloadDeploymentSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	operatorExec(t, db, `UPDATE functions SET version=1,memory_mb=256,cpus=1,timeout_ms=10000,network_mode='none',auth_mode='none',concurrency_policy='queue',env_vars='{}'`)
	operatorExec(t, db, `UPDATE api_keys SET permissions='["write"]'`)
	p := captureDeployment(t, db, ref, owner)
	for _, body := range []string{
		managedReader,
		`{"schema_version":1,"allow":[],"inbound":[{"caller_id":"` + ids.New() + `","triggers":["f2f"]}]}`,
	} {
		s := IAMSourceSnapshot{Record: &IAMPolicyRecord{PolicyJSON: managedEmpty}, InlineJSON: managedEmpty,
			Attachments: []IAMAttachment{{PolicyJSON: body, Enabled: false, Revoked: true}}}
		privileged, err := SourcePrivilege(s)
		if err != nil {
			t.Fatal(err)
		}
		if !privileged {
			t.Fatal("disabled retained grant was ignored")
		}
	}
	operatorExec(t, db, `UPDATE workload_iam_policies SET authority_epoch=authority_epoch+1,review_required=1`)
	if _, err := db.CheckWorkloadDeployment(t.Context(), p.ID); !errors.Is(err, ErrOperatorDenied) {
		t.Fatalf("queued WRITE not protected after grant: %v", err)
	}
}

func TestDeploymentCaptureRollbackAbandonmentAndCancellation(t *testing.T) {
	db, ref, owner := deploymentFixture(t, "api_key")
	operatorExec(t, db, `CREATE TRIGGER reject_capture BEFORE INSERT ON workload_deployment_capture BEGIN SELECT RAISE(ABORT,'test'); END`)
	if _, err := db.CaptureWorkloadDeployment(t.Context(), ref, owner); err == nil {
		t.Fatal("capture failure ignored")
	}
	var count int
	if err := db.read.QueryRow(`SELECT count(*) FROM deployments`).Scan(&count); err != nil || count != 0 {
		t.Fatal("deployment escaped capture rollback", err)
	}
	operatorExec(t, db, `DROP TRIGGER reject_capture`)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := db.CaptureWorkloadDeployment(ctx, ref, owner); err == nil {
		t.Fatal("canceled capture")
	}
	p := captureDeployment(t, db, ref, owner)
	if err := db.AbandonWorkloadDeployments(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := db.AbandonWorkloadDeployments(t.Context()); err != nil {
		t.Fatal(err)
	}
	dep, _ := db.GetDeployment(p.ID)
	if dep.Status != "failed" || dep.Phase != "abandoned" {
		t.Fatal("abandoned work not terminal")
	}
	if _, err := db.CheckWorkloadDeployment(t.Context(), p.ID); err == nil {
		t.Fatal("restart work resumed")
	}
}

func TestQueuedWriterCannotCrossAnyGrant(t *testing.T) {
	for _, kind := range []string{"outbound", "inbound", "attachment", "retained"} {
		t.Run(kind, func(t *testing.T) {
			db, writer, owners := managedDB(t, 1)
			owner := owners[0]
			if err := db.initWorkloadArtifactSchema(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := db.NewWorkloadArtifact(t.Context(), owner, IAMActorReference{Kind: "system", ID: ids.New()}); err != nil {
				t.Fatal(err)
			}
			if err := db.InitWorkloadDeploymentSchema(t.Context()); err != nil {
				t.Fatal(err)
			}
			operatorExec(t, db, `UPDATE functions SET version=1,memory_mb=256,cpus=1,timeout_ms=10000,network_mode='none',auth_mode='none',concurrency_policy='queue',env_vars='{}'`)
			operatorExec(t, db, `UPDATE api_keys SET permissions='["write"]'`)
			admin := OperatorCredentialReference{Kind: "api_key", ID: ids.New()}
			if err := db.InsertAPIKey(&APIKey{ID: admin.ID, Name: "admin", KeyHash: ids.New(), Prefix: "fixture", Permissions: `["admin"]`}); err != nil {
				t.Fatal(err)
			}
			p := captureDeployment(t, db, writer, owner)
			body := managedReader
			if kind == "inbound" {
				body = `{"schema_version":1,"allow":[],"inbound":[{"caller_id":"` + ids.New() + `","triggers":["f2f"]}]}`
			}
			if kind == "attachment" || kind == "retained" {
				def := managedApply(t, db, admin, IAMManagedMutation{Operation: "create", Name: "grant", PolicyJSON: []byte(body)})
				def = managedApply(t, db, admin, managedChange(def, "attach", managedExpected(t, db, owners)))
				if kind == "retained" {
					managedApply(t, db, admin, managedChange(def, "revoke", managedExpected(t, db, owners)))
					sources, err := db.GetIAMSources(t.Context(), owner)
					if err != nil || sources.Record.PolicyJSON != managedEmpty {
						t.Fatal("retained-only fixture still had effective grants", err)
					}
				}
			} else {
				managedApply(t, db, admin, IAMManagedMutation{Operation: "inline", PolicyJSON: []byte(body), Owners: managedExpected(t, db, owners)})
			}
			if _, err := db.CheckWorkloadDeployment(t.Context(), p.ID); !errors.Is(err, ErrOperatorDenied) {
				t.Fatalf("queued WRITE crossed %s: %v", kind, err)
			}
			if _, err := db.CaptureWorkloadDeployment(t.Context(), writer, owner); !errors.Is(err, ErrOperatorDenied) {
				t.Fatalf("fresh WRITE captured %s: %v", kind, err)
			}
			// Even an upgrade of the same credential cannot resurrect the old epoch.
			operatorExec(t, db, `UPDATE api_keys SET permissions='["admin"]' WHERE id=?`, writer.ID)
			if _, err := db.CheckWorkloadDeployment(t.Context(), p.ID); !errors.Is(err, ErrIAMConflict) {
				t.Fatalf("admin bypassed old epoch: %v", err)
			}
		})
	}
}

func TestDeploymentPublicationFailureAtomicity(t *testing.T) {
	for _, trigger := range []string{
		`CREATE TRIGGER reject_build BEFORE UPDATE ON functions WHEN NEW.code_hash!=OLD.code_hash BEGIN SELECT RAISE(ABORT,'test'); END`,
		`CREATE TRIGGER reject_build BEFORE UPDATE ON deployments WHEN NEW.status='succeeded' BEGIN SELECT RAISE(ABORT,'test'); END`,
		`CREATE TRIGGER reject_build BEFORE INSERT ON workload_iam_audit BEGIN SELECT RAISE(ABORT,'test'); END`,
		`CREATE TABLE deployment_guard(owner TEXT REFERENCES functions(id) DEFERRABLE INITIALLY DEFERRED);
		CREATE TRIGGER reject_build AFTER INSERT ON workload_iam_audit BEGIN INSERT INTO deployment_guard VALUES('not-a-function'); END`,
	} {
		t.Run(trigger, func(t *testing.T) {
			db, ref, owner := deploymentFixture(t, "api_key")
			p := captureDeployment(t, db, ref, owner)
			o := deploymentOutput()
			if err := db.RecordWorkloadBuildOutput(t.Context(), p.ID, o); err != nil {
				t.Fatal(err)
			}
			before := artifactTestSnapshot(t, db, owner)
			policy, _ := db.GetIAMPolicy(t.Context(), owner)
			operatorExec(t, db, trigger)
			if _, err := db.PublishWorkloadDeployment(t.Context(), p.ID, o); err == nil {
				t.Fatal("failure ignored")
			}
			artifactTestUnchanged(t, db, before)
			after, _ := db.GetIAMPolicy(t.Context(), owner)
			if after.Revision != policy.Revision || after.AuthorityEpoch != policy.AuthorityEpoch {
				t.Fatal("partial authority")
			}
		})
	}
}

func TestDeploymentProvenanceContainsNoCredentialSecrets(t *testing.T) {
	db, ref, owner := deploymentFixture(t, "oauth")
	p := captureDeployment(t, db, ref, owner)
	var raw string
	if err := db.read.QueryRow(`SELECT provenance FROM workload_deployment_capture WHERE deployment_id=?`, p.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private-test", "CONFIG", "env_vars", "value_encrypted", "permissions", "admin"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("provenance leaked %s", forbidden)
		}
	}
	if len(raw) > 16384 {
		t.Fatal("unbounded provenance")
	}
}

func TestDeploymentDoesNotActivateDisabledTarget(t *testing.T) {
	db, ref, owner := deploymentFixture(t, "api_key")
	operatorExec(t, db, `UPDATE functions SET status='inactive' WHERE id=?`, owner)
	if _, err := db.CaptureWorkloadDeployment(t.Context(), ref, owner); !errors.Is(err, ErrWorkloadArtifactConflict) {
		t.Fatalf("inactive target captured: %v", err)
	}
	fn, err := db.GetFunction(owner)
	if err != nil || fn.Status != "inactive" {
		t.Fatal("disabled target was activated", err)
	}
}

func TestDeploymentBoundsCapturedMetadata(t *testing.T) {
	db, ref, owner := deploymentFixture(t, "api_key")
	for _, description := range []string{strings.Repeat("x", (64<<10)+1), string([]byte{0xff})} {
		operatorExec(t, db, `UPDATE functions SET description=? WHERE id=?`, description, owner)
		if _, err := db.CaptureWorkloadDeployment(t.Context(), ref, owner); !errors.Is(err, ErrWorkloadArtifactInvalid) {
			t.Fatalf("unbounded/invalid metadata accepted: %v", err)
		}
	}
}
