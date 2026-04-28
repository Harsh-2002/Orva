<template>
  <div class="flex flex-col h-full gap-6">
    <div class="flex items-center justify-between">
      <div>
        <h2 class="text-lg font-medium text-white tracking-tight">
          Function Editor
        </h2>
        <p class="text-sm text-foreground-muted">
          Write, deploy, and test your serverless functions.
        </p>
      </div>
      <div class="flex items-center gap-3">
        <router-link
          v-if="isEditing && form.name"
          :to="`/functions/${form.name}/deployments`"
          class="text-xs text-foreground-muted hover:text-white transition-colors"
        >
          Deployments →
        </router-link>
        <Button
          variant="secondary"
          @click="resetForm"
        >
          Reset
        </Button>
        <Button
          :loading="deploying"
          @click="deployFunction"
        >
          <UploadCloud class="w-4 h-4" />
          {{ isEditing ? 'Deploy New Version' : 'Deploy Function' }}
        </Button>
      </div>
    </div>
    
    <!-- Build Logs Panel -->
    <div
      v-if="buildLogs.length > 0"
      class="bg-background border border-border rounded-lg p-4"
    >
      <div class="text-xs font-medium text-white uppercase tracking-wide mb-2">
        Deployment Progress
      </div>
      <div class="space-y-1">
        <div
          v-for="(log, idx) in buildLogs"
          :key="idx"
          class="text-xs font-mono text-foreground-muted"
        >
          {{ log }}
        </div>
      </div>
    </div>

    <div class="flex-1 flex flex-col lg:flex-row gap-6 min-h-0">
      <!-- Left Column: Code Editor. On small screens stacks above the
           settings panel (order-1); the editor itself has a fixed-ish
           min-height so the page is usable on tablets. -->
      <div class="flex-1 flex flex-col min-w-0 bg-background border border-border rounded-lg overflow-hidden shadow-sm order-1 min-h-[400px] lg:min-h-0">
        <div class="h-10 border-b border-border flex items-center justify-between px-4 bg-surface">
          <div class="text-xs font-mono text-foreground-muted flex items-center gap-2">
            <FileCode class="w-3 h-3" />
            <span class="text-white">{{ fileName }}</span>
          </div>
          <div class="text-[10px] text-foreground-muted font-mono">
            {{ code.length }} chars
          </div>
        </div>
        <CodeEditor
          v-model="code"
          :language="form.runtime"
          class="flex-1"
        />
      </div>

      <!-- Right Column: Settings & Test. No internal scroll — page-level
           scroll handles overflow so the code editor area gets every pixel.
           At <lg this column drops below the editor (order-2 + w-full). -->
      <div class="w-full lg:w-72 flex flex-col gap-6 shrink-0 order-2">
        <!-- Configuration Card -->
        <div class="bg-background border border-border rounded-lg p-5 space-y-4 shadow-sm">
          <div class="text-xs font-bold text-white uppercase tracking-wider mb-2">
            Configuration
          </div>
          
          <Input 
            v-model="form.name" 
            label="Function Name" 
            placeholder="my-function" 
            :disabled="isEditing"
          />
          
          <div>
            <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-1.5">Runtime</label>
            <div class="grid grid-cols-2 gap-2">
              <button
                v-for="rt in runtimes"
                :key="rt.id"
                class="px-2 py-2 rounded border text-xs font-medium transition-all duration-200 flex flex-col items-center gap-1"
                :class="form.runtime === rt.id ? 'bg-white text-black border-white' : 'bg-surface-hover text-foreground-muted border-border hover:border-foreground-muted'"
                @click="setRuntime(rt.id)"
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
            placeholder="1" 
          />
        </div>

        <!-- Environment Variables -->
        <div class="bg-background border border-border rounded-lg p-5 space-y-3 shadow-sm">
          <div class="flex items-center justify-between">
            <div class="text-xs font-bold text-white uppercase tracking-wider">
              Environment Variables
            </div>
            <button
              class="text-xs text-foreground-muted hover:text-white"
              @click="addEnvVar"
            >
              + Add
            </button>
          </div>

          <div class="space-y-2">
            <div
              v-for="(pair, idx) in envVars"
              :key="idx"
              class="flex items-center gap-2"
            >
              <input
                v-model="pair.key"
                placeholder="KEY"
                class="flex-1 min-w-0 bg-background border border-border rounded-md px-2 py-1 text-xs text-foreground focus:outline-none focus:border-white"
              >
              <input
                v-model="pair.value"
                placeholder="VALUE"
                class="flex-1 min-w-0 bg-background border border-border rounded-md px-2 py-1 text-xs text-foreground focus:outline-none focus:border-white"
              >
              <button
                class="shrink-0 w-6 h-6 flex items-center justify-center rounded text-foreground-muted hover:text-red-400 hover:bg-surface transition-colors"
                title="Remove"
                @click="removeEnvVar(idx)"
              >
                ×
              </button>
            </div>
          </div>
        </div>

        <!-- Dependencies -->
        <div class="bg-background border border-border rounded-lg p-5 space-y-3 shadow-sm">
          <div class="text-xs font-bold text-white uppercase tracking-wider">
            Dependencies
          </div>
          <div class="text-[10px] text-foreground-muted">
            {{ dependencyFileName }}
          </div>
          <textarea
            v-model="dependencyText"
            class="w-full bg-surface-hover border border-border rounded-md text-xs font-mono p-3 text-foreground focus:outline-none focus:border-white resize-none min-h-[120px]"
            placeholder="Optional dependencies file content"
          />
        </div>

        <!-- Versions / Rollback -->
        <div
          v-if="isEditing && versions.length"
          class="bg-background border border-border rounded-lg p-5 space-y-3 shadow-sm"
        >
          <div class="flex items-center gap-2 text-xs font-bold text-white uppercase tracking-wider">
            <Layers class="w-4 h-4" /> Versions
          </div>
          <div class="space-y-2">
            <div
              v-for="v in versions"
              :key="v.deployment_id"
              class="flex items-center justify-between gap-2 text-xs"
            >
              <div class="flex items-center gap-2 min-w-0">
                <span
                  class="font-mono text-foreground-muted shrink-0"
                >v{{ v.version }}</span>
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
                class="text-foreground-muted hover:text-white disabled:opacity-50 shrink-0"
                @click="rollbackToVersion(v)"
              >
                <RotateCcw class="w-3 h-3 inline" /> Rollback
              </button>
            </div>
          </div>
        </div>

        <!-- Secrets -->
        <div
          v-if="isEditing"
          class="bg-background border border-border rounded-lg p-5 space-y-3 shadow-sm"
        >
          <div class="flex items-center gap-2 text-xs font-bold text-white uppercase tracking-wider">
            <KeyRound class="w-4 h-4" /> Secrets
          </div>

          <div class="space-y-2">
            <div
              v-for="sec in secrets"
              :key="sec.id"
              class="flex items-center justify-between text-xs"
            >
              <div class="text-foreground-muted">
                {{ sec.name }}
              </div>
              <button
                class="text-foreground-muted hover:text-red-400"
                @click="removeSecret(sec.id)"
              >
                Delete
              </button>
            </div>
            <div
              v-if="secrets.length === 0"
              class="text-xs text-foreground-muted"
            >
              No secrets yet.
            </div>
          </div>

          <div class="space-y-2">
            <input
              v-model="secretForm.name"
              placeholder="SECRET_NAME"
              class="w-full bg-background border border-border rounded-md px-2 py-1 text-xs text-foreground focus:outline-none focus:border-white"
            >
            <input
              v-model="secretForm.value"
              placeholder="SECRET_VALUE"
              class="w-full bg-background border border-border rounded-md px-2 py-1 text-xs text-foreground focus:outline-none focus:border-white"
            >
            <Button
              variant="secondary"
              class="w-full"
              @click="saveSecret"
            >
              <ShieldCheck class="w-4 h-4" /> Save Secret
            </Button>
          </div>
        </div>

        <!-- Invoke URL Card -->
        <div
          v-if="fnId"
          class="bg-background border border-border rounded-lg p-5 space-y-3 shadow-sm"
        >
          <div class="flex items-center justify-between gap-2">
            <div>
              <div class="text-xs font-bold text-white uppercase tracking-wider">
                Invoke URL
              </div>
              <div class="text-xs text-foreground-muted mt-1">
                Function ID
              </div>
            </div>
            <button
              class="px-3 py-2 rounded-md border border-border bg-surface-hover hover:bg-surface text-foreground-muted hover:text-white transition-colors flex items-center gap-1.5 text-xs shrink-0"
              :class="urlCopied ? 'text-success border-success/40' : ''"
              :title="urlCopied ? 'Copied!' : 'Copy invoke URL to clipboard'"
              @click="copyInvokeUrl"
            >
              <Check
                v-if="urlCopied"
                class="w-3.5 h-3.5"
              />
              <Copy
                v-else
                class="w-3.5 h-3.5"
              />
              {{ urlCopied ? 'Copied!' : 'Copy URL' }}
            </button>
          </div>
          <div class="font-mono text-sm text-white bg-surface px-3 py-2 rounded border border-border break-all">
            {{ fnId }}
          </div>
        </div>

        <!-- Testing Card -->
        <div class="bg-background border border-border rounded-lg p-5 space-y-4 shadow-sm flex-1 flex flex-col">
          <div class="text-xs font-bold text-white uppercase tracking-wider mb-2">
            Test Invocation
          </div>
          
          <div class="flex-1 flex flex-col gap-2 min-h-[100px]">
            <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide">Payload (JSON)</label>
            <textarea 
              v-model="testPayload"
              class="flex-1 w-full bg-surface-hover border border-border rounded-md text-xs font-mono p-3 text-foreground focus:outline-none focus:border-white resize-none"
              placeholder="{}"
            />
          </div>

          <Button 
            class="w-full" 
            :disabled="!canTest"
            :loading="invoking"
            @click="invokeFunction"
          >
            <Play class="w-4 h-4" />
            Run Function
          </Button>

          <!-- Output -->
          <div
            v-if="invokeLogs.length > 0"
            class="mt-4 p-3 rounded bg-surface border border-border text-xs font-mono break-all"
          >
            <div class="text-foreground-muted mb-1">
              Logs:
            </div>
            <div
              v-for="(log, idx) in invokeLogs"
              :key="idx"
              class="text-foreground-muted"
            >
              {{ log }}
            </div>
          </div>
          
          <div
            v-if="output || error"
            class="mt-4 p-3 rounded bg-surface border border-border text-xs font-mono break-all"
          >
            <div
              v-if="error"
              class="text-red-400 mb-1"
            >
              Error:
            </div>
            <div
              v-else
              class="text-green-400 mb-1"
            >
              Output:
            </div>
            <pre class="text-foreground whitespace-pre-wrap">{{ output || error }}</pre>
            <div
              v-if="duration"
              class="mt-2 pt-2 border-t border-border text-[10px] text-foreground-muted flex justify-between"
            >
              <span>Duration: {{ duration }}ms</span>
              <span>Status: {{ status }}</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onBeforeUnmount } from 'vue'
