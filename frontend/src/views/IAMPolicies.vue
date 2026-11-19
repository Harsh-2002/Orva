<template>
  <div class="space-y-6">
    <header class="flex flex-wrap items-start justify-between gap-4">
      <div>
        <h1 class="text-xl font-semibold tracking-tight text-foreground-strong">
          Policies
        </h1>
        <p class="mt-1.5 measure text-sm leading-body text-foreground-muted">
          Reusable permissions pinned to a version for each function.
        </p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <Button
          v-if="list.data.value?.can_manage && !creating"
          :disabled="!list.fresh.value || list.loading.value || list.busy.value || !!list.error.value"
          :title="list.loading.value || list.busy.value ? 'Wait for the current policy list.' : list.error.value ? 'Refresh policies before creating a reusable policy.' : ''"
          @click="creating = true"
        >
          Create reusable policy
        </Button>
        <Button
          variant="secondary"
          :disabled="list.loading.value"
          @click="list.refresh"
        >
          Refresh policies
        </Button>
      </div>
    </header>
    <p
      v-if="list.error.value"
      role="alert"
      class="text-sm text-danger-fg"
    >
      {{ list.error.value }}
    </p>
    <p
      v-if="list.loading.value"
      role="status"
      class="text-sm text-foreground-muted"
    >
      Loading policies…
    </p>
    <ManagedPolicyDraft
      v-if="creating && list.data.value?.can_manage"
      :targets="targets"
      :disabled="list.busy.value"
      @save="create"
      @cancel="creating = false"
    />
    <ul
      v-else-if="list.data.value?.items.length"
      class="divide-y divide-border rounded-lg border border-border"
    >
      <li
        v-for="item in list.data.value.items"
        :key="item.id"
        class="flex cursor-pointer flex-wrap items-center justify-between gap-3 px-4 py-3"
        @click="rowAction(() => $emit('select', item.id))($event)"
      >
        <button
          type="button"
          class="inline-flex min-h-[1.5rem] touch-expand-xs max-w-full items-center truncate rounded-md text-left text-sm font-medium text-foreground-strong hover:underline underline-offset-4 focus:outline-none focus-visible:ring-2 focus-visible:ring-focus-ring"
          @click="$emit('select', item.id)"
        >
          {{ item.name }}
        </button>
        <span class="text-xs text-foreground-muted">Version <code>{{ item.latest_version }}</code> · {{ !list.fresh.value ? 'Last known state, unverified' : item.revoked ? 'Revoked permanently' : 'Available to attach' }}</span>
      </li>
    </ul>
    <p
      v-else-if="list.data.value && !creating"
      class="text-sm text-foreground-muted"
    >
      No named policies yet. Create one, review its permissions, then attach a pinned version.
    </p>
    <IAMPolicyDetail
      v-if="policyId && !creating"
      :key="policyId"
      :policy-id="policyId"
      :api="api"
      :functions="functions"
      @changed="list.refresh"
      @deleted="$emit('select', ''); list.refresh()"
    />
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import Button from '@/components/common/Button.vue'
import ManagedPolicyDraft from '@/components/permissions/ManagedPolicyDraft.vue'
import { rowAction } from '@/utils/rowNav'
import IAMPolicyDetail from './IAMPolicyDetail.vue'
import { useIAMResource } from '@/composables/useIAMResource.js'
const props = defineProps({ policyId: { type: String, default: '' }, api: { type: Object, required: true }, functions: { type: Array, default: () => [] } })
const emit = defineEmits(['select'])
const creating = ref(false)
const targets = computed(() => props.functions.map((f) => ({ id: f.id, label: f.name, state: 'resolved' })))
const list = useIAMResource(() => 'policies', (_, signal) => props.api.list(signal))
async function create(value) {
  if (!list.data.value?.can_manage || list.loading.value || list.busy.value || list.error.value) return
  const result = await list.mutate((signal) => props.api.create(value, signal))
  if (result) { creating.value = false; emit('select', result.policy.id) }
}
</script>
