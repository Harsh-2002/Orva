# Changelog

Notable changes to Orva, newest first.

Orva releases are dated (`vYYYY.MM.DD`), not semver, so "breaking" is called
out explicitly rather than implied by a version bump. **Read the Breaking
section of every release between your current version and the one you are
upgrading to.**

Entries describe what changes *for an operator*. Implementation detail lives in
the commit messages. Only the current release's tag exists — older tags are
pruned with their releases — so `git log v2026.08.28..HEAD` is the range for
anything unreleased, and the sections below are the record for everything
before it.

## Unreleased

### Fixed

- **Function secrets were visible in the host process list.** Decrypted
  secrets and each worker's `ORVA_INTERNAL_TOKEN` were passed to nsjail as
  `--env KEY=VALUE` command-line arguments. `/proc/<pid>/cmdline` is
  world-readable, and the shipped compose file runs with `pid: host`, so every
  value was readable by any local user for as long as the call ran — and the
  full argument list was written to the debug log as well. Values now travel in
  the sandbox process's environment and the command line carries only the
  variable name. `/proc/<pid>/environ` is readable only by the user Orva runs
  as, and by root. Nothing changes inside the sandbox: a function sees exactly
  the same variables it did before.

- **Four dashboard pages showed, or acted on, the function you were looking at
  before.** Every view is cached in a `<keep-alive>`, and a cached view is not
  unmounted — it stays alive with a live `useRoute()`, so it kept reacting to
  navigations that belonged to another page. The editor wiped an unsaved code
  buffer as soon as you opened any page without a function in its URL (Docs,
  Firewall, the function list), and it still answered the global Cmd/Ctrl-S
  deploy shortcut from a function's KV or Deployments page, so that keystroke
  deployed the last-edited function with nothing on screen to say so. Inbound
  webhooks kept the first function's id for the rest of the session, aiming
  create, delete and test at the wrong function. Deployments listed the previous
  function's builds, with Rollback and Compare acting on them. Trace diagnostics
  rendered the previously-opened trace when you clicked a second one. Each view
  is now scoped by a route prop, which Vue leaves frozen while the view is
  cached: it reloads when *its own* URL changes and at no other time. An unsaved
  editor buffer now survives every trip away and back to the same function, and
  is replaced only when you deliberately open a different one.

- **A failed function list read "No functions deployed yet".** `/functions`
  swallowed the error and fell through to the first-run empty state, telling an
  operator their instance was empty when the request had simply failed. It now
  shows the same load-failure banner, with the server's message and a Retry, that
  the jobs, cron, webhooks, activity and API-key lists already used.
- **Documentation that shipped code which does not run.** Six examples were
  wrong, in the four places a function author actually reads: the canonical
  reference (and its three embedded copies), the dashboard's Docs page, the
  editor's Handler reference modal, and the prompt the in-product assistant
  generates code from. The cron API example named its request field with a UUID
  instead of `cron_expr`, so the documented `curl` always returned 400. The
  Python `jobs.enqueue` example used a UUID as a variable name and was a literal
  `SyntaxError`. The function-to-function and jobs examples indexed
  `event.body` as if the platform had parsed it — Python raised and returned
  500, Node silently sent an empty payload, which is worse. `event.body` has
  always been the raw request body as a string, and every one of these now says
  so and parses it. `docs/RUNTIMES.md` also promised `event.rawPath` and
  `event.httpMethod` aliases that neither adapter has ever provided; the promise
  is gone rather than the aliases added, because the event shape is a contract.
  Two MCP tool schemas claimed job and schedule ids arrive prefixed `job_` /
  `cron_`; both are bare UUIDv7s, and that description ships to every connected
  agent.
- **Concurrent builds wrote into each other's logs.** The build queue runs one
  worker per CPU and they all shared a single builder, each setting its log
  writer on that shared object and clearing it in a defer. So a build streamed
  its `pip`/`npm` output into another deployment's log, and the first build to
  finish cleared the writer out from under every build still running, which
  simply lost the rest. It is a data race, not only a mix-up — the race
  detector reports it. Each job now builds through its own copy.

- **The UUIDv7 id migration left `functions.active_deployment_id` dangling.**
  That column holds a deployment id but was added by `ALTER` with no foreign
  key, so the migration's automatic child discovery could not see it and it was
  missing from the hand-maintained list of unreferenced columns. Deployment ids
  were rewritten and the pointer was not, leaving it aimed at an id that no
  longer existed. The existing backfill could never repair it, because it only
  fills the column when it is empty.

