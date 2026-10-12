import { connectSelected } from './fixtures'
import { expect, type Page } from '@playwright/test'
import { test, scratchRoot } from './fixtures'
import { join } from 'node:path'
import {languageNames,languages} from '../../web/src/languages'

async function token(page: Page) {
  return (await page.locator('meta[name="spk-ocular-api-token"]').getAttribute('content'))!
}

async function emit(page: Page, object: string, event: Record<string, unknown>) {
  const res = await page.request.post('/api/_test/logs/emit', { headers: { Authorization: `Bearer ${await token(page)}` }, data: { object, event: { source: -1, ...event } } })
  expect(res.ok()).toBeTruthy()
  return ((await res.json()) as { delivered: number }).delivered
}

async function stats(page: Page) {
  const res = await page.request.get('/api/_test/stats', { headers: { Authorization: `Bearer ${await token(page)}` } })
  return (await res.json()) as { streams: number }
}

async function openLogs(page: Page, object: string) {
  await page.goto('/')
  await page.getByRole('option', { name: /^demo\b/ }).click()
  await connectSelected(page)
  await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Services', exact: true }).click()
  const grid = page.getByRole('grid', { name: 'resources' })
  await grid.getByRole('gridcell', { name: object, exact: true }).click()
  await page.keyboard.press('l')
  const panel = page.locator('[role=tabpanel]:not([hidden])')
  await expect(panel.getByLabel('stream state')).toHaveText('Live')
  await expect(panel.locator('[data-log-viewport]')).toBeFocused()
  return panel
}

const rows = (panel: ReturnType<Page['locator']>) => panel.locator('[data-log-viewport] [data-index]')

test('opening and reopening logs focuses the viewport and enables its shortcuts',async({page})=>{
 const panel=await openLogs(page,'api'),vp=panel.locator('[data-log-viewport]')
 await page.keyboard.press('Control+f')
 await expect(panel.getByLabel('Search (Ctrl+F)')).toBeFocused()
 await page.locator('[data-area="details"]').getByRole('button',{name:'Logs',exact:true}).click()
 await expect(vp).toBeFocused()
 await page.keyboard.press('Control+f')
 await expect(panel.getByLabel('Search (Ctrl+F)')).toBeFocused()
})

test('one source: backlog, live lines, ANSI, text filters, search, copy', async ({ page }) => {
  const panel = await openLogs(page, 'api')
  // All six backlog lines are visible, including DEBUG.
  await expect(panel.getByLabel('line count')).toHaveText('6 lines')
  // "\x1b[1;32mready": bold green from the theme's ANSI palette
  await expect(panel.getByText('ready', { exact: true })).toHaveAttribute('style', /color: var\(--color-ansi-2\).*font-weight: 600/)
  await expect(rows(panel).filter({ hasText: 'at handler' })).toHaveClass(/text-log-error/) // continuation keeps ERROR

  expect(await emit(page, 'api', { lines: ['INFO live one', 'ERROR live two'] })).toBe(1)
  await expect(rows(panel).last()).toHaveText('ERROR live two')

  await page.screenshot({ path: join(scratchRoot, 'logs-without-level-buttons.png') })
  await panel.getByLabel('Search (Ctrl+F)').fill('live')
  await expect(panel.getByLabel('matches')).toHaveText('1/2')
  await expect(panel.locator('mark.log-match-current')).toHaveText('live')
  await expect(panel.getByRole('button', { name: /^(DEBUG|INFO|ERROR|WARN|TRACE|UNKNOWN)$/ })).toHaveCount(0)
  await panel.getByLabel('Filter (*)', { exact: true }).fill('INFO')
  await expect(panel.getByLabel('matches')).toHaveText('1/1')

  // copy the shown lines
  await panel.getByRole('button', { name: 'Copy' }).click()
  const copied = await page.evaluate(() => navigator.clipboard.readText())
  expect(copied.split('\n')).toEqual(['INFO api/main starting', 'INFO api/main ready', 'INFO live one'])
})

