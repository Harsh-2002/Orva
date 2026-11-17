package scheduler

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/pool"
	"github.com/Harsh-2002/Orva/backend/internal/registry"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/workloadasync"
	"github.com/Harsh-2002/Orva/backend/internal/workloadiam"
)

func cronSchedulerFixture(t *testing.T) (*Scheduler, *database.AsyncCron, *workloadasync.Service) {
	t.Helper()
	db, err := database.New(filepath.Join(t.TempDir(), "cron.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := db.InitCronSchemaForFixture(t.Context()); err != nil {
		t.Fatal(err)
	}
	const id = "019df200-7b00-7e00-9c00-aab1cd2e3f50"
	if err := db.InsertFunction(&database.Function{ID: id, Name: "cron", Status: "active", Runtime: "node", Entrypoint: "handler.js"}); err != nil {
		t.Fatal(err)
	}
	name := "daily"
	e, err := workloadiam.Compile(workloadiam.Metadata{OwnerID: id, Revision: 1, ApprovedArtifact: "artifact"}, workloadiam.Policy{SchemaVersion: 1,
		Allow: []workloadiam.Statement{{Context: workloadiam.Execution, Actions: []workloadiam.Action{workloadiam.CronsUpsert},
			Resource: workloadiam.Resource{OwnCron: &workloadiam.CronResource{Name: &name, TargetID: id}}}},
		Inbound: []workloadiam.Acceptance{{CallerID: id, Triggers: []workloadiam.Trigger{workloadiam.Cron}}}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := workloadiam.NewAuthority(4, []*workloadiam.Evaluator{e})
	if err != nil {
		t.Fatal(err)
	}
	service := &workloadasync.Service{DB: db, Authority: a, CurrentArtifact: func(string) string { return "artifact" }}
	due := time.Now().UTC().Add(-time.Second)
	c := &database.AsyncCron{CronSchedule: database.CronSchedule{FunctionID: id, Name: name, CronExpr: "@every 1s", Timezone: "UTC", Enabled: true, Payload: "{}", NextRunAt: &due},
		AsyncProvenance: database.AsyncProvenance{OriginKind: "function", CreatorID: id}}
	if err := db.SaveAsyncCron(t.Context(), c, nil, workloadiam.AsyncRevisions{Creator: 1, Target: 1}, "upsert"); err != nil {
		t.Fatal(err)
	}
	return New(db, nil, t.TempDir(), nil, WithCronIAM(service)), c, service
}
func TestCronStoragePressureDoesNotAdmitAndReleasesReservation(t *testing.T) {
	s, c, service := cronSchedulerFixture(t)
	leases := make([]*database.ExecutionLease, 0, s.db.WriterStats().CriticalCap)
	for range cap(leases) {
		lease, err := s.db.ReserveExecution(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, lease)
	}
	defer func() {
		for _, l := range leases {
			l.Cancel()
		}
	}()
	call, err := service.BeginCron(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer call.Finish()
	if err := service.ClaimCron(t.Context(), c, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(call.Context(), 20*time.Millisecond)
	defer cancel()
	s.fireCronManaged(ctx, &c.CronSchedule, c, call) // No pool: reaching Acquire would panic.
	got, err := s.db.GetAsyncCron(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastRunAt != nil || got.LastStatus != "" || got.AuthorityState != "active" {
		t.Fatal("storage pressure admitted or wrote a user-code failure")
	}
	for _, l := range leases {
		l.Cancel()
	}
	ctx, cancel = context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	lease, err := s.db.ReserveExecution(ctx)
	if err != nil {
		t.Fatal("reservation leaked", err)
	}
	lease.Cancel()
}
func TestCronAcquireFailureReleasesCriticalSlot(t *testing.T) {
	s, c, service := cronSchedulerFixture(t)
	pm := pool.NewManager(pool.ManagerConfig{ManagedAuthority: service.Authority},
		pool.SandboxTemplate{NsjailBin: "/absent", RootfsDir: "/absent", DataDir: t.TempDir()}, s.db, registry.New(s.db), sandbox.NewLimiter(1))
	defer pm.Shutdown(t.Context())
	s.pool = pm
	call, err := service.BeginCron(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer call.Finish()
	if err := service.ClaimCron(t.Context(), c, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	s.fireCronManaged(call.Context(), &c.CronSchedule, c, call)
	// Reserve every completion slot, not only one, to detect a single leaked slot.
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	leases := []*database.ExecutionLease{}
	defer func() {
		for _, l := range leases {
			l.Cancel()
		}
	}()
	for range s.db.WriterStats().CriticalCap {
		l, err := s.db.ReserveExecution(ctx)
		if err != nil {
			t.Fatal("completion slot leaked on acquire failure", err)
		}
		leases = append(leases, l)
	}
	got, err := s.db.GetAsyncCron(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastRunAt != nil || got.LastStatus != "" {
		t.Fatal("acquire failure recorded an execution")
	}
}
func TestCronOptOutAndExplicitNilDoNotReadStagedSchema(t *testing.T) {
	s, c, _ := cronSchedulerFixture(t)
	before := c.NextRunAt
	jobs := New(s.db, nil, t.TempDir(), nil, WithAsyncIAM(nil))
	jobs.cronTick(t.Context())
	nilCron := New(s.db, nil, t.TempDir(), nil, WithCronIAM(nil))
	nilCron.cronTick(t.Context())
	got, err := s.db.GetAsyncCron(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.NextRunAt.Equal(*before) || got.LastRunAt != nil {
		t.Fatal("disabled fixture advanced schedule")
	}
	// Legacy error persistence remains active with the option omitted.
	legacy := New(s.db, nil, t.TempDir(), nil)
	invalid := c.CronSchedule
	invalid.CronExpr = "invalid"
	legacy.fireCron(t.Context(), &invalid)
	got, err = s.db.GetAsyncCron(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastStatus != "failed" || got.LastRunAt == nil {
		t.Fatal("legacy result path changed")
	}
}
