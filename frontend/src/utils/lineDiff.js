// Line-first diff for the Compare versions view.
//
// @codemirror/merge diffs the two documents character by character, and its
// default budget (scanLimit 500) makes it give up on any substantial edit and
// mark the whole range as replaced: lines identical in both versions came out
// highlighted, and a 3,000-line file with a third of its lines edited showed
// 2,999 changed lines. With no budget the same diff took 15 s and froze the page.
// Diffing whole lines first is linear in practice; the character diff then only
// runs inside blocks that changed, which is what drives the inline highlights.

const MAX_DISTINCT_LINES = 50000 // each distinct line becomes one UTF-16 unit, kept clear of surrogates
const LINE_TIMEOUT_MS = 1500
const REFINE_BUDGET_MS = 400 // total, across every changed block
const REFINE_MAX_CHARS = 20000 // a block larger than this keeps whole-line marks

function lineStarts(text) {
  const starts = [0]
  for (let i = text.indexOf('\n'); i !== -1; i = text.indexOf('\n', i + 1)) starts.push(i + 1)
  if (starts[starts.length - 1] !== text.length) starts.push(text.length)
  return starts // starts[k] is where line k begins; the last entry is the text length
}

// makeLineDiff builds a function for MergeView / unifiedMergeView's
// diffConfig.override. `diff` and `Change` are @codemirror/merge's exports,
// passed in so the module stays loadable (and testable) without the editor chunk.
export function makeLineDiff({ diff, Change }) {
  return function lineDiff(a, b) {
    if (a === b) return []
    const sa = lineStarts(a)
    const sb = lineStarts(b)
    const intern = new Map()
    const encode = (text, starts) => {
      let out = ''
      for (let k = 0; k + 1 < starts.length; k++) {
        const line = text.slice(starts[k], starts[k + 1])
        let id = intern.get(line)
        if (id === undefined) {
          id = intern.size
          intern.set(line, id)
        }
        out += String.fromCharCode(0x100 + id)
      }
      return out
    }
    const ea = encode(a, sa)
    const eb = encode(b, sb)
    if (intern.size > MAX_DISTINCT_LINES) return diff(a, b, { scanLimit: 500, timeout: LINE_TIMEOUT_MS })

    const deadline = Date.now() + REFINE_BUDGET_MS
    const out = []
    for (const c of diff(ea, eb, { timeout: LINE_TIMEOUT_MS })) {
      const fromA = sa[c.fromA], toA = sa[c.toA], fromB = sb[c.fromB], toB = sb[c.toB]
      const refinable = toA > fromA && toB > fromB && toA - fromA + (toB - fromB) <= REFINE_MAX_CHARS
      const left = deadline - Date.now()
      if (!refinable || left <= 0) {
        out.push(new Change(fromA, toA, fromB, toB))
        continue
      }
      // Equal line counts pair up one to one, as GitHub does, so a word change
      // never spans lines; otherwise the block is diffed as a whole.
      const pairs = c.toA - c.fromA === c.toB - c.fromB
        ? Array.from({ length: c.toA - c.fromA }, (_, k) => [sa[c.fromA + k], sa[c.fromA + k + 1], sb[c.fromB + k], sb[c.fromB + k + 1]])
        : [[fromA, toA, fromB, toB]]
      for (const [pa, za, pb, zb] of pairs) {
        const inner = diff(a.slice(pa, za), b.slice(pb, zb), { scanLimit: 500, timeout: Math.max(1, deadline - Date.now()) })
        for (const d of inner) out.push(new Change(pa + d.fromA, pa + d.toA, pb + d.fromB, pb + d.toB))
      }
    }
    return out
  }
}
