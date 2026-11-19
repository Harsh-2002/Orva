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
    <div class="flex flex-col sm:flex-row sm:flex-wrap sm:items-center gap-x-2 gap-y-3 pb-3 border-b border-border">
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
          class="p-1 rounded-md text-foreground-muted hover:text-foreground-strong hover:bg-surface-hover transition-colors shrink-0 touch-expand-iconbtn"
          title="Re-roll a fresh name"
          aria-label="Re-roll a fresh name"
          @click="rerollName"
        >
          <Shuffle class="w-3.5 h-3.5" />
        </button>
        <span class="text-[11px] text-foreground-muted font-medium tracking-tight shrink-0">{{ runtimeLabel(form.runtime) }}</span>
        <!-- A new function has nothing to read yet, so it has no switch and is
             always in edit mode. -->
        <SegmentedControl
          v-if="isEditing"
          :model-value="editorMode"
          :options="modeOptions"
          aria-label="Mode"
          size="sm"
          class="shrink-0"
          @update:model-value="requestMode"
        />
        <span
          class="sr-only"
          aria-live="polite"
        >{{ modeAnnouncement }}</span>
      </div>
      <!-- Wrapper for the dropdowns + actions on mobile so they sit on
           one row (or wrap among themselves). On sm+ they merge back
           into the parent flex-wrap. -->
      <div class="flex flex-wrap items-center gap-x-2 gap-y-3 sm:contents">
        <!-- Config menu — Settings / Env / Deps / Secrets / Versions.
           Versions is hidden until isEditing && versions.length so the
           menu stays small for new-function flows. -->
        <div
          v-if="!viewMode || versions.length"
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
              v-if="!viewMode"
              class="menu-item"
              role="menuitem"
              @click="openMenuItem('settings')"
            >
              <Settings2 class="w-3.5 h-3.5" />
              <span class="flex-1 text-left">Settings</span>
              <span class="text-[10px] text-foreground-muted shrink-0 whitespace-nowrap">runtime · limits</span>
            </button>
            <button
              v-if="!viewMode"
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
              v-if="!viewMode"
              class="menu-item"
              role="menuitem"
              @click="openMenuItem('deps')"
            >
              <Package class="w-3.5 h-3.5" />
              <span class="flex-1 text-left">Deps</span>
              <span class="text-[10px] text-foreground-muted shrink-0 whitespace-nowrap">package · requirements</span>
            </button>
            <button
              v-if="!viewMode"
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

        <!-- Bindings menu. Function-specific actions require a saved function. -->
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
              v-if="iam.available && isEditing && fnId"
              class="menu-item"
              role="menuitem"
              @click="navMenu({ name: 'function-permissions', params: { functionId: fnId } })"
            >
              <ShieldCheck class="w-3.5 h-3.5" />
              <span class="flex-1 text-left truncate whitespace-nowrap">Permissions</span>
              <span class="text-[10px] text-foreground-muted shrink-0 whitespace-nowrap">function access</span>
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
          v-if="!viewMode"
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
          v-if="!viewMode"
          size="sm"
          class="min-w-[7.25rem]"
          :loading="deploying"
          :disabled="installing || deployBlockedByConfiguration || !!rollingBack"
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
          {{ deploying ? 'Building' : justDeployed && !lastBuild?.managed ? 'Deployed' : 'Deploy' }}
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
        <div class="text-xs font-mono text-foreground-muted flex items-center gap-2 min-w-0">
          <FileCode class="w-3 h-3 shrink-0" />
          <span class="text-foreground-strong truncate">{{ fileName }}</span>
          <span
            v-if="templateId"
            class="text-foreground-muted hidden sm:inline"
          >· template: {{ templateId }}</span>
        </div>
        <div class="flex items-center gap-2 sm:gap-3 shrink-0">
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
          <span
            v-else-if="lastBuild?.version"
            class="text-[10px] text-success-fg font-medium"
          >v{{ lastBuild.version }} {{ lastBuild.rollback ? 'restored' : lastBuild.managed ? 'built' : 'live' }}</span>
          <span
            v-if="viewMode"
            class="text-[10px] text-foreground-muted font-medium"
          >Read-only</span>
          <span class="text-[10px] text-foreground-muted font-mono hidden md:inline">
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
          :filename="fileName"
          :read-only="viewMode"
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
          :title="canTest ? 'Replay the current test request' : configurationInstallRequired ? 'Resolve the pending configuration first' : awaitingInstall ? 'Install the build first' : 'Deploy first'"
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
              class="touch-expand-sm inline-flex h-7 items-center rounded-md px-2.5 text-xs text-link hover:text-foreground-strong transition-colors shrink-0"
              @click="buildModalOpen = true"
            >
              View log
            </button>
          </template>
          <span
            v-else-if="configurationReceipt"
            class="text-xs text-foreground"
          >{{ configurationHeadline }}</span>
          <span
            v-else-if="lastBuild?.managed"
            class="text-xs text-foreground"
          >{{ managedBuildHeadline(lastBuild) }}</span>
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
        <Button
          v-if="awaitingInstall && !viewMode && iam.mode === 'managed'"
          variant="secondary"
          size="sm"
          :loading="installing"
          :disabled="deploying || blockedByConfiguration || !!rollingBack"
          @click="installManagedBuild"
        >
          {{ lastBuild.rollback ? 'Install restored version' : 'Install build' }}
        </Button>
        <Button
          v-if="canOfferConfigurationInstall"
          variant="secondary"
          size="sm"
          :loading="secretBusy"
          @click="installManagedConfiguration"
        >
          Install configuration
        </Button>
        <router-link
          v-if="reviewOwner"
          :to="{ name: 'function-permissions', params: { functionId: reviewOwner } }"
          class="touch-expand-sm inline-flex h-7 items-center rounded-md px-2.5 text-xs text-link hover:text-foreground-strong transition-colors shrink-0"
        >
          Review permissions →
        </router-link>
        <button
          v-if="lastRun?.failed"
          type="button"
          class="touch-expand-sm inline-flex h-7 items-center gap-1 rounded-md px-2.5 text-xs text-foreground-muted hover:text-foreground-strong hover:bg-surface-hover transition-colors shrink-0 disabled:opacity-50"
          :disabled="suggestingFix"
          title="Build a debug prompt from the source, the request and this run's stderr"
          @click="suggestFix"
        >
          <Sparkles class="w-3 h-3" />
          Suggest fix
        </button>
        <router-link
          v-if="fnId && form.name && !awaitingInstall && !configurationInstallRequired"
          :to="{ name: 'function-test', params: { name: form.name } }"
          class="touch-expand-sm inline-flex h-7 items-center rounded-md px-2.5 text-xs text-link hover:text-foreground-strong transition-colors shrink-0"
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
          v-if="buildState === 'ok' && fnId && form.name && !lastBuild?.managed"
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

        <div class="border-t border-border pt-4">
          <label class="flex items-start gap-3 cursor-pointer select-none">
            <input
              v-model="form.sdk"
              id="fn-sdk"
              type="checkbox"
              :disabled="viewMode"
              class="mt-0.5 w-4 h-4 rounded border-border bg-background focus:outline-none focus:ring-1 focus:ring-focus-ring"
            >
            <div class="min-w-0">
              <div class="text-sm font-medium text-foreground-strong flex items-center gap-2">
                <Plug class="w-4 h-4 text-foreground-muted" /> Allow Orva SDK
              </div>
              <div class="text-xs text-foreground-muted mt-1 leading-snug">
                Off by default. Enables the function's Orva SDK for KV, function-to-function calls, jobs, crons, and spans. This does not allow outbound network access.
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
          No secrets yet. Add a key-value pair below.<span v-if="!fnId && iam.mode === 'legacy'"> They'll be saved when you deploy.</span>
          <span v-else-if="newManagedUninstalled"> Install a build before saving secrets.</span>
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
            :disabled="managedSecretBlocked"
            @click="removeSecret(sec.id)"
          >
            <Trash2 class="w-3.5 h-3.5" />
          </button>
        </div>

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
            :disabled="managedSecretBlocked"
            @click="saveSecret"
          >
            <ShieldCheck class="w-4 h-4" /> {{ newManagedUninstalled ? 'Keep secret locally' : fnId ? 'Save secret' : 'Queue secret for deploy' }}
          </Button>
          <p
            v-if="configurationInstallRequired && !configurationReceipt.settings"
            role="status"
            class="text-xs text-foreground-muted"
          >
            {{ configurationReceipt.expected ? 'Close Secrets to install the saved configuration.' : secretBusy ? 'Saving secret change…' : 'Reload before continuing.' }}
          </p>
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
            >{{ iam.mode === 'managed' ? 'Current' : 'Active' }}</span>
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
              v-if="!viewMode"
              :disabled="!!rollingBack || deploying || installing || blockedByConfiguration"
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
import { ref, shallowRef, shallowReactive, computed, defineAsyncComponent, h, nextTick, onActivated, onBeforeUnmount, onDeactivated, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { FileCode, UploadCloud, Play, Layers, KeyRound, ShieldCheck, RotateCcw, Copy, Check, BookOpen, ChevronDown, Settings2, Variable, Package, X, Trash2, Terminal, Globe, Lock, Shuffle, Database, Sparkles, Webhook, Plug, GitCompare, FlaskConical, Eye, Pencil } from '@lucide/vue'
import Button from '@/components/common/Button.vue'
import FilterSelect from '@/components/common/FilterSelect.vue'
import Input from '@/components/common/Input.vue'
import Modal from '@/components/common/Modal.vue'
import SegmentedControl from '@/components/common/SegmentedControl.vue'
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
import { useManagedIAMStore } from '@/stores/managedIAM.js'
import { useTestbenchStore } from '@/stores/testbench'
import { describeSnapshotDiff } from '@/utils/rollbackDiff'
import { isCanonicalUuid } from '@/utils/permissionsModel'

// The function this Editor is scoped to; empty on /functions/new. A route prop,
// not useRoute(): Vue does not patch a keep-alived view's props, and an Editor
// that followed the live route reset the unsaved buffer on every navigation to
// a page without a :name.
// `mode` carries ?mode=edit the same way: '' or 'view' opens read-only.
const props = defineProps({
  name: { type: String, default: '' },
  mode: { type: String, default: '' },
})

const route = useRoute()
const router = useRouter()
const confirmStore = useConfirmStore()
const iam = useManagedIAMStore()
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
const initialExecutionSettings = {
  memory_mb: 64,
  cpus: 0.5,
  network_mode: 'none',          // 'none' | 'egress'
  sdk: false,
  max_concurrency: 0,             // 0 = unlimited
  concurrency_policy: 'queue',    // 'queue' | 'reject'
  auth_mode: 'none',              // 'none' | 'platform_key' | 'signed'
  rate_limit_per_min: 0,          // 0 = unlimited
}
const form = ref({
  name: '',
  description: '',
  runtime: 'python',
  ...initialExecutionSettings,
})
const fnId = ref('')  // backend function ID
const managedCreated = shallowRef(null)

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
const rollingBack = shallowRef(null)
const installingBuild = shallowRef(null)
const installing = computed(() => installingBuild.value !== null && installingBuild.value === lastBuild.value)
const awaitingInstall = computed(() => lastBuild.value?.managed && !lastBuild.value.installed)
const configurationPublication = shallowRef(null)
const secretOperations = shallowReactive(new Set())
const secretBusy = computed(() => secretOperations.has(fnId.value))
const configurationReceipt = computed(() => iam.mode === 'managed' && configurationPublication.value?.owner === fnId.value
  ? configurationPublication.value : null)
const configurationInstallRequired = computed(() => !!configurationReceipt.value && !configurationReceipt.value.installed)
const canOfferConfigurationInstall = computed(() => configurationReceipt.value?.expected
  && !configurationReceipt.value.buildUnverified && !deploying.value && !awaitingInstall.value && !viewMode.value)
const configurationHeadline = computed(() => {
  const receipt = configurationReceipt.value
  if (receipt?.installed) return 'Configuration installed. Permissions review required.'
  if (receipt?.settings) {
    if (receipt.buildUnverified) return 'Settings saved. Check deployment history; build status is unverified.'
    return receipt.expected ? 'Settings saved. Install configuration or retry the build.'
      : 'Configuration status unverified. Inspect the current configuration before continuing.'
  }
  if (receipt?.expected) return 'Secrets saved. Install required.'
  return secretBusy.value ? 'Saving secret change…' : 'Configuration status unverified. Reload to continue.'
})
const blockedByConfiguration = computed(() => secretBusy.value || configurationInstallRequired.value)
const deployBlockedByConfiguration = computed(() => secretBusy.value || (configurationInstallRequired.value
  && !(configurationReceipt.value.settings && configurationReceipt.value.expected)))
const managedSecretBlocked = computed(() => iam.mode === 'managed' && (blockedByConfiguration.value
  || deploying.value || installing.value || rollingBack.value || awaitingInstall.value))
const reviewOwner = computed(() => {
  if (iam.mode !== 'managed' || secretBusy.value || deploying.value || rollingBack.value) return ''
  if (configurationReceipt.value) return configurationReceipt.value.installed ? configurationReceipt.value.owner : ''
  return lastBuild.value?.managed && lastBuild.value.installed ? lastBuild.value.owner : ''
})
const newManagedUninstalled = computed(() => !isEditing.value && iam.mode === 'managed' && !deployedThisSession.value)
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
let editorEpoch = 0
let editorActive = true

const buildState = computed(() => {
  if (deploying.value) return 'running'
  if (buildError.value) return 'failed'
  return lastBuild.value ? 'ok' : 'idle'
})
const managedBuildHeadline = (build, duration = '') => {
  if (build.installed) return 'Installed. Permissions review required.'
  if (build.rollback) return 'Version restored. Install required.'
  return `Build succeeded${duration}. Install required.`
}
const buildHeadline = computed(() => {
  if (buildState.value === 'running') return 'Building'
  if (buildState.value === 'failed') return buildError.value
  const b = lastBuild.value
  if (!b) return 'No build has run in this session.'
  const ms = b.durationMs == null ? '' : ` in ${b.durationMs}ms`
  if (b.managed) return managedBuildHeadline(b, ms)
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

// View / Edit. An existing function opens in view so reading it cannot edit or
// deploy it by accident; a new function has nothing to read and is always edit.
// View is unavailable while a build runs: leaving Edit then would either discard
// the buffer being deployed or leave View showing code the build is replacing.
const modeOptions = computed(() => [
  { value: 'view', label: 'View', icon: Eye, disabled: deploying.value, title: deploying.value ? 'Available when the build finishes' : undefined },
  { value: 'edit', label: 'Edit', icon: Pencil },
])
const initialMode = () => (props.name && props.mode !== 'edit' ? 'view' : 'edit')
const editorMode = ref(initialMode())
const viewMode = computed(() => isEditing.value && editorMode.value === 'view')
const modeAnnouncement = ref('')

// What the loaded function looks like, for "does the switch need to ask?".
// Settings/env edits live in `form` until Deploy, so they count as unsaved too.
const snapshotForm = () => JSON.stringify({
  form: form.value,
  env: envVars.value.filter((p) => p.key.trim()).map((p) => [p.key, p.value]),
})
// A shallowRef that is replaced, never mutated: `isDirty` is a computed, and a
// plain object read inside it would never re-run when a deploy moved the baseline.
const emptyBaseline = () => ({ loaded: false, code: '', deps: '', form: '', formState: null })
const baseline = shallowRef(emptyBaseline())
const captureBaseline = () => ({
  loaded: true,
  code: code.value,
  deps: dependencyText.value,
  form: snapshotForm(),
  formState: { form: { ...form.value }, env: envVars.value.map((p) => ({ ...p })) },
})
const markFormClean = () => {
  const b = captureBaseline()
  baseline.value = { ...baseline.value, loaded: true, form: b.form, formState: b.formState }
}
const markSourceClean = () => { baseline.value = { ...baseline.value, code: code.value, deps: dependencyText.value } }
const isDirty = computed(() => baseline.value.loaded && (
  code.value !== baseline.value.code
  || dependencyText.value !== baseline.value.deps
  || snapshotForm() !== baseline.value.form
))
const discardChanges = () => {
  const b = baseline.value
  code.value = b.code
  dependencyText.value = b.deps
  if (b.formState) {
    form.value = { ...b.formState.form }
    envVars.value = b.formState.env.map((p) => ({ ...p }))
  }
}
// What the last deploy request actually sent. Success adopts this, not the
// buffer as it stands when the build finishes, so keystrokes made while it ran
// stay unsaved.
let deployBaseline = null
const adoptDeployBaseline = () => {
  if (deployBaseline) baseline.value = deployBaseline
  deployBaseline = null
}

// Keeps ?mode in step with the switch so the page can be linked and reloaded
// in either mode. replace, not push: flipping the switch is not a destination.
const routeTarget = () => ({ name: route.name, params: { ...route.params } })
const sameRoute = (t) => route.name === t.name && JSON.stringify(route.params) === JSON.stringify(t.params)
const editorScope = () => {
  const epoch = editorEpoch
  const target = routeTarget()
  return () => editorActive && epoch === editorEpoch && sameRoute(target)
    && target.name === (props.name ? 'function-detail' : 'function-new')
    && (target.params.name || '') === props.name
}
const requireEditorMode = (mode) => {
  if ((mode !== 'managed' && mode !== 'legacy') || iam.mode !== mode) {
    throw new Error('Could not determine the server mode. Try again.')
  }
}
const setModeQuery = (next, target = routeTarget()) => {
  // The confirm can outlive the page: never write ?mode onto a route the
  // operator has since moved to.
  if (!sameRoute(target)) return
  const query = { ...route.query }
  if (next === 'edit') query.mode = 'edit'
  else delete query.mode
  router.replace({ name: target.name, params: target.params, query })
}
const announceMode = (text) => {
  modeAnnouncement.value = ''
  nextTick(() => { modeAnnouncement.value = text })
}
// One discard question at a time. The confirm store holds a single resolver, so
// a second ask() would orphan the first promise forever; the second caller is
// told "no" instead, which for every caller here means "stay where you are".
let discardOpen = false
const askDiscard = async (message) => {
  if (discardOpen) return false
  discardOpen = true
  try {
    return await confirmStore.ask({
      title: 'Discard changes?',
      message,
      confirmLabel: 'Discard',
      cancelLabel: 'Keep editing',
      danger: true,
    })
  } finally {
    discardOpen = false
  }
}
const requestMode = async (next, fromUrl = false) => {
  if (!props.name || next === editorMode.value) return
  const target = routeTarget()
  if (next === 'view' && (deploying.value || secretBusy.value)) {
    if (fromUrl) setModeQuery('edit', target)
    return
  }
  if (next === 'view' && isDirty.value) {
    // A link that lost ?mode (list row, breadcrumb) is not a decision to throw
    // work away: stay in Edit and put the flag back.
    if (fromUrl) { setModeQuery('edit', target); return }
    const ok = await askDiscard('This function has unsaved changes. Switching to View discards them.')
    if (!ok || !sameRoute(target) || deploying.value || secretBusy.value) return
    discardChanges()
  }
  editorMode.value = next
  // The switch announces its own state; only a change the control did not make
  // (URL, Back) needs saying.
  if (fromUrl) announceMode(next === 'edit' ? 'Edit mode' : 'View mode, read-only')
  else setModeQuery(next, target)
}

// Opening another function (or /functions/new) from dirty work would run
// resetEditorState and drop the buffer. A global guard, not onBeforeRouteUpdate/
// Leave: vue-router unregisters those while this view is cached, which is
// exactly when "Editor -> list -> another function" happens. Leaving for any
// other page keeps the cached buffer, so only a change of function is asked about.
const guardFunctionChange = async (to) => {
  if (to.name !== 'function-detail' && to.name !== 'function-new') return true
  if ((to.params.name || '') === props.name) return true
  if (viewMode.value || !isDirty.value) return true
  if (!(await askDiscard('This function has unsaved changes. Opening another function discards them.'))) return false
  baseline.value = emptyBaseline()
  return true
}
const removeFunctionGuard = router.beforeEach(guardFunctionChange)
onBeforeUnmount(removeFunctionGuard)

// A mode that turns to View (switch or URL) must not leave an editable panel up.
watch(viewMode, (on) => {
  if (!on) return
  rollingBack.value = null
  closeMenus()
  for (const k of ['settings', 'envVars', 'deps', 'secrets']) modals.value[k] = false
})

// canTest: function has deployed code AND there's no active build in
// flight. While `deploying` is true, the warm pool may be holding stale
// code (or none, on a first deploy) so test invocations should wait.
const isDeployed = computed(() => isEditing.value || deployedThisSession.value)
const canTest = computed(() => isDeployed.value && !deploying.value && !awaitingInstall.value && !blockedByConfiguration.value)

// Orva offers two runtimes, latest-stable only. The id is generic (node /
// python); the version shown here is a display label that tracks latest-stable.
// Functions on legacy versioned ids are migrated to these on server startup.
const runtimes = [
  { id: 'python', label: 'Python 3.15' },
  { id: 'node',   label: 'Node.js 26' },
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
// The record the editor loaded, so a managed redeploy can publish settings with
// the fields the form does not edit (timeout) and detect a real change.
const loadedFunction = ref(null)
let managedSettingsCAS = null

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

// On /functions/new, choosing a runtime, a template or a fresh name picks a
// starting point rather than doing work. When nothing had been typed beforehand,
// re-base after it so only later typing counts as unsaved.
const asStartingPoint = (fn) => {
  const pristine = !isEditing.value && !isDirty.value
  fn()
  if (pristine) baseline.value = captureBaseline()
}
const setRuntime = (rt) => asStartingPoint(() => setRuntimeNow(rt))
const setRuntimeNow = (rt) => {
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

const applyTemplate = () => asStartingPoint(applyTemplateNow)
const applyTemplateNow = () => {
  if (viewMode.value) return
  const list = templates[form.value.runtime] || []
  const selected = list.find((t) => t.id === templateId.value)
  if (selected) {
    if (selected.requiresSDK) form.value.sdk = true
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
const invalidateBuild = () => {
  // A build outlives the editor that started it: the router reuses this
  // instance, so without this the previous function's failure re-opened the
  // build modal over the next one and blamed it for the error.
  buildEpoch += 1
  if (abortActiveStream) abortActiveStream()
  deploying.value = false
  deployBaseline = null
}

const resetEditorState = () => {
  editorEpoch += 1
  rollingBack.value = null
  invalidateBuild()
  fnId.value = ''
  managedCreated.value = null
  loadedFunction.value = null
  managedSettingsCAS = null
  configurationPublication.value = null
  loadedEntrypoint.value = ''
  secrets.value = []
  secretForm.value = { name: '', value: '' }
  pendingSecrets.value = []
  modals.value.secrets = false
  modals.value.firstDeploy = false
  form.value = {
    name: '',
    description: '',
    runtime: 'python',
    ...initialExecutionSettings,
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
  const current = editorScope()
  const name = props.name
  // A different function (or /functions/new) starts from its own URL, never
  // from the mode the previous one was left in.
  editorMode.value = initialMode()
  modeAnnouncement.value = ''
  baseline.value = emptyBaseline()
  if (!current()) return

  if (name) {
    // Edit mode — load function metadata + actual deployed source code.
    try {
      const listRes = await apiClient.get('/functions')
      if (!current()) return
      const fn = (listRes.data.functions || []).find(f => f.name === name)
      if (!fn) throw new Error('Function not found')

      fnId.value = fn.id
      loadedFunction.value = fn
      managedSettingsCAS = { owner: fn.id, read: readManagedSettingsCAS(fn, current), used: false }
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
      form.value.sdk = fn.sdk === true
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
      await reloadSource(fn, { current })
      if (!current()) return
      markFormClean()

      void loadSecrets()
      // Load deployment history so the Versions card can render.
      await loadVersions(fn, current)
    } catch (e) {
      if (!current()) return
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
    // The seed is the starting point, not work: only what is typed after it
    // makes a new function worth a "discard?" question.
    baseline.value = captureBaseline()
  }
}

watch(() => props.name, loadRouteData, { immediate: true })

// ?mode changed under a live Editor (Back, or a link to this same function).
// Declared after the name watcher: a link to another function changes both, and
// loadRouteData has already set the mode by the time this runs.
watch(() => props.mode, (m) => {
  if (!props.name) return
  requestMode(m === 'edit' ? 'edit' : 'view', true)
})

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
const reloadSource = async (fn, { skipIfDirty = false, current = editorScope() } = {}) => {
  if (!fn) return
  // Judged after the fetch, not before: typing while it was in flight is work too.
  const stale = () => !current() || (skipIfDirty && !viewMode.value && isDirty.value)
  try {
    const srcRes = await apiClient.get(`/functions/${fn.id}/source`)
    if (stale()) return
    if (srcRes.data.code) {
      code.value = srcRes.data.code
      dependencyText.value = srcRes.data.dependencies || ''
      markSourceClean()
      return
    }
  } catch {
    /* fall through to template */
  }
  if (stale()) return
  code.value = defaultCode[fn.runtime] || ''
  dependencyText.value = ''
  markSourceClean()
}

// Window-refocus handler — operators commonly deploy / rollback / edit
// configs via CLI or another browser tab, then click back to this
// editor expecting it to reflect the latest state. Without this the
// editor remains stale until a hard reload. Best-effort: any error
// is swallowed so a transient network blip doesn't disrupt the
// editing session.
const onWindowFocus = async () => {
  const id = fnId.value
  const inScope = editorScope()
  const current = () => inScope() && fnId.value === id
  if (!id || !current()) return
  // A dirty Edit session is the only copy of that work; refreshing would
  // replace it and re-base the dirty check on the server's version.
  if (!viewMode.value && isDirty.value) return
  try {
    const listRes = await apiClient.get('/functions')
    if (!current()) return
    const fn = (listRes.data.functions || []).find((f) => f.id === id)
    if (!fn) return
    await reloadSource(fn, { skipIfDirty: true, current })
    await loadVersions(fn, current)
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
  if (viewMode.value || deploying.value) return
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
onActivated(() => {
  bindGlobals()
  if (editorActive) return
  editorActive = true
  if (!baseline.value.loaded) void loadRouteData()
  else void loadSecrets()
})
const deactivateEditor = () => {
  editorActive = false
  editorEpoch += 1
  // A pending receipt belongs to this activation, not a cached return visit.
  const receipt = configurationPublication.value
  if (receipt) configurationPublication.value = receipt.installed ? null : { owner: receipt.owner, settings: receipt.settings }
  rollingBack.value = null
  invalidateBuild()
  unbindGlobals()
}
onDeactivated(deactivateEditor)
// onDeactivated does not fire when keep-alive evicts this instance at :max.
onBeforeUnmount(() => { deactivateEditor(); clearTimeout(deployedTimer) })

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
  asStartingPoint(() => { form.value.name = generateFunctionName() })
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
// Legacy history collapses identical hashes. Managed history keeps the exact
// deployment rows, since equal content does not imply equal sealed provenance.
const loadVersions = async (fn, current = editorScope()) => {
  if (!current()) return
  try {
    const res = await apiClient.get(`/functions/${fn.id}/deployments?limit=50`)
    if (!current()) return
    const deps = res.data.deployments || []
    const managed = iam.mode === 'managed'
    const seen = new Set()
    versions.value = deps
      .filter((d) => d.status === 'succeeded' && d.code_hash)
      .filter((d) => {
        if (managed) return true
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
        is_active: managed ? d.id === fn.active_deployment_id : d.code_hash === fn.code_hash,
      }))
  } catch (e) {
    if (current()) console.warn('failed to load versions', e)
  }
}

// loadVersions ran on mount, on window focus and after a rollback, but not
// after a deploy, which is why Config -> Versions lagged the thing you just did.
const refreshVersions = async () => {
  const id = fnId.value
  const inScope = editorScope()
  const current = () => inScope() && fnId.value === id
  if (!id || !current()) return
  try {
    const res = await apiClient.get('/functions')
    const fn = (res.data.functions || []).find((f) => f.id === id)
    if (fn) await loadVersions(fn, current)
  } catch {
    /* the badge can lag; the deployment itself already landed */
  }
}

// Top-level deploy entry. For brand-new functions we open the
// "Name & deploy" modal first so the user gets a focused prompt for the
// name + final config; once they confirm, runDeploy() does the actual
// upload. Existing functions skip straight to runDeploy().
const deployFunction = async () => {
  if (viewMode.value) return
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

const managedCreation = (form, entrypoint) => {
  const { name, runtime } = form
  if (name.length > 128 || !/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(name) || name.trim() !== name) {
    throw new Error('Use a lowercase function name with letters, numbers and single dashes.')
  }
  if (!runtimes.some(({ id }) => id === runtime) || !entrypoint
    || new TextEncoder().encode(entrypoint).length > 4096 || /[:\\\uFFFD]/u.test(entrypoint) || entrypoint.includes('\0')
    || entrypoint.split('/').some((part) => !part.trim() || part === '.' || part === '..')) {
    throw new Error('Choose a supported runtime and a relative entrypoint file.')
  }
  return { name, runtime, entrypoint }
}

const runDeploy = async () => {
  const inScope = editorScope()
  let id = fnId.value
  if (!inScope() || viewMode.value || deploying.value || installing.value || deployBlockedByConfiguration.value
    || rollingBack.value || (isEditing.value && !id)) return
  if (!form.value.name || !code.value) {
    confirmStore.notify({ title: 'Missing fields', message: 'Please provide a function name and code.' })
    return
  }

  // The build runs headless. Most finish in well under a second, and a modal
  // that opens and closes inside that window is a flash, not information --
  // so Deploy carries its own progress and only a failure raises the log.
  const epoch = ++buildEpoch
  const current = () => inScope() && epoch === buildEpoch && fnId.value === id && !viewMode.value
  deployBaseline = captureBaseline()
  const draft = deployBaseline
  const formDraft = draft.formState.form
  const extras = { ...deployExtras.value }
  const build = {
    code: draft.code,
    filename: fileName.value,
    dependencies: draft.deps || '',
    ...(Object.keys(extras).length ? { extras } : {}),
  }
  const pending = pendingSecrets.value.slice()
  const settings = managedSettings()
  const expectedSettings = loadedManagedSettings()
  const loadedEnvironment = loadedFunction.value?.env_vars
  const loadedDescription = loadedFunction.value?.description || ''
  const settingsCAS = managedSettingsCAS
  deploying.value = true
  buildError.value = ''
  if (!configurationInstallRequired.value) configurationPublication.value = null
  lastBuild.value = null
  buildLogs.value = ['Starting deployment...']
  let submitted = false

  try {
    // Build env_vars map from the envVars array.
    const envVarsMap = {}
    for (const { key, value } of draft.formState.env) {
      if (key.trim()) envVarsMap[key.trim()] = value
    }
    const mode = await iam.checkMode()
    if (!current()) return
    requireEditorMode(mode)

    // Step 1: Create or update function config.
    if (mode === 'managed' && !isEditing.value) {
      if (pending.length || formDraft.description || draft.formState.env.length
        || Object.entries(initialExecutionSettings).some(([key, value]) => formDraft[key] !== value)) {
        throw new Error('Drafts were kept. Clear custom settings, environment and secrets to build; install before configuring.')
      }
      const creation = managedCreation(formDraft, build.filename)
      if (id) {
        if (!managedCreated.value || Object.keys(creation).some((key) => creation[key] !== managedCreated.value[key])) {
          throw new Error('The function was already created. Open it from Functions to build again.')
        }
      } else {
        const res = await apiClient.post('/functions', creation)
        if (!current()) return
        if (!res.data?.id) throw new Error('Function creation could not be verified. Check Functions before retrying.')
        fnId.value = id = res.data.id
        managedCreated.value = creation
        buildLogs.value.push(`Created function: ${id}`)
      }
    } else if (!id) {
      // New function — create it.
      try {
        const createRes = await apiClient.post('/functions', {
          name: formDraft.name,
          description: formDraft.description || '',
          runtime: formDraft.runtime,
          memory_mb: formDraft.memory_mb,
          cpus: formDraft.cpus,
          env_vars: envVarsMap,
          network_mode: formDraft.network_mode,
          sdk: formDraft.sdk,
          max_concurrency: formDraft.max_concurrency || 0,
          concurrency_policy: formDraft.concurrency_policy || 'queue',
          auth_mode: formDraft.auth_mode || 'none',
          rate_limit_per_min: formDraft.rate_limit_per_min || 0,
        })
        if (!current()) return
        fnId.value = id = createRes.data.id
        buildLogs.value.push(`Created function: ${id}`)
      } catch (err) {
        if (!current()) return
        if (err.response?.status === 409) {
          const listRes = await apiClient.get('/functions')
          if (!current()) return
          const fn = (listRes.data.functions || []).find(f => f.name === formDraft.name)
          if (fn) {
            fnId.value = id = fn.id
            buildLogs.value.push(`Function already exists: ${id}`)
          } else {
            throw new Error('Function name conflict but not found in list', { cause: err })
          }
        } else {
          throw err
        }
      }
    } else if (mode === 'managed') {
      // Managed mode publishes configuration; its router does not mount the
      // legacy PUT /functions/{id}, and an unchanged redeploy must not
      // publish a new configuration.
      const changed = JSON.stringify(settings) !== JSON.stringify(expectedSettings)
        || !sameEnvironment(envVarsMap, loadedEnvironment)
      if (changed) {
        await publishManagedSettings(id, current, settings, envVarsMap, settingsCAS)
        if (!current()) return
        buildLogs.value.push('Updated function configuration')
      }
      if (formDraft.description !== loadedDescription) {
        buildLogs.value.push('Description is not saved in managed mode')
      }
    } else {
      // Existing function — update config (memory, cpus, env_vars,
      // network_mode) so changes take effect. The backend drains the warm
      // pool when any of these change so the next invoke respawns with
      // the new config.
      await apiClient.put(`/functions/${id}`, {
        description: formDraft.description || '',
        memory_mb: formDraft.memory_mb,
        cpus: formDraft.cpus,
        env_vars: envVarsMap,
        network_mode: formDraft.network_mode,
        sdk: formDraft.sdk,
        max_concurrency: formDraft.max_concurrency || 0,
        concurrency_policy: formDraft.concurrency_policy || 'queue',
        auth_mode: formDraft.auth_mode || 'none',
        rate_limit_per_min: formDraft.rate_limit_per_min || 0,
      })
      if (!current()) return
      buildLogs.value.push('Updated function config')
    }

    // The managed configuration service needs an installed artifact.
    if (mode === 'legacy') await flushPendingSecrets(id, current, mode, pending)
    if (!current()) return
    requireEditorMode(mode)

    // Step 2: Submit code (async build pipeline returns 202 + deployment_id).
    buildLogs.value.push('Submitting build...')
    submitted = true
    const deployRes = await apiClient.post(`/functions/${id}/deploy-inline`, build)

    const depId = deployRes.data.deployment_id
    // The editor may have been re-scoped while the upload was in flight.
    if (!current()) return
    if (mode === 'managed' && !/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(depId)) {
      throw new Error('No exact deployment ID returned. Check deployment history before retrying.')
    }
    if (!depId) {
      // Legacy synchronous response — older backend without async pipeline.
      buildLogs.value.push(`Deployed! Hash: ${deployRes.data.code_hash || 'unknown'}`)
      deployedThisSession.value = true
      adoptDeployBaseline()
      lastBuild.value = { version: null, durationMs: null }
      deploying.value = false
      return
    }

    // Step 3: Stream the build via SSE. Test button stays disabled until
    // the stream emits `succeeded`. Deploying flag stays true so the
    // Deploy button keeps its loading state.
    buildLogs.value.push(`Build queued (${depId})`)
    await streamBuild(depId, current, mode, id)
  } catch (err) {
    if (!current()) return
    if (submitted && ![400, 401, 403, 413].includes(err.response?.status)) markConfigurationBuildUnverified()
    buildError.value = err.response?.data?.error?.message || err.message || 'Deployment failed'
    buildLogs.value.push(`Error: ${buildError.value}`)
    buildModalOpen.value = true
    deploying.value = false
  }
}

const markConfigurationBuildUnverified = () => {
  const receipt = configurationReceipt.value
  if (receipt?.settings && receipt.expected) configurationPublication.value = { ...receipt, buildUnverified: true }
}

// streamBuild opens an SSE connection to /deployments/{id}/stream and
// resolves when the build hits a terminal state. Build log lines are
// pushed into buildLogs (capped to last 500). The SSE stream emits:
//   event: log           — { seq, stream, line, ts }
//   event: succeeded     — final deployment row
//   event: failed        — final deployment row (with error_message)
//   event: error         — transport/server error; we fall back to polling
const streamBuild = (depId, current, mode, owner) => new Promise((resolve) => {
  if (abortActiveStream) abortActiveStream()
  const es = new EventSource(`/api/v1/deployments/${depId}/stream`)

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
    if (!current()) { resolve(); return }
    deploying.value = false
    if (ok && mode === 'managed') {
      if (payload?.id !== depId || payload?.function_id !== owner || payload?.status !== 'succeeded') {
        markConfigurationBuildUnverified()
        buildError.value = 'Build result could not be verified. Check deployment history.'
        buildModalOpen.value = true
      } else {
        configurationPublication.value = null
        adoptDeployBaseline()
        lastBuild.value = {
          managed: true, owner, deploymentId: depId, installed: false,
          version: payload.version || null, durationMs: payload.duration_ms ?? null,
        }
        buildLogs.value.push('Build succeeded. Install required.')
        refreshVersions()
        buildModalOpen.value = false
      }
    } else if (ok) {
      deployedThisSession.value = true
      adoptDeployBaseline()
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
      if (mode === 'managed' && (payload?.id !== depId || payload?.function_id !== owner || payload?.status !== 'failed')) {
        markConfigurationBuildUnverified()
      }
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
    if (!current()) return
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

const installManagedBuild = async () => {
  const build = lastBuild.value
  const inScope = editorScope()
  const current = () => inScope() && fnId.value === build?.owner
    && lastBuild.value === build && !viewMode.value && iam.mode === 'managed'
  if (!awaitingInstall.value || deploying.value || installing.value || blockedByConfiguration.value || rollingBack.value || !current()) return
  installingBuild.value = build
  try {
    const res = await apiClient.post(`/deployments/${build.deploymentId}/install`)
    if (!current()) return
    if (res.data?.review_required !== true || !res.data.artifact) {
      throw new Error('Installation could not be verified. Check deployment history.')
    }
    build.installed = true
    deployedThisSession.value = true
    lastDeployAt.value = new Date().toISOString()
    buildLogs.value.push('Installed. Permissions review required.')
    refreshVersions()
  } catch (err) {
    if (!current()) return
    confirmStore.notify({
      title: 'Install failed',
      message: err.response?.data?.error?.message || err.message,
      danger: true,
    })
  } finally {
    if (installingBuild.value === build) installingBuild.value = null
  }
}

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

const rollbackToVersion = async (v) => {
  const owner = fnId.value
  const deploymentId = v?.deployment_id
  const inScope = editorScope()
  if (!inScope() || viewMode.value || !owner || !deploymentId || rollingBack.value
    || deploying.value || installing.value || blockedByConfiguration.value) return
  const operation = Symbol()
  rollingBack.value = operation
  const current = () => inScope() && !viewMode.value && fnId.value === owner && rollingBack.value === operation
  let mode
  const currentMode = () => current() && iam.mode === mode
  try {
    mode = await iam.checkMode()
    if (!current()) return
    requireEditorMode(mode)

    let message
    if (mode === 'managed') {
      if (!isCanonicalUuid(owner) || !isCanonicalUuid(deploymentId)) {
        throw new Error('Managed rollback requires exact function and deployment IDs.')
      }
      message = `Restore v${v.version} (code ${v.short_hash})? Install the restored version before invoking it. Review permissions separately. Settings, environment and secrets stay unchanged.`
    } else {
      message = `Code hash ${v.short_hash}. Your current version stays in the history.`
      try {
        const [depRes, listRes] = await Promise.all([
          apiClient.get(`/deployments/${deploymentId}`),
          apiClient.get('/functions'),
        ])
        if (!currentMode()) return
        const snap = depRes.data?.snapshot
        const fn = (listRes.data.functions || []).find((f) => f.id === owner)
        if (snap && fn) {
          const lines = describeSnapshotDiff(fn, snap)
          message = lines.length
            ? `Rolling back to v${v.version} (code ${v.short_hash}) will also change:\n\n${lines.join('\n')}\n\nSecrets keep their current values; they aren't part of the rollback.`
            : `Rolling back to v${v.version} (code ${v.short_hash}). Settings and env are already identical, so only the code changes.`
        }
      } catch {
        // The legacy diff is best-effort.
      }
    }

    if (!currentMode() || confirmStore.visible) return
    const ok = await confirmStore.ask({
      title: `Restore v${v.version}?`,
      message,
      confirmLabel: 'Rollback',
    })
    if (!ok || !currentMode()) return

    if (managedSettingsCAS?.owner === owner) managedSettingsCAS = null
    if (mode === 'managed') {
      configurationPublication.value = null
      lastBuild.value = null
    }
    const { data } = await rollbackFunction(owner, { deployment_id: deploymentId })
    if (!currentMode()) return
    if (mode === 'managed') {
      if (data?.id !== deploymentId || data.deployment_id !== deploymentId || data.function_id !== owner
        || !Number.isSafeInteger(data.version) || data.version < 1 || data.version !== v.version
        || !data.code_hash || data.code_hash !== v.code_hash || data.status !== 'succeeded'
        || data.phase !== 'review_required' || data.function_status !== 'inactive') {
        throw new Error('Rollback result could not be verified. Check deployment history before retrying.')
      }
      lastBuild.value = { managed: true, rollback: true, owner, deploymentId, installed: false, version: data.version }
      buildError.value = ''
      buildLogs.value = ['Version restored. Install required.']
      modals.value.versions = false
      versions.value = versions.value.map((row) => ({ ...row, is_active: row.deployment_id === deploymentId }))
      if (!isDirty.value) {
        try {
          const source = await apiClient.get(`/functions/${owner}/source`)
          if (!currentMode() || isDirty.value) return
          if (typeof source.data?.code !== 'string' || typeof source.data?.dependencies !== 'string') throw new Error()
          code.value = source.data.code
          dependencyText.value = source.data.dependencies
          markSourceClean()
        } catch {
          if (!currentMode()) return
          confirmStore.notify({
            title: 'Source refresh failed',
            message: 'The version was restored. Reopen the editor to load its source before editing.',
            danger: true,
          })
        }
      }
      return
    }

    const listRes = await apiClient.get('/functions')
    if (!currentMode()) return
    const fn = (listRes.data.functions || []).find((f) => f.id === owner)
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
      form.value.sdk = fn.sdk === true
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
      await reloadSource(fn, { current: currentMode })
      if (!currentMode()) return
      markFormClean()
      await loadVersions(fn, currentMode)
    }
  } catch (e) {
    if (!current() || (mode && mode !== 'unknown' && !currentMode())) return
    const code = e.response?.data?.error?.code || ''
    const msg = e.response?.data?.error?.message || e.message || 'Rollback failed'
    if (code === 'VERSION_GCD') {
      confirmStore.notify({ title: 'Version unavailable', message: `This version has been garbage-collected and can no longer be restored.\n\n${msg}`, danger: true })
    } else {
      confirmStore.notify({ title: 'Rollback failed', message: msg, danger: true })
    }
  } finally {
    if (rollingBack.value === operation) rollingBack.value = null
  }
}

// Managed mode replaces the legacy secrets CRUD with a CAS-checked
// configuration publication: read the tokens fresh for every write and never
// reuse them, because they move after each publication.
const loadManagedConfiguration = async (id) => {
  const res = await apiClient.get(`/functions/${id}/configuration`, { quietStatuses: [404] })
  return res.data
}

const managedConfigurationError = (err, fallback, forbidden = 'An administrator must change secrets.') => {
  const status = err.response?.status
  if (status === 409) return 'Configuration changed elsewhere; reload to continue.'
  if (status === 403) return forbidden
  return err.response?.data?.error?.message || err?.message || fallback
}

const managedSecretError = (status, fallback) => {
  if (status === 409) return 'Configuration changed elsewhere; reload to continue.'
  if (status === 403) return 'An administrator must change secrets.'
  return fallback
}

const artifactDigest = (value) => typeof value === 'string' && /^[0-9a-f]{64}$/.test(value)
const configurationRevisions = ['function_version', 'artifact_revision', 'artifact_epoch', 'policy_revision', 'authority_epoch']
const positiveRevision = (value) => typeof value === 'string' && /^[1-9]\d{0,18}$/.test(value)

const configurationSuccessor = (data, previous) => {
  if (data?.review_required !== true || !isCanonicalUuid(data.generation) || data.generation === previous.generation
    || (!['', 'iam:unbound'].includes(data.approved_artifact) && !artifactDigest(data.approved_artifact))
    || data.approved_artifact !== previous.approved_artifact) return null
  const expected = { generation: data.generation, approved_artifact: data.approved_artifact }
  for (const key of configurationRevisions) {
    if (!positiveRevision(data[key]) || !positiveRevision(previous[key])
      || BigInt(data[key]) !== BigInt(previous[key]) + 1n
      || BigInt(data[key]) > (key === 'function_version' ? 2147483647n : 9223372036854775807n)) return null
    expected[key] = data[key]
  }
  return expected
}

const installManagedConfiguration = async () => {
  const receipt = configurationReceipt.value
  const inScope = editorScope()
  const current = () => inScope() && !viewMode.value && configurationReceipt.value === receipt && fnId.value === receipt?.owner
  if (!canOfferConfigurationInstall.value || !current() || secretBusy.value || installing.value || rollingBack.value) return
  const { owner, expected, previousArtifact } = receipt
  const unverified = { owner, settings: receipt.settings }
  const inspect = receipt.settings ? 'Inspect the current configuration before continuing.' : 'Reload before continuing.'
  const conflict = receipt.settings ? `Configuration changed elsewhere. ${inspect}` : 'Configuration changed elsewhere; reload to continue.'
  secretOperations.add(owner)
  try {
    const { data } = await apiClient.post(`/functions/${owner}/configuration/install`, { expected }, { sensitive: true })
    if (!current()) return
    if (data?.review_required !== true || !artifactDigest(data.current_artifact) || data.current_artifact === previousArtifact) {
      configurationPublication.value = unverified
      confirmStore.notify({ title: 'Install unverified', message: inspect, danger: true })
      return
    }
    configurationPublication.value = { owner, installed: true }
    lastDeployAt.value = new Date().toISOString()
  } catch (err) {
    if (!current()) return
    const status = err.response?.status
    if (status !== 403) configurationPublication.value = unverified
    confirmStore.notify({
      title: 'Install failed',
      message: status === 403 ? 'An administrator must install the configuration.'
        : status === 409 ? conflict
        : inspect,
      danger: true,
    })
  } finally {
    secretOperations.delete(owner)
  }
}

const writeManagedSecret = async (id, current, key, value, { remove = false } = {}) => {
  const config = await loadManagedConfiguration(id)
  if (!current()) return
  requireEditorMode('managed')
  if (!config?.expected || !artifactDigest(config.current_artifact)) {
    throw new Error('Configuration is unavailable; reload to continue.')
  }
  const body = { expected: config.expected, current_artifact: config.current_artifact }
  body.secret = remove ? { key } : { key, value }
  const path = `/functions/${id}/configuration/secrets`
  const prior = configurationPublication.value
  configurationPublication.value = { owner: id }
  let res
  try {
    res = remove
      ? await apiClient.delete(path, { data: body, sensitive: true })
      : await apiClient.put(path, body, { sensitive: true })
  } catch (err) {
    if (current() && iam.mode === 'managed' && [400, 401, 403].includes(err.response?.status)) configurationPublication.value = prior
    throw err
  }
  if (!current() || iam.mode !== 'managed') return
  lastBuild.value = null
  const expected = configurationSuccessor(res?.data, body.expected)
  if (!expected) {
    confirmStore.notify({ title: 'Secret update unverified', message: 'Reload before continuing.', danger: true })
    return
  }
  configurationPublication.value = { owner: id, expected, previousArtifact: body.current_artifact }
}

// The 9 fields the managed settings publication accepts, exactly. timeout_ms is
// not in the form, so it comes from the record the editor loaded.
const managedSettings = () => ({
  timeout_ms: Number(loadedFunction.value?.timeout_ms),
  memory_mb: form.value.memory_mb,
  cpus: form.value.cpus,
  network_mode: form.value.network_mode,
  sdk: form.value.sdk === true,
  max_concurrency: form.value.max_concurrency || 0,
  concurrency_policy: form.value.concurrency_policy || 'queue',
  auth_mode: form.value.auth_mode || 'none',
  rate_limit_per_min: form.value.rate_limit_per_min || 0,
})

const loadedManagedSettings = () => ({
  timeout_ms: Number(loadedFunction.value?.timeout_ms),
  memory_mb: loadedFunction.value?.memory_mb,
  cpus: loadedFunction.value?.cpus,
  network_mode: loadedFunction.value?.network_mode || 'none',
  sdk: loadedFunction.value?.sdk === true,
  max_concurrency: loadedFunction.value?.max_concurrency || 0,
  concurrency_policy: loadedFunction.value?.concurrency_policy || 'queue',
  auth_mode: loadedFunction.value?.auth_mode || 'none',
  rate_limit_per_min: loadedFunction.value?.rate_limit_per_min || 0,
})

const sameEnvironment = (current, loaded) => JSON.stringify(Object.entries(current || {}).sort())
  === JSON.stringify(Object.entries(loaded || {}).sort())

const readManagedSettingsCAS = async (fn, current) => {
  const mode = await iam.checkMode()
  const ownsLoad = () => current() && fnId.value === fn.id && iam.mode === 'managed'
  if (mode !== 'managed' || !ownsLoad()) return
  if (!Number.isSafeInteger(fn.version) || fn.version < 1) return
  const config = await loadManagedConfiguration(fn.id).catch(() => null)
  if (!ownsLoad()) return
  // Settings and environment advance the function version in the same DB transaction.
  if (config?.expected?.function_version !== String(fn.version)
    || !artifactDigest(config.current_artifact)) return
  return {
    expected: { ...config.expected },
    current_artifact: config.current_artifact,
  }
}

const publishManagedSettings = async (id, current, settings, environment, captured) => {
  try {
    const config = await captured?.read
    if (!current()) return
    requireEditorMode('managed')
    if (!config || captured !== managedSettingsCAS || captured.owner !== id || captured.used
      || !Number.isInteger(settings.timeout_ms) || settings.timeout_ms < 1) {
      throw new Error('Configuration is unavailable; reload after installing the current artifact if needed.')
    }
    captured.used = true
    const pending = { owner: id, settings: true }
    configurationPublication.value = pending
    let res
    try {
      res = await apiClient.put(`/functions/${id}/configuration/settings`, {
        expected: config.expected,
        current_artifact: config.current_artifact,
        environment,
        settings,
      })
    } catch (err) {
      if (current() && configurationPublication.value === pending && [400, 401, 403, 409].includes(err.response?.status)) {
        configurationPublication.value = null
      }
      throw err
    }
    if (!current()) return
    requireEditorMode('managed')
    const expected = configurationSuccessor(res?.data, config.expected)
    if (captured !== managedSettingsCAS || configurationPublication.value !== pending || !expected) {
      throw new Error('Configuration update could not be verified. Inspect the current configuration before continuing.')
    }
    managedSettingsCAS = null
    loadedFunction.value = { ...loadedFunction.value, ...settings, env_vars: environment, version: Number(expected.function_version) }
    configurationPublication.value = { ...pending, expected, previousArtifact: config.current_artifact }
  } catch (err) {
    throw new Error(managedConfigurationError(err, 'Failed to update configuration', 'An administrator must change the configuration.'), { cause: err })
  }
}

let secretListRequest = 0
const loadSecrets = async () => {
  const id = fnId.value
  const inScope = editorScope()
  if (!id || !inScope()) return
  const request = ++secretListRequest
  const current = () => inScope() && fnId.value === id && request === secretListRequest
  try {
    const res = await apiClient.get(`/functions/${id}/secrets`)
    if (!current()) return
    secrets.value = (res.data.secrets || []).map((name) => ({ id: name, name }))
  } catch (err) {
    if (current()) console.error('Failed to load secrets', err)
  }
}

const saveSecret = async () => {
  const id = fnId.value
  const inScope = editorScope()
  const current = () => inScope() && fnId.value === id && !viewMode.value
  if (!current() || (isEditing.value && !id)) return
  const { name: inputName, value } = secretForm.value
  const name = inputName.trim()
  if (!name) return
  // A new managed owner has no configuration target until installation.
  if (!id || newManagedUninstalled.value) {
    // Replace any prior pending entry with the same name.
    pendingSecrets.value = pendingSecrets.value.filter((s) => s.name !== name)
    pendingSecrets.value.push({ name, value })
    secretForm.value.name = ''
    secretForm.value.value = ''
    return
  }
  // Existing function: save through the API.
  let mode
  let locked = false
  try {
    mode = await iam.checkMode()
    if (!current()) return
    requireEditorMode(mode)
    if (mode === 'managed') {
      if (managedSecretBlocked.value) return
      secretOperations.add(id)
      locked = true
      await writeManagedSecret(id, current, name, value)
    } else {
      await apiClient.post(`/functions/${id}/secrets`, { key: name, value })
    }
    if (!current()) return
    if (secretForm.value.name === inputName && secretForm.value.value === value) {
      secretForm.value = { name: '', value: '' }
    }
    await loadSecrets()
  } catch (err) {
    if (!current()) return
    confirmStore.notify({
      title: 'Save failed',
      message: mode === 'managed'
        ? managedSecretError(err.response?.status, 'Could not save secret. Reload before continuing.')
        : mode !== 'legacy' ? 'Could not determine the server mode. Try again.'
        : (err.response?.data?.error?.message || 'Failed to save secret'),
      danger: true,
    })
  } finally {
    if (locked) secretOperations.delete(id)
  }
}

const removePendingSecret = (idx) => {
  pendingSecrets.value.splice(idx, 1)
}

const removeSecret = async (key) => {
  const id = fnId.value
  const inScope = editorScope()
  const current = () => inScope() && fnId.value === id && !viewMode.value
  if (!id || !current() || managedSecretBlocked.value) return
  const ok = await confirmStore.ask({
    title: 'Delete secret?',
    message: `"${key}" will be removed from this function's environment.`,
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok || !current()) return
  let mode
  let locked = false
  try {
    mode = await iam.checkMode()
    if (!current()) return
    requireEditorMode(mode)
    if (mode === 'managed') {
      if (managedSecretBlocked.value) return
      secretOperations.add(id)
      locked = true
      await writeManagedSecret(id, current, key, '', { remove: true })
    } else {
      await apiClient.delete(`/functions/${id}/secrets/${encodeURIComponent(key)}`)
    }
    if (!current()) return
    await loadSecrets()
  } catch (err) {
    if (!current()) return
    confirmStore.notify({
      title: 'Delete failed',
      message: mode === 'managed'
        ? managedSecretError(err.response?.status, 'Could not delete secret. Reload before continuing.')
        : mode !== 'legacy' ? 'Could not determine the server mode. Try again.'
        : (err.response?.data?.error?.message || 'Failed to delete secret'),
      danger: true,
    })
  } finally {
    if (locked) secretOperations.delete(id)
  }
}

// Legacy creation can persist queued secrets before the first build.
const flushPendingSecrets = async (id, current, mode, pending) => {
  if (!id || !pending.length) return
  for (const sec of pending) {
    if (!current()) return
    requireEditorMode(mode)
    try {
      await apiClient.post(`/functions/${id}/secrets`, {
        key: sec.name, value: sec.value,
      })
      if (!current()) return
      requireEditorMode(mode)
      buildLogs.value.push(`Saved secret: ${sec.name}`)
    } catch (err) {
      if (!current()) return
      requireEditorMode(mode)
      const msg = err.response?.data?.error?.message || err.message
      buildLogs.value.push(`Failed to save secret ${sec.name}: ${msg}`)
    }
  }
  pendingSecrets.value = pendingSecrets.value.filter((sec) => !pending.includes(sec))
  await loadSecrets()
}

const resetForm = async () => {
  if (viewMode.value) return
  const ok = await confirmStore.ask({
    title: 'Reset editor?',
    message: 'Unsaved changes in the editor will be discarded.',
    confirmLabel: 'Reset',
    danger: true,
  })
  if (!ok || viewMode.value) return
  if (isEditing.value) {
    discardChanges()
    return
  }
  form.value.name = ''
  fnId.value = ''
  managedCreated.value = null
  code.value = ''
  form.value.sdk = false
  deployedThisSession.value = false
  buildError.value = ''
  lastBuild.value = null
  setRuntime('python')
}
</script>

<style scoped>
/* Match the shared sm toolbar controls without changing the menu styling. */
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
  /* 0.75rem: 26.6px is the h-7 rung, same as Button size="xs". */
  font-size: 0.75rem;
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

@media (pointer: coarse) {
  .panel-btn,
  .run-btn {
    height: auto;
    min-height: 36px;
    position: relative;
  }
  /* Fixed target height avoids losing the two border pixels to the padding box. */
  .panel-btn::after,
  .run-btn::after {
    content: '';
    position: absolute;
    top: calc(50% - 22px);
    height: 44px;
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
