<template>
  <div
    ref="editorRef"
    class="h-full w-full"
  />
</template>

<script setup>
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { EditorView, basicSetup } from 'codemirror'
import { EditorState, Compartment, Prec } from '@codemirror/state'
import { javascript } from '@codemirror/lang-javascript'
import { python } from '@codemirror/lang-python'
import { oneDark } from '@codemirror/theme-one-dark'
import { HighlightStyle, syntaxHighlighting } from '@codemirror/language'
import { tags } from '@lezer/highlight'

// one-dark paints names, property names and characters in #e06c75, which is
// 4.38:1 on the editor's #282c34 and lower still over the active-line tint --
// under the 4.5:1 AA floor for 16 px body text. It is the only token in the
// theme that misses; every other one clears it comfortably. This lifts just
// that one to #e8858d (5.45:1), which reads as the same red.
//
// Prec.highest is required: syntaxHighlighting added after oneDark would
// otherwise sit at lower precedence and never win.
const contrastPatch = Prec.highest(syntaxHighlighting(HighlightStyle.define([
  {
    tag: [tags.name, tags.propertyName, tags.deleted, tags.character, tags.macroName],
    color: '#e8858d',
  },
])))

const props = defineProps({
  modelValue: {
    type: String,
    default: ''
  },
  // Accepts either the codemirror language id (`javascript`, `python`) or
  // an Orva runtime id (`node`, `python`) — the editor maps both
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
  // typescript: true on every JS-family document, deliberately. TypeScript
  // functions deploy under runtime "node" (the platform ships two generic
  // runtimes and runs tsc at build time), so the language id never says
  // "typescript" and .ts source was getting the plain-JS grammar: annotations,
  // interfaces and generics all highlighted as syntax errors in a file the
  // platform fully supports. TS is a superset, so plain .js still parses
  // correctly under it, which makes this the right default rather than a
  // special case that needs the filename plumbed down from the editor.
  return javascript({ typescript: true })
}

onMounted(() => {
  const startState = EditorState.create({
    doc: props.modelValue,
    extensions: [
      basicSetup,
      languageCompartment.of(getLanguageExtension(props.language)),
      oneDark,
      contrastPatch,
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
