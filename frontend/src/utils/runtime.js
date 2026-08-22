// One source for how a runtime is named in the UI.
//
// Orva ships exactly two generic runtimes and pins their versions in
// CONTRACT.md (Node 24, Python 3.14). The version is a display detail the API
// does not send -- `runtime` is just "node" or "python" -- so the words live
// here rather than being spelled out at each call site. They were previously
// written out twice (RuntimeTag and the editor strip) and printed raw in four
// more places, so the same function read "Node.js 24" on one screen and "node"
// on the next.

const isPython = (rt) => (rt || '').toLowerCase().startsWith('python')

// TypeScript and JavaScript both deploy onto the node runtime.
const isNode = (rt) => {
  const r = (rt || '').toLowerCase()
  return r.startsWith('node') || r.startsWith('javascript') || r.startsWith('typescript')
}

// runtimeLabel returns the operator-facing name, e.g. "Node.js 24".
// An unrecognised value falls back to whatever the API said, which is more
// useful than guessing.
export const runtimeLabel = (rt) => {
  if (isPython(rt)) return 'Python 3.14'
  if (isNode(rt)) return 'Node.js 24'
  return rt || ''
}

export { isPython, isNode }