- **Three installer papercuts.** `install.sh --help` printed its own `curl`
  one-liner with the URL missing, because `--help` exits before that URL is
  derived. The "already at this version, nothing to do" check could never be
  true: it compared `orva --version`'s output, `orva vX.Y.Z`, against `vX.Y.Z`.
  And `install-cli.ps1`'s "checksum entry missing" error deleted the checksums
  file immediately before quoting its first five lines, so the one diagnostic
  that would tell you what went wrong was always blank.

- **On Alpine, a successful `orva backup restore` left the server down
  permanently.** `orva backup restore` exits 70 on purpose so its supervisor
  restarts the process against the restored files, and the shipped systemd unit
  carries `RestartForceExitStatus=70` for exactly that. The OpenRC init script
  had no equivalent: it ran under `start-stop-daemon`, which never respawns
  anything. A restore that worked perfectly therefore ended with the server
  exited and nothing to bring it back — the same outage the systemd fix closed,
  still open on every Alpine bare-metal install. The unit now runs under
  `supervisor="supervise-daemon"`. OpenRC has no per-exit-status restart
  filter, so this respawns on any unexpected exit — a superset of
  `Restart=on-failure` that covers exit 70.

- **One header defeated every rate limit Orva has.** `X-Forwarded-For` (then
  `X-Real-IP`) decided the client identity that the per-function
  `rate_limit_per_min` cap and the unauthenticated login brute-force throttle
  bucket on, and it was trusted whether or not a proxy was in front of Orva.
  Any caller could send a different value per request and get a fresh bucket
  every time, so both limits were advisory. Both now key on the TCP peer
  address unless the operator opts in with `ORVA_TRUSTED_PROXY` — the same flag
  that already governed the OAuth dynamic-registration limiter, the only
  limiter it had ever reached. All three now resolve the caller through one
  function (`urlhint.ClientIP`) so they cannot drift apart again. When the flag
  is on, the rightmost `X-Forwarded-For` entry wins rather than the leftmost,
  and every `X-Forwarded-For` header line is read rather than the first: a
  proxy appends the peer it saw — as a new entry, or as a whole new header
  line — so anything before it is still what the client chose to send.

- **`orva upgrade` replaced a bare-metal server with the CLI, and the host
  never came back.** The Linux server binary registers every client subcommand
  so an operator can work from the server box — `upgrade` included. It always
  resolved the slim CLI asset `orva-cli-<os>-<arch>`, and `executablePath()`
  deliberately follows symlinks, so `/usr/local/bin/orva -> /opt/orva/bin/orva`
  meant the *server* binary was overwritten with a build that has no `serve`
  subcommand; systemd then failed to start it, on a box whose only remaining
  Orva binary could not run a server. It was not even a rare accident: the
  running server's checksum never matched the CLI asset's, so `orva upgrade`
  reported an upgrade as available on every single run, indefinitely. A server
  build now fetches the server asset `orva-<os>-<arch>` and verifies its
  SHA-256 under that name. Server assets are published for linux only, so a
  server build on any other platform now fails loudly instead of quietly
  settling for the CLI asset. `orva upgrade` still replaces the binary and
  nothing else, and now says so: restart the service afterwards, or re-run
  `install.sh` when the adapters, rootfs or service unit also need refreshing.

- **`--port` was silently ignored by the bare-metal installer, which then
  printed a URL nothing listened on.** The flag was parsed and used only by the
  Docker path (as the host side of the `PORT:8443` map). Neither the systemd
  unit nor the OpenRC script passed `ORVA_PORT`, so `install.sh --bare-metal
  --port 9000` produced a server on 8443 and a closing summary reading
  `http://<host>:9000`. Both units now export `ORVA_PORT`, so the flag does what
  it says and the summary is true.

