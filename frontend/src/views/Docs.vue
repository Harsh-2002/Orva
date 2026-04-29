<template>
  <div class="docs-root">
    <!-- Hero -->
    <header class="hero">
      <div class="hero-eyebrow">
        Orva · Documentation
      </div>
      <h1 class="hero-title">
        Build serverless functions <span class="hero-accent">in minutes.</span>
      </h1>
      <p class="hero-lede">
        A practical guide for this Orva instance. Every code example below
        runs against <code class="origin-pill">{{ origin }}</code> — what you copy is what works.
      </p>
      <div class="hero-cta">
        <router-link
          to="/deploy"
          class="cta-primary"
        >
          <RocketIcon class="w-4 h-4" /> Create your first function
        </router-link>
        <a
          :href="`${origin}/api/v1/system/health`"
          target="_blank"
          rel="noopener"
          class="cta-secondary"
        >
          <Activity class="w-4 h-4" /> Check API health
        </a>
      </div>
    </header>

    <!-- Quick start (numbered cards) -->
    <Section
      id="quickstart"
      eyebrow="01"
      title="Quick start"
      kicker="From empty editor to first invocation in under a minute."
    >
      <div class="step-grid">
        <div
          v-for="(step, i) in quickSteps"
          :key="step.title"
          class="step-card"
        >
          <div class="step-num">
            {{ String(i + 1).padStart(2, '0') }}
          </div>
          <div class="step-title">
            {{ step.title }}
          </div>
          <p class="step-body">
            {{ step.body }}
          </p>
        </div>
      </div>
    </Section>

    <!-- Generate with AI -->
    <Section
      id="generate"
      eyebrow="02"
      title="Generate with AI"
      kicker="Skip the boilerplate. Have ChatGPT or Claude write your handler — pre-loaded with everything Orva supports."
    >
      <div class="ai-cta-row">
        <button
          class="ai-btn ai-btn-chatgpt"
          @click="onOpenChatGPT"
        >
          <span class="ai-btn-glyph">
            <OpenAIGlyph />
          </span>
          <span class="ai-btn-text">
            <span class="ai-btn-title">Open in ChatGPT</span>
            <span class="ai-btn-sub">Prefilled — press Enter to send</span>
          </span>
        </button>
        <button
          class="ai-btn ai-btn-claude"
          @click="onOpenClaude"
        >
          <span class="ai-btn-glyph">
            <ClaudeGlyph />
          </span>
          <span class="ai-btn-text">
            <span class="ai-btn-title">Open in Claude</span>
            <span class="ai-btn-sub">Prompt copied — paste in the new tab</span>
          </span>
        </button>
      </div>

      <Callout
        :icon="Wand2"
        tone="info"
        title="What we send"
      >
        A system prompt that teaches the model Orva's runtimes, handler
        contract, sandbox limits, and built-in auth modes. The full text is
        below — copy it for any other AI tool (Gemini, le Chat, your
        self-hosted model). Free to tweak before you send.
      </Callout>

      <div class="ai-prompt-actions">
        <button
          class="ai-copy-btn"
          :class="{ copied: promptCopied }"
          @click="onCopyPrompt"
        >
          <Check
            v-if="promptCopied"
            class="w-3.5 h-3.5"
          />
          <Copy
            v-else
            class="w-3.5 h-3.5"
          />
          {{ promptCopied ? 'Copied' : 'Copy system prompt' }}
        </button>
        <span
          v-if="claudeNote"
          class="ai-claude-note"
        >
          claude.ai removed direct prompt links in 2025 — we put the prompt on your clipboard, paste it in the chat that just opened.
        </span>
      </div>

      <CodeBlock
        :code="aiPromptText"
        lang="text"
      />
    </Section>

    <!-- Connect from your AI agent (MCP) -->
    <Section
      id="mcp"
      eyebrow="03"
      title="Connect from your AI agent (MCP)"
      kicker="One command per agent. Orva detects this instance's URL and bakes it into the snippet — pick your platform and paste."
    >
      <div class="kv-grid">
        <div class="kv">
          <div class="kv-label">
            Endpoint
          </div>
          <div class="kv-value">
            <code>{{ origin }}/api/v1/mcp</code>
          </div>
        </div>
        <div class="kv">
          <div class="kv-label">
            Auth
          </div>
          <div class="kv-value">
            <code>Authorization: Bearer &lt;orva_…&gt;</code>
          </div>
        </div>
        <div class="kv">
          <div class="kv-label">
            Transport
          </div>
          <div class="kv-value">
            Streamable HTTP, MCP <code>2025-11-25</code>
          </div>
        </div>
      </div>

      <!-- Token bar: every snippet uses {{ tokenLabel }}. The button mints
           a real key on click and substitutes it in-place. -->
      <div class="mcp-token-bar">
        <div class="mcp-token-summary">
          <KeyRound class="w-4 h-4 text-foreground-muted" />
          <span v-if="!mcpToken">
            Snippets below show <code>{{ tokenPlaceholder }}</code>. Click to mint a real key and substitute it everywhere.
          </span>
          <span
            v-else
            class="text-success"
          >
            Token minted: <code>{{ mcpTokenPrefix }}…</code> — substituted in every snippet below. Shown once; copy now.
          </span>
        </div>
        <button
          class="ai-copy-btn mcp-token-btn"
          :disabled="mcpTokenBusy"
          @click="onMintMcpToken"
        >
          <KeyRound class="w-3.5 h-3.5" />
          {{ mcpToken ? 'Mint another' : (mcpTokenBusy ? 'Minting…' : 'Generate token') }}
        </button>
      </div>

      <TabbedCode
        :tabs="mcpInstallTabs"
        storage-key="docs.mcp.install"
      />

      <details class="mcp-manual-details">
        <summary>Hand-editing the config file instead? Cursor JSON / Cline / etc.</summary>
        <TabbedCode
          :tabs="mcpConfigTabs"
          storage-key="docs.mcp.manual"
        />
      </details>

      <Callout
        :icon="ShieldCheck"
        tone="info"
        title="The agent has the same surface a human does"
      >
        37 tools cover everything the UI does: function CRUD, deploy /
        rollback, invoke, secrets, routes, API keys, firewall + DNS,
        autoscaler config. Tools are scoped by the API key's
        permissions — a <code>read</code>-only key sees only the
        <code>list_*</code> and <code>get_*</code> tools, period.
        Destructive tools (<code>delete_*</code>, <code>rollback_*</code>)
        require an explicit <code>confirm: true</code> argument.
      </Callout>

      <Callout
        :icon="KeyRound"
        tone="warn"
        title="Secrets stay encrypted, even from the agent"
      >
        Agents can <code>set_secret</code> and <code>delete_secret</code>,
        and <code>list_secrets</code> shows names — but there is
        <em>no</em> tool to read a stored secret value. Values are
        AES-256-GCM encrypted at rest and decrypted only into the
        sandbox process at invocation time. Same for API keys: minted
        plaintext is shown once in <code>create_api_key</code>'s
        response, then SHA256-hashed forever.
      </Callout>

      <p class="hint">
        The MCP server publishes
        <code>/.well-known/oauth-protected-resource</code> per RFC 9728
        for clients that probe for it. No OAuth flow is required — the
        static bearer token <em>is</em> the auth mechanism.
      </p>
    </Section>

    <!-- Handler shape with language tabs -->
    <Section
      id="handler"
      eyebrow="04"
      title="The handler"
      kicker="Export one function. Return an HTTP-shaped object. That's the contract."
    >
      <TabbedCode
        :tabs="handlerTabs"
        storage-key="docs.handler"
      />

      <div class="kv-grid">
        <div class="kv">
          <div class="kv-label">
            Input
          </div>
          <div class="kv-value">
            <code>event.method</code>, <code>event.path</code>, <code>event.headers</code>, <code>event.query</code>, <code>event.body</code>
          </div>
        </div>
        <div class="kv">
          <div class="kv-label">
            Output
          </div>
          <div class="kv-value">
            <code>{ statusCode, headers, body }</code> — body can be a string or any JSON-serialisable value.
          </div>
        </div>
        <div class="kv">
          <div class="kv-label">
            Injected
          </div>
          <div class="kv-value">
            Env vars and decrypted secrets are exposed via <code>process.env</code> / <code>os.environ</code> at spawn time.
          </div>
        </div>
      </div>
    </Section>

    <!-- Runtimes -->
    <Section
      id="runtimes"
      eyebrow="05"
      title="Runtimes"
      kicker="Latest two majors per language. Older minor versions auto-migrate."
    >
      <div class="runtime-grid">
        <div
          v-for="rt in runtimes"
          :key="rt.id"
          class="runtime-card"
          :class="rt.flavor"
        >
          <div class="runtime-icon">
            <component :is="rt.icon" />
          </div>
          <div class="runtime-id">
            {{ rt.id }}
          </div>
          <div class="runtime-name">
            {{ rt.name }}
          </div>
          <ul class="runtime-meta">
            <li>
              <span>Entry</span><code>{{ rt.entry }}</code>
            </li>
            <li>
              <span>Deps</span><code>{{ rt.deps }}</code>
            </li>
          </ul>
        </div>
      </div>
    </Section>

    <!-- Invoking -->
    <Section
      id="invoke"
      eyebrow="06"
      title="Invoking a function"
      kicker="Each function gets a stable URL. Send a body, return whatever the handler returns."
    >
      <TabbedCode
        :tabs="invokeTabs"
        storage-key="docs.invoke"
      />

      <Callout title="Custom routes">
        Want a friendly path like <code>/webhooks/stripe</code>? Attach a route via
        <code>POST /api/v1/routes</code>. Reserved prefixes (<code>/api/</code>, <code>/auth/</code>,
        <code>/web/</code>, <code>/_orva/</code>) are off-limits.
      </Callout>
    </Section>

    <!-- Deploy via API -->
    <Section
      id="deploy"
      eyebrow="07"
      title="Deploying via API"
      kicker="Two-step from CI: create the function row, upload a tarball."
    >
      <div class="deploy-flow">
        <div class="flow-step">
          <div class="flow-step-head">
            <span class="flow-num">1</span>
            <span class="flow-title">Create the function</span>
          </div>
          <CodeBlock
            :code="curlCreate"
            lang="bash"
          />
        </div>
        <div class="flow-arrow">
          <ArrowDown class="w-4 h-4" />
        </div>
        <div class="flow-step">
          <div class="flow-step-head">
            <span class="flow-num">2</span>
            <span class="flow-title">Upload code</span>
          </div>
          <CodeBlock
            :code="curlDeploy"
            lang="bash"
          />
        </div>
      </div>
      <p class="hint">
        Mint a key on the
        <router-link
          to="/api-keys"
          class="link"
        >
          Access Keys
        </router-link>
        page. Builds run async — poll <code>/api/v1/deployments/&lt;id&gt;</code> or watch the SSE stream until <code>phase: succeeded</code>.
      </p>
    </Section>

    <!-- Secrets & env -->
    <Section
      id="secrets"
      eyebrow="08"
      title="Secrets & environment"
      kicker="Plaintext for config, encrypted for credentials. Both reach your handler the same way."
    >
      <div class="dual-card">
        <div class="dual-pane">
          <div class="dual-icon env">
            <Variable class="w-4 h-4" />
          </div>
          <div class="dual-title">
            Environment variables
          </div>
          <p class="dual-body">
            Plaintext, set on the function record. Use for <em>build flags</em>, <em>feature toggles</em>, anything safe to read from the DB.
          </p>
        </div>
        <div class="dual-pane">
          <div class="dual-icon secret">
            <KeyRound class="w-4 h-4" />
          </div>
          <div class="dual-title">
            Secrets
          </div>
          <p class="dual-body">
            AES-256-GCM at rest, decrypted only into the sandbox process. Use for <em>API keys</em>, <em>DB URLs</em>, anything that shouldn't appear in the API.
          </p>
        </div>
      </div>
      <CodeBlock
        :code="curlSecret"
        lang="bash"
      />
      <p class="hint">
        Adding or removing a secret triggers a warm-pool refresh, so the next invocation sees the new value within seconds.
      </p>
    </Section>

    <!-- Network access -->
    <Section
      id="network"
      eyebrow="09"
      title="Network access"
      kicker="Off by default. Opt-in per function — most handlers are pure compute and don't need it."
    >
      <div class="dual-card">
        <div class="dual-pane">
          <div class="dual-icon env">
            <Globe class="w-4 h-4" />
          </div>
          <div class="dual-title">
            none <span class="text-foreground-muted font-normal">(default)</span>
          </div>
          <p class="dual-body">
            Function runs in an isolated network namespace with only loopback.
            <em>No DNS, no outbound TCP/UDP.</em> Best for pure-compute handlers
            and a strong containment guarantee.
          </p>
        </div>
        <div class="dual-pane">
          <div class="dual-icon secret">
            <Globe class="w-4 h-4" />
          </div>
          <div class="dual-title">
            egress
          </div>
          <p class="dual-body">
            Userspace TCP/UDP stack via nsjail's <code>--user_net</code>. The
            function can call <em>external HTTPS APIs</em> — Stripe, OpenAI, your
            DB. Host network interfaces are still not exposed.
          </p>
        </div>
      </div>
      <Callout
        :icon="AlertTriangle"
        tone="warn"
        title="Why off by default"
      >
        A serverless platform is exactly where one buggy or compromised
        function shouldn't be able to phone home. The toggle is per-function so
        you can grant network access only where it's needed and audit it via
        the egress badge on the Functions list.
      </Callout>
      <p class="hint">
        Toggle from the editor's <span class="text-white">Settings</span> modal
        ("Allow outbound network"). Changing the toggle drains warm workers,
        so the next invocation respawns with the new mode within seconds.
      </p>
    </Section>

    <!-- Securing your function -->
    <Section
      id="securing"
      eyebrow="10"
      title="Securing your function"
      kicker="Functions are public by default — same posture as Cloudflare Workers, Vercel Functions, and Lambda Function URLs. Auth is your function's job; the platform gives you opt-in guardrails."
    >
      <Callout
        :icon="ShieldCheck"
        tone="info"
        title="The mental model"
      >
        Your <span class="text-white">platform API key</span> never ships to
        a browser — it deploys functions and manages config. The credential
        a browser sends is the <span class="text-white">end user's</span> JWT
        or session cookie, and your handler verifies it. This is how every
        modern serverless platform works in production.
      </Callout>

      <h3 class="recipe-title">Recipe 1 — Verify a JWT (Auth0 / Clerk / Supabase / Firebase)</h3>
      <p class="recipe-body">
        Most user-facing apps ship a JWT to the browser at login. The browser
        attaches it as <code>Authorization: Bearer &lt;jwt&gt;</code> on every
        invoke. Your handler verifies the signature against the issuer's
        JWKS URL — store the issuer + audience as function secrets.
      </p>
      <CodeBlock
        :code="recipeJWT"
        lang="python"
      />

      <h3 class="recipe-title">Recipe 2 — Verify a Stripe webhook signature</h3>
      <p class="recipe-body">
        Webhook senders can't carry an Orva session. They sign each request
        with a shared secret instead — the canonical pattern. Store
        <code>STRIPE_WEBHOOK_SECRET</code> in function secrets and verify the
        <code>Stripe-Signature</code> header.
      </p>
      <CodeBlock
        :code="recipeStripe"
        lang="python"
      />

      <h3 class="recipe-title">Recipe 3 — Platform-managed gates</h3>
      <p class="recipe-body">
        For internal-only functions (cron jobs, server-to-server) flip
        <span class="text-white">Invoke gate</span> in the editor's Settings
        modal. Two modes are built in so you don't have to write the code:
      </p>
      <div class="dual-card">
        <div class="dual-pane">
          <div class="dual-icon env">
            <Lock class="w-4 h-4" />
          </div>
          <div class="dual-title">
            platform_key
          </div>
          <p class="dual-body">
            Caller must send <code>X-Orva-API-Key</code> or a valid Orva
            session cookie. Useful for "only my CI / cron / backend can hit
            this." Returns <code>401 UNAUTHORIZED</code> otherwise.
          </p>
        </div>
        <div class="dual-pane">
          <div class="dual-icon secret">
            <ShieldCheck class="w-4 h-4" />
          </div>
          <div class="dual-title">
            signed
          </div>
          <p class="dual-body">
            Caller must send <code>X-Orva-Signature: sha256=&lt;hex&gt;</code>
            and <code>X-Orva-Timestamp: &lt;unix-secs&gt;</code> computed as
            <code>HMAC(secret, "&lt;ts&gt;.&lt;body&gt;")</code>. Secret lives
            in the function's secret store under
            <code>ORVA_SIGNING_SECRET</code>. ±5 min skew window.
          </p>
        </div>
      </div>
      <CodeBlock
        :code="recipeSigned"
        lang="bash"
      />

      <Callout
        :icon="Gauge"
        tone="info"
        title="Rate limit (always available)"
      >
        Public functions can still be abuse magnets. Set
        <span class="text-white">Rate limit</span> in the editor to a
        per-IP-per-minute cap. Bursts up to the cap are allowed, then refill
        at <em>cap</em>/60 per second. Returns <code>429 RATE_LIMITED</code>
        with <code>Retry-After: 60</code>.
      </Callout>

      <h3 class="recipe-title">Recipe 4 — CORS for browser callers</h3>
      <p class="recipe-body">
        The platform stays out of the response — your handler controls
        every header. That means CORS lives in your code, where it can
        change per-route or per-user without a config rebuild. Three rules:
        answer <code>OPTIONS</code> preflights without auth, set CORS headers
        on every response (including <code>401</code> / <code>500</code> —
        otherwise the browser hides the real error), and allowlist origins
        rather than wildcarding when credentials are involved.
      </p>
      <CodeBlock
        :code="recipeCORS"
        lang="python"
      />

      <p class="hint">
        Anti-pattern: do not put your platform API key in browser JavaScript.
        It would be visible in DevTools within minutes. CORS is not auth — it
        only restricts <em>other websites</em> from reading your response in
        a user's browser, never blocks direct curl/Postman calls.
      </p>
    </Section>

    <!-- Versions -->
    <Section
      id="versions"
      eyebrow="11"
      title="Versions & rollback"
      kicker="Every deploy is content-addressed and kept on disk. Rollback is a symlink retarget — no rebuild."
    >
      <div class="timeline">
        <div
          v-for="(v, i) in versionTimeline"
          :key="v.label"
          class="timeline-item"
          :class="{ active: v.active }"
        >
          <div class="timeline-dot">
            <span>{{ versionTimeline.length - i }}</span>
          </div>
          <div class="timeline-body">
            <div class="timeline-title">
              {{ v.label }}<span
                v-if="v.active"
                class="timeline-pill"
              >active</span>
            </div>
            <div class="timeline-meta">
              {{ v.meta }}
            </div>
          </div>
        </div>
      </div>
      <Callout
        :icon="AlertTriangle"
        tone="warn"
        title="GC retention"
      >
        Default retention is the last <strong>5</strong> versions per function. Older ones get pruned. A rollback to a GC'd hash returns <code>VERSION_GCD</code> (HTTP 410) — re-deploy that code if you need it back.
      </Callout>
    </Section>

    <!-- Errors -->
    <Section
      id="errors"
      eyebrow="12"
      title="Error envelope"
      kicker="Every error has a stable code, a human message, and a request id. Surface the message; switch on the code."
    >
      <CodeBlock
        :code="errEnvelope"
        lang="json"
      />
      <div class="errors-grid">
        <div
          v-for="e in errorCodes"
          :key="e.code"
          class="error-card"
        >
          <code class="error-code">{{ e.code }}</code>
          <div class="error-when">
            {{ e.when }}
          </div>
        </div>
      </div>
    </Section>

  </div>