import { useRoute } from 'vue-router'
import { FileCode, UploadCloud, Play, Layers, KeyRound, ShieldCheck, RotateCcw, Copy, Check } from 'lucide-vue-next'
import Button from '@/components/common/Button.vue'
import Input from '@/components/common/Input.vue'
import CodeEditor from '@/components/common/CodeEditor.vue'
import apiClient from '@/api/client'
import { getApiKey } from '@/api/client'
import { copyText } from '@/utils/clipboard'
import { rollbackFunction } from '@/api/endpoints'

const route = useRoute()

const code = ref('')
const form = ref({
  name: '',
  runtime: 'python314',
  memory_mb: 128,
  cpus: 0.5,
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
    alert('Could not copy to clipboard. Select the URL manually:\n\n' + invokeUrl.value)
  }
}

const envVars = ref([{ key: '', value: '' }])
const dependencyText = ref('')
const templateId = ref('')
const versions = ref([])
const secrets = ref([])
const secretForm = ref({ name: '', value: '' })

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

// Deploy function — create if needed, then deploy code inline
const deployFunction = async () => {
  if (!form.value.name || !code.value) {
    alert("Please provide a function name and code")
    return
  }

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
      // Existing function — update config (memory, cpus, env_vars) so changes take effect.
      await apiClient.put(`/functions/${fnId.value}`, {
        memory_mb: form.value.memory_mb,
        cpus: form.value.cpus,
        env_vars: envVarsMap,
      })
      buildLogs.value.push('Updated function config')
    }

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

