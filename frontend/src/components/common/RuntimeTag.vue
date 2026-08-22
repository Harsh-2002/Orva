<template>
  <!--
    RuntimeTag — the one place a function's runtime is drawn.

    Two runtimes exist and both have marks an operator already knows, so in a
    dense list the glyph identifies faster than the word: shape resolves before
    text does, and "PYTHON" / "NODE" set in uppercase mono cost a column of
    width to say what a 14 px snake or hexagon says instantly.

    The word is not thrown away, it moves to the accessible name and the
    tooltip, so screen readers and hover both still get "Python 3.14". Icon-only
    is a legitimate choice here precisely because the set is two items wide,
    stable, and has established marks. It would not be legitimate for status,
    which is why StatusBadge keeps its word.

    `withLabel` prints the label alongside the mark. Use it where the operator
    is choosing a runtime rather than recognising one, or anywhere the tag sits
    alone without the surrounding row to give it context.
  -->
  <span
    class="inline-flex items-center gap-1.5 shrink-0"
    :class="tint"
    :title="label"
  >
    <component
      :is="mark"
      v-if="mark"
      class="w-3.5 h-3.5 shrink-0"
    />
    <span
      v-if="withLabel || !mark"
      class="text-xs font-medium"
    >{{ label }}</span>
    <span
      v-else
      class="sr-only"
    >{{ label }}</span>
  </span>
</template>

<script setup>
import { computed } from 'vue'
import { runtimeLabel, isPython as isPythonRuntime, isNode as isNodeRuntime } from '@/utils/runtime'
import PythonIcon from '@/components/icons/brand/PythonIcon.vue'
import NodeIcon from '@/components/icons/brand/NodeIcon.vue'

const props = defineProps({
  runtime: { type: String, default: '' },
  withLabel: { type: Boolean, default: false },
})

const isPython = computed(() => isPythonRuntime(props.runtime))
const isNode = computed(() => isNodeRuntime(props.runtime))

// No mark for a runtime we do not ship a glyph for: an unrecognised value falls
// back to printing whatever the API said, which is more useful than showing the
// wrong logo confidently.
const mark = computed(() => {
  if (isPython.value) return PythonIcon
  if (isNode.value) return NodeIcon
  return null
})

// The pinned versions live in CONTRACT.md (Node 24, Python 3.14). They are
// spelled out here because "Python" alone does not tell an operator which
// interpreter their handler is about to run on.
const label = computed(() => runtimeLabel(props.runtime) || 'Unknown runtime')

const tint = computed(() => {
  if (isPython.value) return 'text-runtime-python'
  if (isNode.value) return 'text-runtime-node'
  return 'text-foreground-muted'
})
</script>