test('a group: sources with prefixes and per-source states', async ({ page }) => {
  const panel = await openLogs(page, 'workers')
  await expect(panel.getByLabel('line count')).toHaveText('18 lines')
  await expect(panel.getByRole('button', { name: 'Source' })).toHaveAttribute('aria-pressed', 'true')
  // the shared "worker-" part of the source names is dropped from the prefix
  await expect(rows(panel).first()).toHaveText('1 INFO worker-1/main starting')
  await emit(page, 'workers', { source: 1, state: { state: 'waiting', message: 'the container exited (code 1); waiting for it to restart' } })
  await expect(panel.getByRole('status')).toContainText('2: waiting — the container exited (code 1)')
  await panel.getByRole('button', { name: 'Source' }).click()
  await expect(rows(panel).first()).toHaveText('INFO worker-1/main starting')
})

test('closing a tab ends its stream', async ({ page }) => {
  await openLogs(page, 'api')
  await expect.poll(async () => (await stats(page)).streams).toBe(1)
  await page.getByRole('tab', { name: /services\/api/ }).getByRole('button', { name: 'Close tab' }).click()
  await expect.poll(async () => (await stats(page)).streams).toBe(0)
  expect(await emit(page, 'api', { lines: ['nobody listens'] })).toBe(0)
})

test('a stream that ends says so and can be reopened', async ({ page }) => {
  const panel = await openLogs(page, 'api')
  await emit(page, 'api', { end: true })
  await expect(panel.getByLabel('stream state')).toHaveText('Complete')
  await panel.getByRole('button', { name: 'Previous', exact: true }).click()
  await expect(rows(panel).first()).toHaveText(/^previous: INFO api\/main starting/)
  await expect(panel.getByLabel('stream state')).toHaveText('Complete')
})

test('scrolling up stops following; new lines do not move the view; Follow returns', async ({ page }) => {
  const panel = await openLogs(page, 'api')
  const many = Array.from({ length: 200 }, (_, i) => `INFO line ${i}`)
  await emit(page, 'api', { lines: many })
  await expect(rows(panel).last()).toHaveText('INFO line 199')
  const vp = panel.locator('[data-log-viewport]')
  await vp.hover()
  await page.mouse.wheel(0, -2000)
  await expect(panel.getByRole('button', { name: 'Follow' })).toBeVisible()
  const top = await vp.evaluate((el) => el.scrollTop)
  await emit(page, 'api', { lines: ['INFO while reading'] })
  await expect(panel.getByLabel('line count')).toHaveText('207 lines')
  expect(await vp.evaluate((el) => el.scrollTop)).toBe(top)
  await expect(panel.getByText('INFO while reading')).toHaveCount(0) // not scrolled into view (virtualized away)
  const follow=panel.getByRole('button',{name:'Follow'})
  await follow.hover()
  const tooltip=page.getByRole('tooltip')
  await expect(tooltip).toContainText('(End)')
  const tip=(await tooltip.boundingBox())!,button=(await follow.boundingBox())!
  expect(tip.x+tip.width).toBeGreaterThanOrEqual(button.x)
  expect(tip.x).toBeLessThanOrEqual(button.x+button.width)
  expect(tip.x+tip.width).toBeLessThanOrEqual(await page.evaluate(()=>window.innerWidth)-8)
  await page.screenshot({path:join(scratchRoot,'follow-tooltip-attached.png')})
  const input=panel.getByLabel('Search (Ctrl+F)')
  await input.focus();await page.keyboard.press('End')
  await expect(follow).toBeVisible()
  await vp.focus();await page.keyboard.press('End')
  await expect(panel.getByText('INFO while reading')).toBeVisible()
  await expect(panel.getByRole('button', { name: 'Follow' })).toHaveCount(0)
  await expect(vp).toBeFocused()
})

test('the app select of the toolbar: focused list, live lines do not close it, Esc is its own', async ({ page }) => {
  const panel = await openLogs(page, 'api')
  const lines = panel.getByRole('button', { name: 'Lines', exact: true })
  await lines.click()
  const list = page.getByRole('listbox', { name: 'Lines' })
  await expect(list).toBeFocused()
  // New lines scroll the viewport beside it: the list stays.
  await emit(page, 'api', { lines: Array.from({ length: 50 }, (_, i) => `more ${i}`) })
  await expect(rows(panel).filter({ hasText: 'more 49' })).toBeAttached()
  await expect(list).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(list).toHaveCount(0)
  await expect(lines).toBeFocused()
  await expect(panel.getByLabel('stream state')).toHaveText('Live')
  await page.keyboard.press('ArrowDown')
  await expect(list).toBeFocused()
  await page.keyboard.press('Home')
  await page.keyboard.press('Enter')
  await expect(lines).toHaveText('100')
  // Tab from the open list goes on from its button, Shift+Tab back.
  await lines.click()
  await page.keyboard.press('Tab')
  await expect(list).toHaveCount(0)
  await expect(panel.getByRole('button', { name: 'Since', exact: true })).toBeFocused()
  await lines.click()
  await page.keyboard.press('Shift+Tab')
  await expect(panel.getByRole('button', { name: 'Channel', exact: true })).toBeFocused()
})

