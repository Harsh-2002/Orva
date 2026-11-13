# Author-side prebundling (opt-in)

You can upload an author-built JavaScript bundle to the existing `node` runtime.
This is an author workflow, not a platform bundling feature: Orva does not run
esbuild, select a bundler, add a cache, or change its handler contract. Platform
integration is deferred. Keep ordinary source deployment when it suits your app.

## Build in the author workspace, upload only the result

Use Node 26 and pin `esbuild` to **0.25.12** in your author-only `package.json`
and lockfile. The runnable example is
[`test/fixtures/node-prebundle`](../test/fixtures/node-prebundle/): it owns a
small JS/TS static module graph and commits actual esbuild output plus SHA-256
provenance. The bundler belongs only in that workspace, never the upload.

From an author workspace containing `src/handler.js` and the pinned lockfile:

```sh
npm ci
mkdir -p upload/dist
node_modules/.bin/esbuild src/handler.js --bundle --packages=bundle \
  --platform=node --target=node26 --format=cjs \
  --external:orva '--external:orva/*' '--external:node:*' \
  --sourcemap=external --outfile=upload/dist/handler.js
orva deploy ./upload --name prebundled-js --runtime node \
  --entrypoint dist/handler.js --follow
# This example calls the SDK control plane for KV access.
orva functions update prebundled-js --network-mode egress
```

For TypeScript, replace `src/handler.js` with `src/handler.ts`; still upload
JavaScript and use `--entrypoint dist/handler.js`. esbuild strips types; it
**does not type-check**. Run an author-side `tsc --noEmit` separately if desired,
with your own declarations/configuration. This is distinct from Orva's existing
server-side TypeScript compilation path.

`--packages=bundle` makes the package inclusion policy explicit. Keep `orva`
and `orva/*` external so CommonJS `require('orva')` resolves the runtime's SDK;
keep Node builtins external too. These examples have no third-party runtime
dependencies. Externalizing a general package does not install it: declare such
dependencies in an **upload-side** `package.json` for Orva's normal installer.
That extended case is outside this fixture's coverage.

Start with a clean upload directory. Its entire contents here are:

```text
upload/
  dist/handler.js
  dist/handler.js.map
```

Do not copy the author package/lock, tools, `node_modules`, `tsconfig.json`, or
a `package.json` with `"type":"module"` into this CommonJS upload root.
The CLI retains `dist` and maps, skips `node_modules` and hidden directories,
but does **not** generally exclude a visible tools directory: cleanliness is
the author's responsibility. The SDK KV example requires `network_mode=egress`
and the instance's normal SDK control-plane access; prebundling changes neither.

## Source, diffs, rollback, and source maps

Keep original sources, lockfile and build provenance in your author repository.
Orva's `/source`, editor and deployment diff see the **uploaded bundle**, not
your original TypeScript. Do not edit a generated bundle in the dashboard and
expect the author tree to update. Rollback promotes an existing deployment;
it does not run esbuild or recreate original sources.

External maps are uploaded artifacts, not an automatic mapped-stack feature.
This recipe neither enables mapped stack traces nor teaches the editor/diff to
read maps. Maps can contain original source text (`sourcesContent`); review
their disclosure before uploading. The fixture intentionally retains maps to
prove packaging, not to promise debugging integration.

## Compatibility boundary

Only the fixture's static JS/TS graph, CommonJS export, Node builtin and external
Orva SDK KV round trip are regression targets. A successful build is not proof
that any arbitrary npm package works in a sandbox.

* Files read with `fs`, templates, WASM and other assets need explicit upload
  placement and runtime paths; bundling does not discover arbitrary reads.
* Computed `require(name)` can survive in output with its dependency unresolved.
  Audit it; the clean dependency-free upload may not satisfy it.
* A `.node` input fails this recipe's default loader. A build-only dummy-file
  probe proves that rejection, not compatibility with real native addons.
  Native addons need separate ABI, architecture, shared-library and sandbox
  validation. Do not invoke these unsupported probes as acceptance examples.
* Side effects, ESM-only packages, optional dependencies and package-specific
  file discovery need individual assessment; no universal bundling guarantee.

## Reproduce the committed fixture

In an isolated disposable copy, from `test/fixtures/node-prebundle`:

```sh
npm ci
node regenerate.mjs --check
# After intentional source/tool changes, regenerate then review the diff:
node regenerate.mjs
```

The bounded script invokes only pinned esbuild on its own fixed sources, with
no plugins or arbitrary configuration scripts. It parses but never imports or
executes a handler for build validation. Author-retained `probes/` sources and
an asset are built only in temporary staging directories. These probes show a
retained asset reference, a retained dynamic require, and `.node` loader
rejection; they are removed with the staging directory. `provenance.json`
records the tool, flags and input/output hashes. The E2E suite consumes the
committed upload artifacts without installing esbuild on the server.
