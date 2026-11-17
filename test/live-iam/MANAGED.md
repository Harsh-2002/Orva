# Named-policy vertical slice: explicit scratch fixture

Normal production routing, boot schema initialization, tool dependencies and SDK
defaults remain unchanged. This fixture uses the actual public invoke handler,
proxy, managed pool, sealed Factory, Node/Python nsjail workers, private SDK,
SQLite source/audit store and shared policy service. No SDK/jail or browser-route
responses are mocked. It is a named own-KV workflow, not full IAM cut-over.

UI correction acceptance checks the rendered human-readable effective and
inline-only outbound/caller cards, not just the Invoke result. Initial attached
access and final last-source removal are captured for both real workers; inbound,
quarantine/review, read-only and busy/conflict presentation have component
regressions. This corrects the parent's observed misleading inline-card capture;
the original workflow PASS was not complete UI acceptance.

Use one disposable 2-CPU/4-GiB/20-GiB guest without host mounts/credentials, private
egress or published ports. Copy the exact source and statically compiled
`go test -c ./backend/internal/workloadspawn` plus the real slim CLI into the
guest. Build with the installed Go version required by go.mod; dependencies and
browser packages must only be installed in the guest. The UI is the separate
`frontend/managed-iam.html` entry, served by `vite.managed-iam.config.js` on guest
port 4300. The normal router/sidebar is not changed.

Authenticate the publisher's rootfs and nsjail packages against its independently
fetched checksums.txt before use, following RUNTIME_QUALIFICATION.md. The unchanged
sealed-runtime producer additionally checks its pinned packaging recipe and SDK
source hashes. Start the existing TestLiveWorkflow with:

```sh
ORVA_LIVE_WORKFLOW=disposable-vm ORVA_MANAGED_POLICIES=1 ORVA_LIVE_HOLD=1 \
ORVA_RUNTIME_PACKAGE_DIR=/packages ORVA_RUNTIME_RECIPE_OUTPUT=/evidence \
ORVA_RUNTIME_NODE_SHA256=<publisher-verified-sha256> \
ORVA_RUNTIME_PYTHON_SHA256=<publisher-verified-sha256> \
ORVA_RUNTIME_NSJAIL_SHA256=<publisher-verified-sha256> \
/live.test -test.run=^TestLiveWorkflow$ -test.v -test.timeout=35m
```

Use a **fresh** evidence root. Wait for the fixture's actual `ready` marker; it
is written only after both real SDK workers deny initial requests. The private
credentials.json file is guest-only, mode 0600. Never export it, raw leases,
databases, profiles, traces or expanded browser configuration. Configure a named
headless Playwright CLI session using that file's admin header, without printing
or placing its value in argv. Browser context must target Chromium with the
guest-only no-sandbox option.

After npm tests/lint/build and the explicit managed-entry build:

```sh
playwright-cli -s=managed open --config=/evidence/playwright-private.json
playwright-cli -s=managed snapshot
ORVA_MANAGED_SCRATCH=disposable-vm node /src/test/live-iam/managed-workflow.mjs
playwright-cli -s=managed close
touch /evidence/stop
```

The driver creates v1 through the real guided UI, attaches one function through
the compiled slim CLI and the other through external MCP, and checks actual own
namespace values differ by runtime. It creates v2 through the UI and proves pins
do not follow, previews explicit rollout, then checks real denial after rollout.
It adds overlapping inline/two-managed sources, verifies a stale single owner
rejects the whole batch, detaches with residual access, terminally revokes both
managed sources, clears final inline access and verifies renewed denial. Every
decision is correlated with public source revision/epoch and audit metadata.

The browser scripts cover central policies and both function-source views,
390px night/1440px day, actual Invoke buttons, independent empty inline authoring
despite a nonempty effective policy, screenshots, page/console errors and root
horizontal overflow. No fixture response or callback is invented by the UI.
The separate TestIAMSurfacesRealMCPAIAndSlimCLI also exercises the real
in-process AI registry against SQLite/service publication, including strict raw
duplicate/unknown rejection on both tool paths; this does not claim a real LLM
turn or a browser Chat approval journey.

Only export the allowlisted managed-results JSON, screenshots and reviewed
test/build/checksum logs. Retain failures separately from corrected passes.
Close the named browser, signal fixture shutdown, stop owned UI jobs and remove
the guest before the step boundary. Verify absence. No production testing,
commit, push or deployment belongs to this fixture.

Open gates: independent QA, full accessibility/hit-region acceptance, managed
inbound authoring beyond retained inspection, async provenance/enforcement, boot
loading, coordinated migration, privileged mutations, function deletion,
restore/quarantine and full source-server cut-over. Rlimit-only execution is not
proof of hard cgroup resource enforcement.