test('panels resize independently while active logs keep streaming', async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 1000 })
  const panel = await openLogs(page, 'api')
  const targets = page.locator('[data-area="targets"]')
  const navigation = page.locator('[data-area="nav"]')
  const details = page.locator('[data-area="details"]')
  const drag = async (label: string, dx: number, dy: number) => {
    const box = (await page.getByRole('separator', { name: label, exact: true }).boundingBox())!
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
    await page.mouse.down()
    await page.mouse.move(box.x + box.width / 2 + dx, box.y + box.height / 2 + dy, { steps: 12 })
    await page.mouse.up()
  }
  const tw = (await targets.boundingBox())!.width
  await drag('Resize targets panel', 60, 0)
  await expect(targets).toBeVisible()
  await expect(navigation).toBeVisible()
  expect((await targets.boundingBox())!.width).toBeCloseTo(tw + 60, 0)
  const nw = (await navigation.boundingBox())!.width
  await drag('Resize resource navigation', 60, 0)
  expect((await navigation.boundingBox())!.width).toBeCloseTo(nw + 60, 0)
  const dw = (await details.boundingBox())!.width
  await drag('Resize details panel', -60, 0)
  expect((await details.boundingBox())!.width).toBeCloseTo(dw + 60, 0)
  await page.getByRole('separator', { name: 'Resize details panel' }).press('ArrowRight')
  expect((await details.boundingBox())!.width).toBeCloseTo(dw + 40, 0)

  // Exercise the expensive case: a full buffer and new lines during drag.
  await emit(page, 'api', { lines: Array.from({ length: 20000 }, (_, i) => `INFO buffered line ${i}`) })
  await expect(panel.getByText('INFO buffered line 19999', { exact: true })).toBeVisible()
  const separator = page.getByRole('separator', { name: 'Resize the bottom panel' })
  const box = (await separator.boundingBox())!
  const x = box.x + 80
  const y = box.y + 2
  await page.mouse.move(x, y)
  await page.mouse.down()
  const latencies: number[] = []
  for (let i = 1; i <= 12; i++) {
    const now = Date.now()
    await page.mouse.move(x, y - i * 10)
    await emit(page, 'api', { lines: [`INFO during resize ${i}`] })
    await page.evaluate(() => new Promise<void>((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))))
    expect(Math.abs((await separator.boundingBox())!.y - (box.y - i * 10))).toBeLessThan(3)
    latencies.push(Date.now() - now)
  }
  await page.mouse.up()
  await expect(panel.getByText('INFO during resize 12', { exact: true })).toBeVisible()
  console.log('resize step ms including emit and two frames:', JSON.stringify(latencies))
  if (process.env.E2E_PANELS_SCREENSHOT) await page.screenshot({ path: process.env.E2E_PANELS_SCREENSHOT })
  // Navigation keeps the user's widths; the table still has room.
  await page.getByRole('option', { name: /^demo2\b/ }).click()
  await connectSelected(page)
  await expect(targets).toBeVisible()
  await expect(navigation).toBeVisible()
  await expect.poll(async () => (await targets.boundingBox())?.width ?? 0).toBeCloseTo(tw + 60, 0)
  await expect.poll(async () => (await navigation.boundingBox())?.width ?? 0).toBeCloseTo(nw + 60, 0)
})