</template>

<script setup>
import { computed, h, ref, defineComponent } from 'vue'
import {
  RocketIcon,
  Activity,
  Variable,
  KeyRound,
  ArrowDown,
  AlertTriangle,
  Globe,
  Copy,
  Check,
  ShieldCheck,
  Lock,
  Gauge,
  Wand2,
} from 'lucide-vue-next'
import { copyText } from '@/utils/clipboard'
import {
  buildPromptText,
  openInChatGPT,
  openInClaude,
  copyPromptToClipboard,
} from '@/utils/aiPrompts'
import apiClient from '@/api/client'
import { useConfirmStore } from '@/stores/confirm'

const confirmStore = useConfirmStore()

// Syntax highlighting — highlight.js core + only the grammars we use.
// Importing the lite core (vs. the auto-bundle) keeps the Docs chunk small;
// each registerLanguage adds a few KB. We register python, js, json, bash,
// and http; everything else falls through as plain text.
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

const quickSteps = [
  { title: 'Pick a runtime', body: 'Node 22 / 24 or Python 3.13 / 3.14. Auto-detected from the code you paste.' },
  { title: 'Write the handler', body: 'A single function that accepts an event and returns { statusCode, headers, body }.' },
  { title: 'Deploy', body: 'One click. Code is content-addressed, the prior version stays available for rollback.' },
  { title: 'Invoke', body: 'Curl the URL printed under the editor, or wire it up to a custom route or cron schedule.' },
  { title: 'Secure it', body: 'Verify a JWT in your handler, or flip Invoke gate to platform_key / signed for server-to-server.' },
]