- **Deleting a function permanently orphaned its traces and structured logs.**
  `DELETE /api/v1/functions/{id}` removed the function and cascaded to its
  `executions` rows, but three tables keyed by `execution_id` carry no foreign
  key — `execution_requests` (captured replay envelopes), `user_spans`
  (`orva.trace.span()`) and `execution_log_entries` (`orva.log.*`). The cascade
  could not reach them, and once the parent `executions` row was gone nothing
  could ever join them back, so every function ever deleted left unreachable
  rows behind forever — in the two fastest-growing tables on the instance.
  Deletion now removes those children first, in one transaction, off the same
  shared list retention uses. On first boot after upgrading, a batched one-shot
  sweep reclaims rows already stranded by earlier deletions. The sweep resumes
  across boots on a very large database and logs `reclaimed orphaned execution
  rows` when it removes anything; a database with nothing to reclaim pays one
  pass and never scans again.

- **Backup dropped every dotfile under `functions/`, so restored versions
  reported as garbage-collected.** The archive walk skipped any dot-prefixed
  entry, which was aimed at the `current` symlink — already excluded a few lines
  above by a symlink check. What it actually excluded was file content: the
  `.orva-ready` marker that tells Orva a version finished building, plus any
  `.env`, `.npmrc` or `.python-version` the author deployed (`orva deploy` packs
  hidden *files*; it only skips hidden *directories*). Without the marker,
  rollback and version diff return HTTP 410 `VERSION_GCD` for code sitting on
  disk, `details.available_hashes` comes back empty, and the version GC skips
  the function entirely — so `versions_to_keep` silently stops applying and old
  versions accumulate forever. A version directory is now captured whole;
  dot-prefixed litter beside one is still skipped.

  **Restore repairs archives already taken.** Every existing backup is missing
  these markers, so fixing only the archive side would have left them
  unrestorable. Restore now recreates `.orva-ready` for any version directory
  that arrives with content and no marker, writing the directory's own name,
  which is exactly what the builder writes. A half-finished build is never
  mistaken for a stripped one: the builder writes the marker into
  `versions/<hash>.tmp.<rand>/` and only then renames it into place, so a
  crashed build is a `.tmp.` directory and never a bare-hash one. Empty
  directories and `.tmp.` scratch are left alone.

- **The dashboard offered a container image tag that does not exist.** Settings
  → Build info showed `ghcr.io/harsh-2002/orva:<version>` with a copy button,
  but Orva publishes exactly one image tag, `:latest`; every per-version tag
  404s on `docker pull`, and a bare-metal install has no image at all.
  `/api/v1/system/health` now reports the image the deployment actually runs
  from — the published image stamps it, the shipped compose file passes through
  whatever it pulled — and reports it empty otherwise, where the dashboard hides
  the row. If you script on that field, it is now empty on bare-metal and
  self-built deployments; `version` remains the build identity.

- **The Docker image shipped an incomplete `orva` SDK.** The image's rootfs
  stage copied `orva.js` in under the name `index.js` and hand-wrote its own
  `package.json` claiming version `0.2.0` — five SDK releases stale — while
  omitting `orva.d.ts` and Python's `py.typed` entirely. The bare-metal
  installer never had this problem: `orva setup` installs the SDK from the
  binary's embedded copy, under the real filenames. Docker deployments now get
  the same files. Nothing an operator does changes; on the next container start
  the entrypoint replaces the SDK directory in a persistent volume, which also
  clears the stale `index.js` an older image left there.

### Upgrade notes

- **Re-take any backup you are keeping as a restore point.** Restoring an old
  archive with this build gives you working versions again — the markers are
  recreated on the way in — but a user dotfile such as `.env` was never written
  to that archive and cannot be recovered from it. Only a fresh backup contains
  them.
- To repair a data directory in place without a restore cycle, see
  "rollback fails with `VERSION_GCD`" in `docs/OPERATIONS.md`.
- **If a reverse proxy fronts your instance, set `ORVA_TRUSTED_PROXY=true`.**
  Orva no longer trusts `X-Forwarded-For` / `X-Real-IP` by default (see Fixed,
  above). Without the flag every rate limiter keys on the TCP peer — which
  behind a proxy is the proxy — so all of your clients share a single bucket:
  a function with `rate_limit_per_min: 60` starts returning 429 at 60 req/min
  *in total* rather than per caller. There is no data migration. The first
  request carrying either header while the flag is off logs one warning naming
  the variable, so a missed upgrade is greppable rather than silent.

  Set it only when the proxy is genuinely in front of every request, and keep
  Orva off the open network (`ORVA_HOST=127.0.0.1`, or a firewall): the flag
  asserts that the peer is always your proxy.
