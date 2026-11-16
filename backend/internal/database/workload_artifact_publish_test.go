package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

func environmentPublishSetup(t *testing.T) (*Database, WorkloadEnvironmentChange) {
	t.Helper()
	ctx := context.Background()
	db, artifact := artifactTestSetup(t)
	if err := db.initWorkloadIAMSchema(ctx); err != nil {
		t.Fatal(err)
	}
	policy, err := db.NewIAMPolicy(ctx, artifact.OwnerID, false, iamTestActor)
	if err != nil {
		t.Fatal(err)
	}
	grants := strings.Replace(iamTestGrant, `"inbound":[]`,
		fmt.Sprintf(`"inbound":[{"caller_id":%q,"triggers":["f2f","job"]}]`, iamTestOther), 1)
	policy, err = db.SaveIAMPolicy(ctx, iamTestChange(policy, grants))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.write.Exec(`UPDATE functions SET version=1,description='preserve',runtime='node',
		entrypoint='handler.ts',run_entrypoint='dist/handler.js',code_hash='original-code',
		active_deployment_id='original-deployment',network_mode='egress',sdk=1,
		timeout_ms=1234,memory_mb=256,cpus=0.5,max_concurrency=7,concurrency_policy='reject',
		auth_mode='platform_key',rate_limit_per_min=17,image='original-image',image_size=99 WHERE id=?`, artifact.OwnerID); err != nil {
		t.Fatal(err)
	}
	return db, WorkloadEnvironmentChange{
		WorkloadArtifactChange: artifactTestChange(artifact), ExpectedFunctionVersion: 1,
		ExpectedPolicyRevision: policy.Revision, ExpectedApprovedArtifact: policy.ApprovedArtifact,
		Env: map[string]string{"CONFIG": "new-private-environment", "UNICODE": "😀雪"},
	}
}

func environmentPublishPolicy(t *testing.T, db *Database) (*IAMPolicyRecord, []IAMPolicyAudit) {
	t.Helper()
	p, err := db.GetIAMPolicy(context.Background(), iamTestOwner)
	if err != nil {
		t.Fatal(err)
	}
	a, err := db.ListIAMPolicyAudit(context.Background(), iamTestOwner)
	if err != nil {
		t.Fatal(err)
	}
	return p, a
}

