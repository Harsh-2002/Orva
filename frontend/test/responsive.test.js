import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join } from 'node:path'

// Mechanical half of DESIGN.md section 7. Every rule asserted here was already
// true in at least one view and false in another when it was written, which is
// the whole argument for checking it: the design system was never the problem,
// the absence of enforcement was.
//
// These are source-text assertions in the style of touchTargets.test.js and
// themeContrast.test.js. They cannot prove a layout looks right; they prove the
// specific mistakes that shipped cannot ship again.

const SRC = new URL('../src/', import.meta.url).pathname

function walk(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry)
    if (statSync(full).isDirectory()) walk(full, out)
    else if (entry.endsWith('.vue')) out.push(full)
  }
  return out
}

const files = walk(SRC)
const rel = (f) => f.slice(SRC.length)

/** Template section with HTML comments stripped, i.e. what actually renders. */
function template(src) {
  const i = src.indexOf('<template')
  if (i < 0) return ''
  const j = src.lastIndexOf('</template>')
  return src.slice(i, j < 0 ? undefined : j).replace(/<!--[\s\S]*?-->/g, '')
}

const sources = files.map((f) => ({ file: rel(f), src: readFileSync(f, 'utf8') }))
const templates = sources.map((s) => ({ ...s, tpl: template(s.src) }))

test('arbitrary grid tracks are separated by underscores, not commas', () => {
  // grid-template-columns is space-separated. A comma at the top level is a
  // parse error and the browser drops the whole declaration, so the grid
  // silently collapses to one column. Commas inside minmax()/repeat() are fine.
  const bad = []
  for (const { file, tpl } of templates) {
    for (const m of tpl.matchAll(/grid-cols-\[([^\]]+)\]/g)) {
      const stripped = m[1].replace(/\([^)]*\)/g, '')
      if (stripped.includes(',')) bad.push(`${file}: ${m[0]}`)
    }
  }
  assert.deepEqual(bad, [], `comma-separated arbitrary grid tracks compile to invalid CSS:\n${bad.join('\n')}`)
})

