package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

const (
	MaxIAMPolicyOwners = 25
	MaxIAMAttachments  = 8
)

type IAMManagedPolicy struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	DefinitionRevision uint64 `json:"definition_revision,string"`
	MembershipRevision uint64 `json:"membership_revision,string"`
	LatestVersion      uint64 `json:"latest_version,string"`
	Revoked            bool   `json:"revoked"`
}

type IAMManagedVersion struct {
	PolicyID  string            `json:"policy_id"`
	Version   uint64            `json:"version,string"`
	Policy    json.RawMessage   `json:"policy"`
	Actor     IAMActorReference `json:"actor"`
	CreatedAt time.Time         `json:"created_at"`
}

type IAMAttachment struct {
	PolicyID   string `json:"policy_id"`
	Name       string `json:"name"`
	Version    uint64 `json:"version,string"`
	Enabled    bool   `json:"enabled"`
	Revoked    bool   `json:"revoked"`
	PolicyJSON string `json:"-"`
}

// Record is the effective projection, not the editable inline document.
type IAMSourceSnapshot struct {
	Record      *IAMPolicyRecord
	InlineJSON  string
	Attachments []IAMAttachment
}

type IAMOwnerExpectation struct {
	OwnerID          string
	Revision         uint64
	ApprovedArtifact string
	CurrentArtifact  string
}

// IAMManagedMutation is a host-service input, not a wire decoder or an identity.
// Attach also performs explicit version-pinned rollout to already-attached owners.
type IAMManagedMutation struct {
	RequireRead        bool   // Host management-adapter requirement, never an authorable field.
	Operation          string // create, update, attach, detach, revoke, delete, inline, review
	PolicyID           string
	Name               string
	PolicyJSON         []byte
	Version            uint64
	DefinitionRevision uint64
	MembershipRevision uint64
	Owners             []IAMOwnerExpectation
}

type IAMOwnerPublication struct {
	Expected  IAMOwnerExpectation
	Before    IAMPolicyRecord
	After     IAMPolicyRecord
	Reduction bool
}

// IAMManagedPlan owns preparation. No caller can replace its source proof or
// effective projection after validation; commit is still exact-CAS and single-use.
type IAMManagedPlan struct {
	request     IAMManagedMutation
	before      *IAMManagedPolicy
	after       *IAMManagedPolicy
	owners      []IAMOwnerPublication
	sources     []IAMSourceSnapshot
	newInline   string
	versionJSON string
	actor       IAMActorReference
	changedAt   time.Time
	auditDigest string
	committed   atomic.Pointer[IAMManagedPolicy]
}

func (p *IAMManagedPlan) Publications() []IAMOwnerPublication {
	if p == nil {
		return nil
	}
	return slices.Clone(p.owners)
}

func (p *IAMManagedPlan) Policy() *IAMManagedPolicy {
	if p == nil || p.after == nil {
		return nil
	}
	if saved := p.committed.Load(); saved != nil {
		out := *saved
		return &out
	}
	out := *p.after
	return &out
}

type iamQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

const managedSelect = `SELECT id,name,definition_revision,membership_revision,latest_version,revoked FROM workload_iam_managed WHERE id=?`

func readManaged(ctx context.Context, q iamQuery, id string) (*IAMManagedPolicy, error) {
	p := &IAMManagedPolicy{}
	var d, m, v int64
	err := q.QueryRowContext(ctx, managedSelect, id).Scan(&p.ID, &p.Name, &d, &m, &v, &p.Revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrIAMPolicyNotFound
	}
	if err != nil {
		return nil, err
	}
	if !validIAMUUID(p.ID) || d <= 0 || m <= 0 || v <= 0 {
		return nil, ErrIAMInvalidPolicy
	}
	p.DefinitionRevision, p.MembershipRevision, p.LatestVersion = uint64(d), uint64(m), uint64(v)
	return p, nil
}

func (db *Database) GetIAMManagedPolicy(ctx context.Context, id string) (*IAMManagedPolicy, error) {
	if !validIAMUUID(id) {
		return nil, ErrIAMInvalidPolicy
	}
	return readManaged(ctx, db.read, id)
}

