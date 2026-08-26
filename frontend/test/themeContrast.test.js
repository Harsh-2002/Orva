import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

const styles = readFileSync(new URL('../src/style.css', import.meta.url), 'utf8')

// Two themes now live in this file, so the parser has to be scope-aware.
//
// The previous version matched `--color-background:` with a NON-GLOBAL regex over
// the whole file, which takes the first occurrence. Adding a day block below
// @theme would have left every assertion below reading the night values and
// PASSING VACUOUSLY while claiming to cover both. That is the failure mode this
// split exists to prevent, so the blocks are extracted first and looked up
// separately, and a token missing from either block is an error rather than a
// silent fall-through to the other theme's value.
function block(label, startRe) {
  const start = styles.search(startRe)
  assert.ok(start >= 0, `could not find the ${label} block in style.css`)
  const end = styles.indexOf('\n}', start)
  assert.ok(end > start, `${label} block is not closed`)
  return styles.slice(start, end)
}

const THEMES = {
  night: block('@theme (night)', /^@theme\s*\{/m),
  day: block("day", /^:root\[data-theme='day'\]\s*\{/m),
}

// Tokens the day block deliberately does not redeclare, because they are already
// correct on both canvases. Looking these up in `day` falls back to night on
// purpose; every other token must be present in the theme being asked for.
const SHARED = new Set([
  // White on #553F83 is 8.66:1 whatever is behind the button, and the danger
  // fill is a fill fact rather than a page fact.
  'primary', 'primary-foreground', 'danger-solid',
  // The four status bases are generators for tints and borders, never painted
  // as text themselves.
  'danger', 'success', 'warning', 'info',
  // Text on a solid status fill. Those fills are light in both themes, so the
  // label is near-black in both -- the mirror of primary-foreground. Asserted
  // against every status base below rather than taken on trust.
  'status-foreground',
  // The eight rgba() tint and ring values. These are the pure status hue at 15%
  // and 30%, so they composite correctly over either canvas: measured over the
  // day page they carry the day -fg values at 4.94 to 5.86:1. Verified, not
  // assumed, and listed here so the parity check below states the intent.
  'success-tint', 'success-ring',
  'warning-tint', 'warning-ring',
  'danger-tint', 'danger-ring',
  'info-tint', 'info-ring',
])

function token(theme, name) {
  const scope = THEMES[theme]
  const re = new RegExp(`--color-${name}:\\s*(#[0-9A-Fa-f]{6})\\b`)
  const hit = scope.match(re)
  if (hit) return hit[1]
  assert.ok(
    SHARED.has(name),
    `--color-${name} is missing from the ${theme} theme and is not on the shared list. ` +
    `Every token needs a value in both blocks unless it is deliberately shared.`,
  )
  const fallback = THEMES.night.match(re)
  assert.ok(fallback, `missing hex token --color-${name} in either theme`)
  return fallback[1]
}

function luminance(hex) {
  const channels = hex.slice(1).match(/../g).map((part) => parseInt(part, 16) / 255)
  const linear = channels.map((value) =>
    value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4)
  return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2]
}

function contrast(a, b) {
  const [lighter, darker] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (lighter + 0.05) / (darker + 0.05)
}

const AA = 4.5          // WCAG 1.4.3, normal-size text
const NON_TEXT = 3      // WCAG 1.4.11, UI components and graphical objects

function atLeast(ratio, floor, what) {
  assert.ok(
    ratio >= floor,
    `${what}: ${ratio.toFixed(2)}:1 is below the ${floor}:1 floor`,
  )
}

for (const theme of Object.keys(THEMES)) {
  test(`${theme}: reading text clears WCAG AA on every surface`, () => {
    const surfaces = ['background', 'surface', 'surface-hover', 'secondary']
    for (const fg of ['foreground', 'foreground-strong', 'foreground-muted']) {
      for (const bg of surfaces) {
        atLeast(contrast(token(theme, fg), token(theme, bg)), AA, `${theme} ${fg} on ${bg}`)
      }
    }
  })

  test(`${theme}: action labels clear WCAG AA on their fills`, () => {
    atLeast(contrast(token(theme, 'primary-foreground'), token(theme, 'primary')), AA,
      `${theme} primary label`)
    atLeast(contrast(token(theme, 'primary-foreground'), token(theme, 'primary-hover')), AA,
      `${theme} primary hover label`)
    // The danger fill is darker than --color-danger precisely so white clears AA.
    atLeast(contrast('#FFFFFF', token(theme, 'danger-solid')), AA, `${theme} danger label`)
    atLeast(contrast(token(theme, 'secondary-foreground'), token(theme, 'secondary')), AA,
      `${theme} secondary label`)
  })

  test(`${theme}: links and status foregrounds clear WCAG AA`, () => {
    for (const bg of ['background', 'surface']) {
      atLeast(contrast(token(theme, 'link'), token(theme, bg)), AA, `${theme} link on ${bg}`)
      for (const role of ['success', 'warning', 'danger', 'info']) {
        atLeast(contrast(token(theme, `${role}-fg`), token(theme, bg)), AA,
          `${theme} ${role}-fg on ${bg}`)
      }
    }
  })

  test(`${theme}: a label on a solid status fill clears WCAG AA`, () => {
    // The deployments "Live" chip is a solid --color-success fill. It used
    // text-background, which is the canvas colour and therefore flipped to
    // near-white by day: 2.13:1, and invisible to the browser suite because
    // that suite could not read a color-mix() result at the time.
    for (const role of ['success', 'warning', 'danger', 'info']) {
      atLeast(contrast(token(theme, 'status-foreground'), token(theme, role)), AA,
        `${theme} status label on ${role}`)
    }
  })

  test(`${theme}: the focus ring clears the non-text floor`, () => {
    // A ring is a graphical object, not text. It was a literal `ring-white`,
    // which is invisible on a light field; this is the assertion that would
    // have caught that.
    for (const bg of ['background', 'surface', 'surface-hover']) {
      atLeast(contrast(token(theme, 'focus-ring'), token(theme, bg)), NON_TEXT,
        `${theme} focus ring on ${bg}`)
    }
  })

  test(`${theme}: runtime marks clear the non-text floor`, () => {
    for (const mark of ['runtime-node', 'runtime-python']) {
      for (const bg of ['background', 'surface', 'surface-hover']) {
        atLeast(contrast(token(theme, mark), token(theme, bg)), NON_TEXT,
          `${theme} ${mark} on ${bg}`)
      }
    }
  })
}

test('the two themes declare the same token names', () => {
  const names = (scope) =>
    new Set([...scope.matchAll(/--color-([a-z0-9-]+):/g)].map((m) => m[1]))
  const night = names(THEMES.night)
  const day = names(THEMES.day)
  const missing = [...night].filter((n) => !day.has(n) && !SHARED.has(n))
  assert.deepEqual(missing, [],
    `these tokens exist in night but not day, and are not on the shared list: ${missing.join(', ')}`)
  const extra = [...day].filter((n) => !night.has(n))
  assert.deepEqual(extra, [], `day declares tokens night does not: ${extra.join(', ')}`)
})
