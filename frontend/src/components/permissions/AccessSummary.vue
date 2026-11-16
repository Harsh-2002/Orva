<template>
  <div>
    <!-- Counts by service. Words and numbers, no chart. -->
    <dl
      class="grid grid-cols-2 gap-x-6 gap-y-2 border-b border-border px-4 py-3 text-xs sm:grid-cols-5"
      aria-label="Current access by service"
    >
      <div
        v-for="s in summary"
        :key="s.id"
        class="min-w-0"
      >
        <dt class="text-foreground-muted">
          {{ s.label }}
        </dt>
        <dd class="text-sm font-medium text-foreground-strong">
          {{ s.text }}
        </dd>
      </div>
    </dl>

    <StatementList
      v-if="!statements.length"
      :statements="statements"
      :function-id="functionId"
      :unconfirmed="unconfirmed"
    />
    <template v-else>
      <section
        v-for="g in groups"
        :key="g.id"
        :aria-label="`${g.label} access`"
      >
        <h3 class="border-b border-border bg-background px-4 py-2 text-xs font-medium uppercase tracking-wide text-foreground-muted">
          {{ g.label }}
        </h3>
        <StatementList
          :statements="statements"
          :function-id="functionId"
          :targets="targets"
          :warnings="warnings"
          :only="g.indices"
          :list-label="`${g.label} permissions`"
        />
      </section>
    </template>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import StatementList from '@/components/permissions/StatementList.vue'
import { SERVICES, SERVICE_ORDER, serviceOf } from '@/utils/permissionsModel.js'

const props = defineProps({
  statements: { type: Array, required: true },
  functionId: { type: String, required: true },
  callers: { type: Array, default: () => [] },
  targets: { type: Array, default: () => [] },
  warnings: { type: Array, default: () => [] },
  unconfirmed: { type: String, default: '' },
})

const groups = computed(() => SERVICE_ORDER.map((id) => ({
  id,
  label: SERVICES[id].label,
  indices: props.statements.flatMap((s, i) => (serviceOf(s) === id ? [i] : [])),
})).filter((g) => g.indices.length))

// An empty list is not a fact while a refresh is pending or has failed.
const count = (n, one, many) => (props.unconfirmed ? 'Not confirmed' : n === 0 ? 'None' : `${n} ${n === 1 ? one : many}`)
const summary = computed(() => [
  ...SERVICE_ORDER.map((id) => {
    const n = props.statements.filter((s) => serviceOf(s) === id).length
    return { id, label: SERVICES[id].label, text: count(n, 'permission', 'permissions') }
  }),
  { id: 'callers', label: 'Callers accepted', text: count(props.callers.length, 'function', 'functions') },
])
</script>
