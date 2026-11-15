// Inline (word-level) marks for the Compare versions view.
//
// @codemirror/merge marks the text of every change, so a wholly added line got
// the line tint AND a second box over its text, and a removed line picked up a
// mark in the wrong colour. A diff reads like GitHub's when whole lines carry
// only the line tint and a word mark appears only where part of a line changed.

// inlineRanges lists the [from, to) ranges in one editor's document that get a
// word mark. `side` is 'a' (old text), 'b' (new text) or null for the unified
// view, whose document is the new text. `slice(from, to)` reads the document and
// `lineAt(pos)` returns the {from, to} of the line holding pos.
export function inlineRanges(chunks, side, slice, lineAt) {
  const isA = side === 'a'
  const out = []
  for (const chunk of chunks) {
    // A pure insertion or deletion has no counterpart lines: the line tint says it all.
    if (chunk.fromA === chunk.toA || chunk.fromB === chunk.toB) continue
    const base = isA ? chunk.fromA : chunk.fromB
    for (const change of chunk.changes) {
      const from = base + (isA ? change.fromA : change.fromB)
      const to = base + (isA ? change.toA : change.toB)
      if (to <= from) continue
      for (let at = from; at < to;) {
        const line = lineAt(at)
        const segTo = Math.min(to, line.to)
        // Marks that are whitespace-only (indentation) or cover nearly the whole
        // line add nothing to the line tint.
        const seg = slice(at, segTo).trim()
        const whole = slice(line.from, line.to).trim()
        if (seg && seg.length < 0.85 * whole.length) out.push([at, segTo])
        at = Math.max(segTo, line.to) + 1 // skip the newline
      }
    }
  }
  return out
}

// inlineDiffMarks builds the editor extension. The CodeMirror modules are passed
// in so this file stays loadable without the editor chunk.
export function inlineDiffMarks({ ViewPlugin, Decoration, RangeSetBuilder, getChunks }) {
  const mark = Decoration.mark({ class: 'cm-diffWord' })
  const build = (view) => {
    const builder = new RangeSetBuilder()
    const info = getChunks(view.state)
    if (info) {
      const slice = (from, to) => view.state.doc.sliceString(from, to)
      const lineAt = (pos) => view.state.doc.lineAt(pos)
      for (const [from, to] of inlineRanges(info.chunks, info.side, slice, lineAt)) builder.add(from, to, mark)
    }
    return builder.finish()
  }
  return ViewPlugin.fromClass(class {
    constructor(view) { this.decorations = build(view) }
    update(update) {
      // The chunk list arrives through an effect after the view is created, and
      // changes when the document is re-diffed.
      if (update.docChanged || getChunks(update.state)?.chunks !== getChunks(update.startState)?.chunks) {
        this.decorations = build(update.view)
      }
    }
  }, { decorations: (plugin) => plugin.decorations })
}
