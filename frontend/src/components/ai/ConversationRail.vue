<template>
  <!--
    ConversationRail — the conversation list. Rendered as a desktop <aside> and,
    on mobile, inside a Drawer bottom-sheet. It drives the store directly;
    `select` fires after any navigation action so the mobile drawer can close.
  -->
  <div class="flex h-full flex-col">
    <div
      v-if="!embedded"
      class="flex h-16 shrink-0 items-center justify-between px-4 border-b border-border"
    >
      <span class="text-sm font-semibold tracking-tight text-white">Conversations</span>
      <Button
        size="xs"
        variant="secondary"
        @click="onNew"
      >
        <Plus class="h-3.5 w-3.5" /> New
      </Button>
    </div>
    <div class="flex-1 overflow-y-auto scrollable p-2 space-y-0.5">
      <!-- In the mobile drawer the Drawer supplies the "Conversations" title,
           so the rail drops its own header and surfaces New as a list action. -->
      <button
        v-if="embedded"
        class="touch-expand-sm mb-1 flex w-full items-center gap-2 rounded-md border border-border px-2.5 py-2 text-left text-sm text-foreground transition-colors hover:bg-surface-hover focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary"
        @click="onNew"
      >
        <Plus class="h-3.5 w-3.5 shrink-0" /> New conversation
      </button>
      <div
        v-for="c in store.conversations"
        :key="c.id"
        class="group flex w-full items-center gap-0.5 rounded-md pr-1 transition-colors"
        :class="c.id === store.activeId ? 'bg-primary/15' : 'hover:bg-surface-hover'"
      >
        <button
          class="touch-expand-sm flex min-w-0 flex-1 items-center gap-2 rounded-md px-2.5 py-2 text-left text-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary"
          :class="c.id === store.activeId ? 'text-white' : 'text-foreground-muted group-hover:text-white'"
          :aria-current="c.id === store.activeId ? 'page' : undefined"
          @click="onOpen(c.id)"
        >
          <MessageSquare class="h-3.5 w-3.5 shrink-0 opacity-70" />
          <span class="flex-1 truncate">{{ c.title || 'New conversation' }}</span>
        </button>
        <button
          type="button"
          class="touch-expand-iconbtn shrink-0 rounded-md p-2 text-foreground-muted opacity-0 transition-opacity hover:bg-surface-hover hover:text-white focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary group-hover:opacity-100 max-md:opacity-100"
          title="Rename conversation"
          aria-label="Rename conversation"
          @click.stop="onRename(c)"
        >
          <Pencil class="h-3.5 w-3.5" />
        </button>
        <button
          type="button"
          class="touch-expand-iconbtn shrink-0 rounded-md p-2 text-foreground-muted opacity-0 transition-opacity hover:bg-surface-hover hover:text-danger-fg focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary group-hover:opacity-100 max-md:opacity-100"
          title="Delete conversation"
          aria-label="Delete conversation"
          @click.stop="onDelete(c.id)"
        >
          <Trash2 class="h-3.5 w-3.5" />
        </button>
      </div>
      <p
        v-if="!store.conversations.length"
        class="px-2.5 py-2 text-xs text-foreground-muted"
      >
        No conversations yet.
      </p>
    </div>
    <div
      v-if="store.conversations.length"
      class="shrink-0 border-t border-border p-2"
    >
      <p
        v-if="clearError"
        class="px-2.5 pb-1.5 text-xs leading-snug text-danger-fg"
        role="alert"
      >
        {{ clearError }}
      </p>
      <button
        type="button"
        class="touch-expand-sm flex h-8 w-full items-center gap-2 rounded-md px-2.5 text-left text-xs text-foreground-muted transition-colors hover:bg-surface-hover hover:text-danger-fg focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-primary disabled:cursor-not-allowed disabled:opacity-50"
        :disabled="clearing"
        @click="onClearAll"
      >
        <LoaderCircle
          v-if="clearing"
          class="h-3.5 w-3.5 shrink-0 animate-spin motion-reduce:animate-none"
        />
        <Trash2
          v-else
          class="h-3.5 w-3.5 shrink-0"
        />
        {{ clearing ? 'Clearing…' : 'Clear all chats' }}
      </button>
    </div>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { Plus, MessageSquare, Trash2, Pencil, LoaderCircle } from '@lucide/vue'
import Button from '@/components/common/Button.vue'
import { useAIStore } from '@/stores/ai'
import { useConfirmStore } from '@/stores/confirm'

const store = useAIStore()
const confirm = useConfirmStore()
const clearing = ref(false)
const clearError = ref('')

async function onRename(c) {
  const title = await confirm.prompt({
    title: 'Rename conversation',
    defaultValue: c.title || '',
    placeholder: 'Conversation name',
    confirmLabel: 'Rename',
  })
  if (title != null && title.trim()) store.renameConversation(c.id, title.trim())
}

async function onDelete(id) {
  const ok = await confirm.ask({
    title: 'Delete conversation?',
    message: 'This permanently deletes the conversation and all its messages.',
    danger: true,
    confirmLabel: 'Delete',
  })
  if (ok) store.deleteConversation(id)
}

async function onClearAll() {
  const count = store.conversations.length
  if (!count || clearing.value) return
  const noun = count === 1 ? 'chat' : 'chats'
  const ok = await confirm.ask({
    title: 'Clear all chats?',
    message: `This permanently deletes all ${count} ${noun} and their messages. This cannot be undone.`,
    danger: true,
    confirmLabel: 'Clear all',
  })
  if (!ok) return

  clearing.value = true
  clearError.value = ''
  try {
    await store.clearConversations()
    emit('select')
  } catch (err) {
    clearError.value = err?.response?.status === 409
      ? 'A chat is still responding. Stop it and try again.'
      : 'Chats could not be cleared. Try again.'
  } finally {
    clearing.value = false
  }
}

defineProps({
  // When true (inside the mobile Drawer) the rail hides its own header and
  // surfaces New as a list action, since the Drawer already titles the sheet.
  embedded: { type: Boolean, default: false },
})

const emit = defineEmits(['select'])

function onNew() {
  store.newConversation()
  emit('select')
}
function onOpen(id) {
  store.openConversation(id)
  emit('select')
}
</script>
