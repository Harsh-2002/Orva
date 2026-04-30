package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Harsh-2002/Orva/internal/builder"
	"github.com/Harsh-2002/Orva/internal/config"
	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/server"
	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the Orva server",
	Long:  "Start the Orva API server and begin accepting requests.",
	Run:   runServe,
}

func init() {
	serveCmd.Flags().Int("port", 0, "listen port (overrides ORVA_PORT)")
	rootCmd.AddCommand(serveCmd)
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

	if len(cfg.ActiveEnvVars) > 0 {
		slog.Info("config", "active_env_vars", len(cfg.ActiveEnvVars),
			"supported", len(config.SupportedEnvVars),
			"vars", cfg.ActiveEnvVars)
	} else {
		slog.Info("config", "active_env_vars", 0,
			"supported", len(config.SupportedEnvVars),
			"note", "all defaults")
	}

	slog.Info("starting orva", "version", Version)

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

	// Round-G: fold any pre-existing flat code/ dirs into the new
	// versions/<hash>/ layout. Idempotent — no-op on subsequent boots.
	builder.MigrateLegacyCodeDirs(cfg.Data.Dir, db)

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
