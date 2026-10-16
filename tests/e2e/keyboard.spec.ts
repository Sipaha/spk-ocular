import {languages,languageNames} from '../../web/src/languages'
import {openAppMenu} from './fixtures'
import { selectConnection } from './fixtures'
import { connectSelected } from './fixtures'
import { expect } from '@playwright/test'
import { test } from './fixtures'
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import { scratchRoot } from './fixtures'

test('modal palette covers overflowing navigation and resize handles',async({page})=>{
 // At this width the navigation grip lies underneath the centred palette.
 await page.setViewportSize({width:960,height:900})
 await page.route('**/api/ListKinds',async route=>{
  const response=await route.fetch(),body=await response.json()
  const kind=body.kinds.find((item:{id:string})=>item.id==='services')
  body.kinds.push(...Array.from({length:80},(_,i)=>({...kind,id:`overflow-${i}`,title:`Overflow ${i}`,singular:`Overflow ${i}`})))
  await route.fulfill({response,json:body})
 })
 await page.goto('/')
 await selectConnection(page,/^demo\b/);await connectSelected(page)
 const nav=page.locator('[data-resource-nav-list]')
 await expect.poll(()=>nav.evaluate(el=>el.scrollHeight-el.clientHeight)).toBeGreaterThan(500)
 await nav.hover();await page.mouse.wheel(0,300)
 await page.keyboard.press('Control+k')
 const dialog=page.getByRole('dialog',{name:'Go to'})
 await expect(dialog).toBeVisible()
 expect(await nav.evaluate(el=>getComputedStyle(el).scrollbarWidth)).toBe('auto')
 const rect=(await dialog.boundingBox())!,handle=(await page.getByRole('separator',{name:'Resize resource navigation',exact:true}).boundingBox())!
 expect(handle.x).toBeGreaterThan(rect.x);expect(handle.x).toBeLessThan(rect.x+rect.width)
 const point={x:handle.x+handle.width/2,y:rect.y+Math.min(100,rect.height/2)}
 expect(await page.evaluate(({x,y})=>!!document.elementFromPoint(x,y)?.closest('.palette-dialog'),point)).toBe(true)
 await page.screenshot({path:join(scratchRoot,'palette-overflow-divider.png')})
 await page.keyboard.press('Escape')
})

test('help close is reachable by Tab and Shift+Tab and restores its opener', async ({ page }) => {
  await page.goto('/')
  await openAppMenu(page)
  const opener = page.getByRole('button', { name: 'Keyboard ?', exact: true })
  await opener.click()
  const dialog = page.getByRole('dialog', { name: 'Keyboard' })
  const close = dialog.getByRole('button', { name: 'Close', exact: true })
  await expect(dialog).toBeFocused()
  await page.keyboard.press('Tab')
  await expect(close).toBeFocused()
  await page.keyboard.press('Shift+Tab')
  await expect(close).toBeFocused()
  const captures = join(scratchRoot, 'audit-screenshots')
  mkdirSync(captures, { recursive: true })
  await page.screenshot({ path: join(captures, 'help-focused-close.png') })
  await page.keyboard.press('Enter')
  await expect(dialog).toBeHidden()
  await expect(page.locator('[data-app-menu]')).toBeFocused()
})

// The keyboard against the synthetic provider: areas (F6), views by arrows,
// the help (?), a table page, and the app-wide palette beside a terminal.

test('F6 goes round the areas; views by arrows; ? shows the keys', async ({ page }) => {
  await page.goto('/')
  await selectConnection(page,/^demo\b/)
  await connectSelected(page)
  const nav = page.getByRole('navigation', { name: 'resources' })
  await nav.getByRole('button', { name: 'Services', exact: true }).click()
  await expect(page.getByRole('grid', { name: 'resources' }).getByRole('gridcell', { name: 'api', exact: true })).toBeVisible()

  await page.locator('body').click({ position: { x: 5, y: 5 } })
  await page.keyboard.press('F6')
  await expect(page.locator('[data-connection-switcher]')).toBeFocused()
  await page.keyboard.press('F6')
  await expect(nav.getByRole('button', { name: 'Services', exact: true })).toBeFocused()
  await page.keyboard.press('ArrowDown')
  await expect(nav.getByRole('button', { name: 'Workloads', exact: true })).toBeFocused()
  await page.keyboard.press('Enter')
  await expect(page.getByRole('heading', { name: 'Workloads' })).toBeVisible()
  await expect(page.getByRole('grid', { name: 'resources' }).getByRole('gridcell', { name: 'web', exact: true })).toBeVisible()
  await page.keyboard.press('F6')
  await expect(page.locator('[data-table-scroll]').first()).toBeFocused()
  await page.keyboard.press('End')
  await expect(page.getByRole('row', { selected: true })).toContainText('web')

  await page.keyboard.press('Shift+Slash')
  const help = page.getByRole('dialog', { name: 'Keyboard' })
  await expect(help.getByRole('region', { name: 'Anywhere' })).toContainText('Ctrl+K')
  await help.dispatchEvent('keydown',{key:',',code:'Slash',shiftKey:true,bubbles:true})
  await expect(help).toBeHidden()
  await expect(page.locator('[data-table-scroll]').first()).toBeFocused()
})

