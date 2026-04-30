<template>
  <div class="space-y-12 pb-16">
    <!-- ── Page header ────────────────────────────────────────────── -->
    <header>
      <h1 class="text-xl font-semibold text-white tracking-tight">
        Docs
      </h1>
    </header>

    <!-- ── 1. Handler contract ────────────────────────────────────── -->
    <section
      id="handler"
      class="space-y-5 scroll-mt-6"
    >
      <div class="doc-section-head">
        <span class="doc-section-num">01</span>
        <div>
          <h2 class="doc-section-title">
            Handler contract
          </h2>
          <p class="doc-lede">
            One exported function receives the inbound HTTP event and returns an
            HTTP-shaped response. The adapter handles serialization and headers.
          </p>
        </div>
      </div>

      <TabbedCode
        :tabs="handlerTabs"
        storage-key="docs.handler"
      />

      <div class="grid grid-cols-1 md:grid-cols-3 gap-3">
        <div class="doc-card">
          <div class="doc-microlabel">
            Event shape
          </div>
          <div class="doc-card-body">
            <code class="doc-chip">method</code>
            <code class="doc-chip">path</code>
            <code class="doc-chip">headers</code>
            <code class="doc-chip">query</code>
            <code class="doc-chip">body</code>
          </div>
        </div>
        <div class="doc-card">
          <div class="doc-microlabel">
            Response
          </div>
          <div class="doc-card-body">
            <code class="doc-chip">{ statusCode, headers, body }</code>
            <p class="mt-1.5 text-foreground-muted">
              Non-string bodies are JSON-encoded by the adapter.
            </p>
          </div>
        </div>
        <div class="doc-card">
          <div class="doc-microlabel">
            Runtime env
          </div>
          <div class="doc-card-body">
            Env vars and secrets land in
            <code class="doc-chip">process.env</code>
            /
            <code class="doc-chip">os.environ</code>.
          </div>
        </div>
      </div>

      <div class="doc-table-wrap">
        <table class="doc-table">
          <thead>
            <tr>
              <th>Runtime</th>
              <th>ID</th>
              <th class="hidden sm:table-cell">
                Entrypoint
              </th>
              <th class="hidden md:table-cell">
                Dependencies
              </th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="rt in runtimes"
              :key="rt.id"
            >
              <td class="doc-cell-key">
                <component
                  :is="rt.icon"
                  class="shrink-0"
                />
                {{ rt.name }}
              </td>
              <td class="doc-cell-mono">
                {{ rt.id }}
              </td>
              <td class="doc-cell-mono hidden sm:table-cell">
                {{ rt.entry }}
              </td>
              <td class="doc-cell-mono hidden md:table-cell">
                {{ rt.deps }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <!-- ── 2. Deploy & invoke ─────────────────────────────────────── -->
    <section
      id="deploy"
      class="space-y-5 scroll-mt-6 border-t border-border pt-12"
    >
      <div class="doc-section-head">
        <span class="doc-section-num">02</span>
        <div>
          <h2 class="doc-section-title">
            Deploy &amp; invoke
          </h2>
          <p class="doc-lede">
            The dashboard handles day-to-day work; these calls are for CI and
            automation. Builds run async — poll
            <code class="doc-chip">/api/v1/deployments/&lt;id&gt;</code>
            or stream
            <code class="doc-chip">/api/v1/deployments/&lt;id&gt;/stream</code>
            until <code class="doc-chip">phase: done</code>.
          </p>
        </div>
      </div>

      <div class="grid grid-cols-1 lg:grid-cols-2 gap-3">
        <div class="space-y-2">
          <div class="doc-step-label">
            <span class="doc-step-num">1</span>
            Create the function row
          </div>
          <CodeBlock
            :code="curlCreate"
            lang="bash"
          />
        </div>
        <div class="space-y-2">
          <div class="doc-step-label">
            <span class="doc-step-num">2</span>
            Upload code
          </div>
          <CodeBlock
            :code="curlDeploy"
            lang="bash"
          />
        </div>
      </div>

      <div class="space-y-2">
        <div class="doc-microlabel">
          Invoke
        </div>
        <TabbedCode
          :tabs="invokeTabs"
          storage-key="docs.invoke"
        />
      </div>

      <Callout
        :icon="Globe"
        title="Custom routes"
      >
        Attach a friendly path with
        <code class="doc-chip">POST /api/v1/routes</code>.
        Reserved prefixes:
        <code class="doc-chip">/api/</code>
        <code class="doc-chip">/fn/</code>
        <code class="doc-chip">/mcp/</code>
        <code class="doc-chip">/web/</code>
        <code class="doc-chip">/_orva/</code>.
      </Callout>
    </section>

    <!-- ── 3. Configuration reference ─────────────────────────────── -->
    <section
      id="config"
      class="space-y-5 scroll-mt-6 border-t border-border pt-12"
    >
      <div class="doc-section-head">
        <span class="doc-section-num">03</span>
        <div>
          <h2 class="doc-section-title">
            Configuration reference
          </h2>
          <p class="doc-lede">
            Everything below lives on the function record. Secrets are stored
            encrypted and only decrypt into the worker environment at spawn
            time.
          </p>
        </div>
      </div>

      <div class="doc-table-wrap">
        <table class="doc-table">
          <thead>
            <tr>
              <th>Field</th>
              <th class="hidden sm:table-cell">
                Purpose
              </th>
              <th>Behaviour</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="row in configRows"
              :key="row.field"
              class="align-top"
            >
              <td class="doc-cell-key whitespace-nowrap">
                <component
                  :is="row.icon"
                  class="w-3.5 h-3.5 shrink-0"
                  :class="row.iconClass"
                />
                <code>{{ row.field }}</code>
              </td>
              <td class="doc-cell-mono hidden sm:table-cell whitespace-nowrap">
                {{ row.purpose }}
              </td>
              <td class="doc-cell-body">
                {{ row.body }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div class="space-y-2">
        <div class="doc-microlabel">
          Set a secret
        </div>
        <CodeBlock
          :code="curlSecret"
          lang="bash"
        />
      </div>

      <details class="doc-details group">
        <summary class="doc-details-summary">
          <ChevronRight class="w-3.5 h-3.5 transition-transform group-open:rotate-90 text-foreground-muted" />
          Signed-invoke recipe (HMAC, opt-in)
        </summary>
        <div class="doc-details-body">
          <CodeBlock
            :code="recipeSigned"
            lang="bash"
          />
        </div>
      </details>
    </section>

    <!-- ── 4. Schedules (cron triggers) ───────────────────────────── -->
    <section
      id="schedules"
      class="space-y-5 scroll-mt-6 border-t border-border pt-12"
    >
      <div class="doc-section-head">
        <span class="doc-section-num">04</span>
        <div>
          <h2 class="doc-section-title">
            Schedules
          </h2>
          <p class="doc-lede">
            Fire any function on a cron expression. The scheduler runs as
            part of the orvad process — no external service. Manage from
            the
            <router-link
              to="/cron"
              class="text-foreground hover:text-white underline decoration-dotted underline-offset-4"
            >Schedules page</router-link>
            or via the API. Standard 5-field cron with the usual shorthands
            (<code class="doc-chip">@daily</code>,
            <code class="doc-chip">@hourly</code>,
            <code class="doc-chip">*/5 * * * *</code>).
          </p>
        </div>
      </div>

      <TabbedCode
        :tabs="cronTabs"
        storage-key="docs.cron"
      />

      <Callout
        :icon="CalendarClock"
        title="Cron-fired headers"
      >
        Every cron-triggered invocation arrives at the function with
        <code class="doc-chip">x-orva-trigger: cron</code>
        and
        <code class="doc-chip">x-orva-cron-id: cron_…</code>
        on the event headers, so user code can branch on origin.
      </Callout>
    </section>

    <!-- ── 5. SDK (KV, invoke, jobs) ──────────────────────────────── -->
    <section
      id="sdk"
      class="space-y-5 scroll-mt-6 border-t border-border pt-12"
    >
      <div class="doc-section-head">
        <span class="doc-section-num">05</span>
        <div>
          <h2 class="doc-section-title">
            SDK from inside a function
          </h2>
          <p class="doc-lede">
            The bundled
            <code class="doc-chip">orva</code>
            module exposes three primitives every function can use without
            extra dependencies: a per-function key/value store, in-process
            calls to other Orva functions, and a fire-and-forget background
            job queue. Routes through the per-process internal token
            injected at worker spawn time.
          </p>
        </div>
      </div>

      <div class="grid grid-cols-1 md:grid-cols-3 gap-3">
        <div class="doc-card">
          <div class="doc-microlabel">
            <code class="doc-chip">orva.kv</code>
          </div>
          <div class="doc-card-body">
            <code class="doc-chip">put / get / delete / list</code>
            <p class="mt-1.5 text-foreground-muted">
              Per-function namespace on SQLite, optional TTL.
            </p>
          </div>
        </div>
        <div class="doc-card">
          <div class="doc-microlabel">
            <code class="doc-chip">orva.invoke</code>
          </div>
          <div class="doc-card-body">
            <code class="doc-chip">invoke(name, payload)</code>
            <p class="mt-1.5 text-foreground-muted">
              In-process call to another function. 8-deep call cap.
            </p>
          </div>
        </div>
        <div class="doc-card">
          <div class="doc-microlabel">
            <code class="doc-chip">orva.jobs</code>
          </div>
          <div class="doc-card-body">
            <code class="doc-chip">jobs.enqueue(name, payload)</code>
            <p class="mt-1.5 text-foreground-muted">
              Fire-and-forget; persisted; retried with exp backoff.
            </p>
          </div>
        </div>
      </div>

      <div class="space-y-2">
        <div class="doc-microlabel">
          KV — get/put with TTL
        </div>
        <TabbedCode
          :tabs="sdkKvTabs"
          storage-key="docs.sdk.kv"
        />
      </div>

      <div class="space-y-2">
        <div class="doc-microlabel">
          Function-to-function — invoke()
        </div>
        <TabbedCode
          :tabs="sdkInvokeTabs"
          storage-key="docs.sdk.invoke"
        />
      </div>

      <div class="space-y-2">
        <div class="doc-microlabel">
          Background jobs — jobs.enqueue()
        </div>
        <TabbedCode
          :tabs="sdkJobsTabs"
          storage-key="docs.sdk.jobs"
        />
      </div>

      <Callout
        :icon="Globe"
        title="Network mode"
      >
        The SDK reaches orvad over loopback through the host gateway, so
        the function needs
        <code class="doc-chip">network_mode: "egress"</code>.
        On the default
        <code class="doc-chip">"none"</code>
        the SDK throws
        <code class="doc-chip">OrvaUnavailableError</code>
        with a clear hint.
      </Callout>
    </section>

    <!-- ── 6. Webhooks ────────────────────────────────────────────── -->
    <section
      id="webhooks"
      class="space-y-5 scroll-mt-6 border-t border-border pt-12"
    >
      <div class="doc-section-head">
        <span class="doc-section-num">06</span>
        <div>
          <h2 class="doc-section-title">
            Webhooks
          </h2>
          <p class="doc-lede">
            Operator-managed subscriptions for system events. Configure
            URLs from the
            <router-link
              to="/webhooks"
              class="text-foreground hover:text-white underline decoration-dotted underline-offset-4"
            >Webhooks page</router-link>; Orva delivers signed POSTs to
            them when matching events fire (deployments, function
            lifecycle, cron failures, job outcomes). Subscriptions are
            global, not per-function.
          </p>
        </div>
      </div>

      <div class="grid grid-cols-1 md:grid-cols-3 gap-3">
        <div class="doc-card">
          <div class="doc-microlabel">
            Headers
          </div>
          <div class="doc-card-body">
            <code class="doc-chip">X-Orva-Event</code>
            <code class="doc-chip">X-Orva-Delivery-Id</code>
            <code class="doc-chip">X-Orva-Timestamp</code>
            <code class="doc-chip">X-Orva-Signature</code>
          </div>
        </div>
        <div class="doc-card">
          <div class="doc-microlabel">
            Signature
          </div>
          <div class="doc-card-body">
            <code class="doc-chip">sha256=hex(hmac(secret, ts.body))</code>
            <p class="mt-1.5 text-foreground-muted">
              Same shape as Stripe / signed-invoke. Receivers verify
              with the secret returned at create time.
            </p>
          </div>
        </div>
        <div class="doc-card">
          <div class="doc-microlabel">
            Retries
          </div>
          <div class="doc-card-body">
            <code class="doc-chip">5 attempts</code>
            <code class="doc-chip">exp backoff (≤ 1h)</code>
            <p class="mt-1.5 text-foreground-muted">
              Receiver must 2xx within 15s.
            </p>
          </div>
        </div>
      </div>

      <div class="doc-table-wrap">
        <table class="doc-table">
          <thead>
            <tr>
              <th>Event</th>
              <th>When it fires</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="e in webhookEvents"
              :key="e.name"
            >
              <td class="doc-cell-key whitespace-nowrap">
                <code>{{ e.name }}</code>
              </td>
              <td class="doc-cell-body">
                {{ e.when }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <div class="space-y-2">
        <div class="doc-microlabel">
          Verify a delivery
        </div>
        <TabbedCode
          :tabs="webhookVerifyTabs"
          storage-key="docs.webhooks.verify"
        />
      </div>
    </section>

    <!-- ── 7. MCP ─────────────────────────────────────────────────── -->
    <section
      id="mcp"
      class="space-y-5 scroll-mt-6 border-t border-border pt-12"
    >
      <div class="doc-section-head">
        <span class="doc-section-num">07</span>
        <div>
          <h2 class="doc-section-title">
            MCP — Model Context Protocol
          </h2>
          <p class="doc-lede">
            Same API surface the dashboard uses, exposed as 37 tools an agent
            can call directly. API key permissions scope the available tool
            set.
          </p>
        </div>
      </div>

      <div class="grid grid-cols-1 md:grid-cols-3 gap-3">
        <div class="doc-card">
          <div class="doc-microlabel">
            Endpoint
          </div>
          <div class="doc-card-body">
            <code class="doc-chip break-all">{{ origin }}/mcp</code>
          </div>
        </div>
        <div class="doc-card">
          <div class="doc-microlabel">
            Auth header
          </div>
          <div class="doc-card-body">
            <code class="doc-chip break-all">Authorization: Bearer &lt;token&gt;</code>
          </div>
        </div>
        <div class="doc-card">
          <div class="doc-microlabel">
            Transport
          </div>
          <div class="doc-card-body">
            <code class="doc-chip">Streamable HTTP</code>
            <code class="doc-chip">MCP 2025-11-25</code>
          </div>
        </div>
      </div>

      <!-- Token bar -->
      <div class="doc-token-bar">
        <div class="flex items-center gap-2 min-w-0 flex-1">
          <KeyRound class="w-4 h-4 shrink-0 text-foreground-muted" />
          <span
            v-if="!mcpToken"
            class="text-sm text-foreground-muted truncate"
          >
            Snippets show
            <code class="doc-chip">{{ tokenPlaceholder }}</code>.
            Mint a token to substitute it everywhere.
          </span>
          <span
            v-else
            class="text-sm text-success truncate"
          >
            Token minted:
            <code class="doc-chip">{{ mcpTokenPrefix }}…</code>
            — shown once, copy now.
          </span>
        </div>
        <button
          class="doc-token-btn"
          :disabled="mcpTokenBusy"
          @click="onMintMcpToken"
        >
          <KeyRound class="w-3.5 h-3.5" />
          {{ mcpToken ? 'Mint another' : (mcpTokenBusy ? 'Minting…' : 'Generate token') }}
        </button>
      </div>

      <TabbedCode
        :tabs="mcpInstallTabsPrimary"
        storage-key="docs.mcp.install"
      />

      <details class="doc-details group">
        <summary class="doc-details-summary">
          <ChevronRight class="w-3.5 h-3.5 transition-transform group-open:rotate-90 text-foreground-muted" />
          More clients (Cursor, VS Code, Codex CLI, OpenCode, Zed, Windsurf, ChatGPT, manual config)
        </summary>
        <div class="doc-details-body space-y-4">
          <TabbedCode
            :tabs="mcpInstallTabsSecondary"
            storage-key="docs.mcp.install.more"
          />
          <div class="doc-microlabel pt-1">
            Hand-edited config files
          </div>
          <TabbedCode
            :tabs="mcpConfigTabs"
            storage-key="docs.mcp.manual"
          />
        </div>
      </details>
    </section>

    <!-- ── 8. Error envelope ──────────────────────────────────────── -->
    <section
      id="errors"
      class="space-y-5 scroll-mt-6 border-t border-border pt-12"
    >
      <div class="doc-section-head">
        <span class="doc-section-num">08</span>
        <div>
          <h2 class="doc-section-title">
            Errors &amp; recovery
          </h2>
          <p class="doc-lede">
            Every error response uses the same envelope so log scrapers and
            retries can match on
            <code class="doc-chip">code</code>. Deploys are content-addressed;
            rollback retargets the active version pointer and refreshes warm
            workers.
          </p>
        </div>
      </div>

      <CodeBlock
        :code="errEnvelope"
        lang="json"
      />

      <div class="doc-table-wrap">
        <table class="doc-table">
          <thead>
            <tr>
              <th>Code</th>
              <th>When you see it</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="e in errorCodes"
              :key="e.code"
            >
              <td class="doc-cell-key whitespace-nowrap">
                <code>{{ e.code }}</code>
              </td>
              <td class="doc-cell-body">
                {{ e.when }}
              </td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>
  </div>
</template>

<script setup>
import { computed, h, ref, defineComponent } from 'vue'
import {
  Variable,
  KeyRound,
  Globe,
  Copy,
  Check,
  Lock,
  Gauge,
  ChevronRight,
  CalendarClock,
} from 'lucide-vue-next'
import { copyText } from '@/utils/clipboard'
import apiClient from '@/api/client'
import { useConfirmStore } from '@/stores/confirm'

const confirmStore = useConfirmStore()

// Syntax highlighting — highlight.js core + only the grammars we use.
// Importing the lite core (vs. the auto-bundle) keeps the Docs chunk small;
// each registerLanguage adds a few KB.
import hljs from 'highlight.js/lib/core'
import python from 'highlight.js/lib/languages/python'
import javascript from 'highlight.js/lib/languages/javascript'
import json from 'highlight.js/lib/languages/json'
import bash from 'highlight.js/lib/languages/bash'
import http from 'highlight.js/lib/languages/http'
import 'highlight.js/styles/github-dark.css'

hljs.registerLanguage('python', python)
hljs.registerLanguage('javascript', javascript)
hljs.registerLanguage('js', javascript)
hljs.registerLanguage('json', json)
hljs.registerLanguage('bash', bash)
hljs.registerLanguage('shell', bash)
hljs.registerLanguage('sh', bash)
hljs.registerLanguage('http', http)

const origin = computed(() => window.location.origin)

// ── Runtime icons (compact, table-sized) ────────────────────────────
const PythonGlyph = defineComponent({
  setup() {
    return () =>
      h('svg', { viewBox: '0 0 256 255', width: '14', height: '14', xmlns: 'http://www.w3.org/2000/svg' }, [
        h('defs', null, [
          h('linearGradient', { id: 'pyg1', x1: '0', y1: '0', x2: '1', y2: '1' }, [
            h('stop', { offset: '0', 'stop-color': '#387EB8' }),
            h('stop', { offset: '1', 'stop-color': '#366994' }),
          ]),
          h('linearGradient', { id: 'pyg2', x1: '0', y1: '0', x2: '1', y2: '1' }, [
            h('stop', { offset: '0', 'stop-color': '#FFE052' }),
            h('stop', { offset: '1', 'stop-color': '#FFC331' }),
          ]),
        ]),
        h('path', {
          fill: 'url(#pyg1)',
          d: 'M126.9 12c-58.3 0-54.7 25.3-54.7 25.3l.1 26.2H128v8H50.5S12 67.2 12 126.1c0 58.9 33.6 56.8 33.6 56.8h19.4v-27.4s-1-33.6 33.1-33.6h55.9s32 .5 32-30.9V43.5S191.7 12 126.9 12zM95.7 29.9a10 10 0 0 1 0 20 10 10 0 0 1 0-20z',
        }),
        h('path', {
          fill: 'url(#pyg2)',
          d: 'M129.1 243c58.3 0 54.7-25.3 54.7-25.3l-.1-26.2H128v-8h77.5s38.5 4.4 38.5-54.5c0-58.9-33.6-56.8-33.6-56.8h-19.4v27.4s1 33.6-33.1 33.6H102s-32-.5-32 30.9v52S64.3 243 129.1 243zm30.4-17.9a10 10 0 0 1 0-20 10 10 0 0 1 0 20z',
        }),
      ])
  },
})

const NodeGlyph = defineComponent({
  setup() {
    return () =>
      h('svg', { viewBox: '0 0 256 280', width: '14', height: '14', xmlns: 'http://www.w3.org/2000/svg' }, [
        h('path', {
          fill: '#3F873F',
          d: 'M128 0 12 67v146l116 67 116-67V67L128 0zm0 24.6 95 54.8v121.2l-95 54.8-95-54.8V79.4l95-54.8z',
        }),
        h('path', {
          fill: '#3F873F',
          d: 'M128 64c-3 0-5.7.7-8 2.3L73 92c-5 2.7-8 8-8 13.6V169c0 5.6 3 10.7 8 13.5l13 7.4c6.3 3.1 8.5 3.1 11.4 3.1 9.4 0 14.8-5.7 14.8-15.6V117c0-1-.7-1.7-1.7-1.7H103c-1 0-1.7.7-1.7 1.7v60.2c0 4.4-4.5 8.7-11.8 5.1l-13.7-7.9a1.6 1.6 0 0 1-.8-1.4v-63.4c0-.6.3-1 .8-1.4l46.8-26.9c.4-.3 1-.3 1.4 0L171 110c.5.4.8.8.8 1.4V174a1.7 1.7 0 0 1-.8 1.4l-46.8 27c-.4.2-1 .2-1.4 0l-12-7.2c-.4-.2-.8-.2-1.2 0-3.4 1.9-4 2.2-7.2 3.3-.8.3-2 .7.4 2.1l15.7 9.3c2.5 1.4 5.3 2.2 8.2 2.2 2.9 0 5.7-.8 8.2-2.2L181 184c5-2.8 8-7.9 8-13.5V107c0-5.6-3-10.7-8-13.5l-46.7-26.7a17 17 0 0 0-6.3-2.8z',
        }),
      ])
  },
})

// ── Section data ────────────────────────────────────────────────────
const handlerTabs = computed(() => [
  {
    label: 'Python',
    lang: 'python',
    code: `def handler(event):
    body = event.get("body") or {}
    return {
        "statusCode": 200,
        "headers": {"Content-Type": "application/json"},
        "body": {"hello": body.get("name", "world")},
    }`,
  },
  {
    label: 'Node.js',
    lang: 'js',
    code: `exports.handler = async (event) => {
  const body = event.body || {};
  return {
    statusCode: 200,
    headers: { 'Content-Type': 'application/json' },
    body: { hello: body.name || 'world' },
  };
};`,
  },
])

const invokeTabs = computed(() => [
  {
    label: 'curl',
    lang: 'bash',
    code: `curl -X POST ${origin.value}/fn/<function_id> \\
  -H 'Content-Type: application/json' \\
  -d '{"name": "Orva"}'`,
  },
  {
    label: 'fetch',
    lang: 'js',
    code: `const res = await fetch('${origin.value}/fn/<function_id>', {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ name: 'Orva' }),
});
console.log(await res.json());`,
  },
  {
    label: 'Python',
    lang: 'python',
    code: `import httpx

r = httpx.post(
    "${origin.value}/fn/<function_id>",
    json={"name": "Orva"},
)
print(r.json())`,
  },
])

const runtimes = [
  { id: 'python314', name: 'Python 3.14', entry: 'handler.py', deps: 'requirements.txt', icon: PythonGlyph },
  { id: 'python313', name: 'Python 3.13', entry: 'handler.py', deps: 'requirements.txt', icon: PythonGlyph },
  { id: 'node24',    name: 'Node.js 24',  entry: 'handler.js', deps: 'package.json',     icon: NodeGlyph },
  { id: 'node22',    name: 'Node.js 22',  entry: 'handler.js', deps: 'package.json',     icon: NodeGlyph },
]

const configRows = [
  { field: 'env_vars',           purpose: 'Plain config',    body: 'Plaintext config stored on the function record. Use for feature flags and non-secret settings.', icon: Variable, iconClass: 'text-violet-300' },
  { field: '/secrets',           purpose: 'Encrypted',       body: 'AES-256-GCM at rest. Values decrypt only into the worker environment at spawn time.',             icon: KeyRound, iconClass: 'text-emerald-300' },
  { field: 'network_mode',       purpose: 'Egress control',  body: 'none = isolated loopback. egress = outbound HTTPS allowed; firewall blocklist applies.',          icon: Globe,    iconClass: 'text-sky-300' },
  { field: 'auth_mode',          purpose: 'Invoke gate',     body: 'none = public. platform_key = require Orva API key. signed = require HMAC.',                       icon: Lock,     iconClass: 'text-violet-300' },
  { field: 'rate_limit_per_min', purpose: 'Per-IP throttle', body: 'Optional cap for public or webhook-facing functions. Exceeding it returns 429.',                  icon: Gauge,    iconClass: 'text-amber-300' },
]

const curlCreate = computed(() => `curl -X POST ${origin.value}/api/v1/functions \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"name":"hello","runtime":"python314","memory_mb":128,"cpus":0.5}'`)

const curlDeploy = computed(() => `tar czf code.tar.gz handler.py requirements.txt
curl -X POST ${origin.value}/api/v1/functions/<function_id>/deploy \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -F code=@code.tar.gz`)

const curlSecret = computed(() => `curl -X POST ${origin.value}/api/v1/functions/<function_id>/secrets \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"key":"DATABASE_URL","value":"postgres://..."}'`)

const recipeSigned = computed(() => `# generate signature
SECRET='your-shared-secret-stored-in-function-secrets'
TS=$(date +%s)
BODY='{"hello":"world"}'
SIG=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$SECRET" -hex | awk '{print $2}')

curl -X POST ${origin.value}/fn/<function_id> \\
  -H "X-Orva-Timestamp: $TS" \\
  -H "X-Orva-Signature: sha256=$SIG" \\
  -H 'Content-Type: application/json' \\
  -d "$BODY"`)

// ── Schedules / cron tabs ───────────────────────────────────────────
const cronTabs = computed(() => [
  {
    label: 'curl',
    lang: 'bash',
    note: 'Create a daily-9am schedule for an existing function. payload is delivered as the invoke body.',
    code: `curl -X POST ${origin.value}/api/v1/functions/<function_id>/cron \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{
    "cron_expr": "0 9 * * *",
    "enabled":   true,
    "payload":   {"task": "daily-summary"}
  }'`,
  },
  {
    label: 'Toggle / edit',
    lang: 'bash',
    note: 'PUT accepts any subset of {cron_expr, enabled, payload}; omitted fields keep their previous value. next_run_at is recomputed on expr changes.',
    code: `# pause
curl -X PUT ${origin.value}/api/v1/functions/<function_id>/cron/<cron_id> \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"enabled": false}'

# change schedule
curl -X PUT ${origin.value}/api/v1/functions/<function_id>/cron/<cron_id> \\
  -H 'X-Orva-API-Key: <YOUR_KEY>' \\
  -H 'Content-Type: application/json' \\
  -d '{"cron_expr": "*/15 * * * *"}'`,
  },
  {
    label: 'List & delete',
    lang: 'bash',
    note: 'GET /api/v1/cron lists every schedule across functions (with function_name JOIN); per-function uses the nested route.',
    code: `# all schedules
curl ${origin.value}/api/v1/cron \\
  -H 'X-Orva-API-Key: <YOUR_KEY>'

# delete one
curl -X DELETE ${origin.value}/api/v1/functions/<function_id>/cron/<cron_id> \\
  -H 'X-Orva-API-Key: <YOUR_KEY>'`,
  },
])

// ── SDK tabs (KV / invoke / jobs) ───────────────────────────────────
const sdkKvTabs = [
  {
    label: 'Python',
    lang: 'python',
    code: `from orva import kv

def handler(event):
    # Store with optional TTL (seconds). 0 = no expiry.
    kv.put("user:42", {"name": "Ada", "tier": "pro"}, ttl_seconds=3600)

    # Read; default returned if missing or expired.
    user = kv.get("user:42", default=None)

    # List by prefix.
    pages = kv.list(prefix="page:", limit=50)

    # Delete is idempotent.
    kv.delete("user:42")

    return {"statusCode": 200, "body": str(user)}`,
  },
  {
    label: 'Node.js',
    lang: 'js',
    code: `const { kv } = require('orva')

exports.handler = async (event) => {
  await kv.put('user:42', { name: 'Ada', tier: 'pro' }, { ttlSeconds: 3600 })

  const user = await kv.get('user:42', null)

  const pages = await kv.list({ prefix: 'page:', limit: 50 })

  await kv.delete('user:42')

  return { statusCode: 200, body: JSON.stringify(user) }
}`,
  },
]

const sdkInvokeTabs = [
  {
    label: 'Python',
    lang: 'python',
    code: `from orva import invoke, OrvaError

def handler(event):
    try:
        # invoke() returns the downstream {statusCode, headers, body}.
        # body is JSON-decoded when possible.
        result = invoke("resize-image", {"url": event["body"]["url"]})
        return {"statusCode": 200, "body": result["body"]}
    except OrvaError as e:
        # 404 = function not found, 507 = call depth exceeded.
        return {"statusCode": e.status or 502, "body": str(e)}`,
  },
  {
    label: 'Node.js',
    lang: 'js',
    code: `const { invoke, OrvaError } = require('orva')

exports.handler = async (event) => {
  try {
    const result = await invoke('resize-image', { url: event.body.url })
    return { statusCode: 200, body: result.body }
  } catch (e) {
    if (e instanceof OrvaError) {
      return { statusCode: e.status || 502, body: e.message }
    }
    throw e
  }
}`,
  },
]

const sdkJobsTabs = [
  {
    label: 'Python',
    lang: 'python',
    code: `from orva import jobs

def handler(event):
    # Fire-and-forget. Returns the job id immediately; the function
    # body runs later via the scheduler. max_attempts retries with
    # exponential backoff on 5xx / exception.
    job_id = jobs.enqueue(
        "send-welcome-email",
        {"to": event["body"]["email"]},
        max_attempts=3,
    )
    return {"statusCode": 202, "body": job_id}`,
  },
  {
    label: 'Node.js',
    lang: 'js',
    code: `const { jobs } = require('orva')

exports.handler = async (event) => {
  const jobId = await jobs.enqueue(
    'send-welcome-email',
    { to: event.body.email },
    { maxAttempts: 3 }
  )
  return { statusCode: 202, body: jobId }
}`,
  },
]

// ── Webhooks (system events) ────────────────────────────────────────

const webhookEvents = [
  { name: 'deployment.succeeded', when: 'A function build finished and the new version is active.' },
  { name: 'deployment.failed',    when: 'A build failed or was rejected.' },
  { name: 'function.created',     when: 'A new function row was created via POST /api/v1/functions.' },
  { name: 'function.updated',     when: 'A function config was edited via PUT /api/v1/functions/{id} (status flips during a deploy do NOT fire this — see deployment.*).' },
  { name: 'function.deleted',     when: 'A function was removed.' },
  { name: 'execution.error',      when: 'An invocation finished with status=error or 5xx.' },
  { name: 'cron.failed',          when: 'A scheduled run failed (bad expr, missing fn, dispatch error, or 5xx).' },
  { name: 'job.succeeded',        when: 'A queued background job finished successfully.' },
  { name: 'job.failed',           when: 'A queued job exhausted its retries (terminal failure).' },
]

const webhookVerifyTabs = [
  {
    label: 'Python',
    lang: 'python',
    note: 'Run on the receiver. Reject anything that fails verification — the signature ensures the request really came from this Orva instance.',
    code: `import hmac, hashlib, time

def verify(secret: str, ts: str, body: bytes, sig_header: str) -> bool:
    if abs(time.time() - int(ts)) > 300:   # 5-min skew window
        return False
    mac = hmac.new(secret.encode(), f"{ts}.".encode() + body, hashlib.sha256)
    expected = "sha256=" + mac.hexdigest()
    return hmac.compare_digest(expected, sig_header)

# In your Flask/FastAPI/etc. handler:
ts  = request.headers["X-Orva-Timestamp"]
sig = request.headers["X-Orva-Signature"]
if not verify(WEBHOOK_SECRET, ts, request.get_data(), sig):
    return "bad signature", 401`,
  },
  {
    label: 'Node.js',
    lang: 'js',
    note: 'Same shape as Stripe. Use timingSafeEqual to avoid sig-leak via timing.',
    code: `const crypto = require('crypto')

function verify(secret, ts, body, sigHeader) {
  if (Math.abs(Date.now() / 1000 - parseInt(ts, 10)) > 300) return false
  const mac = crypto.createHmac('sha256', secret)
  mac.update(ts + '.')
  mac.update(body)
  const expected = 'sha256=' + mac.digest('hex')
  if (expected.length !== sigHeader.length) return false
  return crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(sigHeader))
}

// In an express handler with raw body middleware:
app.post('/webhooks/orva', (req, res) => {
  const ok = verify(
    process.env.WEBHOOK_SECRET,
    req.headers['x-orva-timestamp'],
    req.body,                  // raw bytes — NOT parsed JSON
    req.headers['x-orva-signature']
  )
  if (!ok) return res.status(401).send('bad signature')
  res.sendStatus(200)
})`,
  },
]

const errEnvelope = `{
  "error": {
    "code": "VALIDATION",
    "message": "name must be lowercase and dash-separated",
    "request_id": "req_abc123"
  }
}`

const errorCodes = [
  { code: 'VALIDATION',        when: 'Bad request body or path parameter.' },
  { code: 'UNAUTHORIZED',      when: 'Missing or invalid API key / session cookie.' },
  { code: 'NOT_FOUND',         when: 'Function, deployment, or secret doesn\'t exist.' },
  { code: 'RATE_LIMITED',      when: 'Too many requests — check the Retry-After header.' },
  { code: 'VERSION_GCD',       when: 'Rollback target was garbage-collected.' },
  { code: 'INSUFFICIENT_DISK', when: 'Host is below min_free_disk_mb.' },
]

// ── MCP install state ───────────────────────────────────────────────
const tokenPlaceholder = '<YOUR_ORVA_TOKEN>'
const mcpToken = ref('')
const mcpTokenBusy = ref(false)
const mcpTokenPrefix = computed(() => mcpToken.value.slice(0, 12))
const T = computed(() => mcpToken.value || tokenPlaceholder)

const onMintMcpToken = async () => {
  if (mcpTokenBusy.value) return
  mcpTokenBusy.value = true
  try {
    const stamp = new Date().toISOString().slice(0, 16).replace('T', ' ')
    const res = await apiClient.post('/keys', {
      name: 'MCP — ' + stamp,
      permissions: ['invoke', 'read', 'write', 'admin'],
    })
    mcpToken.value = res.data.key
  } catch (err) {
    console.error('mint mcp key failed', err)
    confirmStore.notify({
      title: 'Could not mint key',
      message: err?.response?.data?.error?.message || err.message || 'Unknown error',
      danger: true,
    })
  } finally {
    mcpTokenBusy.value = false
  }
}

// Two tabs front-and-center; the rest hidden under "More clients" so the
// page doesn't read like a setup wizard.
const mcpInstallTabsPrimary = computed(() => [
  {
    label: 'Claude Code',
    lang: 'bash',
    note: 'Anthropic\'s `claude` CLI. Restart Claude Code afterwards; `/mcp` lists Orva\'s 37 tools.',
    code: `claude mcp add --transport http --scope user orva ${origin.value}/mcp --header "Authorization: Bearer ${T.value}"`,
  },
  {
    label: 'curl',
    lang: 'bash',
    note: 'Talk to MCP directly. Step 1 returns a session id (Mcp-Session-Id) that Step 2 references.',
    code: `curl -sD - -X POST ${origin.value}/mcp \\
  -H 'Authorization: Bearer ${T.value}' \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}'

curl -sX POST ${origin.value}/mcp \\
  -H 'Authorization: Bearer ${T.value}' \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -H 'Mcp-Session-Id: <SID>' \\
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'`,
  },
])

const mcpInstallTabsSecondary = computed(() => [
  {
    label: 'Claude Desktop',
    lang: 'json',
    note: 'Paste into ~/Library/Application Support/Claude/claude_desktop_config.json (macOS), %APPDATA%\\Claude\\claude_desktop_config.json (Windows), or ~/.config/Claude/claude_desktop_config.json (Linux). Restart Claude Desktop.',
    code: `{
  "mcpServers": {
    "orva": {
      "url": "${origin.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${T.value}"
      }
    }
  }
}`,
  },
  {
    label: 'Cursor',
    lang: 'bash',
    note: 'Open the link in your browser. Cursor pops an approval dialog and writes ~/.cursor/mcp.json.',
    code: `cursor://anysphere.cursor-deeplink/mcp/install?name=orva&config=${cursorConfigBase64.value}`,
  },
  {
    label: 'VS Code',
    lang: 'bash',
    note: 'User-scoped install via the Copilot-MCP `code --add-mcp` flag. Pick "Workspace" at the prompt to write .vscode/mcp.json instead.',
    code: `code --add-mcp '{"name":"orva","type":"http","url":"${origin.value}/mcp","headers":{"Authorization":"Bearer ${T.value}"}}'`,
  },
  {
    label: 'Codex CLI',
    lang: 'bash',
    note: 'OpenAI\'s `codex` CLI. Writes to ~/.codex/config.toml.',
    code: `codex mcp add --transport streamable-http orva ${origin.value}/mcp --header "Authorization: Bearer ${T.value}"`,
  },
  {
    label: 'OpenCode',
    lang: 'bash',
    note: `Interactive add. Pick "Remote", paste ${origin.value}/mcp, then add the header Authorization: Bearer ${T.value}.`,
    code: `opencode mcp add`,
  },
  {
    label: 'Zed',
    lang: 'json',
    note: 'Zed runs MCP as stdio subprocesses, so use the `mcp-remote` bridge. Paste under context_servers in ~/.config/zed/settings.json. Restart Zed.',
    code: `{
  "context_servers": {
    "orva": {
      "source": "custom",
      "command": "npx",
      "args": [
        "-y", "mcp-remote",
        "${origin.value}/mcp",
        "--header", "Authorization:Bearer ${T.value}"
      ]
    }
  }
}`,
  },
  {
    label: 'Windsurf',
    lang: 'json',
    note: 'Paste into ~/.codeium/windsurf/mcp_config.json and reload Windsurf.',
    code: `{
  "mcpServers": {
    "orva": {
      "serverUrl": "${origin.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${T.value}"
      }
    }
  }
}`,
  },
  {
    label: 'ChatGPT',
    lang: 'text',
    note: 'UI-only flow. Settings → Apps & Connectors → Developer mode → Add new connector. ChatGPT renders the tool catalog and confirms before destructive calls.',
    code: `URL:    ${origin.value}/mcp
Auth:   API key (Bearer)
Token:  ${T.value}`,
  },
])

const cursorConfigBase64 = computed(() => {
  const cfg = JSON.stringify({
    url: origin.value + '/mcp',
    headers: { Authorization: 'Bearer ' + T.value },
  })
  return typeof window.btoa === 'function' ? window.btoa(cfg) : cfg
})

const mcpConfigTabs = computed(() => [
  {
    label: 'Cursor (global)',
    lang: 'json',
    note: 'Paste into ~/.cursor/mcp.json, or .cursor/mcp.json in your project root for a per-workspace install.',
    code: `{
  "mcpServers": {
    "orva": {
      "url": "${origin.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${T.value}"
      }
    }
  }
}`,
  },
  {
    label: 'Cline',
    lang: 'json',
    note: 'In VS Code: open Cline → MCP icon → Configure MCP Servers. Cline writes cline_mcp_settings.json.',
    code: `{
  "mcpServers": {
    "orva": {
      "url": "${origin.value}/mcp",
      "headers": {
        "Authorization": "Bearer ${T.value}"
      },
      "disabled": false
    }
  }
}`,
  },
])

// ── Render-fn components (CodeBlock / TabbedCode / Callout) ─────────
// These need to live in this SFC because the data they render is
// computed in this script setup. CSS for them is in the unscoped
// <style> block at the bottom — small, prefixed, no leak risk.

const CodeBlock = defineComponent({
  name: 'CodeBlock',
  props: {
    code: { type: String, required: true },
    lang: { type: String, default: '' },
  },
  setup(props) {
    const copied = ref(false)
    const onCopy = async () => {
      const ok = await copyText(props.code)
      if (ok) {
        copied.value = true
        setTimeout(() => { copied.value = false }, 1200)
      }
    }
    const highlighted = computed(() => {
      const lang = (props.lang || '').toLowerCase()
      if (lang && hljs.getLanguage(lang)) {
        try {
          return hljs.highlight(props.code, { language: lang, ignoreIllegals: true }).value
        } catch {
          // fall through
        }
      }
      return props.code
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
    })

    return () =>
      h('div', { class: 'codeblock' }, [
        h('div', { class: 'codeblock-bar' }, [
          h('span', { class: 'codeblock-lang' }, props.lang || ''),
          h('button', { class: 'codeblock-copy', onClick: onCopy, title: 'Copy code' }, [
            copied.value ? h(Check, { class: 'w-3 h-3' }) : h(Copy, { class: 'w-3 h-3' }),
            copied.value ? 'Copied' : 'Copy',
          ]),
        ]),
        h('pre', { class: 'codeblock-pre' }, [
          h('code', {
            class: `hljs language-${(props.lang || 'text').toLowerCase()}`,
            innerHTML: highlighted.value,
          }),
        ]),
      ])
  },
})

const TabbedCode = defineComponent({
  name: 'TabbedCode',
  props: {
    tabs: { type: Array, required: true },
    storageKey: { type: String, default: '' },
  },
  setup(props) {
    const initial = (() => {
      try {
        if (props.storageKey) {
          const v = localStorage.getItem(props.storageKey)
          if (v && props.tabs.some((t) => t.label === v)) return v
        }
      } catch {
        // localStorage may be blocked
      }
      return props.tabs[0]?.label
    })()
    const active = ref(initial)
    const select = (label) => {
      active.value = label
      try {
        if (props.storageKey) localStorage.setItem(props.storageKey, label)
      } catch {
        // best-effort
      }
    }
    return () => {
      const tab = props.tabs.find((t) => t.label === active.value) || props.tabs[0]
      return h('div', { class: 'tabbed' }, [
        h('div', { class: 'tabbed-tabs' },
          props.tabs.map((t) =>
            h('button', {
              key: t.label,
              class: ['tabbed-tab', { active: t.label === active.value }],
              onClick: () => select(t.label),
            }, t.label)
          )
        ),
        tab.note ? h('div', { class: 'tabbed-note' }, tab.note) : null,
        h(CodeBlock, { code: tab.code, lang: tab.lang }),
      ])
    }
  },
})

const Callout = defineComponent({
  name: 'Callout',
  props: {
    title: { type: String, default: '' },
    icon: { type: [Object, Function], default: null },
  },
  setup(props, { slots }) {
    return () =>
      h('div', { class: 'callout' }, [
        h('div', { class: 'callout-head' }, [
          props.icon ? h(props.icon, { class: 'callout-icon' }) : null,
          props.title ? h('span', null, props.title) : null,
        ]),
        h('div', { class: 'callout-body' }, slots.default?.()),
      ])
  },
})
</script>

<style>
/* Unscoped because CodeBlock / TabbedCode / Callout are render-fn
   components inside this SFC, and Vue scoped styles don't reach those.
   All class names are doc-prefixed (.doc-*, .codeblock, .tabbed,
   .callout) so there's no collision risk.

   ── Type system for the Docs page ────────────────────────────────
   Body / prose:    Inter, --font-sans (inherits from body)
   Code / mono:     JetBrains Mono, --font-mono
   The classes below are the canonical set — every text node on this
   page picks one of them. No ad-hoc text-[10px] anywhere. */

/* ── Section landmarks ───────────────────────────────────────────── */
.doc-section-head {
  display: flex;
  align-items: flex-start;
  gap: 0.85rem;
}
.doc-section-num {
  display: inline-flex;
  flex-shrink: 0;
  align-items: center;
  justify-content: center;
  width: 1.85rem;
  height: 1.85rem;
  border-radius: 0.5rem;
  background: linear-gradient(135deg, var(--color-primary) 0%, var(--color-primary-hover) 100%);
  color: var(--color-primary-foreground);
  font-family: var(--font-mono);
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.04em;
  box-shadow: 0 0 0 1px rgba(255, 255, 255, 0.04) inset, 0 6px 18px -8px rgba(85, 63, 131, 0.6);
}
.doc-section-title {
  font-family: var(--font-sans);
  font-size: 1.05rem;
  line-height: 1.25;
  font-weight: 600;
  letter-spacing: -0.01em;
  color: var(--color-foreground);
  margin: 0;
}

/* ── Body prose ──────────────────────────────────────────────────── */
.doc-lede {
  font-family: var(--font-sans);
  font-size: 13px;
  line-height: 1.6;
  color: var(--color-foreground-muted);
  max-width: 64ch;
  margin: 0.35rem 0 0;
}

/* ── Inline code chip — used everywhere prose mentions a token ───── */
.doc-chip {
  display: inline-block;
  font-family: var(--font-mono);
  font-size: 11.5px;
  line-height: 1.4;
  padding: 0.1rem 0.4rem;
  margin: 0 0.05rem;
  border-radius: 0.3rem;
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  color: var(--color-foreground);
  white-space: nowrap;
  vertical-align: baseline;
}
.doc-chip.break-all {
  white-space: normal;
  word-break: break-all;
}

/* ── Microlabels (the all-caps eyebrow above sub-blocks) ─────────── */
.doc-microlabel {
  font-family: var(--font-mono);
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.14em;
  text-transform: uppercase;
  color: var(--color-foreground-muted);
}

/* ── Card (3-up KV style) ────────────────────────────────────────── */
.doc-card {
  position: relative;
  padding: 0.85rem 0.95rem;
  background:
    linear-gradient(180deg, rgba(255,255,255,0.015) 0%, transparent 100%),
    var(--color-background);
  border: 1px solid var(--color-border);
  border-radius: 0.6rem;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  transition: border-color 160ms;
}
.doc-card:hover {
  border-color: rgba(85, 63, 131, 0.6);
}
.doc-card-body {
  font-family: var(--font-sans);
  font-size: 13px;
  line-height: 1.55;
  color: var(--color-foreground);
  display: flex;
  flex-wrap: wrap;
  gap: 0.3rem 0.35rem;
  align-items: center;
}
.doc-card-body p {
  flex-basis: 100%;
  margin: 0;
  font-size: 12.5px;
  line-height: 1.55;
}

/* ── Step labels (numbered "1 → 2" deploy flow) ──────────────────── */
.doc-step-label {
  display: flex;
  align-items: center;
  gap: 0.55rem;
  font-family: var(--font-mono);
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.14em;
  text-transform: uppercase;
  color: var(--color-foreground-muted);
}
.doc-step-num {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1.1rem;
  height: 1.1rem;
  border-radius: 999px;
  background: rgba(85, 63, 131, 0.18);
  border: 1px solid rgba(85, 63, 131, 0.6);
  color: var(--color-foreground);
  font-size: 10px;
  font-weight: 700;
  letter-spacing: 0;
}

/* ── Tables ──────────────────────────────────────────────────────── */
.doc-table-wrap {
  background: var(--color-background);
  border: 1px solid var(--color-border);
  border-radius: 0.6rem;
  overflow: hidden;
}
.doc-table {
  width: 100%;
  border-collapse: collapse;
  font-family: var(--font-sans);
}
.doc-table thead {
  background: var(--color-surface);
  border-bottom: 1px solid var(--color-border);
}
.doc-table thead th {
  text-align: left;
  padding: 0.7rem 1rem;
  font-family: var(--font-mono);
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.14em;
  text-transform: uppercase;
  color: var(--color-foreground-muted);
}
.doc-table tbody tr {
  border-top: 1px solid var(--color-border);
  transition: background-color 120ms;
}
.doc-table tbody tr:first-child {
  border-top: 0;
}
.doc-table tbody tr:hover {
  background: rgba(255, 255, 255, 0.015);
}
.doc-table td {
  padding: 0.75rem 1rem;
  vertical-align: top;
}
.doc-cell-key {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-family: var(--font-sans);
  font-size: 13px;
  font-weight: 500;
  color: var(--color-foreground);
}
.doc-cell-key code {
  font-family: var(--font-mono);
  font-size: 12px;
  color: var(--color-foreground);
  font-weight: 500;
}
.doc-cell-mono {
  font-family: var(--font-mono);
  font-size: 12px;
  color: var(--color-foreground-muted);
}
.doc-cell-body {
  font-family: var(--font-sans);
  font-size: 12.5px;
  line-height: 1.55;
  color: var(--color-foreground);
}

/* ── Token bar (MCP) ─────────────────────────────────────────────── */
.doc-token-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.85rem;
  flex-wrap: wrap;
  padding: 0.65rem 0.85rem;
  background: linear-gradient(
    90deg,
    rgba(85, 63, 131, 0.10) 0%,
    rgba(85, 63, 131, 0.02) 70%,
    transparent 100%
  ), var(--color-background);
  border: 1px solid var(--color-border);
  border-left: 3px solid var(--color-primary);
  border-radius: 0.6rem;
}
.doc-token-btn {
  display: inline-flex;
  align-items: center;
  gap: 0.45rem;
  padding: 0.45rem 0.85rem;
  font-family: var(--font-sans);
  font-size: 12.5px;
  font-weight: 500;
  color: var(--color-foreground);
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: 0.4rem;
  cursor: pointer;
  transition: background 120ms, border-color 120ms;
}
.doc-token-btn:hover {
  background: var(--color-surface-hover);
  border-color: rgba(85, 63, 131, 0.6);
}
.doc-token-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* ── Details / collapsibles ──────────────────────────────────────── */
.doc-details {
  background: var(--color-background);
  border: 1px solid var(--color-border);
  border-radius: 0.6rem;
  overflow: hidden;
}
.doc-details-summary {
  list-style: none;
  display: flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.7rem 0.95rem;
  cursor: pointer;
  font-family: var(--font-sans);
  font-size: 13px;
  font-weight: 500;
  color: var(--color-foreground);
  user-select: none;
  transition: background 120ms;
}
.doc-details-summary::-webkit-details-marker {
  display: none;
}
.doc-details-summary:hover {
  background: rgba(255, 255, 255, 0.02);
}
.doc-details[open] > .doc-details-summary {
  border-bottom: 1px solid var(--color-border);
}
.doc-details-body {
  padding: 0.85rem;
}

/* ── Code block ──────────────────────────────────────────────────── */
.codeblock {
  background: var(--color-background);
  border: 1px solid var(--color-border);
  border-radius: 0.6rem;
  overflow: hidden;
}
.codeblock-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.45rem 0.85rem;
  background: var(--color-surface);
  border-bottom: 1px solid var(--color-border);
}
.codeblock-lang {
  font-family: var(--font-mono);
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.14em;
  text-transform: uppercase;
  color: var(--color-foreground-muted);
}
.codeblock-copy {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  padding: 0.3rem 0.6rem;
  background: var(--color-background);
  border: 1px solid var(--color-border);
  border-radius: 0.35rem;
  color: var(--color-foreground-muted);
  font-family: var(--font-sans);
  font-size: 11.5px;
  cursor: pointer;
  transition: color 120ms, border-color 120ms, background 120ms;
}
.codeblock-copy:hover {
  color: var(--color-foreground);
  border-color: rgba(85, 63, 131, 0.6);
  background: var(--color-surface-hover);
}
.codeblock-pre {
  margin: 0;
  padding: 0.95rem 1.1rem;
  overflow-x: auto;
  font-family: var(--font-mono);
  font-size: 12.5px;
  line-height: 1.6;
  color: #e6edf3;
  background: var(--color-background);
}
.codeblock-pre code {
  background: transparent !important;
  padding: 0 !important;
  font-family: inherit;
  font-size: inherit;
  line-height: inherit;
}

/* ── Tabbed code ─────────────────────────────────────────────────── */
.tabbed {
  background: var(--color-background);
  border: 1px solid var(--color-border);
  border-radius: 0.6rem;
  overflow: hidden;
}
.tabbed-tabs {
  display: flex;
  flex-wrap: wrap;
  gap: 0;
  background: var(--color-surface);
  border-bottom: 1px solid var(--color-border);
  padding: 0 0.35rem;
}
.tabbed-tab {
  position: relative;
  background: transparent;
  border: 0;
  padding: 0.6rem 0.95rem;
  font-family: var(--font-sans);
  font-size: 12.5px;
  font-weight: 500;
  color: var(--color-foreground-muted);
  cursor: pointer;
  transition: color 120ms;
}
.tabbed-tab:hover {
  color: var(--color-foreground);
}
.tabbed-tab.active {
  color: var(--color-foreground);
  font-weight: 600;
}
.tabbed-tab.active::after {
  content: '';
  position: absolute;
  left: 0.6rem;
  right: 0.6rem;
  bottom: -1px;
  height: 2px;
  background: var(--color-primary);
  border-radius: 2px 2px 0 0;
}
.tabbed-note {
  padding: 0.65rem 0.95rem;
  border-bottom: 1px solid var(--color-border);
  background: var(--color-surface);
  color: var(--color-foreground-muted);
  font-family: var(--font-sans);
  font-size: 12px;
  line-height: 1.55;
}
.tabbed > .codeblock {
  border: 0;
  border-radius: 0;
}

/* ── Callout ─────────────────────────────────────────────────────── */
.callout {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
  padding: 0.85rem 1rem;
  background: linear-gradient(
    90deg,
    rgba(85, 63, 131, 0.08) 0%,
    rgba(85, 63, 131, 0.01) 60%,
    transparent 100%
  ), var(--color-background);
  border: 1px solid var(--color-border);
  border-left: 3px solid var(--color-primary);
  border-radius: 0.6rem;
}
.callout-head {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-family: var(--font-mono);
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.14em;
  text-transform: uppercase;
  color: var(--color-foreground-muted);
}
.callout-icon {
  width: 0.95rem;
  height: 0.95rem;
}
.callout-body {
  display: flex;
  flex-wrap: wrap;
  gap: 0.3rem 0.4rem;
  align-items: center;
  font-family: var(--font-sans);
  font-size: 13px;
  line-height: 1.55;
  color: var(--color-foreground);
}

/* ── highlight.js calibration ────────────────────────────────────── */
/* github-dark.css ships with a default background that fights ours;
   strip it so .codeblock-pre's bg shows through. */
.codeblock-pre .hljs {
  background: transparent !important;
  color: inherit;
  padding: 0;
}
</style>
