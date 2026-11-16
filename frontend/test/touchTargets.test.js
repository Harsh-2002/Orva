import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

const styles = readFileSync(new URL('../src/style.css', import.meta.url), 'utf8')

// The coarse-pointer block, isolated. Reading the whole file would let a rule
// from the fine-pointer half satisfy an assertion about the touch half.
function coarseBlock() {
  const at = styles.indexOf('Touch-target hit-area expansion')
  assert.ok(at > -1, 'the touch-target section is gone; these assertions are aimed at nothing')
  const from = styles.indexOf('@media (pointer: coarse)', at)
  assert.ok(from > -1, 'no coarse-pointer block follows the touch-target section')
  // Balance braces so the block ends where it really ends.
  let depth = 0
  for (let i = styles.indexOf('{', from); i < styles.length; i++) {
    if (styles[i] === '{') depth++
    else if (styles[i] === '}' && --depth === 0) return styles.slice(from, i + 1)
  }
  throw new Error('unbalanced coarse-pointer block')
}

// Tier -> the box height it takes on a phone. These are the numbers the section's comment
// claims; the point of the test is that the claim and the CSS cannot drift.
const TIERS = { xs: 32, sm: 36, md: 40, iconbtn: 40 }

// An extension may not reach further than half the smallest gap the toolbars
// use (gap-2, 8px at the 95% root -> 7.6px). Beyond that it lands on the
// neighbour, which is exactly how the first implementation made the row below
// capture taps meant for the row above.
const MAX_INSET = 6

function declaredHeight(block, tier) {
  const m = new RegExp(`\\.touch-expand-${tier}\\s*\\{[^}]*?min-height:\\s*(\\d+)px`).exec(block)
  assert.ok(m, `.touch-expand-${tier} declares no min-height on coarse pointers`)
  return Number(m[1])
}

function declaredTarget(block, tier) {
  const declarations = {}
  const css = block.replace(/\/\*[\s\S]*?\*\//g, '')
  for (const [, selectors, body] of css.matchAll(/([^{}]+)\{([^{}]*)\}/g)) {
    if (!selectors.split(',').map(value => value.trim()).includes(`.touch-expand-${tier}::after`)) continue
    for (const declaration of body.split(';')) {
      const [name, value] = declaration.split(':')
      if (value) declarations[name.trim()] = value.trim()
    }
  }
  assert.equal(declarations.position, 'absolute')
  assert.equal(declarations.content, "''")
  assert.equal(declarations.top, 'calc(50% - 22px)', 'target must stay centered in the padding box')
  assert.equal(declarations.height, '44px', 'target height must not lose pixels to the control border')
  if (tier === 'iconbtn') {
    assert.equal(declarations.left, 'calc(50% - 22px)')
    assert.equal(declarations.width, '44px')
  }
  return Number.parseFloat(declarations.height)
}

test('no tier meets the touch floor by inflating its own box', () => {
  // The regression this replaces: min-height:44px on all four tiers. That is
  // box height, not a hit area, so every tier collapsed into one slab -- 439
  // of 602 visible controls measured exactly 44px on a 360px phone, and a chip
  // carrying 11.4px text became the same object as a primary button.
  const block = coarseBlock()
  for (const tier of Object.keys(TIERS)) {
    const h = declaredHeight(block, tier)
    assert.notEqual(h, 44,
      `.touch-expand-${tier} is back to a 44px box. Give it a real height and buy the target with ::after.`)
  }
})

test('each tier keeps its own height, in proportion to its type', () => {
  const block = coarseBlock()
  const seen = new Set()
  for (const [tier, expected] of Object.entries(TIERS)) {
    assert.equal(declaredHeight(block, tier), expected,
      `.touch-expand-${tier} should be ${expected}px on a phone`)
    seen.add(expected)
  }
  // Three distinct heights across four tiers (iconbtn shares md's 40px).
  assert.ok(seen.size >= 3, 'the tiers have collapsed back into a single height')
})

test('centered extension is exactly the 44px target, in every tier', () => {
  const block = coarseBlock()
  for (const [tier, height] of Object.entries(TIERS)) {
    assert.equal(Math.max(height, declaredTarget(block, tier)), 44)
  }
})

test('no extension reaches far enough to land on a neighbour', () => {
  const block = coarseBlock()
  for (const tier of Object.keys(TIERS)) {
    const inset = (declaredTarget(block, tier) - declaredHeight(block, tier)) / 2
    assert.ok(inset <= MAX_INSET,
      `.touch-expand-${tier} extends ${inset}px, past the ${MAX_INSET}px bound. ` +
      'A gap-2 toolbar leaves 7.6px between controls; anything wider overlaps the next one.')
  }
})

test('a stacked row takes real height, because it has no gap to borrow', () => {
  const block = coarseBlock()
  assert.match(block, /\.touch-expand-row\s*\{[^}]*?min-height:\s*44px/,
    '.touch-expand-row must take the full 44px as box height')
  assert.doesNotMatch(block, /\.touch-expand-row::(?:after|before)/,
    'a stacked row must not extend outward: its neighbour is flush against it, ' +
    'which is how the sidebar produced 97 overlapping targets across the route set')
})

test('stacked picker options use actual row height, not overlapping hit extensions', () => {
  const picker = readFileSync(new URL('../src/components/common/FilterSelect.vue', import.meta.url), 'utf8')
  const option = picker.match(/<button\b[^>]*role="menuitemradio"[^>]*>/)?.[0]
  assert.ok(option, 'the picker option button must be inspected')
  assert.match(option, /\btouch-expand-row\b/)
  assert.doesNotMatch(option, /\btouch-expand-(?:xs|sm|md|iconbtn)\b/)
})

test('leaving overlays stop intercepting clicks immediately', () => {
  assert.match(styles, /\.fade-leave-active\s*\{[\s\S]*?pointer-events: none;/)
})