test('wheel scrolling stays responsive with a text selection and select-all', async ({ page }) => {
 const panel=await openLogs(page,'api')
 const vp=panel.locator('[data-log-viewport]')
 await emit(page,'api',{lines:Array.from({length:1000},(_,i)=>`line ${i} lorem ipsum dolor sit amet`)})
 await expect(panel.getByText('line 999 lorem ipsum dolor sit amet',{exact:true})).toBeVisible()
 const box=(await vp.boundingBox())!
 await page.mouse.move(box.x+150,box.y+80)
 await page.mouse.wheel(0,-6000)
 await expect(panel.getByRole('button',{name:'Follow'})).toBeVisible()
 await page.mouse.move(box.x+150,box.y+80)
 const settle=()=>expect.poll(async()=>{const a=await vp.evaluate(el=>el.scrollTop);await page.waitForTimeout(100);return Math.abs(a-await vp.evaluate(el=>el.scrollTop))}).toBeLessThan(2)
 await settle()
 const baseline=await vp.evaluate(el=>el.scrollTop)
 await page.mouse.wheel(0,600)
 await expect.poll(()=>vp.evaluate(el=>el.scrollTop)).toBeGreaterThan(baseline+400)
 await settle()
 await page.mouse.move(box.x+50,box.y+60);await page.mouse.down();await page.mouse.move(box.x+180,box.y+115,{steps:8});await page.mouse.up()
 expect(await page.evaluate(()=>window.getSelection()?.toString().length ?? 0)).toBeGreaterThan(0)
 const copySelection=()=>page.evaluate(()=>{const data=new DataTransfer();document.activeElement!.dispatchEvent(new ClipboardEvent('copy',{bubbles:true,cancelable:true,clipboardData:data}));return data.getData('text/plain')})
 const selectedBefore=await copySelection()
 const selectedTop=await vp.evaluate(el=>el.scrollTop)
 await page.mouse.wheel(0,600)
 await expect.poll(()=>vp.evaluate(el=>el.scrollTop)).toBeGreaterThan(selectedTop+400)
 expect(await copySelection()).toBe(selectedBefore)
 await settle()
 await vp.focus();await page.keyboard.press('Control+a')
 const allTop=await vp.evaluate(el=>el.scrollTop)
 await page.mouse.wheel(0,600)
 await expect.poll(()=>vp.evaluate(el=>el.scrollTop)).toBeGreaterThan(allTop+400)
 expect((await copySelection()).split('\n').length).toBe(1006)
 await settle()
 await page.mouse.move(box.x+50,box.y+50);await page.mouse.down();await page.mouse.move(box.x+180,box.y+100,{steps:8})
 const dragTop=await vp.evaluate(el=>el.scrollTop)
 await page.mouse.wheel(0,600)
 await expect.poll(()=>vp.evaluate(el=>el.scrollTop)).toBeGreaterThan(dragTop+400)
 await page.mouse.up()
 await expect(page.locator('body')).not.toHaveClass(/log-select-drag/)
})

test('separate log window shares one stream and returns its settings and buffer', async ({ page, context }) => {
 const panel=await openLogs(page,'api')
 await expect(panel.getByLabel('line count')).toHaveText('6 lines')
 await emit(page,'api',{lines:['unique-before-detach']})
 const before=await stats(page)
 const popupPromise=context.waitForEvent('page')
 const detach=panel.getByRole('button',{name:'Open in window',exact:true})
 await expect(detach.locator('svg')).toHaveCount(1)
 await expect(detach).toHaveText('')
 const actions=panel.getByRole('group',{name:'Log actions',exact:true})
 expect((await detach.boundingBox())!.x).toBeGreaterThan((await actions.getByRole('button',{name:'Clear',exact:true}).boundingBox())!.x)
 await detach.click()
 const popup=await popupPromise
 await expect(popup.locator('[data-log-viewport]')).toContainText('unique-before-detach')
 expect((await stats(page)).streams).toBe(before.streams)
 await emit(page,'api',{lines:['unique-in-window']})
 await expect(popup.locator('[data-log-viewport]')).toContainText('unique-in-window')
 await popup.getByLabel('Search (Ctrl+F)').fill('unique')
 await popup.getByRole('button',{name:'Time',exact:true}).click()
 await popup.screenshot({path:join(scratchRoot,'logs-separate-window.png')})
 const back=popup.getByRole('button',{name:'Return to bottom panel',exact:true})
 await expect(back.locator('svg')).toHaveCount(1)
 await expect(back).toHaveText('')
 await back.click()
 await expect(panel.locator('[data-log-viewport]')).toContainText('unique-before-detach')
 await expect(panel.locator('[data-log-viewport]')).toContainText('unique-in-window')
 await expect(panel.getByLabel('Search (Ctrl+F)')).toHaveValue('unique')
 await expect(panel.getByRole('button',{name:'Time',exact:true})).toHaveAttribute('aria-pressed','true')
 expect((await stats(page)).streams).toBe(before.streams)
})

