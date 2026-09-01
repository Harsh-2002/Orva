<template>
  <div class="flex flex-col h-full">
    <!--
      Visually-hidden semantic heading. The visible toolbar carries the
      function name as an <input> for editability; screen readers need
      a real <h1> to anchor the page in their heading-nav. The reactive
      content keeps the AT cursor's "you are on" announcement aligned
      with whatever the operator has typed in the name field.
    -->
    <h1 class="sr-only">
      {{ form.name || 'New function' }}
    </h1>
    <!-- Top action bar — distilled. The 8 individual panel buttons that
         used to live here (Settings, Env, Deps, Secrets, KV, Webhooks,
         Versions, Docs) collapsed to two grouped dropdowns: Config and
         Bindings. Everything still reachable in one extra click; the
         most-used surface in the product no longer fails the cognitive
         load check. Reset and Deploy stay as discrete CTAs. Click-outside
         and Esc dismiss the menus.

         Mobile (<sm): name + runtime label take a full first row; the
         dropdowns + actions take a second row. Above sm, the original
         single-row flex-wrap returns. -->
    <div class="flex flex-col sm:flex-row sm:flex-wrap sm:items-center gap-2 pb-3 border-b border-border">
      <div class="flex items-center gap-2 sm:mr-auto min-w-0 w-full sm:w-auto">
        <FileCode class="w-4 h-4 text-foreground-muted shrink-0" />
        <label
          for="fn-name-toolbar"
          class="sr-only"
        >Function name</label>
        <input
          id="fn-name-toolbar"
          v-model="form.name"
          placeholder="my-function"
          :disabled="isEditing"
          class="bg-transparent border-0 text-sm font-medium text-foreground-strong placeholder-foreground-muted focus:outline-none px-1 py-1 min-w-0 flex-1 sm:flex-none sm:w-40"
        >
        <button
          v-if="!isEditing && !fnId"
          type="button"
          class="p-1 rounded text-foreground-muted hover:text-foreground-strong hover:bg-surface-hover transition-colors shrink-0 touch-expand-iconbtn"
          title="Re-roll a fresh name"
          aria-label="Re-roll a fresh name"
          @click="rerollName"
        >
          <Shuffle class="w-3.5 h-3.5" />
        </button>
        <span class="text-[11px] text-foreground-muted font-medium tracking-tight shrink-0">{{ runtimeLabel(form.runtime) }}</span>
      </div>
      <!-- Wrapper for the dropdowns + actions on mobile so they sit on
           one row (or wrap among themselves). On sm+ they merge back
           into the parent flex-wrap. -->
      <div class="flex flex-wrap items-center gap-2 sm:contents">
        <!-- Config menu — Settings / Env / Deps / Secrets / Versions.
           Versions is hidden until isEditing && versions.length so the
           menu stays small for new-function flows. -->
        <div
          ref="configMenuRef"
          class="relative"
        >
          <button
            type="button"
            class="panel-btn"
            aria-haspopup="menu"
            :aria-expanded="menus.config"
            @click="toggleMenu('config')"
          >
            <Settings2 class="w-3.5 h-3.5" /> Config
            <ChevronDown class="w-3 h-3 text-foreground-muted" />
          </button>
          <div
            v-if="menus.config"
            class="absolute right-0 mt-1 z-30 min-w-[210px] bg-background border border-border rounded-md shadow-xl overflow-hidden"
            role="menu"
          >
            <button
              class="menu-item"
              role="menuitem"
              @click="openMenuItem('settings')"
            >
              <Settings2 class="w-3.5 h-3.5" />
              <span class="flex-1 text-left">Settings</span>
              <span class="text-[10px] text-foreground-muted shrink-0 whitespace-nowrap">runtime · limits</span>
            </button>
            <button
              class="menu-item"
              role="menuitem"
              @click="openMenuItem('envVars')"
            >
              <Variable class="w-3.5 h-3.5" />
              <span class="flex-1 text-left">Env</span>
              <span
                v-if="envVarCount"
                class="text-[10px] text-foreground-muted tabular-nums"
              >{{ envVarCount }}</span>
            </button>
            <button
              class="menu-item"
              role="menuitem"
              @click="openMenuItem('deps')"
            >
              <Package class="w-3.5 h-3.5" />
              <span class="flex-1 text-left">Deps</span>
              <span class="text-[10px] text-foreground-muted shrink-0 whitespace-nowrap">package · requirements</span>
            </button>
            <button
              class="menu-item"
              role="menuitem"
              @click="openMenuItem('secrets')"
            >
              <KeyRound class="w-3.5 h-3.5" />
              <span class="flex-1 text-left">Secrets</span>
              <span
                v-if="totalSecretsCount"
                class="text-[10px] text-foreground-muted tabular-nums"
              >{{ totalSecretsCount }}</span>
            </button>
            <button
              v-if="isEditing && versions.length"
              class="menu-item"
              role="menuitem"
              @click="openMenuItem('versions')"
            >
              <Layers class="w-3.5 h-3.5" />
              <span class="flex-1 text-left">Versions</span>
              <span class="text-[10px] text-foreground-muted tabular-nums">{{ versions.length }}</span>
            </button>
          </div>
        </div>

        <!-- Bindings menu — KV / Webhooks / Docs. KV + Webhooks only
           visible once the function is saved (they need an fnId).
           Docs is always available. -->
        <div
          ref="bindingsMenuRef"
          class="relative"
        >
          <button
            type="button"
            class="panel-btn"
            aria-haspopup="menu"
            :aria-expanded="menus.bindings"
            @click="toggleMenu('bindings')"
          >
            <Plug class="w-3.5 h-3.5" /> Bindings
            <ChevronDown class="w-3 h-3 text-foreground-muted" />
          </button>
          <div
            v-if="menus.bindings"
            class="absolute right-0 mt-1 z-30 min-w-[210px] bg-background border border-border rounded-md shadow-xl overflow-hidden"
            role="menu"
          >
            <button
              v-if="isEditing"
              class="menu-item"
              role="menuitem"
              @click="navMenu({ name: 'function-kv', params: { name: form.name } })"
            >
              <Database class="w-3.5 h-3.5" />
              <span class="flex-1 text-left truncate whitespace-nowrap">KV store</span>
              <span class="text-[10px] text-foreground-muted shrink-0 whitespace-nowrap">per-function state</span>
            </button>
            <button
              v-if="isEditing"
              class="menu-item"
              role="menuitem"
              @click="navMenu({ name: 'function-inbound-webhooks', params: { name: form.name } })"
            >
              <Webhook class="w-3.5 h-3.5" />
              <span class="flex-1 text-left truncate whitespace-nowrap">Webhooks</span>
              <span class="text-[10px] text-foreground-muted shrink-0 whitespace-nowrap">signed POST</span>
            </button>
            <button
              class="menu-item"
              role="menuitem"
              @click="openMenuItem('docs')"
            >
              <BookOpen class="w-3.5 h-3.5" />
              <span class="flex-1 text-left truncate whitespace-nowrap">Docs</span>
              <span class="text-[10px] text-foreground-muted shrink-0 whitespace-nowrap">handler reference</span>
            </button>
          </div>
        </div>

        <div class="w-px h-5 bg-border mx-1" />

        <!-- The workbench was only reachable from the result strip, which is
             empty until you have already run something. Named for the page it
             lands on, the way that page's own button back here says Editor. -->
        <Button
          v-if="fnId && form.name"
          variant="secondary"
          size="sm"
          title="Open the request workbench for this function"
          @click="openWorkbench"
        >
          <FlaskConical class="w-4 h-4" />
          Test
        </Button>
        <Button
          variant="secondary"
          size="sm"
          @click="resetForm"
        >
          Reset
        </Button>
        <!-- Three states in one box. min-w pins it across the label swaps:
             without it the toolbar reflowed on every click, and wrapped at
             phone width. -->
        <Button
          size="sm"
          class="min-w-[7.25rem]"
          :loading="deploying"
          @click="deployFunction"
        >
          <Check
            v-if="justDeployed && !deploying"
            class="w-4 h-4"
          />
          <UploadCloud
            v-else-if="!deploying"
            class="w-4 h-4"
          />
          {{ deploying ? 'Building' : justDeployed ? 'Deployed' : 'Deploy' }}
        </Button>
      </div><!-- /mobile-row wrapper for dropdowns + actions -->
    </div>

    <!-- Optional: Invoke URL strip. Only visible after the function exists,
         so the user always has the URL within reach without opening a modal. -->
    <div
      v-if="fnId"
      class="flex items-center gap-2 px-2 py-1.5 mt-2 border border-border bg-surface rounded text-xs"
    >
      <span class="text-foreground-muted shrink-0 uppercase tracking-wider text-[10px]">Invoke URL</span>
      <code class="font-mono text-foreground-strong truncate flex-1 min-w-0">{{ invokeUrl }}</code>
      <button
        class="touch-expand-xs inline-flex items-center h-7 px-2.5 rounded-md text-foreground-muted hover:text-foreground-strong hover:bg-surface-hover transition-colors flex items-center gap-1 shrink-0 touch-expand-sm"
        @click="copyInvokeUrl"
      >
        <Check
          v-if="urlCopied"
          class="w-3 h-3 text-success"
        />
        <Copy
          v-else
          class="w-3 h-3"
        />
        {{ urlCopied ? 'Copied' : 'Copy' }}
      </button>
      <router-link
        v-if="isEditing && form.name"
        :to="`/functions/${form.name}/deployments`"
        class="text-foreground-muted hover:text-foreground-strong transition-colors inline-flex h-7 items-center rounded-md px-2.5 touch-expand-sm"
      >
        Deployments →
      </router-link>
    </div>


    <!-- Code editor takes the whole main area. Its last row is the run strip,
         so the card is one instrument: name, surface, result. -->
    <div class="flex-1 flex flex-col min-h-0 mt-3 bg-background border border-border rounded-lg overflow-hidden shadow-sm">
      <div class="h-9 border-b border-border flex items-center justify-between px-4 bg-surface shrink-0">
        <div class="text-xs font-mono text-foreground-muted flex items-center gap-2">
          <FileCode class="w-3 h-3" />
          <span class="text-foreground-strong">{{ fileName }}</span>
          <span
            v-if="templateId"
            class="text-foreground-muted"
          >· template: {{ templateId }}</span>
        </div>
        <div class="flex items-center gap-3 shrink-0">
          <!-- A failure survives the modal being dismissed. Without this, the
               only record of a failed deploy was a dialog you had just closed. -->
          <button
            v-if="!deploying && buildError"
            type="button"
            class="touch-expand-sm inline-flex h-7 items-center gap-1.5 rounded-md px-2 text-[10px] font-medium text-danger-fg hover:bg-surface-hover transition-colors"
            @click="buildModalOpen = true"
          >
            <span class="w-1.5 h-1.5 rounded-full bg-danger-fg" />
            Build failed
          </button>
          <span
            v-else-if="deploying"
            class="text-[10px] text-foreground-muted font-medium flex items-center gap-1.5"
          >
            <span class="run-spinner" />
            Building
          </span>
          <!-- Which version is live is a fact about the function, not an event,
               so it stays here rather than being announced and taken away. -->
          <span
            v-else-if="lastBuild?.version"
            class="text-[10px] text-success-fg font-medium"
          >v{{ lastBuild.version }} live</span>
          <span class="text-[10px] text-foreground-muted font-mono">
            {{ code.length }} chars
          </span>
        </div>
      </div>
      <!-- The editor is always dark, in both themes. On paper that reads as a
           hole punched in the page unless it is framed, so it sits on a mat:
           a thin surface border with the instrument mounted inside it. In night
           the mat is nearly invisible against the canvas, which is correct,
           because there the editor already IS the page. -->
      <div class="orva-editor-mat flex-1 min-h-0 flex">
        <CodeEditor
          v-model="code"
          :language="form.runtime"
          class="flex-1 min-h-0"
        />
      </div>
      <!-- Last-run strip, the editor card's own status bar. One line on
           purpose: enough to confirm a deploy and leave, with the workbench a
           click away for anything longer. The row gap clears 11.4px because
           touch-expand-sm reaches 4px past each control, and two wrapped rows
           at gap-y-2 would overlap by 0.4px. -->
      <div class="flex flex-wrap items-center gap-x-3 gap-y-3 border-t border-border bg-surface/60 px-3 py-2">
        <button
          :disabled="!canTest || testbench.invoking"
          class="run-btn shrink-0"
          :title="canTest ? 'Replay the current test request' : 'Deploy first'"
          @click="runTest"
        >
          <Play
            v-if="!testbench.invoking"
            class="w-3 h-3"
          />
          <span
            v-else
            class="run-spinner"
          />
          Run
        </button>
        <!-- One live region, so a run that finishes seconds after the click is
             announced rather than only painted. -->
        <div
          role="status"
          aria-live="polite"
          class="flex flex-wrap items-center gap-x-3 gap-y-1 flex-1 min-w-0"
        >
          <!-- A build owns the strip while it runs: the previous run's verdict
               is about code that is being replaced, and reads as this build's. -->
          <template v-if="deploying">
            <span class="text-[10px] uppercase tracking-[0.14em] font-medium flex items-center gap-1.5 shrink-0 text-foreground-muted">
              <span class="run-spinner" />
              Building
            </span>
            <!-- Hidden on a phone for the same reason as the hint below: at
                 390px it truncated to "Build …", which reports nothing. The
                 spinner says it is building; View log says the rest. -->
            <span
              class="hidden sm:block font-mono text-[11px] text-foreground-muted flex-1 min-w-0 truncate"
            >{{ buildLogs[buildLogs.length - 1] }}</span>
            <button
              type="button"
              class="touch-expand-sm inline-flex h-7 items-center rounded-md px-2.5 text-[11px] text-link hover:text-foreground-strong transition-colors shrink-0"
              @click="buildModalOpen = true"
            >
              View log
            </button>
          </template>
          <template v-else>
            <!-- No status chip. "Idle" reported nothing, and "Response" next to
                 a 200 said it twice; the status code carries the tone itself. -->
            <span
              v-if="lastRunMeta"
              class="text-[11px] font-mono font-medium shrink-0"
              :class="lastRunTone"
            >{{ lastRunMeta }}</span>
            <span
              v-if="lastRunStale"
              class="text-[10px] text-foreground-muted shrink-0"
            >ran before this deploy</span>
            <code
              v-if="resultExcerpt"
              :title="resultExcerpt"
              class="font-mono text-[11px] truncate flex-1 min-w-0"
              :class="lastRun?.failed ? 'text-danger-fg' : 'text-foreground'"
            >{{ resultExcerpt }}</code>
            <span
              v-else-if="lastRun"
              class="text-[11px] text-foreground-muted flex-1 min-w-0 truncate"
            >No output. This run printed nothing and returned an empty body.</span>
            <!-- The hint is the one thing here worth losing on a phone: at 390px
                 it truncated to "Headers, fixtu…", which teaches nobody anything. -->
            <span
              v-else
              class="hidden sm:block text-[11px] text-foreground-muted flex-1 min-w-0 truncate"
            >Headers, fixtures, history and full logs live in the workbench.</span>
            <span
              v-if="runLines.length > 1"
              class="text-[10px] text-foreground-muted shrink-0"
            >{{ runLines.length }} log lines</span>
          </template>
        </div>
        <button
          v-if="lastRun?.failed"
          type="button"
          class="touch-expand-sm inline-flex h-7 items-center gap-1 rounded-md px-2.5 text-[11px] text-foreground-muted hover:text-foreground-strong hover:bg-surface-hover transition-colors shrink-0 disabled:opacity-50"
          :disabled="suggestingFix"
          title="Build a debug prompt from the source, the request and this run's stderr"
          @click="suggestFix"
        >
          <Sparkles class="w-3 h-3" />
          Suggest fix
        </button>
        <router-link
          v-if="fnId && form.name"
          :to="{ name: 'function-test', params: { name: form.name } }"
          class="touch-expand-sm inline-flex h-7 items-center rounded-md px-2.5 text-[11px] text-link hover:text-foreground-strong transition-colors shrink-0"
        >
          Open workbench →
        </router-link>
      </div>
    </div>

    <!-- ─────────────── Modals ─────────────── -->

    <!-- Build log. Raised by a failure, or on demand from the strip; never by
         starting a build. A pure reader of the deploy's state -- neither
         runDeploy nor streamBuild consults buildModalOpen, so dismissing this
         can only hide the log, never cancel what it is showing. -->
    <Modal
      v-model="buildModalOpen"
      title="Build log"
      :icon="Terminal"
      size="lg"
    >
      <div class="space-y-3">
        <div
          class="flex items-start gap-2"
          role="status"
          aria-live="polite"
        >
          <span
            v-if="deploying"
            class="run-spinner mt-1 shrink-0 text-foreground-muted"
          />
          <Check
            v-else-if="buildState === 'ok'"
            class="w-4 h-4 mt-0.5 shrink-0 text-success-fg"
          />
          <X
            v-else-if="buildState === 'failed'"
            class="w-4 h-4 mt-0.5 shrink-0 text-danger-fg"
          />
          <div class="min-w-0">
            <p
              class="text-sm font-medium break-words"
              :class="buildState === 'failed' ? 'text-danger-fg' : 'text-foreground-strong'"
            >
              {{ buildHeadline }}
            </p>
            <p
              v-if="deploying"
              class="text-xs text-foreground-muted mt-1"
            >
              Closing this does not cancel the build. It keeps running, and a failure brings this back.
            </p>
          </div>
        </div>
        <div
          ref="buildLogBox"
          class="scrollable max-h-64 overflow-y-auto rounded-md border border-border bg-surface px-3 py-2 font-mono text-[11px] leading-relaxed"
        >
          <p
            v-if="!buildLogs.length"
            class="text-foreground-muted"
          >
            Waiting for the builder.
          </p>
          <p
            v-for="(log, idx) in buildLogs"
            :key="idx"
            class="text-foreground-muted whitespace-pre-wrap break-words"
          >
            {{ log }}
          </p>
        </div>
      </div>
      <template #footer>
        <Button
          variant="ghost"
          @click="buildModalOpen = false"
        >
          Close
        </Button>
        <Button
          v-if="buildState === 'failed'"
          variant="secondary"
          :loading="suggestingBuildFix"
          @click="suggestBuildFix"
        >
          <Sparkles class="w-4 h-4" />
          Suggest fix
        </Button>
        <Button
          v-if="buildState === 'ok' && fnId && form.name"
          @click="openWorkbench"
        >
          Open workbench →
        </Button>
      </template>
    </Modal>

    <Modal
      v-model="modals.settings"
      title="Function configuration"
      :icon="Settings2"
      size="md"
    >
      <div class="space-y-4">
        <div>
          <label
            for="fn-description"
            class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5"
          >Description</label>
          <textarea
            id="fn-description"
            v-model="form.description"
            rows="2"
            placeholder="One-line summary of what this function does. Surfaces in MCP tool catalogs and the agent channel picker."
            class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring resize-y"
          />
        </div>
        <div>
          <div class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5 flex items-center justify-between">
            <span>Runtime</span>
            <span
              v-if="autoDetected && !runtimeManuallySet"
              class="text-[10px] normal-case tracking-normal text-success-fg"
            >auto-detected</span>
          </div>
          <div
            class="grid grid-cols-2 gap-2"
            role="group"
            aria-label="Runtime"
          >
            <button
              v-for="rt in runtimes"
              :key="rt.id"
              type="button"
              class="px-2 py-2 rounded border text-xs font-medium transition-colors duration-150 flex items-center justify-center gap-1.5 touch-expand-sm"
              :class="form.runtime === rt.id ? 'bg-primary text-primary-foreground border-primary' : 'bg-surface-hover text-foreground-muted border-border hover:border-foreground-muted'"
              :aria-pressed="form.runtime === rt.id"
              @click="setRuntimeManual(rt.id)"
            >
              <!-- Mark AND word here: the operator is choosing a runtime, not
                   recognising one they already picked, so the label carries the
                   pinned version. RuntimeTag renders icon-only in list rows,
                   where the row gives the context this button does not. -->
              <component
                :is="rt.id === 'python' ? PythonIcon : NodeIcon"
                class="w-3.5 h-3.5 shrink-0"
                aria-hidden="true"
              />
              {{ rt.label }}
            </button>
          </div>
        </div>
        <div>
          <label
            for="fn-template"
            class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5"
          >Template</label>
          <FilterSelect
            v-model="templateId"
            :options="templateOptions"
            label="Custom (blank)"
            trigger-id="fn-template"
            wide
            @update:model-value="applyTemplate"
          />
          <p
            v-if="selectedTemplateDescription"
            class="text-[11px] text-foreground-muted mt-1.5"
          >
            {{ selectedTemplateDescription }}
          </p>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <Input
            v-model.number="form.memory_mb"
            label="Memory (MB)"
            type="number"
            placeholder="64"
          />
          <Input
            v-model.number="form.cpus"
            label="CPUs"
            type="number"
            placeholder="0.5"
          />
        </div>

        <div class="border-t border-border pt-4 space-y-2">
          <h3 class="text-xs font-medium text-foreground-muted uppercase tracking-wide block">
            Concurrency
          </h3>
          <div class="grid grid-cols-2 gap-3">
            <div>
              <Input
                v-model.number="form.max_concurrency"
                label="Max concurrent (0 = unlimited)"
                type="number"
                placeholder="0"
              />
            </div>
            <div>
              <label
                for="fn-concurrency-policy"
                class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5"
              >When at cap</label>
              <FilterSelect
                v-model="form.concurrency_policy"
                :options="concurrencyPolicyOptions"
                :disabled="!form.max_concurrency"
                label="When at cap"
                trigger-id="fn-concurrency-policy"
                wide
              />
            </div>
          </div>
          <p class="text-[11px] text-foreground-muted leading-snug">
            Caps how many in-flight invocations one function can have. Use this to protect downstream APIs from a runaway handler.
          </p>
        </div>

        <div class="border-t border-border pt-4">
          <label class="flex items-start gap-3 cursor-pointer select-none">
            <input
              v-model="form.network_mode"
              type="checkbox"
              true-value="egress"
              false-value="none"
              class="mt-0.5 w-4 h-4 rounded border-border bg-background focus:outline-none focus:ring-1 focus:ring-focus-ring"
            >
            <div class="min-w-0">
              <div class="text-sm font-medium text-foreground-strong flex items-center gap-2">
                <Globe class="w-4 h-4 text-foreground-muted" /> Allow outbound network
              </div>
              <div class="text-xs text-foreground-muted mt-1 leading-snug">
                Off by default. Turn on if this function needs to call external
                APIs (Stripe, OpenAI, your DB). Adds ~5 ms cold-start.
              </div>
            </div>
          </label>
        </div>

        <div class="border-t border-border pt-4 space-y-2">
          <label
            for="fn-auth-mode"
            class="text-xs font-medium text-foreground-muted uppercase tracking-wide flex items-center gap-2"
          >
            <Lock class="w-3.5 h-3.5" /> Invoke gate
          </label>
          <FilterSelect
            v-model="form.auth_mode"
            :options="authModeOptions"
            label="Invoke gate"
            trigger-id="fn-auth-mode"
            wide
          />
          <p class="text-[11px] text-foreground-muted leading-snug">
            Public is the default, matches Cloudflare Workers and Vercel
            Functions. For end-user auth (JWT, session cookies), keep this on
            <span class="text-foreground-strong">Public</span> and verify inside your
            handler. <span class="text-foreground-strong">Signed</span> mode reads its key
            from the function secret <span class="font-mono">ORVA_SIGNING_SECRET</span>.
          </p>
        </div>

        <div class="border-t border-border pt-4 space-y-2">
          <h3 class="text-xs font-medium text-foreground-muted uppercase tracking-wide block">
            Rate limit
          </h3>
          <Input
            v-model.number="form.rate_limit_per_min"
            label="Requests per minute, per IP (0 = unlimited)"
            type="number"
            placeholder="0"
          />
          <p class="text-[11px] text-foreground-muted leading-snug">
            Token-bucket per client IP. A burst up to the limit is allowed,
            then refills at rate/60 per second. Returns 429 with
            <span class="font-mono">Retry-After: 60</span> when exceeded.
          </p>
        </div>

        <div class="border-t border-border pt-4 space-y-3">
          <h3 class="text-xs font-medium text-foreground-muted uppercase tracking-wide flex items-center gap-2">
            <Globe class="w-3.5 h-3.5" /> Custom routes
            <span
              v-if="routesLoading"
              class="text-[10px] normal-case tracking-normal text-foreground-muted"
            >loading…</span>
          </h3>

          <p
            v-if="!fnId"
            class="text-[11px] text-foreground-muted leading-snug"
          >
            Save the function first. Custom routes need a target function id.
          </p>

          <div
            v-else-if="myRoutes.length === 0"
            class="text-[11px] text-foreground-muted leading-snug"
          >
            No custom routes for this function. Default invoke URL is
            <span class="font-mono">/fn/{{ fnId.slice(0, 8) }}…</span>. Add a
            pretty path below (e.g. <span class="font-mono">/webhooks/stripe</span>
            or <span class="font-mono">/api/payments/*</span> for prefix match).
          </div>

          <ul
            v-else
            class="space-y-1.5"
          >
            <li
              v-for="r in myRoutes"
              :key="r.path"
              class="flex items-center gap-2 px-2.5 py-1.5 rounded border border-border bg-surface-hover/50"
            >
              <code class="flex-1 min-w-0 font-mono text-xs text-foreground truncate">{{ r.path }}</code>
              <span
                v-if="r.methods && r.methods !== '*'"
                class="text-[10px] font-mono text-foreground-muted"
              >{{ r.methods }}</span>
              <button
                class="shrink-0 w-6 h-6 flex items-center justify-center rounded text-foreground-muted hover:text-danger-fg hover:bg-surface transition-colors"
                title="Remove route"
                :aria-label="`Remove route ${r.path}`"
                @click="removeRoute(r.path)"
              >
                <X class="w-3 h-3" />
              </button>
            </li>
          </ul>

          <div
            v-if="fnId"
            class="space-y-2 pt-1"
          >
            <div class="flex items-center gap-2">
              <input
                v-model="newRoute.path"
                aria-label="Route path"
                placeholder="/path or /prefix/*"
                class="flex-1 min-w-0 bg-background border border-border rounded-md px-2 py-1.5 text-xs font-mono text-foreground focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
              >
              <input
                v-model="newRoute.methods"
                placeholder="*"
                title="Comma-separated methods or * for any (default *)"
                class="w-20 bg-background border border-border rounded-md px-2 py-1.5 text-xs font-mono text-foreground focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
              >
              <Button
                class="shrink-0"
                size="sm"
                @click="saveNewRoute"
              >
                Add
              </Button>
            </div>
            <p
              v-if="newRouteCollision"
              class="text-[11px] text-warning-fg leading-snug"
            >
              ⚠ <span class="font-mono">{{ newRouteCollision.path }}</span> already
              maps to function <span class="font-mono">{{ newRouteCollision.currentFunctionId.slice(0, 8) }}…</span>;
              clicking Add will remap it to this one.
            </p>
            <p
              v-if="routesError"
              class="text-[11px] text-danger-fg leading-snug"
            >
              {{ routesError }}
            </p>
            <p class="text-[11px] text-foreground-muted leading-snug">
              Reserved prefixes: <span class="font-mono">/api/</span>,
              <span class="font-mono">/auth/</span>,
              <span class="font-mono">/web/</span>,
              <span class="font-mono">/_orva/</span>. Prefix routes must end in
              <span class="font-mono">/*</span>.
            </p>
          </div>
        </div>
      </div>
      <template #footer>
        <Button
          variant="secondary"
          @click="modals.settings = false"
        >
          Done
        </Button>
      </template>
    </Modal>

    <Modal
      v-model="modals.envVars"
      title="Environment variables"
      :icon="Variable"
      size="md"
    >
      <div class="space-y-2">
        <div
          v-for="(pair, idx) in envVars"
          :key="idx"
          class="flex items-center gap-2"
        >
          <input
            v-model="pair.key"
            :aria-label="`Environment variable ${i + 1} name`"
            placeholder="KEY"
            class="flex-1 min-w-0 bg-background border border-border rounded-md px-2 py-1.5 text-xs text-foreground focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
          >
          <input
            v-model="pair.value"
            :aria-label="pair.key ? `Value for ${pair.key}` : `Environment variable ${i + 1} value`"
            placeholder="VALUE"
            class="flex-1 min-w-0 bg-background border border-border rounded-md px-2 py-1.5 text-xs text-foreground focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
          >
          <button
            class="shrink-0 w-7 h-7 flex items-center justify-center rounded text-foreground-muted hover:text-danger-fg hover:bg-surface transition-colors"
            title="Remove"
            :aria-label="`Remove environment variable ${pair.key || idx + 1}`"
            @click="removeEnvVar(idx)"
          >
            <X class="w-3.5 h-3.5" />
          </button>
        </div>
        <button
          class="text-xs text-foreground-muted hover:text-foreground-strong transition-colors"
          @click="addEnvVar"
        >
          + Add variable
        </button>
        <p class="text-[11px] text-foreground-muted pt-2 border-t border-border">
          Plaintext at deploy time. Use <span class="text-foreground-strong">Secrets</span> for sensitive values.
        </p>
      </div>
      <template #footer>
        <Button
          variant="secondary"
          @click="modals.envVars = false"
        >
          Done
        </Button>
      </template>
    </Modal>

    <Modal
      v-model="modals.deps"
      title="Dependencies"
      :icon="Package"
      size="md"
    >
      <div class="space-y-2">
        <div class="text-[10px] text-foreground-muted font-mono">
          {{ dependencyFileName }}
        </div>
        <textarea
          v-model="dependencyText"
          aria-label="Dependencies, one package per line"
          class="w-full bg-background border border-border rounded-md text-xs font-mono p-3 text-foreground focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring resize-none min-h-[200px]"
          placeholder="One package per line. e.g. requests==2.31.0"
        />
      </div>
      <template #footer>
        <Button
          variant="secondary"
          @click="modals.deps = false"
        >
          Done
        </Button>
      </template>
    </Modal>

    <Modal
      v-model="modals.secrets"
      title="Secrets"
      :icon="KeyRound"
      size="md"
    >
      <div class="space-y-3">
        <div
          v-if="!totalSecretsCount"
          class="text-xs text-foreground-muted"
        >
          No secrets yet. Add a key-value pair below.<span v-if="!fnId"> They'll be saved when you deploy.</span>
        </div>

        <!-- Persisted secrets (only relevant once the fn exists). -->
        <div
          v-for="sec in secrets"
          :key="sec.id"
          class="flex items-center justify-between text-xs px-3 py-2 rounded border border-border"
        >
          <span class="text-foreground-muted font-mono">{{ sec.name }}</span>
          <button
            class="text-foreground-muted hover:text-danger-fg transition-colors"
            :aria-label="`Delete secret ${sec.name}`"
            @click="removeSecret(sec.id)"
          >
            <Trash2 class="w-3.5 h-3.5" />
          </button>
        </div>

        <!-- Pending secrets — exist only on the new-function flow. They
             flush to the API as part of the first deploy. -->
        <div
          v-for="(sec, idx) in pendingSecrets"
          :key="'pending-' + idx"
          class="flex items-center justify-between text-xs px-3 py-2 rounded border border-warning-ring bg-warning-tint"
        >
          <div class="flex items-center gap-2 min-w-0">
            <span class="text-foreground-muted font-mono">{{ sec.name }}</span>
            <span class="text-[10px] uppercase tracking-wider text-warning-fg/80">pending</span>
          </div>
          <button
            class="text-foreground-muted hover:text-danger-fg transition-colors"
            :aria-label="`Remove pending secret ${sec.name}`"
            @click="removePendingSecret(idx)"
          >
            <Trash2 class="w-3.5 h-3.5" />
          </button>
        </div>

        <div class="border-t border-border pt-3 space-y-2">
          <input
            v-model="secretForm.name"
            aria-label="Secret name"
            placeholder="SECRET_NAME"
            class="w-full bg-background border border-border rounded-md px-2 py-1.5 text-xs text-foreground focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
          >
          <input
            v-model="secretForm.value"
            aria-label="Secret value"
            placeholder="SECRET_VALUE"
            type="password"
            class="w-full bg-background border border-border rounded-md px-2 py-1.5 text-xs text-foreground focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
          >
          <Button
            class="w-full"
            variant="secondary"
            @click="saveSecret"
          >
            <ShieldCheck class="w-4 h-4" /> {{ fnId ? 'Save secret' : 'Queue secret for deploy' }}
          </Button>
        </div>
      </div>
      <template #footer>
        <Button
          variant="secondary"
          @click="modals.secrets = false"
        >
          Done
        </Button>
      </template>
    </Modal>

    <Modal
      v-model="modals.versions"
      title="Version history"
      :icon="Layers"
      size="md"
    >
      <div class="space-y-2">
        <div
          v-for="v in versions"
          :key="v.deployment_id"
          class="flex items-center justify-between gap-2 text-xs px-3 py-2 rounded border border-border"
        >
          <div class="flex items-center gap-2 min-w-0">
            <span class="font-mono text-foreground-muted shrink-0">v{{ v.version }}</span>
            <span
              v-if="v.is_active"
              class="px-1.5 py-0.5 rounded text-[10px] bg-success-tint text-success-fg border border-success-ring shrink-0"
            >Active</span>
            <span
              class="font-mono text-foreground-muted truncate"
              :title="v.code_hash"
            >{{ v.short_hash }}</span>
            <span class="text-foreground-muted shrink-0">·</span>
            <span class="text-foreground-muted shrink-0">{{ new Date(v.created_at).toLocaleDateString() }}</span>
          </div>
          <div
            v-if="!v.is_active"
            class="shrink-0 flex items-center gap-2"
          >
            <router-link
              v-if="activeVersionDeploymentId"
              :to="{ name: 'function-diff', params: { name: route.params.name }, query: { from: v.deployment_id, to: activeVersionDeploymentId } }"
              class="text-foreground-muted hover:text-foreground-strong flex items-center gap-1"
              title="Compare with active version"
              @click="modals.versions = false"
            >
              <GitCompare class="w-3 h-3" /> Compare
            </router-link>
            <button
              :disabled="rollingBack"
              class="touch-expand-xs inline-flex h-7 items-center gap-1 rounded-md px-2.5 text-foreground-muted hover:bg-surface-hover hover:text-foreground-strong disabled:opacity-50"
              @click="rollbackToVersion(v)"
            >
              <RotateCcw class="w-3 h-3" /> Rollback
            </button>
          </div>
        </div>
      </div>
      <template #footer>
        <Button
          variant="secondary"
          @click="modals.versions = false"
        >
          Done
        </Button>
      </template>
    </Modal>

    <Modal
      v-model="modals.docs"
      title="Handler reference"
      :icon="BookOpen"
      size="lg"
    >
      <div class="space-y-3 text-xs text-foreground-muted">
        <p>
          Export a single <code class="font-mono text-foreground-strong">handler(event)</code> that returns an
          HTTP-shaped object. Orva injects env vars and secrets at spawn time.
        </p>
        <pre class="bg-surface border border-border rounded p-3 font-mono text-[12px] text-foreground-strong overflow-x-auto scrollable whitespace-pre">{{ handlerHint }}</pre>
        <ul class="space-y-1 pl-4 list-disc marker:text-foreground-muted">
          <li><span class="text-foreground-strong font-mono">event.body</span> is the raw request body, always a string. Parse it yourself.</li>
          <li>Return <span class="text-foreground-strong font-mono">{ statusCode, headers, body }</span>.</li>
          <li>Add packages via the <span class="text-foreground-strong">Deps</span> panel (installed at build time).</li>
        </ul>
        <router-link
          to="/docs"
          class="inline-flex items-center gap-1 text-foreground-muted hover:text-foreground-strong transition-colors"
          @click="modals.docs = false"
        >
          Open full docs in this UI →
        </router-link>
      </div>
      <template #footer>
        <Button
          variant="secondary"
          @click="modals.docs = false"
        >
          Close
        </Button>
      </template>
    </Modal>

    <!-- First-deploy modal: name + confirm runtime/limits before the
         actual upload starts. Only shown for fresh functions. -->
    <Modal
      v-model="modals.firstDeploy"
      title="Name & deploy"
      :icon="UploadCloud"
      size="md"
    >
      <div class="space-y-4">
        <div>
          <label
            for="deploy-fn-name"
            class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5"
          >
            Function name
          </label>
          <div class="relative">
            <input
              id="deploy-fn-name"
              ref="firstDeployNameInput"
              v-model="form.name"
              placeholder="my-function"
              class="w-full bg-background border border-border rounded-md pl-3 pr-10 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-focus-ring focus:border-focus-ring"
              @keydown.enter="confirmFirstDeploy"
            >
            <button
              type="button"
              class="absolute right-1.5 top-1/2 -translate-y-1/2 p-1.5 rounded text-foreground-muted hover:text-foreground-strong hover:bg-surface-hover transition-colors"
              title="Re-roll a fresh name"
              aria-label="Generate another function name"
              @click="rerollName"
            >
              <Shuffle class="w-3.5 h-3.5" />
            </button>
          </div>
          <p class="text-[11px] text-foreground-muted mt-1.5">
            Lowercase, dash-separated. Used in the invoke URL. Re-roll for a different combination.
          </p>
        </div>
        <div>
          <div class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5 flex items-center justify-between">
            <span>Runtime</span>
            <span
              v-if="autoDetected && !runtimeManuallySet"
              class="text-[10px] normal-case tracking-normal text-success-fg"
            >auto-detected</span>
          </div>
          <div
            class="grid grid-cols-2 gap-2"
            role="group"
            aria-label="Runtime"
          >
            <button
              v-for="rt in runtimes"
              :key="rt.id"
              type="button"
              class="px-2 py-2 rounded border text-xs font-medium transition-colors duration-150 flex items-center justify-center gap-1.5 touch-expand-sm"
              :class="form.runtime === rt.id ? 'bg-primary text-primary-foreground border-primary' : 'bg-surface-hover text-foreground-muted border-border hover:border-foreground-muted'"
              :aria-pressed="form.runtime === rt.id"
              @click="setRuntimeManual(rt.id)"
            >
              <!-- Mark AND word here: the operator is choosing a runtime, not
                   recognising one they already picked, so the label carries the
                   pinned version. RuntimeTag renders icon-only in list rows,
                   where the row gives the context this button does not. -->
              <component
                :is="rt.id === 'python' ? PythonIcon : NodeIcon"
                class="w-3.5 h-3.5 shrink-0"
                aria-hidden="true"
              />
              {{ rt.label }}
            </button>
          </div>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <Input
            v-model.number="form.memory_mb"
            label="Memory (MB)"
            type="number"
            placeholder="64"
          />
          <Input
            v-model.number="form.cpus"
            label="CPUs"
            type="number"
            placeholder="0.5"
          />
        </div>
      </div>
      <template #footer>
        <Button
          variant="ghost"
          @click="modals.firstDeploy = false"
        >
          Cancel
        </Button>
        <Button
          :disabled="!form.name.trim()"
          :loading="deploying"
          @click="confirmFirstDeploy"
        >
          <UploadCloud class="w-4 h-4" /> Deploy
        </Button>
      </template>
    </Modal>
  </div>
</template>

<script setup>
import { ref, computed, defineAsyncComponent, h, nextTick, onActivated, onBeforeUnmount, onDeactivated, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { FileCode, UploadCloud, Play, Layers, KeyRound, ShieldCheck, RotateCcw, Copy, Check, BookOpen, ChevronDown, Settings2, Variable, Package, X, Trash2, Terminal, Globe, Lock, Shuffle, Database, Sparkles, Webhook, Plug, GitCompare, FlaskConical } from '@lucide/vue'
import Button from '@/components/common/Button.vue'
import FilterSelect from '@/components/common/FilterSelect.vue'
import Input from '@/components/common/Input.vue'
import Modal from '@/components/common/Modal.vue'
import PythonIcon from '@/components/icons/brand/PythonIcon.vue'
import NodeIcon from '@/components/icons/brand/NodeIcon.vue'

// CodeMirror is the largest single contributor to the Editor chunk
// (~400 KB raw out of the 654 KB total before this split). The route
// shell, panel buttons, test pane, and modals are useful before the
// editor itself paints; loading CodeMirror eagerly forces every
// Editor visit to ship the full editor library on the critical path.
//
// defineAsyncComponent splits CodeEditor.vue + its codemirror imports
// into a separate chunk. The route's first paint lands fast (header,
// tabs, test pane scaffolding); the code surface streams in immediately
// after. delay: 0 + a skeleton loadingComponent prevents a flash of
// empty space while the chunk fetches.
const CodeEditorSkeleton = {
  // Mirrors CodeEditor's outer shape (flex-1, min-h-0, full width)
  // so the layout doesn't shift when the real component swaps in.
  render() {
    return h('div', {
      class: 'flex-1 min-h-0 w-full bg-background flex items-start',
      'aria-busy': 'true',
      'aria-label': 'Loading code editor',
    }, [
      h('div', { class: 'p-4 space-y-2 w-full font-mono text-xs text-foreground-muted' }, [
        h('div', { class: 'h-3 w-1/3 bg-surface-hover rounded animate-pulse' }),
        h('div', { class: 'h-3 w-2/3 bg-surface-hover rounded animate-pulse' }),
        h('div', { class: 'h-3 w-1/2 bg-surface-hover rounded animate-pulse' }),
      ]),
    ])
  },
}
const CodeEditor = defineAsyncComponent({
  loader: () => import('@/components/common/CodeEditor.vue'),
  loadingComponent: CodeEditorSkeleton,
  delay: 0,
})
import apiClient from '@/api/client'
import { copyText } from '@/utils/clipboard'
import { generateFunctionName } from '@/utils/funName'
import { runtimeLabel } from '@/utils/runtime'
import { templates, defaultCode, categoryOrder } from '@/templates'
import { rollbackFunction, listRoutes as apiListRoutes, setRoute as apiSetRoute, deleteRoute as apiDeleteRoute } from '@/api/endpoints'
import { copyFixSuggestionToClipboard, copyBuildFixToClipboard } from '@/utils/aiPrompts'
import { useConfirmStore } from '@/stores/confirm'
import { useTestbenchStore } from '@/stores/testbench'
import { describeSnapshotDiff } from '@/utils/rollbackDiff'

// The function this Editor is scoped to; empty on /functions/new. A route prop,
// not useRoute(): Vue does not patch a keep-alived view's props, and an Editor
// that followed the live route reset the unsaved buffer on every navigation to
// a page without a :name.
const props = defineProps({ name: { type: String, default: '' } })

const route = useRoute()
const router = useRouter()
const confirmStore = useConfirmStore()
const testbench = useTestbenchStore()

// Modals open-state. One per panel; clicking a header button toggles it.
const modals = ref({
  settings: false,
  envVars: false,
  deps: false,
  secrets: false,
  versions: false,
  docs: false,
  firstDeploy: false,
})

const firstDeployNameInput = ref(null)

// Distilled toolbar: two grouped dropdowns (Config, Bindings) replace
// the old 8-button row. Menu state is mutually exclusive — opening one
// closes the other so the toolbar can never sprout two adjacent panels.
// Click-outside and Esc dismiss; navigating into a route closes both.
const menus = ref({ config: false, bindings: false })
const configMenuRef = ref(null)
const bindingsMenuRef = ref(null)

const closeMenus = () => { menus.value.config = false; menus.value.bindings = false }
const toggleMenu = (which) => {
  const next = !menus.value[which]
  closeMenus()
  menus.value[which] = next
}
// Click a Config-menu item → open its modal and close the menu in one step.
const openMenuItem = (key) => {
  closeMenus()
  modals.value[key] = true
}
// Click a Bindings-menu item that navigates → push the route and close.
const navMenu = (to) => {
  closeMenus()
  router.push(to)
}

// Click-outside listener: closes whichever overlay is open if the press
// landed outside its root. Esc keydown does the same on the global key
// handler. Bound while this view is activated; see bindGlobals below.
//
// pointerdown, not mousedown: a touch device never fires @mouseleave, so a
// dropdown that leans on it has no way out on a phone. Both menu roots wrap
// their trigger AND their dropdown, so a press on an item is "inside".
const onDocClick = (e) => {
  if (!menus.value.config && !menus.value.bindings) return
  const inConfig = configMenuRef.value?.contains(e.target)
  const inBindings = bindingsMenuRef.value?.contains(e.target)
  if (!inConfig && !inBindings) closeMenus()
}
const onDocKey = (e) => {
  if (e.key === 'Escape') closeMenus()
}

// The build log is a modal Deploy opens, not a panel that stands there all day
// holding "No build activity yet." Nothing in the deploy path reads this ref, so
// closing it cannot reach the build.
const buildModalOpen = ref(false)
// Deploy acknowledges itself: the spinner becomes a check for a moment and
// then the button is a button again. A banner for a 1ms build is more
// interruption than the event is worth, and the version it announced is
// already in the file header and the version list.
const justDeployed = ref(false)
let deployedTimer = null

const envVarCount = computed(() => envVars.value.filter((p) => p.key.trim()).length)

const code = ref('')
const form = ref({
  name: '',
  description: '',                // surfaces in list_functions, get_function, the channel picker, and as the MCP tool description
  runtime: 'python',
  memory_mb: 64,
  cpus: 0.5,
  network_mode: 'none',          // 'none' | 'egress'
  max_concurrency: 0,             // 0 = unlimited
  concurrency_policy: 'queue',    // 'queue' | 'reject'
  auth_mode: 'none',              // 'none' | 'platform_key' | 'signed'
  rate_limit_per_min: 0,          // 0 = unlimited
})
const fnId = ref('')  // backend function ID

const concurrencyPolicyOptions = [
  { value: 'queue', label: 'Queue requests' },
  { value: 'reject', label: 'Reject (429)' },
]
const authModeOptions = [
  { value: 'none', label: 'Public, anyone can invoke' },
  { value: 'platform_key', label: 'Require Orva API key (server-to-server)' },
  { value: 'signed', label: 'Require HMAC signature (X-Orva-Signature)' },
]
const deploying = ref(false)
const deployedThisSession = ref(false)
const buildLogs = ref([])
// A build failure used to be written into the Test tab's response ref, which
// painted a build error under a Suggest-fix button for a run that never ran.
const buildError = ref('')
// What the last build produced, so the modal can name the version it made
// rather than making the operator read it back out of the log.
const lastBuild = ref(null)
const lastDeployAt = ref('')
const urlCopied = ref(false)
const suggestingFix = ref(false)
const suggestingBuildFix = ref(false)
const buildLogBox = ref(null)

// Declared up here, not beside streamBuild: resetEditorState runs from the
// props watcher during setup, which is before a later `let` has left its TDZ.
let abortActiveStream = null
// Bumped by every deploy and by every re-scope, so a build that outlives the
// editor that started it can tell it is no longer the one on screen.
let buildEpoch = 0

const buildState = computed(() => {
  if (deploying.value) return 'running'
  if (buildError.value) return 'failed'
  return lastBuild.value ? 'ok' : 'idle'
})
const buildHeadline = computed(() => {
  if (buildState.value === 'running') return 'Building'
  if (buildState.value === 'failed') return buildError.value
  const b = lastBuild.value
  if (!b) return 'No build has run in this session.'
  const ms = b.durationMs == null ? '' : ` in ${b.durationMs}ms`
  return b.version ? `v${b.version} live${ms}` : `Build succeeded${ms}`
})

// Autoscroll: a build log that stops following the newest line is a log you
// have to chase while it is still being written.
watch(() => buildLogs.value.length, () => {
  nextTick(() => {
    const el = buildLogBox.value
    if (el) el.scrollTop = el.scrollHeight
  })
})

const openWorkbench = () => {
  if (!form.value.name) return
  buildModalOpen.value = false
  router.push({ name: 'function-test', params: { name: form.value.name } })
}

// Invoke URL is built from window.location.origin so it works on localhost,
// custom IPs/ports, and behind reverse proxies with TLS termination — the
// browser's view of "where it reached the UI" is the right URL to invoke.
// Trailing slash matters: handler.path will be "/" instead of "" for the
// root, which is what most AWS/Lambda-style routers expect.
const invokeUrl = computed(() => {
  if (!fnId.value) return ''
  return `${window.location.origin}/fn/${fnId.value}`
})
const copyInvokeUrl = async () => {
  if (!invokeUrl.value) return
  const ok = await copyText(invokeUrl.value)
  if (ok) {
    urlCopied.value = true
    setTimeout(() => { urlCopied.value = false }, 1500)
  } else {
    confirmStore.notify({ title: 'Copy failed', message: 'Could not copy to clipboard. Select the URL manually:\n\n' + invokeUrl.value })
  }
}

const envVars = ref([{ key: '', value: '' }])
const dependencyText = ref('')
const templateId = ref('')
const versions = ref([])
const secrets = ref([])
const secretForm = ref({ name: '', value: '' })

// Custom routes — operator-defined URL → function mappings (e.g.
// /webhooks/stripe → this fn). Loaded lazily on Settings-modal open
// so the request only fires when relevant. routesAll holds the global
// list so we can run a collision check before saving a new path that
// already maps to a different function.
const routesAll = ref([])
const routesLoading = ref(false)
const routesError = ref('')
const newRoute = ref({ path: '', methods: '*' })
const newRouteCollision = ref(null) // { path, currentFunctionId } | null
const myRoutes = computed(() =>
  fnId.value ? routesAll.value.filter((r) => r.function_id === fnId.value) : [],
)

// Secrets queued before the function exists. New-function flow can't
// hit POST /functions/<id>/secrets yet (no id), so we hold them here
// until deployFunction creates the row, then flush in one batch.
const pendingSecrets = ref([])
const totalSecretsCount = computed(() => secrets.value.length + pendingSecrets.value.length)

const isEditing = computed(() => !!props.name)

// canTest: function has deployed code AND there's no active build in
// flight. While `deploying` is true, the warm pool may be holding stale
// code (or none, on a first deploy) so test invocations should wait.
const isDeployed = computed(() => isEditing.value || deployedThisSession.value)
const canTest = computed(() => isDeployed.value && !deploying.value)

// Orva offers two runtimes, latest-stable only. The id is generic (node /
// python); the version shown here is a display label that tracks latest-stable.
// Functions on legacy versioned ids are migrated to these on server startup.
const runtimes = [
  { id: 'python', label: 'Python 3.14' },
  { id: 'node',   label: 'Node.js 24' },
]

const isPythonRuntime = (rt) => rt === 'python'
const isNodeRuntime   = (rt) => rt === 'node'

// Support files and entrypoint carried over from a template. Both used to
// be dropped by applyTemplate, which made the TypeScript template
// undeployable from the dashboard: the builder gates its tsc step on
// tsconfig.json existing, and that file only lives in the template's
// `extras`.
const templateExtras = ref({})
const templateEntrypoint = ref('')

// The name this buffer deploys under.
//
// A template's entrypoint wins while one is selected, then the entrypoint of the
// function actually open, and only then a guess from the runtime. That middle
// case used to be missing: reopening a TypeScript function and pressing Deploy
// posted handler.js, so tsc never ran and the build died looking for output it
// had not been asked to produce.
const loadedEntrypoint = ref('')

const fileName = computed(() => {
  if (templateEntrypoint.value) return templateEntrypoint.value
  if (loadedEntrypoint.value) return loadedEntrypoint.value
  if (isPythonRuntime(form.value.runtime)) return 'handler.py'
  if (isNodeRuntime(form.value.runtime))   return 'handler.js'
  return 'handler.js'
})

// A .ts entrypoint needs a tsconfig in the tarball or the builder does not run
// tsc at all. A template supplies one; a reopened function has to carry its own,
// and the compiler options are not something to make the operator retype.
const TS_CONFIG = JSON.stringify({
  compilerOptions: {
    target: 'ES2022', module: 'NodeNext', moduleResolution: 'NodeNext',
    outDir: 'dist', rootDir: '.', strict: true, esModuleInterop: true,
    skipLibCheck: true,
  },
}, null, 2)

const deployExtras = computed(() => {
  if (Object.keys(templateExtras.value).length) return templateExtras.value
  if (fileName.value.endsWith('.ts')) return { 'tsconfig.json': TS_CONFIG }
  return {}
})

const handlerHint = computed(() => {
  if (isPythonRuntime(form.value.runtime)) {
    return `def handler(event):
    return {
        "statusCode": 200,
        "body": "ok"
    }`
  }
  return `exports.handler = async (event) => ({
  statusCode: 200,
  body: 'ok'
});`
})

// Templates and default code now live in /templates/index.js so the
// Editor stays focused on UI/state. Each runtime's list contains 8
// production-grade entries (HTTP / webhooks / auth / utility / scheduled).

const dependencyFileName = computed(() => {
  return isPythonRuntime(form.value.runtime) ? 'requirements.txt' : 'package.json'
})

const setRuntime = (rt) => {
  form.value.runtime = rt
  if (!isEditing.value && (!code.value || Object.values(defaultCode).includes(code.value))) {
    code.value = defaultCode[rt] || ''
  }
  if (!isEditing.value) {
    templateId.value = ''
    dependencyText.value = ''
    // Clearing the template must clear what it carried, or a TypeScript
    // entrypoint and tsconfig would survive a switch to Python.
    templateExtras.value = {}
    templateEntrypoint.value = ''
  }
}

// User-driven runtime change. Stops auto-detect from clobbering their
// pick on subsequent code edits.
const setRuntimeManual = (rt) => {
  runtimeManuallySet.value = true
  setRuntime(rt)
}

// Lightweight runtime auto-detection. Scores Python vs. JavaScript
// signals in the current code; switches form.runtime when one side
// wins clearly. Skipped once the user picks a runtime explicitly,
// or in edit mode (existing functions have a fixed runtime).
const runtimeManuallySet = ref(false)
const autoDetected = ref(false)

const detectLanguage = (src) => {
  if (!src || src.length < 10) return null
  const pyPatterns = [
    /\bdef\s+handler\s*\(/,
    /^import\s+\w/m,
    /^from\s+[\w.]+\s+import/m,
    /:\s*$/m,
    /\bprint\s*\(/,
    /\b(True|False|None)\b/,
    /\belif\b/,
  ]
  const jsPatterns = [
    /=>\s*[{(]/,
    /\bconst\s+\w/,
    /\blet\s+\w/,
    /module\.exports\b/,
    /\brequire\s*\(/,
    /\bexports\.\w/,
    /\basync\s+function\b/,
    /\bawait\s+\w/,
    /;\s*$/m,
  ]
  const py = pyPatterns.filter((re) => re.test(src)).length
  const js = jsPatterns.filter((re) => re.test(src)).length
  if (py >= js + 2) return 'python'
  if (js >= py + 2) return 'node'
  return null
}

let detectTimer = null
const scheduleDetect = (src) => {
  if (runtimeManuallySet.value || isEditing.value) return
  if (detectTimer) clearTimeout(detectTimer)
  detectTimer = setTimeout(() => {
    const lang = detectLanguage(src)
    if (!lang) return
    const isPy = isPythonRuntime(form.value.runtime)
    const isNode = isNodeRuntime(form.value.runtime)
    if (lang === 'python' && !isPy) {
      form.value.runtime = 'python'
      autoDetected.value = true
    } else if (lang === 'node' && !isNode) {
      form.value.runtime = 'node'
      autoDetected.value = true
    }
  }, 400)
}

// Re-run detection on every keystroke. Debounced inside scheduleDetect.
watch(code, (newCode) => scheduleDetect(newCode))

const applyTemplate = () => {
  const list = templates[form.value.runtime] || []
  const selected = list.find((t) => t.id === templateId.value)
  if (selected) {
    code.value = selected.code
    dependencyText.value = selected.deps || ''
    // Carry the template's support files and entrypoint. Both were dropped
    // here, which made the TypeScript template undeployable: the builder
    // gates its tsc step on tsconfig.json existing, and that file only ever
    // lived in the template's `extras`.
    templateExtras.value = selected.extras ? { ...selected.extras } : {}
    templateEntrypoint.value = selected.entrypoint || ''
    // Pre-fill the function's description from the template if the user
    // hasn't typed one yet — saves a step on quick-create flows and
    // ensures the function ships with a meaningful one-liner.
    if (selected.description && !form.value.description) {
      form.value.description = selected.description
    }
  }
}

// Templates grouped by category for the picker. Categories
// render in `categoryOrder`; an entry with no category falls into "Other".
const groupedTemplates = computed(() => {
  const list = templates[form.value.runtime] || []
  const buckets = new Map()
  for (const tpl of list) {
    const cat = tpl.category || 'Other'
    if (!buckets.has(cat)) buckets.set(cat, [])
    buckets.get(cat).push(tpl)
  }
  const ordered = categoryOrder
    .filter((c) => buckets.has(c))
    .map((c) => ({ label: c, items: buckets.get(c) }))
  for (const [c, items] of buckets) {
    if (!categoryOrder.includes(c)) ordered.push({ label: c, items })
  }
  return ordered
})

const templateOptions = computed(() => [
  { value: '', label: 'Custom (blank)' },
  ...groupedTemplates.value.flatMap((cat) => [
    { header: true, label: cat.label },
    ...cat.items.map((tpl) => ({
      value: tpl.id,
      label: `${tpl.label}${tpl.cron ? ' · scheduled' : ''}: ${tpl.description}`,
    })),
  ]),
])

const selectedTemplateDescription = computed(() => {
  const list = templates[form.value.runtime] || []
  const sel = list.find((t) => t.id === templateId.value)
  return sel?.description || ''
})

const addEnvVar = () => envVars.value.push({ key: '', value: '' })
const removeEnvVar = (index) => envVars.value.splice(index, 1)

// ── Custom routes ────────────────────────────────────────────────
//
// These manipulate the same /api/v1/routes resource the CLI/MCP use
// (`set_route`, `delete_route`, `list_routes`). Backend stores upsert-
// style, so saving a path that already maps to a DIFFERENT function
// would silently remap it. We catch that client-side: loadRoutes
// fetches the global list, and saveNewRoute checks for collisions
// before calling the API.

const loadRoutes = async () => {
  if (!fnId.value) return // create mode — no fn yet, nothing to list
  routesLoading.value = true
  routesError.value = ''
  try {
    const res = await apiListRoutes()
    routesAll.value = res.data?.routes || []
  } catch (err) {
    routesError.value = err.response?.data?.error || err.message || 'Failed to load routes'
  } finally {
    routesLoading.value = false
  }
}

// Detect collisions whenever the operator types a path. Looks the
// path up in the cached global list; collision = path exists AND
// points at a different function.
watch(
  () => newRoute.value.path,
  (path) => {
    const trimmed = (path || '').trim()
    if (!trimmed) {
      newRouteCollision.value = null
      return
    }
    const match = routesAll.value.find((r) => r.path === trimmed)
    if (match && match.function_id !== fnId.value) {
      newRouteCollision.value = { path: trimmed, currentFunctionId: match.function_id }
    } else {
      newRouteCollision.value = null
    }
  },
)

const saveNewRoute = async () => {
  if (!fnId.value) {
    routesError.value = 'Save the function first. Routes need a target function id.'
    return
  }
  const path = (newRoute.value.path || '').trim()
  const methods = (newRoute.value.methods || '*').trim() || '*'
  if (!path) {
    routesError.value = 'Path is required (must start with /).'
    return
  }
  if (!path.startsWith('/')) {
    routesError.value = 'Path must start with /.'
    return
  }
  // Collision-with-different-function: surface the in-app confirm so the
  // operator approves the remap explicitly. Browser-native confirm is
  // banned project-wide — themed dialog only.
  if (newRouteCollision.value && newRouteCollision.value.path === path) {
    const ok = await confirmStore.ask({
      title: 'Remap existing route?',
      message: `${path} currently points at function ${newRouteCollision.value.currentFunctionId.slice(0, 8)}…. Saving will remap it to this function.`,
      confirmLabel: 'Remap',
      cancelLabel: 'Keep existing',
      danger: true,
    })
    if (!ok) return
  }
  routesError.value = ''
  try {
    await apiSetRoute(path, fnId.value, methods)
    newRoute.value = { path: '', methods: '*' }
    newRouteCollision.value = null
    await loadRoutes()
  } catch (err) {
    routesError.value = err.response?.data?.error || err.message || 'Failed to save route'
  }
}

const removeRoute = async (path) => {
  const ok = await confirmStore.ask({
    title: 'Remove custom route?',
    message: `${path} will stop dispatching to this function. The function itself stays untouched and is still reachable at /fn/<id>.`,
    confirmLabel: 'Remove route',
    cancelLabel: 'Keep',
    danger: true,
  })
  if (!ok) return
  routesError.value = ''
  try {
    await apiDeleteRoute(path)
    await loadRoutes()
  } catch (err) {
    routesError.value = err.response?.data?.error || err.message || 'Failed to delete route'
  }
}

// Lazy-load routes when the Settings modal opens, so the request
// doesn't fire on every page mount.
watch(
  () => modals.value.settings,
  (open) => {
    if (open) loadRoutes()
  },
)

// loadRouteData re-scopes the Editor to the function in its `name` prop. Vue
// Router REUSES the same component instance between /functions/new (create
// mode) and /functions/<name> (edit mode), so a one-shot onMounted is the
// wrong hook: a user who landed on /functions/new first and then clicked an
// existing function would carry the boilerplate into edit mode. The watcher
// below fires `immediate: true`, so it covers the initial mount too.
const resetEditorState = () => {
  // A build outlives the editor that started it: the router reuses this
  // instance, so without this the previous function's failure re-opened the
  // build modal over the next one and blamed it for the error.
  buildEpoch += 1
  if (abortActiveStream) abortActiveStream()
  deploying.value = false
  fnId.value = ''
  form.value = {
    name: '',
    description: '',
    runtime: 'python',
    memory_mb: 64,
    cpus: 0.5,
    network_mode: 'none',
    max_concurrency: 0,
    concurrency_policy: 'queue',
    auth_mode: 'none',
    rate_limit_per_min: 0,
  }
  envVars.value = []
  code.value = ''
  dependencyText.value = ''
  buildLogs.value = []
  buildError.value = ''
  lastBuild.value = null
  buildModalOpen.value = false
  // Without this the acknowledgement follows you: deploy, navigate, and the
  // next function's button reads "Deployed" for the rest of the timer.
  clearTimeout(deployedTimer)
  justDeployed.value = false
  lastDeployAt.value = ''
  deployedThisSession.value = false
  versions.value = []
  autoDetected.value = false
  runtimeManuallySet.value = false
  templateId.value = ''
  templateExtras.value = {}
  templateEntrypoint.value = ''
}

const loadRouteData = async () => {
  resetEditorState()

  if (props.name) {
    // Edit mode — load function metadata + actual deployed source code.
    try {
      const listRes = await apiClient.get('/functions')
      const fn = (listRes.data.functions || []).find(f => f.name === props.name)
      if (!fn) throw new Error('Function not found')

      fnId.value = fn.id
      form.value.name = fn.name
      form.value.description = fn.description || ''
      form.value.runtime = fn.runtime
      // The file the operator authored. Without it the editor re-deployed every
      // function as handler.js, which silently dropped a TypeScript function's
      // .ts source on the next save.
      loadedEntrypoint.value = fn.entrypoint || ''
      form.value.memory_mb = fn.memory_mb
      form.value.cpus = fn.cpus
      form.value.network_mode = fn.network_mode || 'none'
      form.value.max_concurrency = fn.max_concurrency || 0
      form.value.concurrency_policy = fn.concurrency_policy || 'queue'
      form.value.auth_mode = fn.auth_mode || 'none'
      form.value.rate_limit_per_min = fn.rate_limit_per_min || 0

      if (fn.env_vars && Object.keys(fn.env_vars).length > 0) {
        envVars.value = Object.entries(fn.env_vars).map(([key, value]) => ({ key, value }))
      }

      // Fetch actual deployed source (not a template). Extracted into
      // a helper so any state-change action (rollback, manual refresh,
      // window-refocus) can re-pull without duplicating the fallback
      // logic.
      await reloadSource(fn)

      // Load existing secrets into the sidebar panel.
      await loadSecrets()
      // Load deployment history so the Versions card can render.
      await loadVersions(fn)
    } catch (e) {
      console.error('Failed to load function', e)
      confirmStore.notify({
        title: 'Could not open function',
        message: e.response?.data?.error?.message || e.message,
        danger: true,
      })
    }
  } else {
    // Create mode (/functions/new). Seed a friendly auto-generated
    // name so the field isn't empty. The user can edit it, clear it,
    // or hit the re-roll button next to it.
    setRuntime('python')
    form.value.name = generateFunctionName()
  }
}

watch(() => props.name, loadRouteData, { immediate: true })

// reloadSource pulls the function's currently-deployed source from
// `/api/v1/functions/<id>/source` and slams it into the editor buffer
// + dependency text. If the function exists but hasn't been deployed
// (or the source endpoint 404s), fall back to the runtime default
// template so the buffer is never blank for an existing function.
//
// Used by:
//   - Initial load (loadRouteData)
//   - Rollback (so the editor reflects the rolled-back code without
//     a hard browser refresh — the original bug was that rollback
//     re-fetched metadata but not the source)
//   - Window refocus (catches the "I deployed via CLI in another
//     terminal, came back to the open browser tab" case)
const reloadSource = async (fn) => {
  if (!fn) return
  try {
    const srcRes = await apiClient.get(`/functions/${fn.id}/source`)
    if (srcRes.data.code) {
      code.value = srcRes.data.code
      dependencyText.value = srcRes.data.dependencies || ''
      return
    }
  } catch {
    /* fall through to template */
  }
  code.value = defaultCode[fn.runtime] || ''
  dependencyText.value = ''
}