// ── "Generate with AI" state ────────────────────────────────────────
// aiPromptText is computed once (the spec rarely changes) and rendered
// inline in section 02 as a plain CodeBlock — full transparency.
const aiPromptText = buildPromptText()
const promptCopied = ref(false)
const claudeNote = ref(false)
let promptCopiedTimer = null
let claudeNoteTimer = null

const onOpenChatGPT = () => openInChatGPT()

const onOpenClaude = async () => {
  await openInClaude()
  claudeNote.value = true
  clearTimeout(claudeNoteTimer)
  claudeNoteTimer = setTimeout(() => { claudeNote.value = false }, 8000)
}

const onCopyPrompt = async () => {
  const ok = await copyPromptToClipboard()
  if (!ok) return
  promptCopied.value = true
  clearTimeout(promptCopiedTimer)
  promptCopiedTimer = setTimeout(() => { promptCopied.value = false }, 1500)
}

// ── Section component ─────────────────────────────────────────────────
// Lightweight wrapper so each section's frame (eyebrow / heading / kicker)
// stays consistent without a separate file.
const Section = defineComponent({
  name: 'DocSection',
  props: {
    id: { type: String, required: true },
    eyebrow: { type: String, default: '' },
    title: { type: String, required: true },
    kicker: { type: String, default: '' },
  },
  setup(props, { slots }) {
    return () =>
      h('section', { id: props.id, class: 'doc-section' }, [
        h('div', { class: 'sec-head' }, [
          props.eyebrow ? h('div', { class: 'sec-eyebrow' }, props.eyebrow) : null,
          h('h2', { class: 'sec-title' }, props.title),
          props.kicker ? h('p', { class: 'sec-kicker' }, props.kicker) : null,
        ]),
        h('div', { class: 'sec-body' }, slots.default?.()),
      ])
  },
})

// ── Code block with copy button ──────────────────────────────────────
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
    // Pre-render highlighted HTML once per (code, lang) pair. highlight.js
    // returns escaped HTML so it's safe to dump via v-html. Fallback to the
    // raw escaped code when the language isn't registered.
    const highlighted = computed(() => {
      const lang = (props.lang || '').toLowerCase()
      if (lang && hljs.getLanguage(lang)) {
        try {
          return hljs.highlight(props.code, { language: lang, ignoreIllegals: true }).value
        } catch {
          // fall through to plain
        }
      }
      // Plain text — escape so user payloads don't render as HTML.
      return props.code
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
    })

    return () =>
      h('div', { class: 'codeblock' }, [
        h('div', { class: 'codeblock-bar' }, [
          h('span', { class: 'codeblock-lang' }, props.lang),
          h('button', { class: 'codeblock-copy', onClick: onCopy, title: 'Copy code' }, [
            copied.value ? h(Check, { class: 'w-3 h-3' }) : h(Copy, { class: 'w-3 h-3' }),
            copied.value ? 'Copied' : 'Copy',
          ]),
        ]),
        h('pre', { class: 'codeblock-pre' }, [
          h('code', { class: `hljs language-${(props.lang || 'text').toLowerCase()}`, innerHTML: highlighted.value }),
        ]),
      ])
  },
})

