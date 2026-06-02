<template>
  <!--
    ToolCallCard — one agent tool call. The whole card collapses to a dense
    header (icon · name · group · status); expanding reveals the arguments and
    result as real JSON code blocks (highlight + copy). It auto-expands while
    running or awaiting approval and when it fails, and collapses once it
    succeeds, so a long successful tool run doesn't clutter the transcript.
  -->
  <div
    class="overflow-hidden rounded-lg border border-border bg-surface text-sm"
    :data-approval="tool.status === 'pending_approval' ? 'pending' : null"
  >
    <button
      type="button"
      class="flex w-full items-center gap-2 rounded-lg px-3 py-2 text-left transition-colors hover:bg-surface-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary"
      :aria-expanded="expanded"
      @click="toggle"
    >
      <Wrench
        class="h-3.5 w-3.5 shrink-0 text-foreground-muted"
        aria-hidden="true"
      />
      <span class="truncate font-mono text-foreground">{{ tool.name }}</span>
      <span
        v-if="tool.group"
        class="shrink-0 rounded bg-surface-hover px-1.5 py-0.5 text-[10px] uppercase tracking-label text-foreground-muted"
      >
        {{ tool.group }}
      </span>
      <span class="flex-1" />
      <span
        class="shrink-0 rounded-full px-2 py-0.5 text-xs"
        :class="[badge.classes, status === 'running' ? 'animate-pulse motion-reduce:animate-none' : '']"
      >
        {{ badge.label }}
      </span>
      <ChevronDown
        class="h-3.5 w-3.5 shrink-0 text-foreground-muted transition-transform duration-150"
        :class="{ 'rotate-180': expanded }"
      />
    </button>

    <div
      v-if="expanded"
      class="border-t border-border px-3 py-3"
    >
      <p class="mb-1 text-[10px] uppercase tracking-label text-foreground-muted">
        Arguments
      </p>
      <CodeBlock
        :code="argsStr || '(none)'"
        lang="json"
      />
      <template v-if="tool.result != null">
        <p class="mb-1 mt-3 text-[10px] uppercase tracking-label text-foreground-muted">
          Result
        </p>
        <CodeBlock
          :code="resultStr"
          lang="json"
        />
      </template>
    </div>

    <div
      v-if="tool.status === 'pending_approval'"
      class="flex gap-2 border-t border-border px-3 py-2"
    >
      <Button
        ref="approveBtn"
        size="xs"
        variant="primary"
        @click="$emit('approve', tool.id)"
      >
        Approve
      </Button>
      <Button
        size="xs"
        variant="ghost"
        @click="$emit('reject', tool.id)"
      >
        Reject
      </Button>
    </div>
  </div>
</template>

<script setup>
import { computed, ref, watch, nextTick, onMounted } from 'vue'
import { Wrench, ChevronDown } from 'lucide-vue-next'
import Button from '@/components/common/Button.vue'
import CodeBlock from './CodeBlock.vue'

const props = defineProps({
  tool: {
    type: Object,
    required: true
  }
})

defineEmits(['approve', 'reject'])

const status = computed(() => props.tool.status)

const BADGES = {
  pending_approval: { label: 'needs approval', classes: 'bg-warning-tint text-warning-fg' },
  running: { label: 'running…', classes: 'bg-info-tint text-info-fg' },
  succeeded: { label: 'done', classes: 'bg-success-tint text-success-fg' },
  failed: { label: 'failed', classes: 'bg-danger-tint text-danger-fg' },
  rejected: { label: 'rejected', classes: 'bg-surface-hover text-foreground-muted' }
}

const badge = computed(
  () => BADGES[status.value] ?? { label: status.value ?? 'unknown', classes: 'bg-surface-hover text-foreground-muted' }
)

function pretty(v) {
  if (v == null) return ''
  if (typeof v === 'string') {
    // Tool results often arrive as a JSON string — pretty-print when parseable.
    try { return JSON.stringify(JSON.parse(v), null, 2) } catch { return v }
  }
  return JSON.stringify(v, null, 2)
}
const argsStr = computed(() => pretty(props.tool.args))
const resultStr = computed(() => pretty(props.tool.result))

// Auto-expand while active or failed; collapse on success unless the user opened
// it. The user's manual toggle always wins after that.
const active = (s) => s === 'running' || s === 'pending_approval' || s === 'failed'
const userToggled = ref(false)
const expanded = ref(active(status.value))
function toggle() {
  userToggled.value = true
  expanded.value = !expanded.value
}
watch(status, (s) => {
  if (userToggled.value) return
  expanded.value = active(s)
})

// Move focus to Approve when a tool gates on the operator, so keyboard users
// reach the decision without Tabbing through the whole transcript. The card is
// created already in pending_approval (the tool_call frame sets it), so onMounted
// covers the common case; the watch covers a later transition.
const approveBtn = ref(null)
function focusApprove() {
  nextTick(() => {
    const el = approveBtn.value?.$el
    if (!el) return
    el.scrollIntoView({ behavior: 'smooth', block: 'center' })
    el.focus?.()
  })
}
onMounted(() => { if (status.value === 'pending_approval') focusApprove() })
watch(status, (s) => { if (s === 'pending_approval') focusApprove() })
</script>
