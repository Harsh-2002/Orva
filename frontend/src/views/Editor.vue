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
        <span class="text-[10px] text-foreground-muted font-mono uppercase tracking-wider">{{ runtimeShort(form.runtime) }}</span>
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
            class="panel-btn"
            @click="invokeFunction"
          >
            <Play class="w-3 h-3" /> Run
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

        <!-- Test tab: payload editor on the left, response + function
             stdout/stderr on the right. Single surface — no clicking
             between Test and Output. -->
        <div
          v-else-if="terminalTab === 'test'"
          class="grid grid-cols-1 md:grid-cols-2 gap-0 h-full"
        >
          <!-- Payload column -->
          <div class="p-3 border-b md:border-b-0 md:border-r border-border flex flex-col gap-2 min-h-0">
            <div class="flex items-center justify-between">
              <label class="text-[10px] uppercase tracking-wider text-foreground-muted">Payload (JSON)</label>
              <span
                v-if="!canTest"
                class="text-[10px] text-amber-400/70"
              >Deploy first</span>
            </div>
            <textarea
              v-model="testPayload"
              :disabled="!canTest"
              class="flex-1 w-full bg-surface-hover border border-border rounded text-xs font-mono p-2 text-foreground focus:outline-none focus:border-white resize-none disabled:opacity-50"
              placeholder="{}"
            />
          </div>

          <!-- Response + logs column -->
          <div class="p-3 flex flex-col gap-3 min-h-0 overflow-y-auto">
            <!-- Response panel -->
            <div
              v-if="output || error"
              class="rounded bg-surface border border-border text-xs font-mono break-all"
            >
              <div
                class="px-3 py-2 border-b border-border flex items-center justify-between text-[10px] uppercase tracking-wider"
                :class="error ? 'text-red-400' : 'text-success'"
              >
                <span>{{ error ? 'Error' : 'Response' }}</span>
                <span
                  v-if="duration"
                  class="text-foreground-muted normal-case tracking-normal"
                >{{ status }} · {{ duration }}ms</span>
              </div>
              <pre class="px-3 py-2 text-foreground whitespace-pre-wrap">{{ output || error }}</pre>
            </div>
            <div
              v-else
              class="text-xs text-foreground-muted italic"
            >
              Hit <span class="text-white not-italic">Run</span> to invoke this function with the payload on the left.
            </div>

            <!-- stdout/stderr from the function. Always shown when there
                 are entries — saves a tab switch. -->
            <div
              v-if="invokeLogs.length"
              class="rounded bg-surface border border-border text-xs"
            >
              <div class="px-3 py-2 border-b border-border text-[10px] uppercase tracking-wider text-foreground-muted">
                Function logs
              </div>
              <div class="px-3 py-2 font-mono space-y-0.5 max-h-40 overflow-y-auto">
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
              Custom
            </option>
            <option
              v-for="tpl in (templates[form.runtime] || [])"
              :key="tpl.id"
              :value="tpl.id"
            >
              {{ tpl.label }}
            </option>
          </select>
        </div>
        <div class="grid grid-cols-2 gap-3">
          <Input
            v-model.number="form.memory_mb"
            label="Memory (MB)"
            type="number"
            placeholder="128"
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
          <input
            ref="firstDeployNameInput"
            v-model="form.name"
            placeholder="my-function"
            class="w-full bg-background border border-border rounded-md px-3 py-2 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-white focus:border-white"
            @keydown.enter="confirmFirstDeploy"
          >
          <p class="text-[11px] text-foreground-muted mt-1.5">
            Lowercase, dash-separated. Used in the invoke URL.
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
            placeholder="128"
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
import { useRoute } from 'vue-router'
import { FileCode, UploadCloud, Play, Layers, KeyRound, ShieldCheck, RotateCcw, Copy, Check, BookOpen, ChevronDown, ExternalLink, Settings2, Variable, Package, X, Trash2, Terminal, Activity, Globe, Lock } from 'lucide-vue-next'
import Button from '@/components/common/Button.vue'
import Input from '@/components/common/Input.vue'
import CodeEditor from '@/components/common/CodeEditor.vue'
import Modal from '@/components/common/Modal.vue'
import apiClient from '@/api/client'
import { getApiKey } from '@/api/client'
import { copyText } from '@/utils/clipboard'
import { rollbackFunction } from '@/api/endpoints'
import { useConfirmStore } from '@/stores/confirm'

