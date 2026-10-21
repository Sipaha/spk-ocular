import { selectConnection, openAppMenu } from './fixtures'
import { languages, languageNames } from '../../web/src/languages'
import { connectSelected } from './fixtures'
import { expect, type Page } from '@playwright/test'
import { test, scratchRoot } from './fixtures'
import { join } from 'node:path'
import { token } from './synth'

// The generic UI takes everything from the provider's metadata (P6 Task 1):
// the synthetic target opens its default view (Crates, not pods) in its
// default scope ("blue" — a zone, not a namespace), its scopes cannot be
// listed (typed), objects are keyed apart from their titles, there are no
// events, a terminal has nowhere to run, and views can be read again (F5).

// Start this owned synthetic target with a fresh connection, view and scope.
// Cancellation intentionally survives frontend reloads, so resetting page state
// alone cannot isolate a test from a previous cancelled observation.
// Forget the view and scope
// (from the page: the API wants its Origin).
async function forgetDemo(page: Page) {
  await page.goto('/')
  const tok = await token(page)
  const ok = await page.evaluate(async (tok) => {
    const closed = await fetch('/api/CloseTarget', {
      method: 'POST',
      headers: { Authorization: `Bearer ${tok}`, 'Content-Type': 'application/json' },
      body: JSON.stringify({ provider: 'synthetic', id: 'demo' }),
    })
    if (!closed.ok) return false
    for (const key of ['kind', 'scope']) {
      const res = await fetch('/api/SetTargetState', {
        method: 'POST',
        headers: { Authorization: `Bearer ${tok}`, 'Content-Type': 'application/json' },
        body: JSON.stringify({ provider: 'synthetic', target: 'demo', key, value: '' }),
      })
      if (!res.ok) return false
    }
    return true
  }, tok)
  expect(ok).toBeTruthy()
  await page.reload()
}

async function openDemo(page: Page, zone = 'blue') {
  await forgetDemo(page)
  await selectConnection(page,/^demo\b/)
  await connectSelected(page)
  const grid = page.getByRole('grid', { name: 'resources' })
  await expect(page.getByRole('heading', { name: 'Crates', level: 1 })).toBeVisible()
  const input = page.getByRole('textbox', { name: 'Zone' })
  if ((await input.inputValue()) !== zone) {
    await input.fill(zone)
    await input.press('Enter')
  }
  return grid
}

test('the default view and scope, the provider’s scope words, a typed scope', async ({ page }) => {
  await forgetDemo(page)
  await selectConnection(page,/^demo\b/)
  await connectSelected(page)
  const grid = page.getByRole('grid', { name: 'resources' })
  const zone = page.getByRole('textbox', { name: 'Zone' })
  await expect(zone).toHaveValue('blue')
  await expect(zone).toHaveAttribute('placeholder', 'Type a zone')
  await expect(zone).toHaveAttribute('title', /^Cannot list zones: /)
  await expect(grid.getByRole('gridcell', { name: 'alpha', exact: true })).toBeVisible()
  await expect(grid.getByRole('gridcell', { name: 'beta', exact: true })).toBeVisible()
  await expect(grid.getByRole('gridcell', { name: 'gamma', exact: true })).toHaveCount(0)
  await zone.fill('green')
  await zone.press('Enter')
  await expect(grid.getByRole('gridcell', { name: 'gamma', exact: true })).toBeVisible()
  await expect(grid.getByRole('gridcell', { name: 'alpha', exact: true })).toHaveCount(0)
  // Nothing in the page names Kubernetes scopes.
  await expect(page.getByText(/namespace/i)).toHaveCount(0)
})

test('details: the title shown, the key followed; no events; a terminal with nowhere to run', async ({ page }) => {
  const grid = await openDemo(page)
  await grid.getByRole('gridcell', { name: 'alpha', exact: true }).dblclick()
  const drawer = page.getByRole('dialog', { name: 'crates alpha' })
  await expect(drawer.getByRole('heading', { name: 'alpha' })).toBeVisible()
  await expect(drawer.locator('.drawer-kind')).toHaveText('Crate')
  await expect(drawer.locator('.drawer-kind')).toHaveAttribute('title','crates')
  await drawer.getByRole('tab', { name: 'YAML' }).click()
  await expect(drawer.getByText('key: crate-7f3a')).toBeVisible()
  await drawer.getByRole('tab', { name: 'Details' }).click()
  await expect(drawer.getByRole('region', { name: 'Events' })).toHaveCount(0)
  await drawer.getByRole('button', { name: 'Terminal', exact: true }).click({button:'right'})
  const term = page.getByRole('dialog', { name: 'Open a terminal' })
  await expect(term).toContainText('Nowhere to run it now')
  await expect(term).toContainText('alpha')
})

test('Read again: the button and F5 (also from a field) go to the open view', async ({ page }) => {
  const grid = await openDemo(page)
  const reads = () => grid.getByRole('row').filter({ hasText: 'alpha' }).getByRole('gridcell').last() // Zone is hidden: one zone shown
  const before = Number(await reads().textContent())
  await page.getByRole('button', { name: /^Refresh$/ }).click()
  await expect(reads()).toHaveText(String(before + 1))
  await page.getByRole('textbox', { name: 'Filter rows' }).focus()
  await page.keyboard.press('F5')
  await expect(reads()).toHaveText(String(before + 2))
  await expect(page.getByRole('heading', { name: 'Crates', level: 1 })).toBeVisible() // not reloaded
})

test('scope checkboxes keep the popup and exact set; text chooses one; the set survives target and page changes', async ({ page }) => {
  // Keep the provider's denied-list fixture intact for the typed fallback
  // specs; only this browser is given a catalog of its real zones.
  await page.route('**/api/ListScopes', (route) => route.fulfill({ json: { scopes: ['blue', 'green', 'empty'].map((name) => ({ name })) } }))
  await forgetDemo(page)
  await selectConnection(page,/^demo\b/)
  await connectSelected(page)
  const grid = page.getByRole('grid', { name: 'resources' })
  const picker = page.getByRole('button', { name: 'Zone', exact: true })
  await expect(grid.getByRole('gridcell', { name: 'alpha', exact: true })).toBeVisible()
  await picker.click()
  const search = page.getByRole('combobox', { name: 'Find a zone' })
  await search.fill('green')
  await page.getByRole('checkbox', { name: 'green', exact: true }).click()
  await expect(search).toHaveValue('green')
  await expect(search).toBeFocused()
  await expect(grid.getByRole('gridcell', { name: 'gamma', exact: true })).toBeVisible()
  await expect(grid.getByRole('gridcell', { name: 'alpha', exact: true })).toBeVisible()
  await search.fill('')
  await expect(page.getByRole('checkbox', { name: 'blue', exact: true })).toBeChecked()
  await page.keyboard.press('Escape')
  await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Parcels', exact: true }).click()
  await expect(picker).toHaveText('blue, green')
  await selectConnection(page,/^demo2\b/)
  await connectSelected(page)
  await selectConnection(page,/^demo\b/)
  await connectSelected(page)
  await expect(picker).toHaveText('blue, green')
  await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Crates', exact: true }).click()
  await picker.click()
  await page.getByRole('option', { name: 'green', exact: true }).locator('span').last().click()
  await expect(page.getByRole('listbox', { name: 'Zone' })).toHaveCount(0)
  await expect(picker).toHaveText('green')
  await expect(grid.getByRole('gridcell', { name: 'alpha', exact: true })).toHaveCount(0)
  await expect(grid.getByRole('gridcell', { name: 'gamma', exact: true })).toBeVisible()
  await picker.click()
  await page.getByRole('checkbox', { name: 'green', exact: true }).click()
  await expect(picker).toHaveText('Nothing selected')
  await expect(grid.getByRole('gridcell')).toHaveCount(0)
  await page.getByRole('option', { name: 'All zones', exact: true }).click()
  await expect(grid.getByRole('gridcell', { name: 'alpha', exact: true })).toBeVisible()
  await expect(grid.getByRole('gridcell', { name: 'gamma', exact: true })).toBeVisible()
})

test('a denied scope catalog accepts several explicit names without widening the view', async ({ page }) => {
  const grid = await openDemo(page)
  const input = page.getByRole('textbox', { name: 'Zone', exact: true })
  await input.fill('green, blue, green')
  await input.press('Enter')
  await expect(input).toHaveValue('blue, green')
  await expect(grid.getByRole('gridcell', { name: 'alpha', exact: true })).toBeVisible()
  await expect(grid.getByRole('gridcell', { name: 'gamma', exact: true })).toBeVisible()
  await input.fill('green, empty')
  await input.press('Enter')
  await expect(grid.getByRole('gridcell', { name: 'alpha', exact: true })).toHaveCount(0)
  await expect(grid.getByRole('gridcell', { name: 'gamma', exact: true })).toBeVisible()
})

