<template>
  <!--
    ModelMenu — the model selector in the composer toolbar. The trigger shows
    the active model name (compact, truncated); the popover lists every
    configured provider (when more than one) and the models the active provider
    reports live from its /v1/models endpoint. Selecting a model closes the menu.
    Replaces the old header ModelPicker's two native <select>s.
  -->
  <Popover title="Model">
    <template #trigger="{ toggle }">
      <button
        type="button"
        class="touch-expand-sm inline-flex items-center gap-1.5 h-8 max-w-[180px] px-2.5 rounded-lg text-xs text-foreground-muted hover:text-foreground hover:bg-surface-hover transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-surface"
        :title="store.selectedModel || 'Select model'"
        aria-label="Select model"
        @click="toggle"
      >
        <Cpu class="w-3.5 h-3.5 shrink-0" />
        <span class="truncate font-mono">{{ store.selectedModel || 'No model' }}</span>
        <ChevronDown class="w-3 h-3 shrink-0 opacity-70" />
      </button>
    </template>

    <template #default="{ close }">
      <div class="max-h-[60dvh] overflow-y-auto scrollable py-1">
        <!-- Provider switch (only when there's more than one configured). -->
        <template v-if="store.providers.length > 1">
          <p class="px-3 pt-1.5 pb-1 text-[10px] uppercase tracking-label text-foreground-muted">
            Provider
          </p>
          <button
            v-for="p in store.providers"
            :key="p.id"
            type="button"
            class="flex w-full items-center gap-2.5 px-3 py-1.5 text-left text-sm transition-colors focus-visible:outline-none focus-visible:bg-surface-hover"
            :class="store.selectedProviderId === p.id ? 'text-white bg-primary/15' : 'text-foreground hover:bg-surface-hover'"
            @click="store.selectProvider(p.id)"
          >
            <span class="flex-1 truncate">{{ p.label || p.provider }}</span>
            <Check
              v-if="store.selectedProviderId === p.id"
              class="w-3.5 h-3.5 shrink-0 text-primary"
            />
          </button>
          <div class="my-1 border-t border-border" />
        </template>

        <p class="px-3 pt-1 pb-1 text-[10px] uppercase tracking-label text-foreground-muted">
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
                 ring, /50 placeholder, 16px on mobile so iOS doesn't zoom on
                 focus. bg-surface (not bg-background) for contrast against the
                 popover's bg-background panel. -->
            <input
              v-model="query"
              type="text"
              placeholder="Search models…"
              class="w-full rounded-md border border-border bg-surface py-1.5 pl-8 pr-2.5 text-base sm:text-sm text-foreground placeholder-foreground-muted/50 transition-colors duration-200 focus:border-white focus:outline-none focus:ring-1 focus:ring-white"
            >
          </div>
        </div>
        <p
          v-if="store.modelsLoading"
          class="px-3 py-2 text-sm text-foreground-muted"
        >
          Loading models…
        </p>
        <p
          v-else-if="!store.models.length"
          class="px-3 py-2 text-xs text-foreground-muted leading-snug"
        >
          {{ store.modelsError ? 'No models. Check the provider endpoint.' : 'No models reported.' }}
        </p>
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
          class="flex w-full items-center gap-2.5 px-3 py-1.5 text-left text-sm font-mono transition-colors focus-visible:outline-none focus-visible:bg-surface-hover"
          :class="store.selectedModel === m.id ? 'text-white bg-primary/15' : 'text-foreground hover:bg-surface-hover'"
          @click="pick(m.id, close)"
        >
          <span class="flex-1 truncate">{{ m.id }}</span>
          <Check
            v-if="store.selectedModel === m.id"
            class="w-3.5 h-3.5 shrink-0 text-primary"
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
import { useAIStore } from '@/stores/ai'

const store = useAIStore()

const query = ref('')

// Only surface the search box for genuinely long lists; short lists scan fine.
const showSearch = computed(() => store.models.length > 6)

// Subsequence fuzzy match: every query char must appear in order. Score rewards
// contiguous runs and earlier matches so the closest model id floats to the top.
function fuzzyScore(text, q) {
  let score = 0
  let ti = 0
  let prev = -1
  for (const ch of q) {
    const at = text.indexOf(ch, ti)
    if (at === -1) return -1
    score += at === prev + 1 ? 3 : 1 // contiguous run bonus
    score -= at - ti // penalise gaps from the last match
    prev = at
    ti = at + 1
  }
  return score
}

const filteredModels = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return store.models
  return store.models
    .map((m) => ({ m, s: fuzzyScore(m.id.toLowerCase(), q) }))
    .filter((x) => x.s >= 0)
    .sort((a, b) => b.s - a.s)
    .map((x) => x.m)
})

// Reset the filter when the provider changes (its model list is replaced).
watch(() => store.selectedProviderId, () => { query.value = '' })

function pick(id, close) {
  store.selectModel(id)
  query.value = ''
  close()
}
</script>