test('no component is defined with a runtime template string', () => {
  // Vue resolves to the runtime-only build (no registerRuntimeCompiler in the
  // bundle), so a `template:` option renders nothing at all. Use h().
  const bad = sources
    .filter(({ src }) => /^\s*template:\s*[`'"]/m.test(src))
    .map(({ file }) => file)
  assert.deepEqual(bad, [], `runtime template strings render nothing under the runtime-only Vue build:\n${bad.join('\n')}`)
})

test('layout properties are never animated', () => {
  // Transform and opacity only. Animating width/height/top/padding relayouts
  // the subtree on every frame, which is most visible on the dashboards that
  // re-render on a metrics poll.
  const bad = []
  for (const { file, src } of sources) {
    for (const m of src.matchAll(/transition-\[(width|height|top|left|right|bottom|padding|margin)[^\]]*\]/g)) {
      bad.push(`${file}: ${m[0]}`)
    }
    if (/\btransition-all\b/.test(src)) bad.push(`${file}: transition-all`)
  }
  assert.deepEqual(bad, [], `animate transform/opacity instead:\n${bad.join('\n')}`)
})

test('viewport-sized containers use dvh, not vh', () => {
  // 100vh is the large viewport on mobile browsers, so a vh-sized shell hides
  // its own bottom edge behind the retracting URL bar.
  const bad = []
  for (const { file, tpl } of templates) {
    for (const m of tpl.matchAll(/\b(?:min-)?h-screen\b/g)) bad.push(`${file}: ${m[0]}`)
    for (const m of tpl.matchAll(/\b(?:max-|min-)?h-\[(?:calc\()?[^\]]*\b100vh\b[^\]]*\]/g)) bad.push(`${file}: ${m[0]}`)
  }
  assert.deepEqual(bad, [], `use h-dvh / 100dvh:\n${bad.join('\n')}`)
})

test('every table is reachable below 640px', () => {
  // Two legitimate shapes, and nothing else. Either the view swaps the table
  // for a `sm:hidden` card list, or the table can be scrolled sideways inside
  // its own bounds. The failure this catches is the third shape: a full table
  // kept at phone width with columns merely hidden, which leaves the row
  // actions off-screen with no way to reach them, because the document itself
  // never scrolls horizontally.
  const bad = []
  for (const { file, src, tpl } of templates) {
    if (!/<table\b/.test(tpl)) continue
    const hasMobileList = /<ul[^>]*\bsm:hidden\b/.test(tpl)
    const scrollsInTemplate = /\boverflow-x-auto\b/.test(tpl)
    const scrollsInStyle = /overflow-x:\s*auto/.test(src)
    if (!hasMobileList && !scrollsInTemplate && !scrollsInStyle) bad.push(file)
  }
  assert.deepEqual(bad, [], `table with neither a mobile card list nor its own horizontal scroll:\n${bad.join('\n')}`)
})

test('mobile cards expose every action the desktop row exposes', () => {
  // The headline regression this suite exists for: three views had dropped an
  // action from the mobile branch, so the capability did not exist on a phone.
  // IconButton requires `title`, which makes the action set mechanically
  // comparable between the two branches.
  const bad = []
  for (const { file, tpl } of templates) {
    const cut = tpl.search(/<table[^>]*\bhidden\s+sm:table\b/)
    if (cut < 0) continue
    const titles = (chunk) =>
      [...chunk.matchAll(/<IconButton[\s\S]*?\stitle="([^"]+)"/g)].map((m) => m[1])
    const mobile = new Set(titles(tpl.slice(0, cut)))
    for (const t of titles(tpl.slice(cut))) {
      if (!mobile.has(t)) bad.push(`${file}: "${t}" is desktop-only`)
    }
  }
  assert.deepEqual(bad, [], `actions missing from the mobile card:\n${bad.join('\n')}`)
})

test('no em dashes in user-facing strings', () => {
  // DESIGN.md forbids them in rendered copy. Comments keep theirs.
  const bad = []
  for (const { file, tpl } of templates) {
    if (tpl.includes('—')) {
      const line = tpl.split('\n').find((l) => l.includes('—'))
      bad.push(`${file}: ${line.trim().slice(0, 60)}`)
    }
  }
  assert.deepEqual(bad, [], `rewrite with periods, commas, colons or parentheses:\n${bad.join('\n')}`)
})

test('compact controls carry a touch-target expander', () => {
  // Read against the 95% root: h-7=26.6, h-8=30.4, h-10=38, h-12=45.6px. Only
  // lg clears 44 unaided, so md — the default size, and the page-header CTA on
  // every view — needs expanding too.
  const button = readFileSync(join(SRC, 'components/common/Button.vue'), 'utf8')
  for (const [size, cls] of [['xs', 'touch-expand-xs'], ['sm', 'touch-expand-sm']]) {
    assert.match(button, new RegExp(`case '${size}':[^\\n]*${cls}`), `Button size ${size} lost ${cls}`)
  }
  assert.match(button, /default:\s*return '[^']*touch-expand-md/, 'Button default (md) is 38px and must carry touch-expand-md')
})

test('form controls clear the iOS focus-zoom threshold on touch hardware', () => {
  // text-base is 15.2px under the 95% root and does NOT clear iOS Safari's
  // 16px threshold, so the fix has to be a real pixel value, applied once.
  const styles = readFileSync(join(SRC, 'style.css'), 'utf8')
  assert.match(
    styles,
    /@media \(pointer: coarse\)\s*\{[\s\S]*?input,[\s\S]*?textarea\s*\{[\s\S]*?font-size:\s*16px/,
    'style.css must pin input/select/textarea to 16px on coarse pointers',
  )
})

test('stretched SVG strokes are pinned to a constant weight', () => {
  // preserveAspectRatio="none" scales the stroke with the box, so the same
  // chart renders at a different line weight in a narrow card than a wide one.
  const bad = []
  for (const { file, src } of sources) {
    if (!src.includes("preserveAspectRatio: 'none'") && !src.includes('preserveAspectRatio="none"')) continue
    if (!src.includes('non-scaling-stroke')) bad.push(file)
  }
  assert.deepEqual(bad, [], `add vector-effect="non-scaling-stroke":\n${bad.join('\n')}`)
})