test('global favorites can be added, opened and removed from any connection', async ({ page }) => {
  await openDemo(page)
  const tok = await token(page)
  try {
    const nav = page.getByRole('navigation', { name: 'resources' })
    await nav.getByRole('button', { name: 'Services', exact: true }).hover()
    await nav.getByRole('button', { name: 'Add Services to favorites', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Crates', exact: true })).toBeVisible()
    const favorites = page.getByRole('region', { name: 'Favorites', exact: true })
    await expect(nav.getByRole('button', { name: 'Services', exact: true })).toHaveCount(1)
    await expect(nav.getByRole('region', { name: 'Synthetic', exact: true }).getByRole('button', { name: 'Services', exact: true })).toHaveCount(0)
    await favorites.getByRole('button', { name: 'Services', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Services', exact: true })).toBeVisible()
    await selectConnection(page,/^demo2\b/)
    await connectSelected(page)
    await expect(favorites.getByRole('button', { name: 'Services', exact: true })).toBeVisible()
    await expect(nav.getByRole('button', { name: 'Services', exact: true })).toHaveCount(1)
    await favorites.getByRole('button', { name: 'Services', exact: true }).focus()
    await page.keyboard.press('Shift+F10')
    await page.getByRole('menuitem', { name: 'Remove Services from favorites', exact: true }).click()
    await expect(favorites.getByRole('button', { name: 'Services', exact: true })).toHaveCount(0)
    await expect(nav.getByRole('region', { name: 'Synthetic', exact: true }).getByRole('button', { name: 'Services', exact: true })).toBeVisible()
    await selectConnection(page,/^demo\b/)
    await connectSelected(page)
    await expect(favorites.getByRole('button', { name: 'Services', exact: true })).toHaveCount(0)
    await expect(page.getByRole('heading', { name: 'Services', exact: true })).toBeVisible()
  } finally {
    const res = await page.request.post('/api/SetKindFavorite', { headers: { Authorization: `Bearer ${tok}`, Origin: new URL(page.url()).origin }, data: { provider: 'synthetic', kind: 'services', favorite: false } })
    expect(res.ok()).toBeTruthy()
  }
})

test('favorite stars stay clickable beside the native scrollbar in an overflowing navigation', async ({ page }) => {
  await page.setViewportSize({ width: 960, height: 360 })
  await openDemo(page)
  const tok = await token(page)
  const nav = page.getByRole('navigation', { name: 'resources' })
  const list = nav.locator('[data-resource-nav-list]')
  const favorites = nav.getByRole('region', { name: 'Favorites', exact: true })
  try {
    expect(await list.evaluate((e) => e.scrollHeight > e.clientHeight)).toBe(true)
    for (const adding of [true, false]) {
      const button = (adding ? nav : favorites).getByRole('button', {
        name: adding ? 'Add Services to favorites' : 'Remove Services from favorites', exact: true,
      })
      await button.scrollIntoViewIfNeeded()
      await button.hover()
      const box = (await button.boundingBox())!
      const viewport = (await list.boundingBox())!
      // GTK's overlay scrollbar intercepts the trailing strip even when hidden;
      // Chromium hit testing alone cannot detect it. Keep the whole button clear.
      expect(viewport.x + viewport.width - box.x - box.width).toBeGreaterThanOrEqual(24)
      const point = { x: box.x + box.width - 1, y: box.y + box.height / 2 }
      expect(await button.evaluate((e, p) => e.contains(document.elementFromPoint(p.x, p.y)), point)).toBe(true)
      const scrollTop = await list.evaluate((e) => e.scrollTop)
      await page.mouse.click(point.x, point.y)
      await expect(favorites.getByRole('button', { name: 'Services', exact: true })).toHaveCount(adding ? 1 : 0)
      await expect(page.getByRole('heading', { name: 'Crates', exact: true })).toBeVisible()
      expect(await list.evaluate((e) => e.scrollTop)).toBe(scrollTop)
    }
  } finally {
    const res = await page.request.post('/api/SetKindFavorite', { headers: { Authorization: `Bearer ${tok}`, Origin: new URL(page.url()).origin }, data: { provider: 'synthetic', kind: 'services', favorite: false } })
    expect(res.ok()).toBeTruthy()
  }
})

test('resource navigation search filters kinds and favorites without filtering table rows', async ({ page }) => {
  const grid = await openDemo(page)
  const search = page.getByRole('textbox', { name: 'Filter resources', exact: true })
  await search.fill('parc')
  const nav = page.getByRole('navigation', { name: 'resources' })
  await expect(nav.getByRole('button', { name: 'Crates', exact: true })).toHaveCount(0)
  await expect(nav.getByRole('button', { name: 'Parcels', exact: true })).toBeVisible()
  await expect(grid.getByRole('gridcell', { name: 'alpha', exact: true })).toBeVisible()
  await search.press('Enter')
  await expect(page.getByRole('heading', { name: 'Parcels', exact: true })).toBeVisible()
  await search.fill('no-matching-kind')
  await expect(nav.getByRole('status')).toHaveText('No matching resources')
  await search.press('Escape')
  await expect(search).toHaveValue('')
  await expect(nav.getByRole('button', { name: 'Crates', exact: true })).toBeVisible()
})

test('column boundaries resize headers and rows together, keep sorting, restore across scopes and reset', async ({ page }) => {
  const grid = await openDemo(page)
  const name = grid.getByRole('columnheader', { name: /^Name/ })
  const handle = grid.getByRole('separator', { name: 'Resize column Name', exact: true })
  const before = await name.boundingBox()
  const grip = await handle.boundingBox()
  if (!before || !grip) throw new Error('column is not laid out')
  const sort = await name.getAttribute('aria-sort')
  let writes = 0
  page.on('request', (r) => { if (r.url().endsWith('/api/SetTargetState') && r.postDataJSON()?.key === 'columnWidths.crates') writes++ })
  await page.mouse.move(grip.x + grip.width / 2, grip.y + grip.height / 2)
  await page.mouse.down()
  await page.mouse.move(grip.x + grip.width / 2 + 100, grip.y + grip.height / 2, { steps: 8 })
  await expect.poll(async () => Math.round((await name.boundingBox())!.width)).toBe(Math.round(before.width + 100))
  expect(writes).toBe(0)
  const cell = grid.getByRole('gridcell', { name: 'alpha', exact: true })
  expect(Math.abs((await cell.boundingBox())!.width - (await name.boundingBox())!.width)).toBeLessThan(1)
  await page.mouse.up()
  await expect.poll(() => writes).toBe(1)
  await expect(name).toHaveAttribute('aria-sort', sort!)
  const zone = page.getByRole('textbox', { name: 'Zone', exact: true })
  await zone.fill('blue, green')
  await zone.press('Enter')
  await expect.poll(async () => Math.round((await name.boundingBox())!.width)).toBe(Math.round(before.width + 100))
  await expect(grid.getByRole('columnheader', { name: 'Zone', exact: true })).toBeVisible()
  await handle.dblclick()
  await expect.poll(() => grid.evaluate((e) => e.style.getPropertyValue('--column-0'))).toBe('')
  await expect(name).toHaveAttribute('aria-sort', sort!)
})

test('favorites drag and drop changes the shared order without opening a resource', async ({ page }) => {
  await openDemo(page)
  const tok = await token(page)
  try {
    const nav = page.getByRole('navigation', { name: 'resources' })
    for (const title of ['Services', 'Crates', 'Parcels']) {
      await nav.getByRole('button', { name: title, exact: true }).hover()
      await nav.getByRole('button', { name: `Add ${title} to favorites`, exact: true }).click()
    }
    const fav = page.getByRole('region', { name: 'Favorites', exact: true })
    const rows = fav.locator('.nav-item')
    await expect(rows).toHaveText(['Services', 'Crates', 'Parcels'])
    await fav.getByRole('button', { name: 'Parcels', exact: true }).dragTo(fav.getByRole('button', { name: 'Services', exact: true }), { targetPosition: { x: 20, y: 2 } })
    await expect(rows).toHaveText(['Parcels', 'Services', 'Crates'])
    await expect(page.getByRole('heading', { name: 'Crates', exact: true })).toBeVisible()
    await selectConnection(page,/^demo2\b/)
    await connectSelected(page)
    await expect(rows).toHaveText(['Parcels', 'Services', 'Crates'])
    await fav.getByRole('button', { name: 'Parcels', exact: true }).dragTo(fav.getByRole('button', { name: 'Crates', exact: true }), { targetPosition: { x: 20, y: 28 } })
    await expect(rows).toHaveText(['Services', 'Crates', 'Parcels'])
  } finally {
    for (const kind of ['services', 'crates', 'parcels']) {
      const res = await page.request.post('/api/SetKindFavorite', { headers: { Authorization: `Bearer ${tok}`, Origin: new URL(page.url()).origin }, data: { provider: 'synthetic', kind, favorite: false } })
      expect(res.ok()).toBeTruthy()
    }
  }
})


test('connection information preserves the resource page and old Overview selections migrate on reload', async ({ page }) => {
  const grid = await openDemo(page)
  await expect(grid.getByRole('gridcell', { name: 'alpha', exact: true })).toBeVisible()
  await page.keyboard.press('/')
  await expect(page.getByRole('textbox', { name: 'Filter rows' })).toBeFocused()
  await page.getByRole('button', { name: 'Connection information' }).click()
  const dialog = page.getByRole('dialog', { name: 'Connection information' })
  await expect(dialog.getByRole('heading', { name: 'demo', exact: true })).toBeVisible()
  await expect(grid.getByRole('gridcell', { name: 'alpha', exact: true })).toBeVisible()
  await expect(dialog.getByRole('button', { name: 'Close', exact: true })).toBeFocused()
  await page.keyboard.press('Tab')
  await page.keyboard.press('Shift+Tab')
  await expect(dialog.getByRole('button', { name: 'Close', exact: true })).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(dialog).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Connection information' })).toBeFocused()
  const tok = await token(page)
  const res = await page.request.post('/api/SetTargetState', {
    headers: { Authorization: `Bearer ${tok}`, Origin: new URL(page.url()).origin },
    data: { provider: 'synthetic', target: 'demo', key: 'kind', value: JSON.stringify('__overview') },
  })
  expect(res.ok()).toBeTruthy()
  await page.reload()
  await expect(page.getByRole('heading', { name: 'Crates', level: 1 })).toBeVisible()
  await expect(page.getByRole('button', { name: 'Overview', exact: true })).toHaveCount(0)
  await expect(dialog).toHaveCount(0)
  await expect(page.locator('[data-connection-switcher]')).toContainText('demo')
  await expect(grid.getByRole('gridcell', { name: 'alpha', exact: true })).toBeVisible()
})


test('empty state stays below the table header', async ({ page }) => {
  const grid = await openDemo(page)
  const header = grid.getByRole('columnheader', { name: /^Name/ })
  const before = (await header.boundingBox())!.y
  const zone = page.getByRole('textbox', { name: 'Zone' })
  await zone.fill('empty')
  await zone.press('Enter')
  const empty = page.locator('[data-table-empty]')
  await expect(empty).toContainText('No objects')
  expect((await header.boundingBox())!.y).toBeCloseTo(before, 0)
  expect((await empty.boundingBox())!.y).toBeGreaterThan((await header.boundingBox())!.y)
  await expect(grid.locator('[data-table-scroll] [data-table-empty]')).toBeVisible()
  await page.screenshot({ path: join(scratchRoot, 'table-empty-inside.png') })
})

test('object switching retains the details layout under an inert overlay and rejects late answers', async ({ page }) => {
  const grid = await openDemo(page)
  await grid.getByRole('gridcell', { name: 'alpha', exact: true }).click()
  const drawer = page.locator('[data-area="details"]')
  await expect(drawer.getByRole('heading', { name: 'alpha', exact: true })).toBeVisible()
  const original = await drawer.elementHandle()
  const before = await drawer.boundingBox()
  const toolsBefore=await drawer.locator('.drawer-tools button').evaluateAll(elements=>elements.map(el=>({label:el.getAttribute('aria-label'),x:el.getBoundingClientRect().x,y:el.getBoundingClientRect().y,width:el.getBoundingClientRect().width,height:el.getBoundingClientRect().height})))
  const scrollHeightBefore=await drawer.locator('[aria-busy] > .overflow-y-auto').evaluate(el=>el.scrollHeight)
  let release!: () => void
  const delayed = new Promise<void>(resolve => { release = resolve })
  await page.route('**/api/GetResource', async route => {
    if (route.request().postDataJSON().name === 'crate-91c2') await delayed
    await route.continue()
  })
  try {
    await grid.getByRole('gridcell', { name: 'beta', exact: true }).click()
    await expect(drawer.locator('[data-loading-overlay]')).toContainText('beta')
    expect(await drawer.evaluate((node, previous) => node === previous, original)).toBe(true)
    expect(await drawer.boundingBox()).toEqual(before)
    // Static facts remain; they cannot launch operations on the previous object.
    await expect(drawer.getByRole('heading', { name: 'alpha', exact: true })).toBeVisible()
    await expect(drawer.locator('.drawer-tools')).toHaveAttribute('inert', '')
    expect(await drawer.locator('.drawer-tools button').evaluateAll(elements=>elements.map(el=>({label:el.getAttribute('aria-label'),x:el.getBoundingClientRect().x,y:el.getBoundingClientRect().y,width:el.getBoundingClientRect().width,height:el.getBoundingClientRect().height})))).toEqual(toolsBefore)
    expect(await drawer.locator('[aria-busy] > .overflow-y-auto').evaluate(el=>el.scrollHeight)).toBe(scrollHeightBefore)
    await page.screenshot({ path: join(scratchRoot, 'details-loading-overlay.png') })
    await grid.getByRole('gridcell', { name: 'alpha', exact: true }).click()
    await expect(drawer.locator('[data-loading-overlay]')).toHaveCount(0)
    const lateAnswer = page.waitForResponse(response => response.url().endsWith('/api/GetResource') && response.request().postDataJSON().name === 'crate-91c2')
    release()
    await lateAnswer
    await page.unroute('**/api/GetResource')
    await expect(drawer.getByRole('heading', { name: 'alpha', exact: true })).toBeVisible()
    await expect(drawer.getByRole('heading', { name: 'beta', exact: true })).toHaveCount(0)
    await expect(drawer.locator('.drawer-tools')).not.toHaveAttribute('inert')
    await page.screenshot({ path: join(scratchRoot, 'details-loading-finished.png') })
  } finally { release() }
})


test('table value hints appear only on overflow and use neutral theme colors',async({page})=>{
 const grid=await openDemo(page),cell=grid.getByRole('gridcell',{name:'alpha',exact:true}),handle=grid.getByRole('separator',{name:'Resize column Name',exact:true})
 try{
  await handle.focus();await handle.press('End')
  await cell.hover();await page.waitForTimeout(900)
  await expect(page.locator('.app-tooltip')).toHaveCount(0)
  await handle.focus();await handle.press('Home')
  expect(await cell.evaluate(el=>el.scrollWidth>el.clientWidth)).toBe(true)
  await cell.hover();await expect(page.locator('.app-tooltip')).toHaveText('alpha')
  const colors=await page.locator('.app-tooltip').evaluate(el=>{const s=getComputedStyle(el),probe=document.createElement('span');probe.style.background='var(--color-panel)';document.body.append(probe);const panel=getComputedStyle(probe).backgroundColor;probe.remove();return {bg:s.backgroundColor,fg:s.color,panel,font:s.fontSize}})
  expect(colors.font).toBe('14px');expect(colors.bg).toBe(colors.panel);expect(colors.bg).not.toBe(colors.fg)
  await page.screenshot({path:join(scratchRoot,'resource-overflow-tooltip.png')})
  await page.keyboard.press('Escape');await expect(page.locator('.app-tooltip')).toHaveCount(0)
  await handle.focus();await handle.press('End');await cell.hover();await page.waitForTimeout(900)
  await expect(page.locator('.app-tooltip')).toHaveCount(0)
  const heading=page.locator('.resource-toolbar h1');await expect(heading).toHaveClass('sr-only')
  const trigger=page.locator('[data-connection-switcher]');await trigger.hover();await page.waitForTimeout(900);await expect(page.locator('.app-tooltip')).toHaveCount(0)
 }finally{await handle.dblclick()}
})

test('resource navigation aligns section and subgroup text with children and centers chevrons',async({page})=>{
  await page.route('**/api/ListKinds',async route=>{
    const response=await route.fetch()
    // Setup closes the previous session: its in-flight request may fail.
    // Keep that response intact; only successful catalogs get fixture groups.
    if(!response.ok()){await route.fulfill({response});return}
    const body=await response.json()
    expect(Array.isArray(body.kinds)).toBe(true)
    for(const kind of body.kinds)if(['services','workloads'].includes(kind.id))kind.subgroup='Nested example'
    await route.fulfill({response,json:body})
  })
 await openDemo(page)
 const nav=page.getByRole('navigation',{name:'resources'}),section=nav.getByRole('region',{name:'Synthetic',exact:true}),heading=section.locator('.nav-group-heading'),group=section.getByRole('group',{name:'Nested example',exact:true}),subheading=group.locator('button[aria-expanded]')
 const align=async(title:import('@playwright/test').Locator,child:import('@playwright/test').Locator,left='-1px')=>{
  await expect(title.locator('[data-nav-chevron]')).toHaveCSS('rotate',await title.getAttribute('aria-expanded')==='true'?'90deg':'none')
  const label=(await title.locator('span').first().boundingBox())!,chevron=(await title.locator('[data-nav-chevron]').boundingBox())!
  const text=await child.evaluate(el=>{const range=document.createRange();range.selectNodeContents(el);const b=range.getBoundingClientRect();return{x:b.x}})
  expect(Math.abs(label.x-text.x)).toBeLessThanOrEqual(.5)
  const chevronOffset=await title.locator('[data-nav-chevron]').evaluate(el=>({position:getComputedStyle(el).position,left:getComputedStyle(el).left}))
  const origin=(await nav.boundingBox())!.x
  expect(chevron.x-origin).toBe(left==='-1px'?9:16)
  expect(chevronOffset).toEqual({position:'relative',left});expect(chevron.width).toBe(9);expect(chevron.height).toBe(9);expect(Math.abs(chevron.y+chevron.height/2-label.y-label.height/2)).toBeLessThanOrEqual(.5)
 }
 await align(heading,section.getByRole('button',{name:'Crates',exact:true}))
 await subheading.click();await align(subheading,group.getByRole('button',{name:'Services',exact:true}),'-2px')
 await page.screenshot({path:join(scratchRoot,'resource-nav-alignment.png')})
 await group.getByRole('button',{name:'Services',exact:true}).click()
 await expect(group.getByRole('button',{name:'Services',exact:true})).toHaveAttribute('aria-current','page')
 await subheading.click();await expect(subheading).toHaveAttribute('aria-expanded','false')
 await expect(subheading.locator('[data-nav-chevron]')).toHaveCSS('rotate','none')
 await expect(group.locator('.nav-item')).toHaveCount(0)
 await expect(page.getByRole('heading',{name:'Services',exact:true})).toBeVisible()
 await page.screenshot({path:join(scratchRoot,'nav-selected-subgroup-collapsed.png')})
 await subheading.click();await expect(group.getByRole('button',{name:'Services',exact:true})).toHaveAttribute('aria-current','page')
 await subheading.click()
 await heading.click();await expect(heading).toHaveAttribute('aria-expanded','false');await expect(section.locator('.nav-item')).toHaveCount(0)
 await heading.click();await expect(heading).toHaveAttribute('aria-expanded','true');await align(heading,section.getByRole('button',{name:'Crates',exact:true}))
})

test('object Events Message has one themed hint without an inherited native row title',async({page})=>{
 const message='Failed to pull image "registry.example.invalid/team/application:missing": manifest unknown; retrying after back-off'
 const eventKind={id:'test-events',title:'Events',singular:'Event',group:'Synthetic',scoped:true,columns:[{id:'reason',title:'Reason',type:'text',width:140},{id:'message',title:'Message',type:'text',width:180}]}
 await page.route('**/api/ListKinds',async route=>{
  const response=await route.fetch()
  // Closing the previous owned session can finish a pending catalog read with gone.
  if(!response.ok()){await route.fulfill({response});return}
  const body=await response.json()
  for(const kind of body.kinds)if(kind.id==='crates')kind.eventsKind=eventKind.id
  await route.fulfill({response,json:body})
 })
 await page.route('**/api/OpenView',async route=>{
  if(route.request().postDataJSON().query.kind===eventKind.id)await route.fulfill({json:{viewId:'fixture-events',kind:eventKind,resync:true}})
  else await route.continue()
 })
 await page.route('**/api/GetRows',async route=>{
  if(route.request().postDataJSON().viewId==='fixture-events')await route.fulfill({json:{viewId:'fixture-events',version:1,reset:true,upserts:[{id:'event-1',ref:{provider:'synthetic',target:'demo',kind:eventKind.id,name:'pull-failed',uid:'event-1'},cells:[{text:'Failed'},{text:message}],health:{state:'warning',reason:'Failed',message}}],deleted:[],status:{state:'ready'}}})
  else await route.continue()
 })
 const grid=await openDemo(page);await grid.getByRole('gridcell',{name:'alpha',exact:true}).click()
 const events=page.getByRole('region',{name:'Events',exact:true}),cell=events.getByRole('gridcell',{name:message,exact:true})
 const toolbar=events.locator('[data-object-events-toolbar]'),refresh=toolbar.getByRole('button',{name:'Refresh',exact:true})
 await expect(refresh).toBeVisible()
 const heading=(await toolbar.getByRole('heading').boundingBox())!,button=(await refresh.boundingBox())!,bar=(await toolbar.boundingBox())!
 expect(heading.y+heading.height/2).toBeCloseTo(button.y+button.height/2,1)
 expect(button.x+button.width).toBeCloseTo(bar.x+bar.width,1)
 await page.screenshot({path:join(scratchRoot,'object-events-refresh-aligned.png')})
 const handle=events.getByRole('separator',{name:'Resize column Message',exact:true})
 await handle.focus();await handle.press('Home')
 expect(await cell.evaluate(el=>el.scrollWidth>el.clientWidth)).toBe(true)
 await cell.hover();await expect(page.getByRole('tooltip')).toHaveText(message)
 expect(await cell.evaluate(el=>el.closest('[title]'))).toBeNull()
 await expect(page.getByRole('tooltip')).toHaveCount(1)
 await expect(page.getByRole('tooltip')).toHaveCSS('font-size','14px')
 await page.screenshot({path:join(scratchRoot,'object-events-message-tooltip.png')})
 const dot=events.locator('[data-health]');await dot.hover()
 await expect(page.getByRole('tooltip')).toHaveText('Failed: '+message)
 await page.mouse.move(0,0);await expect(page.getByRole('tooltip')).toHaveCount(0)
 await expect(dot).toHaveAttribute('title','Failed: '+message)
 for(const language of languages){
  await openAppMenu(page);await page.locator('.language-menu button').click()
  await page.getByRole('option',{name:languageNames[language],exact:true}).click()
  await expect(page.locator('html')).toHaveAttribute('lang',language)
  await expect(page.locator('[data-app-menu]')).toHaveAttribute('aria-expanded','false')
  const bar=page.locator('[data-object-events-toolbar]')
  for(const width of [960,1440]){
   await page.setViewportSize({width,height:900})
   const heading=(await bar.getByRole('heading').boundingBox())!,button=(await bar.locator('button').boundingBox())!,area=(await bar.boundingBox())!
   expect(heading.y+heading.height/2).toBeCloseTo(button.y+button.height/2,1)
   expect(button.x+button.width).toBeCloseTo(area.x+area.width,1)
   expect(button.width).toBe(28);expect(button.height).toBe(28)
   await page.screenshot({path:join(scratchRoot,`object-events-aligned-${language}-${width}.png`)})
  }
 }
 await openAppMenu(page);await page.locator('.language-menu button').click()
 await page.getByRole('option',{name:languageNames.en,exact:true}).click()
 await expect(page.locator('html')).toHaveAttribute('lang','en')
})

test('inventory cancellation shows a latched placeholder until explicit retry or reconnect', async ({ page }) => {
  let viewId = ''
  let loading = true
  let successfulOpens = 0
  await page.route('**/api/OpenView', async route => {
    const req = route.request().postDataJSON()
    const response = await route.fetch()
    const body = await response.json()
    if (req.provider === 'synthetic' && req.query.kind === 'crates' && !req.query.name && response.ok()) {
      viewId = body.viewId
      successfulOpens++
    }
    await route.fulfill({response, json: body})
  })
  await page.route('**/api/GetRows', async route => {
    const req = route.request().postDataJSON()
    const response = await route.fetch()
    const body = await response.json()
    if (req.viewId === viewId && loading && response.ok()) {
      body.status = {state: 'loading', downloadedBytes: 65536}
      body.upserts = []
      body.deleted = []
    }
    await route.fulfill({response, json: body})
  })
  await openDemo(page)
  const progress = page.locator('[data-list-loading]')
  await expect(progress).toContainText('Received 64KiB')
  await expect(progress).toContainText(/[1-9]\d* s/)
  await page.screenshot({path: join(scratchRoot, 'inventory-loading-progress.png')})
  await progress.getByRole('button', {name: 'Cancel', exact: true}).click()
  const cancelled = page.locator('[data-load-cancelled]')
  await expect(cancelled).toContainText('Request cancelled')
  await expect(cancelled.getByRole('button', {name: 'Retry', exact: true})).toBeEnabled()
  const before = successfulOpens
  const nav = page.getByRole('navigation', {name: 'resources'})
  await nav.getByRole('button', {name: 'Services', exact: true}).click()
  await nav.getByRole('button', {name: 'Crates', exact: true}).click()
  await expect(cancelled).toBeVisible()
  expect(successfulOpens).toBe(before)
  // The app owns the latch too: reconstructing the entire frontend keeps it.
  await page.reload()
  await expect(cancelled).toBeVisible()
  expect(successfulOpens).toBe(before)
  await page.screenshot({path: join(scratchRoot, 'inventory-cancelled-placeholder.png')})
  loading = false
  await cancelled.getByRole('button', {name: 'Retry', exact: true}).click()
  await expect(cancelled).toHaveCount(0)
  await expect(page.getByRole('grid', {name: 'resources'}).getByRole('row')).not.toHaveCount(1)
  expect(successfulOpens).toBe(before + 1)
  loading = true
  await page.locator('[data-resync]').click()
  await expect(progress).toBeVisible()
  await progress.getByRole('button', {name: 'Cancel', exact: true}).click()
  await expect(cancelled.getByRole('button', {name: 'Retry', exact: true})).toBeEnabled()
  await page.getByRole('button', {name: 'Connections', exact: true}).click()
  await page.getByRole('button', {name: /^demo\b/}).and(page.locator('.connection-choice')).click({button: 'right'})
  await page.getByRole('menuitem', {name: 'Disconnect', exact: true}).click()
  await expect(page.getByRole('button', {name: 'Connect', exact: true})).toBeVisible()
  loading = false
  await connectSelected(page)
  await expect(cancelled).toHaveCount(0)
  await expect(page.getByRole('grid', {name: 'resources'}).getByRole('row')).not.toHaveCount(1)
})

test('cancelling a refresh conceals and retains the details panel without changing its resize parent', async ({page}) => {
  const grid = await openDemo(page)
  await grid.getByRole('gridcell', {name: 'alpha', exact: true}).dblclick()
  const drawer = page.locator('[data-resource-drawer]')
  await expect(drawer).toBeVisible()
  const element = await drawer.elementHandle()
  const width = (await drawer.boundingBox())!.width
  let refreshId = ''
  let loading = true
  await page.route('**/api/ResyncView', async route => {
    refreshId = route.request().postDataJSON().viewId
    await route.continue()
  })
  await page.route('**/api/GetRows', async route => {
    const response = await route.fetch()
    const body = await response.json()
    if (route.request().postDataJSON().viewId === refreshId && loading && response.ok()) {
      body.status = {...body.status, state: 'loading', downloadedBytes: 131072}
    }
    await route.fulfill({response, json: body})
  })
  await page.locator('[data-resync]').click()
  const progress = page.locator('[data-list-loading]')
  await expect(progress).toContainText('Received 128KiB')
  await progress.getByRole('button', {name: 'Cancel', exact: true}).click()
  const placeholder = page.locator('[data-load-cancelled]')
  await expect(placeholder.getByRole('button', {name: 'Retry', exact: true})).toBeEnabled()
  await expect(drawer).toHaveAttribute('inert', '')
  await expect(drawer).toBeHidden()
  await page.keyboard.press('Escape')
  expect(await element!.evaluate(el => el.isConnected)).toBe(true)
  expect(await drawer.evaluate(el => el.getBoundingClientRect().width)).toBeCloseTo(width, 0)
  expect(await drawer.evaluate(el => el.parentElement!.clientWidth)).toBeGreaterThan(width)
  loading = false
  await placeholder.getByRole('button', {name: 'Retry', exact: true}).click()
  await expect(placeholder).toHaveCount(0)
  await expect(drawer).toBeVisible()
  expect(await element!.evaluate(el => el.isConnected)).toBe(true)
  expect((await drawer.boundingBox())!.width).toBeCloseTo(width, 0)
  await page.unrouteAll({behavior:'wait'})
})

test('refresh details wait two seconds and never move toolbar controls', async ({ page }) => {
  await page.addInitScript(() => {
    const Original = window.EventSource
    window.EventSource = class extends Original {
      constructor(url: string | URL, init?: EventSourceInit) {
        super(url, init)
        ;(window as unknown as {testEvents:EventSource}).testEvents=this
      }
    }
  })
  await page.setViewportSize({width:1440,height:900})
  let loading = false
  await page.route('**/api/GetRows', async route => {
    const response = await route.fetch()
    const body = await response.json()
    if (loading && response.ok()) body.status = {state:'loading',downloadedBytes:65536}
    await route.fulfill({response,json:body})
  })
  await openDemo(page)
  const toolbar = page.locator('.resource-list-toolbar')
  const refresh = toolbar.locator('[data-resync]')
  const filter = toolbar.locator('[data-primary-filter]')
  const before = await refresh.boundingBox()
  const filterBefore = await filter.boundingBox()
  // Arm the artificial loading phase only for the explicit refresh. Initial
  // GetRows replies may still be in flight after the first rows are visible.
  await page.route('**/api/ResyncView', route => { loading = true; return route.continue() })
  await refresh.click()
  await expect(refresh).toHaveAttribute('aria-busy','true')
  await expect(toolbar.locator('[data-list-loading]')).toHaveCount(0)
  await expect(toolbar.locator('[data-list-loading]')).toBeVisible()
  expect(await refresh.boundingBox()).toEqual(before)
  expect(await filter.boundingBox()).toEqual(filterBefore)
  const bounds = await toolbar.boundingBox()
  const filterShell = await toolbar.locator('.resource-filter').boundingBox()
  expect(Math.abs(filterShell!.x + filterShell!.width - (bounds!.x + bounds!.width - 16))).toBeLessThan(1)
  expect(before!.x + before!.width).toBeLessThan(filterShell!.x)
  await page.screenshot({path:join(scratchRoot,'refresh-status-wide.png')})
  try { for (const language of languages) {
    await page.setViewportSize({width:1920,height:900})
    await openAppMenu(page); await page.locator('.language-menu button').click()
    await page.getByRole('option',{name:languageNames[language],exact:true}).click()
    await expect(page.locator('html')).toHaveAttribute('lang',language)
    if(await page.locator('[data-app-menu]').getAttribute('aria-expanded')==='true')await page.locator('[data-app-menu]').click()
    await expect(toolbar.locator('[data-list-loading]')).toBeVisible()
    const progressBounds=await toolbar.locator('[data-list-loading]').boundingBox()
    const slotBounds=await toolbar.locator('.resource-refresh-progress').boundingBox()
    expect(progressBounds!.x).toBeGreaterThanOrEqual(slotBounds!.x)
    await page.screenshot({path:join(scratchRoot,`refresh-status-${language}.png`)})
    await page.setViewportSize({width:960,height:700})
    const fits=await toolbar.locator('[data-list-loading]').evaluate(el=>el.getBoundingClientRect().width <= el.parentElement!.clientWidth)
    if(fits) await expect(toolbar.locator('[data-list-loading]')).toBeVisible()
    else await expect(toolbar.locator('[data-list-loading]')).toBeHidden()
  } } finally {
    await openAppMenu(page); await page.locator('.language-menu button').click()
    await page.getByRole('option',{name:languageNames.en,exact:true}).click()
    await expect(page.locator('html')).toHaveAttribute('lang','en')
    if(await page.locator('[data-app-menu]').getAttribute('aria-expanded')==='true')await page.locator('[data-app-menu]').click()
  }
  await page.setViewportSize({width:960,height:700})
  await expect(toolbar.locator('[data-list-loading]')).toBeHidden()
  const narrow = await refresh.boundingBox()
  await page.screenshot({path:join(scratchRoot,'refresh-status-narrow.png')})
  loading = false
  await page.evaluate(() => (window as unknown as {testEvents:EventSource}).testEvents.dispatchEvent(new MessageEvent('message',{data:JSON.stringify({type:'resync'})})))
  await expect(refresh).toHaveAttribute('aria-busy','false')
  expect(await refresh.boundingBox()).toEqual(narrow)
})

test('resource section spacing and muted favorite stars share one trailing column in every locale',async({page})=>{
 await page.setViewportSize({width:1440,height:900})
 await openDemo(page)
 const nav=page.getByRole('navigation',{name:'resources'})
 const headers={Authorization:'Bearer '+await token(page),Origin:new URL(page.url()).origin}
 const initial=await page.request.post('/api/GetFavoriteKinds',{headers,data:{}});expect(initial.ok()).toBeTruthy()
 const favorites=await initial.json() as {provider:string;kind:string}[]
 const added:string[]=[]
 try {
 for(const kind of ['Services','Parcels']){
  if(favorites.some(item=>item.provider==='synthetic'&&item.kind===kind.toLowerCase()))continue
  await nav.getByRole('button',{name:kind,exact:true}).hover()
  await nav.getByRole('button',{name:`Add ${kind} to favorites`,exact:true}).click()
  added.push(kind.toLowerCase())
  await expect(nav.getByRole('button',{name:`Remove ${kind} from favorites`,exact:true})).toBeVisible()
 }
 for(const language of languages){
  await openAppMenu(page);await page.locator('.language-menu button').click()
  await page.getByRole('option',{name:languageNames[language],exact:true}).click()
  await expect(page.locator('html')).toHaveAttribute('lang',language)
  await expect(page.locator('[data-app-menu]')).toHaveAttribute('aria-expanded','false')
  for(const width of [960,1440]){
   await page.setViewportSize({width,height:900})
   const headers=nav.locator('.nav-group-heading'),stars=nav.locator('.nav-favorite[aria-pressed=true]')
   await expect(stars).toHaveCount(2)
   const star=(await stars.first().boundingBox())!,center=star.x+star.width/2
   for(const header of await headers.all()){
    const spacing=await header.evaluate(el=>({top:getComputedStyle(el).paddingTop,bottom:getComputedStyle(el).paddingBottom}))
    expect(spacing).toEqual({top:'8px',bottom:'8px'})
    const count=(await header.locator('.nav-trailing').boundingBox())!
    expect(count.x+count.width/2).toBeCloseTo(center,1)
   }
   const colors=await stars.first().evaluate(el=>({star:getComputedStyle(el).color,accent:getComputedStyle(document.documentElement).getPropertyValue('--color-accent').trim()}))
   const probe=await page.evaluate(accent=>{const el=document.createElement('span');el.style.color=accent;document.body.append(el);const color=getComputedStyle(el).color;el.remove();return color},colors.accent)
   expect(colors.star).not.toBe(probe)
   const list=nav.locator('[data-resource-nav-list]')
   expect(await list.evaluate(el=>({right:getComputedStyle(el).paddingRight,leftMargin:getComputedStyle(el).marginLeft,rightMargin:getComputedStyle(el).marginRight}))).toEqual({right:'25px',leftMargin:'1px',rightMargin:'-2px'})
   const edge=(await list.boundingBox())!
   const navBounds=(await nav.boundingBox())!
   expect(edge.x-navBounds.x).toBe(9)
   const sectionBounds=(await headers.first().boundingBox())!
   expect(sectionBounds.x-navBounds.x).toBe(10)
   expect((await headers.first().locator('[data-nav-chevron]').boundingBox())!.x-navBounds.x).toBe(9)
   expect((await headers.first().locator('span').first().boundingBox())!.x-navBounds.x).toBe(40)
   expect(edge.x+edge.width-star.x-star.width).toBeGreaterThanOrEqual(24)
   expect(await stars.first().evaluate(el=>getComputedStyle(el).right)).toBe('0px')
   for(const svg of await nav.locator('.nav-group-heading > [data-nav-chevron]').all()){
    const painted=await svg.evaluate(el=>{
     const path=el.querySelector('path')!,box=path.getBBox(),margin=parseFloat(getComputedStyle(path).strokeWidth)/2,matrix=path.getScreenCTM()!
     const corners=[[box.x-margin,box.y-margin],[box.x+box.width+margin,box.y-margin],[box.x-margin,box.y+box.height+margin],[box.x+box.width+margin,box.y+box.height+margin]].map(([x,y])=>new DOMPoint(x,y).matrixTransform(matrix))
     return {left:Math.min(...corners.map(p=>p.x)),right:Math.max(...corners.map(p=>p.x))}
    })
    expect(painted.left).toBeGreaterThanOrEqual(edge.x)
    expect(painted.right).toBeLessThanOrEqual(edge.x+edge.width)
   }
   const selected=nav.locator('.nav-item[aria-current=page]'),hover=nav.getByRole('button',{name:'Workloads',exact:true})
   const selectedColor=await selected.evaluate(el=>getComputedStyle(el).backgroundColor)
   await hover.hover()
   expect(await hover.evaluate(el=>getComputedStyle(el).backgroundColor)).not.toBe(selectedColor)
   await expect(selected).toHaveCSS('background-color',selectedColor)
   await page.screenshot({path:join(scratchRoot,`resource-nav-spacing-${language}-${width}.png`)})
  }
 }
 await openAppMenu(page);await page.locator('.language-menu button').click()
 await page.getByRole('option',{name:languageNames.en,exact:true}).click()
 await expect(page.locator('html')).toHaveAttribute('lang','en')
 } finally {
  for(const kind of added){const response=await page.request.post('/api/SetKindFavorite',{headers,data:{provider:'synthetic',kind,favorite:false}});expect(response.ok()).toBeTruthy()}
  const language=await page.request.post('/api/SetLanguage',{headers,data:{language:'en'}});expect(language.ok()).toBeTruthy()
  const restored=await page.request.post('/api/GetFavoriteKinds',{headers,data:{}});expect(restored.ok()).toBeTruthy();expect(await restored.json()).toEqual(favorites)
 }
})

test('ConfigMap values list and individually reviewed edits preserve other keys in all eight locales',async({page})=>{
 await page.setViewportSize({width:1280,height:900})
 const ref={provider:'synthetic',target:'demo',kind:'configmaps',scope:'blue',name:'application',uid:'cm-1'}
 const kind={id:'configmaps',title:'ConfigMaps',singular:'ConfigMap',group:'Config',scoped:true,values:true,columns:[{id:'name',title:'Name',type:'text'}]}
 let version='7',immutable=false
 const data:Record<string,string>={'app.yaml':'port: 8080\n','keep.txt':'unchanged','binary.bin':'AAEC/w=='}
 const writes:Record<string,unknown>[]=[],prepares:Record<string,unknown>[]=[]
 let reveals=0,holdRead=false
 let releaseRead!:()=>void
 const readGate=new Promise<void>(resolve=>{releaseRead=resolve})
 await page.route('**/api/ListKinds',async route=>{
  const response=await route.fetch();if(!response.ok()){await route.fulfill({response});return}
  const body=await response.json();body.kinds.push(kind);await route.fulfill({response,json:body})
 })
 await page.route('**/api/OpenView',async route=>{
  const request=route.request().postDataJSON()
  if(request.query.kind==='configmaps')await route.fulfill({json:{viewId:request.query.name?'fixture-cm-object':'fixture-cm-list',kind}})
  else await route.continue()
 })
 await page.route('**/api/GetRows',async route=>{
  if(String(route.request().postDataJSON().viewId).startsWith('fixture-cm-'))await route.fulfill({json:{viewId:route.request().postDataJSON().viewId,version:1,reset:true,upserts:[{id:'cm-1',ref,cells:[{text:'application'}],health:{state:'ok'}}],deleted:[],status:{state:'ready'}}})
  else await route.continue()
 })
 await page.route('**/api/GetResource',async route=>{
  if(route.request().postDataJSON().kind==='configmaps')await route.fulfill({json:{ref,health:{state:'ok'},facts:[{key:'Namespace',value:'blue'}],yaml:'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: application\n',relations:[]}})
  else await route.continue()
 })
 await page.route('**/api/GetValues',route=>{
  expect(route.request().postDataJSON()).toEqual(ref)
  return route.fulfill({json:{ref,version,base:'base-'+version,keys:Object.entries(data).map(([key,value])=>({key,size:key==='binary.bin'?4:new TextEncoder().encode(value).length,text:key!=='binary.bin'}))}})
 })
 await page.route('**/api/RevealValue',async route=>{
  const request=route.request().postDataJSON();expect(request.ref).toEqual(ref);reveals++
  if(holdRead)await readGate
  return route.fulfill({json:{key:request.key,size:request.key==='binary.bin'?4:data[request.key].length,text:request.key!=='binary.bin',value:data[request.key],uid:ref.uid,version}})
 })
 await page.route('**/api/PrepareValueEdit',route=>{
  const request=route.request().postDataJSON();prepares.push(request);expect(request.ref).toEqual(ref);expect(request.base).toBe('base-'+version)
  return route.fulfill({json:{where:{provider:ref.provider,target:ref.target,targetTitle:'demo',endpoint:'local synthetic fixture',configHash:'fixture',ref},textDiff:request.key==='binary.bin'?undefined:{before:data[request.key]??'',after:request.op==='delete'?'':request.value},key:request.key,op:request.op,before:data[request.key]?.length??-1,after:request.op==='delete'?-1:request.value.length,destructive:request.op==='delete',checked:true,changed:true,rights:{state:'allowed'},consumers:{known:true,items:[]},unavailable:immutable?{key:'kubernetes.values.configMapImmutable',text:'The ConfigMap is immutable: its values cannot change (create a new ConfigMap instead).'}:undefined,token:immutable?undefined:'plan-'+version}})
 })
 await page.route('**/api/RunValueEdit',route=>{
  const request=route.request().postDataJSON();writes.push(request)
  expect(request).toMatchObject({ref,base:'base-7',key:'app.yaml',op:'set',encoding:'text',token:'plan-7',value:'port: 9090\n'})
  data[request.key]=request.value;version='8'
  return route.fulfill({json:{message:'configmap application: key app.yaml written',version}})
 })
 await openDemo(page)
 const nav=page.getByRole('navigation',{name:'resources'}),filter=nav.getByRole('textbox',{name:'Filter resources'})
 await filter.fill('ConfigMaps');await nav.getByRole('button',{name:'ConfigMaps',exact:true}).click();await filter.fill('')
 await page.getByRole('grid',{name:'resources'}).getByRole('gridcell',{name:'application',exact:true}).click()
 const section=page.locator('[data-values]'),app=section.locator('[data-value-key="app.yaml"]'),binary=section.locator('[data-value-key="binary.bin"]')
 await expect(section.locator('[data-value-key]')).toHaveCount(3)
 const dialog=page.locator('.configmap-value-dialog'),editor=dialog.locator('.cm-content')
 for(const row of await section.locator('[data-value-key]').all()){await expect(row.getByRole('button')).toHaveCount(2);await expect(row.getByRole('button').locator('svg')).toHaveCount(2)}
 await expect(section.locator(':scope > p')).toHaveCount(0)
 for(const language of languages){
  await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[language],exact:true}).click()
  await expect(page.locator('html')).toHaveAttribute('lang',language)
  await expect(page.locator('[data-app-menu]')).toHaveAttribute('aria-expanded','false')
  await expect(section.locator('[data-value-key]')).toHaveCount(3)
  await page.screenshot({path:join(scratchRoot,`configmap-values-${language}.png`)})
  await app.getByRole('button').first().click()
  await expect(editor).toContainText('port: 8080')
  await expect(dialog.locator('.cm-gutters')).toBeVisible()
  expect((await dialog.boundingBox())!.width).toBe(1100)
  expect((await dialog.locator('.cm-scroller').boundingBox())!.height).toBeGreaterThanOrEqual(400)
  await page.screenshot({path:join(scratchRoot,`configmap-editor-${language}.png`)})
  await editor.click();await page.keyboard.press('Control+a');await page.keyboard.insertText('port: 9090\n')
  await editor.press('Control+Enter')
  const localizedReview=page.locator('[role=dialog][aria-modal=true]')
  await expect(localizedReview.locator('[data-op="-"]')).toContainText('port: 8080')
  await expect(localizedReview.locator('[data-op="+"]')).toContainText('port: 9090')
  await page.screenshot({path:join(scratchRoot,`configmap-review-${language}.png`)})
  await localizedReview.locator(':scope > div').first().getByRole('button').click()
  await expect(editor).toContainText('9090')
  await dialog.locator(':scope > div').first().getByRole('button').click()
  await page.getByRole('alertdialog').getByRole('button').first().click()
  await expect(dialog).toHaveCount(0)
 }
 prepares.length=0
 await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames.en,exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang','en')
 await app.getByRole('button',{name:'Edit',exact:true}).click()
 await expect(editor).toContainText('port: 8080')
 for(const width of [860,375]){
  await page.setViewportSize({width,height:620})
  const box=(await dialog.boundingBox())!
  expect(box.x).toBeGreaterThanOrEqual(0);expect(box.x+box.width).toBeLessThanOrEqual(width)
  expect(await dialog.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true)
  await dialog.getByRole('button',{name:/^Review/}).scrollIntoViewIfNeeded()
  await page.screenshot({path:join(scratchRoot,`configmap-editor-narrow-${width}.png`)})
 }
 await page.setViewportSize({width:1280,height:900})
 await editor.click();await page.keyboard.press('Control+f');await expect(dialog.locator('.cm-search')).toBeVisible()
 await page.keyboard.press('Escape');await expect(dialog).toBeVisible();await expect(dialog.locator('.cm-search')).toHaveCount(0)
 await editor.click();await page.keyboard.press('Control+a');await page.keyboard.insertText('port: 9090\n')
 await page.keyboard.press('Control+z');await expect(editor).toContainText('8080')
 await page.keyboard.press('Control+Shift+z');await expect(editor).toContainText('9090')
 await dialog.getByRole('button',{name:'Close',exact:true}).click()
 const discard=page.getByRole('alertdialog',{name:'Discard edits?',exact:true})
 await expect(discard).toBeVisible();await discard.getByRole('button',{name:'Keep editing',exact:true}).click()
 await expect(editor).toContainText('9090');await expect(editor).toBeFocused()
 await editor.press('Control+Enter')
 const review=page.locator('[role=dialog][aria-modal=true]').filter({has:page.getByRole('button',{name:'Apply',exact:true})})
 await expect(review.getByRole('region',{name:'Changes'}).locator('[data-op="-"]')).toContainText('port: 8080');await expect(review.getByRole('region',{name:'Changes'}).locator('[data-op="+"]')).toContainText('port: 9090');await expect(review).toContainText('ConfigMap');expect(prepares).toHaveLength(1);expect(prepares[0]).toMatchObject({key:'app.yaml',value:'port: 9090\n',encoding:'text'});await page.screenshot({path:join(scratchRoot,'configmap-value-review.png')})
 await review.getByRole('button',{name:'Close',exact:true}).click();await expect(editor).toContainText('port: 9090');await editor.press('Control+Enter');await expect(review.getByRole('region',{name:'Changes'})).toBeVisible();await review.getByRole('button',{name:'Apply',exact:true}).click();await expect(review).toHaveCount(0)
 expect(writes).toHaveLength(1);expect(data['keep.txt']).toBe('unchanged');expect(data['binary.bin']).toBe('AAEC/w==')
 await binary.getByRole('button',{name:'Edit',exact:true}).click();await expect(editor).toContainText('AAEC/w==')
 await dialog.getByRole('button',{name:'Cancel',exact:true}).click()
 await app.getByRole('button',{name:'Delete',exact:true}).click()
 await expect(review).toContainText('app.yaml');await expect(review.getByRole('button',{name:'Cancel',exact:true})).toBeFocused()
 expect(prepares.at(-1)).toMatchObject({key:'app.yaml',op:'delete'})
 await review.getByRole('button',{name:'Cancel',exact:true}).click();expect(writes).toHaveLength(1)
 const readsBefore=reveals
 await section.getByRole('button',{name:'Add key',exact:true}).click()
 await expect(dialog.getByRole('textbox',{name:'Key',exact:true})).toBeFocused()
 await expect(editor).toBeVisible();expect(reveals).toBe(readsBefore)
 await dialog.getByRole('button',{name:'Cancel',exact:true}).click()
 immutable=true;await app.getByRole('button',{name:'Edit',exact:true}).click();await expect(editor).toContainText('9090');await editor.click();await page.keyboard.press('Control+a');await page.keyboard.insertText('blocked')
 await dialog.getByRole('button',{name:/^Review/}).click();await expect(review).toContainText('ConfigMap is immutable');await expect(review.getByRole('button',{name:'Apply',exact:true})).toBeDisabled()
 expect(writes).toHaveLength(1)
 await page.screenshot({path:join(scratchRoot,'configmap-value-immutable.png')})
 await review.getByRole('button',{name:'Back to the value',exact:true}).click()
 await dialog.getByRole('button',{name:'Cancel',exact:true}).click()
 await page.getByRole('alertdialog').getByRole('button',{name:'Discard',exact:true}).click()
 holdRead=true
 const beforeDelayed=reveals
 try {
  await app.getByRole('button',{name:'Edit',exact:true}).click()
  await expect(dialog.locator('[aria-busy=true]')).toBeVisible()
  await expect(dialog).toBeFocused()
  await expect.poll(()=>reveals).toBe(beforeDelayed+1)
  await page.screenshot({path:join(scratchRoot,'configmap-value-loading.png')})
  const late=page.waitForResponse(response=>response.url().endsWith('/api/RevealValue'))
  await page.keyboard.press('Escape');await expect(dialog).toHaveCount(0)
  releaseRead();await late
  await expect(dialog).toHaveCount(0);expect(writes).toHaveLength(1)
 } finally {releaseRead()}
})

test('collapsing navigation hides the selected resource and Favorites without changing the page',async({page})=>{
 await openDemo(page)
 const nav=page.getByRole('navigation',{name:'resources'}),section=nav.getByRole('region',{name:'Synthetic',exact:true}),favorites=nav.getByRole('region',{name:'Favorites',exact:true})
 const heading=section.getByRole('button',{name:/^Synthetic \(/}),favoriteHeading=favorites.getByRole('button',{name:/^Favorites \(/})
 let opens=0
 await page.route('**/api/OpenView',async route=>{opens++;await route.continue()})
 await expect(section.getByRole('button',{name:'Crates',exact:true})).toHaveAttribute('aria-current','page')
 await heading.click();await expect(heading).toHaveAttribute('aria-expanded','false')
 await expect(section.locator('.nav-item')).toHaveCount(0)
 await expect(page.getByRole('heading',{name:'Crates',exact:true})).toBeVisible()
 await expect(heading.locator('[data-nav-chevron]')).toHaveCSS('rotate','none')
 await page.screenshot({path:join(scratchRoot,'nav-selected-section-collapsed.png')})
 await heading.focus();await page.keyboard.press('F6');await page.keyboard.press('Shift+F6');await expect(nav).toBeFocused()
 await page.keyboard.press('ArrowDown');await expect(favoriteHeading).toBeFocused()
 await heading.click();await expect(section.getByRole('button',{name:'Crates',exact:true})).toHaveAttribute('aria-current','page')
 if(await favoriteHeading.getAttribute('aria-expanded')==='false')await favoriteHeading.click()
 await section.getByRole('button',{name:'Add Crates to favorites',exact:true}).click()
 await expect(favorites.getByRole('button',{name:'Crates',exact:true})).toHaveAttribute('aria-current','page')
 await favoriteHeading.click();await expect(favorites.locator('.nav-item')).toHaveCount(0)
 await expect(nav.locator('[aria-current=page]')).toHaveCount(0)
 await expect(page.getByRole('heading',{name:'Crates',exact:true})).toBeVisible()
 await expect(favoriteHeading.locator('[data-nav-chevron]')).toHaveCSS('rotate','none')
 await page.screenshot({path:join(scratchRoot,'nav-selected-favorite-collapsed.png')})
 const filter=nav.getByRole('textbox',{name:'Filter resources'})
 await filter.fill('Crates');await expect(favorites.getByRole('button',{name:'Crates',exact:true})).toHaveAttribute('aria-current','page')
 await filter.press('Escape');await expect(favorites.locator('.nav-item')).toHaveCount(0)
 await favoriteHeading.click();await expect(favorites.getByRole('button',{name:'Crates',exact:true})).toHaveAttribute('aria-current','page')
 await favorites.getByRole('button',{name:'Remove Crates from favorites',exact:true}).click()
 expect(opens).toBe(0)
})

test('Ingress related routes keep paths to repeated Services in all eight locales',async({page})=>{
 await page.setViewportSize({width:1280,height:900})
 const ref={provider:'synthetic',target:'demo',kind:'networking.k8s.io/ingresses',scope:'blue',name:'app-ingress',uid:'ing-1'}
 const kind={id:'networking.k8s.io/ingresses',title:'Ingresses',singular:'Ingress',group:'Network',scoped:true,columns:[{id:'name',title:'Name',type:'text'}]}
 const service={provider:'synthetic',target:'demo',kind:'services',scope:'blue',name:'api'}
 const paths=[{path:'/api',host:'example.test',pathType:'Prefix'},{path:'/admin',host:'example.test',pathType:'Exact'},{path:'/api',host:'other.test',pathType:'ImplementationSpecific'},{path:'',defaultBackend:true},{path:'/a/very/long/path/with/many/segments/that/must/remain/available/in/a/narrow/details/pane',pathType:'Prefix'}]
 await page.route('**/api/ListKinds',async route=>{const response=await route.fetch();if(!response.ok()){await route.fulfill({response});return};const body=await response.json();body.kinds.push(kind);await route.fulfill({response,json:body})})
 await page.route('**/api/OpenView',async route=>{
  const req=route.request().postDataJSON()
  if(req.query.kind==='networking.k8s.io/ingresses')await route.fulfill({json:{viewId:'fixture-ingresses',kind}});else await route.continue()
 })
 await page.route('**/api/GetRows',async route=>{
  if(route.request().postDataJSON().viewId==='fixture-ingresses')await route.fulfill({json:{viewId:'fixture-ingresses',version:1,reset:true,upserts:[{id:'ing-1',ref,cells:[{text:ref.name}],health:{state:'ok'}}],deleted:[],status:{state:'ready'}}});else await route.continue()
 })
 let openedService=false
 await page.route('**/api/GetResource',async route=>{
  const req=route.request().postDataJSON()
  if(req.kind==='networking.k8s.io/ingresses')await route.fulfill({json:{ref,health:{state:'ok'},yaml:'kind: Ingress\n',facts:[{key:'Hosts',value:'example.test, other.test'}],relations:paths.map(route=>({type:'routes-to',ref:service,route}))}})
  else {if(req.kind==='services'&&req.name==='api'){expect(req).toEqual(service);openedService=true};await route.continue()}
 })
 await openDemo(page)
 const nav=page.getByRole('navigation',{name:'resources'}),filter=nav.getByRole('textbox',{name:'Filter resources'})
 await filter.fill('Ingresses');await nav.getByRole('button',{name:'Ingresses',exact:true}).click();await filter.fill('')
 await page.getByRole('grid',{name:'resources'}).getByRole('gridcell',{name:ref.name,exact:true}).click()
 const drawer=page.getByRole('dialog',{name:'networking.k8s.io/ingresses app-ingress',exact:true}),table=drawer.locator('table').first()
 const labels={ru:'Путь',en:'Path',zh:'路径',es:'Ruta',de:'Pfad',fr:'Chemin',pt:'Caminho',ja:'パス'}
 for(const language of languages){
  await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[language],exact:true}).click()
  await expect(table.getByRole('columnheader').nth(2)).toHaveText(labels[language])
  await expect(table.locator('tbody tr')).toHaveCount(5)
  for(const [i,route] of paths.entries())await expect(table.locator('tbody tr').nth(i).locator('td').nth(2)).toContainText(route.path||'defaultBackend')
  await page.screenshot({path:join(scratchRoot,`ingress-routes-${language}.png`)})
 }
 await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames.en,exact:true}).click()
 await expect(page.locator('html')).toHaveAttribute('lang','en')
 await page.setViewportSize({width:860,height:620})
 await expect(table.locator('tbody tr').last().locator('td').nth(2)).toContainText(paths[4].path)
 expect(await drawer.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true)
 await page.screenshot({path:join(scratchRoot,'ingress-routes-narrow.png')})
 await table.locator('tbody tr').nth(1).getByRole('button',{name:'services/api',exact:true}).click()
 await expect.poll(()=>openedService).toBe(true)
 await expect(page.getByRole('dialog',{name:'services api',exact:true})).toBeVisible()
})