// Window-refocus handler — operators commonly deploy / rollback / edit
// configs via CLI or another browser tab, then click back to this
// editor expecting it to reflect the latest state. Without this the
// editor remains stale until a hard reload. Best-effort: any error
// is swallowed so a transient network blip doesn't disrupt the
// editing session.
const onWindowFocus = async () => {
  if (!fnId.value) return
  try {
    const listRes = await apiClient.get('/functions')
    const fn = (listRes.data.functions || []).find((f) => f.id === fnId.value)
    if (!fn) return
    await reloadSource(fn)
    await loadVersions(fn)
  } catch {
    /* ignore; user can hit Reload manually */
  }
}

// Cmd-S → Deploy. The CommandPalette suppresses the browser save
// dialog and dispatches `orva:deploy`; we listen here and fire the
// existing deployFunction handler so the keybinding works whether
// the palette is open or not. Suppress while a build is already in
// flight to avoid double-clicks racing through cmd-S spam.
const onDeployShortcut = () => {
  if (deploying.value) return
  deployFunction()
}

// Bound to the activation window, not the mount. keep-alive leaves this view
// mounted behind /functions/<name>/kv, where Cmd-S still dispatches
// orva:deploy — and the cached Editor deployed on it, with no page to show it.
const bindGlobals = () => {
  window.addEventListener('focus', onWindowFocus)
  document.addEventListener('pointerdown', onDocClick)
  document.addEventListener('keydown', onDocKey)
  window.addEventListener('orva:deploy', onDeployShortcut)
}
const unbindGlobals = () => {
  window.removeEventListener('focus', onWindowFocus)
  document.removeEventListener('pointerdown', onDocClick)
  document.removeEventListener('keydown', onDocKey)
  window.removeEventListener('orva:deploy', onDeployShortcut)
}
onActivated(bindGlobals)
onDeactivated(unbindGlobals)
// onDeactivated does not fire when keep-alive evicts this instance at :max.
onBeforeUnmount(() => { unbindGlobals(); clearTimeout(deployedTimer) })

