<template>
  <div
    ref="editorRef"
    class="h-full w-full"
  />
</template>

<script setup>
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { EditorView, basicSetup } from 'codemirror'
import { EditorState, Compartment } from '@codemirror/state'
import { javascript } from '@codemirror/lang-javascript'
import { python } from '@codemirror/lang-python'
import { oneDark } from '@codemirror/theme-one-dark'

const props = defineProps({
  modelValue: {
    type: String,
    default: ''
  },
  // Accepts either the codemirror language id (`javascript`, `python`) or
  // an Orva runtime id (`node22`, `python314`, ...) — the editor maps both
  // shapes onto the same CM language extension.
  language: {
    type: String,
    default: 'javascript'
  },
  readOnly: {
    type: Boolean,
    default: false
  }
})

const emit = defineEmits(['update:modelValue'])

const editorRef = ref(null)
let view = null
const languageCompartment = new Compartment()

const getLanguageExtension = (lang) => {
  if (lang?.startsWith('python')) return python()
  if (lang?.startsWith('node') || lang === 'javascript') return javascript()
  return javascript()
}

onMounted(() => {
  const startState = EditorState.create({
    doc: props.modelValue,
    extensions: [
      basicSetup,
      languageCompartment.of(getLanguageExtension(props.language)),
      oneDark,
      EditorView.updateListener.of((update) => {
        if (update.docChanged) {
          emit('update:modelValue', update.state.doc.toString())
        }
      }),
      EditorView.theme({
        '&': {
          // 16 px on phones (the smallest font-size iOS Safari accepts
          // without auto-zooming on focus); back to 14 px from sm up
          // where the dashboard's information density wins. The media
          // query lives inside CodeMirror's own theme system so the
          // change applies to .cm-content and propagates correctly.
          fontSize: '16px',
          height: '100%',
        },
        '@media (min-width: 640px)': {
          '&': { fontSize: '14px' },
        },
        '.cm-scroller': {
          fontFamily: 'JetBrains Mono, monospace',
          lineHeight: '1.6',
        },
        '.cm-content': {
          padding: '16px 0',
        },
        '.cm-line': {
          padding: '0 16px',
        },
      }),
      EditorState.readOnly.of(props.readOnly),
    ],
  })

  view = new EditorView({
    state: startState,
    parent: editorRef.value,
  })
})

onUnmounted(() => {
  if (view) {
    view.destroy()
  }
})

// Watch for external changes
watch(() => props.modelValue, (newValue) => {
  if (view && newValue !== view.state.doc.toString()) {
    view.dispatch({
      changes: {
        from: 0,
        to: view.state.doc.length,
        insert: newValue,
      },
    })
  }
})

// Watch for language changes — swap only the language extension via the
// Compartment, so theme, listeners, and read-only state are preserved.
watch(() => props.language, (newLang) => {
  if (view) {
    view.dispatch({
      effects: languageCompartment.reconfigure(getLanguageExtension(newLang)),
    })
  }
})
</script>

<style scoped>
/* CodeMirror styles are global by default */
</style>