test('inspector Events keeps compact geometry, live refresh, sorting and bounded virtual rows', async ({page}) => {
 const kind={id:'detail-events',title:'Events',singular:'Event',group:'Synthetic',scoped:true,columns:[{id:'reason',title:'Reason',type:'text',width:140},{id:'message',title:'Message',type:'text',width:180}]}
 let count=2,state='ready',version=1,refreshes=0
 const subjects: Record<string,unknown>[]=[]
 await page.route('**/api/ListKinds',async route=>{
  const response=await route.fetch();if(!response.ok()){await route.fulfill({response});return}
  const body=await response.json();for(const item of body.kinds)if(item.id==='crates')item.eventsKind=kind.id
  await route.fulfill({response,json:body})
 })
 await page.route('**/api/GetResource',async route=>{
  const response=await route.fetch();if(!response.ok()){await route.fulfill({response});return}
  const resource=await response.json();resource.revisionsAvailable=true
  resource.revisions=[{number:1,current:true,ref:resource.ref,created:'2026-10-01T10:00:00Z',ready:1,replicas:1,images:['app:v1']}]
  await route.fulfill({response,json:resource})
 })
 await page.route('**/api/OpenView',async route=>{
  const query=route.request().postDataJSON().query
  if(query.kind===kind.id){subjects.push(query);await route.fulfill({json:{viewId:'detail-events-view',kind,resync:true}})}
  else await route.continue()
 })
 await page.route('**/api/GetRows',async route=>{
  if(route.request().postDataJSON().viewId!=='detail-events-view'){await route.continue();return}
  const rows=Array.from({length:count},(_,i)=>({id:`e-${i}`,ref:{provider:'synthetic',target:'demo',kind:kind.id,scope:'blue',name:`event-${i}`,uid:`uid-${i}`},cells:[{text:`Reason ${String(i).padStart(3,'0')}`},{text:`Message ${i}`}],health:{state:'ok'}}))
  await route.fulfill({json:{viewId:'detail-events-view',version:version++,reset:true,upserts:rows,deleted:[],status:{state,...(state==='error'?{class:'unavailable',message:'Event fixture unavailable'}:{})}}})
 })
 await page.route('**/api/ResyncView',async route=>{
  if(route.request().postDataJSON().viewId==='detail-events-view'){refreshes++;await route.fulfill({json:{}})}else await route.continue()
 })
 const show=async()=>{const grid=await openDemo(page);await grid.getByRole('gridcell',{name:'alpha',exact:true}).click();return page.locator('section').filter({has:page.locator('[data-object-events-toolbar]')})}
 let events=await show()
 const grid=events.getByRole('grid'),header=grid.getByRole('columnheader',{name:/Reason/})
 await expect(grid).toHaveAttribute('data-appearance','detail')
 await expect(header).toHaveCSS('font-size','13px');await expect(header).toHaveCSS('padding-left','8.5px')
 expect((await grid.boundingBox())!.height).toBeLessThan(115)
 const revisions=page.getByRole('table',{name:'Deployment revisions',exact:true})
 await expect(revisions).toBeVisible()
 expect(await grid.locator('.resource-grid-header').evaluate(el=>getComputedStyle(el).backgroundColor)).toBe(await revisions.locator('thead').evaluate(el=>getComputedStyle(el).backgroundColor))
 await header.click();await expect(header).toHaveAttribute('aria-sort','descending')
 await expect(grid.locator('[data-row-id]').first()).toContainText('Reason 001')
 const resize=grid.getByRole('separator',{name:'Resize column Message',exact:true})
 await resize.focus();const before=await grid.evaluate(el=>el.style.getPropertyValue('--column-1'))
 await resize.press('ArrowRight');expect(await grid.evaluate(el=>el.style.getPropertyValue('--column-1'))).not.toBe(before)
 await expect(header).toHaveAttribute('aria-sort','descending')
 await events.getByRole('button',{name:'Refresh',exact:true}).click();await expect.poll(()=>refreshes).toBe(1)
 expect(subjects.at(-1)).toMatchObject({scope:{mode:'one',name:'blue'},subject:{name:'crate-7f3a',uid:expect.any(String),scope:'blue'}})
 await page.screenshot({path:join(scratchRoot,'events-compact-with-revisions.png')})
 count=80;events=await show();const many=events.getByRole('grid'),scroll=many.locator('[data-table-scroll]')
 await expect(many).toHaveAttribute('aria-rowcount','80')
 expect((await scroll.boundingBox())!.height).toBeLessThanOrEqual(224)
 expect(await scroll.evaluate(el=>el.scrollHeight>el.clientHeight)).toBe(true)
 await scroll.focus();await scroll.press('End');await expect(many.getByRole('gridcell',{name:'Message 79',exact:true})).toBeVisible()
 await page.screenshot({path:join(scratchRoot,'events-compact-many-rows.png')})
 count=0;events=await show();await expect(events.getByText('No events', {exact:true})).toBeVisible();expect((await events.boundingBox())!.height).toBeLessThan(90)
 state='loading';events=await show();await expect(events.locator('[data-list-loading]')).toBeVisible()
 state='error';events=await show();await expect(events).toContainText('Event fixture unavailable')
 await page.unrouteAll({behavior:'wait'})
})
