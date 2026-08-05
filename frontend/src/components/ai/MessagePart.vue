<script setup>
import { ref, watch, onBeforeUnmount } from 'vue'
import hljs from '@/utils/highlight'
import { createMarkdownRenderer } from '@/utils/markdown'
import CodeBlock from './CodeBlock.vue'
import ThinkingBlock from './ThinkingBlock.vue'

const props = defineProps({
  part: {
    type: Object,
    required: true,
  },
})

const md = createMarkdownRenderer({
  html: false,
  linkify: true,
  breaks: true,
  // Only nested fences (inside lists/quotes) hit this path; top-level fences are
  // extracted into <CodeBlock> below. Returns highlighted inner HTML; md wraps it.
  highlight(str, lang) {
    if (lang && hljs.getLanguage(lang)) {
      try { return hljs.highlight(str, { language: lang, ignoreIllegals: true }).value } catch { /* default */ }
    }
    return ''
  },
})

// Split the markdown into ordered segments so top-level fenced code renders as a
// real <CodeBlock> (language label + copy + collapse) while prose stays HTML.
// md.parse tolerates an unterminated fence (it runs the code to EOF), so a
// half-streamed ```block shows live in a code block — exactly what we want.
function parse(text) {
  text = text || ''
  let tokens
  try { tokens = md.parse(text, {}) } catch { return [{ type: 'html', html: md.render(text) }] }

  const out = []
  let buffer = []
  const flush = () => {
    if (!buffer.length) return
    out.push({ type: 'html', html: md.renderer.render(buffer, md.options, {}) })
    buffer = []
  }
  for (const tok of tokens) {
    if (tok.type === 'fence') {
      flush()
      out.push({ type: 'code', code: tok.content.replace(/\n$/, ''), lang: (tok.info || '').trim().split(/\s+/)[0] })
    } else {
      buffer.push(tok)
    }
  }
  flush()
  return out
}

// During streaming props.part.text changes on every token; re-parsing markdown
// per token spikes CPU on long answers. Throttle to ~12/s with leading + trailing
// edges, so the first and the final render are always exact. Static (loaded)
// messages parse once at setup and the watcher never fires.
const segments = ref(parse(props.part.text))
let parseTimer = null
let lastParse = 0
watch(
  () => props.part.text,
  () => {
    const since = Date.now() - lastParse
    const run = () => { lastParse = Date.now(); segments.value = parse(props.part.text) }
    if (since >= 80) run()
    else { clearTimeout(parseTimer); parseTimer = setTimeout(run, 80 - since) }
  },
)
onBeforeUnmount(() => clearTimeout(parseTimer))
</script>

<template>
  <div v-if="part.type === 'text'">
    <template
      v-for="(seg, i) in segments"
      :key="i"
    >
      <CodeBlock
        v-if="seg.type === 'code'"
        :code="seg.code"
        :lang="seg.lang"
      />
      <div
        v-else
        class="md-body"
        v-html="seg.html"
      />
    </template>
  </div>
  <ThinkingBlock
    v-else-if="part.type === 'thinking'"
    :part="part"
  />
</template>

<style scoped>
.md-body :deep(p) {
  margin: 0.25rem 0;
}
.md-body :deep(p:first-child) {
  margin-top: 0;
}
.md-body :deep(p:last-child) {
  margin-bottom: 0;
}
.md-body :deep(pre) {
  background: var(--color-background, #12111c);
  border: 1px solid var(--color-border, #2d2b42);
  border-radius: 0.5rem;
  padding: 0.75rem;
  overflow-x: auto;
  margin: 0.5rem 0;
  font-size: 0.8125rem;
}
.md-body :deep(code) {
  font-family: var(--font-mono, monospace);
  font-size: 0.85em;
}
.md-body :deep(:not(pre) > code) {
  background: var(--color-surface-hover);
  padding: 0.1em 0.35em;
  border-radius: 0.25rem;
}
.md-body :deep(a) {
  color: var(--color-link);
  text-decoration: underline;
}
.md-body :deep(ul),
.md-body :deep(ol) {
  padding-left: 1.25rem;
  margin: 0.25rem 0;
}
.md-body :deep(ul) {
  list-style: disc;
}
.md-body :deep(ol) {
  list-style: decimal;
}
.md-body :deep(h1),
.md-body :deep(h2),
.md-body :deep(h3),
.md-body :deep(h4),
.md-body :deep(h5),
.md-body :deep(h6) {
  font-weight: 600;
  line-height: 1.3;
  margin: 0.6rem 0 0.25rem;
}
.md-body :deep(h1) { font-size: 1.0625rem; }
.md-body :deep(h2) { font-size: 1rem; }
.md-body :deep(h3) { font-size: 0.9375rem; }
.md-body :deep(h1:first-child),
.md-body :deep(h2:first-child),
.md-body :deep(h3:first-child),
.md-body :deep(h4:first-child) {
  margin-top: 0;
}
.md-body :deep(strong) {
  font-weight: 600;
}
.md-body :deep(em) {
  font-style: italic;
}
.md-body :deep(hr) {
  border: 0;
  border-top: 1px solid var(--color-border, #2d2b42);
  margin: 0.75rem 0;
}
.md-body :deep(table) {
  border-collapse: collapse;
  margin: 0.5rem 0;
  display: block;
  overflow-x: auto;
  font-size: inherit;
}
.md-body :deep(th),
.md-body :deep(td) {
  border: 1px solid var(--color-border, #2d2b42);
  padding: 0.35rem 0.6rem;
  text-align: left;
}
.md-body :deep(th) {
  background: var(--color-surface, #1a1929);
  font-weight: 600;
}
.md-body :deep(blockquote) {
  border-left: 2px solid var(--color-border, #2d2b42);
  padding-left: 0.75rem;
  margin: 0.5rem 0;
  color: var(--color-foreground-muted, #a3a3b3);
}
</style>
