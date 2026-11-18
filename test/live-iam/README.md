# Live scratch IAM: KV, two-sided buffered calls and streams

This isolated fixture connects the unchanged guided Permissions view through the
real request controller and HTTP adapter to `workloadpolicy.Service`, actual
SQLite policy/audit transactions and the shared Authority. Invocations use the
real public `InvokeHandler` → `Proxy` → `pool.Manager` → concrete Factory →
Node/Python nsjail private SDK path, including genuine platform-key authentication
and the normal asynchronous SQLite execution writer.

`ORVA_LIVE_CALLS=1` additionally attaches the existing `InternalInvokeHandler`
through `Host.SetInvoker`, with the same Authority and managed acquired-target
observer. No new public F2F route is registered. The sealed fixture handlers keep
KV as the default and add buffered outgoing-call and callee-echo request modes.
The scratch operation selector uses the existing common controls and server
target metadata; the approved Permissions page and guided wizards are unchanged.

The pool and buffered-call workflows have scoped independent acceptance, not
production activation. This streams increment is based on primary
`9752c79` plus the exact pool patch
`95ceaccd76d37a86305b3432168234bddf049bf32944ef8034e4a0b92c0a69f4` and the exact
calls patch `bfac5febf7130751c53db91a8cd3c42e61ec77e634a0d5c5cd7cef102bfa4714`.
The existing policy action is `functions.invoke`, which the underlying operation
matrix applies to buffered calls and to stream open and next. There is no
`functions.invoke.stream` action and none was added.

`ORVA_LIVE_STREAMS=1` (implies calls) adds real streaming. The sealed handlers
gain a `stream-source` callee, which yields timed chunks through the runtime's own
streaming protocol, and a `stream-call` caller, which reads them with the public
SDK stream API and forwards each chunk on its public response. The caller reduces
errors to the generic `IAM_DENIED` or `CALL_ERROR`, so hidden target names and
messages never reach the response. The fixture's stream route is
`/fn/<caller>/stream`; the writer flushes every chunk unchanged and only records
bounded line timestamps. Fixture functions use a 30 s timeout so a 12 s stream can
be revoked through the UI; this is test configuration, not a production gate.

What the streams checks establish, in both Node to Python and Python to Node:
outbound permission alone and inbound acceptance alone each deny with no callee
output and no callee execution row; both together deliver genuine chunks with
host-derived callee context; revoking the caller permission, and separately the
callee acceptance, cuts a running stream, cancels the callee execution and kills
its worker; Stop from the keyboard also cancels the callee. Bytes already handed
off before a revocation was acknowledged are admitted data and may still reach the
client (the checks allow at most two); what must never happen is a fresh frame
handoff that begins after the acknowledgement. The existing target import may run
before handler admission, so a denial does not prevent module import in the callee.
Oversized stream heads, chunks and errors are covered by the existing sdkhost unit
tests only; the real-runtime run does not exercise them.

It does **not** install production routes, boot schemas, MCP tools, normal
browser-session identity, production pool activation or complete SDK enforcement.
Jobs, cron and spans stay denied. No deployment/editor state is involved. The
shell visibly says “Live scratch IAM · KV, buffered calls and streams ·
not production-ready”.

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
Verify the exact Go archive against independent official `go.dev/dl` publisher
metadata before accepting any build. A cache filename or installed version string
is insufficient; replace a mismatch in the guest and rebuild with a fresh cache.

Run from the guest source root:

