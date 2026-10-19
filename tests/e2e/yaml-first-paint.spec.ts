import {expect} from '@playwright/test'
import {test,connectSelected,selectConnection,scratchRoot} from './fixtures'
import {join} from 'node:path'

const yaml='apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: web\n  annotations:\n'+Array.from({length:10000},(_,i)=>`    key-${i}: "value-${i}"\n`).join('')+'spec:\n  replicas: 2\n'

for(const editable of [false,true])test(`YAML ${editable?'edit':'view'} first paints highlighted text without a full plain-text fallback`,async({page})=>{
 await page.route('**/api/GetResource',async route=>{
  const response=await route.fetch(),body=await response.json()
  body.yaml=yaml
  await route.fulfill({response,json:body})
 })
 if(editable){
  await page.route('**/api/ListKinds',async route=>{const response=await route.fetch(),body=await response.json();for(const kind of body.kinds)if(kind.id==='workloads')kind.editable=true;await route.fulfill({response,json:body})})
  await page.route('**/api/GetEditSource',route=>route.fulfill({json:{ref:route.request().postDataJSON(),text:yaml,base:'first-paint-fixture'}}))
 }
 let release!:()=>void,requests=0
 const held=new Promise<void>(resolve=>{release=resolve})
 await page.route(/\/assets\/YamlView-[^/]+\.js$/,async route=>{requests++;await held;await route.continue()})
 await page.goto('/');await selectConnection(page,/^demo\b/);await connectSelected(page)
 await page.getByRole('navigation',{name:'resources'}).getByRole('button',{name:'Workloads',exact:true}).click()
 await page.getByRole('grid',{name:'resources'}).getByRole('gridcell',{name:'web',exact:true}).click()
 const drawer=page.locator('[data-area=details]')
 await page.evaluate(()=>{
  const w=window as unknown as {yamlPaints:{colored:boolean;pre:number}[];stopYamlPaints:boolean}
  w.yamlPaints=[];w.stopYamlPaints=false
  const sample=()=>{
   const panel=document.querySelector('[data-area=details]')
   const content=panel?.querySelector('.cm-content')
   if(content?.textContent?.includes('apiVersion')){
    const base=getComputedStyle(content).color
    w.yamlPaints.push({colored:[...content.querySelectorAll('.cm-line span')].some(node=>getComputedStyle(node).color!==base),pre:panel!.querySelectorAll('pre').length})
   }
   if(!w.stopYamlPaints)requestAnimationFrame(sample)
  }
  requestAnimationFrame(sample)
 })
 try{
  if(editable)await drawer.getByRole('button',{name:'Edit',exact:true}).click()
  else{
   const tab=drawer.getByRole('tab',{name:'YAML',exact:true})
   await tab.hover();await expect.poll(()=>requests).toBe(1)
   await expect(tab).toHaveAttribute('aria-selected','false')
   await tab.click()
  }
  await expect.poll(()=>requests).toBe(1)
  await expect(drawer.locator('[data-loading-overlay]')).toBeVisible()
  await expect(drawer.locator('pre')).toHaveCount(0)
  await expect(drawer.locator('.cm-editor')).toHaveCount(0)
  await page.screenshot({path:join(scratchRoot,`yaml-${editable?'edit':'view'}-loading.png`)})
  release()
  await expect(drawer.locator('.cm-content')).toContainText('apiVersion')
  await expect(drawer.locator('[data-loading-overlay]')).toHaveCount(0)
  await page.evaluate(()=>new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve()))))
  const frames=await page.evaluate(()=>{const w=window as unknown as {yamlPaints:{colored:boolean;pre:number}[];stopYamlPaints:boolean};w.stopYamlPaints=true;return w.yamlPaints})
  expect(frames.length).toBeGreaterThan(0)
  expect(frames.every(frame=>frame.colored&&frame.pre===0)).toBe(true)
  expect(await drawer.locator('.cm-line').count()).toBeLessThan(200)
  expect(requests).toBe(1)
  if(editable){await expect(drawer.locator('.cm-content')).toBeFocused();await expect(drawer.locator('.cm-content')).toHaveAttribute('contenteditable','true')}
  await page.screenshot({path:join(scratchRoot,`yaml-${editable?'edit':'view'}-highlighted.png`)})
 }finally{release();await page.evaluate(()=>{(window as unknown as {stopYamlPaints:boolean}).stopYamlPaints=true})}
})

test('switching resource YAML reuses the editor DOM through loading and resets navigation',async({page})=>{
 let release!:()=>void,waiting=false
 const held=new Promise<void>(resolve=>{release=resolve})
 await page.route('**/api/GetResource',async route=>{
  const response=await route.fetch(),body=await response.json(),name=route.request().postDataJSON().name
  if(name==='db'){waiting=true;await held}
  body.yaml=name==='web'?yaml:'apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: db\nspec:\n  replicas: 1\n'
  await route.fulfill({response,json:body})
 })
 await page.goto('/');await selectConnection(page,/^demo\b/);await connectSelected(page)
 await page.getByRole('navigation',{name:'resources'}).getByRole('button',{name:'Workloads',exact:true}).click()
 const grid=page.getByRole('grid',{name:'resources'})
 await grid.getByRole('gridcell',{name:'web',exact:true}).click()
 const drawer=page.locator('[data-area=details]')
 await drawer.getByRole('tab',{name:'YAML',exact:true}).click()
 const content=drawer.locator('.cm-content')
 await expect(content).toContainText('apiVersion')
 await page.evaluate(()=>{(window as unknown as {originalYamlEditor:Element|null}).originalYamlEditor=document.querySelector('[data-area=details] .cm-editor')})
 await content.click();await content.press('Control+End')
 await expect.poll(()=>drawer.locator('.cm-scroller').evaluate(el=>el.scrollTop)).toBeGreaterThan(0)
 try{
  await grid.getByRole('gridcell',{name:'db',exact:true}).click()
  await expect.poll(()=>waiting).toBe(true)
  await expect(drawer.locator('[data-loading-overlay]')).toBeVisible()
  expect(await page.evaluate(()=>(window as unknown as {originalYamlEditor:Element|null}).originalYamlEditor===document.querySelector('[data-area=details] .cm-editor'))).toBe(true)
  release()
  await expect(content).toContainText('name: db')
  expect(await page.evaluate(()=>(window as unknown as {originalYamlEditor:Element|null}).originalYamlEditor===document.querySelector('[data-area=details] .cm-editor'))).toBe(true)
  await expect.poll(()=>drawer.locator('.cm-scroller').evaluate(el=>el.scrollTop)).toBe(0)
  await expect(drawer.locator('[data-loading-overlay]')).toHaveCount(0)
  await page.screenshot({path:join(scratchRoot,'yaml-reused-editor-next-resource.png')})
 }finally{release()}
})
