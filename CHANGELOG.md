# Changelog

Notable changes to Orva, newest first.

Orva releases are dated (`vYYYY.MM.DD`), not semver, so "breaking" is called
out explicitly rather than implied by a version bump. **Read the Breaking
section of every release between your current version and the one you are
upgrading to.**

Entries describe what changes *for an operator*. Implementation detail lives in
the commit messages; `git log v2026.08.05..HEAD` is the full record.

## Unreleased

### Breaking

- **`POST /api/v1/channels` now requires the `admin` permission** (was `write`).
  A channel token is a long-lived bearer credential whose tools bypass the
  target function's `auth_mode`, so a `write`-scoped key could mint itself a
  credential that outranked it — for example a CI key deliberately scoped
  without `invoke` could create a channel over a payments function and call it
  unsigned. Automation that creates channels with a non-admin key will now get
  `403`.

- **All of `/api/v1/firewall` now requires the `admin` permission** (was
  `read`/`write`), *including reads*. Egress blocklists and the DNS every
  sandbox resolves through are instance-wide security state; a deploy-scoped
  key could previously repoint every sandbox's resolver. Automation touching
  firewall endpoints with a non-admin key will now get `403`.

- **`orva backup restore` exits `70` instead of `0`.** Exiting 0 told systemd's
  `Restart=on-failure` the job had finished, so a *successful* restore left the
  service down on bare metal. The installer's unit now carries
  `RestartForceExitStatus=70`. **A systemd unit created before this release
  needs that line**, or re-run `install.sh`:

  ```bash
  systemctl cat orva | grep RestartForceExitStatus   # verify
  ```

- **`orva init` has been removed.** It wrote an `orva.yaml` that nothing read
  and pointed at a `--config` flag that does not exist. Configuration has
  always been environment variables only (`docs/CONFIG.md`).

- **Function responses now carry a `Content-Security-Policy`.** Function output
  shares an origin with the dashboard and the API, so a public function
  reflecting its input was same-origin XSS against `/api/v1/*`. Output now runs
  in an *opaque* origin: scripts and forms still work, but `document.cookie`,
  `localStorage` and `sessionStorage` are unavailable. **HTML functions holding
  state in the browser must move it server-side** (the KV store).

- **Inbound `x-orva-*` request headers are stripped.** They are the server's
  channel to its own adapter, and two were forgeable: `x-orva-trigger` defines
  `orva.webhook.parse(event).verified`, so a caller could pass an HMAC check
  that never ran, and a negative `x-orva-call-depth` disabled the
  function-to-function recursion cap. If your handler read an `x-orva-*` header
  that the *caller* set, it will no longer see it.

- **Onboarding an instance that is already in use requires an admin key.**
  `POST /api/v1/auth/onboard` is exempt from the auth middleware and returns a
  session cookie that bypasses the permission model, and `CreateUser` is only
  ever called from there — so an operator who works exclusively through API
  keys never onboarded, and the endpoint stayed open to anyone who could reach
  the port. A virgin instance still onboards from the browser with no
  credentials; one with operator-minted keys or deployed functions now needs an
  `admin` key.

### Upgrade notes