// ── Tabbed code block ────────────────────────────────────────────────
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
      } catch {}
      return props.tabs[0]?.label
    })()
    const active = ref(initial)
    const select = (label) => {
      active.value = label
      try {
        if (props.storageKey) localStorage.setItem(props.storageKey, label)
      } catch {}
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

// ── Inline icons (light-touch SVG components for runtime cards) ──────
// Brand-accurate Python logo (two interlocking serpents). Uses the
// classic blue/yellow palette via gradient fills rather than the
// generic line-art placeholder.
const PythonGlyph = defineComponent({
  setup() {
    return () =>
      h('svg', { viewBox: '0 0 256 256', width: '32', height: '32', xmlns: 'http://www.w3.org/2000/svg' }, [
        h('defs', null, [
          h('linearGradient', { id: 'pygradBlue', x1: '0', y1: '0', x2: '1', y2: '1' }, [
            h('stop', { offset: '0', 'stop-color': '#5A9FD4' }),
            h('stop', { offset: '1', 'stop-color': '#306998' }),
          ]),
          h('linearGradient', { id: 'pygradYellow', x1: '0', y1: '0', x2: '1', y2: '1' }, [
            h('stop', { offset: '0', 'stop-color': '#FFE873' }),
            h('stop', { offset: '1', 'stop-color': '#FFD43B' }),
          ]),
        ]),
        // Top (blue) serpent
        h('path', {
          fill: 'url(#pygradBlue)',
          d: 'M126.9 12c-58.3 0-54.7 25.3-54.7 25.3l.1 26.2H128v8H50.5S12 67.2 12 126.1c0 58.9 33.6 56.8 33.6 56.8h19.4v-27.4s-1-33.6 33.1-33.6h55.9s32 .5 32-30.9V43.5S191.7 12 126.9 12zM95.7 29.9a10 10 0 0 1 0 20 10 10 0 0 1 0-20z',
        }),
        // Bottom (yellow) serpent
        h('path', {
          fill: 'url(#pygradYellow)',
          d: 'M129.1 244c58.3 0 54.7-25.3 54.7-25.3l-.1-26.2H128v-8h77.5s38.5 4.4 38.5-54.5c0-58.9-33.6-56.8-33.6-56.8h-19.4v27.4s1 33.6-33.1 33.6H102s-32-.5-32 30.9v51.5S64.3 244 129.1 244zm31.2-17.9a10 10 0 0 1 0-20 10 10 0 0 1 0 20z',
        }),
      ])
  },
})

// Official OpenAI mark — the "knot" / "blossom" logo from the public
// brand kit. Single monochrome path; uses currentColor so the button's
// --ai-accent flows through.
const OpenAIGlyph = defineComponent({
  setup() {
    return () =>
      h('svg', { viewBox: '0 0 24 24', width: '16', height: '16', xmlns: 'http://www.w3.org/2000/svg' }, [
        h('path', {
          fill: 'currentColor',
          d: 'M22.2819 9.8211a5.9847 5.9847 0 0 0-.5157-4.9108 6.0462 6.0462 0 0 0-6.5098-2.9A6.0651 6.0651 0 0 0 4.9807 4.1818a5.9847 5.9847 0 0 0-3.9977 2.9 6.0462 6.0462 0 0 0 .7427 7.0966 5.98 5.98 0 0 0 .511 4.9107 6.051 6.051 0 0 0 6.5146 2.9001A5.9847 5.9847 0 0 0 13.2599 24a6.0557 6.0557 0 0 0 5.7718-4.2058 5.9894 5.9894 0 0 0 3.9977-2.9001 6.0557 6.0557 0 0 0-.7475-7.073zM13.2599 22.4222a4.4866 4.4866 0 0 1-2.8814-1.0408l.1419-.0804 4.7783-2.7582a.7762.7762 0 0 0 .3927-.6814v-6.7361l2.02 1.1685a.071.071 0 0 1 .038.052v5.5826a4.5046 4.5046 0 0 1-4.4914 4.4938zM3.6029 18.3543a4.4866 4.4866 0 0 1-.5364-3.0218l.1418.0851 4.7783 2.7582a.7704.7704 0 0 0 .7805 0l5.8428-3.3685v2.3324a.0804.0804 0 0 1-.0332.0615L9.74 19.9502a4.4939 4.4939 0 0 1-6.1372-1.5959zM2.3408 7.8956a4.485 4.485 0 0 1 2.3655-1.9728V11.6a.7762.7762 0 0 0 .3879.6813l5.8144 3.3543-2.0201 1.1685a.0757.0757 0 0 1-.071 0l-4.8303-2.7865A4.504 4.504 0 0 1 2.3408 7.872zm16.5963 3.8558L13.1038 8.364 15.1192 7.2a.0757.0757 0 0 1 .071 0l4.8303 2.7913a4.4944 4.4944 0 0 1-.6765 8.1042v-5.6772a.79.79 0 0 0-.407-.667zm2.0107-3.0231l-.142-.0852-4.7735-2.7818a.7759.7759 0 0 0-.7854 0L9.409 9.2297V6.8974a.0662.0662 0 0 1 .0284-.0615l4.8303-2.7866a4.4992 4.4992 0 0 1 6.6802 4.66zM8.3065 12.863l-2.02-1.1638a.0804.0804 0 0 1-.038-.0567V6.0742a4.4992 4.4992 0 0 1 7.3757-3.4537l-.142.0805L8.704 5.459a.7948.7948 0 0 0-.3927.6813zm1.0976-2.3654l2.602-1.4998 2.6069 1.4998v2.9994l-2.5974 1.4997-2.6067-1.4997Z',
        }),
      ])
  },
})

// Official Anthropic/Claude wordmark "A" — sourced from the public
// Anthropic brand asset (simpleicons.org slug "anthropic"). Single
// monochrome path; --ai-accent on the parent supplies the brand
// terracotta.
const ClaudeGlyph = defineComponent({
  setup() {
    return () =>
      h('svg', { viewBox: '0 0 24 24', width: '16', height: '16', xmlns: 'http://www.w3.org/2000/svg' }, [
        h('path', {
          fill: 'currentColor',
          d: 'M17.3041 3.541h-3.6718l6.696 16.918H24Zm-10.6082 0L0 20.459h3.7442l1.3693-3.5527h7.0052l1.3693 3.5527h3.7442L10.5363 3.541Zm-.3712 10.2232 2.2914-5.9456 2.2914 5.9456Z',
        }),
      ])
  },
})

// Brand-accurate Node.js mark — the canonical hexagon with the inner
// "Node" silhouette path simplified for crisp rendering at 32px.
const NodeGlyph = defineComponent({
  setup() {
    return () =>
      h('svg', { viewBox: '0 0 256 280', width: '32', height: '32', xmlns: 'http://www.w3.org/2000/svg' }, [
        h('defs', null, [
          h('linearGradient', { id: 'nodegrad', x1: '0', y1: '0', x2: '1', y2: '1' }, [
            h('stop', { offset: '0', 'stop-color': '#41873F' }),
            h('stop', { offset: '0.5', 'stop-color': '#3F8B3D' }),
            h('stop', { offset: '1', 'stop-color': '#2D5F26' }),
          ]),
        ]),
        // Hexagon outer
        h('path', {
          fill: 'url(#nodegrad)',
          d: 'M128 0 12 67v146l116 67 116-67V67L128 0zm0 24.6 95 54.8v121.2l-95 54.8-95-54.8V79.4l95-54.8z',
        }),
        // Inner mark — stylised "N"
        h('path', {
          fill: '#FFFFFF',
          d: 'M128 64c-3 0-5.7.7-8 2.3L73 92c-5 2.7-8 8-8 13.6V169c0 5.6 3 10.7 8 13.5l13 7.4c6.3 3.1 8.5 3.1 11.4 3.1 9.4 0 14.8-5.7 14.8-15.6V117c0-1-.7-1.7-1.7-1.7H103c-1 0-1.7.7-1.7 1.7v60.2c0 4.4-4.5 8.7-11.8 5.1l-13.7-7.9a1.6 1.6 0 0 1-.8-1.4v-63.4c0-.6.3-1 .8-1.4l46.8-26.9c.4-.3 1-.3 1.4 0L171 110c.5.4.8.8.8 1.4V174a1.7 1.7 0 0 1-.8 1.4l-46.8 27c-.4.2-1 .2-1.4 0l-12-7.2c-.4-.2-.8-.2-1.2 0-3.4 1.9-4 2.2-7.2 3.3-.8.3-2 .7.4 2.1l15.7 9.3c2.5 1.4 5.3 2.2 8.2 2.2 2.9 0 5.7-.8 8.2-2.2L181 184c5-2.8 8-7.9 8-13.5V107c0-5.6-3-10.7-8-13.5l-46.7-26.7a17 17 0 0 0-6.3-2.8z',
        }),
      ])
  },
})

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
    code: `curl -X POST ${origin.value}/api/v1/invoke/<function_id> \\
  -H 'Content-Type: application/json' \\
  -d '{"name": "Orva"}'`,
  },
  {
    label: 'fetch',
    lang: 'js',
    code: `const res = await fetch('${origin.value}/api/v1/invoke/<function_id>', {
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
    "${origin.value}/api/v1/invoke/<function_id>",
    json={"name": "Orva"},
)
print(r.json())`,
  },
])

