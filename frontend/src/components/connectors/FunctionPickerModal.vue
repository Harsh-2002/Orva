<template>
  <div
    class="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/60 backdrop-blur-sm"
    @click.self="$emit('close')"
  >
    <div class="w-full max-w-2xl bg-background border border-border rounded-lg shadow-2xl flex flex-col max-h-[80vh]">
      <!-- Header -->
      <div class="px-5 py-4 border-b border-border flex items-center justify-between">
        <div>
          <div class="text-sm font-semibold text-white">
            Pick functions
          </div>
          <div class="text-xs text-foreground-muted mt-0.5">
            Each selected function will be exposed as one MCP tool inside
            this connector. Names with dashes become snake_case in the
            tool catalog (e.g. <code class="text-foreground">stripe-charge</code> → <code class="text-foreground">stripe_charge</code>).
          </div>
        </div>
        <button
          class="text-foreground-muted hover:text-white"
          @click="$emit('close')"
        >
          <X class="w-4 h-4" />
        </button>
      </div>

      <!-- Search -->
      <div class="px-5 py-3 border-b border-border flex items-center gap-2">
        <Search class="w-4 h-4 text-foreground-muted" />
        <input
          v-model="search"
          type="text"
          placeholder="Filter by name or runtime"
          class="flex-1 bg-transparent text-sm text-foreground placeholder-foreground-muted focus:outline-none"
        >
        <span class="text-xs text-foreground-muted">{{ chosen.size }} selected</span>
      </div>

      <!-- List -->
      <div class="flex-1 overflow-y-auto">
        <div
          v-if="loading"
          class="px-5 py-8 text-center text-xs text-foreground-muted italic"
        >
          Loading functions…
        </div>
        <div
          v-else-if="filtered.length === 0"
          class="px-5 py-8 text-center text-xs text-foreground-muted"
        >
          No functions match.
        </div>
        <ul v-else class="divide-y divide-border">
          <li
            v-for="fn in filtered"
            :key="fn.id"
            class="px-5 py-3 flex items-center gap-3 hover:bg-surface/40 cursor-pointer transition-colors"
            @click="toggle(fn.id)"
          >
            <input
              type="checkbox"
              :checked="chosen.has(fn.id)"
              class="accent-primary"
              @click.stop="toggle(fn.id)"
            >
            <div class="flex-1 min-w-0">
              <div class="text-sm font-medium text-white truncate">{{ fn.name }}</div>
              <div
                v-if="fn.description"
                class="text-xs text-foreground-muted mt-0.5 line-clamp-1"
              >{{ fn.description }}</div>
            </div>
            <code class="text-[11px] text-foreground-muted">{{ fn.runtime }}</code>
          </li>
        </ul>
      </div>

      <!-- Footer -->
      <div class="px-5 py-3 border-t border-border flex items-center justify-between gap-3">
        <div class="text-xs text-foreground-muted">
          {{ chosen.size }} function{{ chosen.size === 1 ? '' : 's' }} selected
        </div>
        <div class="flex gap-2">
          <Button
            variant="secondary"
            @click="$emit('close')"
          >
            Cancel
          </Button>
          <Button
            :disabled="chosen.size === 0"
            @click="apply"
          >
            Apply
          </Button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { Search, X } from 'lucide-vue-next'
import Button from '@/components/common/Button.vue'
import { listFunctions } from '@/api/endpoints'

const props = defineProps({
  selected: { type: Array, default: () => [] },
})
const emit = defineEmits(['close', 'apply'])

const fns = ref([])
const loading = ref(true)
const search = ref('')
const chosen = ref(new Set(props.selected))

const toggle = (id) => {
  const s = new Set(chosen.value)
  if (s.has(id)) {
    s.delete(id)
  } else {
    s.add(id)
  }
  chosen.value = s
}

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return fns.value
  return fns.value.filter(
    (f) =>
      f.name.toLowerCase().includes(q) ||
      (f.description || '').toLowerCase().includes(q) ||
      (f.runtime || '').toLowerCase().includes(q),
  )
})

const apply = () => {
  emit('apply', Array.from(chosen.value))
}

onMounted(async () => {
  try {
    const res = await listFunctions({ limit: 200 })
    fns.value = res.data.functions || []
  } finally {
    loading.value = false
  }
})
</script>
