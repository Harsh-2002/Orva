async page => {
  const errors = []
  const responses = []
  page.on('pageerror', e => errors.push(e.message))
  page.on('response', async r => {
    if (r.url().endsWith('/permissions')) responses.push({method:r.request().method(),status:r.status(),body:await r.json()})
  })
  const metadata = page.waitForResponse(r => r.url().endsWith('/api/scratch/functions'))
  await page.goto('http://127.0.0.1:4300/live-iam.html')
  const functions = await (await metadata).json()
  await page.getByRole('heading',{name:'Permissions',exact:true}).waitFor()
  const fn = runtime => functions.find(f => f.runtime === runtime)
  const label = runtime => runtime === 'node' ? 'Node.js' : 'Python'
  const theme = async (name,width) => {
    await page.setViewportSize({width,height:1050})
    await page.emulateMedia({colorScheme:name === 'day'?'light':'dark'})
    await page.evaluate(t => {document.documentElement.dataset.theme=t},name)
  }
  const owner = async runtime => {
    const control = page.getByRole('radio',{name:label(runtime),exact:true})
    if (!(await control.getAttribute('aria-checked') === 'true')) {
      const reply=page.waitForResponse(r => r.url().endsWith(`/functions/${fn(runtime).id}/permissions`)&&r.request().method()==='GET')
      await control.click()
      const snapshot=await (await reply).json()
      if(snapshot.targets.length!==2 || snapshot.targets.some(t=>t.state!=='resolved'||!functions.some(f=>f.id===t.id&&f.name===t.label))) throw Error('Picker not derived from actual visible active metadata')
    }
    await page.getByText(`What ${fn(runtime).name}`,{exact:false}).waitFor()
  }
  const edit = async runtime => {
    await owner(runtime)
    await page.getByRole('button',{name:/^(Create policy|Edit permissions)$/}).click()
  }
  const save = async () => {
    await page.getByRole('button',{name:'Review changes',exact:true}).click()
    const reply=page.waitForResponse(r=>r.url().endsWith('/permissions')&&r.request().method()==='PUT')
    await page.getByRole('button',{name:'Save permissions',exact:true}).click()
    const r=await reply,s=await r.json()
    if(r.status()!==200 || typeof s.host.revision!=='string'|| !s.history.some(a=>a.revision===s.host.revision&&a.actor.kind==='api_key')) throw Error('Save missing real audit revision')
    await page.getByRole('button',{name:/^(Create policy|Edit permissions)$/}).waitFor()
    return s
  }
  const outbound = async (caller,callee,slug) => {
    await edit(caller)
    const add=page.getByRole('button',{name:'Add permission',exact:true})
    if(await add.isVisible()) await add.click()
    await page.getByRole('radio',{name:/^Functions /}).check()
    await page.getByRole('button',{name:'Next',exact:true}).click()
    await page.getByRole('checkbox',{name:/^Call functions.invoke/}).check()
    await page.getByRole('button',{name:'Next',exact:true}).click()
    await page.locator('#wizard-function').click()
    await page.getByRole('menuitemradio',{name:new RegExp(`^${fn(callee).name} ·`)}).click()
    await page.getByRole('menuitemradio').first().waitFor({state:'hidden'})
    await page.getByRole('button',{name:'Next',exact:true}).click()
    await page.screenshot({path:`/evidence/${slug}-outbound-review-night.png`,fullPage:true,animations:'disabled'})
    await page.getByRole('button',{name:'Add to policy',exact:true}).click()
    return save()
  }
  const inbound = async (callee,caller,slug) => {
    await edit(callee)
    await page.getByRole('radio',{name:'Who can call it',exact:true}).click()
    await page.getByRole('button',{name:'Add caller',exact:true}).click()
    await page.locator('#wizard-caller').click()
    await page.getByRole('menuitemradio',{name:new RegExp(`^${fn(caller).name} ·`)}).click()
    await page.getByRole('menuitemradio').first().waitFor({state:'hidden'})
    await page.getByRole('button',{name:'Next',exact:true}).click()
    await page.getByRole('checkbox',{name:/^Function call f2f/}).check()
    await page.getByRole('button',{name:'Next',exact:true}).click()
    await page.screenshot({path:`/evidence/${slug}-inbound-review-night.png`,fullPage:true,animations:'disabled'})
    await page.getByRole('button',{name:'Add to policy',exact:true}).click()
    return save()
  }
  const remove = async (runtime,part) => {
    await edit(runtime)
    await page.getByRole('radio',{name:part==='inbound'?'Who can call it':'What it can do',exact:true}).click()
    await page.getByRole('button',{name:/^Remove /}).first().click()
    return save()
  }
  const run = async (caller,callee,decision) => {
    await owner(caller)
    await page.getByRole('radio',{name:'Buffered call',exact:true}).click()
    await page.locator('#scratch-call-target').click()
    await page.getByRole('menuitemradio',{name:fn(callee).name,exact:true}).click()
    await page.getByRole('menuitemradio').first().waitFor({state:'hidden'})
    const reply=page.waitForResponse(r=>r.url().includes(`/fn/${fn(caller).id}/`)&&r.request().method()==='POST')
    await page.getByRole('button',{name:'Invoke function call',exact:true}).click()
    const r=await reply
    const result={response:await r.json(),execution:r.headers()['x-orva-execution-id'],worker:r.headers()['x-scratch-worker'],artifact:r.headers()['x-scratch-artifact']}
    if(r.status()!==200 || result.response.decision!==decision || !result.execution || !result.worker || !result.artifact) throw Error(`Actual public call expected ${decision}`)
    if(decision==='ALLOWED') {
      const child=result.response.callee.context
      if(child.functionID!==fn(callee).id || child.depth!=='1' || child.trigger!=='f2f' || !child.credentialAbsent || child.trace!==result.response.context.trace) throw Error('Actual callee host context not preserved')
      await page.getByTestId('callee-result').waitFor()
    } else if(JSON.stringify(result.response).includes('not-visible-or-existing')) throw Error('Hidden target leaked')
    await page.getByTestId('invocation-result').filter({hasText:decision}).waitFor()
    return result
  }
  const journeys=[]
  for(const [caller,callee] of [['node','python'],['python','node']]) {
    const slug=`${caller}-to-${callee}`
    await theme('night',390)
    const outboundOnlySave=await outbound(caller,callee,slug)
    const outboundOnly=await run(caller,callee,'IAM_DENIED')
    const outboundRemovedSave=await remove(caller,'outbound')
    const inboundOnlySave=await inbound(callee,caller,slug)
    const inboundOnly=await run(caller,callee,'IAM_DENIED')
    const bothSave=await outbound(caller,callee,slug)
    const both=await run(caller,callee,'ALLOWED')
    await theme('day',1440)
    await page.screenshot({path:`/evidence/${slug}-allowed-day.png`,fullPage:true,animations:'disabled'})
    const inboundRemovedSave=await remove(callee,'inbound')
    const inboundRemoved=await run(caller,callee,'IAM_DENIED')
    await theme('night',390)
    const restoredInboundSave=await inbound(callee,caller,slug)
    const restored=await run(caller,callee,'ALLOWED')
    const callerRemovedSave=await remove(caller,'outbound')
    const callerRemoved=await run(caller,callee,'IAM_DENIED')
    await page.screenshot({path:`/evidence/${slug}-revoked-night.png`,fullPage:true,animations:'disabled'})
    await remove(callee,'inbound')
    if(new Set([outboundOnly,inboundOnly,both,inboundRemoved,restored,callerRemoved].map(r=>r.worker)).size!==1) throw Error('Caller worker unexpectedly changed')
    journeys.push({caller:fn(caller),callee:fn(callee),outboundOnlySave,outboundOnly,outboundRemovedSave,inboundOnlySave,inboundOnly,bothSave,both,inboundRemovedSave,inboundRemoved,restoredInboundSave,restored,callerRemovedSave,callerRemoved})
  }
  const overflow=await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth)
  if(errors.length||overflow) throw Error('Browser errors or overflow')
  if(!await page.getByRole('radio',{name:'Stream',exact:true}).isDisabled()) throw Error('Unqualified stream mode is enabled')
  return {journeys,responses,pageErrors:errors,overflow,streamEnabled:false}
}
