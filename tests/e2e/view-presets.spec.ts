import {test,expect,type Page,type Browser} from '@playwright/test'
import {mkdirSync,mkdtempSync,writeFileSync} from 'node:fs'
import {join} from 'node:path'
import type {ChildProcess} from 'node:child_process'
import {env,kubeconfig,writeAtomic,scratchRoot,selectConnection,connectSelected,openAppMenu} from './fixtures'
import {start,stop,freePort} from './restartApp'
import {labelServer} from './label-fixture'
import {languages,languageNames} from '../../web/src/languages'
const grid=(page:Page)=>page.getByRole('grid',{name:'resources'})
// Metrics API collections also end in /pods; they are not core inventory.
const corePods=(path:string)=>/^\/api\/v1\/(?:namespaces\/[^/]+\/)?pods$/.test(path)
async function launch(browser:Browser,prepare?:(page:Page)=>Promise<void>){
 mkdirSync(scratchRoot,{recursive:true});const root=mkdtempSync(join(scratchRoot,'presets-')),e=env(root),fixture=await labelServer()
 writeAtomic(e.one,kubeconfig('labels-a','labels-a','labels-b').replace(/https:\/\/[^"\s]+/g,`http://127.0.0.1:${fixture.port}`).replace(/ns-labels-[ab]/g,'blue'))
 const port=await freePort(),url=`http://127.0.0.1:${port}`,page=await browser.newPage({viewport:{width:1440,height:1000}});let app:ChildProcess|undefined
 await prepare?.(page)
 app=await start(port,e);await page.goto(url)
 const token=await page.locator('meta[name=spk-ocular-api-token]').getAttribute('content')
 const imported=await page.request.post(url+'/api/Configurations',{headers:{Authorization:`Bearer ${token}`,Origin:url},data:{command:'import',paths:[e.one]}});expect(imported.ok()).toBe(true)
 await page.reload();await selectConnection(page,/^labels-a\b/);await connectSelected(page)
 await expect(grid(page).getByRole('gridcell',{name:'pod-0000',exact:true})).toBeVisible()
 // First paint may precede inventory readiness. Measure reuse only after the
 // complete warm snapshot; unfinished last-lease cancellation is intentional.
 await expect(page.locator('[data-area=table]')).toHaveAttribute('aria-busy','false')
 return {page,fixture,url,e,app,close:async()=>{await page.close();if(app)await stop(app);await fixture.close()}}
}
async function saveView(page:Page){
 await page.getByRole('button',{name:'Label columns',exact:true}).click()
 const dialog=page.getByRole('dialog',{name:'Label columns',exact:true})
 await dialog.getByLabel('View name',{exact:true}).fill('Team')
 await dialog.getByLabel('Label keys · one per line',{exact:true}).fill('app.kubernetes.io/version\nx:y/z.a\nmissing\nempty\nlarge')
 await expect(dialog).toContainText('literal-0');await expect(dialog).toContainText('Value limit')
 await dialog.getByRole('button',{name:'Save and apply',exact:true}).click();await expect(dialog).toHaveCount(0)
 await expect(grid(page).getByRole('columnheader',{name:/^app\.kubernetes\.io\/version/})).toBeVisible()
}
test('exact label projections share inventory, sort/filter all rows and survive target switch and real restart',async({browser})=>{
 const f=await launch(browser),{page,fixture}=f;const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message))
 const phases:unknown[]=[]
 const note=async(phase:string)=>phases.push({phase,requests:structuredClone(fixture.requests),busy:await page.locator('[data-area=table]').getAttribute('aria-busy'),namespace:await page.getByRole('button',{name:'Namespace',exact:true}).textContent()})
 const views:unknown[]=[];page.on('request',r=>{if(r.url().endsWith('/api/OpenView'))views.push({at:Date.now(),query:r.postDataJSON().query})})
 try{
  await note('before')
  const before=fixture.requests.filter(r=>corePods(r.path)&&!r.watch).length
  await saveView(page)
  await note('after-save')
  expect(fixture.requests.filter(r=>corePods(r.path)&&!r.watch).length,JSON.stringify({phases,views})).toBe(before)
  await expect(grid(page).getByText('Missing',{exact:true}).first()).toBeVisible()
  expect(fixture.requests.filter(r=>/^\/api\/v1\/(?:namespaces\/[^/]+\/)?pods\//.test(r.path))).toEqual([])
  expect(fixture.requests.filter(r=>corePods(r.path)&&r.watch).length).toBeLessThanOrEqual(1)
  expect(fixture.requests.every(r=>r.method==='GET')).toBe(true)
  await page.locator('[data-primary-filter]').fill('needle');await expect(grid(page).getByRole('gridcell',{name:'pod-1999',exact:true})).toBeVisible()
  await page.locator('[data-primary-filter]').fill('');await grid(page).getByRole('columnheader',{name:/^app\.kubernetes\.io\/version/}).click()
  await expect(grid(page).getByRole('gridcell',{name:'pod-1999',exact:true})).toBeVisible();expect(await grid(page).getByRole('row').count()).toBeLessThan(100)
  await page.getByRole('button',{name:'Label columns',exact:true}).click();const dialog=page.getByRole('dialog',{name:'Label columns',exact:true})
  await dialog.getByLabel('Age',{exact:true}).uncheck();await dialog.getByRole('button',{name:'Move app.kubernetes.io/version up',exact:true}).click();await dialog.getByRole('button',{name:'Save and apply',exact:true}).click()
  await expect(grid(page).getByRole('columnheader',{name:/^Age$/})).toHaveCount(0)
  await selectConnection(page,/^labels-b\b/);await connectSelected(page);await expect(grid(page).getByRole('columnheader',{name:/^app\.kubernetes\.io\/version/})).toBeVisible()
  await stop(f.app);f.app=await start(Number(new URL(f.url).port),f.e);await page.goto(f.url);await connectSelected(page)
  await expect(grid(page).getByRole('columnheader',{name:/^app\.kubernetes\.io\/version/})).toBeVisible();await expect(grid(page).getByRole('columnheader',{name:/^Age$/})).toHaveCount(0)
  await page.screenshot({path:join(scratchRoot,'view-presets-restored.png')});expect(errors).toEqual([])
 }finally{writeFileSync(test.info().outputPath('wire-proof.json'),JSON.stringify({phases,views,requests:fixture.requests},null,2));await page.close();await stop(f.app);await fixture.close()}
})
test('a transient row-read failure retries with browser timers and reaches the complete inventory',async({browser})=>{
 const errors:string[]=[];let failed=0,recovered=0
 const f=await launch(browser,async page=>{
  page.on('pageerror',error=>errors.push(error.stack??error.message))
  await page.route('**/api/GetRows',async route=>{
   if(!failed){failed++;await route.fulfill({status:503,json:{code:'internal',detail:'fixture transient read failure'}})}
   else{recovered++;await route.continue()}
  })
 })
 try{
  expect(failed).toBe(1);expect(recovered).toBeGreaterThan(0)
  await expect(grid(f.page).getByRole('gridcell',{name:'pod-0000',exact:true})).toBeVisible()
  await expect(f.page.locator('[data-area=table]')).toHaveAttribute('aria-busy','false')
  expect(errors).toEqual([])
 }finally{await f.close()}
})
test('label configuration fits all eight locales at wide and narrow widths with literal preview',async({browser})=>{
 const f=await launch(browser),{page}=f
 try{
  await saveView(page)
  for(const language of languages){
   await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[language],exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang',language)
   await page.locator('[data-view-presets]').click()
   const dialog=page.getByRole('dialog');await expect(dialog).toContainText('x:y/z.a');await expect(dialog.getByRole('status')).toHaveCount(0)
   for(const width of [1440,375]){await page.setViewportSize({width,height:1000});expect(await dialog.evaluate(e=>e.scrollWidth<=e.clientWidth)).toBe(true);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:join(scratchRoot,`view-presets-${language}-${width}.png`)})}
   await dialog.press('Escape');await page.setViewportSize({width:1440,height:1000})
  }
 }finally{await f.close()}
})

test('applying a layout keeps namespace, open UID properties and a held YAML draft',async({browser})=>{
 const f=await launch(browser),{page,fixture}=f
 try{
  await saveView(page)
  await grid(page).getByRole('gridcell',{name:'pod-0000',exact:true}).click()
  const drawer=page.locator('[data-resource-drawer]')
  await drawer.getByRole('tab',{name:'YAML',exact:true}).click();await drawer.getByRole('button',{name:'Edit',exact:true}).click()
  const editor=drawer.locator('.cm-content');await editor.click();await page.keyboard.press('Control+End');await page.keyboard.type('\n# retained preset draft')
  await page.getByRole('button',{name:'Label columns',exact:true}).click();const dialog=page.getByRole('dialog',{name:'Label columns',exact:true})
  await dialog.getByLabel('Label keys · one per line',{exact:true}).fill('team/name')
  await dialog.getByRole('button',{name:'Save and apply',exact:true}).click();await expect(dialog).toHaveCount(0)
  await expect(editor).toContainText('retained preset draft');await expect(drawer.getByRole('heading',{name:'pod-0000',exact:true})).toBeVisible()
  await expect(page.getByRole('button',{name:'Namespace',exact:true})).toContainText('blue')
  await expect(grid(page).getByRole('columnheader',{name:'team/name',exact:true})).toBeVisible()
  expect(fixture.requests.every(r=>r.method==='GET')).toBe(true)
  await page.screenshot({path:join(scratchRoot,'view-presets-held-draft.png')})
 }finally{await f.close()}
})