// The `prefill` deep link ("Save as fixture" in InvocationsLog) carries a
// captured production request, headers and all: that belongs in the workbench,
// so this loads the store and replaces itself with that route rather than
// leaving a redirect stop in history for the Back button to fall into.
const applyPrefillFromQuery = () => {
  const raw = route.query.prefill
  if (!raw) return
  try {
    const data = JSON.parse(atob(String(raw)))
    const patch = {}
    if (data.method) patch.method = String(data.method).toUpperCase()
    if (data.path)   patch.path = String(data.path)
    if (data.headers && typeof data.headers === 'object') {
      patch.headers = Object.entries(data.headers).map(([key, value]) => ({ key, value: String(value) }))
    }
    if (data.body !== undefined) {
      patch.body = typeof data.body === 'string' ? data.body : JSON.stringify(data.body, null, 2)
    }
    testbench.setRequest(fnId.value, patch)
    router.replace({ name: 'function-test', params: { name: props.name } })
  } catch {
    /* ignore malformed prefill */
  }
}

// Waits on fnId as well as the param, because the store keys the request by it
// and loadRouteData resolves it asynchronously. The name guard stops a cached
// Editor claiming another view's query; `flush: 'post'` runs after that same
// load's pre-flush resetEditorState.
watch(
  () => [route.query.prefill, fnId.value],
  () => {
    if (route.query.prefill && fnId.value && route.params.name === props.name) applyPrefillFromQuery()
  },
  { immediate: true, flush: 'post' },
)

