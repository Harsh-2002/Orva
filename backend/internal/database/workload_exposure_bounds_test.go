package database

import (
	"strings"
	"testing"
)

func TestExposureOversizedExistingMappingDenied(t *testing.T) {
	db, _, old := exposureSetup(t, "api_key")
	next := exposureSecondOwner(t, db, old)
	exposureGrant(t, db, old, "inbound")
	if err := db.UpsertRoute("/oversized", old, strings.Repeat("G", 257)); err != nil {
		t.Fatal(err)
	}
	_, err := db.PrepareExposure(t.Context(), ExposureMutation{Operation: "route_set", Key: "/oversized", FunctionID: next, Expected: exposureExpected(t, db, next)})
	if err == nil {
		t.Fatal("oversized old route was treated as absent; protected owner omitted")
	}
}
