import { chromium } from 'playwright-core'
import { resolveBrowser, session } from './lib/env.mjs'
const BASE='http://127.0.0.1:18000'
process.argv.push('--username','orva','--password','orva-dev-2026')
const auth = await session(BASE)
const browser = await chromium.launch({ executablePath: resolveBrowser(), args:['--no-sandbox'] })
const ctx = await browser.newContext({ viewport:{width:1440,height:900}, colorScheme:'dark' })
await ctx.addCookies([{name:'session_token',value:auth.cookie,domain:'127.0.0.1',path:'/'}])
const page = await ctx.newPage()
page.on('pageerror',e=>console.log('PAGEERROR', String(e)))
page.on('console', m => console.log('CONSOLE', m.type(), m.text().slice(0,200)))
page.on('requestfailed', r => console.log('REQFAILED', r.url().slice(-50), r.failure()?.errorText))
await page.addInitScript(() => {
  window.addEventListener('unhandledrejection', e => console.error('UNHANDLED', String(e.reason?.stack || e.reason)))
})
page.on('response', r => { if (r.url().includes('/fn/') || r.url().includes('/logs')) console.log('  <-', r.status(), r.url().slice(-60)) })
await page.goto(`${BASE}/web/functions/silent-tower`,{waitUntil:'networkidle'})
await page.waitForTimeout(800)
const btn = page.locator('button.run-btn')
console.log('disabled:', await btn.isDisabled(), '| title:', await btn.getAttribute('title'))
console.log('clicking Run...')
const t=Date.now()
await page.locator('button.run-btn').click()
await page.waitForTimeout(6000)
console.log('elapsed', Date.now()-t)
console.log('runs in store:', await page.evaluate(() => {
  const el = document.querySelector('#app')?.__vue_app__
  return 'no-introspection'
}))
// Click again via a real DOM event, in case Playwright's synthetic click missed.
await page.evaluate(() => document.querySelector('button.run-btn')?.dispatchEvent(new MouseEvent('click',{bubbles:true})))
await page.waitForTimeout(4000)
const strip = await page.locator('[role="status"]').first().textContent()
console.log('strip:', JSON.stringify(strip.replace(/\s+/g,' ').trim()))
console.log('run btn:', JSON.stringify((await page.locator('button.run-btn').textContent()).trim()))
await browser.close()
