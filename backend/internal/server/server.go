package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Harsh-2002/Orva/internal/builder"
	"github.com/Harsh-2002/Orva/internal/config"
	"github.com/Harsh-2002/Orva/internal/database"
	"github.com/Harsh-2002/Orva/internal/firewall"
	"github.com/Harsh-2002/Orva/internal/metrics"
	"github.com/Harsh-2002/Orva/internal/pool"
	"github.com/Harsh-2002/Orva/internal/proxy"
	"github.com/Harsh-2002/Orva/internal/registry"
	"github.com/Harsh-2002/Orva/internal/sandbox"
	"github.com/Harsh-2002/Orva/internal/scheduler"
	"github.com/Harsh-2002/Orva/internal/secrets"
	"github.com/Harsh-2002/Orva/internal/server/events"
	"github.com/Harsh-2002/Orva/internal/server/handlers"
)

type Server struct {
	httpServer *http.Server
	router     *Router
	cfg        *config.Config
	db         *database.Database
	Pool       *sandbox.Limiter
	PoolMgr    *pool.Manager
	Registry   *registry.Registry
	Metrics    *metrics.Metrics
	BuildQueue *builder.Queue
	EventHub   *events.Hub
	Firewall   *firewall.Manager
	Scheduler  *scheduler.Scheduler
	WebhookFanout *events.WebhookFanout
}