// ── MCP install: token state + per-platform commands ────────────────
//
// The user clicks "Generate token", we POST /api/v1/keys with a name
// like "MCP — <agent>" and `permissions: [invoke, read, write, admin]`,
// and the plaintext (returned ONCE by the API) gets substituted into
// every snippet on the page. Until that happens we render a clear
// placeholder so people can paste in their own existing key.

const tokenPlaceholder = '<YOUR_ORVA_TOKEN>'
const mcpToken = ref('')
const mcpTokenBusy = ref(false)
const mcpTokenPrefix = computed(() => mcpToken.value.slice(0, 12))
const tokenLabel = computed(() => mcpToken.value || tokenPlaceholder)

// Substituted token for snippet rendering — when no key has been minted
// we show the placeholder; once minted, the real plaintext.
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

// One-line install commands. Each tab's `code` is paste-ready —
// no comments, no extra prose. Tabs that target a config file
// surface the file path via the `note` field, rendered above the
// code block by TabbedCode.
const mcpInstallTabs = computed(() => [
  {
    label: 'Claude Code',
    lang: 'bash',
    note: 'Anthropic\'s official `claude` CLI. Restart Claude Code after running; `/mcp` will list Orva\'s 37 tools.',
    code: `claude mcp add --transport http --scope user orva ${origin.value}/api/v1/mcp --header "Authorization: Bearer ${T.value}"`,
  },
  {
    label: 'Claude Desktop',
    lang: 'json',
    note: 'No CLI install. Paste into ~/Library/Application Support/Claude/claude_desktop_config.json (macOS), %APPDATA%\\Claude\\claude_desktop_config.json (Windows), or ~/.config/Claude/claude_desktop_config.json (Linux). Restart Claude Desktop.',
    code: `{
  "mcpServers": {
    "orva": {
      "url": "${origin.value}/api/v1/mcp",
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
    note: 'Open this URL in your browser. Cursor pops up an approval dialog and writes ~/.cursor/mcp.json on accept.',
    code: `cursor://anysphere.cursor-deeplink/mcp/install?name=orva&config=${cursorConfigBase64.value}`,
  },
  {
    label: 'VS Code',
    lang: 'bash',
    note: 'User-scoped install via the Copilot-MCP `code --add-mcp` flag. Answer "Workspace" at the prompt to write .vscode/mcp.json instead.',
    code: `code --add-mcp '{"name":"orva","type":"http","url":"${origin.value}/api/v1/mcp","headers":{"Authorization":"Bearer ${T.value}"}}'`,
  },
  {
    label: 'Codex CLI',
    lang: 'bash',
    note: 'OpenAI\'s official `codex` CLI. Writes to ~/.codex/config.toml.',
    code: `codex mcp add --transport streamable-http orva ${origin.value}/api/v1/mcp --header "Authorization: Bearer ${T.value}"`,
  },
  {
    label: 'OpenCode',
    lang: 'bash',
    note: `Interactive add. When prompted: pick "Remote", paste the URL ${origin.value}/api/v1/mcp, then add the header Authorization: Bearer ${T.value}.`,
    code: `opencode mcp add`,
  },
  {
    label: 'Zed',
    lang: 'json',
    note: 'Zed runs MCP as stdio subprocesses, so the bridge is `mcp-remote`. Paste into ~/.config/zed/settings.json under context_servers. Restart Zed.',
    code: `{
  "context_servers": {
    "orva": {
      "source": "custom",
      "command": "npx",
      "args": [
        "-y", "mcp-remote",
        "${origin.value}/api/v1/mcp",
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
      "serverUrl": "${origin.value}/api/v1/mcp",
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
    note: 'No CLI — UI-only flow. Go to Settings → Apps & Connectors → toggle Developer mode → Add new connector. ChatGPT renders the tool catalog and asks for confirmation before destructive calls.',
    code: `URL:    ${origin.value}/api/v1/mcp
Auth:   API key (Bearer)
Token:  ${T.value}`,
  },
  {
    label: 'curl',
    lang: 'bash',
    note: 'Talk to the MCP endpoint directly. Step 1 prints the response headers — copy the Mcp-Session-Id value into Step 2.',
    code: `curl -sD - -X POST ${origin.value}/api/v1/mcp \\
  -H 'Authorization: Bearer ${T.value}' \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}'

curl -sX POST ${origin.value}/api/v1/mcp \\
  -H 'Authorization: Bearer ${T.value}' \\
  -H 'Content-Type: application/json' \\
  -H 'Accept: application/json, text/event-stream' \\
  -H 'Mcp-Session-Id: <SID>' \\
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}'`,
  },
])

// Cursor's install URL takes a base64-encoded JSON config — recompute
// whenever the token or origin changes.
const cursorConfigBase64 = computed(() => {
  const cfg = JSON.stringify({
    url: origin.value + '/api/v1/mcp',
    headers: { Authorization: 'Bearer ' + T.value },
  })
  // btoa is fine for ASCII content (URL + bearer are both ASCII).
  return typeof btoa === 'function' ? btoa(cfg) : cfg
})