- **Back up before upgrading.** This release contains the UUIDv7 id migration,
  which rewrites every storage id *and* renames `<dataDir>/functions/<id>/` to
  match. It is idempotent, resumable, and fatal-on-failure by design, but it is
  one-way: there is no down migration and the ids are freshly generated, not
  derived. See
  [Upgrading across the UUIDv7 id migration](docs/OPERATIONS.md#upgrading-across-the-uuidv7-id-migration).

- Rehearse it if you want certainty: `bash test/migration-rehearsal.sh` boots
  the real binary against a seeded legacy data dir and asserts that ids move,
  directories follow, and source survives a GC tick. It needs neither nsjail
  nor Docker.

### Fixed — data loss and availability

- The UUIDv7 migration rewrote `functions.id` but nothing renamed the matching
  directory on disk. Every path is built from the database id, so a
  *successful* migration left every function unable to spawn — and the build GC
  then removed the trees as orphans, minutes after a boot that reported
  success. A second bug (a missing FK child table) was the only thing
  preventing this by aborting the migration first.
- The async writer dropped data two ways: one failing statement rolled back its
  whole batch, taking up to 49 unrelated execution records with it; and any
  stall on the single write connection — `VACUUM` runs on it — discarded
  batches outright. Batches are now retried, and a failed batch is re-applied
  per-job under savepoints.
- Shutdown could panic with `send on closed channel` when a cron or handler
  outlived the drain, turning a clean exit into what a supervisor reads as a
  crash loop.
- Jobs and webhook deliveries abandoned in `running` were never reclaimed:
  never retried despite `max_attempts`, never marked failed, invisible in
  failed lists. Deployments abandoned mid-build were likewise stuck forever.
- Retry backoff overflowed at 63 attempts and turned a failing job into a hot
  loop, spawning a sandbox every scheduler tick.
- nsjail cgroups leaked on every worker kill. Beyond disk, every spawn scans
  that directory, so the leak eventually broke memory sampling and left the
  autoscaler permanently over-reserving.

### Fixed — security

- A `read`+`write` key could read the plaintext bootstrap admin key by pointing
  a function's `entrypoint` at `../../../.admin-key` and calling
  `GET /functions/{id}/source`. `entrypoint` is now validated on write and
  contained on read.
- The DNS `search` field accepted newlines, which injected a `nameserver` line
  ahead of the real ones in every sandbox's `resolv.conf`.
- Revoking an API key through MCP or the AI assistant did not evict it from the
  auth cache, so it kept working against `/api/v1/*` until the process
  restarted — while the tool reported success.
- An OAuth client's registered scope was a default, not a ceiling: a client
  registered `scope="read"` could request and be issued `admin`.
- The OAuth dynamic-registration rate limiter — the only abuse control on the
  unauthenticated `POST /register` — keyed on a client-settable
  `X-Forwarded-For`. It now trusts that header only under the new
  `ORVA_TRUSTED_PROXY`.
- Session cookies now set `Secure` automatically when the request arrived over
  TLS or carried `X-Forwarded-Proto: https`. `ORVA_SECURE_COOKIES` remains as a
  force-on override.
- Session expiry was stored in local time and compared against UTC, so the
  effective lifetime was wrong by the server's offset (a configured 24h expired
  after 17h under `TZ=America/Los_Angeles`).
- Deploy archives no longer carry symlinks: `orva deploy` dereferences them
  while packing, and the builder refuses link entries outright.

### Fixed — correctness

- Execution time filters compared timestamps as text against a
  differently-formatted stored value, so "Last 1 hour" returned nothing at all
  and `orva executions prune` over-deleted by up to a day at the boundary.
- `GET /api/v1/functions/{id}` now resolves names as well as ids. It was the
  only function endpoint that did not, and the dashboard addresses functions by
  name while listing 20 at a time — so any function outside the newest twenty
  reported "Function not found".
- Cron, jobs, function-to-function and inbound-webhook invocations never
  reached the invocation counters, so an instance running entirely on cron
  reported zero invocations.
- The documented error taxonomy (`FUNCTION_BUSY`, `MEMORY_EXHAUSTED`,
  `EGRESS_POLICY_UNAVAILABLE`, `TIMEOUT`) applied on one invoke path of five;
  the rest flattened everything to a bare `503 POOL_ERROR`.
- Streaming responses were severed at the 60s write deadline regardless of
  `stream_max_seconds`.
- An over-limit **chunked** request body was silently truncated and handed to
  the function with a `200`; it now returns `413`.
- `orva deploy` aborted on any source tree containing a symlink.
- The CLI config was mode `0600` only when it did not already exist, so
  `orva login` could write an API key into a world-readable file; and
  `orva backup download` wrote its archive — which contains the secrets master
  key — mode `0644`.
- The API key was forwarded across cross-host redirects.
- `orva login --api-key -` reads the key from stdin, so it need not appear in
  `ps` or shell history.

### Fixed — dashboard

- Added a permission selector to API key creation. Every dashboard-minted key
  was previously full admin, regardless of what the operator intended.
- Inbound webhooks can be paused and resumed; channels can have their function
  set edited. Both were supported server-side with no way to reach them.
- The AI model menu accepts a typed model id when the provider's listing fails,
  instead of leaving Send permanently disabled with no explanation.
- Failed fetches no longer render as empty state: the Firewall page showed "No
  egress policy is compiled" — a security alarm — on any transient error, and
  an expired session left every list silently blank rather than prompting a
  sign-in.

## v2026.08.05

See the [release notes](https://github.com/Harsh-2002/Orva/releases/tag/v2026.08.05).
