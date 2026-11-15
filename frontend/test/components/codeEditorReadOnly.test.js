import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { nextTick } from 'vue'
import { EditorView } from 'codemirror'
import CodeEditor from '@/components/common/CodeEditor.vue'

// View/Edit flips readOnly on a live editor. It has to reconfigure in place:
// rebuilding the state would drop the buffer, scroll position and selection.

window.matchMedia = window.matchMedia || ((q) => ({
  matches: false, media: q, onchange: null, dispatchEvent: () => false,
  addEventListener() {}, removeEventListener() {}, addListener() {}, removeListener() {},
}))
// jsdom has no layout; CodeMirror measures ranges on every update.
window.Range.prototype.getClientRects ||= () => []
window.Range.prototype.getBoundingClientRect ||= () => ({ top: 0, left: 0, right: 0, bottom: 0, width: 0, height: 0 })

describe('CodeEditor readOnly', () => {
  it('toggles read-only without losing the buffer or the selection', async () => {
    const w = mount(CodeEditor, {
      props: { modelValue: 'print("hi")', language: 'python', readOnly: true },
      attachTo: document.body,
    })
    await nextTick()
    const view = EditorView.findFromDOM(w.element)
    expect(view.state.readOnly).toBe(true)
    expect(view.contentDOM.getAttribute('aria-readonly')).toBe('true')

    view.dispatch({ selection: { anchor: 2, head: 5 } })
    await w.setProps({ readOnly: false })
    expect(view.state.readOnly).toBe(false)
    expect(view.contentDOM.getAttribute('aria-readonly')).toBe('false')
    expect(view.state.doc.toString()).toBe('print("hi")')
    expect(view.state.selection.main.from).toBe(2)
    expect(view.state.selection.main.to).toBe(5)

    // Editing works again, and locking again keeps what was typed.
    view.dispatch({ changes: { from: 0, insert: '# ' } })
    expect(w.emitted('update:modelValue').at(-1)).toEqual(['# print("hi")'])
    await w.setProps({ readOnly: true })
    expect(view.state.readOnly).toBe(true)
    expect(view.state.doc.toString()).toBe('# print("hi")')
    w.unmount()
  })
})
