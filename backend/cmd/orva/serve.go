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
	serveCmd.Flags().String("host", "", "bind host (overrides config)")
	serveCmd.Flags().Int("port", 0, "bind port (overrides config)")
	serveCmd.Flags().String("config", "", "path to config file")
	serveCmd.Flags().String("db-path", "", "path to database file (overrides config)")
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) {
	// Load config from file or defaults.
	cfgPath, _ := cmd.Flags().GetString("config")
	if cfgPath != "" {
		os.Setenv("ORVA_CONFIG", cfgPath)
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Apply flag overrides.
	if host, _ := cmd.Flags().GetString("host"); host != "" {
		cfg.Server.Host = host
	}
	if port, _ := cmd.Flags().GetInt("port"); port != 0 {
		cfg.Server.Port = port
	}
	if dbPath, _ := cmd.Flags().GetString("db-path"); dbPath != "" {
		cfg.Database.Path = dbPath
	}

	setupLogger(cfg.Logging)

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