- **Bare-metal hosts: re-run `install.sh` to pick up the fixed service unit.**
  It rewrites the unit on an upgrade, which is what installs the OpenRC
  `supervise-daemon` supervisor (Alpine) — without it an `orva backup restore`
  there still leaves the host down. The rewrite also drops any `Environment=`
  you hand-added, and the unit now sets `ORVA_PORT` explicitly: if your server
  listens on anything other than 8443, pass `--port <n>` (or `ORVA_PORT=<n>`)
  to the reinstall, or it moves back to 8443.
- **Bare-metal hosts running `orva upgrade`:** it now replaces the server with
  the server binary rather than the CLI, but it still only replaces the binary.
  Restart the service after it runs, and prefer re-running `install.sh` when
  the runtime adapters, rootfs or the unit also need refreshing.

## v2026.08.28

A dashboard consistency pass: one picker everywhere, one size ladder, and a new
browser suite that measures controls against each other rather than against a
standard. Nothing here asks anything of an operator — no migration, no
configuration change, no re-issued credentials.

### Changed

- **One control, drawn one way, everywhere.** The dashboard had reached nine
  corner radii, `Refresh` at three heights with three icon sizes across six
  views, `Cancel` as a ghost button on seven screens and a secondary button on
  five, and `Copy secret` rendered four different ways in four places. Each of
  those was individually defensible and the set was not. Radii are down to four
  values from nine, every control sits on the size ladder, and a label that
  appears in two views is now the same control in both.

- **Every picker is the dashboard's own control, not the phone's.** Seventeen
  fields were still native `<select>`s, which hand a phone its operating-system
  wheel — several of them sitting beside a control that opened a panel instead,
  so two fields in the same form behaved differently. All of them are now one
  picker: a type-ahead box once a list runs long (which the timezone and
  template lists needed), grouped headings where the list had them, and a bottom
  sheet on a phone rather than a dropdown too small to hit.

- **Dialogs are sheets on a phone.** A modal used to be the desktop dialog with
  its margins turned down: a bordered, fully rounded card floating against the
  top of the screen, which is the one presentation a phone has no idiom for. It
  now rises from the bottom edge with a grab handle, where the thumb already is,
  and every overlay in the product behaves the same way.

### Fixed

- **Filter strips no longer drag the page sideways.** Swiping a horizontal strip
  that had reached its end pulled the whole page with it, so the layout slid
  diagonally and settled back. The strips stop at their ends now, and the page
  itself does not pan.

- **Buttons no longer flicker when tapped.** The colour transition on 117
  controls also animated three gradient properties, which some mobile browsers
  repaint in full rather than interpolate.

- **The mobile drawer draws one line, not two.** Its header rule sat a few
  pixels below the top bar's, and a vertical edge crossed both.

- **Expandable sections look expandable.** Five of them had four different
  affordances, and two — the egress policy detail and a trace's structured
  attributes — had none at all and read as plain text. All five now carry the
  same chevron.

- **Smaller things that were visibly inconsistent.** Two fields in the same form
  had different fills; button labels and page headings were split between
  sentence case and Title Case; one cron form mixed two label styles; the
  invocations search box was squeezed to three characters by the filter chips
  beside it on a phone; the DNS search-domain field clipped its own placeholder;
  and the docs Copy button was a lone unlabelled square on a phone.

### Verified

- **A new `consistency` browser suite makes this stick.** Every other check in
  the suite measures a control against a standard, which is how the drift above
  survived 2520 passing assertions: `Function` at 26.6px passed, `STATUS` at
  26.6px passed, and that one was 10px uppercase beside the other at 11.4px
  sentence-case was invisible to both. The new suite asserts three relational
  facts instead — controls sharing a row agree on height, label size, case and
  radius; a label that appears in two views is drawn the same way in both; and
  the radius and icon-size populations stay inside a declared set. It found real
  drift on its first run, and each of its three checks has been shown to fail on
  a defect before being trusted to pass.

- **2668 checks, zero failures**, across 19 routes x 7 viewports x both themes,
  run against the embedded binary rather than the dev server — Vite serves
  source and the binary serves a bundle with different CSS ordering. `go vet`
  clean, 25 Go packages green, 53 frontend unit tests green.

