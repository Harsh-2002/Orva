<template>
  <div class="flex flex-col h-full">
    <!-- Top action bar: function name on the left, panel triggers in the
         middle, deploy/reset on the right. Wraps cleanly on narrow widths. -->
    <div class="flex flex-wrap items-center gap-2 pb-3 border-b border-border">
      <div class="flex items-center gap-2 mr-auto min-w-0">
        <FileCode class="w-4 h-4 text-foreground-muted shrink-0" />
        <input
          v-model="form.name"
          placeholder="my-function"
          :disabled="isEditing"
          class="bg-transparent border-0 text-sm font-medium text-white placeholder-foreground-muted focus:outline-none px-1 py-1 min-w-0 w-40"
        >
        <button
          v-if="!isEditing && !fnId"
          type="button"
          class="p-1 rounded text-foreground-muted hover:text-white hover:bg-surface-hover transition-colors shrink-0"
          title="Re-roll a fresh name"
          @click="rerollName"
        >
          <Shuffle class="w-3.5 h-3.5" />
        </button>
        <span class="text-[11px] text-foreground-muted font-medium tracking-tight">{{ runtimeShort(form.runtime) }}</span>
      </div>

      <button
        class="panel-btn"
        :title="`Configuration • runtime, memory, CPU`"
        @click="modals.settings = true"
      >
        <Settings2 class="w-3.5 h-3.5" /> Settings
      </button>
      <button
        class="panel-btn"
        title="Environment variables"
        @click="modals.envVars = true"
      >
        <Variable class="w-3.5 h-3.5" /> Env <span
          v-if="envVarCount"
          class="text-foreground-muted"
        >{{ envVarCount }}</span>
      </button>
      <button
        class="panel-btn"
        title="Dependencies (requirements.txt / package.json)"
        @click="modals.deps = true"
      >
        <Package class="w-3.5 h-3.5" /> Deps
      </button>
      <button
        class="panel-btn"
        title="Encrypted secrets"
        @click="modals.secrets = true"
      >
        <KeyRound class="w-3.5 h-3.5" /> Secrets <span
          v-if="totalSecretsCount"
          class="text-foreground-muted"
        >{{ totalSecretsCount }}</span>
      </button>
      <button
        v-if="isEditing"
        class="panel-btn"
        title="KV store · per-function key/value state"
        @click="router.push({ name: 'function-kv', params: { name: form.name } })"
      >
        <Database class="w-3.5 h-3.5" /> KV
      </button>
      <button
        v-if="isEditing && versions.length"
        class="panel-btn"
        title="Version history & rollback"
        @click="modals.versions = true"
      >
        <Layers class="w-3.5 h-3.5" /> Versions <span class="text-foreground-muted">{{ versions.length }}</span>
      </button>
      <button
        class="panel-btn"
        title="Quick handler reference"
        @click="modals.docs = true"
      >
        <BookOpen class="w-3.5 h-3.5" /> Docs
      </button>

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
                      <button
                        type="button"
                        class="opacity-0 group-hover:opacity-100 text-foreground-muted hover:text-red-400 transition-colors"
                        :title="`Delete ${fx.name}`"
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
              <span
                v-if="duration"
                class="text-[10px] text-foreground-muted/80 font-mono"
              >{{ status }} · {{ duration }}ms</span>
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
                {{ tpl.label }}{{ tpl.cron ? ' · scheduled' : '' }} — {{ tpl.description }}
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
              Public — anyone can invoke
            </option>
            <option value="platform_key">
              Require Orva API key (server-to-server)
            </option>
            <option value="signed">
              Require HMAC signature (X-Orva-Signature)
            </option>
          </select>
          <p class="text-[11px] text-foreground-muted leading-snug">
            Public is the default — matches Cloudflare Workers and Vercel
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
          <li>Add packages via the <span class="text-white">Deps</span> panel — installed at build time.</li>
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
            Lowercase, dash-separated. Used in the invoke URL — re-roll for a different combination.
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
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { FileCode, UploadCloud, Play, Layers, KeyRound, ShieldCheck, RotateCcw, Copy, Check, BookOpen, ChevronDown, ExternalLink, Settings2, Variable, Package, X, Trash2, Terminal, Activity, Globe, Lock, Shuffle, Database } from 'lucide-vue-next'
import Button from '@/components/common/Button.vue'
import Input from '@/components/common/Input.vue'
import CodeEditor from '@/components/common/CodeEditor.vue'
import Modal from '@/components/common/Modal.vue'
import apiClient from '@/api/client'
import { getApiKey } from '@/api/client'
import { copyText } from '@/utils/clipboard'
import { generateFunctionName } from '@/utils/funName'
import { templates, defaultCode, categoryOrder } from '@/templates'
import { rollbackFunction, listFixtures, createFixture, updateFixture, deleteFixture, invokeFunctionFull } from '@/api/endpoints'
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

