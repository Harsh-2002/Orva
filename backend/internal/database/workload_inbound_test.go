package database

import (
	"errors"
	"strings"
	"testing"

	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestManagedInboundLookupPublicationAndRestore(t *testing.T) {
	db, ref, owner := exposureSetup(t, "api_key")
	ctx := t.Context()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	operatorExec(t, db, `UPDATE api_keys SET permissions='["read","admin"]' WHERE id=?`, ref.ID)
	name := "published trigger"
	id := ids.New()
	create := ExposureMutation{Operation: "inbound_create", Key: id, FunctionID: owner, Expected: exposureExpected(t, db, owner), Name: &name}
	row, err := db.CommitExposure(ctx, ref, exposurePrepare(t, db, create))
	must(err)
	secret := row.Secret

	got, err := db.MatchManagedInbound(ctx, id)
	must(err)
	if got == nil || got.FunctionID != owner || got.Secret != secret || !got.Active || got.SignatureFormat != "hmac_sha256_hex" {
		t.Fatalf("published row did not resolve: %+v", got)
	}
	if got, err := db.MatchManagedInbound(ctx, "not-a-uuid"); err != nil || got != nil {
		t.Fatal("non-UUID selector matched", got, err)
	}
	if got, err := db.MatchManagedInbound(ctx, ids.New()); err != nil || got != nil {
		t.Fatal("unknown id matched", got, err)
	}
	snap, err := db.InspectExposure(ctx, ref, "inbound", id, owner)
	must(err)
	if !snap.InboundPublished || snap.Version != 1 || snap.InboundWebhook == nil || snap.InboundWebhook.Secret != "" {
		t.Fatalf("inspection did not report a secret-free published mapping: %+v", snap)
	}

	// Raw legacy writes never inherit publication, even same-value ones.
	operatorExec(t, db, `UPDATE inbound_webhooks SET name=name WHERE id=?`, id)
	if got, _ := db.MatchManagedInbound(ctx, id); got != nil {
		t.Fatal("legacy update stayed published")
	}
	snap, err = db.InspectExposure(ctx, ref, "inbound", id, owner)
	must(err)
	if snap.InboundPublished || snap.Version != 1 {
		t.Fatalf("legacy write changed CAS or stayed published: %+v", snap)
	}
	// A managed update (no field changes) is the explicit republication.
	update := ExposureMutation{Operation: "inbound_update", Key: id, FunctionID: owner, Version: 1, Expected: exposureExpected(t, db, owner)}
	_, err = db.CommitExposure(ctx, ref, exposurePrepare(t, db, update))
	must(err)
	if got, _ := db.MatchManagedInbound(ctx, id); got == nil || got.Secret != secret {
		t.Fatal("managed update did not republish")
	}

	// Tampering with any attested column or the secret unpublishes.
	for _, stmt := range []string{
		`UPDATE inbound_webhooks SET signature_format='github' WHERE id=?`,
		`UPDATE inbound_webhooks SET active=0 WHERE id=?`,
		`UPDATE inbound_webhooks SET secret='` + strings.Repeat("a", 64) + `' WHERE id=?`,
	} {
		operatorExec(t, db, stmt, id)
		if got, _ := db.MatchManagedInbound(ctx, id); got != nil {
			t.Fatalf("%s left a publication", stmt)
		}
		operatorExec(t, db, `UPDATE inbound_webhooks SET signature_format='hmac_sha256_hex',active=1,secret=? WHERE id=?`, secret, id)
	}
	// A forged publication row with the wrong secret digest, version or owner is refused.
	operatorExec(t, db, `INSERT OR REPLACE INTO workload_inbound_publications VALUES(?,?,?,?,?,1,?)`,
		id, 2, owner, "hmac_sha256_hex", "X-Orva-Signature", strings.Repeat("0", 64))
	if got, _ := db.MatchManagedInbound(ctx, id); got != nil {
		t.Fatal("wrong secret digest matched")
	}
	operatorExec(t, db, `INSERT OR REPLACE INTO workload_inbound_publications VALUES(?,?,?,?,?,1,?)`,
		id, 1, owner, "hmac_sha256_hex", "X-Orva-Signature", InboundSecretDigest(secret))
	if got, _ := db.MatchManagedInbound(ctx, id); got != nil {
		t.Fatal("stale publication version matched")
	}
	update.Version = 2
	update.Expected = exposureExpected(t, db, owner)
	_, err = db.CommitExposure(ctx, ref, exposurePrepare(t, db, update))
	must(err)
	if got, _ := db.MatchManagedInbound(ctx, id); got == nil {
		t.Fatal("republish failed")
	}

	// Restore preserves the mapping and CAS counter but removes the grant.
	_, err = db.ApplyRecoveryForFixture(ctx, "")
	must(err)
	if got, _ := db.MatchManagedInbound(ctx, id); got == nil {
		t.Fatal("unchanged recovery operation cleared publication")
	}
	_, err = db.ApplyRecoveryForFixture(ctx, ids.New())
	must(err)
	if got, _ := db.MatchManagedInbound(ctx, id); got != nil {
		t.Fatal("restore left the webhook dispatchable")
	}
	snap, err = db.InspectExposure(ctx, ref, "inbound", id, owner)
	must(err)
	if snap.InboundPublished || snap.InboundWebhook == nil || snap.Version != 3 {
		t.Fatalf("restore did not preserve mapping and CAS counter: %+v", snap)
	}
	// Restore quarantines the owner, so exact current expectations are required.
	update.Version = 3
	update.Expected = exposureExpected(t, db, owner)
	_, err = db.CommitExposure(ctx, ref, exposurePrepare(t, db, update))
	must(err)
	if got, _ := db.MatchManagedInbound(ctx, id); got == nil || got.Secret != secret {
		t.Fatal("explicit republish after restore failed")
	}

	// Delete clears the publication and keeps the version tombstone.
	del := ExposureMutation{Operation: "inbound_delete", Key: id, FunctionID: owner, Version: 4, Expected: exposureExpected(t, db, owner)}
	_, err = db.CommitExposure(ctx, ref, exposurePrepare(t, db, del))
	must(err)
	if got, _ := db.MatchManagedInbound(ctx, id); got != nil {
		t.Fatal("deleted webhook matched")
	}
	var n int
	must(db.read.QueryRow(`SELECT count(*) FROM workload_inbound_publications WHERE id=?`, id).Scan(&n))
	if n != 0 {
		t.Fatal("deletion left a publication row")
	}
}

func TestManagedInboundLegacyRowsNeverDispatch(t *testing.T) {
	db, ref, owner := exposureSetup(t, "api_key")
	ctx := t.Context()
	w := &InboundWebhook{FunctionID: owner, Name: "legacy", Secret: NewInboundWebhookSecret(), Active: true}
	if err := db.InsertInboundWebhook(w); err != nil {
		t.Fatal(err)
	}
	if got, err := db.MatchManagedInbound(ctx, w.ID); err != nil || got != nil {
		t.Fatal("legacy row dispatched", got, err)
	}
	// Even with a forged publication row, the absent version high-water mark denies.
	operatorExec(t, db, `INSERT INTO workload_inbound_publications VALUES(?,?,?,?,?,1,?)`,
		w.ID, 1, owner, w.SignatureFormat, w.SignatureHeader, InboundSecretDigest(w.Secret))
	if got, _ := db.MatchManagedInbound(ctx, w.ID); got != nil {
		t.Fatal("publication without an exposure version dispatched")
	}
	operatorExec(t, db, `UPDATE api_keys SET permissions='["read"]' WHERE id=?`, ref.ID)
	snap, err := db.InspectExposure(ctx, ref, "inbound", w.ID, owner)
	if err != nil || snap.InboundPublished || snap.Version != 0 {
		t.Fatalf("legacy row reported as published: %+v %v", snap, err)
	}
}

func TestManagedInboundAuthorityAndCAS(t *testing.T) {
	db, ref, owner := exposureSetup(t, "api_key")
	ctx := t.Context()
	name := "authority"
	c := ExposureMutation{Operation: "inbound_create", Key: ids.New(), FunctionID: owner, Expected: exposureExpected(t, db, owner), Name: &name}

	// Read alone neither creates nor publishes.
	operatorExec(t, db, `UPDATE api_keys SET permissions='["read"]' WHERE id=?`, ref.ID)
	if _, err := db.CommitExposure(ctx, ref, exposurePrepare(t, db, c)); !errors.Is(err, ErrOperatorDenied) {
		t.Fatal("read-only credential created a webhook", err)
	}
	if got, _ := db.MatchManagedInbound(ctx, c.Key); got != nil {
		t.Fatal("denied create published")
	}
	if _, err := db.InspectExposure(ctx, ref, "inbound", c.Key, owner); err != nil && !errors.Is(err, ErrExposureUnavailable) {
		t.Fatal("read-only credential could not inspect", err)
	}
	// Write may mutate an empty, review-free owner.
	operatorExec(t, db, `UPDATE api_keys SET permissions='["write"]' WHERE id=?`, ref.ID)
	prepared := exposurePrepare(t, db, c)
	if _, err := db.CommitExposure(ctx, ref, prepared); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.MatchManagedInbound(ctx, c.Key); got == nil {
		t.Fatal("write-authorized create did not publish")
	}
	// Replaying the prepared plan is a CAS conflict, not a second publication.
	if _, err := db.CommitExposure(ctx, ref, prepared); err == nil {
		t.Fatal("replayed plan accepted")
	}
	// A stale version cannot prepare.
	c.Operation, c.Version = "inbound_update", 0
	if _, err := db.PrepareExposure(ctx, c); !errors.Is(err, ErrExposureConflict) {
		t.Fatal("stale version accepted", err)
	}
	// Revoked credential: key deleted before commit.
	c.Version = 1
	c.Expected = exposureExpected(t, db, owner)
	plan := exposurePrepare(t, db, c)
	operatorExec(t, db, `DELETE FROM api_keys WHERE id=?`, ref.ID)
	if _, err := db.CommitExposure(ctx, ref, plan); !errors.Is(err, ErrOperatorDenied) {
		t.Fatal("revoked credential committed", err)
	}
	// An expired credential cannot list.
	if _, _, _, err := db.ListInboundExposures(ctx, ref, owner); !errors.Is(err, ErrOperatorDenied) {
		t.Fatal("revoked credential listed", err)
	}
}

func TestManagedExposureListsAreSecretFreeAndBounded(t *testing.T) {
	db, ref, owner := exposureSetup(t, "api_key")
	ctx := t.Context()
	operatorExec(t, db, `UPDATE api_keys SET permissions='["read","write"]' WHERE id=?`, ref.ID)
	var secret string
	for _, name := range []string{"a", "b"} {
		n := name
		row, err := db.CommitExposure(ctx, ref, exposurePrepare(t, db, ExposureMutation{
			Operation: "inbound_create", Key: ids.New(), FunctionID: owner, Expected: exposureExpected(t, db, owner), Name: &n}))
		if err != nil {
			t.Fatal(err)
		}
		secret = row.Secret
	}
	legacy := &InboundWebhook{FunctionID: owner, Name: "legacy", Secret: NewInboundWebhookSecret(), Active: true}
	if err := db.InsertInboundWebhook(legacy); err != nil {
		t.Fatal(err)
	}
	rows, expected, truncated, err := db.ListInboundExposures(ctx, ref, owner)
	if err != nil || truncated || len(rows) != 3 || len(expected) != 1 {
		t.Fatal("unexpected list", rows, expected, truncated, err)
	}
	published := 0
	for _, r := range rows {
		if r.Secret != "" || r.SecretPreview == "" {
			t.Fatal("list leaked or omitted preview", r)
		}
		if r.Published {
			published++
		} else if r.ID != legacy.ID {
			t.Fatal("managed row unpublished")
		}
	}
	if published != 2 || strings.Contains(secret, "…") {
		t.Fatal("publication flags wrong", published)
	}
	if _, _, _, err := db.ListInboundExposures(ctx, ref, "not-a-uuid"); !errors.Is(err, ErrExposureInvalid) {
		t.Fatal("literal owner not required", err)
	}
	if _, _, err := db.ListRouteExposures(ctx, ref); err != nil {
		t.Fatal(err)
	}
}