```sh
export GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOMAXPROCS=2
go test -count=1 ./backend/internal/{pool,proxy,sdkhost,workloadiam,workloadpolicy,workloadspawn}
go test -race -count=1 ./backend/internal/{pool,proxy,sdkhost,workloadiam,workloadpolicy,workloadspawn}
go vet ./backend/internal/{pool,proxy,sdkhost,workloadiam,workloadpolicy,workloadspawn}
go test -c -o /tmp/live.test ./backend/internal/workloadspawn
mkdir -m 700 /evidence
cd backend/internal/workloadspawn
ORVA_LIVE_WORKFLOW=disposable-vm ORVA_LIVE_STREAMS=1 ORVA_LIVE_HOLD=1 \
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
Calls mode additionally checks both real cross-runtime directions: outbound-only
and inbound-only deny, both sides allow, removing either side denies, and unknown
targets return the generic SDK denial. The callee's depth/trace/execution context
comes from the host dispatch, not the deliberately conflicting payload fields.
The observed current artifact comes from the managed pool's bounded atomic
generation map and the same immutable Factory supplying the worker, not a fresh
function lookup. Both policy Service and SDK current-artifact admission observe
that map under the same Authority gate used for installation. Worker transitions use the Authority
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
playwright-cli -s=live-iam run-code --filename=/src/test/live-iam/calls-browser.js
playwright-cli -s=live-iam run-code --filename=/src/test/live-iam/streams-browser.js
playwright-cli -s=live-iam close
touch /evidence/stop
```

The script walks the actual Create wizard, saves exact execution `kv.get` for
own key `demo`, invokes, removes it, saves and invokes the **same worker** again,
for Node and Python. It records real response snapshots and worker/execution/
artifact identity, plus 1440px day and 390px night captures. `X-Orva-Execution-ID`
comes from the real handler. Scratch-only worker/artifact headers are derived
from that execution's observed private SDK identity; the wrapper records, but
does not synthesize, the actual handler response and its committed SQLite row.
The script also checks method/path/body, frozen config/secrets booleans and
credential stripping. CLI response evidence
and `events.json` contain no request headers or bearers. Do not export browser
storage, traces, profiles, private configs, credentials or the SQLite database.

Save the script's returned JSON as `/evidence/browser-result.json`, then run
`python3 /src/test/live-iam/verify-pool-evidence.py /evidence` to match every browser
execution header to its SQLite row, SDK execution-phase lease observation and
saved/revoked policy audit. Only lease presence, never its value, is recorded.
Save the second script's returned JSON as `/evidence/calls-browser-result.json`
and run `verify-calls-evidence.py /evidence`. It checks real two-sided guided
policy saves and audit revisions against parent execution headers, private SDK
lease observations, acquired target identity and actual F2F child SQLite trace
rows. Denied calls must have no callee execution. Both Node→Python and
Python→Node complete the UI workflow with 390px night and 1440px day captures.

Save the third script's returned JSON as `/evidence/streams-browser-result.json`
and run `python3 -I verify-streams-evidence.py /evidence`. For each direction it
checks one-sided denials, the five-chunk completion, the keyboard Stop, and both
in-flight revocations against the server's own stream record, SDK open admission
against the acquired callee worker, the policy acknowledgement time, the F2F child
SQLite rows (cancelled, not succeeded) and the audit revisions. It also asserts the
callee worker was replaced after cancellation. The UI stays honest: delivered
chunks remain listed after a denial, nothing after a terminal marker or Stop is
applied, and changing the shown function neither cancels nor relabels a running
stream; only Stop does.

The HTTP adapter's optional metadata source runs only after authenticated operator
read access, before mutation, outside admission. It returns at most 256 visible
active/current function IDs and names; invalid/duplicate/oversized data fail
closed. These labels are advisory picker data, not capabilities or approval.

With the same checked runtime environment and a fresh evidence directory, run
`/tmp/live.test -test.run=^TestLiveManagedPoolReplacement$ -test.v -test.timeout=2m`.
It delays a real Node import, replaces its pool, rejects the late old spawn,
then retains the second policy while a held old worker is denied after the next
replacement. It checks generation-bound double Release, current acquired-target
observations and frozen config/secrets/resources. Separate code/runtime stores
preserve the existing qualification limits. See `backend/internal/pool/README.md`
for ownership, lock ordering and production-writer limitations.

Export only reviewed logs, response evidence, recipe manifests and screenshots
to `/tmp/opencode/iam-live-streams/`. Stop the owned fixture/Vite process and
delete the owned MSB after active QA. Keep the detached source worktree for
independent review. Full production cut-over and unrelated lifecycle matrices
are separate acceptance gates, not implied by this milestone.
