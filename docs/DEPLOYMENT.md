# Production deployment notes

> Orva is in active development and the maintainers don't recommend it
> for production-critical workloads yet. The notes below are for
> operators running it in homelabs, side projects, internal tools,
> staging environments — places where a few hours of downtime is
> acceptable while bugs get sorted out.

## Sizing

Treat **2 CPU + 4 GB RAM** as a reference test size, not a promise of a
particular request rate or function count. A 30-minute isolated-VM test at
200 offered requests/s across one Node and one Python function returned and
persisted all 360,000 calls without writer loss or cgroup OOM. A separate
50,000-request burst at 1,000 clients returned every call but shed optional
telemetry, so its peak rate is not a sustainable-capacity guarantee. The
useful worker count depends on each function's memory footprint, execution
time, storage working set, and burst pattern; see [CAPACITY.md](CAPACITY.md)
for the measured runs and their limits. Watch `orva system metrics` on your
own workload before deciding how much headroom to reserve.

For bare-metal upgrades, use the installer so the server binary, runtime
rootfs, adapters, and SDK files stay in step. If deliberately swapping only
the binary for a development build against existing rootfs trees, run that
new binary's `orva setup --skip-nsjail --data-dir /var/lib/orva` before
starting it. An old adapter without the server's readiness handshake can
make every invocation wait for a worker and return HTTP 429 even though the
server reports a healthy sandbox runtime.

Builds now use the shipped runtime inside the build jail to reject invalid
JavaScript and Python entrypoints before installing dependencies. A TypeScript
entrypoint requires `tsconfig.json` and still compiles with `tsc`. Read the
deployment log for the file and line when a syntax check fails; the previous
live version remains in service. An unavailable jail or runtime fails the
build rather than silently bypassing the check.

## Runtime selection

The shipped `docker-compose.yml` uses Docker's default container
runtime (`runc`) — the right choice for homelab and trusted-code
use. To run Orva under a stricter, hypervisor-class runtime (Kata
Containers with Cloud Hypervisor underneath), add one line to the
`orva` service in your local `docker-compose.yml`:

```yaml
services:
  orva:
    runtime: kata-clh   # or: kata    (QEMU hypervisor)
    # ...rest unchanged
```

Then recreate:

```bash
docker compose down && docker compose up -d
docker inspect orva --format 'runtime={{.HostConfig.Runtime}}'
# → runtime=kata-clh
```

Prerequisites (Kata installed + the runtime registered in
`/etc/docker/daemon.json`) and the measured perf cost (~75–80 %
throughput tax, ~14 s slower container-start for QEMU vs CLH) are in
[`docs/KATA.md`](KATA.md). Kata-CLH is the recommended Kata path when
you want hypervisor-class isolation around Orva.

## TLS

orvad **does not terminate TLS itself.** Run a reverse proxy in front:
caddy, nginx, traefik, cloudflared. The browser's clipboard API
silently fails on plain HTTP from non-localhost — the dashboard
becomes partially broken.

If your proxy forwards `X-Forwarded-Proto: https` (the nginx example below does), Orva sets the `Secure` flag on session cookies by itself. Set `ORVA_SECURE_COOKIES=true` only when it cannot see the real scheme so
session cookies are only sent over HTTPS.

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
| `/var/lib/orva/.admin-key` | recovery key — the only persisted plaintext copy | static |
| `/var/lib/orva/.master.key` | **critical** — the AES-256-GCM key every function secret is encrypted with | static |
| `/var/lib/orva/functions/*/versions/<hash>/` | rollback targets | bounded by `versions_to_keep` |
| `/var/lib/orva/functions/*/current` (symlink) | active version pointer | static |
| `/var/lib/orva/rootfs/` | ~600 MB of language base images | static; re-seeded by the Docker entrypoint on an empty volume, or by `install.sh` on bare metal |

**A backup of the database and version archives alone is not sufficient.**
Secrets are stored encrypted; `.master.key` is the only copy of the key that
decrypts them, and it lives beside the database rather than inside it. Restore
without it and every secret is unrecoverable ciphertext — the functions come
back and then fail at runtime on a missing credential. Back up `.master.key`
and `.admin-key` together with `orva.db`, and treat them with the same care as
the database itself.

The rootfs trees are the one thing you can safely omit: the Docker entrypoint
copies them out of the image when the volume is empty, and `scripts/install.sh`
downloads them for a bare-metal install. Neither rebuilds them on an existing
install that has lost them — re-run the installer, or recreate the container.