// Manual config snippets — kept under a <details> for users who
// prefer hand-editing the client's config file rather than running
// a CLI. Snippets are clean JSON; file paths in the `note` field.
const mcpConfigTabs = computed(() => [
  {
    label: 'Cursor (global)',
    lang: 'json',
    note: 'Paste into ~/.cursor/mcp.json. Use .cursor/mcp.json in your project root for a per-workspace install.',
    code: `{
  "mcpServers": {
    "orva": {
      "url": "${origin.value}/api/v1/mcp",
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
    note: 'In VS Code: open the Cline panel → MCP icon → Configure MCP Servers. Cline writes to cline_mcp_settings.json.',
    code: `{
  "mcpServers": {
    "orva": {
      "url": "${origin.value}/api/v1/mcp",
      "headers": {
        "Authorization": "Bearer ${T.value}"
      },
      "disabled": false
    }
  }
}`,
  },
])

const runtimes = [
  { id: 'python314', name: 'Python 3.14', entry: 'handler.py', deps: 'requirements.txt', icon: PythonGlyph, flavor: 'flavor-py' },
  { id: 'python313', name: 'Python 3.13', entry: 'handler.py', deps: 'requirements.txt', icon: PythonGlyph, flavor: 'flavor-py' },
  { id: 'node24',    name: 'Node.js 24',  entry: 'handler.js', deps: 'package.json',     icon: NodeGlyph,   flavor: 'flavor-node' },
  { id: 'node22',    name: 'Node.js 22',  entry: 'handler.js', deps: 'package.json',     icon: NodeGlyph,   flavor: 'flavor-node' },
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
  -d '{"key":"DATABASE_URL","value":"postgres://…"}'`)

// JWT verification recipe — works for Auth0, Clerk, Supabase, Firebase, or
// any standard OIDC provider. Store JWT_ISSUER + JWT_AUDIENCE as function
// secrets; PyJWT pulls the JWKS automatically.
const recipeJWT = `import os, jwt
from jwt import PyJWKClient

JWKS = PyJWKClient(os.environ["JWT_JWKS_URL"])
AUD  = os.environ["JWT_AUDIENCE"]
ISS  = os.environ["JWT_ISSUER"]

def handler(event):
    auth = event["headers"].get("authorization", "")
    if not auth.startswith("Bearer "):
        return {"statusCode": 401, "body": "missing bearer token"}
    try:
        key = JWKS.get_signing_key_from_jwt(auth[7:]).key
        claims = jwt.decode(auth[7:], key, algorithms=["RS256"],
                            audience=AUD, issuer=ISS)
    except jwt.PyJWTError as e:
        return {"statusCode": 401, "body": f"invalid token: {e}"}
    user_id = claims["sub"]
    return {"statusCode": 200, "body": f"hello {user_id}"}`

// Stripe webhook signature verification — same shape works for GitHub,
// Slack, Twilio. Store STRIPE_WEBHOOK_SECRET in function secrets.
const recipeStripe = `import os, hmac, hashlib, time

SECRET = os.environ["STRIPE_WEBHOOK_SECRET"].encode()

def handler(event):
    sig_header = event["headers"].get("stripe-signature", "")
    body = event["body"].encode() if isinstance(event["body"], str) else event["body"]
    parts = dict(p.split("=", 1) for p in sig_header.split(","))
    ts = parts.get("t")
    sig = parts.get("v1")
    if not (ts and sig):
        return {"statusCode": 400, "body": "missing signature"}
    if abs(int(time.time()) - int(ts)) > 300:
        return {"statusCode": 400, "body": "timestamp too old"}
    payload = ts.encode() + b"." + body
    expected = hmac.new(SECRET, payload, hashlib.sha256).hexdigest()
    if not hmac.compare_digest(expected, sig):
        return {"statusCode": 400, "body": "signature mismatch"}
    # Process the event …
    return {"statusCode": 200, "body": "ok"}`

// CORS + auth in the handler. Three things to remember: answer OPTIONS
// without auth, attach CORS headers to every response (including failures),
// and allowlist origins rather than echoing "*" when credentials are in play.
const recipeCORS = `import os, jwt
from jwt import PyJWKClient

ALLOWED = {"https://myapp.com", "https://staging.myapp.com"}
JWKS = PyJWKClient(os.environ["JWT_JWKS_URL"])

def cors_headers(origin):
    allow = origin if origin in ALLOWED else "null"
    return {
        "Access-Control-Allow-Origin": allow,
        "Access-Control-Allow-Credentials": "true",
        "Access-Control-Allow-Methods": "GET, POST, OPTIONS",
        "Access-Control-Allow-Headers": "Content-Type, Authorization",
        "Access-Control-Max-Age": "86400",
        "Vary": "Origin",
    }

def handler(event, context):
    origin = event["headers"].get("origin", "")
    cors = cors_headers(origin)

    # 1. Preflight: answer BEFORE any auth check.
    if event["method"] == "OPTIONS":
        return {"statusCode": 204, "headers": cors, "body": ""}

    # 2. Auth — keep CORS headers on the failure response too,
    #    or the browser will hide the real error from your app.
    auth = event["headers"].get("authorization", "")
    if not auth.startswith("Bearer "):
        return {"statusCode": 401, "headers": cors, "body": "missing bearer"}
    try:
        key = JWKS.get_signing_key_from_jwt(auth[7:]).key
        claims = jwt.decode(auth[7:], key, algorithms=["RS256"],
                            audience=os.environ["JWT_AUDIENCE"],
                            issuer=os.environ["JWT_ISSUER"])
    except jwt.PyJWTError as e:
        return {"statusCode": 401, "headers": cors, "body": f"invalid: {e}"}

    # 3. Real handler — also returns CORS headers.
    return {"statusCode": 200,
            "headers": {**cors, "Content-Type": "application/json"},
            "body": '{"user": "' + claims["sub"] + '"}'}`

// Caller-side recipe for Orva's built-in 'signed' mode. Run from any
// trusted backend that already holds ORVA_SIGNING_SECRET out of band.
const recipeSigned = computed(() => `# generate signature
SECRET='your-shared-secret-stored-in-function-secrets'
TS=$(date +%s)
BODY='{"hello":"world"}'
SIG=$(printf '%s.%s' "$TS" "$BODY" | openssl dgst -sha256 -hmac "$SECRET" -hex | awk '{print $2}')

curl -X POST ${origin.value}/api/v1/invoke/<function_id> \\
  -H "X-Orva-Timestamp: $TS" \\
  -H "X-Orva-Signature: sha256=$SIG" \\
  -H 'Content-Type: application/json' \\
  -d "$BODY"`)

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

const versionTimeline = [
  { label: 'v3 — abc123def',  meta: 'Deployed 2m ago', active: true },
  { label: 'v2 — 4f5e6a',     meta: 'Yesterday', active: false },
  { label: 'v1 — 9c2b1f',     meta: '2 days ago', active: false },
]

// ── Inline Callout component ─────────────────────────────────────────
const Callout = defineComponent({
  name: 'Callout',
  props: {
    title: { type: String, default: '' },
    tone: { type: String, default: 'info' },
    icon: { type: [Object, Function], default: null },
  },
  setup(props, { slots }) {
    return () =>
      h('div', { class: ['callout', `tone-${props.tone}`] }, [
        h('div', { class: 'callout-head' }, [
          props.icon ? h(props.icon, { class: 'w-4 h-4' }) : null,
          props.title ? h('span', null, props.title) : null,
        ]),
        h('div', { class: 'callout-body' }, slots.default?.()),
      ])
  },
})
</script>

<style>
/* Docs page: not scoped because we render TabbedCode / CodeBlock /
   Callout / Section / runtime icons as inline render-fn components in
   the same SFC, and Vue scoped styles don't reach those. All class
   names are doc-prefixed (.docs-*, .sec-*, .codeblock, .tabbed, etc.)
   so there's no leak risk. */
.docs-root {
  /* Full-width to match Functions / Logs / Access Keys. The page no
     longer centers itself; readable line-length is enforced inside
     individual prose elements (.kicker, .lede, paragraphs) via ch-based
     max-widths so long lines don't get unreadable while the layout
     itself fills the available space. */
  width: 100%;
  padding-bottom: 4rem;
  color: var(--color-foreground);
}

/* Hero */
.hero {
  padding: 1rem 0 2.75rem;
  border-bottom: 1px solid var(--color-border);
  margin-bottom: 3rem;
}
.hero-eyebrow {
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.18em;
  color: var(--color-foreground-muted);
  text-transform: uppercase;
  margin-bottom: 1.25rem;
}
.hero-title {
  font-size: clamp(1.9rem, 3.4vw, 2.6rem);
  line-height: 1.1;
  letter-spacing: -0.02em;
  font-weight: 700;
  color: white;
  margin: 0 0 1rem;
  max-width: 40ch;
}
.hero-accent {
  background: linear-gradient(120deg, #b591ff 0%, #6a52d5 100%);
  -webkit-background-clip: text;
  background-clip: text;
  -webkit-text-fill-color: transparent;
}
.hero-lede {
  color: var(--color-foreground-muted);
  font-size: 15px;
  line-height: 1.7;
  max-width: 60ch;
  margin: 0 0 1.75rem;
}
.origin-pill {
  font-family: var(--font-mono);
  font-size: 0.85em;
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  padding: 0.1em 0.45em;
  border-radius: 4px;
  color: white;
}
.hero-cta {
  display: flex;
  flex-wrap: wrap;
  gap: 0.625rem;
}
.cta-primary,
.cta-secondary {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  padding: 0.6rem 1rem;
  border-radius: 8px;
  font-size: 13px;
  font-weight: 500;
  text-decoration: none;
  transition: background-color 150ms ease, border-color 150ms ease, color 150ms ease;
}
.cta-primary {
  background: var(--color-primary);
  color: white;
  border: 1px solid transparent;
  box-shadow: 0 6px 14px -4px rgba(85, 63, 131, 0.5);
}
.cta-primary:hover {
  background: var(--color-primary-hover);
}
.cta-secondary {
  background: transparent;
  color: var(--color-foreground-muted);
  border: 1px solid var(--color-border);
}
.cta-secondary:hover {
  color: white;
  border-color: var(--color-foreground-muted);
}

/* Section frame */
.doc-section {
  display: grid;
  /* CRUCIAL: minmax(0, 1fr) on the right column — without it, code
     blocks force the column wider than the viewport and push the
     sidebar offscreen. */
  grid-template-columns: minmax(0, 13rem) minmax(0, 1fr);
  gap: 2rem;
  padding: 2.5rem 0;
  border-top: 1px solid var(--color-border);
}
.doc-section:first-of-type {
  border-top: none;
  padding-top: 0;
}
@media (max-width: 720px) {
  .doc-section {
    grid-template-columns: 1fr;
    gap: 1rem;
  }
}
.sec-head {
  position: relative;
}
.sec-eyebrow {
  font-family: var(--font-mono);
  font-size: 11px;
  letter-spacing: 0.1em;
  color: var(--color-foreground-muted);
  margin-bottom: 0.5rem;
}
.sec-title {
  font-size: 1.35rem;
  font-weight: 600;
  letter-spacing: -0.01em;
  color: white;
  margin: 0 0 0.6rem;
}
.sec-kicker {
  font-size: 13px;
  line-height: 1.55;
  color: var(--color-foreground-muted);
  max-width: 22rem;
  margin: 0;
}
.sec-body {
  display: flex;
  flex-direction: column;
  gap: 1.25rem;
  min-width: 0;
}

/* Quick start step cards */
.step-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(200px, 1fr));
  gap: 0.75rem;
}
.step-card {
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: 10px;
  padding: 1rem;
  transition: border-color 150ms ease, transform 150ms ease;
}
.step-card:hover {
  border-color: var(--color-foreground-muted);
}
.step-num {
  font-family: var(--font-mono);
  font-size: 11px;
  color: var(--color-foreground-muted);
  letter-spacing: 0.08em;
  margin-bottom: 0.6rem;
}
.step-title {
  font-weight: 600;
  font-size: 13.5px;
  color: white;
  margin-bottom: 0.35rem;
}
.step-body {
  margin: 0;
  font-size: 12.5px;
  line-height: 1.55;
  color: var(--color-foreground-muted);
}

/* Tabbed + code blocks */
.tabbed {
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: 10px;
  overflow: hidden;
}
.tabbed-tabs {
  display: flex;
  gap: 0;
  border-bottom: 1px solid var(--color-border);
  background: var(--color-background);
}
.tabbed-tab {
  padding: 0.55rem 1rem;
  font-size: 12px;
  font-weight: 500;
  color: var(--color-foreground-muted);
  background: transparent;
  border: none;
  border-bottom: 2px solid transparent;
  cursor: pointer;
  transition: color 150ms ease, border-color 150ms ease;
}
.tabbed-tab:hover { color: white; }
.tabbed-tab.active {
  color: white;
  border-bottom-color: var(--color-primary);
}
.tabbed :deep(.codeblock) {
  border: none;
  border-radius: 0;
  background: var(--color-surface);
}
.tabbed-note {
  padding: 0.7rem 1rem;
  font-size: 12.5px;
  line-height: 1.5;
  color: var(--color-foreground-muted);
  background: var(--color-background);
  border-bottom: 1px solid var(--color-border);
}
.tabbed-note code {
  font-family: var(--font-mono);
  font-size: 0.85em;
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 0.05em 0.35em;
  color: white;
}

.codeblock {
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: 10px;
  overflow: hidden;
}
.codeblock-bar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 0.4rem 0.75rem;
  background: rgba(255, 255, 255, 0.025);
  border-bottom: 1px solid var(--color-border);
  font-family: var(--font-mono);
  font-size: 10.5px;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  color: var(--color-foreground-muted);
}
.codeblock-copy {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  padding: 0.2rem 0.55rem;
  border: 1px solid var(--color-border);
  border-radius: 5px;
  background: transparent;
  font-size: 10.5px;
  font-family: var(--font-sans);
  color: var(--color-foreground-muted);
  cursor: pointer;
  transition: color 150ms ease, border-color 150ms ease;
  text-transform: none;
  letter-spacing: 0;
}
.codeblock-copy:hover {
  color: white;
  border-color: var(--color-foreground-muted);
}
.codeblock-pre {
  margin: 0;
  padding: 1rem;
  overflow-x: auto;
  font-family: var(--font-mono);
  font-size: 12.5px;
  line-height: 1.6;
  color: white;
  white-space: pre;
}
/* highlight.js github-dark imports its own background; we override so the
   highlighted block blends with the page surface and only the token colors
   come from the theme. */
