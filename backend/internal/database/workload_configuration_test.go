package database

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Harsh-2002/Orva/internal/ids"
)

func configurationPlanSetup(t *testing.T, kind string) (*Database, OperatorCredentialReference, *WorkloadConfigurationPlan) {
	t.Helper()
	db, ref, c := operatorPublishSetup(t, kind)
	if err := db.initManagedIAMSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	snap := artifactTestSnapshot(t, db, c.OwnerID)
	p, _ := environmentPublishPolicy(t, db)
	expected := WorkloadConfigurationExpected{FunctionVersion: snap.Function.Version, ArtifactRevision: snap.Artifact.Revision,
		ArtifactEpoch: snap.Artifact.Epoch, Generation: snap.Artifact.Generation, PolicyRevision: p.Revision, AuthorityEpoch: p.AuthorityEpoch, ApprovedArtifact: p.ApprovedArtifact}
	plan, err := db.PrepareWorkloadConfiguration(t.Context(), c.OwnerID, expected, c.Env)
	if err != nil {
		t.Fatal(err)
	}
	return db, ref, plan
}

func TestWorkloadConfigurationAtomicAndCommitFailure(t *testing.T) {
	for _, failure := range []string{"none", "commit", "audit", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			db, ref, plan := configurationPlanSetup(t, "api_key")
			before := plan.Snapshot()
			policy, audit := environmentPublishPolicy(t, db)
			switch failure {
			case "commit":
				operatorExec(t, db, `CREATE TABLE config_guard(owner TEXT REFERENCES functions(id) DEFERRABLE INITIALLY DEFERRED);
				CREATE TRIGGER config_failure AFTER INSERT ON workload_iam_audit BEGIN INSERT INTO config_guard VALUES('absent'); END`)
			case "audit":
				operatorExec(t, db, `CREATE TRIGGER config_failure BEFORE INSERT ON workload_iam_audit BEGIN SELECT RAISE(ABORT,'sensitive-test-payload'); END`)
			}
			ctx := context.Background()
			if failure == "cancel" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			out, err := db.CommitWorkloadConfiguration(ctx, ref, plan)
			if failure == "none" {
				if err != nil || out == nil {
					t.Fatal("publication failed", err)
				}
				if out.Policy.ApprovedArtifact != policy.ApprovedArtifact || !out.Policy.ReviewRequired || out.Policy.AuthorityEpoch != policy.AuthorityEpoch+1 {
					t.Fatal("approval rebound")
				}
				sources, err := db.GetIAMSources(t.Context(), before.Artifact.OwnerID)
				if err != nil || sources.InlineJSON != plan.sources.InlineJSON || !reflect.DeepEqual(sources.Attachments, plan.sources.Attachments) {
					t.Fatal("sources rewritten")
				}
				if _, err := db.CommitWorkloadConfiguration(ctx, ref, plan); err == nil {
					t.Fatal("plan reused")
				}
				return
			}
			if err == nil || out != nil {
				t.Fatal("failure committed")
			}
			artifactTestUnchanged(t, db, before)
			iamTestUnchanged(t, db, policy, audit)
		})
	}
}

func TestWorkloadConfigurationLiveCredential(t *testing.T) {
	for kind, queries := range map[string][]string{
		"api_key": {`DELETE FROM api_keys`, `UPDATE api_keys SET expires_at='2000-01-01 00:00:00'`, `UPDATE api_keys SET permissions='["write"]'`},
		"oauth": {`UPDATE oauth_access_tokens SET revoked_at=CURRENT_TIMESTAMP`, `UPDATE oauth_clients SET revoked_at=CURRENT_TIMESTAMP`,
			`UPDATE oauth_access_tokens SET access_expires_at='2000-01-01 00:00:00'`, `UPDATE oauth_clients SET scope='read write'`, `UPDATE oauth_access_tokens SET resource='https://wrong.invalid/mcp'`},
		"session": {`DELETE FROM sessions`, `DELETE FROM users`, `UPDATE sessions SET expires_at='2000-01-01 00:00:00'`},
	} {
		for i, query := range queries {
			t.Run(kind+string(rune('a'+i)), func(t *testing.T) {
				db, ref, plan := configurationPlanSetup(t, kind)
				operatorExec(t, db, query)
				before := plan.Snapshot()
				p, audit := environmentPublishPolicy(t, db)
				out, err := db.CommitWorkloadConfiguration(t.Context(), ref, plan)
				if out != nil || !errors.Is(err, ErrOperatorDenied) {
					t.Fatalf("live credential accepted: %v", err)
				}
				artifactTestUnchanged(t, db, before)
				iamTestUnchanged(t, db, p, audit)
			})
		}
	}
}

