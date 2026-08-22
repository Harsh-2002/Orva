<template>
  <!--
    AISettingsPanel — the AI assistant's operator configuration, embedded as a
    card body in the centralized Settings page (migrated out of the old in-chat
    modal). Covers everything that is *configuration*: providers + encrypted API
    keys + base URL, the active provider/model, and the assistant's defaults
    (approval policy). The same active selection also remains
    available in the chat composer for quick switching while working.
  -->
  <div class="space-y-6">
    <!-- Providers -->
    <section class="space-y-3">
      <div>
        <h3 class="text-sm font-semibold text-foreground">
          Providers
        </h3>
        <p class="text-xs text-foreground-muted mt-1.5 max-w-prose leading-snug">
          Keys are encrypted at rest. Choose which provider and model Chat uses.
        </p>
      </div>

      <div
        v-if="store.providers.length"
        class="grid grid-cols-1 gap-3 sm:grid-cols-2 sm:items-start"
      >
        <div>
          <label
            for="ai-active-provider"
            class="text-xs font-medium text-foreground-muted uppercase tracking-wide"
          >Active provider</label>
          <select
            id="ai-active-provider"
            :value="store.selectedProviderId || ''"
            class="mt-1.5 h-10 w-full bg-background border border-border rounded-md text-sm px-3 text-foreground transition-colors duration-200 focus:outline-none focus:ring-1 focus:ring-white focus:border-white"
            @change="onSelectProvider"
          >
            <option
              v-for="p in store.providers"
              :key="p.id"
              :value="p.id"
            >
              {{ p.label ? `${p.provider} (${p.label})` : p.provider }}
            </option>
          </select>
        </div>
        <div>
          <label
            for="ai-active-model"
            class="text-xs font-medium text-foreground-muted uppercase tracking-wide"
          >
            Active model
          </label>
          <div class="mt-1.5">
            <ModelMenu
              wide
              trigger-id="ai-active-model"
            />
          </div>
        </div>
        <p
          v-if="store.modelsError"
          class="text-xs text-danger-fg sm:col-span-2"
        >
          Models could not be loaded. Check the provider endpoint and credentials.
        </p>
      </div>

      <div
        v-if="store.providers.length"
        class="divide-y divide-border border-y border-border"
      >
        <div
          v-for="p in store.providers"
          :key="p.id"
          class="py-1"
        >
          <!--
            The row wraps instead of overflowing: html/body clip overflow-x
            globally, so a labelled provider pushed the rightmost item — the
            Remove button — off the edge with no scrollbar, and the provider
            could not be deleted from a phone at all. Names and labels shrink
            (min-w-0 + truncate); the chips and the action never do.
            `active` reads --color-link, not --color-primary: primary as a
            foreground on its own /15 tint is 2.5:1, under the 4.5:1 this
            11px chip needs.
          -->
          <div class="flex flex-wrap items-center gap-2 px-3 py-2">
            <span class="min-w-0 truncate font-mono text-sm text-foreground">{{ p.provider }}</span>
            <span
              v-if="p.label"
              class="min-w-0 truncate text-xs text-foreground-muted"
            >{{ p.label }}</span>
            <span
              v-if="store.selectedProviderId === p.id"
              class="shrink-0 text-xs uppercase tracking-label rounded px-1.5 py-0.5 bg-primary/15 text-link"
            >active</span>
            <span
              class="shrink-0 text-xs uppercase tracking-label rounded px-1.5 py-0.5"
              :class="p.has_key ? 'bg-success-tint text-success-fg' : 'bg-surface-hover text-foreground-muted'"
            >{{ p.has_key ? 'key set' : 'no key' }}</span>
            <Button
              size="xs"
              variant="ghost"
              class="ml-auto shrink-0"
              @click="onRemoveProvider(p.id)"
            >
              Remove
            </Button>
          </div>
        </div>
      </div>
      <!-- Outside the divide-y wrapper: with no providers it drew two stray
           rules around flush-left text. -->
      <p
        v-else
        class="px-3 py-2 text-xs text-foreground-muted"
      >
        No providers configured yet.
      </p>
    </section>

    <!-- Add / update provider -->
    <details class="group border-t border-border pt-4">
      <summary class="flex cursor-pointer list-none items-center justify-between rounded-sm text-sm font-semibold text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary">
        Add provider
        <span
          class="text-foreground-muted transition-transform group-open:rotate-45"
          aria-hidden="true"
        >+</span>
      </summary>
      <div class="mt-4 space-y-3">
        <p class="text-xs text-foreground-muted max-w-prose leading-snug">
          For compatible endpoints, choose <span class="font-mono">openai</span> and add the Base URL.
        </p>
        <div>
          <label
            for="ai-provider"
            class="text-xs font-medium text-foreground-muted uppercase tracking-wide"
          >Provider</label>
          <select
            id="ai-provider"
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
          :label="baseURLRequired ? 'Base URL (required)' : 'Base URL (optional)'"
          :placeholder="baseURLPlaceholder"
          :hint="baseURLHint"
          :error="baseURLError"
          :required="baseURLRequired"
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
          :disabled="!providerFormValid"
          @click="onSaveProvider"
        >
          Save provider
        </Button>
      </div>
    </details>

    <!-- Defaults -->
    <section
      v-if="store.settings"
      class="space-y-3 border-t border-border pt-5"
    >
      <h3 class="text-sm font-semibold text-foreground">
        Defaults
      </h3>
      <fieldset class="space-y-2">
        <legend class="text-xs font-medium text-foreground-muted uppercase tracking-wide">
          Approval policy
        </legend>
        <p class="text-xs text-foreground-muted leading-snug">
          Choose when changes require confirmation.
        </p>
        <label
          v-for="opt in APPROVAL_OPTIONS"
          :key="opt.value"
          class="flex cursor-pointer items-start gap-3 px-1 py-2 transition-colors hover:bg-surface-hover focus-within:outline-none focus-within:ring-2 focus-within:ring-inset focus-within:ring-primary"
        >
          <input
            v-model="store.settings.approval_policy"
            type="radio"
            name="approval-policy"
            :value="opt.value"
            class="sr-only"
          >
          <!--
            The chosen option carries --color-foreground, not --color-primary:
            violet on the near-black panel is 2.25:1, which is both under the
            3:1 that WCAG 1.4.11 asks of a state indicator and *dimmer than the
            unselected ring* — the live approval policy was the hardest of the
            three rows to read, on the one setting an operator opens this panel
            to confirm.
          -->
          <span
            class="mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full border transition-colors"
            :class="store.settings.approval_policy === opt.value ? 'border-foreground' : 'border-foreground-muted/50'"
          >
            <span
              v-if="store.settings.approval_policy === opt.value"
              class="h-2 w-2 rounded-full bg-foreground"
            />
          </span>
          <span class="min-w-0">
            <span class="block text-sm font-medium text-foreground">{{ opt.label }}</span>
            <span class="mt-0.5 block text-xs text-foreground-muted leading-snug">{{ opt.hint }}</span>
          </span>
        </label>
      </fieldset>
      <Button
        variant="primary"
        :loading="savingSettings"
        @click="onSaveSettings"
      >
        Save defaults
      </Button>
      <p
        v-if="settingsError"
        class="mt-2 text-xs text-danger"
      >
        {{ settingsError }}
      </p>
    </section>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import Button from '@/components/common/Button.vue'
