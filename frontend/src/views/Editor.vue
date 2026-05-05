<template>
  <div class="flex flex-col h-full">
    <!--
      Visually-hidden semantic heading. The visible toolbar carries the
      function name as an <input> for editability; screen readers need
      a real <h1> to anchor the page in their heading-nav. The reactive
      content keeps the AT cursor's "you are on" announcement aligned
      with whatever the operator has typed in the name field.
    -->
    <h1 class="sr-only">{{ form.name || 'New function' }}</h1>
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
        <input
          v-model="form.name"
          placeholder="my-function"
          :disabled="isEditing"
          class="bg-transparent border-0 text-base sm:text-sm font-medium text-white placeholder-foreground-muted focus:outline-none px-1 py-1 min-w-0 flex-1 sm:flex-none sm:w-40"
        >
        <button
          v-if="!isEditing && !fnId"
          type="button"
          class="p-1 rounded text-foreground-muted hover:text-white hover:bg-surface-hover transition-colors shrink-0 touch-expand-iconbtn"
          title="Re-roll a fresh name"
          aria-label="Re-roll a fresh name"
          @click="rerollName"
        >
          <Shuffle class="w-3.5 h-3.5" />
        </button>
        <span class="text-[11px] text-foreground-muted font-medium tracking-tight shrink-0">{{ runtimeShort(form.runtime) }}</span>
      </div>
      <!-- Wrapper for the dropdowns + actions on mobile so they sit on
           one row (or wrap among themselves). On sm+ they merge back
           into the parent flex-wrap. -->
      <div class="flex flex-wrap items-center gap-2 sm:contents">

      <!-- Config menu — Settings / Env / Deps / Secrets / Versions.
           Versions is hidden until isEditing && versions.length so the
           menu stays small for new-function flows. -->
      <div class="relative" ref="configMenuRef">
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
          <button class="menu-item" role="menuitem" @click="openMenuItem('settings')">
            <Settings2 class="w-3.5 h-3.5" />
            <span class="flex-1 text-left">Settings</span>
            <span class="text-[10px] text-foreground-muted">runtime · limits</span>
          </button>
          <button class="menu-item" role="menuitem" @click="openMenuItem('envVars')">
            <Variable class="w-3.5 h-3.5" />
            <span class="flex-1 text-left">Env</span>
            <span v-if="envVarCount" class="text-[10px] text-foreground-muted tabular-nums">{{ envVarCount }}</span>
          </button>
          <button class="menu-item" role="menuitem" @click="openMenuItem('deps')">
            <Package class="w-3.5 h-3.5" />
            <span class="flex-1 text-left">Deps</span>
            <span class="text-[10px] text-foreground-muted">package · requirements</span>
          </button>
          <button class="menu-item" role="menuitem" @click="openMenuItem('secrets')">
            <KeyRound class="w-3.5 h-3.5" />
            <span class="flex-1 text-left">Secrets</span>
            <span v-if="totalSecretsCount" class="text-[10px] text-foreground-muted tabular-nums">{{ totalSecretsCount }}</span>
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
      <div class="relative" ref="bindingsMenuRef">
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
            <span class="flex-1 text-left">KV store</span>
            <span class="text-[10px] text-foreground-muted">per-function state</span>
          </button>
          <button
            v-if="isEditing"
            class="menu-item"
            role="menuitem"
            @click="navMenu({ name: 'function-inbound-webhooks', params: { name: form.name } })"
          >
            <Webhook class="w-3.5 h-3.5" />
            <span class="flex-1 text-left">Inbound webhooks</span>
            <span class="text-[10px] text-foreground-muted">signed POST</span>
          </button>
          <button class="menu-item" role="menuitem" @click="openMenuItem('docs')">
            <BookOpen class="w-3.5 h-3.5" />
            <span class="flex-1 text-left">Docs</span>
            <span class="text-[10px] text-foreground-muted">handler reference</span>
          </button>
        </div>
      </div>

      <div class="w-px h-5 bg-border mx-1" />

      <Button
        variant="secondary"
        size="sm"
        @click="resetForm"
      >
        Reset
      </Button>
      <Button
        size="sm"
        :loading="deploying"
        @click="deployFunction"
      >
        <UploadCloud class="w-4 h-4" />
        {{ isEditing ? 'Deploy New Version' : 'Deploy' }}
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
      <code class="font-mono text-white truncate flex-1 min-w-0">{{ invokeUrl }}</code>
      <button
        class="px-2 py-1 rounded text-foreground-muted hover:text-white hover:bg-surface-hover transition-colors flex items-center gap-1 shrink-0"
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
        class="text-foreground-muted hover:text-white transition-colors px-2"
      >
        Deployments →
      </router-link>
    </div>


    <!-- Code editor takes the main area. No sidebar, no scroll inside the
         page — the editor and the bottom terminal share vertical space. -->
    <div class="flex-1 flex flex-col min-h-0 mt-3 bg-background border border-border rounded-lg overflow-hidden shadow-sm">
      <div class="h-9 border-b border-border flex items-center justify-between px-4 bg-surface shrink-0">
        <div class="text-xs font-mono text-foreground-muted flex items-center gap-2">
          <FileCode class="w-3 h-3" />
          <span class="text-white">{{ fileName }}</span>
          <span
            v-if="templateId"
            class="text-foreground-muted"
          >· template: {{ templateId }}</span>
        </div>
        <div class="text-[10px] text-foreground-muted font-mono">
          {{ code.length }} chars
        </div>
      </div>
      <CodeEditor
        v-model="code"
        :language="form.runtime"
        class="flex-1 min-h-0"
      />
    </div>

    <!-- Bottom terminal: VS Code-style. Tabs across build, output, and
         test-run results. Collapsible — clicking the header chevron hides
         the panel body. -->
    <div class="mt-3 bg-background border border-border rounded-lg overflow-hidden shrink-0">
      <div class="h-9 border-b border-border flex items-center px-2 bg-surface">
        <button
          v-for="t in terminalTabs"
          :key="t.id"
          class="px-3 h-9 text-xs flex items-center gap-1.5 border-b-2 transition-colors"
          :class="terminalTab === t.id
            ? 'text-white border-primary'
            : 'text-foreground-muted border-transparent hover:text-white'"
          @click="terminalTab = t.id; terminalOpen = true"
        >
          <component
            :is="t.icon"
            class="w-3 h-3"
          />
          {{ t.label }}
          <span
            v-if="t.badge"
            class="ml-1 text-[10px] px-1.5 rounded bg-surface-hover text-foreground-muted"
          >{{ t.badge }}</span>
        </button>
        <div class="ml-auto flex items-center gap-1">
          <button
            v-if="terminalTab === 'test'"
            :disabled="!canTest || invoking"
            class="run-btn"
            :title="canTest ? 'Invoke with the payload below' : 'Deploy first'"
            @click="invokeFunction"
          >
            <Play
              v-if="!invoking"
              class="w-3 h-3"
            />
            <span
              v-else
              class="run-spinner"
            />
            Run
          </button>
          <button
            class="p-1.5 rounded text-foreground-muted hover:text-white hover:bg-surface-hover transition-colors"
            :title="terminalOpen ? 'Collapse' : 'Expand'"
            @click="terminalOpen = !terminalOpen"
          >
            <ChevronDown
              class="w-4 h-4 transition-transform"
              :class="terminalOpen ? 'rotate-0' : 'rotate-180'"
            />
          </button>
        </div>
      </div>
      <div
        v-show="terminalOpen"
        class="h-48 overflow-y-auto bg-background"
      >
        <!-- Build logs tab -->
        <div
          v-if="terminalTab === 'build'"
          class="p-3 font-mono text-xs space-y-0.5"
        >
          <div
            v-if="!buildLogs.length"
            class="text-foreground-muted"
          >
            No build activity yet. Deploy the function to stream logs here.
          </div>
          <div
            v-for="(log, idx) in buildLogs"
            :key="idx"
            class="text-foreground-muted whitespace-pre-wrap break-words"
          >
            {{ log }}
          </div>
        </div>

        <!-- Test tab — VS-Code-style split: request on the left, response
             on the right. Each side has its own column header that
             matches the surrounding tab strip's micro-label rhythm so
             the divider line and label baselines align across both
             columns. v0.4 B3: method/path/headers/body controls + saved
             fixtures popover land in the request column. -->
        <div
          v-else-if="terminalTab === 'test'"
          class="grid grid-cols-1 md:grid-cols-[minmax(0,1fr)_minmax(0,1.3fr)] h-full"
        >
          <!-- Request column -->
          <div class="flex flex-col min-h-0 border-b md:border-b-0 md:border-r border-border">
            <!-- Method + path + Saved popover sit on the column header. -->
            <div class="h-7 px-2 flex items-center gap-1.5 bg-surface/60 border-b border-border shrink-0">
              <select
                v-model="testMethod"
                :disabled="!canTest"
                class="text-[11px] font-mono bg-background border border-border rounded px-1.5 py-0.5 text-foreground focus:outline-none focus:ring-1 focus:ring-primary disabled:opacity-50"
              >
                <option v-for="m in methods" :key="m" :value="m">{{ m }}</option>
              </select>
              <input
                v-model="testPath"
                :disabled="!canTest"
                spellcheck="false"
                placeholder="/"
                class="flex-1 min-w-0 text-[11px] font-mono bg-background border border-border rounded px-2 py-0.5 text-foreground focus:outline-none focus:ring-1 focus:ring-primary disabled:opacity-50"
              >
              <div class="relative shrink-0">
                <button
                  type="button"
                  class="text-[11px] font-medium text-foreground-muted hover:text-white px-1.5 py-0.5 rounded hover:bg-surface-hover transition-colors flex items-center gap-1"
                  :disabled="!fnId"
                  :title="fnId ? 'Saved fixtures for this function' : 'Deploy first'"
                  @click="toggleSavedPopover"
                >
                  Saved
                  <span
                    v-if="fixtures.length"
                    class="text-[10px] text-foreground-muted"
                  >· {{ fixtures.length }}</span>
                  <ChevronDown class="w-3 h-3" />
                </button>
                <div
                  v-if="savedPopoverOpen"
                  class="absolute right-0 top-full mt-1 z-30 w-64 bg-surface border border-border rounded shadow-lg overflow-hidden"
                  @mouseleave="savedPopoverOpen = false"
                >
                  <div class="px-3 py-2 text-[10px] uppercase tracking-[0.14em] text-foreground-muted/80 border-b border-border bg-surface/60">
                    Saved fixtures
                  </div>
                  <div
                    v-if="!fixtures.length"
                    class="px-3 py-3 text-[11px] text-foreground-muted italic"
                  >
                    No fixtures yet. Set up a request and click Save.
                  </div>
                  <ul
                    v-else
                    class="max-h-56 overflow-y-auto"
                  >
                    <li
                      v-for="fx in fixtures"
                      :key="fx.id"
                      class="flex items-center gap-2 px-3 py-1.5 text-xs hover:bg-surface-hover cursor-pointer group"
                      @click="loadFixture(fx)"
                    >
                      <span class="font-mono text-[10px] text-foreground-muted shrink-0">{{ fx.method }}</span>
                      <span class="truncate flex-1 text-foreground">{{ fx.name }}</span>
                      <!--
                        Below lg the delete affordance is permanently
                        visible (touch devices have no hover state).
                        From lg up it fades in on row hover so the
                        fixtures list stays calm at rest on desktop.
                      -->
                      <button
                        type="button"
                        class="opacity-100 lg:opacity-0 lg:group-hover:opacity-100 text-foreground-muted hover:text-red-400 transition-opacity"
                        :title="`Delete ${fx.name}`"
                        :aria-label="`Delete ${fx.name}`"
                        @click.stop="removeFixture(fx)"
                      >
                        <Trash2 class="w-3 h-3" />
                      </button>
                    </li>
                  </ul>
                  <div class="border-t border-border px-2 py-1.5 bg-surface/50">
                    <button
                      type="button"
                      class="text-[11px] text-foreground hover:text-white w-full text-left px-1.5 py-1 rounded hover:bg-surface-hover transition-colors disabled:opacity-50"
                      :disabled="!canTest"
                      @click="saveCurrentAsFixture"
                    >
                      + Save current as…
                    </button>
                  </div>
                </div>
              </div>
            </div>

            <!-- Headers (collapsible). -->
            <div class="border-b border-border shrink-0">
              <button
                type="button"
                class="w-full h-6 px-3 flex items-center justify-between text-[10px] uppercase tracking-[0.14em] text-foreground-muted hover:text-white bg-surface/30 transition-colors"
                @click="headersOpen = !headersOpen"
              >
                <span>
                  Headers
                  <span
                    v-if="headerCount"
                    class="ml-1 text-foreground-muted/80 normal-case tracking-normal"
                  >· {{ headerCount }}</span>
                </span>
                <ChevronDown
                  class="w-3 h-3 transition-transform"
                  :class="headersOpen ? 'rotate-0' : '-rotate-90'"
                />
              </button>
              <div v-if="headersOpen" class="px-2 py-2 space-y-1">
                <div
                  v-for="(h, idx) in testHeaders"
                  :key="idx"
                  class="flex items-center gap-1.5"
                >
                  <input
                    v-model="h.name"
                    :disabled="!canTest"
                    spellcheck="false"
                    placeholder="Header name"
                    class="flex-1 min-w-0 text-[11px] font-mono bg-background border border-border rounded px-2 py-0.5 text-foreground focus:outline-none focus:ring-1 focus:ring-primary disabled:opacity-50"
                  >
                  <input
                    v-model="h.value"
                    :disabled="!canTest"
                    spellcheck="false"
                    placeholder="value"
                    class="flex-1 min-w-0 text-[11px] font-mono bg-background border border-border rounded px-2 py-0.5 text-foreground focus:outline-none focus:ring-1 focus:ring-primary disabled:opacity-50"
                  >
                  <button
                    type="button"
                    class="text-foreground-muted hover:text-red-400 p-0.5 transition-colors"
                    title="Remove header"
                    @click="removeHeaderRow(idx)"
                  >
                    <X class="w-3 h-3" />
                  </button>
                </div>
                <button
                  type="button"
                  class="text-[11px] text-foreground-muted hover:text-white transition-colors px-1.5 py-0.5 rounded hover:bg-surface-hover"
                  :disabled="!canTest"
                  @click="addHeaderRow"
                >
                  + Add header
                </button>
              </div>
            </div>

            <!-- Body sub-header + textarea. -->
            <div class="h-6 px-3 flex items-center justify-between bg-surface/30 border-b border-border shrink-0">
              <span class="text-[10px] uppercase tracking-[0.14em] font-medium text-foreground-muted">
                Body
              </span>
              <span
                v-if="!canTest"
                class="text-[10px] text-amber-400/80"
              >Deploy first</span>
              <span
                v-else
                class="text-[10px] text-foreground-muted/70 font-mono"
              >{{ testPayload.length }} chars</span>
            </div>
            <textarea
              v-model="testPayload"
              :disabled="!canTest"
              spellcheck="false"
              class="flex-1 w-full min-h-0 bg-background text-xs font-mono p-3 text-foreground focus:outline-none resize-none disabled:opacity-50 placeholder:text-foreground-muted/50"
              placeholder="{}"
            />
          </div>

          <!-- Response + logs column -->
          <div class="flex flex-col min-h-0">
            <div class="h-7 px-3 flex items-center justify-between bg-surface/60 border-b border-border shrink-0">
              <span class="text-[10px] uppercase tracking-[0.14em] font-medium flex items-center gap-1.5"
                :class="error ? 'text-red-400' : output ? 'text-success' : 'text-foreground-muted'"
              >
                <span
                  class="w-1.5 h-1.5 rounded-full"
                  :class="error ? 'bg-red-400' : output ? 'bg-success' : 'bg-foreground-muted/40'"
                />
                {{ error ? 'Error' : output ? 'Response' : 'Idle' }}
              </span>
              <div class="flex items-center gap-2">
                <!-- v0.4 B4: AI Suggest-fix button. Only shows after a
                     failed run; assembles a debug prompt from the
                     in-memory editor state (no network fetch needed —
                     source, request, and stderr are all already loaded
                     in this pane) and writes it to the clipboard. -->
                <button
                  v-if="lastRunFailed"
                  type="button"
                  class="text-[10px] uppercase tracking-[0.14em] text-foreground-muted hover:text-white px-1.5 py-0.5 rounded hover:bg-surface-hover transition-colors flex items-center gap-1 disabled:opacity-50"
                  :disabled="suggestingFix"
                  title="Build a paste-ready debug prompt with source + request + stderr"
                  @click="suggestFix"
                >
                  <Sparkles class="w-3 h-3" />
                  Suggest fix
                </button>
                <span
                  v-if="duration"
                  class="text-[10px] text-foreground-muted/80 font-mono"
                >{{ status }} · {{ duration }}ms</span>
              </div>
            </div>

            <div class="flex-1 min-h-0 overflow-y-auto">
              <!-- Response body -->
              <pre
                v-if="output || error"
                class="px-3 py-2.5 font-mono text-xs whitespace-pre-wrap break-all leading-relaxed"
                :class="error ? 'text-red-200' : 'text-foreground'"
              >{{ output || error }}</pre>
              <div
                v-else
                class="px-3 py-3 text-xs text-foreground-muted italic"
              >
                Hit <span class="not-italic text-white">Run</span> to invoke this function with the request payload.
              </div>

              <!-- Function stdout/stderr — only when present, with its own
                   micro-divider so it doesn't blend into the response. -->
              <div
                v-if="invokeLogs.length"
                class="border-t border-border"
              >
                <div class="h-6 px-3 flex items-center text-[10px] uppercase tracking-[0.14em] text-foreground-muted/80 bg-surface/30">
                  Function logs · {{ invokeLogs.length }}
                </div>
                <div class="px-3 py-2 font-mono text-xs space-y-0.5">
                  <div
                    v-for="(log, idx) in invokeLogs"
                    :key="idx"
                    class="text-foreground-muted whitespace-pre-wrap break-words"
                  >
                    {{ log }}
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- ─────────────── Modals ─────────────── -->
    <Modal
      v-model="modals.settings"
      title="Function configuration"
      :icon="Settings2"
      size="md"
    >
      <div class="space-y-4">
        <div>
          <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5">Description</label>
          <textarea
            v-model="form.description"
            rows="2"
            placeholder="One-line summary of what this function does. Surfaces in MCP tool catalogs and the agent channel picker."
            class="w-full bg-surface-hover border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:border-white resize-y"
          />
        </div>
        <div>
          <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5 flex items-center justify-between">
            <span>Runtime</span>
            <span
              v-if="autoDetected && !runtimeManuallySet"
              class="text-[10px] normal-case tracking-normal text-success/80"
            >auto-detected</span>
          </label>
          <div class="grid grid-cols-2 gap-2">
            <button
              v-for="rt in runtimes"
              :key="rt.id"
              class="px-2 py-2 rounded border text-xs font-medium transition-colors duration-150 flex items-center justify-center"
              :class="form.runtime === rt.id ? 'bg-white text-black border-white' : 'bg-surface-hover text-foreground-muted border-border hover:border-foreground-muted'"
              @click="setRuntimeManual(rt.id)"
            >
              {{ rt.label }}
            </button>
          </div>
        </div>
        <div>
          <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5">Template</label>
          <select
            v-model="templateId"
            class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-white focus:border-white"
            @change="applyTemplate"
          >
            <option value="">
              Custom (blank)
            </option>
            <optgroup
              v-for="cat in groupedTemplates"
              :key="cat.label"
              :label="cat.label"
            >
              <option
                v-for="tpl in cat.items"
                :key="tpl.id"
                :value="tpl.id"
              >
                {{ tpl.label }}{{ tpl.cron ? ' · scheduled' : '' }}: {{ tpl.description }}
              </option>
            </optgroup>
          </select>
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
          <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide block">Concurrency</label>
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
              <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5">When at cap</label>
              <select
                v-model="form.concurrency_policy"
                :disabled="!form.max_concurrency"
                class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-white focus:border-white disabled:opacity-50"
              >
                <option value="queue">
                  Queue requests
                </option>
                <option value="reject">
                  Reject (429)
                </option>
              </select>
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
              class="mt-0.5 w-4 h-4 rounded border-border bg-background"
            >
            <div class="min-w-0">
              <div class="text-sm font-medium text-white flex items-center gap-2">
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
          <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide flex items-center gap-2">
            <Lock class="w-3.5 h-3.5" /> Invoke gate
          </label>
          <select
            v-model="form.auth_mode"
            class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-white focus:border-white"
          >
            <option value="none">
              Public, anyone can invoke
            </option>
            <option value="platform_key">
              Require Orva API key (server-to-server)
            </option>
            <option value="signed">
              Require HMAC signature (X-Orva-Signature)
            </option>
          </select>
          <p class="text-[11px] text-foreground-muted leading-snug">
            Public is the default, matches Cloudflare Workers and Vercel
            Functions. For end-user auth (JWT, session cookies), keep this on
            <span class="text-white">Public</span> and verify inside your
            handler. <span class="text-white">Signed</span> mode reads its key
            from the function secret <span class="font-mono">ORVA_SIGNING_SECRET</span>.
          </p>
        </div>

        <div class="border-t border-border pt-4 space-y-2">
          <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide block">Rate limit</label>
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
          <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide flex items-center gap-2">
            <Globe class="w-3.5 h-3.5" /> Custom routes
            <span
              v-if="routesLoading"
              class="text-[10px] normal-case tracking-normal text-foreground-muted"
            >loading…</span>
          </label>

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
                class="shrink-0 w-6 h-6 flex items-center justify-center rounded text-foreground-muted hover:text-red-400 hover:bg-surface transition-colors"
                title="Remove route"
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
                placeholder="/path or /prefix/*"
                class="flex-1 min-w-0 bg-background border border-border rounded-md px-2 py-1.5 text-xs font-mono text-foreground focus:outline-none focus:border-white"
              >
              <input
                v-model="newRoute.methods"
                placeholder="*"
                title="Comma-separated methods or * for any (default *)"
                class="w-20 bg-background border border-border rounded-md px-2 py-1.5 text-xs font-mono text-foreground focus:outline-none focus:border-white"
              >
              <button
                class="shrink-0 px-3 py-1.5 rounded-md bg-white text-black text-xs font-medium hover:bg-white/90 transition-colors"
                @click="saveNewRoute"
              >
                Add
              </button>
            </div>
            <p
              v-if="newRouteCollision"
              class="text-[11px] text-amber-400 leading-snug"
            >
              ⚠ <span class="font-mono">{{ newRouteCollision.path }}</span> already
              maps to function <span class="font-mono">{{ newRouteCollision.currentFunctionId.slice(0, 8) }}…</span>;
              clicking Add will remap it to this one.
            </p>
            <p
              v-if="routesError"
              class="text-[11px] text-red-400 leading-snug"
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
            placeholder="KEY"
            class="flex-1 min-w-0 bg-background border border-border rounded-md px-2 py-1.5 text-xs text-foreground focus:outline-none focus:border-white"
          >
          <input
            v-model="pair.value"
            placeholder="VALUE"
            class="flex-1 min-w-0 bg-background border border-border rounded-md px-2 py-1.5 text-xs text-foreground focus:outline-none focus:border-white"
          >
          <button
            class="shrink-0 w-7 h-7 flex items-center justify-center rounded text-foreground-muted hover:text-red-400 hover:bg-surface transition-colors"
            title="Remove"
            @click="removeEnvVar(idx)"
          >
            <X class="w-3.5 h-3.5" />
          </button>
        </div>
        <button
          class="text-xs text-foreground-muted hover:text-white transition-colors"
          @click="addEnvVar"
        >
          + Add variable
        </button>
        <p class="text-[11px] text-foreground-muted pt-2 border-t border-border">
          Plaintext at deploy time. Use <span class="text-white">Secrets</span> for sensitive values.
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
          class="w-full bg-surface-hover border border-border rounded-md text-xs font-mono p-3 text-foreground focus:outline-none focus:border-white resize-none min-h-[200px]"
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
            class="text-foreground-muted hover:text-red-400 transition-colors"
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
          class="flex items-center justify-between text-xs px-3 py-2 rounded border border-amber-500/30 bg-amber-500/5"
        >
          <div class="flex items-center gap-2 min-w-0">
            <span class="text-foreground-muted font-mono">{{ sec.name }}</span>
            <span class="text-[10px] uppercase tracking-wider text-amber-400/80">pending</span>
          </div>
          <button
            class="text-foreground-muted hover:text-red-400 transition-colors"
            @click="removePendingSecret(idx)"
          >
            <Trash2 class="w-3.5 h-3.5" />
          </button>
        </div>

        <div class="border-t border-border pt-3 space-y-2">
          <input
            v-model="secretForm.name"
            placeholder="SECRET_NAME"
            class="w-full bg-background border border-border rounded-md px-2 py-1.5 text-xs text-foreground focus:outline-none focus:border-white"
          >
          <input
            v-model="secretForm.value"
            placeholder="SECRET_VALUE"
            type="password"
            class="w-full bg-background border border-border rounded-md px-2 py-1.5 text-xs text-foreground focus:outline-none focus:border-white"
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
              class="px-1.5 py-0.5 rounded text-[10px] bg-success/15 text-success border border-success/30 shrink-0"
            >Active</span>
            <span
              class="font-mono text-foreground-muted truncate"
              :title="v.code_hash"
            >{{ v.short_hash }}</span>
            <span class="text-foreground-muted shrink-0">·</span>
            <span class="text-foreground-muted shrink-0">{{ new Date(v.created_at).toLocaleDateString() }}</span>
          </div>
          <button
            v-if="!v.is_active"
            :disabled="rollingBack"
            class="text-foreground-muted hover:text-white disabled:opacity-50 shrink-0 flex items-center gap-1"
            @click="rollbackToVersion(v)"
          >
            <RotateCcw class="w-3 h-3" /> Rollback
          </button>
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
          Export a single <code class="font-mono text-white">handler(event)</code> that returns an
          HTTP-shaped object. Orva injects env vars and secrets at spawn time.
        </p>
        <pre class="bg-surface border border-border rounded p-3 font-mono text-[12px] text-white overflow-x-auto whitespace-pre">{{ handlerHint }}</pre>
        <ul class="space-y-1 pl-4 list-disc marker:text-foreground-muted/50">
          <li><span class="text-white font-mono">event.body</span> is the raw request body (string or parsed JSON).</li>
          <li>Return <span class="text-white font-mono">{ statusCode, headers, body }</span>.</li>
          <li>Add packages via the <span class="text-white">Deps</span> panel (installed at build time.</li>
        </ul>
        <router-link
          to="/docs"
          class="inline-flex items-center gap-1 text-foreground-muted hover:text-white transition-colors"
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
          <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5">
            Function name
          </label>
          <div class="relative">
            <input
              ref="firstDeployNameInput"
              v-model="form.name"
              placeholder="my-function"
              class="w-full bg-background border border-border rounded-md pl-3 pr-10 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-white focus:border-white"
              @keydown.enter="confirmFirstDeploy"
            >
            <button
              type="button"
              class="absolute right-1.5 top-1/2 -translate-y-1/2 p-1.5 rounded text-foreground-muted hover:text-white hover:bg-surface-hover transition-colors"
              title="Re-roll a fresh name"
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
          <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5 flex items-center justify-between">
            <span>Runtime</span>
            <span
              v-if="autoDetected && !runtimeManuallySet"
              class="text-[10px] normal-case tracking-normal text-success/80"
            >auto-detected</span>
          </label>
          <div class="grid grid-cols-2 gap-2">
            <button
              v-for="rt in runtimes"
              :key="rt.id"
              class="px-2 py-2 rounded border text-xs font-medium transition-colors duration-150 flex items-center justify-center"
              :class="form.runtime === rt.id ? 'bg-white text-black border-white' : 'bg-surface-hover text-foreground-muted border-border hover:border-foreground-muted'"
              @click="setRuntimeManual(rt.id)"
            >
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
          variant="secondary"
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
import { ref, computed, defineAsyncComponent, h, onMounted, onBeforeUnmount, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { FileCode, UploadCloud, Play, Layers, KeyRound, ShieldCheck, RotateCcw, Copy, Check, BookOpen, ChevronDown, ExternalLink, Settings2, Variable, Package, X, Trash2, Terminal, Activity, Globe, Lock, Shuffle, Database, Sparkles, Webhook, Plug } from 'lucide-vue-next'
import Button from '@/components/common/Button.vue'
import Input from '@/components/common/Input.vue'
import Modal from '@/components/common/Modal.vue'

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
      h('div', { class: 'p-4 space-y-2 w-full font-mono text-xs text-foreground-muted/40' }, [
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
import { getApiKey } from '@/api/client'
import { copyText } from '@/utils/clipboard'
import { generateFunctionName } from '@/utils/funName'
import { templates, defaultCode, categoryOrder } from '@/templates'
import { rollbackFunction, listFixtures, createFixture, updateFixture, deleteFixture, invokeFunctionFull, listRoutes as apiListRoutes, setRoute as apiSetRoute, deleteRoute as apiDeleteRoute } from '@/api/endpoints'
import { copyFixSuggestionToClipboard } from '@/utils/aiPrompts'
import { useConfirmStore } from '@/stores/confirm'

const route = useRoute()
const router = useRouter()
const confirmStore = useConfirmStore()

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

// Click-outside listener: closes whichever menu is open if the click
// landed outside both dropdown roots. Esc keydown does the same on the
// global key handler. Mounted once via onMounted below.
const onDocClick = (e) => {
  if (!menus.value.config && !menus.value.bindings) return
  const inConfig = configMenuRef.value?.contains(e.target)
  const inBindings = bindingsMenuRef.value?.contains(e.target)
  if (!inConfig && !inBindings) closeMenus()
}
const onDocKey = (e) => {
  if (e.key === 'Escape' && (menus.value.config || menus.value.bindings)) closeMenus()
}

// Bottom terminal: two tabs only — Build (deploy progress) and Test
// (payload + response + function logs all in one place). The terminal
// auto-switches: Build during a deploy, Test when the user runs the
// function. No need to click between three different surfaces.
const terminalOpen = ref(true)
const terminalTab = ref('build')
const terminalTabs = computed(() => [
  { id: 'build', label: 'Build', icon: Terminal, badge: buildLogs.value.length || null },
  { id: 'test',  label: 'Test',  icon: Play,     badge: invokeLogs.value.length || null },
])

// Pretty runtime label for the editor strip — "python314" → "Python 3.14",
// "node24" → "Node.js 24". Anything we don't recognize falls back to the
// raw id so an unknown runtime still surfaces something visible.
const runtimeShort = (rt) => {
  if (!rt) return ''
  const m = /^(python|node)(\d)(\d+)$/.exec(rt)
  if (!m) return rt
  const family = m[1] === 'python' ? 'Python' : 'Node.js'
  const major = m[2]
  const minor = m[3]
  return m[1] === 'python' ? `${family} ${major}.${minor}` : `${family} ${major}`
}
const envVarCount = computed(() => envVars.value.filter((p) => p.key.trim()).length)

const code = ref('')
const form = ref({
  name: '',
  description: '',                // surfaces in list_functions, get_function, the channel picker, and as the MCP tool description
  runtime: 'python314',
  memory_mb: 64,
  cpus: 0.5,
  network_mode: 'none',          // 'none' | 'egress'
  max_concurrency: 0,             // 0 = unlimited
  concurrency_policy: 'queue',    // 'queue' | 'reject'
  auth_mode: 'none',              // 'none' | 'platform_key' | 'signed'
  rate_limit_per_min: 0,          // 0 = unlimited
})
const fnId = ref('')  // backend function ID

const testPayload = ref('{"name": "World"}')
// v0.4 B3: Postman-style request controls. testHeaders is an array of
// {name, value} pairs so the editor can render an ordered list with
// inline edit + delete; we collapse to a flat object on send. headersOpen
// keeps the headers section collapsed by default to stay focused on the
// body, which is still the primary input for most users.
const testMethod = ref('POST')
const testPath = ref('/')
const testHeaders = ref([])
const headersOpen = ref(false)
const methods = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE']
const fixtures = ref([])
const savedPopoverOpen = ref(false)
const headerCount = computed(() => testHeaders.value.filter((h) => h.name && h.name.trim()).length)
const deploying = ref(false)
const invoking = ref(false)
const deployedThisSession = ref(false)
const output = ref(null)
const error = ref(null)
const duration = ref(0)
const status = ref('')
const buildLogs = ref([])
const invokeLogs = ref([])
const urlCopied = ref(false)
// v0.4 B4 — Suggest-fix affordance for the Test pane. Tracks "did the
// most recent run fail" so the button hides on idle / success runs.
// Set true when status >= 500 OR the request threw before getting a
// response (network error, timeout); reset when the user runs again.
const lastRunFailed = ref(false)
const suggestingFix = ref(false)

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

const isEditing = computed(() => !!route.params.name)

// canTest: function has deployed code AND there's no active build in
// flight. While `deploying` is true, the warm pool may be holding stale
// code (or none, on a first deploy) so test invocations should wait.
const isDeployed = computed(() => isEditing.value || deployedThisSession.value)
const canTest = computed(() => isDeployed.value && !deploying.value)

// Supported runtimes: latest two stable majors per language. The user
// picks version explicitly; existing functions on EOL runtimes (node20,
// python312) are auto-migrated one step up on server startup.
const runtimes = [
  { id: 'python314', label: 'Python 3.14' },
  { id: 'python313', label: 'Python 3.13' },
  { id: 'node24',    label: 'Node.js 24 (LTS)' },
  { id: 'node22',    label: 'Node.js 22 (LTS)' },
]

const isPythonRuntime = (rt) => rt === 'python313' || rt === 'python314'
const isNodeRuntime   = (rt) => rt === 'node22' || rt === 'node24'

const fileName = computed(() => {
  if (isPythonRuntime(form.value.runtime)) return 'handler.py'
  if (isNodeRuntime(form.value.runtime))   return 'handler.js'
  return 'handler.js'
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
      form.value.runtime = 'python314'
      autoDetected.value = true
    } else if (lang === 'node' && !isNode) {
      form.value.runtime = 'node24'
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
    // Pre-fill the function's description from the template if the user
    // hasn't typed one yet — saves a step on quick-create flows and
    // ensures the function ships with a meaningful one-liner.
    if (selected.description && !form.value.description) {
      form.value.description = selected.description
    }
  }
}

// Templates grouped by category for the picker's <optgroup>s. Categories
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

// loadRoute synchronises Editor state with the current route. Vue
// Router REUSES the same component instance when navigating between
// /functions/new (create mode) and /functions/<name> (edit mode) — the
// route record is the same shape — so a one-shot onMounted is the
// wrong hook. Without this watcher, a user who lands on /functions/new
// first, then clicks an existing function, would carry the boilerplate
// code + create-mode form state into edit mode. The bug surfaces as
// "I clicked the function but it shows the deploy template" — the
// deploy template is just whatever the create-mode default seeded.
//
// The watcher fires `immediate: true` so this also handles the
// initial mount (no separate onMounted needed).
const resetEditorState = () => {
  fnId.value = ''
  form.value = {
    name: '',
    description: '',
    runtime: 'python314',
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
  invokeLogs.value = []
  output.value = null
  error.value = null
  duration.value = 0
  status.value = ''
  deployedThisSession.value = false
  versions.value = []
  fixtures.value = []
  // Test pane defaults — keep these consistent so a fresh /functions/new
  // visit always starts with the same blank canvas.
  testMethod.value = 'POST'
  testPath.value = '/'
  testHeaders.value = []
  testPayload.value = '{"name": "World"}'
  headersOpen.value = false
  autoDetected.value = false
  runtimeManuallySet.value = false
  templateId.value = ''
}

const loadRouteData = async () => {
  resetEditorState()

  if (route.params.name) {
    // Edit mode — load function metadata + actual deployed source code.
    try {
      const listRes = await apiClient.get('/functions')
      const fn = (listRes.data.functions || []).find(f => f.name === route.params.name)
      if (!fn) throw new Error('Function not found')

      fnId.value = fn.id
      form.value.name = fn.name
      form.value.description = fn.description || ''
      form.value.runtime = fn.runtime
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
      // v0.4 B3: pre-fill the test pane from a deep link. Used by the
      // Activity drawer's "Save as fixture" button which round-trips
      // through this route with the captured method/path/headers/body
      // ready to be reviewed and saved.
      applyPrefillFromQuery()
    } catch (e) {
      console.error("Failed to load function", e)
      error.value = "Failed to load function: " + (e.response?.data?.error?.message || e.message)
    }
  } else {
    // Create mode (/functions/new). Seed a friendly auto-generated
    // name so the field isn't empty. The user can edit it, clear it,
    // or hit the re-roll button next to it.
    setRuntime('python314')
    form.value.name = generateFunctionName()
  }
}

watch(() => route.params.name, loadRouteData, { immediate: true })

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

onMounted(() => {
  window.addEventListener('focus', onWindowFocus)
  document.addEventListener('mousedown', onDocClick)
  document.addEventListener('keydown', onDocKey)
  window.addEventListener('orva:deploy', onDeployShortcut)
})
onBeforeUnmount(() => {
  window.removeEventListener('focus', onWindowFocus)
  document.removeEventListener('mousedown', onDocClick)
  document.removeEventListener('keydown', onDocKey)
  window.removeEventListener('orva:deploy', onDeployShortcut)
})

// applyPrefillFromQuery reads a base64-encoded JSON request envelope from
// the `prefill` query param and populates the test pane. Used by the
// "Save as fixture" affordance in InvocationsLog.vue's Request panel:
// the drawer encodes the captured request and links here so the user
// lands on the editor with the form already filled in.
const applyPrefillFromQuery = () => {
  const raw = route.query.prefill
  if (!raw) return
  try {
    const decoded = atob(String(raw))
    const data = JSON.parse(decoded)
    if (data.method) testMethod.value = String(data.method).toUpperCase()
    if (data.path)   testPath.value = String(data.path)
    if (data.headers && typeof data.headers === 'object') {
      testHeaders.value = Object.entries(data.headers).map(([name, value]) => ({ name, value: String(value) }))
      if (testHeaders.value.length) headersOpen.value = true
    }
    if (data.body !== undefined) {
      testPayload.value = typeof data.body === 'string' ? data.body : JSON.stringify(data.body, null, 2)
    }
    // Drop the focus to the test panel so the user sees the prefill.
    terminalTab.value = 'test'
    terminalOpen.value = true
    // Strip the query param without triggering a fresh navigation
    // (router.replace keeps the same component instance).
    router.replace({ query: { ...route.query, prefill: undefined } })
  } catch {
    /* ignore malformed prefill */
  }
}

// Re-roll the auto-generated function name. No-op once the function
// has been deployed (the name is the routing identity at that point).
const rerollName = () => {
  if (isEditing.value || fnId.value) return
  form.value.name = generateFunctionName()
}

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

// Close any in-flight SSE stream when the user leaves the page so we don't
// keep a phantom connection open (and so the next page-load gets a fresh
// view of build state).
onBeforeUnmount(() => {
  if (activeStream) {
    try { activeStream.close() } catch {}
    activeStream = null
  }
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

  // Auto-switch to the Build tab so logs are visible without a click.
  terminalTab.value = 'build'
  terminalOpen.value = true
  deploying.value = true
  output.value = null
  error.value = null
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
            throw new Error('Function name conflict but not found in list')
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
    })

    const depId = deployRes.data.deployment_id
    if (!depId) {
      // Legacy synchronous response — older backend without async pipeline.
      buildLogs.value.push(`Deployed! Hash: ${deployRes.data.code_hash || 'unknown'}`)
      deployedThisSession.value = true
      deploying.value = false
      return
    }

    // Step 3: Stream the build via SSE. Test button stays disabled until
    // the stream emits `succeeded`. Deploying flag stays true so the
    // Deploy button keeps its loading state.
    buildLogs.value.push(`Build queued (${depId})`)
    await streamBuild(depId)
  } catch (err) {
    error.value = err.response?.data?.error?.message || err.message || 'Deployment failed'
    buildLogs.value.push(`Error: ${error.value}`)
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
let activeStream = null
const streamBuild = (depId) => new Promise((resolve) => {
  if (activeStream) {
    try { activeStream.close() } catch {}
    activeStream = null
  }
  const es = new EventSource(`/api/v1/deployments/${depId}/stream`)
  activeStream = es

  // settled becomes true once we've seen a terminal `succeeded`/`failed`
  // event. After that the server closes the stream — which fires `onerror`
  // with readyState=CLOSED. That's a normal termination, not a failure.
  let settled = false
  const finish = (ok, payload) => {
    if (settled) return
    settled = true
    try { es.close() } catch {}
    activeStream = null
    deploying.value = false
    if (ok) {
      deployedThisSession.value = true
      buildLogs.value.push(`✓ Build succeeded in ${payload?.duration_ms ?? '?'}ms`)
      // Helpful nudge: when the build finishes, jump to the Test tab so
      // the user can run their function with one click instead of two.
      terminalTab.value = 'test'
    } else {
      const msg = payload?.error_message || 'build failed (see logs)'
      error.value = msg
      buildLogs.value.push(`✗ Build failed: ${msg}`)
    }
    resolve()
  }

  es.addEventListener('log', (e) => {
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

// Collapse the editor's [{name, value}, …] header rows into a flat object
// for the request. Empty/whitespace-only names are dropped — the user
// often leaves an empty trailing row from the "+ Add header" button.
const buildHeadersObject = () => {
  const out = {}
  for (const h of testHeaders.value) {
    const k = (h.name || '').trim()
    if (!k) continue
    out[k] = h.value ?? ''
  }
  return out
}

// Default Content-Type for body-bearing methods. Keeping the test pane's
// behaviour roughly aligned with curl: if the user supplied a body but
// didn't explicitly set Content-Type, assume JSON (the most common case).
const ensureContentType = (headers, method, body) => {
  const m = (method || 'POST').toUpperCase()
  if (m === 'GET' || m === 'HEAD') return headers
  if (!body) return headers
  const hasCT = Object.keys(headers).some((k) => k.toLowerCase() === 'content-type')
  if (!hasCT) headers['Content-Type'] = 'application/json'
  return headers
}

// Invoke function by ID. v0.4 B3: routes through invokeFunctionFull so
// the method/path/headers from the Test pane round-trip correctly.
const invokeFunction = async () => {
  if (!fnId.value) {
    error.value = 'No function deployed yet'
    return
  }

  // Auto-show the Test tab + open terminal so the user sees the result
  // without juggling two surfaces.
  terminalTab.value = 'test'
  terminalOpen.value = true
  invoking.value = true
  output.value = null
  error.value = null
  invokeLogs.value = []
  // Reset the failed-run flag at the start of every run so the
  // Suggest-fix button stays in sync with THIS run's outcome, not a
  // stale one from a prior invocation.
  lastRunFailed.value = false

  try {
    const headers = ensureContentType(buildHeadersObject(), testMethod.value, testPayload.value)
    headers['X-Orva-API-Key'] = headers['X-Orva-API-Key'] || getApiKey()

    const res = await invokeFunctionFull(fnId.value, {
      method: testMethod.value,
      path: testPath.value || '/',
      headers,
      body: testPayload.value || '',
    })

    const text = typeof res.data === 'string' ? res.data : JSON.stringify(res.data)
    status.value = `${res.status}`
    duration.value = res.headers?.['x-orva-duration-ms'] || res.headers?.['X-Orva-Duration-MS'] || '?'

    try {
      output.value = JSON.stringify(JSON.parse(text), null, 2)
    } catch {
      output.value = text
    }
    // 2xx/3xx are happy paths; only flag 5xx so the Suggest-fix button
    // shows on real failures (a deliberate 4xx from an authz check is
    // not a bug to debug).
    if (typeof res.status === 'number' && res.status >= 500) {
      lastRunFailed.value = true
    }
  } catch (err) {
    // Axios surfaces non-2xx as throws — pull out body + status for the UI.
    if (err.response) {
      status.value = `${err.response.status}`
      const t = err.response.data
      error.value = typeof t === 'string' ? t : JSON.stringify(t)
      if (err.response.status >= 500) lastRunFailed.value = true
    } else {
      error.value = err.message || 'Invocation failed'
      status.value = 'Error'
      // Network-level error: no status, but the operator still wants
      // the AI to look at the source + payload + whatever the platform
      // wrote to stderr. Treat as a failure.
      lastRunFailed.value = true
    }
  } finally {
    invoking.value = false
  }
}

// v0.4 B4 — assemble a debug prompt for the most-recent failed run
// and write it to the clipboard. All inputs are already in memory:
// - source: the editor's `code` ref (what the user is currently
//   editing). This is the right thing to debug; if the user has made
//   uncommitted edits since the failed run, those edits are what they
//   want the AI to look at.
// - runtime: from form.runtime.
// - request preview: built from the test pane's method/path/headers/body.
// - stderr: joined from invokeLogs (the function logs panel content).
// - error/status: from the response panel's existing refs.
// NOTHING goes to the network from this handler.
const suggestFix = async () => {
  if (suggestingFix.value) return
  suggestingFix.value = true
  try {
    // Stitch invokeLogs into a single stderr-shaped string so the
    // prompt's <stderr> section reads like a real traceback.
    const stderrText = (invokeLogs.value || []).join('\n')
    // Normalise the request preview. Headers in testHeaders are an
    // ordered list of {name,value}; collapse to a flat object and
    // drop empty rows so the prompt isn't littered with blank pairs.
    const headersObj = {}
    for (const h of testHeaders.value || []) {
      if (h?.name && h.name.trim()) headersObj[h.name.trim()] = h.value || ''
    }
    const requestPreview = {
      method: testMethod.value || 'POST',
      path: testPath.value || '/',
      headers: headersObj,
      body: testPayload.value || '',
    }
    // status.value is a string ("500", "Error", …); coerce to a
    // number when it parses cleanly so the prompt's <error> line
    // reads "500 …" instead of "500 (string) …".
    const sc = /^\d+$/.test(status.value) ? Number(status.value) : status.value
    const ok = await copyFixSuggestionToClipboard({
      source: code.value || '',
      runtime: form.value.runtime || '',
      stderr: stderrText,
      requestPreview,
      errorMessage: error.value || '',
      statusCode: sc || '',
    })
    if (ok) {
      confirmStore.notify({
        title: 'Prompt copied',
        message: 'Paste into ChatGPT, Claude, or your AI tool of choice.',
      })
    } else {
      confirmStore.notify({
        title: 'Copy failed',
        message: 'Could not write to the clipboard. Try again, or copy the stderr by hand.',
        danger: true,
      })
    }
  } finally {
    suggestingFix.value = false
  }
}

// ── Fixtures (v0.4 B3) ──────────────────────────────────────────────
//
// A fixture is one saved (method, path, headers, body) preset attached
// to a function. We refresh them on first deploy / first test-tab open
// so the popover always reflects what the backend has. Concurrency note:
// the backend's UNIQUE(function_id, name) means create races resolve
// server-side via 409; the editor opts into the simpler "PUT acts as
// upsert" path so there's never a true race in normal use.
const addHeaderRow = () => {
  testHeaders.value.push({ name: '', value: '' })
  headersOpen.value = true
}
const removeHeaderRow = (idx) => {
  testHeaders.value.splice(idx, 1)
}

const refreshFixtures = async () => {
  if (!fnId.value) return
  try {
    const res = await listFixtures(fnId.value)
    fixtures.value = res.data?.fixtures || []
  } catch (e) {
    // Soft-fail — fixture popover stays empty if the backend is
    // unreachable; the rest of the editor still works.
    fixtures.value = []
  }
}

const toggleSavedPopover = async () => {
  savedPopoverOpen.value = !savedPopoverOpen.value
  if (savedPopoverOpen.value) {
    await refreshFixtures()
  }
}

// loadFixture populates the test pane from a saved fixture and closes
// the popover. We replace (not merge) headers so the operator never gets
// a surprise mash-up of "what I had typed" + "what the fixture saved".
const loadFixture = (fx) => {
  testMethod.value = fx.method || 'POST'
  testPath.value = fx.path || '/'
  testHeaders.value = Object.entries(fx.headers || {}).map(([name, value]) => ({ name, value }))
  if (testHeaders.value.length) headersOpen.value = true
  testPayload.value = fx.body || ''
  savedPopoverOpen.value = false
}

const removeFixture = async (fx) => {
  const ok = await confirmStore.ask({
    title: `Delete fixture "${fx.name}"?`,
    message: 'This only removes the saved request preset. Function code and execution history are untouched.',
    confirmLabel: 'Delete',
    danger: true,
  })
  if (!ok) return
  try {
    await deleteFixture(fnId.value, fx.name)
    await refreshFixtures()
  } catch (e) {
    confirmStore.notify({
      title: 'Delete failed',
      message: e.response?.data?.error?.message || e.message,
      danger: true,
    })
  }
}

const saveCurrentAsFixture = async () => {
  if (!fnId.value) return
  const raw = await confirmStore.prompt({
    title: 'Save fixture',
    message: 'Give this request a name so you can replay or share it later.',
    placeholder: 'e.g. happy-path, signed-stripe, empty-body',
    confirmLabel: 'Save',
  })
  const name = (raw || '').trim()
  if (!name) return
  const headers = buildHeadersObject()
  const body = {
    name,
    method: testMethod.value,
    path: testPath.value || '/',
    headers,
    body: testPayload.value || '',
  }
  try {
    // PUT-by-name is an upsert on (function_id, name) so re-saving with
    // the same name overwrites without a separate confirm step.
    await updateFixture(fnId.value, name, body)
    savedPopoverOpen.value = false
    await refreshFixtures()
  } catch (e) {
    confirmStore.notify({
      title: 'Save failed',
      message: e.response?.data?.error?.message || e.message,
      danger: true,
    })
  }
}

// Refresh fixtures whenever fnId becomes known (initial load for an
// existing function, or right after first deploy lands a fresh id).
watch(fnId, async (id) => {
  if (id) await refreshFixtures()
  else fixtures.value = []
})

// describeRollbackDiff compares the current function record against a
// deployment snapshot and returns human lines describing what's about to
// change. Used by the confirm dialog so the operator sees the blast
// radius before clicking Rollback. Format prioritises legibility over
// completeness — env vars are shown by name; numeric fields by old →
// new; identical fields are omitted.
const describeRollbackDiff = (fn, snap) => {
  const lines = []

  // Env vars: classify into added / removed / changed.
  const cur = fn.env_vars || {}
  const next = snap.env_vars || {}
  const added = Object.keys(next).filter((k) => !(k in cur))
  const removed = Object.keys(cur).filter((k) => !(k in next))
  const changed = Object.keys(next).filter((k) => k in cur && cur[k] !== next[k])
  if (added.length)   lines.push(`+ Add env: ${added.join(', ')}`)
  if (removed.length) lines.push(`- Remove env: ${removed.join(', ')}`)
  if (changed.length) lines.push(`~ Change env: ${changed.join(', ')}`)

  // Spawn config — only mention what differs.
  const num = (label, a, b, suffix = '') => {
    if (a !== b) lines.push(`~ ${label}: ${a}${suffix} → ${b}${suffix}`)
  }
  num('Memory', fn.memory_mb, snap.memory_mb, ' MB')
  num('CPUs', fn.cpus, snap.cpus)
  num('Timeout', fn.timeout_ms, snap.timeout_ms, ' ms')
  num('Network', fn.network_mode || 'none', snap.network_mode || 'none')
  num('Auth gate', fn.auth_mode || 'none', snap.auth_mode || 'none')
  num('Rate limit', fn.rate_limit_per_min || 0, snap.rate_limit_per_min || 0, '/min')
  num('Max concurrency', fn.max_concurrency || 0, snap.max_concurrency || 0)
  num('Concurrency policy', fn.concurrency_policy || 'queue', snap.concurrency_policy || 'queue')

  return lines
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
      const lines = describeRollbackDiff(fn, snap)
      if (lines.length) {
        diffMessage = `Rolling back to v${v.version} (code ${v.short_hash}) will also change:\n\n${lines.join('\n')}\n\nSecrets keep their current values; they aren't part of the rollback.`
      } else {
        diffMessage = `Rolling back to v${v.version} (code ${v.short_hash}). Settings and env are already identical, so only the code changes.`
      }
    }
  } catch (e) {
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
    error.value = err.response?.data?.error?.message || 'Failed to save secret'
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
    error.value = err.response?.data?.error?.message || 'Failed to delete secret'
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
  output.value = null
  error.value = null
  setRuntime('python314')
}
</script>

<style scoped>
/* Compact panel-trigger button used in the editor's top action bar.
   Visible height ~28 px keeps the toolbar dense for desktop operators;
   the ::before pseudo extends the click region vertically to 44 px so
   the WCAG 2.5.5 touch-target floor is met on phone-portrait without
   reflowing the toolbar. Vertical-only expansion (no horizontal) so
   adjacent buttons in the wrapping row don't get overlapping hit
   regions; the gap-2 between siblings stays unambiguous. */
.panel-btn {
  position: relative;
  display: inline-flex;
  align-items: center;
  gap: 0.375rem;
  padding: 0.375rem 0.625rem;
  border-radius: 0.375rem;
  border: 1px solid var(--color-border);
  background-color: var(--color-surface-hover);
  color: var(--color-foreground-muted);
  font-size: 11px;
  font-weight: 500;
  white-space: nowrap;
  transition: color 150ms ease, background-color 150ms ease, border-color 150ms ease;
}
.panel-btn::before {
  content: '';
  position: absolute;
  inset-inline: 0;
  inset-block: -8px;
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
}
.menu-item:hover,
.menu-item:focus-visible {
  background-color: var(--color-surface-hover);
  outline: none;
}
.menu-item + .menu-item {
  border-top: 1px solid var(--color-border);
}

/* Compact Run button in the terminal-tab strip. Smaller than panel-btn
   but distinguishable via the primary tint so the user spots the
   action immediately. Sits right-aligned next to the collapse chevron. */
.run-btn {
  display: inline-flex;
  align-items: center;
  gap: 0.3rem;
  padding: 0.2rem 0.55rem;
  border-radius: 0.3rem;
  border: 1px solid rgba(85, 63, 131, 0.55);
  background: rgba(85, 63, 131, 0.18);
  color: var(--color-foreground);
  font-size: 11px;
  font-weight: 500;
  cursor: pointer;
  transition: background 120ms ease, border-color 120ms ease, color 120ms ease;
}
.run-btn:hover:not(:disabled) {
  background: rgba(85, 63, 131, 0.32);
  border-color: var(--color-primary);
  color: white;
}
.run-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
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

</style>
