import { expect, type Page } from '@playwright/test'
import { test } from './fixtures'

// The Ctrl+K palette against the synthetic provider: views by alias, rows of
// the current table, recent objects (persisted in SQLite), a context switch.

async function openDemo(page: Page) {
  await page.goto('/')
  await page.getByRole('option', { name: /^demo\b/ }).click()
  await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Services', exact: true }).click()
  await expect(page.getByRole('grid', { name: 'resources' }).getByRole('gridcell', { name: 'workers', exact: true })).toBeVisible()
}

async function palette(page: Page) {
  await page.keyboard.press('Control+k')
  const dlg = page.getByRole('dialog', { name: 'Go to' })
  await expect(dlg.getByRole('combobox', { name: 'Go to' })).toBeFocused()
  return dlg
}

test('a view by its alias, a row of the table, then the same object as recent from another view', async ({ page }) => {
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

  // Workloads has no "workers" row: it is offered as recently opened.
  dlg = await palette(page)
  const recent = dlg.getByRole('option').filter({ hasText: 'workers' })
  await expect(recent).toContainText('Recent')
  await recent.click()
  await expect(page.getByRole('dialog', { name: 'services workers' })).toBeVisible()
  await expect(page.getByRole('heading', { name: 'Workloads' })).toBeVisible()
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
  await page.getByRole('option', { name: /^demo\b/ }).click()
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