import Input from '@/components/common/Input.vue'
import ModelMenu from '@/components/ai/ModelMenu.vue'
import { useAIStore } from '@/stores/ai'
import { useConfirmStore } from '@/stores/confirm'

const store = useAIStore()
const confirm = useConfirmStore()

// Removing a provider deletes its encrypted key irreversibly — confirm first.
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
    hint: 'Confirm creates, updates, and deletes.',
  },
  {
    value: 'destructive_only',
    label: 'Ask before deletes only',
    hint: 'Confirm deletes; run other changes automatically.',
  },
  {
    value: 'auto',
    label: 'Bypass: allow everything',
    hint: 'Run every action without confirmation.',
  },
]

const form = ref({ provider: 'openai', label: '', api_key: '', base_url: '' })

// Providers the gateway cannot reach without an explicit base URL. Every other
// provider has a known endpoint, so the field really is optional there — but
// ollama is only ever self-hosted and has no default to fall back on, and
// calling the field "optional" for it meant selecting ollama and saving
// produced a provider that could not answer a single turn.
const BASE_URL_REQUIRED = ['ollama']
const baseURLRequired = computed(() => BASE_URL_REQUIRED.includes(form.value.provider))
const baseURLError = computed(() =>
  baseURLRequired.value && !form.value.base_url.trim()
    ? 'Base URL is required for Ollama.'
    : '')
const providerFormValid = computed(() => Boolean(form.value.provider) && !baseURLError.value)

const baseURLPlaceholder = computed(() =>
  baseURLRequired.value
    ? 'http://192.168.1.50:11434  or  http://ollama.lan:11434'
    : 'https://api.openai.com/v1  or  https://your-host/v1')

const baseURLHint = computed(() =>
  baseURLRequired.value
    ? 'Where your server is listening. A private LAN address works; Orva permits it when your egress blocklist does.'
    : 'For custom / self-hosted endpoints. Either with or without /v1 works. A private LAN address works too.')
const savingProvider = ref(false)
const savingSettings = ref(false)
// The radios v-model straight onto the store, so a rejected save left the
// new choice displayed as though it had stuck. try/finally with no catch
// meant nothing ever surfaced the failure.
const settingsError = ref('')

onMounted(async () => {
  await store.loadSettings()
  await store.loadProviders()
})

async function onSelectProvider(event) {
  await store.selectProvider(event.target.value)
}

async function onSaveProvider() {
  if (!providerFormValid.value) return
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
  settingsError.value = ''
  try {
    await store.saveSettings(store.settings)
  } catch (e) {
    settingsError.value =
      e?.response?.data?.error?.message || e?.message || 'Failed to save settings'
    // Re-read so the UI stops showing a value the server rejected.
    try {
      await store.loadSettings?.()
    } catch { /* leave the error message as the signal */ }
  } finally {
    savingSettings.value = false
  }
}
</script>
