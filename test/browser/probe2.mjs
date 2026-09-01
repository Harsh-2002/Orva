import { chromium } from 'playwright-core'
import { resolveBrowser, session } from './lib/env.mjs'
const BASE='http://127.0.0.1:18000'
process.argv.push('--username','orva','--password','orva-dev-2026')
const auth = await session(BASE)
const browser = await chromium.launch({ executablePath: resolveBrowser(), args:['--no-sandbox'] })
const ctx = await browser.newContext({ viewport:{width:1440,height:900}, colorScheme:'dark' })
await ctx.addCookies([{name:'session_token',value:auth.cookie,domain:'127.0.0.1',path:'/'}])
const page = await ctx.newPage()
await page.goto(`${BASE}/web/functions/silent-tower`,{waitUntil:'networkidle'})
await page.waitForTimeout(1000)
let recording = true
page.on('request', r => recording && console.log('  ->', r.method(), r.url().replace(BASE,'')))
console.log('--- clicking ---')
await page.locator('button.run-btn').click()
await page.waitForTimeout(5000)
recording = false
console.log('--- strip ---')
console.log('count role=status:', await page.locator('[role="status"]').count())
console.log('STRIP HTML:', (await page.locator('[role="status"]').first().innerHTML()).replace(/\s+/g,' ').slice(0,600))
console.log('--- now the workbench, same session ---')
await page.goto(`${BASE}/web/functions/silent-tower/test`,{waitUntil:'networkidle'})
await page.waitForTimeout(1200)
const t = (await page.locator('body').textContent()).replace(/\s+/g,' ')
console.log('workbench shows a run?', /503|Response|Never answered|logged nothing/.test(t))
console.log('workbench excerpt:', t.slice(t.indexOf('Response'), t.indexOf('Response')+160) || t.slice(0,200))
await browser.close()