test('left panel resize keeps search geometry fixed and avoids log row updates during drag', async ({ page }) => {
 await page.setViewportSize({width:1600,height:1000})
 const panel=await openLogs(page,'api')
 await emit(page,'api',{lines:Array.from({length:20000},(_,i)=>`buffered line ${i}`)})
 await expect(panel.getByText('buffered line 19999',{exact:true})).toBeVisible()
 const field=page.locator('.target-filter')
 const icon=field.locator('svg');const input=field.locator('input')
 const originalIcon=(await icon.boundingBox())!;const originalInput=(await input.boundingBox())!
 const separator=page.getByRole('separator',{name:'Resize targets panel',exact:true})
 const box=(await separator.boundingBox())!
 await page.mouse.move(box.x+2,box.y+40);await page.mouse.down()
 await page.evaluate(()=>new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve()))))
 await panel.locator('[data-log-viewport]').evaluate(el=>{
  const probe={mutations:0,observer:new MutationObserver(list=>{probe.mutations+=list.length})}
  probe.observer.observe(el,{childList:true,subtree:true,characterData:true})
  ;(window as unknown as {resizeProbe:typeof probe}).resizeProbe=probe
 })
 for(const dx of [-40,-80,0,60,100]){
  await page.mouse.move(box.x+2+dx,box.y+40)
  await page.evaluate(()=>new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve()))))
  const currentIcon=(await icon.boundingBox())!,currentInput=(await input.boundingBox())!
  expect(currentIcon.x).toBeCloseTo(originalIcon.x,1);expect(currentIcon.width).toBeCloseTo(originalIcon.width,1)
  expect(currentInput.x).toBeCloseTo(originalInput.x,1)
  await emit(page,'api',{lines:[`queued during width drag ${dx}`]})
 }
 const mutations=await page.evaluate(()=>{const p=(window as unknown as {resizeProbe:{mutations:number;observer:MutationObserver}}).resizeProbe;p.observer.disconnect();return p.mutations})
 expect(mutations).toBe(0)
 await page.screenshot({path:join(scratchRoot,'left-panel-resize.png')})
 await page.mouse.up()
 await expect(panel.getByText('queued during width drag 100',{exact:true})).toBeVisible()
})

test('Download chooses a destination and exports uncolored text; the last row clears the scrollbar', async ({ page }) => {
 await page.addInitScript(()=>{
  ;(window as unknown as {showSaveFilePicker:()=>Promise<unknown>}).showSaveFilePicker=async()=>({name:'chosen.log',createWritable:async()=>({write:async(text:string)=>{const target=window as unknown as {downloadedLogs?:string};target.downloadedLogs=(target.downloadedLogs??'')+text},close:async()=>{},abort:async()=>{}})})
 })
 const panel=await openLogs(page,'api')
 await emit(page,'api',{lines:['\u001b[1;31mcolored download proof\u001b[0m',`last row ${'long text '.repeat(400)}`]})
 await expect(panel.getByText('colored download proof',{exact:true})).toBeVisible()
 const vp=panel.locator('[data-log-viewport]')
 await expect.poll(()=>vp.evaluate(el=>el.scrollWidth-el.clientWidth)).toBeGreaterThan(0)
 await expect.poll(async()=>{
  const last=panel.locator('[data-index]').last();const row=(await last.boundingBox())!,viewport=(await vp.boundingBox())!
  return viewport.y+viewport.height-row.y-row.height
 }).toBeGreaterThanOrEqual(20)
 await panel.getByRole('button',{name:'Download',exact:true}).click()
 await expect.poll(()=>page.evaluate(()=>(window as unknown as {downloadedLogs?:string}).downloadedLogs)).toContain('INFO api/main starting')
 expect(await page.evaluate(()=>(window as unknown as {downloadedLogs:string}).downloadedLogs)).toContain('INFO api/main ready')
 expect(await page.evaluate(()=>(window as unknown as {downloadedLogs:string}).downloadedLogs)).not.toContain('\u001b')
 await expect(panel).toContainText('Saved to chosen.log')
 await page.screenshot({path:join(scratchRoot,'logs-scrollbar-clearance.png')})
})


