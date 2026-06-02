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
        class="inline-flex items-center gap-1.5 h-8 max-w-[180px] px-2.5 rounded-lg text-xs text-foreground-muted hover:text-foreground hover:bg-surface-hover transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-surface"
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
        <button
          v-for="m in store.models"
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
import { Cpu, ChevronDown, Check } from 'lucide-vue-next'
import Popover from '@/components/common/Popover.vue'
import { useAIStore } from '@/stores/ai'

const store = useAIStore()

function pick(id, close) {
  store.selectModel(id)
  close()
}
</script>