// Invoke URL is built from window.location.origin so it works on localhost,
// custom IPs/ports, and behind reverse proxies with TLS termination — the
// browser's view of "where it reached the UI" is the right URL to invoke.
// Trailing slash matters: handler.path will be "/" instead of "" for the
// root, which is what most AWS/Lambda-style routers expect.
const invokeUrl = computed(() => {
  if (!fnId.value) return ''
  return `${window.location.origin}/fn/${fnId.value.replace(/^fn_/, '')}`
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

onMounted(async () => {
  if (route.params.name) {
    // Edit mode — load function metadata + actual deployed source code
    try {
      const listRes = await apiClient.get('/functions')
      const fn = (listRes.data.functions || []).find(f => f.name === route.params.name)
      if (!fn) throw new Error('Function not found')

      fnId.value = fn.id
      form.value.name = fn.name
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

      // Fetch actual deployed source (not a template).
      try {
        const srcRes = await apiClient.get(`/functions/${fn.id}/source`)
        if (srcRes.data.code) {
          code.value = srcRes.data.code
          dependencyText.value = srcRes.data.dependencies || ''
        } else {
          // Not yet deployed — show default template.
          code.value = defaultCode[fn.runtime] || ''
        }
      } catch {
        code.value = defaultCode[fn.runtime] || ''
      }

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
    // New mode — seed a friendly auto-generated name so the field
    // isn't empty. The user can edit it, clear it, or hit the re-roll
    // button next to it.
    setRuntime('python314')
    if (!form.value.name) {
      form.value.name = generateFunctionName()
    }
  }
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
  } catch (err) {
    // Axios surfaces non-2xx as throws — pull out body + status for the UI.
    if (err.response) {
      status.value = `${err.response.status}`
      const t = err.response.data
      error.value = typeof t === 'string' ? t : JSON.stringify(t)
    } else {
      error.value = err.message || 'Invocation failed'
      status.value = 'Error'
    }
  } finally {
    invoking.value = false
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
  // window.prompt is a deliberate choice — confirmStore is async-confirm,
  // not async-input, and we don't want a heavyweight modal for "name?".
  // Hooking up a richer dialog later is one localised swap.
  const name = (window.prompt('Save fixture as…') || '').trim()
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
        diffMessage = `Rolling back to v${v.version} (code ${v.short_hash}) will also change:\n\n${lines.join('\n')}\n\nSecrets keep their current values — they aren't part of the rollback.`
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
    // Re-pull function metadata + versions so the Active pill moves.
    const listRes = await apiClient.get('/functions')
    const fn = (listRes.data.functions || []).find((f) => f.id === fnId.value)
    if (fn) {
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
/* Compact panel-trigger button used in the editor's top action bar. */
.panel-btn {
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
.panel-btn:hover {
  color: var(--color-foreground);
  border-color: var(--color-foreground-muted);
}
.panel-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
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