- **Looked at, not only measured.** Every fix above the test suites could not
  see — a squeezed search box, two fields with different fills, a disclosure
  with no affordance — was found by reading screenshots of the running product.

## v2026.08.27

The dashboard's second theme, and a mobile pass that gives its controls their
proportions back. One change for contributors: the built dashboard is no longer
committed to the repository.

### Added

- **The dashboard has a day theme, and the theme is now your choice.** It
  follows your operating system by default and remembers an explicit choice per
  browser; the control is in Settings, under Appearance. Both palettes are warm
  neutrals at the same hue, so switching changes the lightness of the interface
  and nothing else about its temperature. The code editor stays dark in both,
  the way a terminal does, and sits on a mat in day so it reads as an instrument
  rather than a hole in the page.

  Nothing to do on upgrade. An instance that has never had a theme chosen
  follows the operating system, which for most operators means it looks exactly
  as it did.

### Changed

- **The built dashboard is no longer committed.** This affects contributors,
  not operators: nothing about installing or running Orva changes, and every
  published binary is built exactly as before. If you build from source,
  `make build` now builds the dashboard for you when it is missing, so you need
  **Node 24** as well as Go. A fresh clone to a working binary takes about 15
  seconds. The two supported ways to get Orva are unchanged: take a release
  binary, or build both halves yourself.

  `backend/internal/server/ui_dist/` was tracked, and it was dead weight: the
  release workflow, the Dockerfile and CI each rebuilt the UI and overwrote it,
  so the committed copy reached no shipped binary. All it did was let a local
  `make build` serve a stale dashboard that looked correct.

### Fixed

- **Placeholder text is legible again.** In nine views the placeholder was the
  muted text token faded further with alpha, which put it at 3.09:1 against the
  field at its worst, under the 4.5:1 floor. It is the token's own value now:
  8.49:1 at night, 5.79:1 by day. This one was real before the day theme
  existed, on every Orva ever shipped.

  Not fixed, and not new: form-control borders are 1.70:1 against the night
  canvas and 1.89:1 against the day one, both under WCAG 1.4.11's 3:1 floor for
  a component boundary. Raising that token redraws the grid of the whole
  interface, so it is a design decision to take deliberately rather than a
  contrast patch to slip into a release.

- **Controls have one size, everywhere.** On a phone the 44px touch floor was
  being met by inflating each control's box, so every tier collapsed into the
  same slab: measured on a 360px screen, 439 of 602 visible controls were
  exactly 44px tall, and a filter chip was the same size as a primary button.
  On a desktop the opposite had happened -- the interface had drifted to more
  than twenty different button heights, six different squares for the same
  icon-only action, and around forty controls with no height at all that drew
  themselves as small as their text.

  Everything now sits on one ladder, with a rung for each kind of control and a
  matching set for touch. Labels that sat pinned to the top of an oversized box
  are centred, the checkboxes on API Keys are square rather than 13px wide by
  44px tall, and the trace pages no longer read a size larger than the rest of
  the dashboard.

- **Four screens that read as broken on a phone.** The conversations sheet in
  Chat dims the page behind it, instead of leaving it fully lit and looking
  interactive while it was not. The chat composer has a visible surface and its
  send button no longer sits faded at rest. The starter prompts are pills you
  can see, not plain text. The Jobs filter strip gets the full width, so the
  next filter is no longer sliced in half by the button beside it. Six page
  headers that could collide with their own action button now wrap.

- **The function test panel is usable on a phone.** It is drawn as a dense
  desktop instrument, and on a touch device only its inputs were being enlarged
  -- leaving a full-size method selector beside labels less than half its size,
  in rows that no longer lined up. The panel now scales as a whole on touch, and
  it gets enough height that you can see the response without scrolling past the
  request you just sent. The desktop layout is unchanged.

### Verified

- **Measured in a real browser, on the embedded binary rather than the dev
  server** (Vite serves source; the binary serves a Rollup bundle with
  different CSS ordering, and cascade order is what a second `:root` block
  depends on). 2440 checks across 19 routes x 7 viewports x both themes on a
  populated instance, 1290 more against a near-empty one so empty states are
  covered, plus every placeholder, focus indicator and touch target measured
  separately. Zero failures.
- **The control ladder is measured, not asserted.** A new browser suite renders
  every control at both pointer types and names any that drifts off a rung,
  because a control's real height is padding plus a line box plus whatever a
  scoped rule did, and none of that is visible from the source.
