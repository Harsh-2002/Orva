# Production deployment notes

> Orva is in active development and the maintainers don't recommend it
> for production-critical workloads yet. The notes below are for
> operators running it in homelabs, side projects, internal tools,
> staging environments — places where a few hours of downtime is
> acceptable while bugs get sorted out.

## Sizing

Measured on a 2-CPU / 12 GB host (see [CAPACITY.md](CAPACITY.md)):

- **Idle**: ~50 MB RSS for the orvad process. Each warm worker takes
  ~18 MB when idle. 20 deployed-but-unused functions cost ~50 MB total.
- **Under sustained c=500**: ~880 req/s aggregate, 35% returning fast
  `429 TOO_MANY_REQUESTS` once `sandbox.max_concurrent` is hit. p50 of
  successful invocations: ~500 ms.

For a single-host deploy, **2 CPU + 4 GB RAM** is plenty for ~50
functions of mixed traffic. Bigger if your functions hold significant
memory each.

## TLS

orvad **does not terminate TLS itself.** Run a reverse proxy in front:
caddy, nginx, traefik, cloudflared. The browser's clipboard API
silently fails on plain HTTP from non-localhost — the dashboard
becomes partially broken.

Caddy example:

```caddy
orva.example.com {
  reverse_proxy localhost:8443 {
    flush_interval -1   # required so SSE streams aren't buffered
  }
}
```

nginx example:

```nginx
server {
  listen 443 ssl http2;
  server_name orva.example.com;
  ssl_certificate     /etc/letsencrypt/live/orva.example.com/fullchain.pem;
  ssl_certificate_key /etc/letsencrypt/live/orva.example.com/privkey.pem;

  location / {
    proxy_pass http://127.0.0.1:8443;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;
    proxy_read_timeout 600s;
    chunked_transfer_encoding off;
  }
}
```

Critical bits: **buffering off** for SSE streams (`/api/v1/events` and
`/api/v1/deployments/*/stream`).

## Backups

Everything Orva persists lives under `/var/lib/orva/`. Two strategies:

### Volume-level (simplest)

Stop orvad → snapshot/copy the data dir → restart.

```bash
systemctl stop orva
tar -czf orva-backup-$(date +%F).tar.gz /var/lib/orva
systemctl start orva
```

A few seconds of downtime per backup. Restore by extracting back into
`/var/lib/orva/` before `systemctl start`.

### SQLite hot backup

The DB is in WAL mode — safe to back up while orvad is running, as
long as you use SQLite's online backup API (not a raw file copy).

```bash
sqlite3 /var/lib/orva/orva.db ".backup /backup/orva-$(date +%F).db"
```

This grabs a consistent snapshot of just the database. The version
archive at `/var/lib/orva/functions/<id>/versions/` and the rootfs
trees at `/var/lib/orva/rootfs/` need a separate `tar` while orvad is
either stopped or guaranteed not to be deploying / GC'ing.

### What you actually need

| path | importance | grows |
|---|---|---|
| `/var/lib/orva/orva.db` (+ `-wal`, `-shm`) | **critical** — users, functions, secrets, executions | slowly |
| `/var/lib/orva/.admin-key` | recovery key | static |
| `/var/lib/orva/functions/*/versions/<hash>/` | rollback targets | bounded by `versions_to_keep` |
| `/var/lib/orva/functions/*/current` (symlink) | active version pointer | static |
| `/var/lib/orva/rootfs/` | ~600 MB of language base images | static; rebuilt on first boot if missing |

A backup of the DB + version archives is sufficient to recover
everything. The rootfs trees are reproducible from the official Docker
images — orvad's entrypoint regenerates them if the volume is empty.

## Log rotation

Orvad writes to stdout. Three sources:

1. **HTTP request logs** — one line per request via `slog` (JSON if
   you set `log.format=json`).
2. **Function execution logs** — stored in SQLite (`execution_logs`
   table), not on disk.
3. **Build logs** — same, in `build_logs`.

The SQLite tables grow unbounded today. The `system_config.log_retention_days`
knob is a **placeholder** — automatic prune isn't implemented yet
(planned for a future release). Manual prune:

```sql
-- run periodically (cron):
docker exec orva sqlite3 /var/lib/orva/orva.db <<SQL
DELETE FROM execution_logs WHERE execution_id IN
  (SELECT id FROM executions WHERE started_at < datetime('now', '-7 days'));
DELETE FROM executions WHERE started_at < datetime('now', '-7 days');
DELETE FROM build_logs WHERE deployment_id IN
  (SELECT id FROM deployments WHERE submitted_at < datetime('now', '-30 days'));
VACUUM;
SQL
```

Docker stdout logs rotate via the daemon's `log-driver` config (the
shipped `docker-compose.yml` sets `max-size: 10m, max-file: 5`).

## Upgrades

### Docker

```bash
docker pull ghcr.io/harsh-2002/orva:latest
docker stop orva && docker rm orva
# re-run with the same volume mount
docker run -d --name orva ... -v orva-data:/var/lib/orva ghcr.io/harsh-2002/orva:latest
```

The DB schema migrations are idempotent additive ALTERs — running a
newer image on an older volume is safe. **Downgrade is not.**

### Bare metal

```bash
sudo systemctl stop orva
curl -fsSL https://github.com/Harsh-2002/Orva/releases/latest/download/install.sh | sudo sh
sudo systemctl start orva
```

The installer is idempotent — same data dir is preserved.

### Downtime

Image swap: ~5 seconds (orvad shuts down cleanly within `TimeoutStopSec=30s`,
new image starts in ~2 seconds, healthcheck passes within 15 s).

In-flight invocations during the swap: their HTTP connection drops.
Retry logic at the caller (or the SDK) handles it.

## Reverse proxy considerations

- **Body size**: increase the proxy's body limit if you'll deploy
  large tarballs. Default Orva cap is 6 MB for invoke bodies; the
  deploy endpoint accepts up to `system_config.max_code_size_bytes`
  (50 MB).
- **Read timeout**: must exceed the longest function `timeout_ms`. SSE
  streams need a long read timeout (≥ 5 min recommended).
- **HTTP/2**: helps with the dashboard's parallel API calls. SSE works
  over both HTTP/1.1 and HTTP/2.
- **WebSockets**: not used by Orva. SSE-only.

## Multi-host

Not supported. Orva is single-host by design. Two patterns to scale:

- **Vertical**: bigger box, raise `sandbox.max_concurrent`, more RAM.
  This is the easy answer for sub-1000-req/s aggregate workloads.
- **Stamp out copies**: run multiple independent Orva instances,
  shard functions across them at the LB layer (deterministic hash on
  function name → host). Each instance has its own SQLite, its own
  data dir. No state sharing.

If you genuinely need clustered serverless, this isn't the platform.

## Monitoring

Three integration points:

1. **`GET /api/v1/system/metrics`** — Prometheus text format. Scrape
   from your existing Prometheus.
2. **`GET /api/v1/system/health`** — single-line health probe for
   load balancers (`200 {"status":"ok"}`).
3. **Structured logs** — set `log.format=json` and ship to your log
   aggregator (Loki, Elasticsearch, datadog, whatever).

The dashboard's live metrics tiles + invocation log are an alternative
to Prometheus for small deployments.

## Security checklist before exposing publicly

- [ ] HTTPS terminator in front (caddy / nginx / traefik / cloudflared)
- [ ] `network_mode: isolated` is the default — verify your functions
      stayed on it (operator can opt into `bridge` for outbound)
- [ ] Bootstrap admin key rotated — issue a new key via the dashboard
      and delete the bootstrap one (or move `.admin-key` out-of-band)
- [ ] API keys for clients have only the permissions they need
      (`invoke` only is enough for end-users; reserve `admin` for ops)
- [ ] Backup automation in place (see Backups above)
- [ ] Disk growth monitored — `versions_to_keep` × deps size × num
      functions can chew through GBs
- [ ] [`docs/SECURITY.md`](SECURITY.md) read end-to-end so you understand
      what's protected and what isn't

## When something breaks

See [`docs/OPERATIONS.md`](OPERATIONS.md) for the runbook.
