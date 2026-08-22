<template>
  <div class="min-h-dvh flex items-center justify-center bg-background p-4">
    <div class="w-full max-w-md">
      <!-- Logo & Branding -->
      <div class="text-center mb-8">
        <div class="flex items-center justify-center gap-3 mb-4">
          <OrvaLogo class="w-12 h-12" />
          <h1 class="text-3xl font-bold tracking-tight text-foreground">
            Orva
          </h1>
        </div>
        <p class="text-foreground-muted text-sm">
          Sign in to this instance
        </p>
      </div>

      <!-- Login Card -->
      <div class="bg-surface border border-border rounded-lg shadow-lg p-8">
        <h2 class="text-xl font-semibold text-foreground mb-6">
          Sign In
        </h2>

        <!-- The focus ring is white, not primary: it marks where the cursor is
             rather than accenting the field, and violet-on-card measures 2.06:1,
             under the 3:1 WCAG 1.4.11 floor for a focus indicator that has
             replaced the native outline. Onboarding's inputs already do this. -->
        <form
          class="space-y-5"
          @submit.prevent="handleLogin"
        >
          <div>
            <label
              for="login-username"
              class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-2"
            >
              Username
            </label>
            <input
              id="login-username"
              v-model="form.username"
              type="text"
              required
              autocomplete="username"
              class="w-full bg-background border border-border rounded-md px-4 py-2.5 text-sm text-foreground placeholder-foreground-muted focus:outline-none focus:ring-1 focus:ring-white focus:border-white transition-colors"
              placeholder="Enter your username"
              :disabled="loading"
            >
          </div>

          <div>
            <label
              for="login-password"
              class="text-xs font-medium text-foreground-muted uppercase tracking-wide block mb-2"
            >
              Password
            </label>
            <input
              id="login-password"
              v-model="form.password"
              type="password"
              required
              autocomplete="current-password"
              class="w-full bg-background border border-border rounded-md px-4 py-2.5 text-sm text-foreground placeholder-foreground-muted focus:outline-none focus:ring-1 focus:ring-white focus:border-white transition-colors"
              placeholder="Enter your password"
              :disabled="loading"
            >
          </div>

          <div
            v-if="error"
            class="bg-danger-tint border border-danger-ring rounded-md px-4 py-3 flex items-start gap-2"
            role="alert"
          >
            <AlertCircle class="w-5 h-5 text-danger-fg shrink-0 mt-0.5" />
            <p class="text-sm text-danger-fg">
              {{ error }}
            </p>
          </div>

          <Button
            type="submit"
            class="w-full"
            :loading="loading"
            :disabled="!form.username || !form.password || loading"
          >
            <LogIn class="w-4 h-4" />
            Sign In
          </Button>
        </form>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { LogIn, AlertCircle } from '@lucide/vue'
import Button from '@/components/common/Button.vue'
import OrvaLogo from '@/components/OrvaLogo.vue'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const auth = useAuthStore()

// Check if onboarding is needed on mount
onMounted(async () => {
  const hasUser = await auth.fetchAuthStatus()
  if (hasUser === false) {
    router.replace('/onboarding')
  }
})

const form = ref({
  username: '',
  password: ''
})

const error = ref('')
const loading = ref(false)

const handleLogin = async () => {
  error.value = ''
  loading.value = true

  const result = await auth.login(form.value.username, form.value.password)

  loading.value = false

  if (result.success) {
    router.push('/')
  } else if (result.code === 'ONBOARDING_REQUIRED') {
    router.push('/onboarding')
  } else {
    error.value = result.error
  }
}
</script>