const rollingBack = ref(false)
const rollbackToVersion = async (v) => {
  if (!fnId.value || !v?.deployment_id || rollingBack.value) return
  if (!confirm(`Restore version v${v.version} (${v.short_hash})? Current version stays in the history.`)) return
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
      alert(`This version has been garbage-collected and can no longer be restored.\n\n${msg}`)
    } else {
      alert('Rollback failed: ' + msg)
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
  if (!fnId.value) {
    alert('Deploy the function first, then add secrets.')
    return
  }
  if (!secretForm.value.name.trim()) return
  try {
    await apiClient.post(`/functions/${fnId.value}/secrets`, {
      key: secretForm.value.name.trim(),
      value: secretForm.value.value,
    })
    secretForm.value.name = ''
    secretForm.value.value = ''
    await loadSecrets()
  } catch (err) {
    error.value = err.response?.data?.error?.message || 'Failed to save secret'
  }
}

const removeSecret = async (key) => {
  if (!fnId.value) return
  if (!confirm(`Delete secret "${key}"?`)) return
  try {
    await apiClient.delete(`/functions/${fnId.value}/secrets/${encodeURIComponent(key)}`)
    await loadSecrets()
  } catch (err) {
    error.value = err.response?.data?.error?.message || 'Failed to delete secret'
  }
}

const resetForm = () => {
  if (confirm("Reset editor?")) {
    form.value.name = ''
    fnId.value = ''
    code.value = ''
    deployedThisSession.value = false
    output.value = null
    error.value = null
    setRuntime('python314')
  }
}
</script>
