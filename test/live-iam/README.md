# Live scratch IAM: KV milestone only

This isolated fixture connects the unchanged guided Permissions view through the
real request controller and HTTP adapter to `workloadpolicy.Service`, actual
SQLite policy/audit transactions, the shared Authority, and Factory-created
Node/Python nsjail workers using `sdkhost.WithIAM`.

It does **not** install production routes, boot schemas, MCP tools, normal
browser-session identity, pool/provider integration or complete SDK enforcement.
No deployment/editor state is involved. The shell visibly says
“Live scratch IAM · KV workflow · not production-ready”.

## Disposable guest only

Use a fresh 2-CPU/4-GiB/20-GiB MSB with bounded lifetime. Public setup is confined
to that guest. Publish only the approved private development mapping
`10.1.3.1:4300:4300` after checking `devurl`; never touch production or another
worker's VM. Copy the exact checkout in, rather than mounting host credentials.

Install Go 1.27, Node 26, zstd, Python 3 (for existing unit-test helpers) and a C
compiler in the guest. Download and
authenticate the same public runtime packages used by the accepted qualification
recipe (see `backend/internal/workloadspawn/RUNTIME_QUALIFICATION.md`). Resolve Go
modules and run `npm ci` in `frontend` during setup only. Install the official
`@playwright/cli` and its Chromium dependencies only in the guest; use
`playwright-cli install-browser chromium` so its bundled engine version matches.

Run from the guest source root:

```sh
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOMAXPROCS=2
go test -count=1 ./backend/internal/workloadpolicy ./backend/internal/workloadspawn
go test -race -count=1 ./backend/internal/workloadpolicy ./backend/internal/workloadspawn
go vet ./backend/internal/workloadpolicy ./backend/internal/workloadspawn
go test -c -o /tmp/live.test ./backend/internal/workloadspawn
mkdir -m 700 /evidence
cd backend/internal/workloadspawn
ORVA_LIVE_WORKFLOW=disposable-vm ORVA_LIVE_HOLD=1 \
ORVA_RUNTIME_PACKAGE_DIR=/packages ORVA_RUNTIME_RECIPE_OUTPUT=/evidence \
ORVA_RUNTIME_NODE_SHA256=34c7b8f1559a55018827fef7f96a540afb8cfeba665ee89dbbe3170852284ed3 \
ORVA_RUNTIME_PYTHON_SHA256=ff692cfc7da5bc142b027b34194da7653db79fe1277945c2363227eb04c56ed5 \
ORVA_RUNTIME_NSJAIL_SHA256=288b230569afb7d54966010cdf43dc2d4c23869d74aac72563d02dae482ae1ea \
/tmp/live.test -test.run=^TestLiveWorkflow$ -test.v -test.timeout=35m
```

The fixture initializes staged IAM tables explicitly from canonical schema
source **in the fixture only**. It generates genuine scratch API keys, stores
only hashes in SQLite, and writes the private browser credentials to the guest's
`/evidence/credentials.json` mode 0600. Never print, copy out or screenshot that
file. It binds the backend only to guest loopback 8443. Key authentication
checks live database expiry/permissions; it is not production login.

Before signalling ready, both runtimes must pass actual HTTP deny/allow/revoke,
old-CAS 409, read-only/write-only 403, channel/missing authentication rejection
and strict-wire 400 checks. Readiness is `/evidence/ready`, not process existence.
The observed current artifact comes from the same immutable Factory supplying
the worker, not a fresh function lookup. Worker transitions use the Authority
hook configured before Spawn/import. No synthetic SDK handler supplies results.
The fixture uses stock sandbox cgroup discovery. Without guest delegation it
reports `rlimit_only`; a working KV journey is not proof of hard resource limits
or Domain/fencing qualification.

In another guest command:

```sh
cd /src/frontend
npm test
npm run lint
npm run build
npx vite build --config vite.live-iam.config.js
npx vite --config vite.live-iam.config.js
```

Create a private Playwright CLI configuration in the guest with the scratch admin
key as `browser.contextOptions.extraHTTPHeaders["X-Orva-API-Key"]`. Read the key
from the 0600 file; never interpolate it into a command line or log configuration.
Use headless Chromium and the disposable-guest `--no-sandbox` launch option.
Then use a named CLI session:

```sh
playwright-cli -s=live-iam open --config=/evidence/playwright-private.json
playwright-cli -s=live-iam snapshot
playwright-cli -s=live-iam run-code --filename=/src/test/live-iam/browser.js
playwright-cli -s=live-iam close
touch /evidence/stop
```

The script walks the actual Create wizard, saves exact execution `kv.get` for
own key `demo`, invokes, removes it, saves and invokes the **same worker** again,
for Node and Python. It records real response snapshots and worker/execution/
artifact identity, plus 1440px day and 390px night captures. CLI response evidence
and `events.json` contain no request headers or bearers. Do not export browser
storage, traces, profiles, private configs, credentials or the SQLite database.

Export only reviewed logs, response evidence, recipe manifests and screenshots
to `/tmp/opencode/iam-live-workflow/`. Stop the owned fixture/Vite process and
delete the owned MSB after active QA. Keep the detached source worktree for
independent review. Full production cut-over and unrelated lifecycle matrices
are separate acceptance gates, not implied by this milestone.
