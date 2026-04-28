<template>
  <div class="max-w-2xl space-y-6 animate-fadeIn">
    <div>
      <h1 class="text-3xl font-bold text-white mb-2">
        Deploy Function
      </h1>
      <p class="text-foreground-muted">
        Upload and deploy a new serverless function
      </p>
    </div>

    <Card>
      <form
        class="space-y-6"
        @submit.prevent="deploy"
      >
        <!-- Function Name -->
        <Input
          v-model="formData.name"
          label="Function Name"
          placeholder="my-function"
          required
          :error="errors.name"
        />

        <!-- Runtime -->
        <div>
          <label class="block text-sm font-medium text-foreground-muted mb-2">Runtime</label>
          <select
            v-model="formData.runtime"
            class="w-full px-3 py-2 bg-background border border-border rounded-md text-white focus:outline-none focus:ring-1 focus:ring-white focus:border-white"
            required
          >
            <option value="">
              Select runtime
            </option>
            <option value="node">
              Node.js
            </option>
            <option value="go">
              Go
            </option>
            <option value="python">
              Python
            </option>
            <option value="ruby">
              Ruby
            </option>
            <option value="rust">
              Rust
            </option>
          </select>
        </div>

        <!-- Source File -->
        <div>
          <label class="block text-sm font-medium text-foreground-muted mb-2">Source File</label>
          <div
            class="border-2 border-dashed border-border rounded-lg p-8 text-center hover:border-foreground-muted transition-colors"
            :class="{ 'border-primary bg-primary/5': fileDropActive }"
            @dragover.prevent="fileDropActive = true"
            @dragleave.prevent="fileDropActive = false"
            @drop.prevent="handleFileDrop"
          >
            <Upload class="w-12 h-12 mx-auto text-foreground-muted opacity-70 mb-3" />
            <p class="text-foreground-muted mb-2">
              Drag and drop your file here, or
              <label class="text-primary hover:text-primary-hover cursor-pointer">
                browse
                <input
                  type="file"
                  class="hidden"
                  :accept="getFileExtensions"
                  @change="handleFileSelect"
                >
              </label>
            </p>
            <p
              v-if="formData.file"
              class="text-sm text-foreground-muted"
            >
              Selected: {{ formData.file.name }}
            </p>
          </div>
        </div>

        <!-- Entrypoint (for interpreted languages) -->
        <div v-if="requiresEntrypoint">
          <Input
            v-model="formData.entrypoint"
            label="Entrypoint"
            placeholder="server.js"
            hint="The main file to execute"
            :required="requiresEntrypoint"
          />
        </div>

        <!-- Memory -->
        <div>
          <label class="block text-sm font-medium text-foreground-muted mb-2">Memory</label>
          <select
            v-model="formData.memory_mb"
            class="w-full px-3 py-2 bg-background border border-border rounded-md text-white focus:outline-none focus:ring-1 focus:ring-white focus:border-white"
          >
            <option value="32">
              32 MB
            </option>
            <option value="64">
              64 MB (default)
            </option>
            <option value="128">
              128 MB
            </option>
            <option value="256">
              256 MB
            </option>
            <option value="512">
              512 MB
            </option>
          </select>
        </div>

        <!-- Environment Variables -->
        <div>
          <div class="flex items-center justify-between mb-2">
            <label class="text-sm font-medium text-foreground-muted">Environment Variables</label>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              @click="addEnvVar"
            >
              <Plus class="w-4 h-4 mr-1" />
              Add
            </Button>
          </div>
          <div class="space-y-2">
            <div
              v-for="(env, index) in formData.env_vars"
              :key="index"
              class="flex gap-2"
            >
              <Input
                v-model="env.key"
                placeholder="KEY"
                class="flex-1"
                required
              />
              <Input
                v-model="env.value"
                placeholder="value"
                class="flex-1"
              />
              <Button
                type="button"
                variant="ghost"
                size="sm"
                @click="removeEnvVar(index)"
              >
                <X class="w-4 h-4" />
              </Button>
            </div>
          </div>
          <p
            v-if="formData.env_vars.length === 0"
            class="text-sm text-foreground-muted opacity-70 mt-2"
          >
            No environment variables configured
          </p>
        </div>

        <!-- Actions -->
        <div class="flex gap-3 pt-4 border-t border-border">
          <Button
            type="submit"
            :loading="deploying"
            :disabled="!isFormValid"
            variant="primary"
            class="flex-1"
          >
            <Upload class="w-4 h-4 mr-2" />
            {{ deploying ? 'Deploying...' : 'Deploy Function' }}
          </Button>
          <router-link to="/functions">
            <Button
              type="button"
              variant="secondary"
            >
              Cancel
            </Button>
          </router-link>
        </div>
      </form>
    </Card>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { deployFunction } from '@/api/endpoints'
