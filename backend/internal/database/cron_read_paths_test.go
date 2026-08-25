package database

import (
	"testing"
	"time"
)

// Every list path shares scanCronRows, so its column list and each caller's
// SELECT have to move together. Adding a column and missing one SELECT still
// compiles, then fails at runtime with a Scan arity error on whichever path
// was missed — and one of those callers is DueCronSchedules, where the symptom
// is the scheduler quietly firing nothing at all, logged once at Warn.
//
// There were no tests over these read paths at all when `name` was added. This
// exercises all five against a real row. Keep it exhaustive: an uncovered path
// is one whose arity error nobody sees until production.
func TestEveryCronReadPathScansARow(t *testing.T) {
	db := newTestDB(t)
	fn := insertTestFunction(t, db, "cron-reader")

	// Due in the past, so DueCronSchedules must return it.
	past := time.Now().UTC().Add(-time.Hour)
	sched := &CronSchedule{
		ID: "cron_1", FunctionID: fn.ID, Name: "nightly-sweep",
		CronExpr: "0 3 * * *", Timezone: "UTC", Enabled: true,
		Payload: `{"source":"sdk"}`, NextRunAt: &past,
	}
	if err := db.InsertCronSchedule(sched); err != nil {
		t.Fatal(err)
	}

	t.Run("GetCronSchedule", func(t *testing.T) {
		got, err := db.GetCronSchedule("cron_1")
		if err != nil {
			t.Fatal(err)
		}
		if got.Name != "nightly-sweep" {
			t.Errorf("name = %q, want nightly-sweep", got.Name)
		}
	})

	t.Run("ListCronSchedulesForFunction", func(t *testing.T) {
		rows, err := db.ListCronSchedulesForFunction(fn.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].Name != "nightly-sweep" {
			t.Fatalf("rows=%d name=%q", len(rows), namesOf(rows))
		}
	})

	t.Run("ListAllCronSchedulesWithFunction", func(t *testing.T) {
		rows, err := db.ListAllCronSchedulesWithFunction()
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 {
			t.Fatalf("rows = %d, want 1", len(rows))
		}
		// This is the query the dashboard renders from, and the name is what
		// tells an operator a schedule came from a function rather than from
		// them. It did not select the column at all.
		if rows[0].Name != "nightly-sweep" {
			t.Errorf("name = %q: an SDK-created schedule is indistinguishable from one the operator made", rows[0].Name)
		}
	})

	t.Run("ListAllCronSchedules", func(t *testing.T) {
		// recomputeNextRunOnBoot reads through this one, and swallows its
		// error with a single slog.Warn -- so a mismatch here means every
		// schedule silently loses its next-run time across a restart.
		rows, err := db.ListAllCronSchedules()
		if err != nil {
			t.Fatalf("the boot recompute query failed: %v", err)
		}
		if len(rows) != 1 || rows[0].Name != "nightly-sweep" {
			t.Fatalf("rows=%d names=%v", len(rows), namesOf(rows))
		}
	})

	t.Run("DueCronSchedules", func(t *testing.T) {
		rows, err := db.DueCronSchedules(time.Now().UTC(), 10)
		if err != nil {
			t.Fatalf("the scheduler's own query failed, which means no cron in the instance fires: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("due rows = %d, want 1", len(rows))
		}
		if rows[0].Name != "nightly-sweep" {
			t.Errorf("name = %q, want nightly-sweep", rows[0].Name)
		}
	})
}

// An operator-created schedule carries no name, which is what makes
// `name <> ”` mean "created by the SDK" — the partial index
// idx_cron_function_name depends on the same invariant. If the dashboard ever
// grows a name field, that meaning is gone and this fails.
func TestOperatorCreatedSchedulesCarryNoName(t *testing.T) {
	db := newTestDB(t)
	fn := insertTestFunction(t, db, "cron-operator")

	if err := db.InsertCronSchedule(&CronSchedule{
		ID: "cron_op", FunctionID: fn.ID,
		CronExpr: "0 4 * * *", Timezone: "UTC", Enabled: true, Payload: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetCronSchedule("cron_op")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "" {
		t.Errorf("name = %q, want empty: an unnamed row is how the schema records 'the operator made this'", got.Name)
	}
}

func namesOf(rows []*CronSchedule) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Name)
	}
	return out
}
