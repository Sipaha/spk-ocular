import { selectConnection } from './fixtures'
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
  const trigger=page.locator('[data-connection-switcher]');await trigger.hover();await expect(page.locator('.app-tooltip')).toHaveText('demo')
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
 const align=async(title:import('@playwright/test').Locator,child:import('@playwright/test').Locator)=>{
  const label=(await title.locator('span').first().boundingBox())!,chevron=(await title.locator('svg').boundingBox())!
  const text=await child.evaluate(el=>{const range=document.createRange();range.selectNodeContents(el);const b=range.getBoundingClientRect();return{x:b.x}})
  expect(Math.abs(label.x-text.x)).toBeLessThanOrEqual(.5)
  const chevronOffset=await title.locator('svg').evaluate(el=>({position:getComputedStyle(el).position,left:getComputedStyle(el).left}))
  expect(chevronOffset).toEqual({position:'relative',left:'-1px'});expect(Math.abs(chevron.y+chevron.height/2-label.y-label.height/2)).toBeLessThanOrEqual(.5)
 }
 await align(heading,section.getByRole('button',{name:'Crates',exact:true}))
 await subheading.click();await align(subheading,group.getByRole('button',{name:'Services',exact:true}))
 await page.screenshot({path:join(scratchRoot,'resource-nav-alignment.png')})
 await subheading.click();await expect(subheading).toHaveAttribute('aria-expanded','false')
 await heading.click();await expect(heading).toHaveAttribute('aria-expanded','false');await align(heading,section.getByRole('button',{name:'Crates',exact:true}))
 await heading.click();await expect(heading).toHaveAttribute('aria-expanded','true')
})

test('object Events Message has one themed hint without an inherited native row title',async({page})=>{
 const message='Failed to pull image "registry.example.invalid/team/application:missing": manifest unknown; retrying after back-off'
 const eventKind={id:'test-events',title:'Events',singular:'Event',group:'Synthetic',scoped:true,columns:[{id:'reason',title:'Reason',type:'text',width:140},{id:'message',title:'Message',type:'text',width:180}]}
 await page.route('**/api/ListKinds',async route=>{
  const response=await route.fetch(),body=await response.json()
  for(const kind of body.kinds)if(kind.id==='crates')kind.eventsKind=eventKind.id
  await route.fulfill({response,json:body})
 })
 await page.route('**/api/OpenView',async route=>{
  if(route.request().postDataJSON().query.kind===eventKind.id)await route.fulfill({json:{viewId:'fixture-events',kind:eventKind}})
  else await route.continue()
 })
 await page.route('**/api/GetRows',async route=>{
  if(route.request().postDataJSON().viewId==='fixture-events')await route.fulfill({json:{viewId:'fixture-events',version:1,reset:true,upserts:[{id:'event-1',ref:{provider:'synthetic',target:'demo',kind:eventKind.id,name:'pull-failed',uid:'event-1'},cells:[{text:'Failed'},{text:message}],health:{state:'warning',reason:'Failed',message}}],deleted:[],status:{state:'ready'}}})
  else await route.continue()
 })
 const grid=await openDemo(page);await grid.getByRole('gridcell',{name:'alpha',exact:true}).click()
 const events=page.getByRole('region',{name:'Events',exact:true}),cell=events.getByRole('gridcell',{name:message,exact:true})
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
})