- **The regressions the day theme would have introduced were caught before
  release**, so they are absent from Fixed above: no operator ever saw them.
  The browser suite could not see two of them either, until its contrast probe
  was taught to read the `oklab()` that Tailwind v4 compiles every alpha into,
  and its touch-target probe was taught to measure the target rather than the
  ink.

## v2026.08.26

One security fix, and a documentation correction large enough to matter on its
own. Nothing here asks anything of an operator: no migration, no configuration
change, no re-issued credentials.

### Fixed

- **Redeploying a function now invalidates a leaked SDK credential.** The
  `ORVA_INTERNAL_TOKEN` your function's code holds was derived from the
  function's ID alone, so it was identical across deploys: if a compromised
  dependency copied it, removing that dependency and redeploying re-issued the
  same token, and only restarting Orva cleared it. The documented remediation
  therefore looked like it worked and did not.

  Each credential now also names the worker process it was issued to, and dies
  when that process does. Redeploying retires a function's warm workers, so it
  is a real remediation. Credentials still expire on restart, and on ordinary
  worker recycling (idle timeout, use limit).

  Nothing to do on upgrade: credentials are minted at spawn and every worker is
  replaced when you restart into the new version.

- **The documentation now matches the code.** An audit compared every document
  against the source and found 181 defects, 81 of them factually wrong. The
  ones you were most likely to hit:

  - The handler contract described `event.body` as parsed JSON. It has always
    been a raw string, so both headline examples in the reference were broken —
    the Python one returned HTTP 500, the Node one silently answered the wrong
    thing. Corrected, and the examples were executed rather than read.
  - The in-product AI assistant was prompted with an API that partly does not
    exist (`event.path_params`, an `x-orva-base64` response flag,
    `jobs.enqueue(delay_seconds=…)`, `auth_mode: "public"`), so it generated
    handlers that could not run.
  - `RUNTIMES.md` said dependency installs run on the host. They run inside
    nsjail.
  - The recovery procedure for a lost admin key deleted your accounts and
    recovered nothing.
  - The backup procedure omitted `.master.key`, without which restored secrets
    are undecryptable ciphertext.
  - `docker compose up -d` could not work from the downloaded compose file, and
    the README never mentioned Compose publishes on port 3000.

  Documentation is now part of a change rather than a follow-up: `CONTRACT.md`
  §6a requires docs to move in the same commit as the code, with a table of
  what to update for what.

### Verified

Beyond CI's gate (`test/e2e/run.py` with `ORVA_REQUIRE_SANDBOX=1` on amd64 and
arm64, the server and CLI installers, native systemd, docker smoke, CodeQL):

- **Every regression test was run against the unfixed code and shown to fail
  first.** For the credential fix that meant three separate proofs: removing
  the reaper hook leaves a credential valid five seconds after its worker died,
  removing the release from the spawn error path leaves one valid for a worker
  that never started, and removing the nonce check fails four `sdkauth` cases.
- **The reaper path was exercised locally** against a real spawned-and-killed
  worker, rather than relying on the sandbox E2E alone.
- **`go test -race`** on `sdkauth`, `pool`, `sandbox` and `server`.

Not run: `test/atscale.sh` and the migration rehearsal. Neither is implicated —
no pool sizing, scheduler or migration behaviour changed — but CONTRACT §6 lists
them as beyond CI, so their absence is stated rather than implied.

## v2026.08.25

Three things in this release need an operator's attention rather than just a
read: `crons.upsert` is now handler-scoped (**Breaking**, below), an API key you
revoked through the AI assistant may still be live until you restart, and if you
copied `ORVA_SECURE_COOKIES=true` out of the old `docker-compose.yml` onto a
plain-HTTP instance, remove it.

### Breaking

- **`crons.upsert()` must be called from inside your handler.** It now requires
  a live execution of the function it is scheduling, and returns
  `403 SDK_SCOPE_VIOLATION` otherwise. Calling it at module scope — outside the
  handler function, where it ran once per cold start — no longer works, and
  neither does calling it after your handler has returned without awaiting it.
  The documented form, `await orva.crons.upsert(...)` inside the handler, is
  unaffected.

  **Why:** a schedule is the one thing an SDK credential can create that
  outlives the credential. Every other use of a leaked `ORVA_INTERNAL_TOKEN`
  stops working when Orva restarts, because the signing key is random per
  process; a cron row does not, and cron-fired invocations do not consult the
  function's `auth_mode`. Requiring a live execution means a copy of the token
  taken off the box cannot plant one.

  **Upgrade:** if a deploy starts logging `SDK_SCOPE_VIOLATION`, move the
  `crons.upsert` call inside your handler and `await` it. Registration is
  idempotent by `(function, name)`, so calling it on every invocation is fine —
  and is what the documentation already shows.

