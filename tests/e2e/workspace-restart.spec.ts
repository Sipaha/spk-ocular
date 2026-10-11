import {expect,test,type Page} from '@playwright/test'
import type {ChildProcess} from 'node:child_process'
import {mkdirSync,mkdtempSync} from 'node:fs'
import {join} from 'node:path'
import {connectSelected,env,ONE,scratchRoot,writeAtomic} from './fixtures'
import {freePort,start,stop} from './restartApp'

const dock=(page:Page)=>page.locator('[data-area=dock]')
async function selectWorkloads(page:Page){
 await page.getByRole('option',{name:/^demo\b/}).click();await connectSelected(page)
 const nav=page.getByRole('navigation',{name:'resources'})
 await nav.getByRole('button',{name:/^Synthetic/}).click()
 await nav.getByRole('button',{name:'Services',exact:true}).click()
 await page.getByRole('grid',{name:'resources'}).getByRole('gridcell',{name:'api',exact:true}).click()
 return page.locator('[data-area=details]')
}
async function state(page:Page,url:string){
 const token=await page.locator('meta[name="spk-ocular-api-token"]').getAttribute('content')
 const r=await page.request.post(url+'/api/GetTargetState',{headers:{Authorization:'Bearer '+token,Origin:url},data:{provider:'ocular',target:'workspace'}})
 expect(r.ok()).toBe(true);return (await r.json()).workspacePanels as string|undefined
}

test('pinned properties, logs, ended terminals and layout survive a real restart without connections or command replay',async({browser})=>{
 mkdirSync(scratchRoot,{recursive:true});const root=mkdtempSync(join(scratchRoot,'workspace-restart-')),e=env(root);writeAtomic(e.one,ONE)
 const port=await freePort(),url=`http://127.0.0.1:${port}`
 let app:ChildProcess|undefined
 const page=await browser.newPage({viewport:{width:1500,height:950}})
 const errors:string[]=[];page.on('pageerror',error=>errors.push(error.message))
 try{
  app=await start(port,e);await page.goto(url)
  const details=await selectWorkloads(page)
  await details.getByRole('button',{name:'Pin properties',exact:true}).click()
  await expect(dock(page).getByRole('dialog',{name:'Pinned properties · api',exact:true})).toBeVisible();await expect(dock(page).getByRole('dialog',{name:'Pinned properties · api',exact:true}).getByRole('button',{name:'Files',exact:true})).toHaveCount(0)
  await details.getByRole('button',{name:'Logs',exact:true}).click()
  // Workloads may offer an aggregate source picker; choose its first option.
  const picker=page.getByRole('listbox',{name:'Log source'})
  if(await picker.isVisible())await picker.getByRole('option').first().click()
  await expect(dock(page).locator('[data-tab-kind=logs]')).toBeVisible()
  await expect(dock(page).locator('[aria-label="stream state"]')).toContainText('Live')
  const logToken=await page.locator('meta[name="spk-ocular-api-token"]').getAttribute('content')
  const emitted=await page.request.post(url+'/api/_test/logs/emit',{headers:{Authorization:'Bearer '+logToken},data:{object:'api',event:{source:-1,lines:['PRIVATE_LOG_CONTENT']}}});expect(emitted.ok()).toBe(true)
  await expect(dock(page)).toContainText('PRIVATE_LOG_CONTENT')
  await dock(page).locator('[data-tab-kind=logs]').getByRole('button',{name:'Keep after restart',exact:true}).click()
  await details.getByRole('button',{name:'Terminal',exact:true}).click({button:'right'})
  const setup=page.getByRole('dialog',{name:'Open a terminal',exact:true});await expect(setup).toBeVisible()
  await setup.getByRole('textbox',{name:'Command',exact:true}).fill('echo DO_NOT_REPLAY')
  await setup.getByRole('button',{name:'Open',exact:true}).click()
  await expect(dock(page).locator('[data-tab-kind=term]')).toBeVisible()
  await dock(page).locator('[data-tab-kind=term]').getByRole('button',{name:'Keep after restart',exact:true}).click()
  const targetResize=page.getByRole('separator',{name:'Resize targets panel',exact:true});await targetResize.focus();await targetResize.press('ArrowRight')
  const navResize=page.getByRole('separator',{name:'Resize resource navigation',exact:true});await navResize.focus();await navResize.press('ArrowRight')
  const dockResize=dock(page).getByRole('separator').first();await dockResize.focus();await dockResize.press('ArrowUp')
  await expect.poll(async()=>{const saved=await state(page,url);return saved?JSON.parse(saved).panels.length:0}).toBe(3)
  const stored=(await state(page,url))!;expect(stored).not.toContain('DO_NOT_REPLAY');expect(stored).not.toContain('command');expect(stored).not.toContain('PRIVATE_LOG_CONTENT');expect(stored).not.toContain('screen');expect(stored).not.toContain('yaml')
  const saved=JSON.parse(stored);expect(saved.widths.targets).toBe(260);expect(saved.widths.navigation).toBe(212);expect(saved.height).toBe(340)
  await stop(app);app=await start(port,e)
  const reads:string[]=[],commands:Record<string,unknown>[]=[]
  page.on('request',r=>{if(/\/api\/(ConnectTarget|OpenLogStream|OpenTerminal)$/.test(r.url()))reads.push(r.url());if(r.url().endsWith('/api/OpenTerminal'))commands.push(r.postDataJSON())})
  await page.goto(url)
  await expect(dock(page)).toContainText('The previous terminal session ended')
  await expect(page.getByRole('button',{name:'Connect',exact:true})).toBeVisible();expect(reads).toEqual([])
  await dock(page).locator('[data-tab-kind=logs]').getByRole('button').first().click()
  await expect(dock(page).getByRole('button',{name:'Resume logs',exact:true})).toBeDisabled()
  await connectSelected(page);await expect(dock(page).getByRole('button',{name:'Resume logs',exact:true})).toBeEnabled();expect(reads.filter(x=>/Open(LogStream|Terminal)$/.test(x))).toEqual([])
  await dock(page).getByRole('button',{name:'Resume logs',exact:true}).click();await expect(dock(page).locator('[aria-label="stream state"]')).toContainText('Live')
  await dock(page).locator('[data-tab-kind=properties]').getByRole('button').first().click();await dock(page).getByRole('button',{name:'Open pinned properties',exact:true}).click();await expect(dock(page).getByRole('dialog',{name:'Pinned properties · api',exact:true})).toBeVisible();await expect(dock(page).getByRole('dialog',{name:'Pinned properties · api',exact:true}).getByRole('button',{name:'Files',exact:true})).toHaveCount(0)
  await page.screenshot({path:join(scratchRoot,'workspace-restored-properties.png')})
  await dock(page).locator('[data-tab-kind=term-restored]').getByRole('button').first().click();await dock(page).getByRole('button',{name:'Set up a new terminal',exact:true}).click();await expect(setup).toBeVisible();await expect(setup.getByRole('textbox',{name:'Command',exact:true})).toHaveValue('');expect(commands).toEqual([])
  await setup.getByRole('button',{name:'Open',exact:true}).click();await expect.poll(()=>commands.length).toBe(1);expect(JSON.stringify(commands)).not.toContain('DO_NOT_REPLAY')
  await page.screenshot({path:join(scratchRoot,'workspace-new-terminal.png')})
  await expect(page.locator('[data-area=targets]')).toHaveCSS('width','260px')
  expect(errors).toEqual([])
 }finally{await page.close();if(app)await stop(app)}
})