// Re-roll the auto-generated function name. No-op once the function
// has been deployed (the name is the routing identity at that point).
const rerollName = () => {
  if (isEditing.value || fnId.value) return
  form.value.name = generateFunctionName()
}

// activeVersionDeploymentId: the deployment_id of the row marked active
// in the version list. Used to pre-fill the "from" side of every Compare
// link in the Versions modal (operators almost always want "this old
// version vs what's running now").
const activeVersionDeploymentId = computed(() => {
  const a = versions.value.find((v) => v.is_active)
  return a?.deployment_id || null
})

// loadVersions builds the Versions card data from the deployments table.
// We deduplicate by code_hash (keep the most recent succeeded deployment
// for each unique hash) so a redeploy of identical content doesn't pad
// the list. The rollback button uses deployment_id, which disambiguates
// when the same hash was deployed twice via different code archives.
const loadVersions = async (fn) => {
  try {
    const res = await apiClient.get(`/functions/${fn.id}/deployments?limit=50`)
    const deps = res.data.deployments || []
    const seen = new Set()
    versions.value = deps
      .filter((d) => d.status === 'succeeded' && d.code_hash)
      .filter((d) => {
        if (seen.has(d.code_hash)) return false
        seen.add(d.code_hash)
        return true
      })
      .map((d) => ({
        deployment_id: d.id,
        version: d.version,
        code_hash: d.code_hash,
        short_hash: d.code_hash.slice(0, 12),
        created_at: d.finished_at || d.submitted_at,
        is_active: d.code_hash === fn.code_hash,
      }))
  } catch (e) {
    console.warn('failed to load versions', e)
  }
}

