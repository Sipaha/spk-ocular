import {openAppMenu} from './fixtures'
import { selectConnection } from './fixtures'
import { connectSelected } from './fixtures'
import { expect, type Page } from '@playwright/test'
import { test, scratchRoot } from './fixtures'
import { join } from 'node:path'

// The Ctrl+K palette against the synthetic provider: views by alias, rows of
// the current table, recent objects (persisted in SQLite), a context switch.

// Live GetRows handlers must settle before Playwright disposes their responses.
test.afterEach(async ({ page }) => { await page.unrouteAll({ behavior: 'wait' }) })

async function openDemo(page: Page) {
  await page.goto('/')
  await selectConnection(page,/^demo\b/)
  await connectSelected(page)
  await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Services', exact: true }).click()
  await expect(page.getByRole('grid', { name: 'resources' }).getByRole('gridcell', { name: 'workers', exact: true })).toBeVisible()
}

async function palette(page: Page) {
  await page.keyboard.press('Control+k')
  const dlg = page.getByRole('dialog', { name: 'Go to' })
  await expect(dlg.getByRole('combobox', { name: 'Go to' })).toBeFocused()
  await expect(dlg.locator('.palette-search svg')).toHaveCount(0)
  return dlg
}

test('current-view matching and explicit type search do not introduce history-only objects', async ({ page }) => {
  await openDemo(page)
  let dlg = await palette(page)
  await page.keyboard.type('work')
  await expect(dlg.getByRole('option', { selected: true })).toContainText('workers')
  await page.keyboard.press('Enter')
  const drawer = page.getByRole('dialog', { name: 'services workers' })
  await expect(drawer).toBeVisible()
  await expect(page.getByRole('grid', { name: 'resources' }).getByRole('row', { selected: true })).toContainText('workers')
  await drawer.getByRole('button', { name: 'Close' }).click()

  dlg = await palette(page)
  await page.keyboard.type(':wl')
  await page.keyboard.press('Enter')
  await expect(page.getByRole('heading', { name: 'Workloads' })).toBeVisible()

  // History does not introduce Services into a Workloads query.
  dlg = await palette(page)
  await expect(dlg.getByRole('option').filter({hasText:'workers'})).toHaveCount(0)
  await dlg.getByRole('combobox').fill('serv workers')
  const recent = dlg.getByRole('option').filter({ hasText: 'workers' })
  await expect(recent).toContainText('Object')
  await page.screenshot({path:join(scratchRoot,'palette-type-name-without-magnifier.png')})
  await recent.click()
  await expect(page.getByRole('dialog', { name: 'services workers' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Services', exact: true })).toBeVisible()
  await expect(page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Services', exact: true })).toHaveAttribute('aria-current', 'page')
  await expect(page.getByRole('grid', { name: 'resources' }).getByRole('row', { selected: true })).toContainText('workers')
})

test(':wl <text> opens the view filtered; Esc closes and gives focus back', async ({ page }) => {
  await openDemo(page)
  const filter = page.getByRole('textbox', { name: 'Filter rows' })
  await filter.focus()
  const dlg = await palette(page)
  await page.keyboard.press('Escape')
  await expect(dlg).toBeHidden()
  await expect(filter).toBeFocused()

  await palette(page)
  await page.keyboard.type(':wl db')
  await page.keyboard.press('Enter')
  await expect(page.getByRole('heading', { name: 'Workloads' })).toBeVisible()
  await expect(filter).toHaveValue('db')
  const grid = page.getByRole('grid', { name: 'resources' })
  await expect(grid.getByRole('gridcell', { name: 'db', exact: true })).toBeVisible()
  await expect(grid.getByRole('gridcell', { name: 'web', exact: true })).toBeHidden()
})

test('an unknown command says so and does nothing', async ({ page }) => {
  await openDemo(page)
  const dlg = await palette(page)
  await page.keyboard.type(':nope')
  await expect(dlg.getByRole('status')).toHaveText('Unknown command')
  await page.keyboard.press('Enter')
  await expect(dlg).toBeVisible()
})

// A long table makes the navigation reveal observable with real layout.
test('opening a palette result selects and reveals its off-screen row', async ({ page }) => {
  await page.route('**/api/GetRows', async (route) => {
    const response = await route.fetch()
    const body = await response.json()
    if (body.upserts?.length === 2 && body.upserts.every((r: { ref: { kind: string } }) => r.ref.kind === 'services')) {
      const source = body.upserts[0]
      const fillers = Array.from({ length: 120 }, (_, i) => {
        const name = `filler-${String(i).padStart(3, '0')}`
        return { ...source, id: name, ref: { ...source.ref, name, uid: name }, cells: [{ text: name }, ...source.cells.slice(1)] }
      })
      body.upserts.push(...fillers)
    }
    await route.fulfill({ response, json: body })
  })
  await page.goto('/')
  await selectConnection(page,/^demo\b/)
  await connectSelected(page)
  await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Services', exact: true }).click()
  const grid = page.getByRole('grid', { name: 'resources' })
  await expect(grid.getByRole('gridcell', { name: 'filler-000', exact: true })).toBeVisible()
  await expect(grid.getByRole('gridcell', { name: 'workers', exact: true })).toHaveCount(0)
  await palette(page)
  await page.keyboard.type('workers')
  await page.keyboard.press('Enter')
  await expect(page.getByRole('dialog', { name: 'services workers' })).toBeVisible()
  const selected = grid.getByRole('row', { selected: true })
  await expect(selected).toContainText('workers')
  await expect(selected).toBeInViewport()
  await expect(grid.locator('[data-table-scroll]')).toBeFocused()
  await page.screenshot({path:join(scratchRoot,'table-focus-over-scrolled-rows.png')})
  await page.keyboard.press('l')
  await expect(page.getByRole('region', { name: 'Bottom panel' })).toBeVisible()
  if (process.env.E2E_PALETTE_SCREENSHOT) await page.screenshot({ path: process.env.E2E_PALETTE_SCREENSHOT })
})

test('a slow resource list shows loading until its rows arrive', async ({ page }) => {
  await openDemo(page)
  await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Workloads', exact: true }).click()
  await expect(page.getByRole('grid', { name: 'resources' }).getByRole('gridcell', { name: 'web', exact: true })).toBeVisible()
  let release!: () => void
  const pending = new Promise<void>((resolve) => { release = resolve })
  await page.route('**/api/GetRows', async (route) => {
    const response = await route.fetch()
    await pending
    await route.fulfill({ response })
  })
  try {
    await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Services', exact: true }).click()
    const loading = page.getByRole('status', { name: 'Loading…' })
    await expect(loading).toHaveText('Loading Services…')
    await expect(page.getByText('No objects', { exact: true })).toHaveCount(0)
    await expect(page.locator('[data-area="table"]')).toHaveAttribute('aria-busy', 'true')
    if (process.env.E2E_LOADING_SCREENSHOT) await page.screenshot({ path: process.env.E2E_LOADING_SCREENSHOT })
    release()
    await expect(page.getByRole('grid', { name: 'resources' }).getByRole('gridcell', { name: 'workers', exact: true })).toBeVisible()
    await expect(loading).toHaveCount(0)
    await expect(page.locator('[data-area="table"]')).toHaveAttribute('aria-busy', 'false')
  } finally { release() }
})

test('object search stays inside selected scopes across views and opens the matching kind', async ({ page }) => {
  await page.goto('/')
  await selectConnection(page,/^demo\b/)
  await connectSelected(page)
  const nav = page.getByRole('navigation', { name: 'resources' })
  await nav.getByRole('button', { name: 'Crates', exact: true }).click()
  const grid = page.getByRole('grid', { name: 'resources' })
  const zone = page.getByRole('textbox', { name: 'Zone', exact: true })
  const setZone = async (value: string) => {
    await zone.fill(value)
    await zone.press('Enter')
  }
  const remember = async (name: string) => {
    const saved = page.waitForResponse((r) => r.url().endsWith('/api/TouchRecent') && r.ok())
    await grid.getByRole('gridcell', { name, exact: true }).click()
    await saved
    await page.getByRole('dialog', { name: `crates ${name}` }).getByRole('button', { name: 'Close', exact: true }).click()
  }
  await setZone('green')
  await remember('gamma')
  await setZone('blue')
  await remember('alpha')
  // An unscoped table must not widen the remembered namespace selection.
  await nav.getByRole('button', { name: 'Services', exact: true }).click()
  let dlg = await palette(page)
  const search = dlg.getByRole('combobox', { name: 'Go to' })
  await search.fill('gamma')
  await expect(dlg.getByRole('status')).toHaveText('Nothing found')
  await expect(dlg.getByRole('option')).toHaveCount(0)
  if (process.env.E2E_PALETTE_SCOPE_SCREENSHOT) await page.screenshot({ path: process.env.E2E_PALETTE_SCOPE_SCREENSHOT })
  await search.fill('alpha')
  await expect(dlg.getByRole('option')).toHaveCount(0)
  await search.fill('cr alpha')
  const recent = dlg.getByRole('option').filter({ hasText: 'alpha' })
  await expect(recent).toContainText('Object')
  await recent.click()
  await expect(page.getByRole('heading', { name: 'Crates', exact: true })).toBeVisible()
  await expect(nav.getByRole('button', { name: 'Crates', exact: true })).toHaveAttribute('aria-current', 'page')
  await expect(zone).toHaveValue('blue')
  await expect(grid.getByRole('row', { selected: true })).toContainText('alpha')
  await page.getByRole('dialog', { name: 'crates alpha' }).getByRole('button', { name: 'Close', exact: true }).click()
  await setZone('blue, green')
  dlg = await palette(page)
  await dlg.getByRole('combobox', { name: 'Go to' }).fill('gamma')
  await expect(dlg.getByRole('option')).toHaveCount(1)
  await expect(dlg.getByRole('option')).toContainText('Object')
  await page.keyboard.press('Escape')
  await setZone('') // explicit All
  await nav.getByRole('button', { name: 'Services', exact: true }).click()
  dlg = await palette(page)
  await dlg.getByRole('combobox', { name: 'Go to' }).fill('gamma')
  await expect(dlg.getByRole('option')).toHaveCount(0)
  await dlg.getByRole('combobox', { name: 'Go to' }).fill('cr gamma')
  await expect(dlg.getByRole('option')).toHaveCount(1)
  await expect(dlg.getByRole('option')).toContainText('Object')
  await page.keyboard.press('Enter')
  await expect(nav.getByRole('button', { name: 'Crates', exact: true })).toHaveAttribute('aria-current', 'page')
  await expect(zone).toHaveValue('')
  await expect(grid.getByRole('row', { selected: true })).toContainText('gamma')
  if (process.env.E2E_PALETTE_KIND_SCREENSHOT) await page.screenshot({ path: process.env.E2E_PALETTE_KIND_SCREENSHOT })
})

test('type and name reads only the selected kind and scope, then closes its temporary view',async({page})=>{
 await openDemo(page)
 const nav=page.getByRole('navigation',{name:'resources'})
 await nav.getByRole('button',{name:'Crates',exact:true}).click()
 const zone=page.getByRole('textbox',{name:'Zone',exact:true})
 await zone.fill('blue');await zone.press('Enter')
 await expect(page.getByRole('grid',{name:'resources'}).getByRole('gridcell',{name:'alpha',exact:true})).toBeVisible()
 await nav.getByRole('button',{name:'Services',exact:true}).click()
 await expect(page.getByRole('grid',{name:'resources'}).getByRole('gridcell',{name:'workers',exact:true})).toBeVisible()
 const requests:{kind:string;scope:{mode:string;name?:string}}[]=[]
 page.on('request',r=>{if(r.url().endsWith('/api/OpenView'))requests.push(r.postDataJSON().query)})
 const dlg=await palette(page)
 await dlg.getByRole('combobox').fill('cr alpha')
 const object=dlg.getByRole('option').filter({hasText:'alpha'})
 await expect(object).toContainText('Object')
 expect(requests).toHaveLength(1);expect(requests[0]).toEqual({kind:'crates',scope:{mode:'one',name:'blue'}})
 await dlg.getByRole('combobox').fill('cr beta')
 await expect(dlg.getByRole('option').filter({hasText:'beta'})).toContainText('Object')
 expect(requests).toHaveLength(1)
 const closed=page.waitForResponse(r=>r.url().endsWith('/api/CloseView')&&r.ok())
 await page.keyboard.press('Escape');await closed
 await expect(page.getByRole('heading',{name:'Services',exact:true})).toBeVisible()
})

test('wrong keyboard layout recovers one scoped type/name query, keeps input and UID navigation', async ({ page }) => {
 await openDemo(page)
 const nav = page.getByRole('navigation', { name: 'resources' })
 await nav.getByRole('button', { name: 'Crates', exact: true }).click()
 const zone = page.getByRole('textbox', { name: 'Zone', exact: true })
 await zone.fill('blue'); await zone.press('Enter')
 await expect(page.getByRole('grid', { name: 'resources' }).getByRole('gridcell', { name: 'alpha', exact: true })).toBeVisible()
 await nav.getByRole('button', { name: 'Services', exact: true }).click()
 await expect(page.getByRole('grid', { name: 'resources' }).getByRole('gridcell', { name: 'workers', exact: true })).toBeVisible()
 const reads: unknown[] = []
 page.on('request', request => { if (request.url().endsWith('/api/OpenView')) reads.push(request.postDataJSON().query) })
 const dlg = await palette(page)
 const input = dlg.getByRole('combobox')
 await input.fill('ск фдзрф')
 await expect(dlg.getByRole('option')).toContainText('Object')
 await expect(dlg.getByRole('option')).toContainText('alpha')
 await expect(input).toHaveValue('ск фдзрф')
 await expect(dlg.getByText('Keyboard layout: searching for “cr alpha”')).toBeVisible()
 expect(reads).toEqual([{ kind: 'crates', scope: { mode: 'one', name: 'blue' } }])
 await input.fill('ск иуеф')
 await expect(dlg.getByRole('option')).toContainText('beta')
 expect(reads).toHaveLength(1)
 await input.fill('ск пфььф')
 await expect(dlg.getByRole('option')).not.toContainText('Object')
 await input.fill('ск фдзрф')
 await page.screenshot({ path: join(scratchRoot, 'keyboard-layout-en.png') })
 await input.press('Enter')
 await expect(page.getByRole('dialog', { name: 'crates alpha', exact: true })).toBeVisible()
 await expect(page.getByRole('grid', { name: 'resources' }).getByRole('row', { selected: true })).toContainText('alpha')
 await expect(zone).toHaveValue('blue')
})

test('keyboard fallback hint renders in eight languages at normal and narrow widths', async ({ page }) => {
 await openDemo(page)
 const languages = [['en', 'English'], ['ru', 'Русский'], ['zh', '简体中文'], ['es', 'Español'], ['de', 'Deutsch'], ['fr', 'Français'], ['pt', 'Português (Brasil)'], ['ja', '日本語']]
 try {
  for (const [code, name] of languages) {
   await openAppMenu(page);await page.locator('.language-trigger').click()
   await page.getByRole('option', { name, exact: true }).click()
   await expect(page.locator('html')).toHaveAttribute('lang', code)
   await page.keyboard.press('Control+k')
   const dlg = page.locator('.palette-dialog')
   await dlg.getByRole('combobox').fill('ск фдзрф')
   await expect(dlg.getByRole('option').filter({ hasText: 'alpha' })).toHaveCount(1)
   const hint = dlg.getByRole('status').filter({ hasText: 'cr alpha' })
   await expect(hint).toBeVisible()
   for (const width of [1280, 860]) {
    await page.setViewportSize({ width, height: 900 })
    expect(await dlg.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true)
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    await page.screenshot({ path: join(scratchRoot, `keyboard-layout-${code}-${width}.png`) })
   }
   await page.keyboard.press('Escape')
  }
 } finally {
  await page.keyboard.press('Escape')
  await openAppMenu(page);await page.locator('.language-trigger').click()
  await page.getByRole('option', { name: 'English', exact: true }).click()
 }
})
