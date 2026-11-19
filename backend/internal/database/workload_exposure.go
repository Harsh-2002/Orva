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
	"time"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
	"github.com/Harsh-2002/Orva/internal/ids"
)

var (
	ErrExposureInvalid     = errors.New("invalid exposure request")
	ErrExposureConflict    = errors.New("exposure or authority changed")
	ErrExposureUnavailable = errors.New("exposure unavailable")
)

type ExposureOwnerExpected struct {
	PolicyRevision uint64 `json:"policy_revision,string"`
	AuthorityEpoch uint64 `json:"authority_epoch,string"`
}

type ExposureInspection struct {
	Version        uint64                           `json:"version,string"`
	Expected       map[string]ExposureOwnerExpected `json:"expected"`
	Route          *Route                           `json:"route,omitempty"`
	RoutePublished bool                             `json:"route_published,omitempty"`
	InboundWebhook *InboundWebhook                  `json:"inbound_webhook,omitempty"`
	// InboundPublished reports whether this exact mapping is dispatchable.
	InboundPublished bool `json:"inbound_published,omitempty"`
}

// ExposureMutation contains authorable fields only; credential provenance is separate.
type ExposureMutation struct {
	Operation, Key, FunctionID             string
	Version                                uint64
	Expected                               map[string]ExposureOwnerExpected
	Name, SignatureHeader, SignatureFormat *string
	Active                                 *bool
	Methods                                string
}

type exposureState struct {
	version   uint64
	route     *Route
	published bool
	inbound   *InboundWebhook
}
type exposureOwner struct {
	sources    IAMSourceSnapshot
	version    int
	status     string
	privileged bool
}
type ExposurePlan struct {
	request ExposureMutation
	before  exposureState
	owners  map[string]exposureOwner
	after   *InboundWebhook
}

func exposureKind(op string) string {
	if strings.HasPrefix(op, "route_") {
		return "route"
	}
	return "inbound"
}

func boundedExposureText(s string, max int) bool {
	return s != "" && len(s) <= max && utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\r\n")
}

func readExposure(ctx context.Context, q iamQuery, kind, key string) (exposureState, error) {
	var out exposureState
	err := q.QueryRowContext(ctx, `SELECT version FROM workload_exposure_versions WHERE kind=? AND key=?`, kind, key).Scan(&out.version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, ErrExposureUnavailable
	}
	if kind == "route" {
		r := &Route{}
		err = q.QueryRowContext(ctx, `SELECT
 CASE WHEN length(CAST(path AS BLOB))<=4096 THEN path END,function_id,
 CASE WHEN length(CAST(methods AS BLOB))<=256 THEN methods END,created_at,
 EXISTS(SELECT 1 FROM workload_route_publications AS p WHERE p.path=routes.path
 AND p.version=? AND p.function_id=routes.function_id AND p.methods=routes.methods)
 FROM routes WHERE path=?`, out.version, key).Scan(&r.Path, &r.FunctionID, &r.Methods, &r.CreatedAt, &out.published)
		if err == nil {
			out.route = r
		}
	} else {
		w := &InboundWebhook{}
		var created int64
		var digest sql.NullString
		err = q.QueryRowContext(ctx, `SELECT id,function_id,
 CASE WHEN length(CAST(name AS BLOB))<=256 THEN name END,
 CASE WHEN length(CAST(secret AS BLOB))<=4096 THEN secret END,
 CASE WHEN length(CAST(signature_header AS BLOB))<=256 THEN signature_header END,
 CASE WHEN length(CAST(signature_format AS BLOB))<=64 THEN signature_format END,active,created_at,
 (SELECT p.secret_digest FROM workload_inbound_publications AS p WHERE p.id=inbound_webhooks.id
  AND p.version=? AND p.function_id=inbound_webhooks.function_id AND p.signature_format=inbound_webhooks.signature_format
  AND p.signature_header=inbound_webhooks.signature_header AND p.active=inbound_webhooks.active)
 FROM inbound_webhooks WHERE id=?`, out.version, key).Scan(&w.ID, &w.FunctionID, &w.Name, &w.Secret, &w.SignatureHeader, &w.SignatureFormat, &w.Active, &created, &digest)
		if err == nil {
			out.published = digest.Valid && digest.String == InboundSecretDigest(w.Secret)
			w.CreatedAt = time.UnixMilli(created).UTC()
			if len(w.Secret) >= 8 {
				w.SecretPreview = w.Secret[:8] + "…"
			}
			out.inbound = w
		}
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, ErrExposureUnavailable
	}
	return out, nil
}

