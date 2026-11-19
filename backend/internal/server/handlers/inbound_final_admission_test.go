package handlers

import (
	"path/filepath"
	"testing"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/internal/ids"
)

// The publication that verified a signature must still be the exact one at
// final admission; any later commit that unpublishes or changes it is refused.
func TestInboundFinalAdmissionRechecksExactPublication(t *testing.T) {
	db, err := database.New(filepath.Join(t.TempDir(), "orva.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.InitManagedServingSchemas(t.Context()); err != nil {
		t.Fatal(err)
	}
	owner := ids.New()
	if err := db.InsertFunction(&database.Function{ID: owner, Name: "admit", Runtime: "node", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	row := &database.InboundWebhook{FunctionID: owner, Name: "hook", Secret: database.NewInboundWebhookSecret(), Active: true}
	if err := db.InsertInboundWebhook(row); err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.WriteDB().Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Publication is written last; the row triggers clear any earlier one.
	publish := func(version int) {
		t.Helper()
		exec(`INSERT INTO workload_exposure_versions(kind,key,version) VALUES('inbound',?,?) ON CONFLICT(kind,key) DO UPDATE SET version=excluded.version`, row.ID, version)
		exec(`INSERT OR REPLACE INTO workload_inbound_publications SELECT id,?,function_id,signature_format,signature_header,active,? FROM inbound_webhooks WHERE id=?`,
			version, database.InboundSecretDigest(row.Secret), row.ID)
	}
	h := &InboundTriggerHandler{DB: db, Managed: true}
	verified := func(t *testing.T) (*database.InboundWebhook, uint64) {
		t.Helper()
		w, v, err := db.MatchManagedInboundVersion(t.Context(), row.ID)
		if err != nil || w == nil {
			t.Fatal("published fixture did not resolve", err)
		}
		return w, v
	}
	for name, mutate := range map[string]func(){
		"raw edit": func() { exec(`UPDATE inbound_webhooks SET name='raw' WHERE id=?`, row.ID) },
		"disable":  func() { exec(`UPDATE inbound_webhooks SET active=0 WHERE id=?`, row.ID) },
		"format":   func() { exec(`UPDATE inbound_webhooks SET signature_format='github' WHERE id=?`, row.ID) },
		"header":   func() { exec(`UPDATE inbound_webhooks SET signature_header='X-Other' WHERE id=?`, row.ID) },
		"secret": func() {
			exec(`UPDATE inbound_webhooks SET secret=? WHERE id=?`, database.NewInboundWebhookSecret(), row.ID)
		},
		"delete":        func() { exec(`DELETE FROM inbound_webhooks WHERE id=?`, row.ID) },
		"restore":       func() { exec(`DELETE FROM workload_inbound_publications`) },
		"republication": func() { publish(2) },
	} {
		t.Run(name, func(t *testing.T) {
			exec(`DELETE FROM inbound_webhooks WHERE id=?`, row.ID)
			if err := db.InsertInboundWebhook(row); err != nil {
				t.Fatal(err)
			}
			publish(1)
			snapshot, version := verified(t)
			if status, _, _ := h.finalAdmission(t.Context(), snapshot, version); status != 0 {
				t.Fatalf("unchanged publication refused: %d", status)
			}
			mutate()
			if status, code, _ := h.finalAdmission(t.Context(), snapshot, version); status != 404 || code != "NOT_FOUND" {
				t.Fatalf("changed publication admitted or misreported: %d %s", status, code)
			}
		})
	}
}
