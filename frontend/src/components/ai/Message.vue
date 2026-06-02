<script setup>
import { ref, computed, nextTick } from 'vue'
import { Copy, Check, RotateCcw, Pencil, Trash2 } from 'lucide-vue-next'
import MessagePart from './MessagePart.vue'
import Button from '@/components/common/Button.vue'
import { copyText } from '@/utils/clipboard'

const props = defineProps({
  role: {
    type: String,
    required: true,
    validator: (v) => v === 'user' || v === 'assistant',
  },
  parts: {
    type: Array,
    default: () => [],
  },
  // Regenerate shows only on the last assistant message (when idle).
  canRegenerate: { type: Boolean, default: false },
  editable: { type: Boolean, default: false },
  deletable: { type: Boolean, default: false },
})

const emit = defineEmits(['regenerate', 'edit', 'delete'])

// Shared look for the inline action buttons (revealed on hover / focus).
const actionCls =
  'inline-flex items-center rounded-md px-1.5 py-1 text-[11px] text-foreground-muted opacity-0 transition-[opacity,color,background-color] hover:bg-surface-hover hover:text-foreground focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary focus-visible:ring-offset-2 focus-visible:ring-offset-background group-hover:opacity-100 max-md:opacity-100'

// Plain-text of the message for copy + edit seeding — skip reasoning parts.
const copyableText = computed(() =>
  props.parts
    .filter((p) => p.type === 'text' && p.text)
    .map((p) => p.text)
    .join('\n\n')
    .trim()
)

const copied = ref(false)
let resetTimer = null
async function onCopy() {
  const ok = await copyText(copyableText.value)
  if (!ok) return
  copied.value = true
  clearTimeout(resetTimer)
  resetTimer = setTimeout(() => { copied.value = false }, 1200)
}

// Inline edit (user messages): the bubble itself becomes editable in place via
// contenteditable, so it keeps its exact shape and content width and grows
// naturally as you type — no jump to a wide rectangular box. We seed and read
// the text imperatively (textContent/innerText) rather than binding it, so Vue
// never re-renders the node out from under the caret.
const editing = ref(false)
const editBox = ref(null)
const editEmpty = ref(false)
function startEdit() {
  editing.value = true
  nextTick(() => {
    const el = editBox.value
    if (!el) return
    el.textContent = copyableText.value
    editEmpty.value = !copyableText.value.trim()
    el.focus()
    // Place the caret at the end of the seeded text.
    const range = document.createRange()
    range.selectNodeContents(el)
    range.collapse(false)
    const sel = window.getSelection()
    sel.removeAllRanges()
    sel.addRange(range)
  })
}
function onEditInput() {
  editEmpty.value = !(editBox.value?.innerText || '').trim()
}
function cancelEdit() { editing.value = false }
function saveEdit() {
  const v = (editBox.value?.innerText || '').trim()
  if (!v) return
  editing.value = false
  emit('edit', v)
}
function onEditKey(e) {
  if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); saveEdit() }
  else if (e.key === 'Escape') { e.preventDefault(); cancelEdit() }
}
</script>

<template>
  <!--
    User messages read as input: a compact right-aligned bubble in the brand
    color, or an inline editor styled like the Composer when editing. Assistant
    messages read as output: bare on the surface, full column width. Both reveal
    a quiet action row on hover (always visible on touch).
  -->
  <div
    class="group flex"
    :class="{ 'justify-end': role === 'user' }"
  >
    <!-- USER -->
    <div
      v-if="role === 'user'"
      class="flex max-w-[85%] flex-col items-end"
    >
      <template v-if="editing">
        <!-- The bubble edits in place: same shape + content width as the sent
             message, but switches to the editable-field surface (bg-surface +
             border + the app's white focus ring) so it reads as an input. -->
        <div
          ref="editBox"
          contenteditable="plaintext-only"
          role="textbox"
          aria-multiline="true"
          aria-label="Edit message"
          class="max-w-full whitespace-pre-wrap break-words rounded-2xl rounded-br-md border border-border bg-surface px-3.5 py-2.5 text-sm leading-relaxed text-foreground outline-none transition-colors focus:border-white focus:ring-1 focus:ring-white"
          @input="onEditInput"
          @keydown="onEditKey"
        />
        <div class="mt-1.5 flex items-center gap-2">
          <Button
            size="xs"
            variant="ghost"
            @click="cancelEdit"
          >
            Cancel
          </Button>
          <Button
            size="xs"
            variant="primary"
            :disabled="editEmpty"
            @click="saveEdit"
          >
            Send
          </Button>
        </div>
      </template>
      <template v-else>
        <div class="rounded-2xl rounded-br-md bg-primary px-3.5 py-2.5 text-sm leading-relaxed text-primary-foreground">
          <MessagePart
            v-for="p in parts"
            :key="p.type"
            :part="p"
          />
        </div>
        <div class="mt-1 flex items-center gap-1">
          <button
            v-if="editable"
            type="button"
            :class="actionCls"
            aria-label="Edit and resend"
            title="Edit and resend"
            @click="startEdit"
          >
            <Pencil class="h-3 w-3" />
          </button>
          <button
            v-if="deletable"
            type="button"
            :class="actionCls"
            aria-label="Delete from here"
            title="Delete from here"
            @click="emit('delete')"
          >
            <Trash2 class="h-3 w-3" />
          </button>
        </div>
      </template>
    </div>

    <!-- ASSISTANT -->
    <div
      v-else
      class="w-full text-sm leading-relaxed text-foreground"
    >
      <MessagePart
        v-for="(p, index) in parts"
        :key="p.type"
        :part="p"
        :class="index > 0 ? 'mt-3' : ''"
      />
      <div class="mt-1.5 flex items-center gap-1">
        <button
          v-if="copyableText"
          type="button"
          :class="[actionCls, 'gap-1']"
          :title="copied ? 'Copied' : 'Copy message'"
          @click="onCopy"
        >
          <component
            :is="copied ? Check : Copy"
            class="h-3 w-3"
          />
          {{ copied ? 'Copied' : 'Copy' }}
        </button>
        <button
          v-if="canRegenerate"
          type="button"
          :class="[actionCls, 'gap-1']"
          title="Regenerate this answer"
          @click="emit('regenerate')"
        >
          <RotateCcw class="h-3 w-3" /> Retry
        </button>
        <button
          v-if="deletable"
          type="button"
          :class="actionCls"
          aria-label="Delete from here"
          title="Delete from here"
          @click="emit('delete')"
        >
          <Trash2 class="h-3 w-3" />
        </button>
      </div>
    </div>
  </div>
</template>
