import { selectConnection, openAppMenu } from './fixtures'
import {expect,type Page} from '@playwright/test'
import {readFile} from 'node:fs/promises'
import {join} from 'node:path'
import {test,connectSelected,scratchRoot} from './fixtures'
import {languages,languageNames} from '../../web/src/languages'

async function logs(page:Page) {
 await page.goto('/');await selectConnection(page,/^demo\b/);await connectSelected(page)
 await page.getByRole('navigation',{name:'resources'}).getByRole('button',{name:'Services',exact:true}).click()
 await page.getByRole('grid',{name:'resources'}).getByRole('gridcell',{name:'api',exact:true}).click();await page.keyboard.press('l')
 const panel=page.locator('[role=tabpanel]:not([hidden])');await expect(page.locator('.status-bar').getByLabel('stream state')).toHaveText('Live');return panel
}

test('Downloads lists a real browser download, restores its history and clears metadata without deleting the file',async({page})=>{
 await page.addInitScript(()=>Object.defineProperty(window,'showSaveFilePicker',{value:undefined,configurable:true}))
 const panel=await logs(page)
 const downloaded=page.waitForEvent('download');await panel.getByRole('button',{name:'Download',exact:true}).click();const file=await downloaded
 const path=join(scratchRoot,'downloads-panel-real.log');await file.saveAs(path)
 const contents=await readFile(path,'utf8');expect(contents).toContain('[api/main]');expect(contents).not.toContain('\x1b')
 const trigger=page.getByRole('button',{name:'Downloads',exact:true});await trigger.click()
 const downloads=page.getByRole('dialog',{name:'Downloads'});await expect(downloads).toContainText(file.suggestedFilename());await expect(downloads).toContainText('Sent to browser downloads')
 expect(await downloads.getByRole('button',{name:'Open downloaded item'}).count()).toBe(0)
 await page.keyboard.press('Escape');await expect(trigger).toBeFocused();await page.reload()
 await page.getByRole('button',{name:'Downloads',exact:true}).click();await expect(page.getByRole('dialog',{name:'Downloads',exact:true})).toContainText(file.suggestedFilename())
 await page.getByRole('button',{name:'Clear history',exact:true}).click();await expect(page.getByRole('dialog',{name:'Downloads',exact:true})).toContainText('No downloads yet')
 expect(await readFile(path,'utf8')).toBe(contents)
 const clear=page.getByRole('button',{name:'Clear history',exact:true});await expect(clear).toBeDisabled()
 await page.mouse.move(10,300);const color=await clear.evaluate(el=>getComputedStyle(el).color)
 await clear.hover();expect(await clear.evaluate(el=>getComputedStyle(el).color)).toBe(color)
 expect(await clear.evaluate(el=>getComputedStyle(el).cursor)).toBe('default')
 await page.screenshot({path:join(scratchRoot,'downloads-empty.png')})
})

