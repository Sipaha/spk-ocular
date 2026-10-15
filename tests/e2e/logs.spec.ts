import { selectConnection, openAppMenu } from './fixtures'
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
  await selectConnection(page,/^demo\b/)
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
  const navigation = page.locator('[data-area="nav"]')
  const details = page.locator('[data-area="details"]')
  const drag = async (label: string, dx: number, dy: number) => {
    const box = (await page.getByRole('separator', { name: label, exact: true }).boundingBox())!
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2)
    await page.mouse.down()
    await page.mouse.move(box.x + box.width / 2 + dx, box.y + box.height / 2 + dy, { steps: 12 })
    await page.mouse.up()
  }
  await expect(navigation).toBeVisible()
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
  await selectConnection(page,/^demo2\b/)
  await connectSelected(page)
  await expect(navigation).toBeVisible()
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

test('resource navigation resize keeps search geometry fixed and avoids log row updates during drag', async ({ page }) => {
 await page.setViewportSize({width:1600,height:1000})
 const panel=await openLogs(page,'api')
 await emit(page,'api',{lines:Array.from({length:20000},(_,i)=>`buffered line ${i}`)})
 await expect(panel.getByText('buffered line 19999',{exact:true})).toBeVisible()
 const field=page.locator('.resource-nav-filter')
 const icon=field.locator('svg');const input=field.locator('input')
 const originalIcon=(await icon.boundingBox())!;const originalInput=(await input.boundingBox())!
 const separator=page.getByRole('separator',{name:'Resize resource navigation',exact:true})
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
   await openAppMenu(page);await page.locator('.language-menu button').click()
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
  await openAppMenu(page);await page.locator('.language-menu button').click()
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

for (const wrap of [false, true]) for (const following of [true, false]) {
 test(`bottom dock resize preserves painted log rows (${wrap?'wrapped':'fixed'}, ${following?'following':'history'})`, async ({page})=>{
  await page.setViewportSize({width:1400,height:1000})
  const panel=await openLogs(page,'api'),vp=panel.locator('[data-log-viewport]')
  if(wrap) await panel.getByRole('button',{name:'Wrap',exact:true}).click()
  await emit(page,'api',{lines:Array.from({length:400},(_,i)=>`resize row ${i} ${'content '.repeat(wrap?30:2)}`)})
  await expect(rows(panel).last()).toContainText('resize row 399')
  if(!following){await vp.hover();await page.mouse.wheel(0,-3000);await expect(panel.getByRole('button',{name:'Follow',exact:true})).toBeVisible()}
  // Pick a row near the fixed bottom, which remains inside the shrinking panel.
  const anchor=await vp.evaluate(el=>{
   const b=el.getBoundingClientRect();const rs=Array.from(el.querySelectorAll<HTMLElement>('[data-index]'))
   const row=rs.filter(r=>{const p=r.getBoundingClientRect();return p.top>b.top+50&&p.bottom<b.bottom-35}).at(-1)!
   return {index:row.dataset.index!,y:row.getBoundingClientRect().top}
  })
  await vp.evaluate((el,a)=>{
   const samples:{y:number|null;top:number;height:number;scroll:number}[]=[]
   const probe={samples,observer:null as ResizeObserver|null};Object.assign(window,{logResizeProbe:probe})
   const sample=()=>{const r=el.querySelector<HTMLElement>(`[data-index="${a.index}"]`);samples.push({y:r?.getBoundingClientRect().top??null,top:el.getBoundingClientRect().top,height:el.clientHeight,scroll:el.scrollTop});}
   probe.observer=new ResizeObserver(sample);probe.observer.observe(el)
  },anchor)
  const sep=page.getByRole('separator',{name:'Resize the bottom panel',exact:true}),b=(await sep.boundingBox())!
  await page.mouse.move(b.x+80,b.y+2);await page.mouse.down()
  for(let i=1;i<=12;i++){
   await page.mouse.move(b.x+80,b.y+2+i*7)
   await page.evaluate(()=>new Promise<void>(r=>requestAnimationFrame(()=>requestAnimationFrame(()=>r()))))
  }
  // Grow back through the same geometry, then cancel a second drag. Both
  // directions and Escape must preserve the row, not only the settled size.
  for(let i=11;i>=0;i--){
   await page.mouse.move(b.x+80,b.y+2+i*7)
   await page.evaluate(()=>new Promise<void>(r=>requestAnimationFrame(()=>requestAnimationFrame(()=>r()))))
  }
  await page.mouse.up()
  await page.mouse.down();await page.mouse.move(b.x+80,b.y+44)
  await page.evaluate(()=>new Promise<void>(r=>requestAnimationFrame(()=>requestAnimationFrame(()=>r()))))
  await page.keyboard.press('Escape');await page.mouse.up()
  expect((await sep.boundingBox())!.y).toBeCloseTo(b.y,0)
  await page.evaluate(()=>new Promise<void>(r=>requestAnimationFrame(()=>requestAnimationFrame(()=>r()))))
  const samples=await page.evaluate(()=>{const p=(window as unknown as {logResizeProbe:{samples:{y:number|null;top:number;height:number;scroll:number}[];observer:ResizeObserver}}).logResizeProbe;p.observer.disconnect();return p.samples})
  console.log('painted resize geometry',JSON.stringify({wrap,following,anchor,samples}))
  await page.screenshot({path:join(scratchRoot,`resize-${wrap?'wrap':'fixed'}-${following?'follow':'history'}.png`)})
  expect(samples.length).toBeGreaterThanOrEqual(13)
  expect(samples.every(s=>s.y!==null)).toBe(true)
  expect(Math.max(...samples.map(s=>Math.abs(s.y!-anchor.y)))).toBeLessThanOrEqual(1)
  await expect(panel.getByRole('button',{name:'Follow',exact:true})).toHaveCount(following?0:1)
 })
}

test('hidden history tab keeps its offset across dock resizing and clamps at history bounds',async({page})=>{
 await page.setViewportSize({width:1400,height:1000})
 const panel=await openLogs(page,'api'),vp=panel.locator('[data-log-viewport]')
 await emit(page,'api',{lines:Array.from({length:400},(_,i)=>`history boundary ${i}`)})
 await expect(rows(panel).last()).toHaveText('history boundary 399')
 await vp.hover();await page.mouse.wheel(0,-2000)
 await expect(panel.getByRole('button',{name:'Follow',exact:true})).toBeVisible()
 const before=await vp.evaluate(el=>el.scrollTop)
 await page.getByRole('grid',{name:'resources',exact:true}).getByRole('gridcell',{name:'workers',exact:true}).click()
 await page.keyboard.press('l')
 await expect(page.getByRole('tab',{name:/services\/workers/})).toHaveAttribute('aria-selected','true')
 const sep=page.getByRole('separator',{name:'Resize the bottom panel',exact:true})
 await sep.press('ArrowDown')
 await page.getByRole('tab',{name:/services\/api/}).click()
 const restored=page.locator('[role=tabpanel]:not([hidden])'),restoredVP=restored.locator('[data-log-viewport]')
 await expect(restoredVP).toBeVisible()
 await expect.poll(()=>restoredVP.evaluate(el=>el.scrollTop)).toBe(before)
 await expect(restored.getByRole('button',{name:'Follow',exact:true})).toBeVisible()
 // At the beginning there is no older content to expose when growing.
 await restoredVP.hover();await page.mouse.wheel(0,-20000)
 await expect.poll(()=>restoredVP.evaluate(el=>el.scrollTop)).toBe(0)
 await sep.press('ArrowUp')
 await expect.poll(()=>restoredVP.evaluate(el=>el.scrollTop)).toBe(0)
 await expect(restored.getByRole('button',{name:'Follow',exact:true})).toBeVisible()
 await restoredVP.focus();await page.keyboard.press('End')
 await expect(restored.getByRole('button',{name:'Follow',exact:true})).toHaveCount(0)
 await expect(rows(restored).last()).toHaveText('history boundary 399')
})

test('wrap changes during resize do not leave scrollbar scrolling ignored',async({page})=>{
 const panel=await openLogs(page,'api'),vp=panel.locator('[data-log-viewport]')
 await emit(page,'api',{lines:Array.from({length:400},(_,i)=>`wrap gate ${i}`)})
 await expect(rows(panel).last()).toHaveText('wrap gate 399')
 // Switch wrap in the same observer delivery as the resize correction, before
 // its two-frame programmatic-scroll guard has had a chance to clear.
 await vp.evaluate(el=>{
  const height=el.clientHeight
  const observer=new ResizeObserver(()=>{
   if(el.clientHeight===height)return
   observer.disconnect()
   const panel=el.closest('[role=tabpanel]')!
   ;(panel.querySelector('[aria-label="Wrap"]') as HTMLButtonElement).click()
  })
  observer.observe(el)
 })
 await page.getByRole('separator',{name:'Resize the bottom panel',exact:true}).press('ArrowDown')
 await expect(panel.getByRole('button',{name:'Wrap',exact:true})).toHaveAttribute('aria-pressed','true')
 await vp.focus();await page.keyboard.press('PageUp')
 await expect(panel.getByRole('button',{name:'Follow',exact:true})).toBeVisible()
})

test('changing wrap retains the live tail before the next dock resize',async({page})=>{
 const panel=await openLogs(page,'api'),vp=panel.locator('[data-log-viewport]')
 const tail=`wrap tail 399 ${'message '.repeat(50)}`
 await emit(page,'api',{lines:Array.from({length:400},(_,i)=>`wrap tail ${i} ${'message '.repeat(50)}`)})
 await expect(panel.getByText(tail,{exact:true})).toBeInViewport()
 await panel.getByRole('button',{name:'Wrap',exact:true}).click()
 await expect(panel.getByText(tail,{exact:true})).toBeInViewport()
 await expect(panel.getByRole('button',{name:'Follow',exact:true})).toHaveCount(0)
 await panel.getByRole('button',{name:'Wrap',exact:true}).click()
 await expect(panel.getByText(tail,{exact:true})).toBeInViewport()
 expect(await vp.evaluate(el=>el.scrollHeight-el.scrollTop-el.clientHeight)).toBeLessThanOrEqual(1)
})

for (const initiallyWrapped of [false, true]) {
 test(`wrap toggles preserve paused history and its logical row (${initiallyWrapped?'wrapped':'fixed'} first)`,async({page})=>{
  await page.setViewportSize({width:1400,height:1000})
  const panel=await openLogs(page,'api'),vp=panel.locator('[data-log-viewport]')
  const wrap=panel.getByRole('button',{name:'Wrap',exact:true}),follow=panel.getByRole('button',{name:'Follow',exact:true})
  if(initiallyWrapped)await wrap.click()
  await emit(page,'api',{lines:Array.from({length:400},(_,i)=>`wrap history ${i} ${'message '.repeat(50)}`)})
  await expect(panel.getByText(`wrap history 399 ${'message '.repeat(50)}`,{exact:true})).toBeInViewport()
  await vp.hover();await page.mouse.wheel(0,-3000)
  await expect(follow).toBeVisible()
  const anchor=async()=>vp.evaluate(el=>{
   const top=el.getBoundingClientRect().top+parseFloat(getComputedStyle(el).paddingTop)
   const row=Array.from(el.querySelectorAll<HTMLElement>('[data-index]')).find(r=>r.getBoundingClientRect().bottom>top)!
   return {index:row.dataset.index!,inset:Math.max(0,top-row.getBoundingClientRect().top),height:row.getBoundingClientRect().height}
  })
  for(let toggle=0;toggle<2;toggle++){
   const before=await anchor()
   await wrap.click()
   await expect(follow).toBeVisible()
   await expect.poll(async()=>(await anchor()).index).toBe(before.index)
   const after=await anchor()
   expect(after.inset).toBeLessThanOrEqual(Math.min(before.inset,after.height-1)+1)
   await emit(page,'api',{lines:[`history stays paused ${toggle}`]})
   await expect(follow).toBeVisible()
   expect((await anchor()).index).toBe(before.index)
   await page.screenshot({path:join(scratchRoot,`wrap-history-${initiallyWrapped?'wrapped':'fixed'}-${toggle}.png`)})
  }
  await page.getByRole('separator',{name:'Resize the bottom panel',exact:true}).press('ArrowDown')
  await expect(follow).toBeVisible()
  await vp.focus();await page.keyboard.press('End')
  await expect(follow).toHaveCount(0)
  await expect(panel.getByText('history stays paused 1',{exact:true})).toBeInViewport()
 })
}

for(const wrapped of [false,true]){
 test(`live Follow keeps one painted tail gap (${wrapped?'wrapped':'fixed'})`,async({page})=>{
  await page.setViewportSize({width:1400,height:1000})
  const panel=await openLogs(page,'api'),vp=panel.locator('[data-log-viewport]')
  if(wrapped)await panel.getByRole('button',{name:'Wrap',exact:true}).click()
  const line=(i:number)=>`painted live tail ${i} ${'content '.repeat(wrapped?40:2)}`
  await emit(page,'api',{lines:Array.from({length:400},(_,i)=>line(i))})
  await expect(panel.getByText(line(399),{exact:true})).toBeInViewport()
  await page.evaluate(()=>new Promise<void>(r=>requestAnimationFrame(()=>requestAnimationFrame(()=>r()))))
  await vp.evaluate(el=>{
   const probe={gaps:[] as number[],frame:0,observer:null as ResizeObserver|null}
   const sample=()=>{
    const row=el.querySelector('[data-log-content]')?.lastElementChild
    if(row)probe.gaps.push(el.getBoundingClientRect().bottom-row.getBoundingClientRect().bottom)
   }
   const frame=()=>{sample();probe.frame=requestAnimationFrame(frame)}
   probe.frame=requestAnimationFrame(frame)
   probe.observer=new ResizeObserver(sample);probe.observer.observe(el.querySelector('[data-log-content]')!)
   Object.assign(window,{liveTailPaintProbe:probe})
  })
  for(let i=400;i<412;i++){
   await emit(page,'api',{lines:[line(i)]})
   await expect(panel.getByText(line(i),{exact:true})).toBeInViewport()
   await page.evaluate(()=>new Promise<void>(r=>requestAnimationFrame(()=>requestAnimationFrame(()=>r()))))
  }
  const gaps=await page.evaluate(()=>{
   const p=(window as unknown as {liveTailPaintProbe:{gaps:number[];frame:number;observer:ResizeObserver}}).liveTailPaintProbe
   cancelAnimationFrame(p.frame);p.observer.disconnect();return p.gaps
  })
  console.log('painted live tail gaps',JSON.stringify({wrapped,gaps}))
  expect(gaps.length).toBeGreaterThan(20)
  expect(Math.max(...gaps)-Math.min(...gaps)).toBeLessThanOrEqual(1)
  await expect(panel.getByRole('button',{name:'Follow',exact:true})).toHaveCount(0)
 })
}

for(const following of [true,false]){
 test(`unwrapped streaming resize keeps every painted row at fractional steps (${following?'Follow':'history'})`,async({page})=>{
  await page.setViewportSize({width:1280,height:820})
  const panel=await openLogs(page,'api'),vp=panel.locator('[data-log-viewport]')
  await emit(page,'api',{lines:Array.from({length:400},(_,i)=>`fractional resize ${i} ${'content '.repeat(20)}`)})
  await expect(rows(panel).last()).toContainText('fractional resize 399')
  if(!following){await vp.hover();await page.mouse.wheel(0,-2000);await expect(panel.getByRole('button',{name:'Follow',exact:true})).toBeVisible()}
  const anchor=await vp.evaluate(el=>{
   const b=el.getBoundingClientRect(),rs=Array.from(el.querySelectorAll<HTMLElement>('[data-index]'))
   const row=rs.filter(r=>r.getBoundingClientRect().top>b.top+50&&r.getBoundingClientRect().bottom<b.bottom-35).at(-1)!
   return {index:row.dataset.index!,y:row.getBoundingClientRect().top}
  })
  await vp.evaluate((el,a)=>{
   const probe={samples:[] as (number|null)[],frame:0,observer:null as ResizeObserver|null}
   const sample=()=>probe.samples.push(el.querySelector(`[data-index="${a.index}"]`)?.getBoundingClientRect().top??null)
   const frame=()=>{sample();probe.frame=requestAnimationFrame(frame)}
   probe.frame=requestAnimationFrame(frame);probe.observer=new ResizeObserver(sample);probe.observer.observe(el)
   Object.assign(window,{fractionalResizeProbe:probe})
  },anchor)
  const sep=page.getByRole('separator',{name:'Resize the bottom panel',exact:true}),b=(await sep.boundingBox())!
  await page.mouse.move(b.x+60,b.y+2);await page.mouse.down()
  for(let i=1;i<=24;i++){
   await page.mouse.move(b.x+60,b.y+2+i*1.25)
   await emit(page,'api',{lines:[`pending during fractional drag ${i}`]})
   await page.evaluate(()=>new Promise<void>(r=>requestAnimationFrame(()=>requestAnimationFrame(()=>r()))))
  }
  const samples=await page.evaluate(()=>{
   const p=(window as unknown as {fractionalResizeProbe:{samples:(number|null)[];frame:number;observer:ResizeObserver}}).fractionalResizeProbe
   cancelAnimationFrame(p.frame);p.observer.disconnect();return p.samples
  })
  await page.mouse.up()
  console.log('fractional streaming resize',JSON.stringify({following,anchor,samples}))
  expect(samples.length).toBeGreaterThan(40)
  expect(samples.every(y=>y!==null)).toBe(true)
  expect(Math.max(...samples.map(y=>Math.abs(y!-anchor.y)))).toBeLessThanOrEqual(1)
  await expect(panel.getByRole('button',{name:'Follow',exact:true})).toHaveCount(following?0:1)
 })
}