## Log rotation

Orvad writes to stdout. Three sources:

1. **HTTP request logs** — one line per request via `slog`, always JSON.
   The format is fixed; there is no knob to change it.
2. **Function execution logs** — stored in SQLite (`execution_logs`
   table), not on disk.
3. **Build logs** — same, in `build_logs`.

Execution history is pruned automatically: `system_config.execution_retention_days`
(default 30; `0` disables it) is first applied one hour after startup, then every 24 hours, removing
old `executions` rows together with their logs, captured requests, structured log
entries and user spans. See [CONFIG.md](CONFIG.md).

Build logs **are** covered: the same sweep removes `build_logs` for terminal
deployments past the cutoff, along with finished `jobs`, `webhook_deliveries`
and expired `sessions`. None of that needed a cron job before this was true,
and none needs one now.

If you are catching up an instance that ran for a long time before this
existed, a one-off compaction reclaims the space the sweep has since freed:

```bash
docker exec orva sqlite3 /var/lib/orva/orva.db "VACUUM;"
```

Prefer `POST /api/v1/system/vacuum` (or the dashboard's Compact database
button) over raw sqlite3 — it checkpoints the WAL first and reports a busy
checkpoint instead of silently compacting a stale file.

Docker stdout logs rotate via the daemon's `log-driver` config (the
shipped `docker-compose.yml` sets `max-size: 10m, max-file: 5`).

## Upgrades

### Docker

```bash
# With the shipped compose (recommended — it carries all the sandbox flags):
docker compose pull && docker compose up -d

# Or with plain docker run, re-passing the full flag set and the same volume:
docker pull ghcr.io/harsh-2002/orva:latest
docker stop orva && docker rm orva
docker run -d --name orva -p 8443:8443 \
  --pid host --cgroupns host \
  --cap-add SYS_ADMIN \
  --security-opt seccomp=unconfined \
  --security-opt apparmor=unconfined \
  --security-opt systempaths=unconfined \
  --device /dev/net/tun \
  -v orva-data:/var/lib/orva \
  -v /sys/fs/cgroup:/sys/fs/cgroup:rw \
  ghcr.io/harsh-2002/orva:latest
```

`--pid host` + `--cgroupns host` are required on the runc runtime: nsjail
enrolls each sandbox PID in the host cgroup hierarchy, and omitting them
makes every invocation fail with `Launching child process failed`.
The entrypoint moves its known `tini` supervisor and short-lived CLI helper
into `orva.supervisor`; the daemon creates `orva.daemon` and `orva.workers` as
sibling leaves beneath its own container cgroup. That leaves the parent empty
so cgroup-v2 domain controllers can be enabled, while Docker's CPU/memory
budget still contains the sandboxes. No worker group is created at the host
cgroup root. Docker health checks and `docker exec` continue to work from the
supervisor leaf. Check system health:
`sandbox.resource_limits=cgroup_v2` means the child controls were verified;
`rlimit_only` means hard per-function CPU/memory/PID caps are unavailable on
this host and should be fixed before treating them as a security boundary.