import Card from '@/components/common/Card.vue'
import Button from '@/components/common/Button.vue'
import Input from '@/components/common/Input.vue'
import {
  Upload,
  Plus,
  X,
} from 'lucide-vue-next'

const router = useRouter()

const formData = ref({
  name: '',
  runtime: '',
  file: null,
  entrypoint: '',
  memory_mb: 64,
  env_vars: [],
})

const errors = ref({})
const deploying = ref(false)
const fileDropActive = ref(false)

const handlerMode = computed(() => {
  switch (formData.value.runtime) {
    case 'node':
    case 'python':
    case 'ruby':
      return 'lambda'
    default:
      return 'http'
  }
})

const requiresEntrypoint = computed(() => {
  const interpreted = ['node', 'python', 'ruby']
  return interpreted.includes(formData.value.runtime) && handlerMode.value === 'http'
})

const getFileExtensions = computed(() => {
  const extensions = {
    node: '.js,.mjs',
    go: '.bin',
    python: '.py',
    ruby: '.rb',
    rust: '.bin',
  }
  return extensions[formData.value.runtime] || '*'
})

const isFormValid = computed(() => {
  return formData.value.name &&
         formData.value.runtime &&
         formData.value.file &&
         (!requiresEntrypoint.value || formData.value.entrypoint)
})

const handleFileSelect = (event) => {
  const file = event.target.files[0]
  if (file) {
    formData.value.file = file
  }
}

const handleFileDrop = (event) => {
  fileDropActive.value = false
  const file = event.dataTransfer.files[0]
  if (file) {
    formData.value.file = file
  }
}

const addEnvVar = () => {
  formData.value.env_vars.push({ key: '', value: '' })
}

const removeEnvVar = (index) => {
  formData.value.env_vars.splice(index, 1)
}

const validateForm = () => {
  errors.value = {}
  
  if (!formData.value.name) {
    errors.value.name = 'Function name is required'
  } else if (!/^[a-zA-Z0-9-_]+$/.test(formData.value.name)) {
    errors.value.name = 'Name must be alphanumeric with hyphens/underscores'
  }
  
  if (!formData.value.runtime) {
    errors.value.runtime = 'Runtime is required'
  }
  
  if (!formData.value.file) {
    errors.value.file = 'File is required'
  }
  
  if (requiresEntrypoint.value && !formData.value.entrypoint) {
    errors.value.entrypoint = 'Entrypoint is required'
  }
  
  return Object.keys(errors.value).length === 0
}

const deploy = async () => {
  if (!validateForm()) return
  
  deploying.value = true
  errors.value = {}
  
  try {
    // Convert env_vars to object
    const envVars = {}
    formData.value.env_vars.forEach(env => {
      if (env.key && env.value) {
        envVars[env.key] = env.value
      }
    })
    
    await deployFunction({
      name: formData.value.name,
      runtime: formData.value.runtime,
      file: formData.value.file,
      entrypoint: formData.value.entrypoint,
      memory_mb: formData.value.memory_mb,
      env_vars: envVars,
      handler_mode: handlerMode.value,
    })
    
    alert(`Function "${formData.value.name}" deployed successfully!`)
    router.push('/functions')
  } catch (error) {
    console.error('Deployment failed:', error)
    const errorMessage = error.response?.data?.error?.message || error.message || 'Deployment failed'
    alert(`Failed to deploy: ${errorMessage}`)
  } finally {
    deploying.value = false
  }
}
</script>