// loadVersions ran on mount, on window focus and after a rollback, but not
// after a deploy, which is why Config -> Versions lagged the thing you just did.
const refreshVersions = async () => {
  if (!fnId.value) return
  try {
    const res = await apiClient.get('/functions')
    const fn = (res.data.functions || []).find((f) => f.id === fnId.value)
    if (fn) await loadVersions(fn)
  } catch {
    /* the badge can lag; the deployment itself already landed */
  }
}

// Close any in-flight SSE stream when the user leaves the page so we don't
// keep a phantom connection open (and so the next page-load gets a fresh
// view of build state).
onBeforeUnmount(() => {
  if (abortActiveStream) abortActiveStream()
})

// Top-level deploy entry. For brand-new functions we open the
// "Name & deploy" modal first so the user gets a focused prompt for the
// name + final config; once they confirm, runDeploy() does the actual
// upload. Existing functions skip straight to runDeploy().
const deployFunction = async () => {
  if (!code.value) {
    confirmStore.notify({ title: 'Missing code', message: 'Write a handler before deploying.' })
    return
  }
  if (!isEditing.value && !fnId.value && !form.value.name.trim()) {
    modals.value.firstDeploy = true
    // Focus the name field on the next tick — Modal teleports to body so
    // it isn't in the document until v-if flips.
    setTimeout(() => firstDeployNameInput.value?.focus(), 50)
    return
  }
  await runDeploy()
}

