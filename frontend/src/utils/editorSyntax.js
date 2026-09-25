import { javascript } from '@codemirror/lang-javascript'
import { python } from '@codemirror/lang-python'
import { ensureSyntaxTree } from '@codemirror/language'

export function editorLanguage(runtime, filename) {
  if (runtime?.startsWith('python')) return python()
  return javascript({ typescript: /\.(ts|mts|cts)$/i.test(filename || '') })
}

export function syntaxDiagnostics(state) {
  const tree = ensureSyntaxTree(state, state.doc.length, 50)
  if (!tree) return null
  const diagnostics = []
  tree.iterate({ enter(node) {
    if (!node.type.isError) return
    diagnostics.push({
      from: node.from,
      to: Math.min(state.doc.length, Math.max(node.from + 1, node.to)),
      severity: 'error',
      message: 'Syntax error',
    })
  } })
  return diagnostics
}

export function syntaxIssueAnnouncement(state, diagnostics) {
  if (!diagnostics?.length) return ''
  const first = diagnostics[0]
  const line = state.doc.lineAt(first.from)
  const column = first.from - line.from + 1
  if (diagnostics.length === 1) return `Syntax error at line ${line.number}, column ${column}.`
  return `${diagnostics.length} syntax errors. First at line ${line.number}, column ${column}.`
}
