<template>
  <div
    ref="editorRef"
    class="h-full w-full"
  />
</template>

<script setup>
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { EditorView, basicSetup } from 'codemirror'
import { EditorState } from '@codemirror/state'
import { javascript } from '@codemirror/lang-javascript'
import { python } from '@codemirror/lang-python'
import { oneDark } from '@codemirror/theme-one-dark'

const props = defineProps({
  modelValue: {
    type: String,
    default: ''
  },
  language: {
    type: String,
    default: 'javascript',
    validator: (value) => ['javascript', 'python', 'go', 'ruby', 'rust'].includes(value)
  },
  readOnly: {
    type: Boolean,
    default: false
  }
})

const emit = defineEmits(['update:modelValue'])

const editorRef = ref(null)
let view = null

const getLanguageExtension = (lang) => {
  switch (lang) {
    case 'python':
      return python()
    case 'javascript':
    case 'node':
      return javascript()
    default:
      return javascript()
  }
}

onMounted(() => {
  const startState = EditorState.create({
    doc: props.modelValue,
    extensions: [
      basicSetup,
      getLanguageExtension(props.language),
      oneDark,
      EditorView.updateListener.of((update) => {
        if (update.docChanged) {
          emit('update:modelValue', update.state.doc.toString())
        }
      }),
      EditorView.theme({
        '&': {
          fontSize: '14px',
          height: '100%',
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

// Watch for language changes
watch(() => props.language, (newLang) => {
  if (view) {
    view.dispatch({
      effects: EditorState.reconfigure.of([
        basicSetup,
        getLanguageExtension(newLang),
        oneDark,
        EditorView.updateListener.of((update) => {
          if (update.docChanged) {
            emit('update:modelValue', update.state.doc.toString())
          }
        }),
        EditorView.theme({
          '&': {
            fontSize: '14px',
            height: '100%',
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
      ]),
    })
  }
})
</script>

<style scoped>
/* CodeMirror styles are global by default */
</style>