test('the app header opens the palette while a terminal keeps Ctrl+K', async ({ page }) => {
  await page.goto('/')
  await selectConnection(page,/^demo\b/)
  await connectSelected(page)
  await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Services', exact: true }).click()
  const grid = page.getByRole('grid', { name: 'resources' })
  await grid.getByRole('gridcell', { name: 'api', exact: true }).click()
  await page.getByRole('dialog', { name: 'services api' }).getByRole('button', { name: 'Close' }).click()
  await page.keyboard.press('KeyS')
  const panel = page.getByRole('region', { name: 'Bottom panel' })
  await expect(panel.locator('.xterm-rows')).toContainText('synthetic terminal on api')
  await page.keyboard.press('Control+k')
  await expect(page.getByRole('dialog', { name: 'Go to' })).toHaveCount(0)
  await expect(panel.getByRole('button', { name: 'Go to (Ctrl+K)' })).toHaveCount(0)
  await page.getByRole('button', { name: 'Go to (Ctrl+K)' }).click()
  await expect(page.getByRole('dialog', { name: 'Go to' })).toBeVisible()
})

test('workspace header and grouped log controls stay reachable in a narrow window', async ({ page }) => {
  await page.setViewportSize({ width: 960, height: 720 })
  await page.goto('/')
  await selectConnection(page,/^demo\b/)
  await connectSelected(page)
  await expect(page.locator('[data-connection-switcher]')).toContainText('demo')

  const go = page.getByRole('button', { name: 'Go to (Ctrl+K)' })
  await go.click()
  await expect(page.getByRole('dialog', { name: 'Go to' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(go).toBeFocused()
  await openAppMenu(page)
  const help = page.getByRole('button', { name: 'Keyboard ?', exact: true })
  await help.click()
  await expect(page.getByRole('dialog', { name: 'Keyboard' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.locator('[data-app-menu]')).toBeFocused()

  await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Services', exact: true }).click()
  await page.getByRole('gridcell', { name: 'api', exact: true }).click()
  await page.getByRole('dialog', { name: 'services api' }).getByRole('button', { name: 'Logs', exact: true }).click()
  const panel = page.getByRole('region', { name: 'Bottom panel' })
  await expect(page.locator('.status-bar').getByLabel('line count')).toHaveText('6 lines')
  for (const label of ['Log source', 'Search in logs', 'Filter logs', 'Log display', 'Log actions']) {
    const group = panel.getByRole('group', { name: label, exact: true })
    await expect(group).toBeInViewport()
    for (const control of await group.locator('button, input').all()) await expect(control).toBeInViewport({ ratio: 1 })
  }
  await expect(page.getByRole('textbox', { name: 'Filter rows', exact: true })).toBeInViewport({ ratio: 1 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(960)
  await panel.getByLabel('Search (Ctrl+F)').fill('starting')
  await expect(panel.getByLabel('matches')).toHaveText('1/1')
  await panel.getByRole('button', { name: 'Wrap', exact: true }).click()
  await expect(panel.getByRole('button', { name: 'Wrap', exact: true })).toHaveAttribute('aria-pressed', 'true')
})

test('Alt+Shift arrows resize the focused navigation, details and dock without moving focus or drafts',async({page})=>{
 await page.route('**/api/ListKinds',async route=>{
  const response=await route.fetch()
  if(!response.ok()){await route.fulfill({response});return}
  const data=await response.json()
  for(const kind of data.kinds)if(kind.id==='crates')kind.editable=true
  await route.fulfill({response,json:data})
 })
 await page.route('**/api/GetEditSource',route=>route.fulfill({json:{ref:route.request().postDataJSON(),text:'key: crate-7f3a\ntitle: alpha\n',base:'keyboard-resize-fixture'}}))
 await page.setViewportSize({width:1440,height:1000})
 await page.goto('/');await selectConnection(page,/^demo\b/);await connectSelected(page)
 const nav=page.getByRole('navigation',{name:'resources'}),filter=nav.getByRole('textbox',{name:'Filter resources'})
 const navGrip=page.getByRole('separator',{name:'Resize resource navigation',exact:true})
 await filter.focus()
 const initial=Number(await navGrip.getAttribute('aria-valuenow'))
 await page.keyboard.press('Alt+Shift+ArrowRight')
 await expect(navGrip).toHaveAttribute('aria-valuenow',String(initial+20));await expect(filter).toBeFocused()
 await page.keyboard.press('Alt+Shift+ArrowLeft')
 await expect(navGrip).toHaveAttribute('aria-valuenow',String(initial))
 await filter.fill('word one two');await page.keyboard.press('Control+Shift+ArrowLeft')
 await expect(navGrip).toHaveAttribute('aria-valuenow',String(initial));await expect(filter).toHaveValue('word one two')
 await filter.fill('');await nav.getByRole('button',{name:'Services',exact:true}).click()
 await page.getByRole('grid',{name:'resources'}).getByRole('gridcell',{name:'api',exact:true}).click()
 await page.keyboard.press('l')
 const viewport=page.locator('[role=tabpanel]:not([hidden]) [data-log-viewport]')
 await expect(viewport).toBeFocused()
 await nav.getByRole('button',{name:'Crates',exact:true}).click()
 await page.getByRole('grid',{name:'resources'}).getByRole('gridcell',{name:'alpha',exact:true}).click()
 const details=page.locator('[data-area=details]'),detailsGrip=details.getByRole('separator',{name:'Resize details panel',exact:true})
 await details.getByRole('tab',{name:'YAML',exact:true}).click()
 await details.getByRole('button',{name:'Edit',exact:true}).click()
 const editor=details.locator('.cm-content')
 await editor.click();await page.keyboard.press('Control+End');await page.keyboard.type('\n# keep this draft')
 const width=(await details.boundingBox())!.width
 await page.keyboard.press('Alt+Shift+ArrowLeft')
 await expect.poll(async()=>(await details.boundingBox())!.width).toBe(width+20)
 await expect(editor).toContainText('keep this draft');await expect(editor).toBeFocused()
 await page.keyboard.press('Alt+Shift+ArrowRight')
 await expect.poll(async()=>(await details.boundingBox())!.width).toBe(width)
 // An open modal owns its keys; the details behind it must stay fixed.
 await page.keyboard.press('Control+k');await expect(page.getByRole('dialog',{name:'Go to',exact:true})).toBeVisible()
 await page.keyboard.press('Alt+Shift+ArrowLeft')
 expect((await details.boundingBox())!.width).toBe(width)
 await page.keyboard.press('Escape')
 await viewport.focus()
 await expect(viewport).toBeFocused()
 const dock=page.locator('[data-area=dock]'),height=(await dock.boundingBox())!.height
 await page.keyboard.press('Alt+Shift+ArrowUp')
 await expect.poll(async()=>(await dock.boundingBox())!.height).toBe(height+20)
 await expect(viewport).toBeFocused()
 await page.keyboard.press('Alt+Shift+ArrowDown')
 await expect.poll(async()=>(await dock.boundingBox())!.height).toBe(height)
 await expect(editor).toContainText('keep this draft')
 await page.screenshot({path:join(scratchRoot,'keyboard-panel-resize.png')})
 // Existing separator keys still work and retain their limits.
 await detailsGrip.focus();await detailsGrip.press('Home')
 await expect(detailsGrip).toHaveAttribute('aria-valuenow','280')
})


test('panel reorganization shortcuts are explained in all eight languages',async({page})=>{
 await page.setViewportSize({width:1280,height:900});await page.goto('/')
 for(const language of languages){
  await openAppMenu(page);await page.locator('.language-menu button').click()
  await page.getByRole('option',{name:languageNames[language],exact:true}).click()
  await expect(page.locator('html')).toHaveAttribute('lang',language)
  await expect(page.locator('[data-app-menu]')).toHaveAttribute('aria-expanded','false')
  await openAppMenu(page);await page.locator('.app-utilities > button').last().click()
  const dialog=page.getByRole('dialog').filter({has:page.getByText('Alt+Shift+Enter',{exact:true})})
  await expect(dialog).toBeVisible()
  await expect(dialog.getByText('Alt+Shift+←',{exact:true})).toBeVisible()
  await expect(dialog).not.toContainText('keys.resizePanel')
  await expect(dialog).not.toContainText('keys.logWindow')
  await page.screenshot({path:join(scratchRoot,`keyboard-panel-help-${language}.png`)})
  await page.keyboard.press('Escape');await expect(dialog).toHaveCount(0)
 }
 await openAppMenu(page);await page.locator('.language-menu button').click()
 await page.getByRole('option',{name:languageNames.en,exact:true}).click()
 await expect(page.locator('html')).toHaveAttribute('lang','en')
})