test('inspector and log actions use compact icons with hover/focus descriptions in all eight languages',async({page})=>{
 const panel=await openLogs(page,'api')
 const details=page.locator('[data-area=details]')
 const logs=details.getByRole('button',{name:'Logs',exact:true})
 await logs.hover()
 await expect(page.getByRole('tooltip')).toContainText('Right-click to choose Pod and container.')
 await logs.focus()
 await page.keyboard.press('Escape')
 await expect(page.getByRole('tooltip')).toHaveCount(0)
 await expect(details).toBeVisible()
 await expect(logs).toBeFocused()
 const copy=panel.getByRole('button',{name:'Copy',exact:true})
 await copy.focus()
 await expect(page.getByRole('tooltip')).toHaveText('Copy the shown lines (Ctrl+Shift+C)')
 const rect=(await page.getByRole('tooltip').boundingBox())!
 expect(rect.x).toBeGreaterThanOrEqual(0);expect(rect.x+rect.width).toBeLessThanOrEqual(1280)
 await page.keyboard.press('Escape')
 await panel.getByRole('button',{name:'Time',exact:true}).click()
 await expect(panel.getByRole('button',{name:'Time',exact:true})).toHaveAttribute('aria-pressed','true')
 try {
  for(const language of languages){
   await page.locator('.language-menu button').click()
   await page.getByRole('option',{name:languageNames[language],exact:true}).click()
   await expect(page.locator('html')).toHaveAttribute('lang',language)
   for(const width of [1280,960]){
    await page.setViewportSize({width,height:900})
    const icons=page.locator('.drawer-tools .action-icon-button, .log-toolbar .action-icon-button')
    expect(await icons.count()).toBeGreaterThanOrEqual(14)
    for(const icon of await icons.all()){
     await expect(icon).toHaveText('');await expect(icon.locator('svg')).toHaveCount(1)
     expect(await icon.getAttribute('aria-label')).toBeTruthy()
     expect(await icon.evaluate(el=>document.getElementById(el.getAttribute('aria-describedby')!)?.textContent)).toBeTruthy()
     const box=(await icon.boundingBox())!;expect(box.width).toBe(28);expect(box.height).toBe(28)
     await expect(icon).toBeInViewport({ratio:1})
    }
    const tools=details.locator('.drawer-tools')
    const ys=await tools.locator('button').evaluateAll(els=>els.map(el=>el.getBoundingClientRect().y))
    expect(new Set(ys).size).toBe(1)
    expect(await page.evaluate(()=>document.documentElement.scrollWidth)).toBe(width)
    const last=panel.locator('.log-toolbar .action-icon-button').last()
    await panel.locator('input').first().focus()
    await last.focus();await expect(page.getByRole('tooltip')).toBeVisible()
    const tip=(await page.getByRole('tooltip').boundingBox())!
    expect(tip.x).toBeGreaterThanOrEqual(0);expect(tip.x+tip.width).toBeLessThanOrEqual(width)
    await page.screenshot({path:join(scratchRoot,`icon-actions-${language}-${width}.png`)})
    await page.keyboard.press('Escape');await expect(page.getByRole('tooltip')).toHaveCount(0)
   }
  }
 } finally {
  await page.locator('.language-menu button').click()
  await page.getByRole('option',{name:languageNames.en,exact:true}).click()
 }
})


test('Download reads all retained history rather than viewport, loaded buffer or panel filters',async({page})=>{
 await page.addInitScript(()=>{
  ;(window as unknown as {showSaveFilePicker:()=>Promise<unknown>}).showSaveFilePicker=async()=>({name:'full-history.log',createWritable:async()=>({write:async(text:string)=>{const target=window as unknown as {historyText?:string};target.historyText=(target.historyText??'')+text},close:async()=>{(window as unknown as {historyCommitted:boolean}).historyCommitted=true},abort:async()=>{(window as unknown as {historyAborted:boolean}).historyAborted=true}})})
 })
 let historyReads=0
 await page.route('**/api/OpenLogHistory',async route=>{
  historyReads++;const request=route.request().postDataJSON()
  expect(request.channel).toBe('main');expect(request.previous).toBe(false)
  expect(Object.keys(request).sort()).toEqual(['channel','previous','ref'])
  expect(request.ref.uid).toBeTruthy()
  await route.fulfill({json:{streamId:'full-history-fixture'}})
 })
 await page.route('**/logs/full-history-fixture',route=>{
  const source={k:'source',id:1,key:'history-uid/main',label:'api/main'}
  const lines=Array.from({length:4000},(_,i)=>['2026-10-11T01:00:00Z',`archive line ${i}`])
  return route.fulfill({contentType:'application/x-ndjson',body:[source,{k:'lines',s:1,l:lines},{k:'end',reason:'done'}].map(f=>JSON.stringify(f)+'\n').join('')})
 })
 const panel=await openLogs(page,'api')
 await expect(panel.getByLabel('line count')).toHaveText('6 lines')
 await panel.getByLabel('Filter (*)',{exact:true}).fill('DOES_NOT_MATCH_HISTORY')
 await panel.getByRole('button',{name:'Download',exact:true}).click()
 await expect.poll(()=>page.evaluate(()=>(window as unknown as {historyCommitted?:boolean}).historyCommitted)).toBe(true)
 const text=await page.evaluate(()=>(window as unknown as {historyText:string}).historyText)
 expect(text.split('\n').filter(Boolean)).toHaveLength(4000);expect(text).toContain('archive line 0\n');expect(text).toContain('archive line 3999\n')
 expect(historyReads).toBe(1);await expect(panel).toContainText('Saved to full-history.log')
 await page.screenshot({path:join(scratchRoot,'history-export-all-lines.png')})
})