func readExposureOwner(ctx context.Context, q iamQuery, owner string) (exposureOwner, error) {
	var out exposureOwner
	if !validIAMUUID(owner) {
		return out, ErrExposureUnavailable
	}
	var err error
	out.sources, err = readSources(ctx, q, owner)
	if err != nil {
		return out, ErrExposureUnavailable
	}
	if err = q.QueryRowContext(ctx, `SELECT version,status FROM functions WHERE id=?`, owner).Scan(&out.version, &out.status); err != nil || out.status == FunctionStatusDeleting {
		return out, ErrExposureUnavailable
	}
	projected, err := projectSources(out.sources.InlineJSON, out.sources.Attachments)
	if err != nil || projected != out.sources.Record.PolicyJSON {
		return out, ErrExposureConflict
	}
	out.privileged, err = configurationPrivileged(out.sources)
	if err != nil {
		return out, ErrExposureUnavailable
	}
	return out, nil
}

func exposureOwnerIDs(state exposureState, owner string) []string {
	owners := []string{}
	if owner != "" {
		owners = append(owners, owner)
	}
	if state.route != nil {
		owners = append(owners, state.route.FunctionID)
	}
	if state.inbound != nil {
		owners = append(owners, state.inbound.FunctionID)
	}
	slices.Sort(owners)
	return slices.Compact(owners)
}

func (db *Database) InspectExposure(ctx context.Context, ref OperatorCredentialReference, kind, key, owner string) (*ExposureInspection, error) {
	if (kind != "route" && kind != "inbound") || !boundedExposureText(key, 4096) {
		return nil, ErrExposureInvalid
	}
	tx, err := db.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, ErrExposureUnavailable
	}
	defer tx.Rollback()
	_, expiry, err := authorizeOperatorPermissionTx(ctx, tx, ref, "read")
	if err != nil {
		return nil, err
	}
	state, err := readExposure(ctx, tx, kind, key)
	if err != nil {
		return nil, err
	}
	if kind == "inbound" && state.inbound != nil && state.inbound.FunctionID != owner {
		return nil, ErrExposureUnavailable
	}
	out := &ExposureInspection{Version: state.version, Expected: map[string]ExposureOwnerExpected{}, Route: state.route, InboundWebhook: state.inbound}
	if kind == "route" {
		out.RoutePublished = state.published
	} else {
		out.InboundPublished = state.published
	}
	for _, id := range exposureOwnerIDs(state, owner) {
		o, e := readExposureOwner(ctx, tx, id)
		if e != nil {
			return nil, e
		}
		p := o.sources.Record
		out.Expected[id] = ExposureOwnerExpected{p.Revision, p.AuthorityEpoch}
	}
	if len(out.Expected) == 0 {
		return nil, ErrExposureUnavailable
	}
	if out.InboundWebhook != nil {
		out.InboundWebhook.Secret = ""
	}
	if ctx.Err() != nil || (!expiry.IsZero() && !expiry.After(time.Now().UTC())) {
		return nil, operatorDenied(ctx)
	}
	if tx.Commit() != nil {
		return nil, ErrExposureUnavailable
	}
	return out, nil
}

