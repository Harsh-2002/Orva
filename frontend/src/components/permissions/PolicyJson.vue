<template>
  <div class="space-y-2 px-4 py-3">
    <div class="flex flex-wrap items-center justify-between gap-2">
      <p class="min-w-0 text-xs text-foreground-muted">
        {{ label }} Read only. The builder above is the way to change it.
      </p>
      <Button
        variant="secondary"
        size="sm"
        :disabled="!json"
        @click="copy"
      >
        <component
          :is="copied ? Check : Copy"
          class="h-3.5 w-3.5"
          aria-hidden="true"
        />
        {{ copied ? 'Copied' : 'Copy JSON' }}
      </Button>
    </div>
    <pre
      class="max-h-80 overflow-auto rounded-md border border-border bg-background p-3 font-mono text-xs text-foreground-strong"
      tabindex="0"
      aria-label="Policy JSON, read only"
    >{{ json || 'The policy is not valid, so there is no JSON to show.' }}</pre>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import { Check, Copy } from '@lucide/vue'
import Button from '@/components/common/Button.vue'
import { copyText } from '@/utils/clipboard.js'
import { canonicalPolicyJson } from '@/utils/permissionsModel.js'

const props = defineProps({
  policy: { type: Object, default: null },
  label: { type: String, default: 'The stored policy as the server reads it.' },
})

// Canonical copy of the authorable policy only: never host metadata.
const json = computed(() => (props.policy ? canonicalPolicyJson(props.policy) : ''))
const copied = ref(false)
async function copy() {
  if (!(await copyText(json.value))) return
  copied.value = true
  setTimeout(() => { copied.value = false }, 2000)
}
</script>
