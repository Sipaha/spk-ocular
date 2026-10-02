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