func New(cfg *config.Config, db *database.Database) *Server {
	reg := registry.New(db)
	bld := builder.New()
	bld.DataDir = cfg.Data.Dir
	bld.DB = db
	met := metrics.New()

	// Initialize sandbox concurrency limiter.
	limiter := sandbox.NewLimiter(cfg.Sandbox.MaxConcurrent)
	slog.Info("sandbox ready",
		"nsjail", cfg.Sandbox.NsjailBin,
		"rootfs", cfg.Sandbox.RootfsDir,
		"max_concurrent", cfg.Sandbox.MaxConcurrent)

	// Builder: extract + validate + hash code. No Docker build.
	bld.BuildFunc = func(ctx context.Context, dockerfilePath, contextDir, imageTag string) (int64, error) {
		// nsjail doesn't need image builds. The code directory IS the
		// deployable artifact. Just return success.
		return 0, nil
	}

	// Surface persistence state at startup. If the operator is using
	// docker compose down -v (or a non-persistent mount) they'll see
	// users=0 every boot — which makes the "I have to onboard every
	// time" symptom obvious in the logs instead of mysteriously firing.
	if uc, err := db.CountUsers(); err == nil {
		ak, _ := db.CountAPIKeys()
		slog.Info("data dir state",
			"path", cfg.Data.Dir,
			"users", uc,
			"api_keys", ak,
			"hint", "if users=0 after rebuild, your volume isn't persisting",
		)
	}

	// Bootstrap admin key on first run only (when the DB has no keys at all).
	// After first run the UI authenticates via session cookies from /auth/login.
	bootstrapAdminKey(db, cfg.Data.Dir)

	// Internal token — process-lifetime random secret injected into every
	// sandboxed worker as ORVA_INTERNAL_TOKEN. The adapter uses it to
	// authenticate against /api/v1/_kv/, /api/v1/_internal/invoke/, and
	// /api/v1/_jobs/. Regenerated on each restart so a leaked token from
	// stderr can't be replayed past the next deploy.
	internalToken := generateInternalToken()
	// API base for the worker SDK. From inside a sandbox with
	// network_mode=egress, 127.0.0.1 is the sandbox's own loopback —
	// useless for reaching orvad. The right destination is whatever IP
	// nsjail's nstun NAT routes externally; in Docker that's the
	// container's default-gateway IP on the bridge network.
	// detectHostIP reads /proc/net/route to find that gateway.
	// Functions with network_mode=none have no network stack at all;
	// the SDK surfaces OrvaUnavailableError for those.
	apiBase := fmt.Sprintf("http://%s:%d", detectHostIP(), cfg.Server.Port)
	slog.Info("internal SDK base configured", "api_base", apiBase)

	// Wire the warm pool manager. It owns the sandbox.Limiter as a
	// host-wide ceiling and spawns per-function worker pools lazily.
	poolMgr := pool.NewManager(
		pool.ManagerConfig{
			DefaultMin:     1,
			// Operator soft cap; autoscaler still clamps further by memory
			// + CPU headroom at runtime. Was 5 (static dumb default); the
			// autoscaler now reads load signals so this only matters as a
			// defensive ceiling when operators have set no pool_config row.
			DefaultMax:     50,
			DefaultIdleTTL: 10 * time.Minute,
			DefaultMaxUses: 1000,
			ReapInterval:   30 * time.Second,
			EagerWarmup:    true,
		},
		pool.SandboxTemplate{
			NsjailBin:      cfg.Sandbox.NsjailBin,
			RootfsDir:      cfg.Sandbox.RootfsDir,
			DataDir:        cfg.Data.Dir,
			DefaultSeccomp: cfg.Sandbox.SeccompPolicy,
			InternalToken:  internalToken,
			APIBaseURL:     apiBase,
			Metrics:        met,
		},
		db, reg, limiter,
	)

	// Create proxy with sandbox config.
	px := &proxy.Proxy{
		Sandbox: limiter,
		Pool:    poolMgr,
		DB:      db,
		Config: proxy.ProxyConfig{
			NsjailBin: cfg.Sandbox.NsjailBin,
			RootfsDir: cfg.Sandbox.RootfsDir,
		},
	}

	// Seed the cached replay-capture toggle + max-bytes (v0.4 A3). The
	// hot path reads these via atomic loads; this loader pre-fills them
	// and starts a background refresher so operators can flip the toggle
	// from the Settings page without restarting orvad.
	proxy.LoadCaptureConfig(db)

	sm := secrets.New(db, cfg.Data.Dir)

	// Plumb secrets into the pool's spawn template so warm workers get the
	// decrypted env on creation. Without this, secrets only reach functions
	// via the proxy's per-request env build — which we plumb to spawn time
	// via this lookup. Secret upsert/delete triggers RefreshForDeploy so
	// the next spawn picks up the changes.
	poolMgr.SetSecretsLookup(func(fnID string) map[string]string {
		if sm == nil {
			return nil
		}
		s, _ := sm.GetForFunction(fnID)
		return s
	})

	// SSE hub: in-process pub/sub broker for live UI streams. Created
	// before the build queue starts so the queue's first job can publish
	// events. Subscribers (the HTTP /api/v1/events handler) drop events
	// rather than block producers.
	hub := events.NewHub()

	// Wire the registry to the SSE hub so any Set/Delete (function created,
	// updated, deleted, or status flipped after a build) fans out to all
	// connected clients — the FunctionsList page subscribes and re-renders
	// without polling.
	reg.PublishEvent = hub.Publish

	// Async build queue — drains npm/pip install off the deploy HTTP path.
	// runtime.NumCPU() workers is enough for the single-box target.
	buildQueue := builder.NewQueue(bld, db, reg)
	buildQueue.FnLock = poolMgr.FunctionLock
	buildQueue.PublishEvent = hub.Publish
	buildQueue.Start()

	// Round-G: prune archived version dirs in the background. Always
	// preserves the actively-served version; tunable via
	// system_config.versions_to_keep + gc_interval_seconds.
	go builder.NewGC(cfg.Data.Dir, db).Run(context.Background())

	// Background ticker: snapshot system metrics every 5 s and publish
	// to the hub. The HTTP handler GET /api/v1/system/metrics.json still
	// returns a fresh snapshot for curl clients — this just adds a push
	// path for the dashboard.
	go runMetricsPublisher(context.Background(), hub, db, met, limiter, poolMgr, buildQueue, reg)

	// Egress firewall manager. Started immediately so the initial nft
	// rules are in place before any function can run. If nft isn't
	// available (no NET_ADMIN, BSD host, etc.) the manager logs a
	// warning and the API surfaces it via /firewall/rules → status.
	fw := firewall.NewManager(db, cfg.Data.Dir)
	fw.Start(context.Background())

	deps := RouterDeps{
		Registry:      reg,
		Proxy:         px,
		Builder:       bld,
		Metrics:       met,
		Secrets:       sm,
		BuildQueue:    buildQueue,
		PoolMgr:       poolMgr,
		EventHub:      hub,
		Firewall:      fw,
		InternalToken: internalToken,
	}

	router := NewRouter(cfg, db, deps)

	// Scheduler runs cron triggers (P1) + future TTL/queue ticks. Started
	// later in serve.go after the HTTP listener is up so health probes
	// pass first.
	sched := scheduler.New(db, poolMgr, cfg.Data.Dir, hub)

	// WebhookFanout subscribes to the Hub and writes webhook_deliveries
	// rows for every event that any operator-configured subscription
	// matches. Started later in serve.go alongside the scheduler.
	fanout := events.NewWebhookFanout(db, hub)

	return &Server{
		httpServer: &http.Server{
			Handler:      router,
			ReadTimeout:  time.Duration(cfg.Server.ReadTimeoutSec) * time.Second,
			WriteTimeout: time.Duration(cfg.Server.WriteTimeoutSec) * time.Second,
		},
		router:        router,
		cfg:           cfg,
		db:            db,
		Pool:          limiter,
		PoolMgr:       poolMgr,
		Registry:      reg,
		Metrics:       met,
		BuildQueue:    buildQueue,
		EventHub:      hub,
		Firewall:      fw,
		Scheduler:     sched,
		WebhookFanout: fanout,
	}
}

