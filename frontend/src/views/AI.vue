<template>
  <!-- Layout injects `overflow-auto p-page` onto the view root; the chat wants a
       fixed-height app shell with its own internal scroll + pinned composer, so
       we neutralise padding/overflow inline and build a flex column. -->
  <div
    class="flex h-full bg-background"
    style="overflow: hidden; padding: 0"
  >
    <!-- Conversation rail — desktop aside -->
    <aside class="hidden w-64 shrink-0 border-r border-border md:block">
      <ConversationRail />
    </aside>

    <!-- Conversation rail — mobile drawer (only ever opened via the header
         hamburger, which is itself md:hidden, so this stays a phone affordance) -->
    <Drawer
      v-model="railOpen"
      title="Conversations"
    >
      <ConversationRail
        embedded
        @select="railOpen = false"
      />
    </Drawer>

    <!-- Chat column -->
    <div class="flex min-w-0 flex-1 flex-col">
      <ChatHeader
        :title="title"
        :can-export="!!store.timeline.length"
        @toggle-rail="railOpen = true"
        @export="store.exportActive"
      />

      <!-- No provider configured banner — only after the first provider fetch
           settles, so it never flashes during the initial load. -->
      <div
        v-if="store.providersLoaded && !store.providers.length"
        class="flex shrink-0 items-center justify-between gap-3 border-b border-warning-ring bg-warning-tint px-4 py-2.5 text-xs text-warning-fg"
      >
        <span>No AI provider configured. Add a provider API key to start chatting.</span>
        <Button
          size="xs"
          variant="secondary"
          @click="openAISettings"
        >
          Configure
        </Button>
      </div>

      <!-- Empty (new chat): centered greeting + composer -->
      <div
        v-if="!store.timeline.length"
        class="scrollable flex flex-1 flex-col items-center justify-center overflow-y-auto px-4 py-6"
      >
        <div class="w-full max-w-3xl">
          <EmptyState @pick="fillComposer" />
          <Composer
            ref="emptyComposer"
            class="mt-6"
            :docked="false"
            :streaming="store.streaming"
            :disabled="store.providersLoaded && !store.providers.length"
            :model-ready="!!store.selectedModel"
            @send="store.sendMessage"
            @stop="store.stop"
          />
        </div>
      </div>

      <!-- Chatting: scrolling timeline above the bottom-docked composer -->
      <template v-else>
        <MessageList
          :items="store.timeline"
          :streaming="store.streaming"
          @approve="store.approveTool"
          @reject="store.rejectTool"
          @regenerate="store.regenerate"
          @edit="onEdit"
          @delete="onDelete"
          @retry="store.retry"
          @dismiss="store.dismissError"
        />
        <Composer
          :streaming="store.streaming"
          :disabled="store.providersLoaded && !store.providers.length"
          :model-ready="!!store.selectedModel"
          @send="store.sendMessage"
          @stop="store.stop"
        />
      </template>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, onActivated } from 'vue'
import { useRouter } from 'vue-router'
import Button from '@/components/common/Button.vue'
import Drawer from '@/components/common/Drawer.vue'
import ChatHeader from '@/components/ai/ChatHeader.vue'
import ConversationRail from '@/components/ai/ConversationRail.vue'
import EmptyState from '@/components/ai/EmptyState.vue'
import MessageList from '@/components/ai/MessageList.vue'
import Composer from '@/components/ai/Composer.vue'
import { useAIStore } from '@/stores/ai'
import { useConfirmStore } from '@/stores/confirm'

const store = useAIStore()
const confirm = useConfirmStore()
const router = useRouter()

// AI configuration (providers, keys, defaults) lives in the centralized Settings
// page now; the chat's gear + "no provider" banner deep-link to it.
function openAISettings() {
  router.push({ name: 'settings', hash: '#ai' })
}

function onEdit({ id, content }) {
  store.editAndResend(id, content)
}

// Deleting truncates everything from this message onward, so confirm first.
async function onDelete(id) {
  const ok = await confirm.ask({
    title: 'Delete from here?',
    message: 'This removes this message and everything after it in the conversation. This cannot be undone.',
    danger: true,
    confirmLabel: 'Delete',
  })
  if (ok) store.deleteMessageFrom(id)
}
const railOpen = ref(false)
const emptyComposer = ref(null)

// Starter cards drop their prompt into the composer (rather than sending) so the
// operator can read and tweak it before sending.
function fillComposer(prompt) {
  emptyComposer.value?.setText(prompt)
}

const title = computed(
  () => store.conversations.find((c) => c.id === store.activeId)?.title || 'Assistant'
)

async function bootstrap() {
  // Settings carry the saved provider/model selection, so load them BEFORE
  // providers — loadProviders restores that selection on first paint.
  await store.loadSettings()
  await Promise.all([
    store.loadProviders(),
    store.loadConversations(),
  ])
}

onMounted(bootstrap)
// keep-alive re-entry: refresh conversations + provider/settings state, so
// changes made over in Settings -> AI are reflected when returning to the chat.
onActivated(() => {
  store.loadConversations()
  store.loadSettings()
  store.loadProviders()
})

// Power-user accelerator: Escape stops an in-flight stream (unless a dialog or
// menu is open, which owns Escape for closing itself).
function onKeydown(e) {
  if (e.key === 'Escape' && store.streaming && !document.querySelector('[role="dialog"], [role="menu"]')) {
    store.stop()
  }
}
onMounted(() => window.addEventListener('keydown', onKeydown))
onUnmounted(() => window.removeEventListener('keydown', onKeydown))
</script>