func readManagedVersion(ctx context.Context, q iamQuery, id string, version uint64) (*IAMManagedVersion, error) {
	out := &IAMManagedVersion{PolicyID: id, Version: version}
	var body, created string
	err := q.QueryRowContext(ctx, `SELECT policy_json,actor_kind,actor_id,created_at FROM workload_iam_managed_versions WHERE policy_id=? AND version=?`,
		id, int64(version)).Scan(&body, &out.Actor.Kind, &out.Actor.ID, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrIAMPolicyNotFound
	}
	if err != nil {
		return nil, err
	}
	if out.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return nil, ErrIAMInvalidPolicy
	}
	out.Policy = json.RawMessage(body)
	return out, nil
}

func (db *Database) GetIAMManagedVersion(ctx context.Context, id string, version uint64) (*IAMManagedVersion, error) {
	if !validIAMUUID(id) || version == 0 || version > math.MaxInt64 {
		return nil, ErrIAMInvalidPolicy
	}
	return readManagedVersion(ctx, db.read, id, version)
}

func (db *Database) ListIAMManagedPolicies(ctx context.Context, offset int) ([]IAMManagedPolicy, error) {
	if offset < 0 {
		return nil, ErrIAMInvalidPolicy
	}
	rows, err := db.read.QueryContext(ctx, `SELECT id,name,definition_revision,membership_revision,latest_version,revoked FROM workload_iam_managed ORDER BY id LIMIT 100 OFFSET ?`, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IAMManagedPolicy{}
	for rows.Next() {
		var p IAMManagedPolicy
		if err := rows.Scan(&p.ID, &p.Name, &p.DefinitionRevision, &p.MembershipRevision, &p.LatestVersion, &p.Revoked); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func readSources(ctx context.Context, q iamQuery, owner string) (IAMSourceSnapshot, error) {
	var out IAMSourceSnapshot
	if err := iamFunctionExists(ctx, q, owner); err != nil {
		return out, err
	}
	// Raw bounded state is compared again at commit, without recompiling under the gate.
	r := &IAMPolicyRecord{}
	var created, updated string
	err := q.QueryRowContext(ctx, iamPolicySelect, owner).Scan(&r.OwnerID, &r.Revision, &r.AuthorityEpoch,
		&r.ApprovedArtifact, &r.ReviewRequired, &r.Quarantined, &r.PolicyJSON, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrIAMPolicyNotFound
	}
	if err != nil {
		return out, err
	}
	if r.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return out, ErrIAMInvalidPolicy
	}
	if r.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return out, ErrIAMInvalidPolicy
	}
	out.Record, out.Attachments = r, []IAMAttachment{}
	err = q.QueryRowContext(ctx, `SELECT policy_json FROM workload_iam_inline WHERE function_id=?`, owner).Scan(&out.InlineJSON)
	if err != nil {
		return out, ErrIAMInvalidPolicy
	}
	rows, err := q.QueryContext(ctx, `SELECT a.policy_id,p.name,a.version,a.enabled,p.revoked,v.policy_json
 FROM workload_iam_attachments a JOIN workload_iam_managed p ON p.id=a.policy_id
 JOIN workload_iam_managed_versions v ON v.policy_id=a.policy_id AND v.version=a.version
 WHERE a.function_id=? ORDER BY a.policy_id LIMIT 9`, owner)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var a IAMAttachment
		if err := rows.Scan(&a.PolicyID, &a.Name, &a.Version, &a.Enabled, &a.Revoked, &a.PolicyJSON); err != nil {
			return out, err
		}
		out.Attachments = append(out.Attachments, a)
	}
	if len(out.Attachments) > MaxIAMAttachments {
		return out, ErrIAMInvalidPolicy
	}
	return out, rows.Err()
}

func projectSources(inline string, attachments []IAMAttachment) (string, error) {
	p, err := workloadiam.Decode([]byte(inline))
	if err != nil {
		return "", ErrIAMInvalidPolicy
	}
	for _, a := range attachments {
		if !a.Enabled || a.Revoked {
			continue
		}
		part, err := workloadiam.Decode([]byte(a.PolicyJSON))
		if err != nil {
			return "", ErrIAMInvalidPolicy
		}
		p.Allow = append(p.Allow, part.Allow...)
		p.Inbound = append(p.Inbound, part.Inbound...)
	}
	if err := workloadiam.Validate(p); err != nil {
		return "", ErrIAMInvalidPolicy
	}
	body, err := json.Marshal(p)
	return string(body), err
}