const route = useRoute()
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

const runtimeShort = (rt) => {
  if (!rt) return ''
  return rt.replace('python', 'py').replace('node', 'node')
}
const envVarCount = computed(() => envVars.value.filter((p) => p.key.trim()).length)

const code = ref('')
const form = ref({
  name: '',
  runtime: 'python314',
  memory_mb: 128,
  cpus: 0.5,
  network_mode: 'none',          // 'none' | 'egress'
  max_concurrency: 0,             // 0 = unlimited
  concurrency_policy: 'queue',    // 'queue' | 'reject'
  auth_mode: 'none',              // 'none' | 'platform_key' | 'signed'
  rate_limit_per_min: 0,          // 0 = unlimited
})
const fnId = ref('')  // backend function ID

const testPayload = ref('{"name": "World"}')
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
  return `${window.location.origin}/api/v1/invoke/${fnId.value}/`
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

// Default code + template snippets are the same across minor version
// bumps. Keys map to every supported runtime so the indexed lookups
// (defaultCode[fn.runtime], templates[fn.runtime]) don't return undefined.
const pythonHelloWorld = `import json

def handler(event):
    body = event.get("body", "{}") if isinstance(event.get("body"), str) else event.get("body", {})
    if isinstance(body, str):
        body = json.loads(body) if body else {}

    name = body.get("name", "World") if isinstance(body, dict) else "World"

    return {
        "statusCode": 200,
        "headers": {"Content-Type": "application/json"},
        "body": json.dumps({
            "message": f"Hello {name}!",
            "language": "Python"
        })
    }`

const nodeHelloWorld = `module.exports.handler = async function(event) {
  const body = typeof event.body === 'string'
    ? JSON.parse(event.body || '{}')
    : event.body || {};

  const name = body.name || 'World';

  return {
    statusCode: 200,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      message: \`Hello \${name}!\`,
      language: 'Node.js'
    })
  };
};`

const pythonEcho = `import json

def handler(event):
    return {
        "statusCode": 200,
        "headers": {"Content-Type": "application/json"},
        "body": json.dumps({"echo": event, "message": "Echo from Orva"})
    }`

const nodeEcho = `module.exports.handler = async function(event) {
  return {
    statusCode: 200,
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ echo: event, message: 'Echo from Orva' })
  };
};`

const defaultCode = {
  python313: pythonHelloWorld,
  python314: pythonHelloWorld,
  node22:    nodeHelloWorld,
  node24:    nodeHelloWorld,
}

const pythonTemplates = [
  { id: 'py-basic', label: 'Hello World', code: pythonHelloWorld, deps: '' },
  { id: 'py-echo',  label: 'JSON Echo',   code: pythonEcho,       deps: '' },
]
const nodeTemplates = [
  { id: 'node-basic', label: 'Hello World', code: nodeHelloWorld, deps: '' },
  { id: 'node-echo',  label: 'JSON Echo',   code: nodeEcho,       deps: '' },
]

const templates = {
  python313: pythonTemplates,
  python314: pythonTemplates,
  node22:    nodeTemplates,
  node24:    nodeTemplates,
}

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
    } catch (e) {
      console.error("Failed to load function", e)
      error.value = "Failed to load function: " + (e.response?.data?.error?.message || e.message)
    }
  } else {
    // New mode
    setRuntime('python314')
  }
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

// Invoke function by ID
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
    const payload = testPayload.value ? JSON.parse(testPayload.value) : {}

    const res = await fetch(`/api/v1/invoke/${fnId.value}`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Orva-API-Key': getApiKey(),
      },
      credentials: 'include',
      body: JSON.stringify(payload),
    })

    const text = await res.text()
    status.value = `${res.status}`
    duration.value = res.headers.get('X-Orva-Duration-MS') || '?'

    if (res.ok) {
      try {
        output.value = JSON.stringify(JSON.parse(text), null, 2)
      } catch {
        output.value = text
      }
    } else {
      error.value = text
    }
  } catch (err) {
    error.value = err.message || 'Invocation failed'
    status.value = 'Error'
  } finally {
    invoking.value = false
  }
}

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
</style>