const confirmFirstDeploy = async () => {
  if (!form.value.name.trim()) return
  modals.value.firstDeploy = false
  await runDeploy()
}

const runDeploy = async () => {
  if (!form.value.name || !code.value) {
    confirmStore.notify({ title: 'Missing fields', message: 'Please provide a function name and code.' })
    return
  }

  // The build runs headless. Most finish in well under a second, and a modal
  // that opens and closes inside that window is a flash, not information --
  // so Deploy carries its own progress and only a failure raises the log.
  const epoch = ++buildEpoch
  deploying.value = true
  buildError.value = ''
  lastBuild.value = null
  buildLogs.value = ['Starting deployment...']

  try {
    // Build env_vars map from the envVars array.
    const envVarsMap = {}
    for (const { key, value } of envVars.value) {
      if (key.trim()) envVarsMap[key.trim()] = value
    }

    // Step 1: Create or update function config.
    if (!fnId.value) {
      // New function — create it.
      try {
        const createRes = await apiClient.post('/functions', {
          name: form.value.name,
          description: form.value.description || '',
          runtime: form.value.runtime,
          memory_mb: form.value.memory_mb,
          cpus: form.value.cpus,
          env_vars: envVarsMap,
          network_mode: form.value.network_mode,
          max_concurrency: form.value.max_concurrency || 0,
          concurrency_policy: form.value.concurrency_policy || 'queue',
          auth_mode: form.value.auth_mode || 'none',
          rate_limit_per_min: form.value.rate_limit_per_min || 0,
        })
        fnId.value = createRes.data.id
        buildLogs.value.push(`Created function: ${fnId.value}`)
      } catch (err) {
        if (err.response?.status === 409) {
          const listRes = await apiClient.get('/functions')
          const fn = (listRes.data.functions || []).find(f => f.name === form.value.name)
          if (fn) {
            fnId.value = fn.id
            buildLogs.value.push(`Function already exists: ${fnId.value}`)
          } else {
            throw new Error('Function name conflict but not found in list', { cause: err })
          }
        } else {
          throw err
        }
      }
    } else {
      // Existing function — update config (memory, cpus, env_vars,
      // network_mode) so changes take effect. The backend drains the warm
      // pool when any of these change so the next invoke respawns with
      // the new config.
      await apiClient.put(`/functions/${fnId.value}`, {
        description: form.value.description || '',
        memory_mb: form.value.memory_mb,
        cpus: form.value.cpus,
        env_vars: envVarsMap,
        network_mode: form.value.network_mode,
        max_concurrency: form.value.max_concurrency || 0,
        concurrency_policy: form.value.concurrency_policy || 'queue',
        auth_mode: form.value.auth_mode || 'none',
        rate_limit_per_min: form.value.rate_limit_per_min || 0,
      })
      buildLogs.value.push('Updated function config')
    }

    // Step 1.5: Flush any secrets queued before the function existed.
    // For first-time deploys this is the moment they actually persist.
    await flushPendingSecrets()

    // Step 2: Submit code (async build pipeline returns 202 + deployment_id).
    buildLogs.value.push('Submitting build...')
    const deployRes = await apiClient.post(`/functions/${fnId.value}/deploy-inline`, {
      code: code.value,
      filename: fileName.value,
      dependencies: dependencyText.value || '',
      ...(Object.keys(deployExtras.value).length
        ? { extras: deployExtras.value }
        : {}),
    })

    const depId = deployRes.data.deployment_id
    // The editor may have been re-scoped while the upload was in flight.
    if (epoch !== buildEpoch) return
    if (!depId) {
      // Legacy synchronous response — older backend without async pipeline.
      buildLogs.value.push(`Deployed! Hash: ${deployRes.data.code_hash || 'unknown'}`)
      deployedThisSession.value = true
      lastBuild.value = { version: null, durationMs: null }
      deploying.value = false
      return
    }

    // Step 3: Stream the build via SSE. Test button stays disabled until
    // the stream emits `succeeded`. Deploying flag stays true so the
    // Deploy button keeps its loading state.
    buildLogs.value.push(`Build queued (${depId})`)
    await streamBuild(depId, epoch)
  } catch (err) {
    if (epoch !== buildEpoch) return
    buildError.value = err.response?.data?.error?.message || err.message || 'Deployment failed'
    buildLogs.value.push(`Error: ${buildError.value}`)
    buildModalOpen.value = true
    deploying.value = false
  }
}

