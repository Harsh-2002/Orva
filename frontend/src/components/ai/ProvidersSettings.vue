<template>
  <!--
    Chat settings. Renders through the shared common/Modal.vue shell so it
    inherits the app's canonical modal contract: teleport, fade transition,
    focus trap + #app inert, Escape-to-close, role/aria-modal/aria-labelledby,
    the mobile bottom-sheet + safe-area layout, and the standard close button.
    The three sections each own their own save action, so the per-section
    primary buttons stay in the body; the footer carries a single Done.
  -->
  <Modal
    :model-value="modelValue"
    title="Chat settings"
    size="md"
    @update:model-value="$emit('update:modelValue', $event)"
  >
    <div class="space-y-6">
      <!-- Providers -->
      <section class="space-y-3">
        <div>
          <h3 class="text-sm font-semibold text-white tracking-tight">
            Providers
          </h3>
          <p class="text-xs text-foreground-muted mt-0.5">
            Bring your own keys. Keys are encrypted at rest; models are listed live from each provider. Pick the provider + model inside the chat.
          </p>
        </div>

        <div class="space-y-2">
          <div
            v-for="p in store.providers"
            :key="p.id"
            class="rounded-md bg-surface border border-border"
          >
            <div class="flex items-center gap-2 px-3 py-2">
              <span class="font-mono text-sm text-foreground">{{ p.provider }}</span>
              <span
                v-if="p.label"
                class="text-xs text-foreground-muted"
              >{{ p.label }}</span>
              <span
                class="text-[10px] uppercase tracking-label rounded px-1.5 py-0.5"
                :class="p.has_key ? 'bg-success-tint text-success-fg' : 'bg-surface-hover text-foreground-muted'"
              >{{ p.has_key ? 'key set' : 'no key' }}</span>
              <span class="flex-1" />
              <Button
                size="xs"
                variant="secondary"
                :loading="modelState(p.id).loading"
                @click="loadModels(p.id)"
              >
                Models
              </Button>
              <Button
                size="xs"
                variant="ghost"
                @click="onRemoveProvider(p.id)"
              >
                Remove
              </Button>
            </div>
            <!-- live model preview -->
            <div
              v-if="modelState(p.id).loaded"
              class="px-3 pb-2.5 -mt-0.5"
            >
              <p
                v-if="modelState(p.id).error"
                class="text-xs text-danger-fg"
              >
                {{ modelState(p.id).error }}
              </p>
              <p
                v-else-if="!modelState(p.id).models.length"
                class="text-xs text-foreground-muted"
              >
                No models reported by this endpoint.
              </p>
              <div
                v-else
                class="flex flex-wrap gap-1"
              >
                <span
                  v-for="m in modelState(p.id).models"
                  :key="m.id"
                  class="text-[11px] font-mono text-foreground-muted bg-surface-hover rounded px-1.5 py-0.5"
                >{{ m.id }}</span>
              </div>
            </div>
          </div>
          <p
            v-if="!store.providers.length"
            class="text-xs text-foreground-muted"
          >
            No providers configured yet.
          </p>
        </div>
      </section>

      <!-- Add / update provider -->
      <section class="space-y-3">
        <h3 class="text-sm font-semibold text-white tracking-tight">
          Add or update provider
        </h3>
        <p class="text-xs text-foreground-muted">
          For any OpenAI-compatible endpoint (self-hosted, vLLM, Together, …) choose <span class="font-mono">openai</span> and set the Base URL.
        </p>
        <div>
          <label class="text-xs font-medium text-foreground-muted uppercase tracking-wide">Provider</label>
          <select
            v-model="form.provider"
            class="mt-1.5 w-full bg-background border border-border rounded-md text-sm px-3 py-2 text-foreground transition-colors duration-200 focus:outline-none focus:ring-1 focus:ring-white focus:border-white"
          >
            <option
              v-for="opt in PROVIDERS"
              :key="opt"
              :value="opt"
            >
              {{ opt }}
            </option>
          </select>
        </div>
        <Input
          v-model="form.label"
          label="Label"
          placeholder="e.g. personal, work (optional)"
        />
        <Input
          v-model="form.base_url"
          label="Base URL (optional)"
          placeholder="https://api.openai.com/v1  or  https://your-host/v1"
          hint="For custom / self-hosted endpoints. Either with or without /v1 works."
        />
        <Input
          v-model="form.api_key"
          label="API key"
          type="password"
          placeholder="sk-…"
          hint="Stored encrypted; never shown again. Leave blank when updating to keep the current key."
        />
        <Button
          variant="primary"
          :loading="savingProvider"
          :disabled="!form.provider"
          @click="onSaveProvider"
        >
          Save provider
        </Button>
      </section>

      <!-- Defaults -->
      <section
        v-if="store.settings"
        class="space-y-3"
      >
        <h3 class="text-sm font-semibold text-white tracking-tight">
          Defaults
        </h3>
        <fieldset class="space-y-2">
          <legend class="text-xs font-medium text-foreground-muted uppercase tracking-wide">
            Approval policy
          </legend>
          <p class="text-xs text-foreground-muted leading-snug">
            Reads always run on their own. This controls when the assistant pauses for your OK before it changes anything.
          </p>
          <label
            v-for="opt in APPROVAL_OPTIONS"
            :key="opt.value"
            class="flex cursor-pointer items-start gap-3 rounded-md border px-3 py-2.5 transition-colors focus-within:outline-none focus-within:ring-2 focus-within:ring-inset focus-within:ring-primary"
            :class="store.settings.approval_policy === opt.value
              ? 'border-primary/50 bg-primary/10'
              : 'border-border bg-background hover:bg-surface-hover'"
          >
            <input
              v-model="store.settings.approval_policy"
              type="radio"
              name="approval-policy"
              :value="opt.value"
              class="sr-only"
            >
            <span
              class="mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full border transition-colors"
              :class="store.settings.approval_policy === opt.value ? 'border-primary' : 'border-foreground-muted/50'"
            >
              <span
                v-if="store.settings.approval_policy === opt.value"
                class="h-2 w-2 rounded-full bg-primary"
              />
            </span>
            <span class="min-w-0">
              <span class="block text-sm font-medium text-foreground">{{ opt.label }}</span>
              <span class="mt-0.5 block text-xs text-foreground-muted leading-snug">{{ opt.hint }}</span>
            </span>
          </label>
        </fieldset>
        <Input
          v-model="store.settings.max_tool_iterations"
          type="number"
          label="Tool steps per reply"
          hint="The most tool calls the assistant may chain while answering one message before it stops and responds. Higher allows more complex multi-step tasks; 25 is a sensible default."
        />
        <Button
          variant="primary"
          :loading="savingSettings"
          @click="onSaveSettings"
        >
          Save defaults
        </Button>
      </section>
    </div>

    <template #footer>
      <Button
        variant="secondary"
        @click="$emit('update:modelValue', false)"
      >
        Done
      </Button>
    </template>
  </Modal>
