// Everything the suite needs from the outside world: the browser binary, a
// dashboard session, and the route set. Kept in one file so a failure to stand
// the harness up reads as a setup problem rather than a test failure.

import { existsSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { homedir } from 'node:os'

export const arg = (name, fallback) => {
  const i = process.argv.indexOf(`--${name}`)
  return i > -1 && process.argv[i + 1] ? process.argv[i + 1] : fallback
}

// The three viewports that decide something, and four that only add confidence.
// sm (640) and lg (1024) are the breakpoints that restructure the page, so
// 375 / 768 / 1280 lands one viewport in each band. Note 768 is a COARSE
// pointer: tablets are touch devices, and checking the touch floor only below
// 640 silently exempts every tablet.
export const CORE_VIEWPORTS = [
  { name: 'phone', w: 375, h: 812, touch: true },
  { name: 'tablet', w: 768, h: 1024, touch: true },
  { name: 'laptop', w: 1280, h: 800, touch: false },
]

export const EXTRA_VIEWPORTS = [
  { name: 'phone-mid', w: 390, h: 844, touch: true },
  { name: 'phone-lg', w: 430, h: 932, touch: true },
  { name: 'tablet-lg', w: 820, h: 1180, touch: true },
  { name: 'desktop', w: 1920, h: 1080, touch: false },
]

export function resolveBrowser() {
  if (process.env.ORVA_BROWSER) return process.env.ORVA_BROWSER

  // Playwright's own download, if anyone ran `playwright install chromium`.
  const cache = join(homedir(), '.cache', 'ms-playwright')
  if (existsSync(cache)) {
    const dirs = readdirSync(cache).filter((d) => d.startsWith('chromium')).sort().reverse()
    for (const dir of dirs) {
      for (const rel of ['chrome-linux64/chrome', 'chrome-linux/chrome', 'chrome-linux/headless_shell']) {
        const p = join(cache, dir, rel)
        if (existsSync(p)) return p
      }
    }
  }

  // Whatever the machine already has. GitHub's ubuntu x64 images ship Chrome;
  // their arm64 images do not, because Google publishes no linux/arm64 build.
  for (const p of [
    '/usr/bin/google-chrome', '/usr/bin/google-chrome-stable',
    '/usr/bin/chromium', '/usr/bin/chromium-browser', '/snap/bin/chromium',
    '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
  ]) if (existsSync(p)) return p

  return null
}

const cookieOf = (res) => (res.headers.getSetCookie?.() || [])
  .map((c) => /session_token=([^;]+)/.exec(c)?.[1])
  .find(Boolean)

// The router guard wants a session cookie, not an API key, and only login and
// onboard mint one.
//
// Onboarding is the CI path: the e2e job starts a fresh instance, so the first
// call claims it. That call needs an admin key, because an instance carrying
// operator-minted keys or deployed functions no longer onboards anonymously,
// and by the time this runs the API suite has deployed several.
//
// Login is tried first so a re-run against an instance this harness already
// claimed does not depend on onboarding being repeatable.
export async function session(base, apiKey) {
  const username = arg('username', process.env.ORVA_USER || 'browser-harness')
  const password = arg('password', process.env.ORVA_PASS || 'browser-harness-pw-1')
  const creds = { username, password }
  const json = { 'Content-Type': 'application/json' }

  const login = await fetch(`${base}/api/v1/auth/login`, {
    method: 'POST', headers: json, body: JSON.stringify(creds),
  })
  const viaLogin = login.ok && cookieOf(login)
  if (viaLogin) return { cookie: viaLogin, username, password }

  const headers = { ...json }
  if (apiKey) headers['X-Orva-API-Key'] = apiKey
  const onboard = await fetch(`${base}/api/v1/auth/onboard`, {
    method: 'POST', headers, body: JSON.stringify(creds),
  })
  const viaOnboard = onboard.ok && cookieOf(onboard)
  if (viaOnboard) return { cookie: viaOnboard, username, password }

  throw new Error(
    `could not obtain a dashboard session (login ${login.status}, onboard ${onboard.status}).\n` +
    '  A fresh instance needs --api-key, because onboarding stops being anonymous\n' +
    '  once functions exist. An instance someone already claimed needs\n' +
    '  --username and --password for that account.',
  )
}

// The routes the dashboard actually serves. Function-scoped ones are resolved
// against whatever is deployed so the suite does not depend on fixture names,
// and are simply absent on an empty instance rather than failing.
export async function discoverRoutes(base, apiKey) {
  const routes = [
    { name: 'overview', path: '/' },
    { name: 'functions', path: '/functions' },
    { name: 'cron', path: '/cron' },
    { name: 'jobs', path: '/jobs' },
    { name: 'activity', path: '/activity' },
    { name: 'invocations', path: '/invocations' },
    { name: 'traces', path: '/traces' },
    { name: 'api-keys', path: '/api-keys' },
    { name: 'channels', path: '/channels' },
    { name: 'webhooks', path: '/webhooks' },
    { name: 'firewall', path: '/firewall' },
    { name: 'settings', path: '/settings' },
    { name: 'docs', path: '/docs' },
    { name: 'ai', path: '/ai' },
    { name: 'not-found', path: '/this-route-does-not-exist' },
  ]
  if (!apiKey) return routes

  try {
    const res = await fetch(`${base}/api/v1/functions?limit=1`, {
      headers: { 'X-Orva-API-Key': apiKey },
    })
    const fn = (await res.json())?.functions?.[0]?.name
    if (fn) {
      const e = encodeURIComponent(fn)
      routes.push(
        { name: 'editor', path: `/functions/${e}`, fn },
        { name: 'deployments', path: `/functions/${e}/deployments`, fn },
        { name: 'kv', path: `/functions/${e}/kv`, fn },
        { name: 'inbound-webhooks', path: `/functions/${e}/inbound-webhooks`, fn },
      )
    }
  } catch {
    // Function-scoped routes are a bonus. An instance with none is a valid
    // target for every other suite.
  }
  return routes
}
