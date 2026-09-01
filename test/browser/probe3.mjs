import { chromium } from 'playwright-core'
import { resolveBrowser, session } from './lib/env.mjs'
const BASE='http://127.0.0.1:18000'
process.argv.push('--username','orva','--password','orva-dev-2026')
const auth = await session(BASE)
const browser = await chromium.launch({ executablePath: resolveBrowser(), args:['--no-sandbox'] })
const ctx = await browser.newContext({ viewport:{width:1440,height:900}, colorScheme:'dark' })
await ctx.addCookies([{name:'session_token',value:auth.cookie,domain:'127.0.0.1',path:'/'}])
const page = await ctx.newPage()
await page.addInitScript(() => {
  window.addEventListener('unhandledrejection', e =>
    console.warn('UNHANDLED: ' + String(e.reason?.stack || e.reason).slice(0, 400)))
})
page.on('console', m => console.log('CONSOLE', m.type(), m.text().slice(0,400)))
page.on('response', async r => {
  if (r.url().includes('/fn/')) console.log('  <-', r.status(), (await r.text().catch(()=>'')).slice(0,200))
})
await page.goto(`${BASE}/web/functions/silent-tower`,{waitUntil:'networkidle'})
await page.waitForTimeout(1000)
console.log('--- clicking ---')
await page.locator('button.run-btn').click()
await page.waitForTimeout(40000)
console.log('STRIP:', (await page.locator('[role="status"]').first().textContent()).replace(/\s+/g,' ').trim().slice(0,200))
await page.screenshot({path:'/SSD/dev/.tmp/wb3/strip-settled.png', clip:{x:200,y:820,width:1240,height:80}})
await browser.close()