func TestPublishWorkloadEnvironmentAtomicDenyAndOwnership(t *testing.T) {
	for _, quarantine := range []bool{false, true} {
		t.Run(fmt.Sprint(quarantine), func(t *testing.T) {
			ctx := context.Background()
			db, c := environmentPublishSetup(t)
			if _, err := db.write.Exec(`UPDATE workload_iam_policies SET quarantined=?`, quarantine); err != nil {
				t.Fatal(err)
			}
			before := artifactTestSnapshot(t, db, c.OwnerID)
			oldPolicy, oldAudit := environmentPublishPolicy(t, db)
			iamTestFunction(t, db, iamTestOther, false)
			other, err := db.NewWorkloadArtifact(ctx, iamTestOther, iamTestActor)
			if err != nil {
				t.Fatal(err)
			}
			otherBefore := artifactTestSnapshot(t, db, other.OwnerID)
			// Opaque/corrupt legacy secrets must not be fetched or decrypted here.
			if err := db.UpsertSecret(c.OwnerID, "opaque", ""); err != nil {
				t.Fatal(err)
			}
			key := "private-key-selector"
			call := workloadiam.Context{
				Principal: workloadiam.Principal{FunctionID: c.OwnerID, WorkerID: "worker", ArtifactRevision: oldPolicy.ApprovedArtifact},
				Phase:     workloadiam.Execution, Ready: true,
				Lease: workloadiam.Lease{ID: "lease", WorkerID: "worker", ExecutionID: iamTestOther, Active: true},
			}
			req := workloadiam.Request{Operation: workloadiam.OpKVGet, FunctionID: c.OwnerID,
				Resource: workloadiam.Resource{OwnKV: &workloadiam.KVResource{Key: &key}}}
			e, err := oldPolicy.AdmissionEvaluator()
			if err != nil || e.Allows(call, req) != !quarantine || e.Accepts(iamTestOther, workloadiam.F2F) != !quarantine {
				t.Fatal("fixture did not carry effective outbound/inbound authority")
			}
			r, err := db.PublishWorkloadEnvironment(ctx, c)
			if err != nil || r == nil {
				t.Fatalf("publication: %v", err)
			}
			if r.Artifact.Revision != before.Artifact.Revision+1 || r.Artifact.Epoch != before.Artifact.Epoch+1 ||
				r.Artifact.Generation == before.Artifact.Generation || !validIAMUUID(r.Artifact.Generation) ||
				r.Artifact.Generation[14] != '7' || r.Artifact.ChangeKind != "rotate" || r.Artifact.Actor != c.Actor {
				t.Fatal("generation did not rotate exactly once with host identity")
			}
			wantFunction := before.Function.Clone()
			wantFunction.EnvVars, wantFunction.Version, wantFunction.UpdatedAt = c.Env, 2, r.Function.UpdatedAt
			if !reflect.DeepEqual(wantFunction, r.Function) {
				t.Fatal("publication changed unrelated function fields")
			}
			p, audit := environmentPublishPolicy(t, db)
			if !reflect.DeepEqual(p, r.Policy) || p.Revision != oldPolicy.Revision+1 ||
				p.AuthorityEpoch != oldPolicy.AuthorityEpoch+1 || !p.ReviewRequired || p.Quarantined != quarantine ||
				p.ApprovedArtifact != oldPolicy.ApprovedArtifact || p.PolicyJSON != oldPolicy.PolicyJSON ||
				!p.CreatedAt.Equal(oldPolicy.CreatedAt) || !p.UpdatedAt.Equal(r.Artifact.UpdatedAt) ||
				!p.UpdatedAt.Equal(r.Function.UpdatedAt) {
				t.Fatal("publication rebound grants or failed to advance deny metadata")
			}
			e, err = p.AdmissionEvaluator()
			if err != nil || e.Metadata().Revision != p.Revision || e.Allows(call, req) ||
				e.Accepts(iamTestOther, workloadiam.F2F) || e.Accepts(iamTestOther, workloadiam.Job) {
				t.Fatal("retained policy did not compile empty outbound/inbound authority")
			}
			data, _ := json.Marshal(p)
			digest := sha256.Sum256(data)
			if len(audit) != len(oldAudit)+1 || audit[0].Digest != hex.EncodeToString(digest[:]) ||
				audit[0].Revision != p.Revision || audit[0].AuthorityEpoch != p.AuthorityEpoch || audit[0].Actor != c.Actor {
				t.Fatal("audit did not bind committed policy")
			}
			metadata, _ := json.Marshal(audit)
			if strings.Contains(string(metadata), c.Env["CONFIG"]) || strings.Contains(string(metadata), key) {
				t.Fatal("audit retained private input")
			}
			stored, err := db.GetFunction(c.OwnerID)
			if err != nil {
				t.Fatal(err)
			}
			c.Env["CONFIG"] = "input-mutated"
			if r.Function.EnvVars["CONFIG"] == c.Env["CONFIG"] {
				t.Fatal("result aliases input")
			}
			r.Function.EnvVars["CONFIG"], r.Policy.PolicyJSON, r.Artifact.Generation = "result-mutated", "mutated", ids.New()
			got, err := db.GetFunction(c.OwnerID)
			if err != nil || !reflect.DeepEqual(got, stored) {
				t.Fatal("result/input mutation changed durable function")
			}
			iamTestUnchanged(t, db, p, audit)
			artifactTestUnchanged(t, db, otherBefore)
		})
	}
}

