import { describe, expect, it } from 'vitest'
import { EditorState } from '@codemirror/state'
import { editorLanguage, syntaxDiagnostics, syntaxIssueAnnouncement } from '@/utils/editorSyntax'

function issues(runtime, filename, source) {
  const state = EditorState.create({ doc: source, extensions: [editorLanguage(runtime, filename)] })
  return syntaxDiagnostics(state)
}

describe('editor syntax hints', () => {
  it('reports JavaScript syntax errors and clears after correction', () => {
    expect(issues('node', 'handler.js', 'exports.handler = () => {')).not.toHaveLength(0)
    expect(issues('node', 'handler.js', 'exports.handler = () => 200')).toHaveLength(0)
  })

  it('uses the filename to distinguish TypeScript from JavaScript', () => {
    const source = 'interface Answer { value: number }'
    expect(issues('node', 'handler.ts', source)).toHaveLength(0)
    expect(issues('node', 'handler.js', source)).not.toHaveLength(0)
  })

  it('reports Python syntax errors and accepts corrected code', () => {
    const diagnostics = issues('python', 'handler.py', 'def handler(:\n    pass')
    expect(diagnostics).not.toHaveLength(0)
    expect(diagnostics.every((diagnostic) => diagnostic.message === 'Syntax error' && !diagnostic.source)).toBe(true)
    expect(issues('python', 'handler.py', 'def handler(event):\n    return 200')).toHaveLength(0)
  })

  it('announces the first issue location only while an error exists', () => {
    const state = EditorState.create({ doc: 'ok\nbad(' })
    expect(syntaxIssueAnnouncement(state, null)).toBe('')
    expect(syntaxIssueAnnouncement(state, [])).toBe('')
    expect(syntaxIssueAnnouncement(state, [{ from: 6 }])).toBe('Syntax error at line 2, column 4.')
    expect(syntaxIssueAnnouncement(state, [{ from: 6 }, { from: 7 }]))
      .toBe('2 syntax errors. First at line 2, column 4.')
  })
})