test('Downloads shows active and completed picker exports, and refuses to invent a browser path',async({page})=>{
 await page.addInitScript(()=>{
  const target=window as unknown as {showSaveFilePicker:()=>Promise<unknown>;finishDownload?:()=>void}
  target.showSaveFilePicker=async()=>({name:'chosen-history.log',createWritable:async()=>({write:async()=>{},close:async()=>new Promise<void>(resolve=>{target.finishDownload=resolve}),abort:async()=>{}})})
 })
 const panel=await logs(page);await panel.getByRole('button',{name:'Download',exact:true}).click()
 await expect.poll(()=>page.evaluate(()=>typeof(window as unknown as {finishDownload?:()=>void}).finishDownload)).toBe('function')
 const trigger=page.getByRole('button',{name:'Downloads',exact:true})
 await expect(trigger).toHaveAttribute('data-active','true');await expect(trigger).toHaveAttribute('data-unseen','false')
 const icon=trigger.locator('svg'),track=trigger.locator('.downloads-trigger-track'),runner=trigger.locator('.downloads-trigger-runner')
 await expect(track).toBeVisible()
 await expect.poll(()=>icon.evaluate(el=>getComputedStyle(el).animationName)).toBe('none')
 const iconBefore=await icon.boundingBox()
 await expect.poll(()=>runner.evaluate(el=>getComputedStyle(el).animationName)).toBe('download-button-progress')
 const frame=()=>runner.evaluate(el=>{const m=new DOMMatrixReadOnly(getComputedStyle(el).transform);return {x:m.m41,y:m.m42}})
 await expect.poll(()=>runner.evaluate(async el=>{
  const before=new DOMMatrixReadOnly(getComputedStyle(el).transform)
  await new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve())))
  const after=new DOMMatrixReadOnly(getComputedStyle(el).transform)
  return before.m42===0&&after.m42===0&&after.m41>before.m41
 })).toBe(true)
 expect(await icon.boundingBox()).toEqual(iconBefore)
 const bounds=await trigger.boundingBox(),bar=await track.boundingBox();expect(bar!.height).toBe(2);expect(bar!.y).toBeGreaterThan(iconBefore!.y+iconBefore!.height-1);expect(bar!.x).toBeGreaterThan(bounds!.x);expect(bar!.x+bar!.width).toBeLessThan(bounds!.x+bounds!.width)
 await page.screenshot({path:join(scratchRoot,'downloads-active-button.png')})
 await page.emulateMedia({reducedMotion:'reduce'});await expect.poll(()=>runner.evaluate(el=>getComputedStyle(el).animationName)).toBe('none')
 const still=await frame();await page.waitForTimeout(100);expect(await frame()).toEqual(still)
 await expect(track).toBeVisible();expect(await icon.boundingBox()).toEqual(iconBefore)
 await page.emulateMedia({reducedMotion:'no-preference'})
 await trigger.click();const downloads=page.getByRole('dialog',{name:'Downloads'})
 await expect(downloads.getByRole('progressbar')).toBeVisible();await expect(downloads).toContainText('chosen-history.log')
 await page.keyboard.press('Escape');await page.evaluate(()=>(window as unknown as {finishDownload:()=>void}).finishDownload())
 await expect(trigger).toHaveAttribute('data-active','false');await expect(track).toHaveCount(0);await expect(trigger).toHaveAttribute('data-unseen','true')
 await page.screenshot({path:join(scratchRoot,'downloads-new-button.png')})
 await trigger.click();await expect(trigger).toHaveAttribute('data-unseen','false')
 await expect(downloads).toContainText('Completed');await expect(downloads.getByRole('progressbar')).toHaveCount(0)
 for(const name of ['Open downloaded item','Show in folder','Copy path'])await expect(downloads.getByRole('button',{name,exact:true})).toBeDisabled()
 await page.screenshot({path:join(scratchRoot,'downloads-browser-completed.png')})
 await expect(downloads.locator('time')).toHaveCount(0);await expect(downloads.locator('[data-download-id] span[title]').first()).toHaveAttribute('title',/.+/)
 await expect(downloads.locator('[data-download-id]')).toHaveCount(1)
 expect(await downloads.locator('[data-download-id]').evaluate(el=>el.getBoundingClientRect().height)).toBeLessThan(65)
 await expect(downloads.getByRole('button',{name:'Delete downloaded item',exact:true})).toBeDisabled()
 await page.keyboard.press('Escape');await page.reload();await expect(page.getByRole('button',{name:'Downloads',exact:true})).toHaveAttribute('data-unseen','false')
})

test('Downloads shares detached log exports with the main window',async({page,context})=>{
 await context.addInitScript(()=>{
  ;(window as unknown as {showSaveFilePicker:()=>Promise<unknown>}).showSaveFilePicker=async()=>({name:'detached-history.log',createWritable:async()=>({write:async()=>{},close:async()=>{},abort:async()=>{}})})
 })
 const panel=await logs(page)
 const opened=context.waitForEvent('page');await panel.getByRole('button',{name:'Open in window',exact:true}).click();const popup=await opened
 await expect(popup.getByRole('button',{name:'Download',exact:true})).toBeVisible()
 await popup.getByRole('button',{name:'Download',exact:true}).click();await expect(popup.locator('main')).toContainText('Saved to detached-history.log')
 await page.getByRole('button',{name:'Downloads',exact:true}).click();await expect(page.getByRole('dialog',{name:'Downloads',exact:true})).toContainText('detached-history.log')
 await expect(popup.getByRole('button',{name:'Downloads',exact:true})).toHaveCount(0)
 await expect(popup.getByRole('dialog',{name:'Downloads',exact:true})).toHaveCount(0)
 await page.getByRole('button',{name:'Remove from history',exact:true}).click();await expect(page.getByRole('dialog',{name:'Downloads',exact:true})).toContainText('No downloads yet')
 await popup.getByRole('button',{name:'Download',exact:true}).click();await expect(page.getByRole('dialog',{name:'Downloads',exact:true})).toContainText('detached-history.log')
 await expect(page.getByRole('dialog',{name:'Downloads',exact:true}).locator('[data-download-id]')).toHaveCount(1)
 await page.getByRole('button',{name:'Clear history',exact:true}).click();await expect(page.getByRole('dialog',{name:'Downloads',exact:true})).toContainText('No downloads yet')
 await popup.close()
})

