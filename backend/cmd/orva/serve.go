package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	_ "net/http/pprof" // registers /debug/pprof/* on http.DefaultServeMux (not Orva's mux)
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Harsh-2002/Orva/backend/internal/builder"
	"github.com/Harsh-2002/Orva/backend/internal/config"
	"github.com/Harsh-2002/Orva/backend/internal/database"
	"github.com/Harsh-2002/Orva/backend/internal/sandbox"
	"github.com/Harsh-2002/Orva/backend/internal/server"
	"github.com/Harsh-2002/Orva/backend/internal/version"
	"github.com/spf13/cobra"
)

// maybeStartPprof starts a debug pprof/expvar listener when ORVA_PPROF_ADDR is
// set (e.g. "127.0.0.1:6060"). It serves http.DefaultServeMux, which the
// net/http/pprof import populates; Orva's real API runs on its own mux, so
// pprof is NEVER exposed on the main port. Bind it to loopback only — the
// endpoints leak goroutine stacks, heap profiles, and env-dependent internals.
func maybeStartPprof() {
	addr := os.Getenv("ORVA_PPROF_ADDR")
	if addr == "" {
		return
	}
	slog.Warn("pprof debug listener enabled — bind to loopback only, never expose publicly", "addr", addr)
	go func() {
		srv := &http.Server{Addr: addr, ReadHeaderTimeout: 5 * time.Second}
		if err := srv.ListenAndServe(); err != nil {
			slog.Warn("pprof listener stopped", "error", err)
		}
	}()
}

// newServeCmd constructs the `orva serve` subcommand. Server binary only.
func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Orva server",
		Long:  "Start the Orva API server and begin accepting requests.",
		Run:   runServe,
	}
	cmd.Flags().Int("port", 0, "listen port (overrides ORVA_PORT)")
	return cmd
}

func runServe(cmd *cobra.Command, args []string) {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	if port, _ := cmd.Flags().GetInt("port"); port != 0 {
		cfg.Server.Port = port
	}

	setupLogger(cfg.Logging)

	maybeStartPprof()

	if len(cfg.ActiveEnvVars) > 0 {
		slog.Info("config", "active_env_vars", len(cfg.ActiveEnvVars),
			"supported", len(config.SupportedEnvVars),
			"vars", cfg.ActiveEnvVars)
	} else {
		slog.Info("config", "active_env_vars", 0,
			"supported", len(config.SupportedEnvVars),
			"note", "all defaults")
	}

	slog.Info("starting orva",
		"version", version.Version,
		"commit", version.Commit,
		"build_time", version.BuildTime)

	db, err := database.New(cfg.Database.Path)
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Migrate(); err != nil {
		slog.Error("failed to run migrations", "error", err)
		os.Exit(1)
	}

	// The UUIDv7 migration renames function ids, and each function's code
	// lives at <dataDir>/functions/<id>/. Complete (or resume) the matching
	// directory rename before anything reads or reclaims that tree. This is
	// fatal on failure by design: booting with ids that do not match the
	// names on disk means every function fails to spawn and the GC then
	// deletes the operator's only copy of their source as orphans.
	if err := db.ReconcileFunctionDirs(cfg.Data.Dir); err != nil {
		slog.Error("failed to reconcile function directories after id migration",
			"error", err,
			"hint", "function code is intact on disk; fix the cause and restart")
		os.Exit(1)
	}

	// Round-G: fold any pre-existing flat code/ dirs into the new
	// versions/<hash>/ layout. Idempotent — no-op on subsequent boots.
	builder.MigrateLegacyCodeDirs(cfg.Data.Dir, db)

	// Reclaim build scratch dirs left behind by a crash. RunBuild defers its
	// own cleanup, which only covers a live process — a kill -9 mid-install
	// leaks the whole working set under build-tmp/ permanently.
	builder.SweepBuildScratch(cfg.Data.Dir)

	// Fail deployments abandoned mid-build. Only the finish paths set a
	// terminal status and both run in-process, so a restart left the row at
	// 'queued'/'building' forever and the dashboard span a spinner that
	// never resolved. The tarball they would have built from is in the
	// scratch dir the sweep above just reclaimed.
	if n, err := db.RequeueStuckDeployments(); err != nil {
		slog.Warn("could not reconcile stuck deployments", "error", err)
	} else if n > 0 {
		slog.Info("failed deployments abandoned by a previous run", "count", n)
	}

	// Reclaim NSJAIL.* cgroup directories left behind by a previous process.
	// Workers are SIGKILLed, so nsjail never runs its own cleanup, and every
	// spawn scans this directory to find its own cgroup -- an accumulation
	// slows cold starts and eventually breaks memory sampling outright,
	// which leaves the autoscaler permanently over-reserving.
	sandbox.SweepOrphanCgroups()

	// Trim execution history so the database does not grow without bound.
	// Runs once now and daily thereafter; the window is the system_config key
	// database.RetentionSettingKey (0 disables it).
	db.StartRetention(context.Background())

	srv := server.New(cfg, db)

	// Load the active function set into the registry cache, then kick off
	// background prewarm of the warm worker pool so the first invoke is
	// fast. Both run after the HTTP server is listening so health checks
	// succeed immediately.
	if err := srv.Registry.LoadAll(); err != nil {
		slog.Warn("registry load failed", "error", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		addr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)
		slog.Info("listening", "addr", addr)
		if err := srv.Start(addr); err != nil {
			slog.Error("server error", "error", err)
			stop()
		}
	}()

	go srv.Prewarm(ctx)

	// Start the scheduler after the HTTP listener so health probes don't
	// block on it. The scheduler runs cron triggers, KV TTL sweep,
	// queued jobs, and (v0.3) webhook delivery.
	if srv.Scheduler != nil {
		srv.Scheduler.Start(ctx)
	}
	// Webhook fanout listener subscribes to the Hub and queues
	// webhook_deliveries for any matching subscription. Cheap goroutine.
	if srv.WebhookFanout != nil {
		srv.WebhookFanout.Start(ctx)
	}

	<-ctx.Done()
	slog.Info("shutting down")
	srv.Shutdown(context.Background())
	slog.Info("shutdown complete")
}

func setupLogger(cfg config.LoggingConfig) {
	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: parseLevel(cfg.Level)}

	if cfg.Format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(handler))
}

func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