// bootstrapAdminKey ensures a "bootstrap-admin" API key exists. It persists
// the plaintext key to ${dataDir}/.admin-key (mode 0600) so operators can
// recover it after restarts without re-onboarding — the same trust boundary
// as the SQLite file itself. Three paths:
//
//   - keyfile + matching DB row → reprint, no-op DB-side
//   - keyfile only (DB row missing — e.g. fresh DB but persisted volume) →
//     re-insert the row from the keyfile so the key keeps working
//   - neither → generate fresh, insert, write keyfile, print
//
// The DB still only stores the SHA-256 hash; the keyfile is the *only*
// persisted plaintext copy.
func bootstrapAdminKey(db *database.Database, dataDir string) {
	keyPath := filepath.Join(dataDir, ".admin-key")

	// Path 1 / 2: a keyfile exists. Hash it and decide based on DB state.
	if existing, err := os.ReadFile(keyPath); err == nil {
		plaintext := strings.TrimSpace(string(existing))
		if !strings.HasPrefix(plaintext, "orva_") {
			slog.Warn("admin key file present but malformed; regenerating", "path", keyPath)
		} else {
			hash := sha256.Sum256([]byte(plaintext))
			keyHash := hex.EncodeToString(hash[:])

			if _, err := db.GetAPIKeyByHash(keyHash); err == nil {
				printBootstrapKey(plaintext, "(loaded from " + keyPath + ")")
				return
			}
			// Keyfile present but DB row missing — re-insert.
			idBytes := make([]byte, 8)
			rand.Read(idBytes)
			permsJSON, _ := json.Marshal([]string{"invoke", "read", "write", "admin"})
			if err := db.InsertAPIKey(&database.APIKey{
				ID:          "key_" + hex.EncodeToString(idBytes),
				KeyHash:     keyHash,
				Prefix:      plaintext[:min(12, len(plaintext))],
				Name:        "bootstrap-admin",
				Permissions: string(permsJSON),
			}); err != nil {
				slog.Error("failed to restore bootstrap admin key from keyfile", "error", err)
				return
			}
			slog.Info("restored bootstrap admin key from keyfile", "path", keyPath)
			printBootstrapKey(plaintext, "(restored from " + keyPath + ")")
			return
		}
	}

	// Path 3: no keyfile. If the DB has any key at all (e.g. operator deleted
	// the file but kept the volume), don't print anything noisy — we can't
	// recover the plaintext from a hash. Operators can issue a fresh key via
	// /api/v1/keys.
	count, err := db.CountAPIKeys()
	if err != nil {
		slog.Warn("failed to check API keys count", "error", err)
		return
	}
	if count > 0 {
		slog.Info("api keys present in DB but no keyfile — keeping existing keys",
			"hint", "issue a fresh admin key via POST /api/v1/keys if needed")
		return
	}

	rawKey := make([]byte, 32)
	if _, err := rand.Read(rawKey); err != nil {
		slog.Error("failed to generate admin key", "error", err)
		return
	}
	plaintextKey := "orva_" + hex.EncodeToString(rawKey)

	hash := sha256.Sum256([]byte(plaintextKey))
	keyHash := hex.EncodeToString(hash[:])
	idBytes := make([]byte, 8)
	rand.Read(idBytes)
	keyID := "key_" + hex.EncodeToString(idBytes)
	permsJSON, _ := json.Marshal([]string{"invoke", "read", "write", "admin"})

	if err := db.InsertAPIKey(&database.APIKey{
		ID:          keyID,
		KeyHash:     keyHash,
		Prefix:      plaintextKey[:min(12, len(plaintextKey))],
		Name:        "bootstrap-admin",
		Permissions: string(permsJSON),
	}); err != nil {
		slog.Error("failed to insert bootstrap admin key", "error", err)
		return
	}

	// Persist the plaintext alongside SQLite. Same trust boundary; eliminates
	// the "I lost the key on first run" frustration on Docker recreates.
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		slog.Warn("failed to create data dir for keyfile", "error", err, "path", dataDir)
	}
	if err := os.WriteFile(keyPath, []byte(plaintextKey+"\n"), 0o600); err != nil {
		slog.Warn("failed to persist admin key file (key still in DB; copy now)", "error", err)
	}

	printBootstrapKey(plaintextKey, "(saved at " + keyPath + ")")
}