test('Downloads panel fits all eight locales and keeps keyboard focus within its popup',async({page})=>{
 await page.addInitScript(()=>localStorage.setItem('ocular-download-history-v1',JSON.stringify([{id:'locale-file',name:'application-'+('long-name-'.repeat(12))+'.log',kind:'logs',status:'failed',failureReason:'permission',bytes:12345,startedAt:Date.now()}])))
 await page.goto('/')
 const titles={en:'Downloads',ru:'Загрузки',zh:'下载',es:'Descargas',de:'Downloads',fr:'Téléchargements',pt:'Downloads',ja:'ダウンロード'}
 try {
 for(const lang of languages) {
  await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[lang],exact:true}).click()
  await expect(page.locator('html')).toHaveAttribute('lang',lang)
  for(const width of [1440,860]) {
   await page.setViewportSize({width,height:900});await page.getByRole('button',{name:titles[lang],exact:true}).click()
   const panel=page.getByRole('dialog',{name:titles[lang],exact:true});await expect(panel.locator('[data-download-id]')).toHaveCount(1)
   await expect(panel.locator('[data-download-failure]')).toBeVisible()
   expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
   expect(await panel.evaluate(el=>{const r=el.getBoundingClientRect();return r.left>=0&&r.right<=innerWidth&&r.bottom<=innerHeight&&el.scrollWidth<=el.clientWidth})).toBe(true)
   await page.keyboard.press('Tab');expect(await panel.evaluate(el=>el.contains(document.activeElement))).toBe(true)
   await page.keyboard.press('Control+k');await expect(page.locator('[role=dialog][aria-modal=true]')).toHaveCount(1)
   await page.screenshot({path:join(scratchRoot,`downloads-${lang}-${width}.png`)})
   if(await page.getByRole('tooltip').count()) {
    await page.keyboard.press('Escape');await expect(page.getByRole('tooltip')).toHaveCount(0);await expect(panel).toHaveCount(1)
   }
   await page.keyboard.press('Escape');await expect(panel).toHaveCount(0)
  }
 }
 }finally{
  const panel=page.locator('[data-downloads-panel]')
  if(await panel.isVisible())await panel.locator('header button').last().click()
  await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:'English',exact:true}).click()
  await expect(page.locator('html')).toHaveAttribute('lang','en')
 }
})

test('Downloads compact rows show localized metadata and typed icons without exposing browser paths',async({page})=>{
 await page.addInitScript(()=>localStorage.setItem('ocular-download-history-v1',JSON.stringify([
  {id:'archive',name:'backup.tar.gz',kind:'logs',status:'browser',bytes:12500000,startedAt:Date.now()-180000,path:'/must-not-be-in-browser'},
  {id:'small',name:'server.log',kind:'logs',status:'complete',bytes:1536,startedAt:Date.now()-3000},
  {id:'failed',name:'failed.log',kind:'logs',status:'failed',bytes:0,startedAt:Date.now()-60000}
 ])))
 await page.goto('/');const trigger=page.getByRole('button',{name:'Downloads',exact:true});await expect(trigger).toHaveAttribute('data-unseen','true');await trigger.click()
 const panel=page.getByRole('dialog',{name:'Downloads',exact:true});await expect(panel.locator('[data-download-id]')).toHaveCount(3)
 await expect(panel).toContainText('12.5 MB');await expect(panel).toContainText('1.5 kB');await expect(panel).not.toContainText('/must-not-be-in-browser')
 await expect(panel.locator('time').first()).toHaveAttribute('title',/.+/)
 await expect(trigger).toHaveAttribute('data-unseen','false')
 await page.screenshot({path:join(scratchRoot,'downloads-compact.png')})
 await panel.locator('[data-download-id="failed"]').getByRole('button',{name:'Remove from history',exact:true}).click();await expect(panel.locator('[data-download-id]')).toHaveCount(2)
 await page.reload();await trigger.click();await expect(page.getByRole('dialog',{name:'Downloads',exact:true}).locator('[data-download-id]')).toHaveCount(2)
})

for(const [name,errorName,reason,message] of [
 ['permission','NotAllowedError','permission','Permission to read or save the download was denied.'],
 ['quota','QuotaExceededError','no_space','The destination has insufficient space or its quota was exceeded.'],
] as const) {
 test(`Downloads explains ${name} failures, keeps the first reason and persists no raw error`,async({page})=>{
  await page.addInitScript(({errorName})=>{
   ;(window as unknown as {showSaveFilePicker:()=>Promise<unknown>}).showSaveFilePicker=async()=>({name:'failed-'+errorName+'.log',createWritable:async()=>({write:async()=>{throw new DOMException('credential=DO-NOT-RETAIN',errorName)},close:async()=>{(window as unknown as {committed:boolean}).committed=true},abort:async()=>{(window as unknown as {aborted:boolean}).aborted=true}})})
  },{errorName})
  const panel=await logs(page);await panel.getByRole('button',{name:'Download',exact:true}).click()
  await expect.poll(()=>page.evaluate(()=>(window as unknown as {aborted?:boolean}).aborted)).toBe(true)
  expect(await page.evaluate(()=>(window as unknown as {committed?:boolean}).committed)).not.toBe(true)
  const trigger=page.getByRole('button',{name:'Downloads',exact:true});await expect(trigger).toHaveAttribute('data-unseen','false');await trigger.click()
  const downloads=page.getByRole('dialog',{name:'Downloads'});await expect(downloads.locator('[data-download-failure]')).toHaveText(message)
  await expect(downloads).not.toContainText('DO-NOT-RETAIN')
  const records=await page.evaluate(()=>localStorage.getItem('ocular-download-history-v1'));expect(records).toContain(`"failureReason":"${reason}"`);expect(records).not.toContain('DO-NOT-RETAIN')
  await page.screenshot({path:join(scratchRoot,`downloads-failure-${reason}.png`)})
  await page.reload();await page.getByRole('button',{name:'Downloads',exact:true}).click();await expect(page.getByRole('dialog',{name:'Downloads'}).locator('[data-download-failure]')).toHaveText(message)
 })
}