func TestPublishWorkloadEnvironmentRollback(t *testing.T) {
	for _, failure := range []string{"generation", "function", "policy", "audit", "retention", "commit", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			db, c := environmentPublishSetup(t)
			before := artifactTestSnapshot(t, db, c.OwnerID)
			p, audit := environmentPublishPolicy(t, db)
			var trigger string
			switch failure {
			case "generation":
				trigger = `CREATE TRIGGER publish_fail BEFORE UPDATE ON workload_artifact_generations BEGIN SELECT RAISE(ABORT,'private-trigger-value'); END`
			case "function":
				trigger = `CREATE TRIGGER publish_fail BEFORE UPDATE ON functions BEGIN SELECT RAISE(ABORT,'private-trigger-value'); END`
			case "policy":
				trigger = `CREATE TRIGGER publish_fail BEFORE UPDATE ON workload_iam_policies BEGIN SELECT RAISE(ABORT,'private-trigger-value'); END`
			case "audit":
				trigger = `CREATE TRIGGER publish_fail BEFORE INSERT ON workload_iam_audit BEGIN SELECT RAISE(ABORT,'private-trigger-value'); END`
			case "retention":
				trigger = `CREATE TRIGGER publish_fail BEFORE DELETE ON workload_iam_audit BEGIN SELECT RAISE(ABORT,'private-trigger-value'); END`
				for range IAMAuditRetention {
					var err error
					p, err = db.SaveIAMPolicy(context.Background(), iamTestChange(p, p.PolicyJSON))
					if err != nil {
						t.Fatal(err)
					}
				}
				c.ExpectedPolicyRevision = p.Revision
				p, audit = environmentPublishPolicy(t, db)
			case "commit":
				trigger = `CREATE TABLE publish_guard (owner TEXT REFERENCES functions(id) DEFERRABLE INITIALLY DEFERRED);
					CREATE TRIGGER publish_fail AFTER INSERT ON workload_iam_audit BEGIN INSERT INTO publish_guard VALUES('absent'); END`
			}
			if trigger != "" {
				if _, err := db.write.Exec(trigger); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			wantErr := ErrWorkloadArtifactStorage
			if failure == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				wantErr = context.Canceled
			}
			r, err := db.PublishWorkloadEnvironment(ctx, c)
			if r != nil || !errors.Is(err, wantErr) || strings.Contains(err.Error(), "private") {
				t.Fatalf("failure returned partial state/unsafe diagnostics: %v", err)
			}
			artifactTestUnchanged(t, db, before)
			iamTestUnchanged(t, db, p, audit)
		})
	}
}

func TestPublishWorkloadEnvironmentExactCAS(t *testing.T) {
	db, base := environmentPublishSetup(t)
	before := artifactTestSnapshot(t, db, base.OwnerID)
	p, audit := environmentPublishPolicy(t, db)
	for _, tt := range []struct {
		name string
		edit func(*WorkloadEnvironmentChange)
		want error
	}{
		{"function version", func(c *WorkloadEnvironmentChange) { c.ExpectedFunctionVersion++ }, ErrWorkloadArtifactConflict},
		{"artifact revision", func(c *WorkloadEnvironmentChange) { c.ExpectedRevision++ }, ErrWorkloadArtifactConflict},
		{"artifact epoch", func(c *WorkloadEnvironmentChange) { c.ExpectedEpoch++ }, ErrWorkloadArtifactConflict},
		{"artifact generation", func(c *WorkloadEnvironmentChange) { c.ExpectedGeneration = ids.New() }, ErrWorkloadArtifactConflict},
		{"policy revision", func(c *WorkloadEnvironmentChange) { c.ExpectedPolicyRevision++ }, ErrIAMConflict},
		{"stored approval", func(c *WorkloadEnvironmentChange) { c.ExpectedApprovedArtifact = c.ExpectedGeneration }, ErrIAMConflict},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := base
			tt.edit(&c)
			if r, err := db.PublishWorkloadEnvironment(context.Background(), c); r != nil || !errors.Is(err, tt.want) {
				t.Fatalf("inexact CAS: %v", err)
			}
			artifactTestUnchanged(t, db, before)
			iamTestUnchanged(t, db, p, audit)
		})
	}
	const contenders = 12
	start := make(chan struct{})
	results := make(chan error, contenders)
	var wg sync.WaitGroup
	for range contenders {
		wg.Go(func() {
			<-start
			_, err := db.PublishWorkloadEnvironment(context.Background(), base)
			results <- err
		})
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, ErrWorkloadArtifactConflict) {
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	p, audit = environmentPublishPolicy(t, db)
	s := artifactTestSnapshot(t, db, base.OwnerID)
	if winners != 1 || s.Function.Version != 2 || s.Artifact.Revision != 2 ||
		p.Revision != base.ExpectedPolicyRevision+1 || len(audit) != 3 {
		t.Fatal("publication CAS did not have exactly one complete winner")
	}
}