func (db *Database) PrepareExposure(ctx context.Context, c ExposureMutation) (*ExposurePlan, error) {
	switch c.Operation {
	case "route_set", "route_delete", "inbound_create", "inbound_update", "inbound_delete":
	default:
		return nil, ErrExposureInvalid
	}
	if !boundedExposureText(c.Key, 4096) || c.Version >= math.MaxInt64 || len(c.Expected) < 1 || len(c.Expected) > 2 {
		return nil, ErrExposureInvalid
	}
	kind := exposureKind(c.Operation)
	if kind == "route" {
		if !validManagedRoutePattern(c.Key) {
			return nil, ErrExposureInvalid
		}
		if c.Methods == "" {
			c.Methods = "*"
		}
		if !boundedExposureText(c.Methods, 256) {
			return nil, ErrExposureInvalid
		}
	} else if !validIAMUUID(c.Key) || !validIAMUUID(c.FunctionID) {
		return nil, ErrExposureInvalid
	}
	if c.Operation == "route_set" && !validIAMUUID(c.FunctionID) {
		return nil, ErrExposureInvalid
	}
	if c.Operation == "route_delete" && c.FunctionID != "" {
		return nil, ErrExposureInvalid
	}
	tx, err := db.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, ErrExposureUnavailable
	}
	defer tx.Rollback()
	state, err := readExposure(ctx, tx, kind, c.Key)
	if err != nil {
		return nil, err
	}
	if state.version != c.Version {
		return nil, ErrExposureConflict
	}
	if c.Operation == "inbound_create" {
		if state.inbound != nil || state.version != 0 {
			return nil, ErrExposureConflict
		}
	} else if kind == "inbound" && (state.inbound == nil || state.inbound.FunctionID != c.FunctionID) {
		return nil, ErrExposureUnavailable
	}
	if c.Operation == "route_delete" && state.route == nil {
		return nil, ErrExposureUnavailable
	}
	p := &ExposurePlan{request: c, before: state, owners: map[string]exposureOwner{}}
	for _, id := range exposureOwnerIDs(state, c.FunctionID) {
		o, e := readExposureOwner(ctx, tx, id)
		if e != nil {
			return nil, e
		}
		r := o.sources.Record
		if c.Expected[id] != (ExposureOwnerExpected{r.Revision, r.AuthorityEpoch}) {
			return nil, ErrExposureConflict
		}
		p.owners[id] = o
	}
	if len(p.owners) != len(c.Expected) {
		return nil, ErrExposureConflict
	}
	if kind == "inbound" && c.Operation != "inbound_delete" {
		row := InboundWebhook{ID: c.Key, FunctionID: c.FunctionID, SignatureFormat: "hmac_sha256_hex", Active: true}
		if state.inbound != nil {
			row = *state.inbound
		}
		if c.Name != nil {
			row.Name = strings.TrimSpace(*c.Name)
		}
		if c.SignatureFormat != nil {
			format := strings.TrimSpace(*c.SignatureFormat)
			if row.SignatureHeader == DefaultSignatureHeader(row.SignatureFormat) {
				row.SignatureHeader = DefaultSignatureHeader(format)
			}
			row.SignatureFormat = format
		}
		if c.SignatureHeader != nil {
			row.SignatureHeader = strings.TrimSpace(*c.SignatureHeader)
		}
		if c.Active != nil {
			row.Active = *c.Active
		}
		if row.SignatureHeader == "" && c.Operation == "inbound_create" {
			row.SignatureHeader = DefaultSignatureHeader(row.SignatureFormat)
		}
		if _, ok := AllowedInboundFormats[row.SignatureFormat]; !ok {
			return nil, ErrExposureInvalid
		}
		if !boundedExposureText(row.Name, 256) || !validExposureHeader(row.SignatureHeader) {
			return nil, ErrExposureInvalid
		}
		if c.Operation == "inbound_create" {
			row.Secret = NewInboundWebhookSecret()
			row.CreatedAt = time.Now().UTC()
			row.SecretPreview = row.Secret[:8] + "…"
		}
		p.after = &row
	}
	if tx.Commit() != nil {
		return nil, ErrExposureUnavailable
	}
	return p, nil
}

func validExposureHeader(s string) bool {
	if !boundedExposureText(s, 256) {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", c)) {
			return false
		}
	}
	return true
}

