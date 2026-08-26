<template>
  <!--
    ModelMenu — the shared model selector used by Settings and the chat
    composer. The trigger shows the active model name; the popover lists every
    configured provider (when more than one) and the models the active provider
    reports live from its /v1/models endpoint. Selecting a model closes the menu.
  -->
  <Popover
    title="Model"
    :wide="wide"
  >
    <template #trigger="{ open, toggle }">
      <button
        :id="triggerId || undefined"
        ref="triggerBtn"
        type="button"
        class="inline-flex items-center gap-1.5 transition-colors focus-visible:outline-none"
        :class="wide
          ? 'h-10 w-full justify-between rounded-md border border-border bg-background px-3 text-sm text-foreground hover:bg-surface-hover focus-visible:border-focus-ring focus-visible:ring-1 focus-visible:ring-focus-ring'
          : 'touch-expand-sm h-8 max-w-[min(24rem,48vw)] rounded-lg px-2.5 text-xs text-foreground-muted hover:text-foreground hover:bg-surface-hover focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-surface'"
        :title="store.selectedModel || 'Select model'"
        aria-label="Select model"
        aria-haspopup="menu"
        :aria-expanded="open"
        @click="onToggle(open, toggle)"
      >
        <Cpu class="w-3.5 h-3.5 shrink-0" />
        <!-- flex-1 + text-left, or the wide variant's justify-between leaves the
             label floating in the middle of the field while the compact variant
             left-aligns it. Same component, two different reading positions. -->
        <span class="min-w-0 flex-1 truncate text-left font-mono">{{ store.selectedModel || 'No model' }}</span>
        <ChevronDown class="w-3 h-3 shrink-0 opacity-70" />
      </button>
    </template>

    <template #default="{ close }">
      <div
        ref="panelBody"
        class="py-1"
        @keydown="(e) => onMenuKeydown(e, close)"
      >
        <!-- Provider switch (only when there's more than one configured). -->
        <template v-if="store.providers.length > 1">
          <p class="px-3 pt-1.5 pb-1 text-xs uppercase tracking-label text-foreground-muted">
            Provider
          </p>
          <button
            v-for="p in store.providers"
            :key="p.id"
            type="button"
            class="flex w-full items-center gap-2.5 px-3 py-1.5 touch-expand-sm text-left text-sm transition-colors focus-visible:outline-none focus-visible:bg-surface-hover"
            :class="store.selectedProviderId === p.id ? 'text-foreground-strong bg-primary/15' : 'text-foreground hover:bg-surface-hover'"
            role="menuitemradio"
            :aria-checked="store.selectedProviderId === p.id"
            @click="store.selectProvider(p.id)"
          >
            <span class="flex-1 truncate">{{ p.label || p.provider }}</span>
            <Check
              v-if="store.selectedProviderId === p.id"
              class="w-3.5 h-3.5 shrink-0 text-link"
            />
          </button>
          <div class="my-1 border-t border-border" />
        </template>

        <p class="px-3 pt-1 pb-1 text-xs uppercase tracking-label text-foreground-muted">
          Model
        </p>
        <!-- Fuzzy filter — only shown once a provider reports enough models to be
             worth searching; sticks to the top while the list scrolls below. -->
        <div
          v-if="showSearch"
          class="sticky top-0 z-10 bg-background px-3 pb-2 pt-0.5"
        >
          <div class="relative">
            <Search class="pointer-events-none absolute left-2.5 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-foreground-muted" />
            <!-- Matches the app's input vocabulary (Input.vue): white focus
                 ring, /50 placeholder, bg-surface (not bg-background) for
                 contrast against the popover's bg-background panel.
                 Deliberately no text-base here: it reads as the iOS anti-zoom
                 trick but computes to 15.2px under the 95% root, and a class
                 outranks the element selector the global coarse-pointer floor
                 in style.css uses, so setting it actually re-enabled the zoom
                 this comment used to claim it prevented. -->
            <input
              v-model="query"
              type="text"
              aria-label="Search models"
              placeholder="Search models…"
              class="w-full rounded-md border border-border bg-surface py-1.5 pl-8 pr-2.5 text-sm text-foreground placeholder-foreground-muted transition-colors duration-200 focus:border-focus-ring focus:outline-none focus:ring-1 focus:ring-focus-ring"
            >
          </div>
        </div>
        <p
          v-if="store.modelsLoading"
          class="px-3 py-2 text-sm text-foreground-muted"
        >
          Loading models…
        </p>
        <!-- The server returns 200 {models: [], error} when it cannot list
             models, and its own comment says "the UI should let the user
             type a model id manually". It never did: this branch was a dead
             end and Send stays disabled without a selection, so a configured
             provider whose /models call failed left the composer greyed out
             with no explanation and no way forward. -->
        <div
          v-else-if="!store.models.length"
          class="px-3 py-2 text-xs text-foreground-muted leading-snug space-y-2"
        >
          <p>
            {{ store.modelsError
              ? `Could not list models: ${store.modelsError}`
              : 'The provider reported no models.' }}
          </p>
          <p>Enter a model id directly:</p>
          <form
            class="flex gap-1.5"
            @submit.prevent="applyManualModel"
          >
            <input
              v-model="manualModel"
              type="text"
              aria-label="Model id"
              placeholder="e.g. claude-sonnet-5"
              class="min-w-0 flex-1 rounded-md border border-border bg-surface px-2 py-1.5 font-mono text-sm text-foreground placeholder-foreground-muted focus:border-focus-ring focus:outline-none"
            >
            <button
              type="submit"
              :disabled="!manualModel.trim()"
              class="touch-expand-sm rounded-md border border-border px-2 py-1.5 text-xs text-foreground hover:bg-surface-hover disabled:opacity-40"
            >
              Use
            </button>
          </form>
        </div>
        <p
          v-else-if="!filteredModels.length"
          class="px-3 py-2 text-xs text-foreground-muted leading-snug"
        >
          No models match “{{ query.trim() }}”.
        </p>
        <button
          v-for="m in filteredModels"
          v-else
          :key="m.id"
          type="button"
          class="flex w-full items-center gap-2.5 px-3 py-1.5 touch-expand-sm text-left text-sm font-mono transition-colors focus-visible:outline-none focus-visible:bg-surface-hover"
          :class="store.selectedModel === m.id ? 'text-foreground-strong bg-primary/15' : 'text-foreground hover:bg-surface-hover'"
          role="menuitemradio"
          :aria-checked="store.selectedModel === m.id"
          :title="m.id"
          @click="pick(m.id, close)"
        >
          <!-- title as well as truncate: the panel is wide enough for every id
               this provider currently reports, but provider catalogues grow and
               a truncated id is not one an operator can act on. -->
          <span class="flex-1 truncate">{{ m.id }}</span>
          <Check
            v-if="store.selectedModel === m.id"
            class="w-3.5 h-3.5 shrink-0 text-link"
          />
        </button>
      </div>
    </template>
  </Popover>
</template>

<script setup>
import { ref, computed, watch } from 'vue'
import { Cpu, ChevronDown, Check, Search } from '@lucide/vue'
import Popover from '@/components/common/Popover.vue'
import { useMenuFocus } from '@/composables/useMenuFocus'
import { useAIStore } from '@/stores/ai'
import { filterModels } from '@/utils/modelSearch'

defineProps({
  wide: { type: Boolean, default: false },
  triggerId: { type: String, default: '' },
})

const store = useAIStore()

// Popover teleports its panel to <body>; without moving focus into it, Tab from
// the trigger leaves the composer entirely and Escape never reaches the panel.
const triggerBtn = ref(null)
const panelBody = ref(null)
const { onMenuKeydown } = useMenuFocus(panelBody, triggerBtn)

const query = ref('')
// Free-text model id, for the case the server explicitly anticipates:
// ai_handler returns 200 {models: [], error} when listing fails and expects
// the UI to let the operator type one.
const manualModel = ref('')

// Only surface the search box for genuinely long lists; short lists scan fine.
const showSearch = computed(() => store.models.length > 6)

const filteredModels = computed(() => filterModels(store.models, query.value))

// Reset the filter when the provider changes (its model list is replaced).
watch(() => store.selectedProviderId, () => { query.value = '' })

function onToggle(open, toggle) {
  if (!open) query.value = ''
  toggle()
}

function pick(id, close) {
  store.selectModel(id)
  query.value = ''
  close()
}

function applyManualModel() {
  const id = manualModel.value.trim()
  if (!id) return
  store.selectModel(id)
  manualModel.value = ''
}
</script>
