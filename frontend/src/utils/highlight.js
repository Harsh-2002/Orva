// Shared highlight.js instance for every surface that renders code: the chat
// UI and the Docs page. hljs/lib/core is a singleton
// module, so registering languages here makes them available to every importer
// (MessagePart's markdown fences, CodeBlock's standalone blocks, tool-call JSON).
// Centralising the language set + theme import keeps the two consumers in sync
// and the bundle lean (only the languages we actually render).
import hljs from 'highlight.js/lib/core'
import javascript from 'highlight.js/lib/languages/javascript'
import typescript from 'highlight.js/lib/languages/typescript'
import python from 'highlight.js/lib/languages/python'
import json from 'highlight.js/lib/languages/json'
import bash from 'highlight.js/lib/languages/bash'
import xml from 'highlight.js/lib/languages/xml'
import css from 'highlight.js/lib/languages/css'
import yaml from 'highlight.js/lib/languages/yaml'
import http from 'highlight.js/lib/languages/http'
// Token colours come from the dashboard's own theme tokens, not the vendored
// GitHub palette. See highlight.css for the mapping and why it is scoped the
// way it is.
import './highlight.css'

hljs.registerLanguage('javascript', javascript)
hljs.registerLanguage('typescript', typescript)
hljs.registerLanguage('python', python)
hljs.registerLanguage('json', json)
hljs.registerLanguage('bash', bash)
hljs.registerLanguage('xml', xml)
hljs.registerLanguage('css', css)
hljs.registerLanguage('yaml', yaml)
hljs.registerLanguage('http', http)

// Common aliases the model emits in fences but hljs doesn't register by default.
const ALIASES = { js: 'javascript', ts: 'typescript', py: 'python', sh: 'bash', shell: 'bash', yml: 'yaml', html: 'xml' }

function escapeHtml(s) {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

// highlightCode returns highlighted HTML for a code string, falling back to
// escaped plain text when the language is unknown or highlighting throws.
export function highlightCode(code, lang) {
  const resolved = ALIASES[(lang || '').toLowerCase()] || (lang || '').toLowerCase()
  if (resolved && hljs.getLanguage(resolved)) {
    try {
      return hljs.highlight(code, { language: resolved, ignoreIllegals: true }).value
    } catch {
      // fall through to escaped plain text
    }
  }
  return escapeHtml(code)
}

export default hljs
