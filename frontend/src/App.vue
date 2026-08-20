<template>
  <router-view />
  <!-- Global confirm dialog — single instance reachable from any view. -->
  <ConfirmDialog />
</template>

<script setup>
import { onBeforeUnmount, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'

const router = useRouter()

// An expired session used to leave every list silently blank: nothing
// inspected the response status, so each view just rendered its empty
// state and the user was left looking at a dashboard that appeared to have
// no data. The API client emits this on any 401; send them to sign in.
const onUnauthorized = () => {
  if (router.currentRoute.value.name === 'login') return
  router.push({
    name: 'login',
    query: { redirect: router.currentRoute.value.fullPath, reason: 'expired' },
  })
}

onMounted(() => window.addEventListener('orva:unauthorized', onUnauthorized))
onBeforeUnmount(() => window.removeEventListener('orva:unauthorized', onUnauthorized))
</script>
