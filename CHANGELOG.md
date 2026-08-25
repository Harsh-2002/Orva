# Changelog

Notable changes to Orva, newest first.

Orva releases are dated (`vYYYY.MM.DD`), not semver, so "breaking" is called
out explicitly rather than implied by a version bump. **Read the Breaking
section of every release between your current version and the one you are
upgrading to.**

Entries describe what changes *for an operator*. Implementation detail lives in
the commit messages; `git log v2026.08.05..HEAD` is the full record.

## Unreleased

### Changed

- **Rolling back promotes an existing deployment instead of appending a new
  one.** The version history used to grow by one row every time you rolled
  back, so after a rollback the newest version was not the one serving and
  there was no way to tell which one was. The deployment list now marks the
  live version and rollback simply moves that marker; the row count does not
  change. Editing a function still creates a new version, as before.

- **Deployment version numbers are gapless from 1.** They used to be taken from
  `functions.version`, which is a mutation counter that the dashboard bumps
  twice per deploy (once for the settings PUT, once for the build), so a brand
  new function's first deploy was `v2` and its history read v3, v5, v7. Numbers
  now come from the deployment sequence itself. **Existing deployment rows keep
  the numbers they were given** — the sequence continues from the highest one
  already recorded, so no history is renumbered.

### Fixed

- **Revoking an API key from the AI assistant or an MCP client now actually
  revokes it.** The dashboard and CLI evicted the key from the authentication
  cache; the MCP tool deleted the database row and stopped there. Because
  `/mcp` and `/fn/` read credentials straight from the database, the key
  stopped working on exactly the surface it was revoked from — while it kept
  full access to the REST API, including minting new keys, reading secrets and
  downloading a backup, until the server restarted. The revocation looked
  confirmed and was not. **If you have revoked a key through the AI sidebar or
  an MCP client, restart Orva** — that key may still be live. Keys revoked from
  the dashboard or `orva keys revoke` were never affected.

- **Logging out, revoking a device, refreshing a session, and changing your
  password now take effect at once.** All four deleted the session row but left
  the session in the authentication cache, so the cookie kept working for up to
  30 seconds afterwards — with no permission check, meaning full operator
  access on every API route. For a password change that is the entire window
  the change exists to close. (A request already mid-flight through the
  authentication check when you revoke can still finish and re-cache; that
  window is now bounded by the 30-second re-check below rather than by a
  restart.)

- **A cached API key is now re-checked against the database at least every 30
  seconds.** The cache had no expiry, so anything that revoked a key without
  evicting it stayed stale until the process restarted. Sessions were already
  re-checked on this interval; keys now match. This is the backstop, not the
  fix — every revocation path evicts directly.

- **`docker compose up -d` no longer locks you out of the dashboard on a LAN
  address.** The shipped `docker-compose.yml` set `ORVA_SECURE_COOKIES=true`
  while publishing plain HTTP on port 3000. A browser will not store a `Secure`
  cookie over `http://` on anything but `localhost`, so signing in from
  `http://<your-lan-ip>:3000` appeared to succeed and then bounced straight back
  to the login screen. The setting predates Orva learning to detect the scheme
  on its own and is now left unset: put a TLS proxy in front and the `Secure`
  flag is applied automatically. **If you copied that line into your own compose
  file and reach Orva over plain HTTP, remove it.**

- **Session cookies keep the `Secure` flag behind a proxy chain that appends to
  `X-Forwarded-Proto`.** Orva compared the whole header against `https`, but a
  chain that appends rather than replaces sends a list — `https, http` — and
  Orva read that as plaintext. Such an instance dropped `Secure` from its
  session cookie and advertised its OAuth issuer and MCP `invoke_url` as
  `http://` over a TLS connection, which strict OAuth clients reject. The
  leftmost entry, which is the scheme the client actually spoke, is now the one
  that counts. Affects HAProxy `add-header`, Envoy, and ingress controllers with
  append semantics; plain nginx and Caddy replace the header and were never hit.

- **Logging out clears the cookie with the attributes it was set with.** The
  clearing cookie was written out as its own copy of the same eight-line
  literal and had drifted from the three that set it, carrying neither `Secure`
  nor `SameSite`. All four now go through one place.

- **A `tsconfig.json` can no longer point a build outside its own directory.**
  `compilerOptions.outDir` was taken verbatim from the uploaded tarball and
  joined onto the function's version directory, so a value like `../../..`
  walked the lookup out of the function's tree — and the path it selected was
  stored as the file the sandbox executes. The build now refuses a traversing
  or absolute `outDir` and falls back to `dist`, and both the build and
  build-cache paths resolve entrypoints through a directory handle that cannot
  be escaped. Rollback likewise refuses a version identifier that is not a
  content hash. (The handle also closes the symlink spelling of the same
  escape; archive extraction already refused link entries, so that half is
  depth rather than a hole that was open.)

