// Orva dashboard UI test suite.
//
// Renders the shipped dashboard in a real browser and asserts what the source
// cannot prove. Everything else under test/ checks behaviour through HTTP or
// the CLI and never opens a page; this is the only layer that sees geometry,
// the accessibility tree, or a runtime error in a view.
//
//   node run.mjs --url http://127.0.0.1:8443 --api-key "$ADMIN_KEY"
//   node run.mjs --url ... --api-key ... --destructive     # include delete flows
//   node run.mjs --url ... --suite responsive,consistency
//   node run.mjs --url ... --theme day                     # one theme, not both
//
// Exit codes:  0 all checks passed   1 at least one failed   2 could not start
//
// See CLAUDE.md in this directory for what each suite covers and why.

import { chromium } from 'playwright-core'
import { mkdirSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { Report } from './lib/report.mjs'
import {
  arg, resolveBrowser, session, discoverRoutes,
  CORE_VIEWPORTS, EXTRA_VIEWPORTS,
} from './lib/env.mjs'

import * as smoke from './suites/smoke.mjs'
import * as responsive from './suites/responsive.mjs'
import * as touchTargets from './suites/touch-targets.mjs'
import * as accessibility from './suites/accessibility.mjs'
import * as controlScale from './suites/control-scale.mjs'
import * as edgeGuard from './suites/edge-guard.mjs'
import * as consistency from './suites/consistency.mjs'
import * as journeys from './suites/journeys.mjs'

const PAGE_SUITES = [smoke, responsive, touchTargets, controlScale, consistency, edgeGuard, accessibility]
const FLOW_SUITES = [journeys]

const BASE = arg('url', process.env.ORVA_URL || 'http://127.0.0.1:8443').replace(/\/$/, '')
const API_KEY = arg('api-key', process.env.ORVA_API_KEY || '')
const ONLY = (arg('suite', process.env.ORVA_SUITE || '') || '').split(',').filter(Boolean)
const DESTRUCTIVE = process.argv.includes('--destructive') || process.env.ORVA_DESTRUCTIVE === '1'
const FULL = process.argv.includes('--full') || process.env.ORVA_FULL_MATRIX === '1'
const SHOT_DIR = arg('shots', process.env.ORVA_SHOT_DIR || '')
// Both themes by default. A single-theme run is a partial answer now that the
// theme is the operator's choice: the day palette is a different set of colour
// values against the same markup, so a night-only pass proves nothing about it.
const THEME = arg('theme', process.env.ORVA_THEME || 'both')
const JSON_OUT = arg('json', process.env.ORVA_JSON_OUT || '')

const wanted = (m) => !ONLY.length || ONLY.includes(m.meta.id)
const pageSuites = PAGE_SUITES.filter(wanted)
const flowSuites = FLOW_SUITES.filter(wanted)
const VIEWPORTS = FULL ? [...CORE_VIEWPORTS, ...EXTRA_VIEWPORTS] : CORE_VIEWPORTS
const THEMES = THEME === 'both' ? ['night', 'day'] : [THEME]

if (THEMES.some((t) => t !== 'night' && t !== 'day')) {
  console.error(`\ncannot start: --theme must be night, day or both (got "${THEME}")`)
  process.exit(2)
}

// Every check reports where it happened, and with two themes the theme is part
// of "where". Prefixing here keeps it out of every suite: a suite states the
// rule, the runner states the conditions.
const themed = (r, theme) => ({
  pass: (s, w, n) => r.pass(s, `${theme} ${w}`, n),
  fail: (s, w, n, d) => r.fail(s, `${theme} ${w}`, n, d),
  skip: (s, w, n, d) => r.skip(s, `${theme} ${w}`, n, d),
  record: (s, w, n, f) => r.record(s, `${theme} ${w}`, n, f),
})

function die(msg, code = 2) {
  console.error(`\ncannot start: ${msg}`)
  process.exit(code)
}

const exe = resolveBrowser()
if (!exe) {
  die('no Chromium or Chrome found.\n' +
      '  Set ORVA_BROWSER=/path/to/chrome, or install one:\n' +
      '    npx playwright install chromium        (downloads its own)\n' +
      '    sudo apt-get install -y chromium       (uses the system one)')
}

let auth
try {
  auth = await session(BASE, API_KEY)
} catch (e) {
  die(String(e.message || e))
}

const routes = await discoverRoutes(BASE, API_KEY)
if (SHOT_DIR) mkdirSync(SHOT_DIR, { recursive: true })

console.log('Orva dashboard UI suite')
console.log(`  target    ${BASE}`)
console.log(`  browser   ${exe}`)
console.log(`  user      ${auth.username}`)
console.log(`  suites    ${[...pageSuites, ...flowSuites].map((s) => s.meta.id).join(', ')}`)
console.log(`  matrix    ${routes.length} routes x ${VIEWPORTS.length} viewports` +
            ` x ${THEMES.join('/')}${DESTRUCTIVE ? ' (+ destructive flows)' : ''}`)

const report = new Report()
const browser = await chromium.launch({
  executablePath: exe,
  args: ['--no-sandbox', '--disable-dev-shm-usage'],
})

try {
 for (const theme of THEMES) {
  const themeReport = themed(report, theme)
  for (const viewport of VIEWPORTS) {
    const context = await browser.newContext({
      viewport: { width: viewport.w, height: viewport.h },
      hasTouch: viewport.touch,
      isMobile: viewport.touch,
      deviceScaleFactor: 1,
      // Set the OS preference to match, so a page that follows the system and
      // one that was told explicitly both land on the theme under test. Without
      // it a headless Chromium reports light and every "night" run is a lie.
      colorScheme: theme === 'day' ? 'light' : 'dark',
    })
    // The dashboard resolves the theme before first paint from this key, so it
    // has to exist before the document runs, not after the page settles.
    await context.addInitScript((t) => {
      try { window.localStorage.setItem('orva:theme', t) } catch { /* private mode */ }
    }, theme)
    await context.addCookies([{
      name: 'session_token',
      value: auth.cookie,
      domain: new URL(BASE).hostname,
      path: '/',
    }])

    const page = await context.newPage()
    const errors = []
    page.on('pageerror', (e) => errors.push(`uncaught: ${String(e)}`))
    page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()) })

    for (const route of routes) {
      errors.length = 0
      const where = `${viewport.name} ${route.name}`
      try {
        await page.goto(`${BASE}/web${route.path}`, { waitUntil: 'networkidle', timeout: 30000 })
        await page.waitForTimeout(600)
      } catch (e) {
        themeReport.fail('smoke', where, 'route loads', String(e).slice(0, 160))
        continue
      }

      // The not-found route is expected to 404 its data calls; only its render
      // is interesting, so let the smoke suite see it and skip the rest.
      const forgiving = route.name === 'not-found'

      for (const suite of pageSuites) {
        if (forgiving && suite.meta.id !== 'smoke') continue
        try {
          await suite.onPage({
            page, route, viewport, theme,
            errors: forgiving ? [] : errors,
            report: themeReport,
          })
        } catch (e) {
          themeReport.fail(suite.meta.id, where, 'suite ran', `threw: ${String(e).slice(0, 160)}`)
        }
      }

      if (SHOT_DIR) {
        await page.screenshot({ path: join(SHOT_DIR, `${theme}__${viewport.name}__${route.name}.png`) })
      }
    }

    // A relational check needs every route in hand before it can speak.
    for (const suite of pageSuites) {
      if (!suite.afterViewport) continue
      try {
        await suite.afterViewport({ viewport, theme, report: themeReport })
      } catch (e) {
        themeReport.fail(suite.meta.id, viewport.name, 'suite ran', `threw: ${String(e).slice(0, 160)}`)
      }
    }

    // Flows run once, on the widest viewport, since they drive interactions
    // rather than measure layout.
    if (viewport.name === 'laptop') {
      for (const suite of flowSuites) {
        try {
          await suite.run({
            context, base: BASE, report: themeReport, theme,
            destructive: DESTRUCTIVE, apiKey: API_KEY,
          })
        } catch (e) {
          // Keep enough of the message to see WHY. Playwright puts the
          // actionable reason -- "<div ...> intercepts pointer events" -- near
          // the end of its call log, so truncating to a couple of hundred
          // characters throws away the only line that identifies the defect and
          // leaves a bare "Timeout exceeded" to guess at.
          themeReport.fail(suite.meta.id, 'laptop', 'suite ran', `threw: ${String(e).slice(0, 1200)}`)
        }
      }
    }

    await context.close()
  }
 }
} finally {
  await browser.close()
}

report.print()
if (JSON_OUT) {
  writeFileSync(JSON_OUT, report.toJSON())
  console.log(`\nwrote ${JSON_OUT}`)
}
process.exit(report.failed ? 1 : 0)