func (db *Database) GetIAMSources(ctx context.Context, owner string) (*IAMSourceSnapshot, error) {
	if !validIAMUUID(owner) {
		return nil, ErrIAMInvalidPolicy
	}
	tx, err := db.read.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	s, err := readSources(ctx, tx, owner)
	if err != nil {
		return nil, err
	}
	projected, err := projectSources(s.InlineJSON, s.Attachments)
	if err != nil || projected != s.Record.PolicyJSON {
		return nil, ErrIAMConflict
	}
	if _, err := s.Record.AdmissionEvaluator(); err != nil {
		return nil, err
	}
	return &s, nil
}

func policyOwners(ctx context.Context, q iamQuery, id string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT function_id FROM workload_iam_attachments WHERE policy_id=? ORDER BY function_id LIMIT 26`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var owner string
		if err := rows.Scan(&owner); err != nil {
			return nil, err
		}
		out = append(out, owner)
	}
	if len(out) > MaxIAMPolicyOwners {
		return nil, ErrIAMInvalidPolicy
	}
	return out, rows.Err()
}

func canonicalManagedDocument(body []byte) (string, error) {
	p, err := workloadiam.Decode(body)
	if err != nil {
		return "", ErrIAMInvalidPolicy
	}
	// Reusable cron self selectors need a separate closed semantic. Do not bind
	// them to a fabricated owner or silently interpret fixed UUIDs as self.
	for _, s := range p.Allow {
		if s.Resource.OwnCron != nil {
			return "", ErrIAMInvalidPolicy
		}
	}
	for _, a := range p.Inbound {
		for _, trigger := range a.Triggers {
			if trigger == workloadiam.Cron {
				return "", ErrIAMInvalidPolicy
			}
		}
	}
	raw, err := json.Marshal(p)
	return string(raw), err
}

// PrepareIAMManagedMutation performs owned parsing/composition/compilation before
// owner locks or admission. Commit repeats source, membership and live-admin CAS.
func (db *Database) PrepareIAMManagedMutation(ctx context.Context, ref OperatorCredentialReference, r IAMManagedMutation) (*IAMManagedPlan, error) {
	if ctx == nil || len(r.Owners) > MaxIAMPolicyOwners {
		return nil, ErrIAMInvalidPolicy
	}
	r.Owners = slices.Clone(r.Owners)
	r.PolicyJSON = slices.Clone(r.PolicyJSON)
	slices.SortFunc(r.Owners, func(a, b IAMOwnerExpectation) int { return strings.Compare(a.OwnerID, b.OwnerID) })
	for i, e := range r.Owners {
		if !validIAMUUID(e.OwnerID) || e.Revision == 0 || e.Revision >= math.MaxInt64 || (i > 0 && r.Owners[i-1].OwnerID == e.OwnerID) {
			return nil, ErrIAMInvalidPolicy
		}
	}
	isDefinition := r.Operation == "create" || r.Operation == "update"
	isOwner := r.Operation == "inline" || r.Operation == "review"
	if (r.Operation != "create" && r.Name != "") ||
		(!isDefinition && r.Operation != "inline" && r.PolicyJSON != nil) ||
		(r.Operation != "attach" && r.Version != 0) ||
		(isOwner && (r.DefinitionRevision != 0 || r.MembershipRevision != 0)) {
		return nil, ErrIAMInvalidPolicy
	}
	switch r.Operation {
	case "create", "update", "delete":
		if len(r.Owners) != 0 {
			return nil, ErrIAMInvalidPolicy
		}
	case "inline", "review":
		if len(r.Owners) != 1 || r.PolicyID != "" {
			return nil, ErrIAMInvalidPolicy
		}
	case "attach", "detach":
		if len(r.Owners) == 0 {
			return nil, ErrIAMInvalidPolicy
		}
	case "revoke":
	default:
		return nil, ErrIAMInvalidPolicy
	}
	p := &IAMManagedPlan{request: r, changedAt: time.Now().UTC()}
	if isDefinition {
		var err error
		p.versionJSON, err = canonicalManagedDocument(r.PolicyJSON)
		if err != nil {
			return nil, err
		}
	} else if r.Operation == "inline" {
		doc, err := workloadiam.Decode(r.PolicyJSON)
		if err != nil {
			return nil, ErrIAMInvalidPolicy
		}
		body, _ := json.Marshal(doc)
		p.newInline = string(body)
	}
	tx, err := db.read.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	p.actor, _, err = authorizeOperatorTx(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	if r.RequireRead {
		if _, _, err := authorizeOperatorPermissionTx(ctx, tx, ref, "read"); err != nil {
			return nil, err
		}
	}
	if r.Operation == "create" {
		if r.PolicyID != "" || r.DefinitionRevision != 0 || r.MembershipRevision != 0 ||
			len(r.Name) == 0 || len(r.Name) > 128 || !utf8.ValidString(r.Name) || strings.TrimSpace(r.Name) != r.Name || strings.ContainsAny(r.Name, "\x00\r\n") {
			return nil, ErrIAMInvalidPolicy
		}
		p.after = &IAMManagedPolicy{ID: ids.New(), Name: r.Name, DefinitionRevision: 1, MembershipRevision: 1, LatestVersion: 1}
		p.prepareAuditDigest()
		return p, nil
	}
	if !isOwner {
		if !validIAMUUID(r.PolicyID) {
			return nil, ErrIAMInvalidPolicy
		}
		p.before, err = readManaged(ctx, tx, r.PolicyID)
		if err != nil {
			return nil, err
		}
		definitionCAS := r.Operation != "attach" && r.Operation != "detach"
		membershipCAS := r.Operation != "update"
		if (definitionCAS && p.before.DefinitionRevision != r.DefinitionRevision) ||
			(membershipCAS && p.before.MembershipRevision != r.MembershipRevision) {
			return nil, ErrIAMConflict
		}
		if p.before.Revoked && r.Operation != "detach" && r.Operation != "delete" {
			return nil, ErrIAMConflict
		}
		after := *p.before
		p.after = &after
		attached, err := policyOwners(ctx, tx, r.PolicyID)
		if err != nil {
			return nil, err
		}
		requested := make([]string, len(r.Owners))
		for i, e := range r.Owners {
			requested[i] = e.OwnerID
		}
		switch r.Operation {
		case "update":
			if after.DefinitionRevision == math.MaxInt64 || after.LatestVersion == math.MaxInt64 {
				return nil, ErrIAMRevisionExhausted
			}
			p.after.DefinitionRevision++
			p.after.LatestVersion++
		case "delete":
			if len(attached) != 0 {
				return nil, ErrIAMConflict
			}
			p.after = nil
		case "revoke":
			if !slices.Equal(attached, requested) {
				return nil, ErrIAMConflict
			}
			if after.DefinitionRevision == math.MaxInt64 || after.MembershipRevision == math.MaxInt64 {
				return nil, ErrIAMRevisionExhausted
			}
			p.after.Revoked = true
			p.after.DefinitionRevision++
			p.after.MembershipRevision++
		case "attach", "detach":
			if after.MembershipRevision == math.MaxInt64 {
				return nil, ErrIAMRevisionExhausted
			}
			// Terminal definitions remain immutable; detach still removes their
			// disabled references, allowing explicit deletion after inspection.
			p.after.MembershipRevision++
			if r.Operation == "attach" {
				if r.Version == 0 || r.Version > math.MaxInt64 {
					return nil, ErrIAMInvalidPolicy
				}
				version, err := readManagedVersion(ctx, tx, r.PolicyID, r.Version)
				if err != nil {
					return nil, err
				}
				p.versionJSON = string(version.Policy)
				all := slices.Clone(attached)
				for _, owner := range requested {
					if !slices.Contains(all, owner) {
						all = append(all, owner)
					}
				}
				if len(all) > MaxIAMPolicyOwners {
					return nil, ErrIAMInvalidPolicy
				}
			}
		}
	}
	for _, e := range r.Owners {
		s, err := readSources(ctx, tx, e.OwnerID)
		if err != nil {
			return nil, err
		}
		old := *s.Record
		if old.Revision != e.Revision || old.ApprovedArtifact != e.ApprovedArtifact {
			return nil, ErrIAMConflict
		}
		if old.AuthorityEpoch >= math.MaxInt64 {
			return nil, ErrIAMRevisionExhausted
		}
		effective, err := projectSources(s.InlineJSON, s.Attachments)
		if err != nil || effective != old.PolicyJSON {
			return nil, ErrIAMConflict
		}
		if _, err := old.AdmissionEvaluator(); err != nil {
			return nil, err
		}
		parts := slices.Clone(s.Attachments)
		inline := s.InlineJSON
		reduction := r.Operation == "detach" || r.Operation == "revoke"
		switch r.Operation {
		case "inline":
			inline = p.newInline
			doc, _ := workloadiam.Decode([]byte(inline))
			reduction = len(doc.Allow) == 0 && len(doc.Inbound) == 0
		case "review":
		case "attach", "detach", "revoke":
			index := slices.IndexFunc(parts, func(a IAMAttachment) bool { return a.PolicyID == r.PolicyID })
			if r.Operation == "attach" {
				a := IAMAttachment{PolicyID: r.PolicyID, Name: p.before.Name, Version: r.Version, Enabled: true, PolicyJSON: p.versionJSON}
				if index < 0 {
					parts = append(parts, a)
				} else {
					parts[index] = a
				}
			} else {
				if index < 0 {
					return nil, ErrIAMConflict
				}
				if r.Operation == "detach" {
					parts = slices.Delete(parts, index, index+1)
				} else {
					parts[index].Enabled = false
					parts[index].Revoked = true
				}
			}
		}
		if len(parts) > MaxIAMAttachments {
			return nil, ErrIAMInvalidPolicy
		}
		slices.SortFunc(parts, func(a, b IAMAttachment) int { return strings.Compare(a.PolicyID, b.PolicyID) })
		projected, err := projectSources(inline, parts)
		if err != nil {
			return nil, err
		}
		after := old
		after.Revision++
		after.AuthorityEpoch++
		after.PolicyJSON = projected
		after.UpdatedAt = p.changedAt
		if !reduction {
			after.ApprovedArtifact = e.CurrentArtifact
		}
		if r.Operation == "review" {
			after.Quarantined = false
			after.ReviewRequired = false
		}
		if _, err := after.AdmissionEvaluator(); err != nil {
			return nil, err
		}
		p.sources = append(p.sources, s)
		p.owners = append(p.owners, IAMOwnerPublication{Expected: e, Before: old, After: after, Reduction: reduction})
	}
	p.prepareAuditDigest()
	return p, nil
}

func sameSourceState(a, b IAMSourceSnapshot) bool { return reflect.DeepEqual(a, b) }

func (p *IAMManagedPlan) prepareAuditDigest() {
	raw, _ := json.Marshal(struct {
		Before, After *IAMManagedPolicy
		Operation     string
		VersionJSON   string
		Owners        []IAMOwnerPublication
	}{p.before, p.after, p.request.Operation, p.versionJSON, p.owners})
	h := sha256.Sum256(raw)
	p.auditDigest = hex.EncodeToString(h[:])
}

// CommitIAMManagedMutation must run under the service's sorted owner locks and
// IAM gate. It never compiles or installs authority, and commits exactly once.
func (db *Database) CommitIAMManagedMutation(ctx context.Context, ref OperatorCredentialReference, p *IAMManagedPlan) error {
	if p == nil || ctx == nil {
		return ErrIAMInvalidPolicy
	}
	db.lifecycleMu.RLock()
	defer db.lifecycleMu.RUnlock()
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return operatorDenied(ctx)
	}
	defer tx.Rollback()
	actor, _, err := authorizeOperatorTx(ctx, tx, ref)
	if err != nil || actor != p.actor {
		return operatorDenied(ctx)
	}
	if p.request.RequireRead {
		if _, _, err := authorizeOperatorPermissionTx(ctx, tx, ref, "read"); err != nil {
			return err
		}
	}
	r := p.request
	var committed *IAMManagedPolicy
	if p.after != nil {
		owned := *p.after
		committed = &owned
	}
	if p.before != nil {
		now, err := readManaged(ctx, tx, r.PolicyID)
		if err != nil {
			return err
		}
		switch r.Operation {
		case "update":
			if now.DefinitionRevision != p.before.DefinitionRevision || now.LatestVersion != p.before.LatestVersion ||
				now.Revoked != p.before.Revoked || now.Name != p.before.Name {
				return ErrIAMConflict
			}
			committed.MembershipRevision = now.MembershipRevision
		case "attach", "detach":
			if now.MembershipRevision != p.before.MembershipRevision || now.Revoked != p.before.Revoked {
				return ErrIAMConflict
			}
			committed.DefinitionRevision = now.DefinitionRevision
			committed.LatestVersion = now.LatestVersion
		default:
			if *now != *p.before {
				return ErrIAMConflict
			}
		}
		if r.Operation == "revoke" || r.Operation == "delete" {
			current, err := policyOwners(ctx, tx, r.PolicyID)
			if err != nil {
				return err
			}
			expected := make([]string, len(p.owners))
			for i, e := range p.owners {
				expected[i] = e.Expected.OwnerID
			}
			if !slices.Equal(current, expected) {
				return ErrIAMConflict
			}
		}
	}
	for i, c := range p.owners {
		current, err := readSources(ctx, tx, c.Expected.OwnerID)
		if err != nil {
			return err
		}
		if !sameSourceState(current, p.sources[i]) {
			return ErrIAMConflict
		}
	}
	if r.Operation == "create" {
		a := committed
		if _, err := tx.ExecContext(ctx, `INSERT INTO workload_iam_managed(id,name,definition_revision,membership_revision,latest_version,revoked) VALUES(?,?,?,?,?,0)`,
			a.ID, a.Name, int64(a.DefinitionRevision), int64(a.MembershipRevision), int64(a.LatestVersion)); err != nil {
			return ErrIAMConflict
		}
	} else if p.before != nil && r.Operation != "delete" {
		a := committed
		// A revoked policy permits membership cleanup only, never a new version,
		// grant, or transition back to active.
		if _, err := tx.ExecContext(ctx, `UPDATE workload_iam_managed SET definition_revision=?,membership_revision=?,latest_version=?,revoked=? WHERE id=?`,
			int64(a.DefinitionRevision), int64(a.MembershipRevision), int64(a.LatestVersion), a.Revoked, a.ID); err != nil {
			return err
		}
	}
	if r.Operation == "create" || r.Operation == "update" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO workload_iam_managed_versions(policy_id,version,policy_json,actor_kind,actor_id,created_at) VALUES(?,?,?,?,?,?)`,
			p.after.ID, int64(p.after.LatestVersion), p.versionJSON, p.actor.Kind, p.actor.ID, p.changedAt.Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	for _, c := range p.owners {
		owner := c.Expected.OwnerID
		switch r.Operation {
		case "inline":
			_, err = tx.ExecContext(ctx, `UPDATE workload_iam_inline SET policy_json=? WHERE function_id=?`, p.newInline, owner)
		case "attach":
			_, err = tx.ExecContext(ctx, `INSERT INTO workload_iam_attachments(function_id,policy_id,version,enabled) VALUES(?,?,?,1)
 ON CONFLICT(function_id,policy_id) DO UPDATE SET version=excluded.version,enabled=1`, owner, r.PolicyID, int64(r.Version))
		case "detach":
			_, err = tx.ExecContext(ctx, `DELETE FROM workload_iam_attachments WHERE function_id=? AND policy_id=?`, owner, r.PolicyID)
		case "revoke":
			_, err = tx.ExecContext(ctx, `UPDATE workload_iam_attachments SET enabled=0 WHERE function_id=? AND policy_id=?`, owner, r.PolicyID)
		}
		if err != nil {
			return err
		}
		a := c.After
		_, err = tx.ExecContext(ctx, `UPDATE workload_iam_policies SET revision=?,authority_epoch=?,approved_artifact=?,review_required=?,quarantined=?,policy_json=?,updated_at=? WHERE function_id=?`,
			int64(a.Revision), int64(a.AuthorityEpoch), a.ApprovedArtifact, a.ReviewRequired, a.Quarantined, a.PolicyJSON, a.UpdatedAt.Format(time.RFC3339Nano), owner)
		if err != nil {
			return err
		}
		if err := appendIAMAudit(ctx, tx, &a, actor); err != nil {
			return err
		}
	}
	if p.before != nil || p.after != nil {
		id := r.PolicyID
		if p.after != nil {
			id = p.after.ID
		}
		if err := appendManagedAuditTx(ctx, tx, id, r.Operation, p.auditDigest, actor, p.changedAt); err != nil {
			return err
		}
	}
	if r.Operation == "delete" {
		if _, err := tx.ExecContext(ctx, `DELETE FROM workload_iam_managed_versions WHERE policy_id=?`, r.PolicyID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM workload_iam_managed WHERE id=?`, r.PolicyID); err != nil {
			return err
		}
	}
	if _, _, err := authorizeOperatorTx(ctx, tx, ref); err != nil || ctx.Err() != nil {
		return operatorDenied(ctx)
	}
	if p.request.RequireRead {
		if _, _, err := authorizeOperatorPermissionTx(ctx, tx, ref, "read"); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		if ctx.Err() != nil {
			return operatorDenied(ctx)
		}
		return err
	}
	if committed != nil {
		p.committed.Store(committed)
	}
	return nil
}
