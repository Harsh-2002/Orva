<template>
  <div class="min-h-screen grid lg:grid-cols-2 bg-background">
    <!-- Left Panel: Brand & Vision -->
    <div class="relative hidden lg:flex flex-col justify-between p-12 bg-gradient-to-br from-slate-900 via-purple-950 to-slate-900 text-white overflow-hidden">
      <!-- Noise Texture -->
      <div class="absolute inset-0 opacity-[0.15] mix-blend-soft-light" style="background-image: url('data:image/svg+xml,%3Csvg viewBox=%270 0 400 400%27 xmlns=%27http://www.w3.org/2000/svg%27%3E%3Cfilter id=%27noiseFilter%27%3E%3CfeTurbulence type=%27fractalNoise%27 baseFrequency=%270.9%27 numOctaves=%274%27 stitchTiles=%27stitch%27/%3E%3C/filter%3E%3Crect width=%27100%25%27 height=%27100%25%27 filter=%27url(%23noiseFilter)%27/%3E%3C/svg%3E');" />
      <!-- Abstract Background Shapes -->
      <div class="absolute top-0 right-0 -mr-20 -mt-20 w-96 h-96 rounded-full bg-purple-500/20 blur-3xl" />
      <div class="absolute bottom-0 left-0 -ml-20 -mb-20 w-80 h-80 rounded-full bg-violet-500/20 blur-3xl" />
        
      <!-- Logo -->
      <div class="relative z-10 flex items-center gap-3">
        <div class="shadow-lg shadow-purple-500/20 rounded-lg">
          <OrvaLogo class="w-10 h-10" />
        </div>
        <span class="text-2xl font-bold tracking-tight">Orva</span>
      </div>

      <!-- Main Content -->
      <div class="relative z-10 space-y-8 max-w-lg">
        <h1 class="text-5xl font-extrabold tracking-tight leading-tight">
          Serverless,<br>
          <span class="text-violet-300">Self-Hosted.</span>
        </h1>
        <p class="text-lg text-slate-100/80 leading-relaxed">
          A modern FaaS platform you run yourself. Warm worker pools, encrypted
          secrets, and an autoscaler that fits your hardware — no vendor lock-in.
        </p>
            
        <div class="grid gap-6 pt-4">
          <!-- Feature Items -->
          <div class="flex gap-4">
            <div class="w-10 h-10 rounded-lg bg-white/10 flex items-center justify-center border border-white/10 shrink-0 backdrop-blur-sm">
              <Shield class="w-5 h-5 text-violet-200" />
            </div>
            <div>
              <h3 class="font-semibold text-white">
                Secure by Default
              </h3>
              <p class="text-sm text-slate-200/70 mt-1">
                Single-tenant architecture with encrypted secrets management.
              </p>
            </div>
          </div>
          <div class="flex gap-4">
            <div class="w-10 h-10 rounded-lg bg-white/10 flex items-center justify-center border border-white/10 shrink-0 backdrop-blur-sm">
              <Zap class="w-5 h-5 text-violet-200" />
            </div>
            <div>
              <h3 class="font-semibold text-white">
                Warm Worker Pools
              </h3>
              <p class="text-sm text-slate-200/70 mt-1">
                Hot invocations land in single-digit milliseconds; the autoscaler
                grows pools per function under load.
              </p>
            </div>
          </div>
          <div class="flex gap-4">
            <div class="w-10 h-10 rounded-lg bg-white/10 flex items-center justify-center border border-white/10 shrink-0 backdrop-blur-sm">
              <Boxes class="w-5 h-5 text-violet-200" />
            </div>
            <div>
              <h3 class="font-semibold text-white">
                Multi-Runtime Support
              </h3>
              <p class="text-sm text-slate-200/70 mt-1">
                Deploy Node.js (22, 24) and Python (3.13, 3.14) functions.
              </p>
            </div>
          </div>
        </div>
      </div>

      <!-- Footer - Empty div for spacing -->
      <div class="relative z-10" />
    </div>

    <!-- Right Panel: Form -->
    <div class="flex items-center justify-center p-6 lg:p-12">
      <div class="w-full max-w-md space-y-8">
        <!-- Mobile Logo (visible only on small screens) -->
        <div class="lg:hidden flex justify-center mb-8">
          <OrvaLogo class="w-12 h-12" />
        </div>

        <div class="text-center lg:text-left space-y-2">
          <h2 class="text-3xl font-bold text-foreground">
            Setup Admin Access
          </h2>
          <p class="text-muted-foreground">
            Create the root user for your Orva instance.
          </p>
        </div>

        <form
          class="space-y-6 bg-surface lg:bg-transparent lg:p-0 p-6 rounded-xl lg:shadow-none shadow-lg border lg:border-none border-border"
          @submit.prevent="handleOnboard"
        >
          <div class="space-y-4">
            <div>
              <label class="block text-sm font-medium text-foreground mb-1.5">Username</label>
              <input
                v-model="form.username"
                type="text"
                required
                class="w-full bg-background border border-border rounded-lg px-4 py-2.5 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary/50 focus:border-primary transition-all"
                placeholder="orva"
                :disabled="loading"
              >
            </div>

            <div>
              <label class="block text-sm font-medium text-foreground mb-1.5">Password</label>
              <div class="flex gap-2">
                <div class="relative flex-1">
                  <input
                    v-model="form.password"
                    :type="showPassword ? 'text' : 'password'"
                    required
                    class="w-full bg-background border border-border rounded-lg px-4 py-2.5 pr-20 text-sm text-foreground focus:outline-none focus:ring-2 focus:ring-primary/50 focus:border-primary transition-all"
                    placeholder="Minimum 10 characters"
                    :disabled="loading"
                  >
                  <div class="absolute right-2 top-1/2 -translate-y-1/2 flex gap-1">
                    <button
                      v-if="form.password"
                      type="button"
                      class="p-1.5 rounded hover:bg-surface-hover transition-colors"
                      :disabled="loading"
                      @click="copyPassword"
                      title="Copy password"
                    >
                      <Copy class="w-4 h-4 text-muted-foreground hover:text-foreground" />
                    </button>
                    <button
                      type="button"
                      class="p-1.5 rounded hover:bg-surface-hover transition-colors"
                      :disabled="loading"
                      @click="showPassword = !showPassword"
                      :title="showPassword ? 'Hide password' : 'Show password'"
                    >
                      <Eye v-if="!showPassword" class="w-4 h-4 text-muted-foreground hover:text-foreground" />
                      <EyeOff v-else class="w-4 h-4 text-muted-foreground hover:text-foreground" />
                    </button>
                  </div>
                </div>
                <button
                  type="button"
                  class="px-4 py-2 rounded-lg border border-border bg-surface hover:bg-surface-hover text-sm font-medium text-foreground transition-colors"
                  :disabled="loading"
                  @click="generatePassword"
                >
                  Generate
                </button>
              </div>
              
              <!-- Password Strength Indicators -->
              <div class="mt-3 grid grid-cols-2 gap-2">
                <div 
                  v-for="(valid, key) in passwordChecks" 
                  :key="key"
                  class="flex items-center gap-1.5 text-xs transition-colors duration-200"
                  :class="valid ? 'text-green-500' : 'text-muted-foreground'"
                >
                  <div
                    class="w-1.5 h-1.5 rounded-full"
                    :class="valid ? 'bg-success' : 'bg-surface'"
                  />
                  <span class="capitalize">{{ formatCheckLabel(key) }}</span>
                </div>
              </div>
            </div>
          </div>

          <div
            v-if="error"
            class="bg-red-500/10 border border-red-500/20 rounded-lg px-4 py-3 flex items-start gap-3"
          >
            <AlertCircle class="w-5 h-5 text-red-500 shrink-0 mt-0.5" />
            <p class="text-sm text-red-500">
              {{ error }}
            </p>
          </div>

          <Button
            type="submit"
            class="w-full py-2.5 text-base"
            :loading="loading"
            :disabled="!isPasswordValid || !form.username || loading"
          >
            Create Account
          </Button>

          <p class="text-xs text-center text-muted-foreground pt-4">
            This action initializes your instance and cannot be undone.
          </p>
        </form>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { AlertCircle, Shield, Zap, Boxes, Eye, EyeOff, Copy } from 'lucide-vue-next'
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
