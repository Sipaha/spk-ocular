import {test,expect,type Browser,type Page} from '@playwright/test'
import {mkdirSync,mkdtempSync,writeFileSync} from 'node:fs'
import {join} from 'node:path'
import {env,kubeconfig,writeAtomic,scratchRoot,selectConnection,connectSelected,openAppMenu} from './fixtures'
import {start,stop,freePort} from './restartApp'
import {compositionServer} from './log-composition-fixture'
import {languages,languageNames} from '../../web/src/languages'
import {returnWithWindowButton} from './synth'

async function launch(browser:Browser){
 mkdirSync(scratchRoot,{recursive:true});const root=mkdtempSync(join(scratchRoot,'composed-')),e=env(root),fixture=await compositionServer()
 writeAtomic(e.one,kubeconfig('composed','composed').replace(/https:\/\/[^"\s]+/g,`http://127.0.0.1:${fixture.port}`).replace(/ns-composed/g,'blue'))
 const port=await freePort(),url=`http://127.0.0.1:${port}`,page=await browser.newPage({viewport:{width:1440,height:1000}}),app=await start(port,e)
 try{
  await page.context().addInitScript(()=>{
   const target=window as unknown as {composedText:string;composedCommitted:boolean;composedAborted:boolean;showSaveFilePicker:()=>Promise<unknown>}
   target.showSaveFilePicker=async()=>{target.composedText='';target.composedCommitted=false;target.composedAborted=false;return {name:'composed-history.log',createWritable:async()=>({write:async(text:string)=>{target.composedText+=text},close:async()=>{target.composedCommitted=true},abort:async()=>{target.composedAborted=true}})}}
  })
  await page.goto(url);const token=await page.locator('meta[name=spk-ocular-api-token]').getAttribute('content')
  const headers={Authorization:`Bearer ${token}`,Origin:url}
  expect((await page.request.post(url+'/api/Configurations',{headers,data:{command:'import',paths:[e.one]}})).ok()).toBe(true)
  await page.reload();await selectConnection(page,/^composed\b/);await connectSelected(page)
  return {page,fixture,url,headers,close:async()=>{await page.close();await stop(app);await fixture.close()}}
 }catch(error){await page.close();await stop(app);await fixture.close();throw error}
}
async function kind(page:Page,name:string){const nav=page.getByRole('navigation',{name:'resources'});await nav.getByRole('textbox',{name:'Filter resources'}).fill(name);await nav.getByRole('button',{name,exact:true}).click()}
async function add(page:Page,name:string){await page.getByRole('grid',{name:'resources'}).getByRole('gridcell',{name,exact:true}).click({button:'right'});await page.getByRole('menuitem',{name:'Add to composed logs',exact:true}).click()}
async function prepare(page:Page){await kind(page,'Deployments');await add(page,'web');await kind(page,'Pods');await add(page,'web-a');await page.getByRole('button',{name:'Review sources',exact:true}).click();const dialog=page.getByRole('dialog',{name:'Composed logs',exact:true});await expect(dialog).toContainText('deploy-web');await expect(dialog).toContainText('pod-a');return dialog}

test('composed source review is inert; overlap, filters, finite download and detached owner retain exact set',async({browser})=>{
 const f=await launch(browser),{page,fixture}=f
 try{
  const dialog=await prepare(page)
  expect(fixture.requests.filter(r=>r.path.endsWith('/log'))).toHaveLength(0)
  await dialog.getByRole('button',{name:'Open composed logs',exact:true}).click()
  const dock=page.locator('[data-area="dock"]');await expect(dock).toContainText('blue/web-a/app');await expect(dock).toContainText('blue ERROR first');await expect(dock).toContainText('blue INFO second');await expect(dock.locator('[data-log-viewport] [data-index]')).toHaveCount(2)
  expect(fixture.requests.filter(r=>r.path.endsWith('/log')&&r.query.follow!=='true')).toHaveLength(1)
  await expect(dock.getByRole('button',{name:'Keep after restart',exact:true})).toBeDisabled()
  await dock.getByRole('textbox',{name:'Filter (*)',exact:true}).fill('*ERROR*');await expect(dock).toContainText('blue ERROR first');await expect(dock).not.toContainText('blue INFO second');await expect(dock.locator('[data-log-viewport] [data-index]')).toHaveCount(1)
  await dock.getByRole('button',{name:'Download',exact:true}).click({button:'right'})
  await expect.poll(()=>page.evaluate(()=>(window as unknown as {composedCommitted:boolean}).composedCommitted)).toBe(true)
  const exported=await page.evaluate(()=>(window as unknown as {composedText:string}).composedText)
  expect(exported).toMatch(/blue\/web-a\/app +blue ERROR first/);expect(exported).toMatch(/blue\/web-a\/app +blue INFO second/);expect(exported.split('blue ERROR first')).toHaveLength(2)
  await page.getByRole('dialog',{name:'Downloads',exact:true}).getByRole('button',{name:'Close',exact:true}).click()
  const popupEvent=page.waitForEvent('popup');await dock.getByRole('button',{name:'Open in window',exact:true}).click();const popup=await popupEvent
  await expect(popup.locator('main')).toContainText('blue/web-a/app');await expect(popup.locator('main')).toContainText('blue ERROR first')
  fixture.append('ERROR detached-live');await expect(popup.locator('main')).toContainText('detached-live')
  await popup.getByRole('button',{name:'Download',exact:true}).click({button:'right'})
  await expect.poll(()=>popup.evaluate(()=>(window as unknown as {composedCommitted:boolean}).composedCommitted)).toBe(true)
  expect(await popup.evaluate(()=>(window as unknown as {composedText:string}).composedText)).toMatch(/blue\/web-a\/app +blue INFO second/)
  // Detached exports appear in the main window's Downloads history.
  await returnWithWindowButton(popup,page)
  await expect(dock.locator('[data-log-viewport]')).toBeVisible();await expect(dock).toContainText('detached-live')
  expect(fixture.requests.every(r=>r.method==='GET')).toBe(true)
 }finally{writeFileSync(test.info().outputPath('composition-wire.json'),JSON.stringify(fixture.requests,null,2));await f.close()}
})
test('source review and composed viewer fit eight locales at wide and compact widths',async({browser})=>{
 const f=await launch(browser),{page}=f
 try{
  await prepare(page);await page.getByRole('dialog',{name:'Composed logs',exact:true}).getByRole('button',{name:'Open composed logs',exact:true}).click();await expect(page.locator('[data-area="dock"]')).toContainText('blue ERROR first')
  for(const code of languages){
   await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[code],exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang',code)
   for(const width of [1440,860]){
    await page.setViewportSize({width,height:1000});await page.screenshot({path:join(scratchRoot,`composition-${code}-${width}.png`)});expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
    await page.locator('[data-composition-review]').click();const review=page.getByRole('dialog',{exact:false});await expect(review).toContainText('deploy-web');await expect(review).toContainText('pod-a');await page.screenshot({path:join(scratchRoot,`composition-${code}-${width}-review.png`)});expect(await review.evaluate(e=>e.scrollWidth<=e.clientWidth)).toBe(true);await review.locator('header button').click()
   }
   await page.setViewportSize({width:1440,height:1000})
  }
 }finally{await f.close()}
})

test('denied roots keep their own coverage while permitted overlap streams and incomplete export aborts the destination',async({browser})=>{
 const f=await launch(browser),{page}=f
 try{
  await kind(page,'Deployments');await add(page,'blocked');await add(page,'web');await kind(page,'Pods');await add(page,'web-a')
  await page.getByRole('button',{name:'Review sources',exact:true}).click();await page.getByRole('dialog',{name:'Composed logs',exact:true}).getByRole('button',{name:'Open composed logs',exact:true}).click()
  const dock=page.locator('[data-area="dock"]');await expect(dock).toContainText('blue ERROR first');await expect(dock).toContainText('blocked')
  await dock.locator('details summary').click();await expect(dock.locator('details')).toContainText('access denied')
  await dock.getByRole('button',{name:'Download',exact:true}).click({button:'right'})
  await expect.poll(()=>page.evaluate(()=>(window as unknown as {composedAborted:boolean}).composedAborted)).toBe(true)
  expect(await page.evaluate(()=>(window as unknown as {composedCommitted:boolean}).composedCommitted)).toBe(false)
  expect(f.fixture.requests.filter(r=>r.path.endsWith('/log')&&r.query.follow!=='true')).toHaveLength(1)
 }finally{await f.close()}
})