func TestWorkloadConfigurationSourceAndCipherCAS(t *testing.T) {
	for _, change := range []string{"env", "secrets", "version", "grant-regrant", "retained-inbound", "attachment"} {
		t.Run(change, func(t *testing.T) {
			db, ref, plan := configurationPlanSetup(t, "api_key")
			switch change {
			case "env":
				operatorExec(t, db, `UPDATE functions SET env_vars='{"hidden":"mutation"}'`)
			case "secrets":
				operatorExec(t, db, `INSERT INTO function_secrets(function_id,key,value_encrypted) VALUES(?, 'opaque','cipher')`, plan.before.Artifact.OwnerID)
			case "version":
				operatorExec(t, db, `UPDATE functions SET version=version+1`)
			case "grant-regrant":
				operatorExec(t, db, `UPDATE workload_iam_policies SET authority_epoch=authority_epoch+2`)
			case "retained-inbound":
				operatorExec(t, db, `UPDATE workload_iam_policies SET quarantined=1`)
			case "attachment":
				id := ids.New()
				operatorExec(t, db, `INSERT INTO workload_iam_managed VALUES(?, 'disabled-source',1,1,1,0)`, id)
				operatorExec(t, db, `INSERT INTO workload_iam_managed_versions VALUES(?,1,?,'system',?,'2026-11-18T00:00:00Z')`, id, iamTestGrant, ids.New())
				operatorExec(t, db, `INSERT INTO workload_iam_attachments VALUES(?,?,1,0)`, plan.before.Artifact.OwnerID, id)
			}
			out, err := db.CommitWorkloadConfiguration(t.Context(), ref, plan)
			if out != nil || (!errors.Is(err, ErrWorkloadArtifactConflict) && !errors.Is(err, ErrIAMConflict)) {
				t.Fatalf("source CAS ignored: %v", err)
			}
		})
	}
}

func TestWorkloadConfigurationOrdinaryWriterAndPrivilegeRace(t *testing.T) {
	for _, authority := range []string{"empty", "inbound", "quarantine", "review", "attached-disabled", "grant-after-prepare"} {
		t.Run(authority, func(t *testing.T) {
			db, ref, _ := configurationPlanSetup(t, "api_key")
			empty := `{"schema_version":1,"allow":[],"inbound":[]}`
			operatorExec(t, db, `UPDATE api_keys SET permissions='["write"]'`)
			operatorExec(t, db, `UPDATE workload_iam_policies SET policy_json=?,review_required=0,quarantined=0`, empty)
			operatorExec(t, db, `UPDATE workload_iam_inline SET policy_json=?`, empty)
			if authority == "inbound" {
				body := `{"schema_version":1,"allow":[],"inbound":[{"caller_id":"` + ids.New() + `","triggers":["f2f"]}]}`
				operatorExec(t, db, `UPDATE workload_iam_policies SET policy_json=?`, body)
				operatorExec(t, db, `UPDATE workload_iam_inline SET policy_json=?`, body)
			}
			if authority == "quarantine" {
				operatorExec(t, db, `UPDATE workload_iam_policies SET quarantined=1`)
			}
			if authority == "review" {
				operatorExec(t, db, `UPDATE workload_iam_policies SET review_required=1`)
			}
			if authority == "attached-disabled" {
				id := ids.New()
				operatorExec(t, db, `INSERT INTO workload_iam_managed VALUES(?,'retained',1,1,1,1)`, id)
				operatorExec(t, db, `INSERT INTO workload_iam_managed_versions VALUES(?,1,?,'system',?,'2026-11-18T00:00:00Z')`, id, iamTestGrant, ids.New())
				operatorExec(t, db, `INSERT INTO workload_iam_attachments VALUES(?,?,1,0)`, iamTestOwner, id)
			}
			before := artifactTestSnapshot(t, db, iamTestOwner)
			p, _ := environmentPublishPolicy(t, db)
			plan, err := db.PrepareWorkloadConfiguration(t.Context(), iamTestOwner, WorkloadConfigurationExpected{FunctionVersion: before.Function.Version, ArtifactRevision: before.Artifact.Revision,
				ArtifactEpoch: before.Artifact.Epoch, Generation: before.Artifact.Generation, PolicyRevision: p.Revision, AuthorityEpoch: p.AuthorityEpoch, ApprovedArtifact: p.ApprovedArtifact}, map[string]string{})
			if err != nil {
				t.Fatal(err)
			}
			if authority == "grant-after-prepare" {
				operatorExec(t, db, `UPDATE workload_iam_policies SET policy_json=?,authority_epoch=authority_epoch+1`, iamTestGrant)
				operatorExec(t, db, `UPDATE workload_iam_inline SET policy_json=?`, iamTestGrant)
			}
			out, err := db.CommitWorkloadConfiguration(t.Context(), ref, plan)
			if authority == "empty" {
				if err != nil || out == nil {
					t.Fatal("ordinary WRITE denied", err)
				}
				return
			}
			if out != nil || !errors.Is(err, ErrOperatorDenied) {
				t.Fatalf("privileged ordinary writer accepted: %v", err)
			}
		})
	}
}

