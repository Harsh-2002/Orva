<template>
  <!--
    Single-column onboarding. PRODUCT.md anti-references explicitly
    name the Vercel/Railway split-screen brand panel (gradient + blurred
    circles + glassmorphic feature chips) as the shape to avoid; the
    hero rebuild replaces it with content native to Orva — a real
    terminal trace showing what the operator is about to do — and the
    form sits below at every breakpoint.

    pt-safe + pb-safe respect the iOS notch and home indicator when
    onboarding runs in PWA / fullscreen mode. The page itself never
    scrolls horizontally; the only scroll is the form's own column.
  -->
  <div class="min-h-screen flex flex-col items-center justify-center bg-background pt-safe pb-safe pl-safe pr-safe px-page py-8 sm:py-12">
    <div class="w-full max-w-md space-y-8">
      <!-- Wordmark + lede. Same typographic register as the rest of
           the dashboard: text-xl semibold tracking-tight on the H1,
           muted body subhead. No outsized "self-hosted" violet flourish. -->
      <div class="space-y-3">
        <div class="flex items-center gap-3">
          <OrvaLogo class="w-9 h-9" />
          <span class="text-2xl font-semibold tracking-tight text-white">Orva</span>
        </div>
        <h1 class="text-xl font-semibold text-white tracking-tight">
          Set up admin access
        </h1>
        <p class="text-sm text-foreground-muted leading-relaxed">
          Create the root user for this Orva instance. This is a one-time setup
          and cannot be repeated; export your password somewhere durable before
          you continue.
        </p>
      </div>

      <!-- Native register: a real terminal trace of what an operator does
           on day one. Replaces the SaaS hero panel. Static text, two
           prompts + outputs, monospace throughout. -->
      <div
        class="bg-surface border border-border rounded-md font-mono text-[12px] leading-relaxed overflow-x-auto scrollable"
        aria-hidden="true"
      >
        <div class="px-3 py-2 border-b border-border flex items-center gap-1.5 text-[10px] uppercase tracking-wider text-foreground-muted">
          <span class="w-2 h-2 rounded-full bg-foreground-muted/30" />
          <span class="w-2 h-2 rounded-full bg-foreground-muted/30" />
          <span class="w-2 h-2 rounded-full bg-foreground-muted/30" />
          <span class="ml-2">~ /lab/orva</span>
        </div>
        <pre class="px-3 py-3 text-foreground whitespace-pre overflow-x-auto"><span class="text-primary">$</span> orva deploy hello ./code
<span class="text-foreground-muted">build  </span><span class="text-success">succeeded</span> <span class="text-foreground-muted">·  421 ms</span>
<span class="text-foreground-muted">deploy </span><span class="text-success">active</span>    <span class="text-foreground-muted">·   v1</span>