test('Downloads explains incomplete remote history without storing its raw reason or log content',async({page})=>{
 await page.addInitScript(()=>{
  ;(window as unknown as {showSaveFilePicker:()=>Promise<unknown>}).showSaveFilePicker=async()=>({name:'incomplete.log',createWritable:async()=>({write:async()=>{},close:async()=>{(window as unknown as {committed:boolean}).committed=true},abort:async()=>{(window as unknown as {aborted:boolean}).aborted=true}})})
 })
 await page.route('**/api/OpenLogHistory',route=>route.fulfill({json:{streamId:'safe-failure-fixture'}}))
 await page.route('**/logs/safe-failure-fixture',route=>route.fulfill({contentType:'application/x-ndjson',body:[{k:'source',id:1,key:'history-uid/main',label:'api/main'},{k:'lines',s:1,l:[['','payload=DO-NOT-RETAIN']]},{k:'state',s:1,state:'truncated',msg:'credential=DO-NOT-RETAIN'},{k:'end',reason:'done'}].map(f=>JSON.stringify(f)+'\n').join('')}))
 const panel=await logs(page);await panel.getByRole('button',{name:'Download',exact:true}).click()
 await expect.poll(()=>page.evaluate(()=>(window as unknown as {aborted?:boolean}).aborted)).toBe(true)
 expect(await page.evaluate(()=>(window as unknown as {committed?:boolean}).committed)).not.toBe(true)
 await page.getByRole('button',{name:'Downloads',exact:true}).click();const downloads=page.getByRole('dialog',{name:'Downloads'})
 await expect(downloads.locator('[data-download-failure]')).toHaveText('The source returned incomplete or invalid log history; no partial file was saved.')
 await expect(downloads).not.toContainText('DO-NOT-RETAIN');expect(await page.evaluate(()=>localStorage.getItem('ocular-download-history-v1'))).not.toContain('DO-NOT-RETAIN')
 await page.screenshot({path:join(scratchRoot,'downloads-failure-incomplete.png')})
})

test('Downloads hides sub-minute age, freezes relative time while open and refreshes it on reopening',async({page})=>{
 const opened=Date.parse('2026-10-13T06:00:00Z')
 await page.clock.install({time:opened})
 await page.clock.pauseAt(opened+1000)
 await page.addInitScript(({opened})=>localStorage.setItem('ocular-download-history-v1',JSON.stringify([
  {id:'frozen-older',name:'older.log',kind:'logs',status:'complete',bytes:1536,startedAt:opened-120000},
  {id:'frozen-recent',name:'recent.log',kind:'logs',status:'complete',bytes:2048,startedAt:opened-30000}
 ])),{opened})
 await page.goto('/');const trigger=page.getByRole('button',{name:'Downloads',exact:true});await trigger.click()
 const panel=page.getByRole('dialog',{name:'Downloads'}),older=panel.locator('[data-download-id="frozen-older"]'),recent=panel.locator('[data-download-id="frozen-recent"]')
 await expect(older.locator('time')).toHaveText('2 minutes ago');await expect(recent.locator('time')).toHaveCount(0)
 await expect(recent.locator('span[title]').first()).toHaveAttribute('title',/.+/)
 await page.screenshot({path:join(scratchRoot,'downloads-time-at-opening.png')})
 await page.clock.runFor(120000)
 await page.evaluate(()=>window.dispatchEvent(new StorageEvent('storage',{key:'ocular-download-history-v1'})))
 await expect(older.locator('time')).toHaveText('2 minutes ago');await expect(recent.locator('time')).toHaveCount(0)
 await page.keyboard.press('Escape');await trigger.click()
 await expect(older.locator('time')).toHaveText('4 minutes ago');await expect(recent.locator('time')).toHaveText('2 minutes ago')
 await page.screenshot({path:join(scratchRoot,'downloads-time-on-reopening.png')})
})
