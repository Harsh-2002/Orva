<template>
  <router-view />
  <!-- Global confirm dialog — single instance reachable from any view. -->
  <ConfirmDialog />
</template>

<script setup>
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
</script>