<span class="text-primary">$</span> curl http://orva.lab/fn/$ID
<span class="text-foreground-muted">{ "ok": true, "msg": "hi from orva" }</span></pre>
      </div>

      <!-- Form. Single column, max-w-md, full-width primary on mobile.
           Inputs use text-base on mobile so iOS does not auto-zoom on
           focus, falling back to text-sm at sm+ for density. -->
      <form
        class="space-y-5"
        @submit.prevent="handleOnboard"
      >
        <div>
          <label
            for="onboard-username"
            class="block text-xs font-medium text-foreground-muted uppercase tracking-wide mb-1.5"
          >Username</label>
          <input
            id="onboard-username"
            v-model="form.username"
            type="text"
            required
            autocomplete="username"
            class="w-full bg-background border border-border rounded-md px-3 py-2.5 text-base sm:text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-white focus:border-white transition-colors"
            placeholder="orva"
            :disabled="loading"
          >
        </div>

        <div>
          <label
            for="onboard-password"
            class="block text-xs font-medium text-foreground-muted uppercase tracking-wide mb-1.5"
          >Password</label>
          <div class="flex flex-col sm:flex-row gap-2">
            <div class="relative flex-1 min-w-0">
              <input
                id="onboard-password"
                v-model="form.password"
                :type="showPassword ? 'text' : 'password'"
                required
                autocomplete="new-password"
                class="w-full bg-background border border-border rounded-md px-3 py-2.5 pr-20 text-base sm:text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-white focus:border-white transition-colors font-mono"
                placeholder="Minimum 10 characters"
                :disabled="loading"
              >
              <div class="absolute right-1.5 top-1/2 -translate-y-1/2 flex gap-0.5">
                <button
                  v-if="form.password"
                  type="button"
                  class="p-1.5 rounded hover:bg-surface-hover transition-colors touch-expand-iconbtn"
                  :disabled="loading"
                  aria-label="Copy password"
                  title="Copy password"
                  @click="copyPassword"
                >
                  <Copy class="w-4 h-4 text-foreground-muted hover:text-foreground" />
                </button>
                <button
                  type="button"
                  class="p-1.5 rounded hover:bg-surface-hover transition-colors touch-expand-iconbtn"
                  :disabled="loading"
                  :aria-label="showPassword ? 'Hide password' : 'Show password'"
                  :title="showPassword ? 'Hide password' : 'Show password'"
                  @click="showPassword = !showPassword"
                >
                  <Eye v-if="!showPassword" class="w-4 h-4 text-foreground-muted hover:text-foreground" />
                  <EyeOff v-else class="w-4 h-4 text-foreground-muted hover:text-foreground" />
                </button>
              </div>
            </div>
            <button
              type="button"
              class="px-4 py-2.5 rounded-md border border-border bg-surface hover:bg-surface-hover text-sm font-medium text-foreground transition-colors shrink-0"
              :disabled="loading"
              @click="generatePassword"
            >
              Generate
            </button>
          </div>

          <div class="mt-3 grid grid-cols-2 gap-y-1.5 gap-x-3">
            <div
              v-for="(valid, key) in passwordChecks"
              :key="key"
              class="flex items-center gap-1.5 text-xs transition-colors duration-200"
              :class="valid ? 'text-success' : 'text-foreground-muted'"
            >
              <div
                class="w-1.5 h-1.5 rounded-full"
                :class="valid ? 'bg-success' : 'bg-foreground-muted/40'"
              />
              <span>{{ formatCheckLabel(key) }}</span>
            </div>
          </div>
        </div>

        <div
          v-if="error"
          class="bg-danger/10 border border-danger/30 rounded-md px-4 py-3 flex items-start gap-3"
        >
          <AlertCircle class="w-5 h-5 text-danger shrink-0 mt-0.5" />
          <p class="text-sm text-danger">
            {{ error }}
          </p>
        </div>

        <Button
          type="submit"
          class="w-full"
          :loading="loading"
          :disabled="!isPasswordValid || !form.username || loading"
        >
          Create account
        </Button>

        <p class="text-xs text-center text-foreground-muted pt-2">
          This action initialises your instance and cannot be undone.
        </p>
      </form>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { AlertCircle, Eye, EyeOff, Copy } from 'lucide-vue-next'
import Button from '@/components/common/Button.vue'
import OrvaLogo from '@/components/OrvaLogo.vue'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const auth = useAuthStore()

const form = ref({
  username: 'orva',
  password: ''
})

const error = ref('')
const loading = ref(false)
const showPassword = ref(false)

const passwordChecks = computed(() => ({
  length: form.value.password.length >= 10,
  lower: /[a-z]/.test(form.value.password),
  upper: /[A-Z]/.test(form.value.password),
  digit: /[0-9]/.test(form.value.password),
  symbol: /[^A-Za-z0-9]/.test(form.value.password),
}))

const isPasswordValid = computed(() => {
  const checks = passwordChecks.value
  return checks.length && checks.lower && checks.upper && checks.digit && checks.symbol
})

const formatCheckLabel = (key) => {
  const labels = {
    length: '10+ characters',
    lower: 'Lowercase',
    upper: 'Uppercase',
    digit: 'Number',
    symbol: 'Symbol'
  }
  return labels[key] || key
}

const generatePassword = () => {
  const length = 16
  const lower = 'abcdefghijklmnopqrstuvwxyz'
  const upper = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ'
  const digits = '0123456789'
  const symbols = '!@#$%^&*()-_=+[]{}|;:,.<>?'
  const all = lower + upper + digits + symbols

  const getRandom = (chars) => chars[crypto.getRandomValues(new Uint32Array(1))[0] % chars.length]

  let pwd = [
    getRandom(lower),
    getRandom(upper),
    getRandom(digits),
    getRandom(symbols),
  ]

  for (let i = pwd.length; i < length; i += 1) {
    pwd.push(getRandom(all))
  }

  // Shuffle
  for (let i = pwd.length - 1; i > 0; i -= 1) {
    const j = crypto.getRandomValues(new Uint32Array(1))[0] % (i + 1)
    ;[pwd[i], pwd[j]] = [pwd[j], pwd[i]]
  }

  form.value.password = pwd.join('')
}

const copyPassword = async () => {
  try {
    await navigator.clipboard.writeText(form.value.password)
  } catch (err) {
    console.error('Failed to copy password:', err)
  }
}

const handleOnboard = async () => {
  error.value = ''
  loading.value = true

  const result = await auth.onboard(form.value.username, form.value.password)

  loading.value = false

  if (result.success) {
    router.push('/')
  } else {
    error.value = result.error
  }
}

onMounted(async () => {
  const hasUser = await auth.fetchAuthStatus()
  if (hasUser) {
    router.push('/login')
  }
})
</script>