func printBootstrapKey(key, note string) {
	fmt.Println("========================================")
	fmt.Println("  BOOTSTRAP ADMIN API KEY")
	fmt.Printf("  %s\n", key)
	if note != "" {
		fmt.Printf("  %s\n", note)
	}
	fmt.Println("========================================")
}

// detectHostIP returns the IP a sandboxed worker should use to reach the
// orva server. Reads /proc/net/route for the default-gateway entry; that
// IP is what nsjail's nstun NAT exposes to sandboxes as the host. Falls
// back to "127.0.0.1" if /proc isn't readable (rare; test environments).
func detectHostIP() string {
	const fallback = "127.0.0.1"
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return fallback
	}
	for _, line := range strings.Split(string(data), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		// Destination 00000000 = default route. Gateway is little-endian hex.
		if fields[1] == "00000000" {
			gwHex := fields[2]
			if len(gwHex) != 8 {
				return fallback
			}
			// Decode 4 bytes from little-endian hex.
			b := make([]byte, 4)
			for i := 0; i < 4; i++ {
				v, err := strconv.ParseUint(gwHex[i*2:i*2+2], 16, 8)
				if err != nil {
					return fallback
				}
				b[3-i] = byte(v)
			}
			return fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], b[3])
		}
	}
	return fallback
}

// generateInternalToken returns a fresh 32-byte random hex string used as
// the per-process token injected into every worker as ORVA_INTERNAL_TOKEN.
// Regenerated on each server start so a leaked token from a single boot
// can't be replayed forever.
func generateInternalToken() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fall back to a time-based string. Should never happen on Linux.
		return fmt.Sprintf("orva-internal-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func (s *Server) Start(addr string) error {
	s.httpServer.Addr = addr
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	// Drain HTTP first so no new requests hit the pool while we quit workers.
	if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
		slog.Warn("http shutdown error", "err", err)
	}
	if s.BuildQueue != nil {
		s.BuildQueue.Shutdown(shutdownCtx)
	}
	if s.PoolMgr != nil {
		_ = s.PoolMgr.Shutdown(shutdownCtx)
	}
	if s.Firewall != nil {
		_ = s.Firewall.Stop(shutdownCtx)
	}
	if s.Scheduler != nil {
		s.Scheduler.Stop(5 * time.Second)
	}
	return nil
}

// runMetricsPublisher snapshots the same JSON the GET /api/v1/system/metrics.json
// handler returns and pushes it to every SSE subscriber every 5 s. Stopping
// the goroutine is a no-op at shutdown — the hub closes its subscribers and
// the next tick is a wasted snapshot.
func runMetricsPublisher(
	ctx context.Context,
	hub *events.Hub,
	db *database.Database,
	met *metrics.Metrics,
	limiter *sandbox.Limiter,
	poolMgr *pool.Manager,
	buildQueue *builder.Queue,
	reg *registry.Registry,
) {
	// Reuse the existing handler's snapshot builder so the SSE payload is
	// always identical to the GET endpoint — no drift between push and pull.
	h := &handlers.SystemHandler{
		Metrics:    met,
		DB:         db,
		Sandbox:    limiter,
		PoolMgr:    poolMgr,
		BuildQueue: buildQueue,
		Registry:   reg,
		StartTime:  time.Now(),
	}
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			// Cheap when no subscribers — Publish iterates an empty map.
			if hub.SubscriberCount() == 0 {
				continue
			}
			hub.Publish(events.TypeMetrics, h.BuildMetricsSnapshot())
		}
	}
}

// Prewarm spawns min_warm workers for each active function. Safe to call
// after the server is up — it uses the configured EagerWarmup toggle, so a
// no-op if disabled.
func (s *Server) Prewarm(ctx context.Context) {
	if s.PoolMgr != nil {
		s.PoolMgr.PrewarmAll(ctx)
	}
}

func (s *Server) Router() *Router {
	return s.router
}