test('cross-target pins remain dormant and reject a same-name replacement after restart',async({browser})=>{
 mkdirSync(scratchRoot,{recursive:true});const root=mkdtempSync(join(scratchRoot,'workspace-identity-')),e=env(root);writeAtomic(e.one,ONE)
 const port=await freePort(),url=`http://127.0.0.1:${port}`
 let app:ChildProcess|undefined;const page=await browser.newPage({viewport:{width:1500,height:950}})
 try{
  app=await start(port,e);await page.goto(url);const details=await selectWorkloads(page);await details.getByRole('button',{name:'Pin properties',exact:true}).click()
  await page.getByRole('option',{name:/^demo2\b/}).click();await connectSelected(page)
  const nav=page.getByRole('navigation',{name:'resources'});await nav.getByRole('button',{name:'Services',exact:true}).click();await page.getByRole('grid',{name:'resources'}).getByRole('gridcell',{name:'api',exact:true}).click();await page.locator('[data-area=details]').getByRole('button',{name:'Pin properties',exact:true}).click()
  await expect.poll(async()=>{const saved=await state(page,url);return saved?JSON.parse(saved).panels.length:0}).toBe(2)
  const saved=JSON.parse((await state(page,url))!);saved.panels[0].ref.uid='old-object-replaced'
  const token=await page.locator('meta[name="spk-ocular-api-token"]').getAttribute('content')
  const changed=await page.request.post(url+'/api/SetTargetState',{headers:{Authorization:'Bearer '+token,Origin:url},data:{provider:'ocular',target:'workspace',key:'workspacePanels',value:JSON.stringify(saved)}});expect(changed.ok()).toBe(true)
  await stop(app);app=await start(port,e)
  const connections:string[]=[];page.on('request',r=>{if(r.url().endsWith('/api/ConnectTarget'))connections.push(r.postDataJSON().id)})
  await page.goto(url);await expect(dock(page).locator('[data-tab-kind=properties]')).toHaveCount(2)
  await dock(page).locator('[data-tab-kind=properties]').first().getByRole('button').first().click();await expect(dock(page).getByRole('button',{name:'Open pinned properties',exact:true})).toBeDisabled()
  await connectSelected(page);expect(connections).toEqual(['demo2']);await expect(dock(page).getByRole('button',{name:'Open pinned properties',exact:true})).toBeDisabled()
  await dock(page).locator('[data-tab-kind=properties]').nth(1).getByRole('button').first().click();await dock(page).getByRole('button',{name:'Open pinned properties',exact:true}).click();await expect(dock(page).getByRole('dialog',{name:'Pinned properties · api',exact:true})).toBeVisible();await expect(dock(page).getByRole('dialog',{name:'Pinned properties · api',exact:true}).getByRole('button',{name:'Files',exact:true})).toHaveCount(0)
  await dock(page).locator('[data-tab-kind=properties]').first().getByRole('button').first().click();await page.getByRole('option',{name:/^demo\b/}).click();await connectSelected(page);await dock(page).getByRole('button',{name:'Open pinned properties',exact:true}).click()
  const pinned=dock(page).getByRole('dialog',{name:'Pinned properties · api',exact:true});await expect(pinned).toContainText('This object no longer exists.');await expect(pinned.locator('.cm-editor')).toHaveCount(0)
  await page.screenshot({path:join(scratchRoot,'workspace-replaced-object.png')})
 }finally{await page.close();if(app)await stop(app)}
})
