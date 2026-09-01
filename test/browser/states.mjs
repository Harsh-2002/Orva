import { chromium } from 'playwright-core'
import { resolveBrowser, session } from './lib/env.mjs'
const BASE='http://127.0.0.1:18000'; const OUT='/SSD/dev/.tmp/wb3'
process.argv.push('--username','orva','--password','orva-dev-2026')
const auth = await session(BASE)
const browser = await chromium.launch({ executablePath: resolveBrowser(), args:['--no-sandbox'] })
const ctx = await browser.newContext({ viewport:{width:1440,height:900}, colorScheme:'dark' })
await ctx.addCookies([{name:'session_token',value:auth.cookie,domain:'127.0.0.1',path:'/'}])
const page = await ctx.newPage()
const errs=[]; page.on('pageerror',e=>errs.push(String(e)))
// A stream that succeeds, so the post-build acknowledgement is reachable.
await page.route('**/deployments/*/stream', route => route.fulfill({
  status:200, headers:{'content-type':'text/event-stream'},
  body:'event: log\ndata: {"stream":"build","line":"compiling handler"}\n\n'
     + 'event: succeeded\ndata: {"version":9,"duration_ms":1240}\n\n' }))
await page.goto(`${BASE}/web/functions/silent-tower`,{waitUntil:'networkidle'})
await page.waitForTimeout(800)
await page.getByRole('button',{name:'Deploy'}).click()
await page.waitForTimeout(900)
await page.screenshot({path:`${OUT}/deployed.png`, clip:{x:200,y:0,width:1240,height:180}})
console.log('button now:', await page.locator('button').filter({hasText:/Deploy|Building|Deployed/}).last().textContent())
console.log('body has banner copy:', (await page.content()).includes('The full build log is on the'))
await page.waitForTimeout(2200)
await page.screenshot({path:`${OUT}/settled.png`, clip:{x:200,y:0,width:1240,height:180}})
console.log('after 3s:', (await page.locator('button').filter({hasText:/Deploy|Building|Deployed/}).last().textContent()).trim())
// Now a run, to see the strip without its status chip.
await page.locator('button.run-btn').click()
await page.waitForTimeout(6000)
await page.screenshot({path:`${OUT}/strip.png`, clip:{x:200,y:820,width:1240,height:80}})
console.log('errors:', errs.length, errs.slice(0,2))
await browser.close()