func TestPublishWorkloadEnvironmentValidation(t *testing.T) {
	db, base := environmentPublishSetup(t)
	before := artifactTestSnapshot(t, db, base.OwnerID)
	p, audit := environmentPublishPolicy(t, db)
	for _, tt := range []struct {
		name string
		edit func(*WorkloadEnvironmentChange)
	}{
		{"owner", func(c *WorkloadEnvironmentChange) { c.OwnerID = "bad" }},
		{"actor", func(c *WorkloadEnvironmentChange) { c.Actor.ID = "token" }},
		{"actor kind", func(c *WorkloadEnvironmentChange) { c.Actor.Kind = "bad" }},
		{"generation", func(c *WorkloadEnvironmentChange) { c.ExpectedGeneration = "bad" }},
		{"revision", func(c *WorkloadEnvironmentChange) { c.ExpectedRevision = 0 }},
		{"epoch", func(c *WorkloadEnvironmentChange) { c.ExpectedEpoch = math.MaxUint64 }},
		{"zero version", func(c *WorkloadEnvironmentChange) { c.ExpectedFunctionVersion = 0 }},
		{"negative version", func(c *WorkloadEnvironmentChange) { c.ExpectedFunctionVersion = -1 }},
		{"overflow version", func(c *WorkloadEnvironmentChange) { c.ExpectedFunctionVersion = math.MaxInt }},
		{"zero policy revision", func(c *WorkloadEnvironmentChange) { c.ExpectedPolicyRevision = 0 }},
		{"overflow policy revision", func(c *WorkloadEnvironmentChange) { c.ExpectedPolicyRevision = math.MaxInt64 + 1 }},
		{"invalid UTF8 name", func(c *WorkloadEnvironmentChange) { c.Env = map[string]string{"\xff": "v"} }},
		{"invalid UTF8 value", func(c *WorkloadEnvironmentChange) { c.Env = map[string]string{"K": "\xff"} }},
		{"empty name", func(c *WorkloadEnvironmentChange) { c.Env = map[string]string{"": "v"} }},
		{"NUL name", func(c *WorkloadEnvironmentChange) { c.Env = map[string]string{"K\x00": "v"} }},
		{"equals name", func(c *WorkloadEnvironmentChange) { c.Env = map[string]string{"K=x": "v"} }},
		{"NUL value", func(c *WorkloadEnvironmentChange) { c.Env = map[string]string{"K": "v\x00"} }},
		{"raw byte cap", func(c *WorkloadEnvironmentChange) {
			c.Env = map[string]string{"K": strings.Repeat("v", MaxWorkloadArtifactEnvBytes)}
		}},
		{"encoded byte cap", func(c *WorkloadEnvironmentChange) {
			c.Env = map[string]string{"K": strings.Repeat("\n", MaxWorkloadArtifactEnvBytes/2)}
		}},
		{"entry cap", func(c *WorkloadEnvironmentChange) {
			c.Env = map[string]string{}
			for i := range MaxWorkloadEnvironmentEntries + 1 {
				c.Env[fmt.Sprint(i)] = ""
			}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := base
			tt.edit(&c)
			r, err := db.PublishWorkloadEnvironment(context.Background(), c)
			if r != nil || !(errors.Is(err, ErrWorkloadArtifactInvalid) || errors.Is(err, ErrIAMInvalidPolicy)) {
				t.Fatalf("invalid input accepted: %v", err)
			}
			artifactTestUnchanged(t, db, before)
			iamTestUnchanged(t, db, p, audit)
		})
	}
}