.codeblock-pre code.hljs {
  background: transparent !important;
  padding: 0 !important;
  font-size: inherit;
  font-family: inherit;
}

/* Inline KV grid (handler section) */
.kv-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 0.75rem;
}
.kv {
  border: 1px solid var(--color-border);
  background: var(--color-background);
  border-radius: 8px;
  padding: 0.85rem 1rem;
}
.kv-label {
  font-family: var(--font-mono);
  font-size: 10.5px;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--color-foreground-muted);
  margin-bottom: 0.4rem;
}
.kv-value {
  font-size: 13px;
  line-height: 1.55;
  color: white;
}
.kv-value :deep(code) {
  font-family: var(--font-mono);
  font-size: 0.85em;
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 0.05em 0.35em;
}

/* Runtime cards */
.runtime-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(180px, 1fr));
  gap: 0.75rem;
}
.runtime-card {
  border: 1px solid var(--color-border);
  background: var(--color-surface);
  border-radius: 10px;
  padding: 1rem;
  position: relative;
  overflow: hidden;
}
.runtime-card::before {
  content: '';
  position: absolute;
  inset: 0 0 auto 0;
  height: 2px;
  background: var(--accent, var(--color-primary));
}
.flavor-py    { --accent: linear-gradient(90deg, #4584b6, #ffde57); }
.flavor-node  { --accent: linear-gradient(90deg, #3c873a, #80bb38); }
.runtime-icon {
  width: 32px; height: 32px;
  border-radius: 8px;
  display: flex; align-items: center; justify-content: center;
  background: var(--color-background);
  border: 1px solid var(--color-border);
  color: white;
  margin-bottom: 0.6rem;
}
.runtime-id {
  font-family: var(--font-mono);
  font-size: 11px;
  color: var(--color-foreground-muted);
  letter-spacing: 0.05em;
}
.runtime-name {
  font-weight: 600;
  font-size: 14px;
  color: white;
  margin: 0.15rem 0 0.6rem;
}
.runtime-meta {
  list-style: none;
  margin: 0;
  padding: 0;
  font-size: 11.5px;
}
.runtime-meta li {
  display: flex;
  justify-content: space-between;
  padding: 0.2rem 0;
  color: var(--color-foreground-muted);
}
.runtime-meta li + li { border-top: 1px dashed var(--color-border); }
.runtime-meta code {
  font-family: var(--font-mono);
  font-size: 11px;
  color: white;
}

/* Deploy flow */
.deploy-flow {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.flow-step-head {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin-bottom: 0.5rem;
}
.flow-num {
  width: 22px; height: 22px;
  border-radius: 50%;
  display: inline-flex; align-items: center; justify-content: center;
  background: var(--color-primary);
  color: white;
  font-size: 11px;
  font-weight: 600;
}
.flow-title {
  font-weight: 600;
  font-size: 13px;
  color: white;
}
.flow-arrow {
  display: flex;
  justify-content: center;
  color: var(--color-foreground-muted);
  padding: 0.15rem 0;
}
.hint {
  font-size: 12.5px;
  color: var(--color-foreground-muted);
  line-height: 1.55;
  margin: 0;
}
.recipe-title {
  font-size: 13px;
  font-weight: 600;
  color: white;
  margin: 1.25rem 0 0.4rem;
  letter-spacing: 0.01em;
}
.recipe-body {
  font-size: 13px;
  color: var(--color-foreground-muted);
  line-height: 1.6;
  margin: 0 0 0.65rem;
}
.recipe-body code {
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: 3px;
  padding: 0.1rem 0.35rem;
  font-size: 12px;
}
.link {
  color: white;
  text-decoration: underline;
  text-underline-offset: 2px;
  text-decoration-color: var(--color-foreground-muted);
}
.link:hover { text-decoration-color: white; }

/* Dual-card (env vs secrets) */
.dual-card {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: 0.75rem;
}
.dual-pane {
  border: 1px solid var(--color-border);
  background: var(--color-surface);
  border-radius: 10px;
  padding: 1rem;
}
.dual-icon {
  width: 30px; height: 30px;
  border-radius: 8px;
  display: inline-flex; align-items: center; justify-content: center;
  margin-bottom: 0.6rem;
}
.dual-icon.env { background: rgba(85, 63, 131, 0.2); color: #b591ff; border: 1px solid rgba(181, 145, 255, 0.3); }
.dual-icon.secret { background: rgba(34, 197, 94, 0.15); color: #4ade80; border: 1px solid rgba(74, 222, 128, 0.3); }
.dual-title {
  font-weight: 600;
  font-size: 13.5px;
  color: white;
  margin-bottom: 0.4rem;
}
.dual-body {
  margin: 0;
  font-size: 12.5px;
  line-height: 1.55;
  color: var(--color-foreground-muted);
}
.dual-body em {
  color: white;
  font-style: normal;
  font-weight: 500;
}

/* Versions timeline */
.timeline {
  display: flex;
  flex-direction: column;
  gap: 0;
  padding-left: 0.5rem;
}
.timeline-item {
  display: flex;
  gap: 1rem;
  padding: 0.75rem 0;
  position: relative;
}
.timeline-item:not(:last-child)::after {
  content: '';
  position: absolute;
  left: 0.85rem;
  top: 2.25rem;
  bottom: -0.25rem;
  width: 1px;
  background: var(--color-border);
}
.timeline-dot {
  flex-shrink: 0;
  width: 28px; height: 28px;
  border-radius: 50%;
  display: inline-flex; align-items: center; justify-content: center;
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  font-family: var(--font-mono);
  font-size: 11px;
  color: var(--color-foreground-muted);
  z-index: 1;
}
.timeline-item.active .timeline-dot {
  background: var(--color-primary);
  border-color: var(--color-primary);
  color: white;
}
.timeline-title {
  font-weight: 600;
  font-size: 13px;
  color: white;
  display: flex;
  align-items: center;
  gap: 0.5rem;
}
.timeline-pill {
  font-size: 10px;
  padding: 0.15rem 0.4rem;
  background: rgba(34, 197, 94, 0.15);
  color: #4ade80;
  border: 1px solid rgba(74, 222, 128, 0.3);
  border-radius: 999px;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  font-weight: 500;
}
.timeline-meta {
  font-size: 11.5px;
  color: var(--color-foreground-muted);
  margin-top: 0.2rem;
}

/* Errors grid */
.errors-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 0.5rem;
}
.error-card {
  border: 1px solid var(--color-border);
  background: var(--color-background);
  border-radius: 8px;
  padding: 0.7rem 0.85rem;
}
.error-code {
  font-family: var(--font-mono);
  font-size: 11.5px;
  color: white;
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: 5px;
  padding: 0.1em 0.4em;
}
.error-when {
  font-size: 12px;
  color: var(--color-foreground-muted);
  margin-top: 0.45rem;
  line-height: 1.5;
}

/* Callout */
.callout {
  border: 1px solid var(--color-border);
  background: var(--color-surface);
  border-radius: 10px;
  padding: 0.85rem 1rem;
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}
.callout-head {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-size: 12px;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: var(--color-foreground-muted);
  font-weight: 600;
}
.callout-body {
  font-size: 13px;
  line-height: 1.55;
  color: var(--color-foreground);
}
.callout-body :deep(code) {
  font-family: var(--font-mono);
  font-size: 0.85em;
  background: var(--color-background);
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 0.05em 0.35em;
  color: white;
}
.tone-warn { background: rgba(234, 179, 8, 0.06); border-color: rgba(234, 179, 8, 0.3); }
.tone-warn .callout-head { color: #facc15; }
.tone-info .callout-head { color: var(--color-foreground-muted); }

/* "Generate with AI" — section 02 */
.ai-cta-row {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: 0.75rem;
}
.ai-btn {
  display: flex;
  align-items: center;
  gap: 0.85rem;
  padding: 0.95rem 1.1rem;
  border-radius: 12px;
  border: 1px solid var(--color-border);
  background: var(--color-surface);
  color: white;
  cursor: pointer;
  text-align: left;
  transition: border-color 150ms ease, background-color 150ms ease, transform 150ms ease;
  position: relative;
  overflow: hidden;
}
.ai-btn::before {
  content: '';
  position: absolute;
  inset: 0;
  background: var(--ai-glow, transparent);
  opacity: 0.08;
  pointer-events: none;
  transition: opacity 150ms ease;
}
.ai-btn:hover {
  transform: translateY(-1px);
}
.ai-btn:hover::before {
  opacity: 0.16;
}
.ai-btn-glyph {
  flex-shrink: 0;
  width: 32px;
  height: 32px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 8px;
  background: var(--color-background);
  border: 1px solid var(--color-border);
  color: var(--ai-accent, white);
}
.ai-btn-text {
  display: flex;
  flex-direction: column;
  min-width: 0;
}
.ai-btn-title {
  font-weight: 600;
  font-size: 13.5px;
  line-height: 1.2;
}
.ai-btn-sub {
  font-size: 11.5px;
  color: var(--color-foreground-muted);
  margin-top: 0.2rem;
}
.ai-btn-chatgpt {
  --ai-accent: #10a37f;
  --ai-glow: linear-gradient(120deg, #10a37f 0%, #0d8a6a 100%);
}
.ai-btn-chatgpt:hover {
  border-color: rgba(16, 163, 127, 0.55);
}
.ai-btn-claude {
  --ai-accent: #d97757;
  --ai-glow: linear-gradient(120deg, #d97757 0%, #b35e3f 100%);
}
.ai-btn-claude:hover {
  border-color: rgba(217, 119, 87, 0.55);
}

.ai-prompt-actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.75rem;
}
.ai-copy-btn {
  display: inline-flex;
  align-items: center;
  gap: 0.4rem;
  padding: 0.4rem 0.75rem;
  border-radius: 6px;
  border: 1px solid var(--color-border);
  background: var(--color-surface);
  color: var(--color-foreground-muted);
  font-size: 12px;
  cursor: pointer;
  transition: color 150ms ease, border-color 150ms ease;
}
.ai-copy-btn:hover {
  color: white;
  border-color: var(--color-foreground-muted);
}
.ai-copy-btn.copied {
  color: #4ade80;
  border-color: rgba(74, 222, 128, 0.4);
}
.ai-claude-note {
  font-size: 11.5px;
  color: var(--color-foreground-muted);
  line-height: 1.5;
  flex: 1 1 220px;
}
.fade-enter-active, .fade-leave-active { transition: opacity 220ms ease; }
.fade-enter-from, .fade-leave-to { opacity: 0; }

/* MCP install — token bar + manual-config disclosure */
.mcp-token-bar {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0.7rem 0.9rem;
  border: 1px solid var(--color-border);
  border-radius: 10px;
  background: var(--color-surface);
  flex-wrap: wrap;
}
.mcp-token-summary {
  display: flex;
  align-items: center;
  gap: 0.55rem;
  font-size: 12.5px;
  color: var(--color-foreground-muted);
  flex: 1 1 280px;
  line-height: 1.5;
}
.mcp-token-summary code {
  font-family: var(--font-mono);
  font-size: 0.85em;
  background: var(--color-background);
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 0.05em 0.35em;
  color: white;
}
.mcp-token-summary .text-success {
  color: #4ade80;
}
.mcp-token-btn {
  flex-shrink: 0;
}
.mcp-manual-details {
  border: 1px solid var(--color-border);
  border-radius: 10px;
  background: var(--color-surface);
  overflow: hidden;
}
.mcp-manual-details > summary {
  padding: 0.7rem 0.9rem;
  font-size: 12.5px;
  color: var(--color-foreground-muted);
  cursor: pointer;
  list-style: none;
  user-select: none;
  transition: color 150ms ease, background-color 150ms ease;
}
.mcp-manual-details > summary::-webkit-details-marker { display: none; }
.mcp-manual-details > summary::before {
  content: "▸";
  display: inline-block;
  margin-right: 0.45rem;
  transition: transform 150ms ease;
  color: var(--color-foreground-muted);
}
.mcp-manual-details[open] > summary::before {
  transform: rotate(90deg);
}
.mcp-manual-details > summary:hover { color: white; }
.mcp-manual-details[open] > summary {
  border-bottom: 1px solid var(--color-border);
}
.mcp-manual-details > :not(summary) {
  border-top-left-radius: 0;
  border-top-right-radius: 0;
}
</style>
