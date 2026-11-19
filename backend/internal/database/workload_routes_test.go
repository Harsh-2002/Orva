package database

import (
	"testing"

	"github.com/Harsh-2002/Orva/internal/ids"
)

func TestManagedRouteLookupAndRestore(t *testing.T) {
	db, ref, owner := exposureSetup(t, "api_key")
	ctx := t.Context()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	operatorExec(t, db, `UPDATE api_keys SET permissions='["read","admin"]' WHERE id=?`, ref.ID)
	set := func(path string, version uint64) {
		t.Helper()
		m := ExposureMutation{Operation: "route_set", Key: path, FunctionID: owner, Methods: "POST",
			Version: version, Expected: exposureExpected(t, db, owner)}
		_, err := db.CommitExposure(ctx, ref, exposurePrepare(t, db, m))
		must(err)
	}
	match := func(path, pattern, strip string) {
		t.Helper()
		got, prefix, err := db.MatchManagedRoute(ctx, path)
		must(err)
		if pattern == "" {
			if got != nil || prefix != "" {
				t.Fatalf("%q unexpectedly selected %+v with prefix %q", path, got, prefix)
			}
		} else if got == nil || got.Path != pattern || got.FunctionID != owner || prefix != strip {
			t.Fatalf("%q selected %+v with prefix %q; want %q with %q", path, got, prefix, pattern, strip)
		}
	}
	set("/cafè/*", 0)
	set("/cafè/雪/*", 0)
	set("/cafè/雪/exact", 0)
	must(db.UpsertRoute("/cafè/雪/legacy", owner, "*"))
	match("/cafè/雪/legacy", "/cafè/雪/*", "/cafè/雪/")
	match("/cafè/雪/exact", "/cafè/雪/exact", "/cafè/雪/exact")
	match("/cafè/雪/", "/cafè/雪/*", "/cafè/雪/")
	match("/cafè/雪", "/cafè/*", "/cafè/")
	match("/cafè", "", "")
	match("/Cafè/雪/exact", "", "")

	// Even same-value updates and raw recreation cannot inherit an old version.
	must(db.UpsertRoute("/cafè/雪/exact", owner, "POST"))
	match("/cafè/雪/exact", "/cafè/雪/*", "/cafè/雪/")
	set("/cafè/雪/exact", 1)
	must(db.DeleteRoute("/cafè/雪/exact"))
	must(db.UpsertRoute("/cafè/雪/exact", owner, "POST"))
	match("/cafè/雪/exact", "/cafè/雪/*", "/cafè/雪/")
	set("/cafè/雪/exact", 2)

	_, err := db.ApplyRecoveryForFixture(ctx, "")
	must(err)
	match("/cafè/雪/exact", "/cafè/雪/exact", "/cafè/雪/exact")
	operation := ids.New()
	_, err = db.ApplyRecoveryForFixture(ctx, operation)
	must(err)
	match("/cafè/雪/exact", "", "")
	snapshot, err := db.InspectExposure(ctx, ref, "route", "/cafè/雪/exact", owner)
	must(err)
	if snapshot.Route == nil || snapshot.RoutePublished || snapshot.Version != 3 {
		t.Fatalf("restore did not preserve inactive mapping and CAS: %+v", snapshot)
	}
	set("/cafè/雪/exact", 3)
	_, err = db.ApplyRecoveryForFixture(ctx, "")
	must(err)
	match("/cafè/雪/exact", "/cafè/雪/exact", "/cafè/雪/exact")
}

func TestManagedRouteReservedPatterns(t *testing.T) {
	for _, root := range []string{"api", "auth", "web", "_orva", "fn", "mcp", "webhook", "metrics", "register", "oauth", ".well-known"} {
		for _, suffix := range []string{"", "/", "/*", "/child"} {
			if validManagedRoutePattern("/" + root + suffix) {
				t.Errorf("allowed reserved route %q", "/"+root+suffix)
			}
		}
	}
	for _, raw := range []string{"/", "//api", "/a/../api", "/a/./child", "/%61pi", "/a\\child", "/a*/*", "/a?b"} {
		if validManagedRoutePattern(raw) {
			t.Errorf("allowed invalid route %q", raw)
		}
	}
}