func TestPublishWorkloadEnvironmentBoundsAndNil(t *testing.T) {
	db, c := environmentPublishSetup(t)
	envs := []map[string]string{nil, {}, {"K": strings.Repeat("v", MaxWorkloadArtifactEnvBytes-len(`{"K":""}`))}}
	full := map[string]string{}
	for i := range MaxWorkloadEnvironmentEntries {
		full[fmt.Sprint(i)] = ""
	}
	envs = append(envs, full)
	for _, env := range envs {
		c.Env = env
		r, err := db.PublishWorkloadEnvironment(context.Background(), c)
		if err != nil || r.Function.EnvVars == nil || len(r.Function.EnvVars) != len(env) {
			t.Fatalf("valid boundary/nil normalization failed: %v", err)
		}
		var stored string
		if err := db.read.QueryRow(`SELECT env_vars FROM functions WHERE id=?`, c.OwnerID).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if len(env) == 0 && stored != "{}" {
			t.Fatal("nil environment did not persist an empty object")
		}
		if r.Artifact.Generation == c.ExpectedGeneration {
			t.Fatal("even identical environment publication must rotate")
		}
		c.WorkloadArtifactChange = artifactTestChange(&r.Artifact)
		c.ExpectedFunctionVersion, c.ExpectedPolicyRevision = r.Function.Version, r.Policy.Revision
	}
}

func TestPublishWorkloadEnvironmentExhaustion(t *testing.T) {
	for _, column := range []string{"artifact revision", "artifact epoch", "policy revision", "policy epoch", "function version"} {
		t.Run(column, func(t *testing.T) {
			db, c := environmentPublishSetup(t)
			var statement string
			switch column {
			case "artifact revision":
				statement = `UPDATE workload_artifact_generations SET revision=?`
				c.ExpectedRevision = math.MaxInt64
			case "artifact epoch":
				statement = `UPDATE workload_artifact_generations SET epoch=?`
				c.ExpectedEpoch = math.MaxInt64
			case "policy revision":
				statement = `UPDATE workload_iam_policies SET revision=?`
				c.ExpectedPolicyRevision = math.MaxInt64
			case "policy epoch":
				statement = `UPDATE workload_iam_policies SET authority_epoch=?`
			case "function version":
				statement = `UPDATE functions SET version=?`
				c.ExpectedFunctionVersion = math.MaxInt
			}
			if _, err := db.write.Exec(statement, int64(math.MaxInt64)); err != nil {
				t.Fatal(err)
			}
			before := artifactTestSnapshot(t, db, c.OwnerID)
			p, audit := environmentPublishPolicy(t, db)
			want := ErrWorkloadArtifactExhausted
			if strings.HasPrefix(column, "policy") {
				want = ErrIAMRevisionExhausted
			} else if column == "function version" {
				want = ErrWorkloadArtifactInvalid
			}
			if r, err := db.PublishWorkloadEnvironment(context.Background(), c); r != nil || !errors.Is(err, want) {
				t.Fatalf("exhaustion did not deny: %v", err)
			}
			artifactTestUnchanged(t, db, before)
			iamTestUnchanged(t, db, p, audit)
		})
	}
}