The DB schema migrations are idempotent additive ALTERs, so running a newer
image on an older volume is safe — with one carve-out. The **UUIDv7 id
migration** rewrites every storage id and renames `<dataDir>/functions/<id>/`
to match, in a boot step that is fatal on failure by design. It is idempotent
and resumable, and your code is never at risk, but it is one-way: **back up
before upgrading across it.** See [Upgrading across the UUIDv7 id
migration](OPERATIONS.md#upgrading-across-the-uuidv7-id-migration).
**Downgrade is not safe.**

### Bare metal

```bash
sudo systemctl stop orva
curl -fsSL https://github.com/Harsh-2002/Orva/releases/latest/download/install.sh | sudo sh
sudo systemctl start orva
```

The installer is idempotent — same data dir is preserved. Re-running it also
rewrites the service unit, which is how a unit predating
`RestartForceExitStatus=70` picks that up. Without it, `orva backup restore`
exits 70 to force a restart and systemd's `Restart=on-failure` leaves the
service down instead. Verify with
`systemctl cat orva | grep RestartForceExitStatus`. On Alpine/OpenRC the same
restore needs `supervisor="supervise-daemon"` in `/etc/init.d/orva`
(`start-stop-daemon` never respawns); verify with
`grep supervise-daemon /etc/init.d/orva`.

### Downtime

Image swap: ~5 seconds (orvad shuts down cleanly within `TimeoutStopSec=30s`,
new image starts in ~2 seconds, healthcheck passes within 15 s).

In-flight invocations during the swap: their HTTP connection drops.
Retry logic at the caller (or the SDK) handles it.

## Reverse proxy considerations

- **Body size**: increase the proxy's body limit if you'll deploy
  large tarballs. Default Orva cap is 6 MB for invoke bodies (an
  over-limit body now returns `413 PAYLOAD_TOO_LARGE`, including chunked
  uploads that carry no `Content-Length`); the
  deploy endpoint accepts up to 50 MB, a compile-time constant
  (`builder.DefaultMaxCodeSize`) with no config key.
- **Read timeout**: must exceed the longest function `timeout_ms`. SSE
  streams need a long read timeout (≥ 5 min recommended).
- **`X-Forwarded-For`**: Orva **ignores** this header (and `X-Real-IP`)
  unless you set `ORVA_TRUSTED_PROXY=true`. Left off, every rate limiter —
  per-function `rate_limit_per_min`, the login brute-force throttle, and the
  OAuth dynamic-registration limiter that is the only abuse control on the
  unauthenticated `POST /register` — keys on your proxy's own address, so
  every client behind it shares one bucket. **Set it whenever a proxy fronts
  Orva**, and **not before**: trusting a client-settable value means a caller
  can have no rate limit at all simply by varying it. When on, Orva reads the
  **rightmost** entry, which is the one your proxy appended
  (`$proxy_add_x_forwarded_for` in the nginx example above; Caddy and Traefik
  append by default). Anything further left was sent by the client. Also make
  sure Orva is reachable *only* through the proxy — `ORVA_HOST=127.0.0.1`, or
  a firewall — because the flag is an assertion that the peer always is the
  proxy. Make sure that proxy is the one writing `X-Forwarded-For`: a config
  that sets only `X-Real-IP` and passes the client's `X-Forwarded-For` through
  untouched leaves the bucket client-chosen even with the flag on.
- **`X-Forwarded-Proto`**: forward it (the nginx example above does) and Orva
  marks session cookies `Secure` automatically, without `ORVA_SECURE_COOKIES`.
- **HTTP/2**: helps with the dashboard's parallel API calls. SSE works
  over both HTTP/1.1 and HTTP/2.
- **WebSockets**: not used by Orva. SSE-only.

## Multi-host

Not supported. Orva is single-host by design. Two patterns to scale:

- **Vertical**: more memory can support more warm sandboxes, while more CPU
  can help CPU-bound functions and cold starts. Orva also has a host-wide
  execution limiter (default `max(200, NumCPU × 64)`), but worker admission
  can become constrained earlier by detected memory, file-descriptor, and
  CPU envelopes. Measure your own workload before sizing.
- **Stamp out copies**: run multiple independent Orva instances,
  shard functions across them at the LB layer (deterministic hash on
  function name → host). Each instance has its own SQLite, its own
  data dir. No state sharing.

If you genuinely need clustered serverless, this isn't the platform.

## Monitoring

Three integration points:

1. **`GET /api/v1/system/metrics`** — Prometheus text format. Scrape
   from your existing Prometheus.
2. **`GET /api/v1/system/health`** — health probe for load balancers.
   `200` + `{"status":"healthy", …}` when up; `503` +
   `{"status":"degraded"}` when the database ping fails. Match on
   `healthy`, not `ok`.
3. **Structured logs** — orvad only ever emits JSON, so ship stdout
   straight to your aggregator (Loki, Elasticsearch, Datadog, whatever).

The dashboard's live metrics tiles + invocation log are an alternative
to Prometheus for small deployments.

## Security checklist before exposing publicly

- [ ] HTTPS terminator in front (caddy / nginx / traefik / cloudflared)
- [ ] `network_mode: none` is the default — verify your functions
      stayed on it (operator can opt into `egress` per-function for outbound HTTPS)
- [ ] Egress policy reviewed on the Egress controls page. Note that it also filters
      orvad's own outbound calls, so enabling a broad RFC1918 rule
      (`10.0.0.0/8` and friends) blocks internal package mirrors, LAN-hosted AI
      providers, and internal webhook targets too — block a narrower CIDR
      instead. `status.enforced` must be `true`; if it is `false`, egress
      functions will not spawn at all
      ([`SECURITY.md`](SECURITY.md#sandbox-egress-policy))
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