// streamBuild opens an SSE connection to /deployments/{id}/stream and
// resolves when the build hits a terminal state. Build log lines are
// pushed into buildLogs (capped to last 500). The SSE stream emits:
//   event: log           — { seq, stream, line, ts }
//   event: succeeded     — final deployment row
//   event: failed        — final deployment row (with error_message)
//   event: error         — transport/server error; we fall back to polling
const streamBuild = (depId, epoch = buildEpoch) => new Promise((resolve) => {
  if (abortActiveStream) abortActiveStream()
  const es = new EventSource(`/api/v1/deployments/${depId}/stream`)
  const mine = () => epoch === buildEpoch

  // settled becomes true once we've seen a terminal `succeeded`/`failed`
  // event. After that the server closes the stream — which fires `onerror`
  // with readyState=CLOSED. That's a normal termination, not a failure.
  let settled = false
  const release = () => {
    settled = true
    try { es.close() } catch {}
    abortActiveStream = null
  }
  // Abandon the build without reporting it. The caller has moved on; the
  // deployment itself is unaffected and the server still records its outcome.
  abortActiveStream = () => {
    if (settled) return
    release()
    resolve()
  }
  const finish = (ok, payload) => {
    if (settled) return
    release()
    if (!mine()) { resolve(); return }
    deploying.value = false
    if (ok) {
      deployedThisSession.value = true
      lastDeployAt.value = new Date().toISOString()
      // The button no longer promises a new version, so the result names the
      // one it made; a response without `version` still reports its duration.
      const ms = payload?.duration_ms ?? '?'
      lastBuild.value = { version: payload?.version || null, durationMs: ms }
      buildLogs.value.push(payload?.version
        ? `✓ v${payload.version} live in ${ms}ms`
        : `✓ Build succeeded in ${ms}ms`)
      refreshVersions()
      // A finished build is done with the screen: the full log stays reachable
      // on the Deployments page and the version lands in the file header.
      buildModalOpen.value = false
      justDeployed.value = true
      clearTimeout(deployedTimer)
      deployedTimer = setTimeout(() => { justDeployed.value = false }, 2500)
    } else {
      const msg = payload?.error_message || 'build failed (see logs)'
      buildError.value = msg
      buildLogs.value.push(`✗ Build failed: ${msg}`)
      // A dismissed build that never went live has nowhere else to report it:
      // the strip speaks for the last run, not the last build.
      buildModalOpen.value = true
    }
    resolve()
  }

  es.addEventListener('log', (e) => {
    if (!mine()) return
    try {
      const line = JSON.parse(e.data)
      const text = `[${line.stream || 'log'}] ${line.line}`
      buildLogs.value.push(text)
      if (buildLogs.value.length > 500) {
        buildLogs.value.splice(0, buildLogs.value.length - 500)
      }
    } catch {}
  })
  es.addEventListener('succeeded', (e) => {
    try { finish(true, JSON.parse(e.data)) } catch { finish(true) }
  })
  es.addEventListener('failed', (e) => {
    try { finish(false, JSON.parse(e.data)) } catch { finish(false) }
  })
  es.onerror = () => {
    // If we've already seen a terminal event, the server-initiated close
    // is normal — don't paint it as a failure.
    if (settled) return
    if (es.readyState === EventSource.CLOSED) {
      // Stream closed without a terminal event. Fall back to a single
      // poll of /deployments/<id> to capture the real outcome instead of
      // assuming the worst.
      fetch(`/api/v1/deployments/${depId}`, { credentials: 'include' })
        .then((r) => r.ok ? r.json() : null)
        .then((d) => {
          if (d && d.status === 'succeeded') return finish(true, d)
          if (d && d.status === 'failed')    return finish(false, d)
          finish(false, { error_message: 'stream closed before terminal state' })
        })
        .catch(() => finish(false, { error_message: 'stream closed; deployment status unknown' }))
    }
  }
})

// The strip reads one run object, so its dot, status and duration cannot
// disagree the way three independently-cleared refs could.
const lastRun = computed(() => (fnId.value ? testbench.latestRun(fnId.value) : null))
// The store calls only 5xx `failed`, so painting everything below it green put
// a success dot beside a 404. Three tones: answered, refused, broke.
// A 4xx the handler meant to send is not the same as a 5xx it did not, so the
// status code is drawn in three tones rather than pass/fail.
const lastRunTone = computed(() => {
  const run = lastRun.value
  if (!run) return 'text-foreground-muted'
  if (run.failed) return 'text-danger-fg'
  return Number(run.status) >= 400 ? 'text-warning-fg' : 'text-success-fg'
})
// A transport failure has no duration to report, so the status stands alone
// rather than reading "Error · ms".
const lastRunMeta = computed(() => {
  const run = lastRun.value
  if (!run) return ''
  return run.durationMs ? `${run.status} · ${run.durationMs}ms` : run.status
})
// A deploy leaves the run history alone, so the strip says when the result on
// screen describes code that is no longer the live one.
const lastRunStale = computed(
  () => !!(lastRun.value && lastDeployAt.value && lastRun.value.at < lastDeployAt.value),
)

// Both log paths, in one list: console.log/print arrive as stderr, orva.log.*
// as parsed entries that never reached the editor at all. The level prefix is
// what keeps a structured line distinguishable from something the handler printed.
const runLines = computed(() => {
  const run = lastRun.value
  if (!run) return []
  return [
    ...(run.stderr || []).filter((l) => l.trim()),
    ...(run.structured || []).map((e) => `${(e.level || 'info').toLowerCase()}: ${e.message || ''}`),
  ]
})