func (p *ExposurePlan) Owners() []string {
	out := make([]string, 0, len(p.owners))
	for id := range p.owners {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}
func (p *ExposurePlan) Metadata() []workloadiam.Metadata {
	out := []workloadiam.Metadata{}
	for _, id := range p.Owners() {
		r := p.owners[id].sources.Record
		out = append(out, workloadiam.Metadata{OwnerID: id, Revision: r.Revision, ApprovedArtifact: r.ApprovedArtifact})
	}
	return out
}

// CommitExposure is persistence-only; shared service holds sorted owners then IAM.
func (db *Database) CommitExposure(ctx context.Context, ref OperatorCredentialReference, p *ExposurePlan) (*InboundWebhook, error) {
	if p == nil || len(p.owners) == 0 {
		return nil, ErrExposureInvalid
	}
	db.lifecycleMu.RLock()
	defer db.lifecycleMu.RUnlock()
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, ErrExposureUnavailable
	}
	defer tx.Rollback()
	required := "write"
	changed := false
	for _, id := range p.Owners() {
		o := p.owners[id]
		current, e := readSources(ctx, tx, id)
		if e != nil {
			return nil, ErrExposureUnavailable
		}
		if o.privileged || !sameSourceState(current, o.sources) {
			required = "admin"
		}
		changed = changed || !sameSourceState(current, o.sources)
		var version int
		var status string
		if tx.QueryRowContext(ctx, `SELECT version,status FROM functions WHERE id=?`, id).Scan(&version, &status) != nil || status == FunctionStatusDeleting {
			return nil, ErrExposureUnavailable
		}
		changed = changed || version != o.version || status != o.status
	}
	actor, expiry, err := authorizeOperatorPermissionTx(ctx, tx, ref, required)
	if err != nil && required == "write" {
		required = "admin"
		actor, expiry, err = authorizeOperatorPermissionTx(ctx, tx, ref, required)
	}
	if err != nil {
		return nil, err
	}
	if changed {
		return nil, ErrExposureConflict
	}
	c := p.request
	kind := exposureKind(c.Operation)
	current, err := readExposure(ctx, tx, kind, c.Key)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(current, p.before) {
		return nil, ErrExposureConflict
	}
	switch c.Operation {
	case "route_set":
		_, err = tx.ExecContext(ctx, `INSERT INTO routes(path,function_id,methods) VALUES(?,?,?) ON CONFLICT(path) DO UPDATE SET function_id=excluded.function_id,methods=excluded.methods`, c.Key, c.FunctionID, c.Methods)
	case "route_delete":
		_, err = tx.ExecContext(ctx, `DELETE FROM routes WHERE path=?`, c.Key)
	case "inbound_create":
		w := p.after
		_, err = tx.ExecContext(ctx, `INSERT INTO inbound_webhooks(id,function_id,name,secret,signature_header,signature_format,active,created_at) VALUES(?,?,?,?,?,?,?,?)`, w.ID, w.FunctionID, w.Name, w.Secret, w.SignatureHeader, w.SignatureFormat, w.Active, w.CreatedAt.UnixMilli())
	case "inbound_update":
		w := p.after
		_, err = tx.ExecContext(ctx, `UPDATE inbound_webhooks SET name=?,signature_header=?,signature_format=?,active=? WHERE id=?`, w.Name, w.SignatureHeader, w.SignatureFormat, w.Active, w.ID)
	case "inbound_delete":
		_, err = tx.ExecContext(ctx, `DELETE FROM inbound_webhooks WHERE id=?`, c.Key)
	}
	if err != nil {
		return nil, ErrExposureUnavailable
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO workload_exposure_versions(kind,key,version) VALUES(?,?,?) ON CONFLICT(kind,key) DO UPDATE SET version=excluded.version`, kind, c.Key, c.Version+1)
	if err != nil {
		return nil, ErrExposureUnavailable
	}
	switch c.Operation {
	case "inbound_create", "inbound_update":
		// The row triggers cleared any prior publication; this is the sole way to publish.
		w := p.after
		_, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO workload_inbound_publications(id,version,function_id,signature_format,signature_header,active,secret_digest)
 VALUES(?,?,?,?,?,?,?)`, w.ID, c.Version+1, w.FunctionID, w.SignatureFormat, w.SignatureHeader, boolToInt(w.Active), InboundSecretDigest(w.Secret))
		if err != nil {
			return nil, ErrExposureUnavailable
		}
	case "inbound_delete":
		if _, err = tx.ExecContext(ctx, `DELETE FROM workload_inbound_publications WHERE id=?`, c.Key); err != nil {
			return nil, ErrExposureUnavailable
		}
	}
	if c.Operation == "route_set" {
		// Updating the mapping clears its prior publication, even for same-value legacy writes.
		_, err = tx.ExecContext(ctx, `INSERT INTO workload_route_publications(path,version,function_id,methods) VALUES(?,?,?,?)`,
			c.Key, c.Version+1, c.FunctionID, c.Methods)
		if err != nil {
			return nil, ErrExposureUnavailable
		}
	}
	// Audit hashes only public mapping identifiers, never signature secrets or bodies.
	raw, _ := json.Marshal([]any{c.Operation, c.Key, c.FunctionID, c.Version + 1, p.Owners()})
	sum := sha256.Sum256(raw)
	event, now := ids.New(), time.Now().UTC().Format(time.RFC3339Nano)
	for _, id := range p.Owners() {
		r := p.owners[id].sources.Record
		_, err = tx.ExecContext(ctx, `INSERT INTO workload_exposure_audit VALUES(?,?,?,?,?,?,?,?,?,?)`, event, id, c.Operation, c.Version+1, r.Revision, r.AuthorityEpoch, hex.EncodeToString(sum[:]), actor.Kind, actor.ID, now)
		if err != nil {
			return nil, ErrExposureUnavailable
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM workload_exposure_audit WHERE function_id=? AND id NOT IN (SELECT id FROM workload_exposure_audit WHERE function_id=? ORDER BY id DESC LIMIT 32)`, id, id)
		if err != nil {
			return nil, ErrExposureUnavailable
		}
	}
	if _, _, err = authorizeOperatorPermissionTx(ctx, tx, ref, required); err != nil {
		return nil, err
	}
	if ctx.Err() != nil || (!expiry.IsZero() && !expiry.After(time.Now().UTC())) {
		return nil, operatorDenied(ctx)
	}
	if tx.Commit() != nil {
		return nil, ErrExposureUnavailable
	}
	if p.after != nil {
		out := *p.after
		return &out, nil
	}
	return nil, nil
}