func TestWorkloadConfigurationSecretAtomic(t *testing.T) {
	for _, remove := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			t.Run(string(rune('a'+btoi(remove)*2+btoi(fail))), func(t *testing.T) {
				db, ref, base := configurationPlanSetup(t, "api_key")
				owner := base.before.Artifact.OwnerID
				operatorExec(t, db, `INSERT INTO function_secrets(function_id,key,value_encrypted) VALUES(?,'K','old-opaque-cipher')`, owner)
				a, p := base.before.Artifact, base.sources.Record
				expected := WorkloadConfigurationExpected{FunctionVersion: base.before.Function.Version, ArtifactRevision: a.Revision, ArtifactEpoch: a.Epoch,
					Generation: a.Generation, PolicyRevision: p.Revision, AuthorityEpoch: p.AuthorityEpoch, ApprovedArtifact: p.ApprovedArtifact}
				plan, err := db.PrepareWorkloadSecret(t.Context(), owner, expected, "K", "new-opaque-cipher", remove)
				if err != nil {
					t.Fatal(err)
				}
				before := plan.Snapshot()
				policy, audit := environmentPublishPolicy(t, db)
				if fail {
					operatorExec(t, db, `CREATE TABLE secret_guard(owner TEXT REFERENCES functions(id) DEFERRABLE INITIALLY DEFERRED);
					CREATE TRIGGER secret_failure AFTER INSERT ON workload_iam_audit BEGIN INSERT INTO secret_guard VALUES('absent'); END`)
				}
				out, err := db.CommitWorkloadConfiguration(t.Context(), ref, plan)
				if fail {
					if out != nil || !errors.Is(err, ErrWorkloadArtifactStorage) {
						t.Fatal("secret failed COMMIT was not rolled back")
					}
					artifactTestUnchanged(t, db, before)
					iamTestUnchanged(t, db, policy, audit)
					return
				}
				if err != nil || out == nil {
					t.Fatal("secret publication failed", err)
				}
				after := artifactTestSnapshot(t, db, owner)
				if !reflect.DeepEqual(after.Function.EnvVars, before.Function.EnvVars) || after.Artifact.Revision != a.Revision+1 ||
					out.Policy.AuthorityEpoch != p.AuthorityEpoch+1 || !out.Policy.ReviewRequired {
					t.Fatal("secret generation/policy/environment not atomic")
				}
				if remove && len(after.Secrets) != 0 || !remove && (len(after.Secrets) != 1 || after.Secrets[0].ValueEncrypted != "new-opaque-cipher") {
					t.Fatal("secret ciphertext not committed")
				}
			})
		}
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestWorkloadConfigurationInspectionReadIsSeparate(t *testing.T) {
	db, ref, plan := configurationPlanSetup(t, "api_key")
	owner := plan.before.Artifact.OwnerID
	if _, err := db.InspectWorkloadConfiguration(t.Context(), ref, owner); !errors.Is(err, ErrOperatorDenied) {
		t.Fatal("ADMIN implied READ")
	}
	operatorExec(t, db, `UPDATE api_keys SET permissions='["read"]'`)
	out, err := db.InspectWorkloadConfiguration(t.Context(), ref, owner)
	if err != nil || out == nil || out.Expected.FunctionVersion != plan.before.Function.Version {
		t.Fatal("metadata inspection failed", err)
	}
	if _, err := db.CommitWorkloadConfiguration(t.Context(), ref, plan); !errors.Is(err, ErrOperatorDenied) {
		t.Fatal("READ implied mutation authority")
	}
	operatorExec(t, db, `DELETE FROM api_keys`)
	if _, err := db.InspectWorkloadConfiguration(t.Context(), ref, owner); !errors.Is(err, ErrOperatorDenied) {
		t.Fatal("revoked reader accepted")
	}
}