func TestPublishWorkloadEnvironmentMissingAndInert(t *testing.T) {
	for _, missing := range []string{"function", "deleting", "artifact", "policy", "schema"} {
		t.Run(missing, func(t *testing.T) {
			db, c := environmentPublishSetup(t)
			var statement string
			want := ErrIAMFunctionNotFound
			switch missing {
			case "function":
				statement = `DELETE FROM functions`
			case "deleting":
				statement, want = `UPDATE functions SET status='deleting'`, ErrFunctionDeleting
			case "artifact":
				statement, want = `DELETE FROM workload_artifact_generations`, ErrWorkloadArtifactNotFound
			case "policy":
				statement, want = `DELETE FROM workload_iam_policies`, ErrIAMPolicyNotFound
			case "schema":
				statement, want = `DROP TABLE workload_artifact_generations`, ErrWorkloadArtifactStorage
			}
			if _, err := db.write.Exec(statement); err != nil {
				t.Fatal(err)
			}
			before, _ := db.GetFunction(c.OwnerID)
			if r, err := db.PublishWorkloadEnvironment(context.Background(), c); r != nil || !errors.Is(err, want) {
				t.Fatalf("missing/deleting state accepted: %v", err)
			}
			after, _ := db.GetFunction(c.OwnerID)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("missing metadata altered function")
			}
		})
	}
	db := newTestDB(t)
	fn := iamTestFunction(t, db, iamTestOwner, true)
	for range 2 {
		if err := db.Migrate(); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := db.read.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'workload_iam_%' OR name LIKE 'workload_artifact_%'`).Scan(&n); err != nil || n != 0 {
			t.Fatal("boot initialized staged metadata")
		}
	}
	got, err := db.GetFunction(fn.ID)
	if err != nil || !reflect.DeepEqual(fn, got) {
		t.Fatal("boot changed legacy configuration")
	}
}

func TestPublishWorkloadEnvironmentAuditRetention(t *testing.T) {
	db, c := environmentPublishSetup(t)
	for range IAMAuditRetention + 3 {
		r, err := db.PublishWorkloadEnvironment(context.Background(), c)
		if err != nil {
			t.Fatal(err)
		}
		c.WorkloadArtifactChange = artifactTestChange(&r.Artifact)
		c.ExpectedFunctionVersion, c.ExpectedPolicyRevision = r.Function.Version, r.Policy.Revision
	}
	p, audit := environmentPublishPolicy(t, db)
	if len(audit) != IAMAuditRetention || audit[0].Revision != p.Revision || audit[len(audit)-1].Revision != p.Revision-IAMAuditRetention+1 {
		t.Fatal("publication audit retention is not bounded/current")
	}
}

func TestPublishWorkloadEnvironmentSnapshotVisibility(t *testing.T) {
	ctx := context.Background()
	db, c := environmentPublishSetup(t)
	tx, err := db.read.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	before, err := readWorkloadArtifactTx(ctx, tx, c.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	oldPolicy, _ := environmentPublishPolicy(t, db)
	r, err := db.PublishWorkloadEnvironment(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := readWorkloadArtifactSnapshotTx(ctx, tx, c.OwnerID)
	if err != nil || pinned.Artifact != *before || pinned.Function.Version != c.ExpectedFunctionVersion ||
		pinned.Function.EnvVars["PRIVATE"] != "not-an-audit-value" {
		t.Fatalf("pinned snapshot mixed old/new inputs: %v", err)
	}
	policy, err := scanIAMPolicy(tx.QueryRowContext(ctx, iamPolicySelect, c.OwnerID))
	if err != nil || !reflect.DeepEqual(policy, oldPolicy) {
		t.Fatalf("pinned snapshot mixed policy with new generation: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	current := artifactTestSnapshot(t, db, c.OwnerID)
	policy, _ = environmentPublishPolicy(t, db)
	if current.Artifact != r.Artifact || !reflect.DeepEqual(current.Function, r.Function) || !reflect.DeepEqual(policy, r.Policy) {
		t.Fatal("fresh snapshot omitted committed publication")
	}
}

func TestPublishWorkloadEnvironmentLastSignedIncrement(t *testing.T) {
	db, c := environmentPublishSetup(t)
	if _, err := db.write.Exec(`UPDATE workload_artifact_generations SET revision=?,epoch=?`,
		int64(math.MaxInt64-1), int64(math.MaxInt64-1)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.write.Exec(`UPDATE workload_iam_policies SET revision=?,authority_epoch=?`,
		int64(math.MaxInt64-1), int64(math.MaxInt64-1)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.write.Exec(`UPDATE functions SET version=?`, math.MaxInt-1); err != nil {
		t.Fatal(err)
	}
	c.ExpectedRevision, c.ExpectedEpoch, c.ExpectedPolicyRevision = math.MaxInt64-1, math.MaxInt64-1, math.MaxInt64-1
	c.ExpectedFunctionVersion = math.MaxInt - 1
	r, err := db.PublishWorkloadEnvironment(context.Background(), c)
	if err != nil || r.Artifact.Revision != math.MaxInt64 || r.Artifact.Epoch != math.MaxInt64 ||
		r.Policy.Revision != math.MaxInt64 || r.Policy.AuthorityEpoch != math.MaxInt64 || r.Function.Version != math.MaxInt {
		t.Fatalf("last signed increment failed: %v", err)
	}
}