</template>

<script setup>
import { ref, watch } from 'vue'
import Modal from '@/components/common/Modal.vue'
import Button from '@/components/common/Button.vue'
import Input from '@/components/common/Input.vue'
import apiClient from '@/api/client'
import { useAIStore } from '@/stores/ai'
import { useConfirmStore } from '@/stores/confirm'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
})

defineEmits(['update:modelValue'])

const store = useAIStore()
const confirm = useConfirmStore()

// Removing a provider deletes its encrypted key irreversibly — confirm first,
// matching the conversation-delete pattern. The danger affordance lives in the
// dialog, so the row button stays a quiet ghost.
async function onRemoveProvider(id) {
  const ok = await confirm.ask({
    title: 'Remove provider?',
    message: 'This permanently removes the provider and its encrypted API key.',
    danger: true,
    confirmLabel: 'Remove',
  })
  if (ok) store.deleteProvider(id)
}

const PROVIDERS = ['openai', 'anthropic', 'groq', 'gemini', 'ollama', 'mistral', 'openrouter', 'xai', 'cohere']

// Approval policy in plain language. Values map 1:1 to the backend
// (all_writes | destructive_only | auto); only the wording changed.
const APPROVAL_OPTIONS = [
  {
    value: 'all_writes',
    label: 'Ask before changes (recommended)',
    hint: 'The assistant asks first before it creates, updates, or deletes anything.',
  },
  {
    value: 'destructive_only',
    label: 'Ask before deletes only',
    hint: 'Routine changes run on their own. Only destructive actions, like deleting, ask first.',
  },
  {
    value: 'auto',
    label: 'Bypass: allow everything',
    hint: 'Every action runs automatically with no prompts. Fastest, least safe.',
  },
]

const form = ref({ provider: 'openai', label: '', api_key: '', base_url: '' })
const savingProvider = ref(false)
const savingSettings = ref(false)

// Per-provider live model preview (separate from the chat's selected models).
const modelsState = ref({})
function modelState(id) {
  return modelsState.value[id] || { loading: false, loaded: false, models: [], error: '' }
}

// The shell keeps this component mounted (v-model controlled), so refresh
// providers + settings each time the modal opens rather than once on mount.
watch(
  () => props.modelValue,
  (open) => {
    if (open) {
      store.loadProviders()
      store.loadSettings()
    }
  },
  { immediate: true },
)

async function loadModels(id) {
  modelsState.value = { ...modelsState.value, [id]: { loading: true, loaded: false, models: [], error: '' } }
  try {
    const { data } = await apiClient.get(`/ai/providers/${id}/models`)
    modelsState.value = { ...modelsState.value, [id]: { loading: false, loaded: true, models: data.models || [], error: data.error || '' } }
  } catch (e) {
    modelsState.value = { ...modelsState.value, [id]: { loading: false, loaded: true, models: [], error: e.message } }
  }
}

async function onSaveProvider() {
  if (!form.value.provider) return
  savingProvider.value = true
  try {
    await store.saveProvider({
      provider: form.value.provider,
      label: form.value.label,
      api_key: form.value.api_key,
      base_url: form.value.base_url,
      enabled: true,
    })
    form.value.api_key = ''
  } finally {
    savingProvider.value = false
  }
}

async function onSaveSettings() {
  savingSettings.value = true
  try {
    store.settings.max_tool_iterations = Number(store.settings.max_tool_iterations) || 25
    await store.saveSettings(store.settings)
  } finally {
    savingSettings.value = false
  }
}
</script>