// A stack frame names the runtime, not the bug. Node prints the message first
// and Python last, so skipping frames from the end lands on the message in both;
// taking the last line outright showed `at process.processTicksAndRejections`.
const isStackFrame = (l) => /^\s*(?:at\s|File ")/.test(l)

// A pretty-printed body's first line is `{`, which is what the strip showed for
// every successful JSON response. Collapse the whole value onto the one line.
const oneLine = (text) => (text || '').split('\n').map((l) => l.trim()).filter(Boolean).join(' ')

// A failure wants the line that explains it; a success wants the answer, and
// falls back to a log line so a 204 that only called orva.log.* still says something.
const resultExcerpt = computed(() => {
  const run = lastRun.value
  if (!run) return ''
  const lines = runLines.value
  const spoken = [...lines].reverse().find((l) => !isStackFrame(l)) || lines[lines.length - 1] || ''
  const body = oneLine(run.body)
  const err = oneLine(run.error)
  return run.failed ? (spoken || err || body) : (body || err || spoken)
})

const runTest = async () => {
  if (!fnId.value || !canTest.value || testbench.invoking) return
  await testbench.invoke(fnId.value)
}

// Debugs the buffer, not the deployed version: `code` is what the operator is
// editing, and uncommitted edits are the thing they want looked at. The request
// and both log streams come from the run in the store. Nothing goes over the
// network from here.
const suggestFix = async () => {
  if (suggestingFix.value) return
  const run = lastRun.value
  if (!run) return
  suggestingFix.value = true
  try {
    const req = testbench.requestFor(fnId.value)
    const headersObj = {}
    for (const h of req.headers || []) {
      if (h?.key && h.key.trim()) headersObj[h.key.trim()] = h.value || ''
    }
    // orva.log.* lines are parsed out of stderr server-side, so they have to be
    // folded back in or the prompt gets a traceback with the context missing.
    const structured = (run.structured || []).map(
      (e) => `[${e.level || 'info'}] ${e.message || ''}`,
    )
    const sc = /^\d+$/.test(run.status) ? Number(run.status) : run.status
    const ok = await copyFixSuggestionToClipboard({
      source: code.value || '',
      runtime: form.value.runtime || '',
      stderr: [...(run.stderr || []), ...structured].join('\n'),
      requestPreview: {
        method: req.method || 'POST',
        path: req.path || '/',
        headers: headersObj,
        body: req.body || '',
      },
      errorMessage: run.error || '',
      statusCode: sc || '',
    })
    if (!ok) {
      confirmStore.notify({
        title: 'Copy failed',
        message: 'Could not write to the clipboard. Try again, or copy the stderr by hand.',
        danger: true,
      })
      return
    }
    // The clipboard is the handoff because it is the only one that exists:
    // stores/ai.js holds no draft and AI.vue fills its composer from a local ref.
    const open = await confirmStore.ask({
      title: 'Prompt copied',
      message: 'Paste it into Chat to debug this run inside Orva, or into whichever AI tool you prefer.',
      confirmLabel: 'Open Chat',
      cancelLabel: 'Stay here',
    })
    if (open) router.push({ name: 'ai' })
  } finally {
    suggestingFix.value = false
  }
}

// The build's counterpart to suggestFix. Same clipboard handoff, different
// artefacts: there is no request and no run, so the builder's log stands in for
// the stderr a failed invocation would have produced.
const suggestBuildFix = async () => {
  if (suggestingBuildFix.value || !buildError.value) return
  suggestingBuildFix.value = true
  try {
    const ok = await copyBuildFixToClipboard({
      source: code.value || '',
      runtime: form.value.runtime || '',
      dependencies: dependencyText.value || '',
      buildLog: buildLogs.value.join('\n'),
      errorMessage: buildError.value,
    })
    if (!ok) {
      confirmStore.notify({
        title: 'Copy failed',
        message: 'Could not write to the clipboard. Try again, or copy the build log by hand.',
        danger: true,
      })
      return
    }
    const open = await confirmStore.ask({
      title: 'Prompt copied',
      message: 'Paste it into Chat to fix the build inside Orva, or into whichever AI tool you prefer.',
      confirmLabel: 'Open Chat',
      cancelLabel: 'Stay here',
    })
    if (open) {
      buildModalOpen.value = false
      router.push({ name: 'ai' })
    }
  } finally {
    suggestingBuildFix.value = false
  }
}

const rollingBack = ref(false)
const rollbackToVersion = async (v) => {
  if (!fnId.value || !v?.deployment_id || rollingBack.value) return

  // Pull the target deployment's snapshot + the current function record
  // so we can show the operator exactly what will change before they
  // confirm. Best-effort: if either fetch fails, fall through to a plain
  // confirm — the rollback itself still works.
  let diffMessage = `Code hash ${v.short_hash}. Your current version stays in the history.`
  try {
    const [depRes, listRes] = await Promise.all([
      apiClient.get(`/deployments/${v.deployment_id}`),
      apiClient.get('/functions'),
    ])
    const snap = depRes.data?.snapshot
    const fn = (listRes.data.functions || []).find((f) => f.id === fnId.value)
    if (snap && fn) {
      const lines = describeSnapshotDiff(fn, snap)
      if (lines.length) {
        diffMessage = `Rolling back to v${v.version} (code ${v.short_hash}) will also change:\n\n${lines.join('\n')}\n\nSecrets keep their current values; they aren't part of the rollback.`
      } else {
        diffMessage = `Rolling back to v${v.version} (code ${v.short_hash}). Settings and env are already identical, so only the code changes.`
      }
    }
  } catch {
    // fall through to default message
  }

  const ok = await confirmStore.ask({
    title: `Restore v${v.version}?`,
    message: diffMessage,
    confirmLabel: 'Rollback',
  })
  if (!ok) return
  rollingBack.value = true
  try {
    await rollbackFunction(fnId.value, { deployment_id: v.deployment_id })
    // Re-pull function metadata, source, AND versions so the Active
    // pill moves AND the editor buffer reflects the rolled-back code.
    // Without the reloadSource() call, CodeMirror keeps showing the
    // pre-rollback content and the operator has to hard-refresh —
    // that was the original bug report ("I rolled back, navigated
    // back to the function, still saw the new code").
    const listRes = await apiClient.get('/functions')
    const fn = (listRes.data.functions || []).find((f) => f.id === fnId.value)
    if (fn) {
      // Re-hydrate the form too — rollback restores env_vars,
      // memory, network_mode, etc. from the deployment snapshot.
      form.value.runtime = fn.runtime
      // The file the operator authored. Without it the editor re-deployed every
      // function as handler.js, which silently dropped a TypeScript function's
      // .ts source on the next save.
      loadedEntrypoint.value = fn.entrypoint || ''
      form.value.memory_mb = fn.memory_mb
      form.value.cpus = fn.cpus
      form.value.network_mode = fn.network_mode || 'none'
      form.value.max_concurrency = fn.max_concurrency || 0
      form.value.concurrency_policy = fn.concurrency_policy || 'queue'
      form.value.auth_mode = fn.auth_mode || 'none'
      form.value.rate_limit_per_min = fn.rate_limit_per_min || 0
      form.value.description = fn.description || ''
      if (fn.env_vars && Object.keys(fn.env_vars).length > 0) {
        envVars.value = Object.entries(fn.env_vars).map(([key, value]) => ({ key, value }))
      } else {
        envVars.value = []
      }
      await reloadSource(fn)
      await loadVersions(fn)
    }
  } catch (e) {
    const code = e.response?.data?.error?.code || ''
    const msg = e.response?.data?.error?.message || e.message || 'Rollback failed'
    if (code === 'VERSION_GCD') {
      confirmStore.notify({ title: 'Version unavailable', message: `This version has been garbage-collected and can no longer be restored.\n\n${msg}`, danger: true })
    } else {
      confirmStore.notify({ title: 'Rollback failed', message: msg, danger: true })
    }
  } finally {
    rollingBack.value = false
  }
}

const loadSecrets = async () => {
  if (!fnId.value) return
  try {
    const res = await apiClient.get(`/functions/${fnId.value}/secrets`)
    secrets.value = (res.data.secrets || []).map((name) => ({ id: name, name }))
  } catch (err) {
    console.error('Failed to load secrets', err)
  }
}

const saveSecret = async () => {
  const name = secretForm.value.name.trim()
  if (!name) return
  // Pre-deploy: queue locally; flush during deployFunction.
  if (!fnId.value) {
    // Replace any prior pending entry with the same name.
    pendingSecrets.value = pendingSecrets.value.filter((s) => s.name !== name)
    pendingSecrets.value.push({ name, value: secretForm.value.value })
    secretForm.value.name = ''
    secretForm.value.value = ''
    return
  }
  // Existing function: save through the API.
  try {
    await apiClient.post(`/functions/${fnId.value}/secrets`, {
      key: name,
      value: secretForm.value.value,
    })
    secretForm.value.name = ''
    secretForm.value.value = ''
    await loadSecrets()
  } catch (err) {
    confirmStore.notify({
      title: 'Save failed',
      message: err.response?.data?.error?.message || 'Failed to save secret',
      danger: true,
    })
  }
}

const removePendingSecret = (idx) => {
  pendingSecrets.value.splice(idx, 1)
}

const removeSecret = async (key) => {
  if (!fnId.value) return
  const ok = await confirmStore.ask({
    title: 'Delete secret?',
    message: `"${key}" will be removed from this function's environment.`,
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok) return
  try {
    await apiClient.delete(`/functions/${fnId.value}/secrets/${encodeURIComponent(key)}`)
    await loadSecrets()
  } catch (err) {
    confirmStore.notify({
      title: 'Delete failed',
      message: err.response?.data?.error?.message || 'Failed to delete secret',
      danger: true,
    })
  }
}

// flushPendingSecrets POSTs queued secrets after the function row exists.
// Called from runDeploy between the create step and deploy-inline.
const flushPendingSecrets = async () => {
  if (!fnId.value || !pendingSecrets.value.length) return
  for (const sec of pendingSecrets.value) {
    try {
      await apiClient.post(`/functions/${fnId.value}/secrets`, {
        key: sec.name, value: sec.value,
      })
      buildLogs.value.push(`Saved secret: ${sec.name}`)
    } catch (err) {
      const msg = err.response?.data?.error?.message || err.message
      buildLogs.value.push(`Failed to save secret ${sec.name}: ${msg}`)
    }
  }
  pendingSecrets.value = []
  await loadSecrets()
}

const resetForm = async () => {
  const ok = await confirmStore.ask({
    title: 'Reset editor?',
    message: 'Unsaved changes in the editor will be discarded.',
    confirmLabel: 'Reset',
    danger: true,
  })
  if (!ok) return
  form.value.name = ''
  fnId.value = ''
  code.value = ''
  deployedThisSession.value = false
  buildError.value = ''
  lastBuild.value = null
  setRuntime('python')
}
</script>

<style scoped>
/* Compact panel-trigger button used in the editor's top action bar.
   Visible height ~28 px keeps the toolbar dense for desktop operators; on
   coarse pointers the real box grows to the WCAG 2.5.5 floor (see the
   media query at the bottom of this block).

   This used to be a ::before overlay inset -8px vertically. That is the
   exact pattern style.css removed: the toolbar is flex-wrap, so once the
   row passes a phone's width the buttons wrap onto two rows separated by
   gap-2 (7.6 px) and each row-2 overlay covered the row-1 buttons above
   it — winning the hit test on paint order and swallowing taps meant for
   Config. Grow the box, never the shadow of it. */
.panel-btn {
  display: inline-flex;
  align-items: center;
  gap: 0.375rem;
  padding: 0 0.625rem;
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border);
  background-color: var(--color-surface-hover);
  color: var(--color-foreground-muted);
  /* 0.75rem, not 11px: Button size="sm" sits beside these in the same row at
     the same 30.4px height, and an arbitrary 11px put two type sizes in one
     toolbar. */
  font-size: 0.75rem;
  font-weight: 500;
  white-space: nowrap;
  transition: color 150ms ease, background-color 150ms ease, border-color 150ms ease;
  height: 30.4px;
}
.panel-btn:hover {
  color: var(--color-foreground);
  border-color: var(--color-foreground-muted);
}
.panel-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

/* Menu-row inside Config / Bindings dropdowns. Compact, single-line,
   leading icon + label + secondary hint. Hover lifts the background to
   surface-hover; no shadow, no glow, flat-by-default per DESIGN.md.

   min-height is 44 px so the touch target meets WCAG 2.5.5; visible
   font stays at 12 px for the operator-grade density the rest of the
   editor toolbar uses. The vertical padding scales accordingly. */
.menu-item {
  display: flex;
  width: 100%;
  align-items: center;
  gap: 0.5rem;
  min-height: 44px;
  padding: 0.5rem 0.75rem;
  font-size: 12px;
  color: var(--color-foreground);
  background-color: transparent;
  border: 0;
  text-align: left;
  cursor: pointer;
  transition: background-color 120ms ease;
  border-radius: var(--radius-md);
}
.menu-item:hover,
.menu-item:focus-visible {
  background-color: var(--color-surface-hover);
  outline: none;
}
.menu-item + .menu-item {
  border-top: 1px solid var(--color-border);
}

/* Compact Run button in the editor card's result strip. Smaller than
   panel-btn but distinguishable via the primary tint so the user spots the
   action immediately. */
.run-btn {
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  padding: 0 0.55rem;
  border-radius: var(--radius-md);
  border: 1px solid color-mix(in srgb, var(--color-primary) 55%, transparent);
  background: color-mix(in srgb, var(--color-primary) 18%, transparent);
  color: var(--color-foreground);
  font-size: 11px;
  font-weight: 500;
  cursor: pointer;
  transition: background 120ms ease, border-color 120ms ease, color 120ms ease;
  height: 26.6px;
}
.run-btn:hover:not(:disabled) {
  background: color-mix(in srgb, var(--color-primary) 32%, transparent);
  border-color: var(--color-primary);
  color: var(--color-foreground-strong);
}
.run-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

/* Coarse-pointer floors for the controls this view styles itself. The
   touch-expand-* helpers cover everything that goes through Button /
   IconButton; these two are scoped-CSS controls that never see them.
   Desktop (fine pointers) is untouched. */
@media (pointer: coarse) {
  /* The sm rung, then buy the target: a 44px BOX made Config and Bindings
     visibly taller than the Test / Reset / Deploy trio beside them, which is
     the "floor is not the size" mistake DESIGN.md names. */
  .panel-btn {
    height: auto;
    min-height: 36px;
    position: relative;
  }
  .panel-btn::after {
    content: '';
    position: absolute;
    top: -4px;
    bottom: -4px;
    left: 0;
    right: 0;
  }

  /* Run sat at 44px inside a 44px bar: a bordered control exactly as tall as
     the strip containing it, touching both edges, which is what made it read
     as a slab rather than a button. It takes the sm rung (36px) and buys the
     rest of its target the same bounded way the shared helpers do. */
  .run-btn {
    height: auto;
    min-height: 36px;
    position: relative;
  }
  .run-btn::after {
    content: '';
    position: absolute;
    top: -4px;
    bottom: -4px;
    left: 0;
    right: 0;
  }
}
.run-spinner {
  width: 0.65rem;
  height: 0.65rem;
  border-radius: 999px;
  border: 1.5px solid currentColor;
  border-top-color: transparent;
  animation: run-spin 700ms linear infinite;
}
@keyframes run-spin {
  to { transform: rotate(360deg); }
}


/* The mat around the always-dark code surface. Padding only in day, where the
   contrast between paper and editor is what needs softening; in night the mat
   collapses to nothing because there is nothing to soften. */
.orva-editor-mat {
  background: var(--color-surface);
}
:root[data-theme='day'] .orva-editor-mat {
  padding: 8px;
  border-top: 1px solid var(--color-border);
}
</style>
