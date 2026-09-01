import { chromium } from 'playwright-core'
import { resolveBrowser, session } from './lib/env.mjs'
const BASE='http://127.0.0.1:18000'
process.argv.push('--username','orva','--password','orva-dev-2026')
const auth = await session(BASE)
const browser = await chromium.launch({ executablePath: resolveBrowser(), args:['--no-sandbox'] })
const ctx = await browser.newContext({ viewport:{width:1440,height:1000}, colorScheme:'dark' })
await ctx.addCookies([{name:'session_token',value:auth.cookie,domain:'127.0.0.1',path:'/'}])
const page = await ctx.newPage()

const PROBE = () => {
  const out = []
  for (const el of document.querySelectorAll('button, a[class*="rounded"], [role="button"]')) {
    const r = el.getBoundingClientRect()
    if (r.width < 4 || r.height < 4) continue
    const cs = getComputedStyle(el)
    const label = (el.innerText || el.getAttribute('aria-label') || '').replace(/\s+/g,' ').trim().slice(0,26)
    if (!label) continue
    const padL = parseFloat(cs.paddingLeft), padR = parseFloat(cs.paddingRight)
    const fs = parseFloat(cs.fontSize)
    const rad = parseFloat(cs.borderTopLeftRadius)
    out.push({
      label,
      h: +r.height.toFixed(1),
      padX: +((padL+padR)/2).toFixed(1),
      fs: +fs.toFixed(1),
      rad: +rad.toFixed(1),
      // The proportions the eye actually reads.
      hOverFs: +(r.height/fs).toFixed(2),
      padOverH: +(((padL+padR)/2)/r.height).toFixed(2),
      radOverH: +(rad/r.height).toFixed(2),
      weight: cs.fontWeight,
    })
  }
  return out
}

const PAGES = [
  ['workbench',  '/functions/silent-tower/test'],
  ['editor',     '/functions/silent-tower'],
  ['functions',  '/functions'],
  ['settings',   '/settings'],
  ['invocations','/invocations'],
  ['api-keys',   '/api-keys'],
]
const all = {}
for (const [name, path] of PAGES) {
  await page.goto(`${BASE}/web${path}`,{waitUntil:'networkidle'})
  await page.waitForTimeout(900)
  all[name] = await page.evaluate(PROBE)
}
console.log(JSON.stringify(all, null, 0))
await browser.close()
