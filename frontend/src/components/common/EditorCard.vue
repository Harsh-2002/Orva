<template>
  <!-- EditorCard is the right-column panel pattern repeated 7+ times across
       Editor.vue (Configuration, Env Vars, Dependencies, Versions, Secrets,
       Invoke URL, Test Invocation) and once on the Deployments active-version
       banner. Centralising it here makes the "no internal scrollbar / page-
       scroll instead" decision authoritative — DON'T add max-h or overflow
       inside this component. Long-content cards (build logs, etc.) get their
       own scroll handling at the call-site, not here.

       Slots:
         - default: card body
         - title: replaces the header text; or use the :title prop
         - actions: optional right-aligned buttons in the header
       Props:
         - title (String): convenience for plain-text titles
         - icon (Component): optional Lucide icon rendered before the title -->
  <div class="bg-background border border-border rounded-lg p-5 space-y-4 shadow-sm">
    <div
      v-if="title || $slots.title || $slots.actions"
      class="flex items-center justify-between"
    >
      <div class="flex items-center gap-2 text-xs font-bold text-white uppercase tracking-wider">
        <component
          :is="icon"
          v-if="icon"
          class="w-4 h-4"
        />
        <slot name="title">{{ title }}</slot>
      </div>
      <div
        v-if="$slots.actions"
        class="flex items-center gap-2"
      >
        <slot name="actions" />
      </div>
    </div>
    <slot />
  </div>
</template>

<script setup>
defineProps({
  title: { type: String, default: '' },
  icon: { type: Object, default: null },
})
</script>