- **Rolling back a TypeScript function no longer breaks it.** `entrypoint`
  carried two meanings: the file you wrote, and — after a successful `tsc` —
  the compiler's output. The editor served compiled JavaScript instead of your
  source, and re-deploying failed with `entrypoint not found: dist/handler.js`.
  The authored file and the build output are now separate; the build pipeline
  never overwrites the file you wrote.

  Rolling back onto a deployment recorded *before* this change returned a
  working `200` but pointed the sandbox at the `.ts` source, which Node cannot
  execute — every invocation of the rolled-back version failed with
  `WORKER_CRASHED`. The file a version runs is now derived from what that
  version has on disk rather than from what its snapshot remembers, so this is
  correct for deployments made before and after the change. Existing rows are
  migrated on boot; no action needed.

- **The mobile navigation drawer can be closed again.** The drawer's backdrop
  and the top bar were on the same stacking level, so the backdrop covered the
  close button: with the menu open, tapping the X did nothing and the only way
  out was tapping the dimmed area.

- Accessibility and touch-target fixes: the "Log out" button and the Settings
  "Docs" link both fell below the 4.5:1 AA contrast floor, one editor syntax
  colour missed it, and the function-name control in the invocations table was
  below the 44 px touch floor on tablets.

- **A list that fails to load no longer claims you have nothing.** Jobs,
  scheduled jobs, API keys, webhooks and activity each caught a failed request,
  logged it to the browser console and then rendered their empty state, so a
  server error and an empty account looked identical -- "No API keys yet" was
  shown as fact when the request had simply failed. Each now shows what could
  not be loaded, the reason, and a Retry.

- **Editing a KV value no longer changes its type.** The inspect drawer showed a
  normalised rendering of the stored value but wrote back whatever was in the
  box, so opening the string `"123"`, changing nothing and pressing Save stored
  the number `123`; `"true"` became a boolean and a JSON-encoded string became an
  object. The drawer now shows the value exactly as stored.

- **Testing an inbound webhook no longer asks for the secret**, and works for
  every signature format. The dashboard used to ask the operator to paste back
  the plaintext secret -- one Orva already holds -- then sign in the browser,
  which could only produce 2 of the 5 formats and refused Stripe, Slack and
  base64 outright. Signing now happens on the server.

- **Cron schedule failures are reported.** Pausing, resuming or deleting a
  schedule failed silently: the request errored, the row stayed as it was, and
  nothing was shown.

- **nsjail's own warnings no longer appear in your function's stderr.** Every
  cold start prepended two lines about the sandbox running as UID 0 with
  "root-level access to files" to the invocation's logs, and fed them to the
  dashboard's suggest-a-fix prompt as if the handler had written them. nsjail
  errors are still kept -- they diagnose a failed spawn.

- **Creating a channel with a name that is taken returns `409`, not `500`.** A
  name collision is a client error; reporting it as an internal failure told
  operators the server had broken and told retry logic the request was worth
  repeating. Every other create already returned `409`.

- The function-name control in the KV browser was below the 44 px touch floor.

- The firewall page's **"Apply now" is now "Refresh policy"**. Adding, editing or
  deleting a rule already compiles and publishes a new policy generation; the
  button re-resolves hostname rules to current IPs. Calling it "Apply" implied
  blocks sat inert until it was pressed.

## v2026.08.21

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
  autoscaler permanently over-reserving. Reclaimed at reap time, on every GC
  tick, and at startup — measured on a load run, 224 spawns previously left
  82 directories behind.

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

### Verified

Beyond CI's gate (`test/e2e/run.py` with `ORVA_REQUIRE_SANDBOX=1` on amd64 and
arm64, six server installers, six CLI installers, native systemd, docker
smoke, `-race`, govulncheck):

- **The id migration was rehearsed against the real binary**
  (`test/migration-rehearsal.sh`): a seeded legacy data dir, three boots, and a
  GC tick waited out — ids moved, directories followed, `current` resolved,
  source survived, and the migration did not re-run once its marker was set.
- **`test/atscale.sh` was run against a live instance** with nsjail and both
  runtime rootfs trees: 20 functions deployed, 5 hammered concurrently.
  nsjail process count matched the pool exactly (32 vs 32) and every pool
  stayed within its `effective_max`. Throughput figures from that run are a
  smoke test, not a benchmark — the host was shared.

## v2026.08.05

See the [release notes](https://github.com/Harsh-2002/Orva/releases/tag/v2026.08.05).