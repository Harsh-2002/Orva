import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

const styles = readFileSync(new URL('../src/style.css', import.meta.url), 'utf8')

function token(name) {
  const match = styles.match(new RegExp(`--color-${name}:\\s*(#[0-9A-Fa-f]{6})`))
  assert.ok(match, `missing hex token --color-${name}`)
  return match[1]
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

test('global graphite theme text pairs exceed WCAG AA contrast', () => {
  const background = token('background')
  const surface = token('surface')
  const foreground = token('foreground')
  const muted = token('foreground-muted')

  assert.ok(contrast(foreground, background) >= 4.5)
  assert.ok(contrast(foreground, surface) >= 4.5)
  assert.ok(contrast(muted, background) >= 4.5)
  assert.ok(contrast(muted, surface) >= 4.5)
})

test('primary action labels exceed WCAG AA contrast', () => {
  assert.ok(contrast(token('primary-foreground'), token('primary')) >= 4.5)
  assert.ok(contrast(token('primary-foreground'), token('primary-hover')) >= 4.5)
})