test('Incomplete history aborts its chosen destination without committing a partial file',async({page})=>{
 await page.addInitScript(()=>{
  ;(window as unknown as {showSaveFilePicker:()=>Promise<unknown>}).showSaveFilePicker=async()=>({name:'existing.log',createWritable:async()=>({write:async()=>{},close:async()=>{(window as unknown as {committed:boolean}).committed=true},abort:async()=>{(window as unknown as {aborted:boolean}).aborted=true}})})
 })
 await page.route('**/api/OpenLogHistory',route=>route.fulfill({json:{streamId:'incomplete-fixture'}}))
 await page.route('**/logs/incomplete-fixture',route=>route.fulfill({contentType:'application/x-ndjson',body:[{k:'source',id:1,key:'history-uid/main',label:'api/main'},{k:'lines',s:1,l:[['','partial prefix']]},{k:'state',s:1,state:'truncated',msg:'source history incomplete'},{k:'end',reason:'done'}].map(f=>JSON.stringify(f)+'\n').join('')}))
 const panel=await openLogs(page,'api');await panel.getByRole('button',{name:'Download',exact:true}).click()
 await expect.poll(()=>page.evaluate(()=>(window as unknown as {aborted?:boolean}).aborted)).toBe(true)
 expect(await page.evaluate(()=>(window as unknown as {committed?:boolean}).committed)).not.toBe(true)
 await expect(panel).toContainText('source history incomplete')
 await page.screenshot({path:join(scratchRoot,'history-export-incomplete.png')})
})

test('Cancelling a pending history download aborts its destination and ignores a late reply',async({page})=>{
 await page.addInitScript(()=>{
  ;(window as unknown as {showSaveFilePicker:()=>Promise<unknown>}).showSaveFilePicker=async()=>({name:'pending.log',createWritable:async()=>({write:async()=>{},close:async()=>{(window as unknown as {committed:boolean}).committed=true},abort:async()=>{(window as unknown as {aborted:boolean}).aborted=true}})})
 })
 await page.route('**/api/OpenLogHistory',route=>route.fulfill({json:{streamId:'pending-history'}}))
 let release!:()=>void
 const paused=new Promise<void>(resolve=>{release=resolve})
 let started=false
 await page.route('**/logs/pending-history',async route=>{
  started=true;await paused
  try{await route.fulfill({contentType:'application/x-ndjson',body:'{"k":"source","id":1,"key":"uid/main","label":"api/main"}\n{"k":"end","reason":"done"}\n'})}catch{/* The cancelled fetch may already be gone. */}
 })
 const panel=await openLogs(page,'api');await panel.getByRole('button',{name:'Download',exact:true}).click()
 await expect.poll(()=>started).toBe(true)
 await panel.getByRole('button',{name:'Cancel log download',exact:true}).click()
 await expect.poll(()=>page.evaluate(()=>(window as unknown as {aborted?:boolean}).aborted)).toBe(true)
 release();await expect(panel).toContainText('Log download cancelled')
 expect(await page.evaluate(()=>(window as unknown as {committed?:boolean}).committed)).not.toBe(true)
 await expect(panel.getByRole('button',{name:'Download',exact:true})).toBeEnabled()
})
