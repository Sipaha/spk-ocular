import { connectSelected } from './fixtures'
import { expect, type Page } from '@playwright/test'
import { test } from './fixtures'
import { token } from './synth'

// The generic UI takes everything from the provider's metadata (P6 Task 1):
// the synthetic target opens its default view (Crates, not pods) in its
// default scope ("blue" — a zone, not a namespace), its scopes cannot be
// listed (typed), objects are keyed apart from their titles, there are no
// events, a terminal has nowhere to run, and views can be read again (F5).

// Other specs leave demo's last view and scope remembered: forget them
// (from the page: the API wants its Origin).
async function forgetDemo(page: Page) {
  await page.goto('/')
  const tok = await token(page)
  const ok = await page.evaluate(async (tok) => {
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
  await page.getByRole('option', { name: /^demo\b/ }).click()
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
  await page.getByRole('option', { name: /^demo\b/ }).click()
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
  await drawer.getByRole('tab', { name: 'YAML' }).click()
  await expect(drawer.getByText('key: crate-7f3a')).toBeVisible()
  await drawer.getByRole('tab', { name: 'Details' }).click()
  await expect(drawer.getByRole('region', { name: 'Events' })).toHaveCount(0)
  await drawer.getByRole('button', { name: 'Terminal…' }).click()
  const term = page.getByRole('dialog', { name: 'Open a terminal' })
  await expect(term).toContainText('Nowhere to run it now')
  await expect(term).toContainText('alpha')
})

test('Read again: the button and F5 (also from a field) go to the open view', async ({ page }) => {
  const grid = await openDemo(page)
  const reads = () => grid.getByRole('row').filter({ hasText: 'alpha' }).getByRole('gridcell').last() // Zone is hidden: one zone shown
  const before = Number(await reads().textContent())
  await page.getByRole('button', { name: /Read again/ }).click()
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
  await page.getByRole('option', { name: /^demo\b/ }).click()
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
  await page.getByRole('option', { name: /^demo2\b/ }).click()
  await connectSelected(page)
  await page.getByRole('option', { name: /^demo\b/ }).click()
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
    await page.getByRole('option', { name: /^demo2\b/ }).click()
    await connectSelected(page)
    await expect(favorites.getByRole('button', { name: 'Services', exact: true })).toBeVisible()
    await expect(nav.getByRole('button', { name: 'Services', exact: true })).toHaveCount(1)
    await favorites.getByRole('button', { name: 'Services', exact: true }).focus()
    await page.keyboard.press('Shift+F10')
    await page.getByRole('menuitem', { name: 'Remove Services from favorites', exact: true }).click()
    await expect(favorites.getByRole('button', { name: 'Services', exact: true })).toHaveCount(0)
    await expect(nav.getByRole('region', { name: 'Synthetic', exact: true }).getByRole('button', { name: 'Services', exact: true })).toBeVisible()
    await page.getByRole('option', { name: /^demo\b/ }).click()
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
    await page.getByRole('option', { name: /^demo2\b/ }).click()
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
  await expect(page.getByRole('option', { name: /^demo\b/ })).toHaveAttribute('aria-selected', 'true')
  await expect(grid.getByRole('gridcell', { name: 'alpha', exact: true })).toBeVisible()
})