### Upgrade notes

- **Restart Orva after upgrading if you have ever revoked an API key through
  the AI assistant or an MCP client.** Those revocations deleted the key but
  left it in the authentication cache, so it may still be working on the REST
  API right now. A restart clears the cache; the fix below stops it recurring.
  Keys revoked from the dashboard or `orva keys revoke` were never affected.

- **If you copied `ORVA_SECURE_COOKIES=true` from the old `docker-compose.yml`
  into your own compose file, and you reach Orva over plain HTTP, remove it.**
  It is why signing in from a LAN address bounces you back to the login screen.
  The shipped compose file no longer sets it.

- Nothing else to do. No migration, no configuration change, no re-issued
  credentials.

### Added

- **Schedules a function declared for itself are marked in the dashboard.**
  The Schedules page now shows an **SDK** badge on any schedule created through
  `crons.upsert()`, with the declared name in its tooltip. The dashboard has no
  name field, so a named schedule is one the function's own code registered —
  which is what makes an entry you did not expect visible as one. The `name` is
  also now returned by the cron API, where it had been stored but never read
  back.

- **A function may declare at most 25 schedules for itself.** Upsert is keyed
  by name, so distinct names multiplied without bound and nothing rejected
  `* * * * *`. Schedules you create in the dashboard are not capped.

- **You can remove a connected application, not just revoke its grant.**
  Settings → Connected applications gains a **Remove** control beside Revoke.
  Revoke ends the current connection; the application can reconnect on its own,
  because Orva accepts dynamic client registration. Remove retires the
  registration itself — its grants stop working at once (including on `/mcp`,
  where they previously kept working until the access token expired), its
  pending authorization codes are discarded, and it cannot connect again
  without you approving it on the consent screen.

  The plumbing for this was already in place and unreachable: the column, the
  read path and three checks in the authorization flow all existed, and nothing
  ever set the flag.

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

- **Requests to the internal SDK endpoints are now authenticated by the gate.**
  Everything under `/api/v1/_kv/` and `/api/v1/_internal/` authenticates with
  the process-signed worker credential rather than an API key, and the
  middleware discarded the verification error and called the handler anyway.
  Every route behind those prefixes checked the credential again for itself, so
  nothing was reachable without one — but that made it a convention each
  handler had to remember rather than a property of the gate, and the next
  route added would have been open. Every existing caller is unaffected: the
  status, code and message of the rejection are unchanged, and the SDKs always
  send the credential. (The rejection is now emitted by the gate rather than a
  handler, so it no longer echoes an `X-Request-ID` you supplied.)

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

### Verified

Beyond CI's gate (`test/e2e/run.py` with `ORVA_REQUIRE_SANDBOX=1` on amd64 and
arm64, the server and CLI installers, native systemd, docker smoke, CodeQL):

- **Every regression test in this release was run against the unfixed code and
  shown to fail first.** That is not a formality here — five tests written for
  these fixes passed against the code they were meant to pin, and were rebuilt
  until they failed: a traversal at the wrong depth, an escape planted one
  directory off, a cache TTL that still passed with the timeout set to eleven
  years, a route table every handler already rejected, and two session
  revocation paths that could be neutered with the suite staying green. Two
  tests that legitimately pass both ways are labelled in-file as controls.
- **`go test -race`** on `server`, `handlers`, `database` and `mcp` after each
  change, locally.
- **The migration surface is untouched.** Nothing in this release adds,
  alters or reads a migration; the OAuth work reuses columns that already
  exist on every installed instance.

Not run for this release: `test/atscale.sh` and the migration rehearsal. Neither
is implicated — no pool, scheduler-capacity or migration behaviour changed — but
CONTRACT §6 lists them as beyond CI, so their absence is stated rather than
implied.

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

Superseded; its release and tag have been pruned. The summary below is the record.