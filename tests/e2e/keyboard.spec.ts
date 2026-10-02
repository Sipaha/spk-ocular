import { expect } from '@playwright/test'
import { test } from './fixtures'

// The keyboard against the synthetic provider: areas (F6), views by arrows,
// the help (?), a table page, and the palette from the bottom panel.

test('F6 goes round the areas; views by arrows; ? shows the keys', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('option', { name: /^demo\b/ }).click()
  const nav = page.getByRole('navigation', { name: 'resources' })
  await nav.getByRole('button', { name: 'Services', exact: true }).click()
  await expect(page.getByRole('grid', { name: 'resources' }).getByRole('gridcell', { name: 'api', exact: true })).toBeVisible()

  await page.locator('body').click({ position: { x: 5, y: 5 } })
  await page.keyboard.press('F6')
  await expect(page.getByRole('listbox', { name: 'targets' })).toBeFocused()
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
  await page.keyboard.press('Escape')
  await expect(help).toBeHidden()
  await expect(page.locator('[data-table-scroll]').first()).toBeFocused()
})

test('the bottom panel opens the palette (a terminal keeps Ctrl+K)', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('option', { name: /^demo\b/ }).click()
  await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Services', exact: true }).click()
  const grid = page.getByRole('grid', { name: 'resources' })
  await grid.getByRole('gridcell', { name: 'api', exact: true }).click()
  await page.getByRole('dialog', { name: 'services api' }).getByRole('button', { name: 'Close' }).click()
  await page.keyboard.press('KeyL')
  const panel = page.getByRole('region', { name: 'Bottom panel' })
  await panel.getByRole('button', { name: 'Go to (Ctrl+K)' }).click()
  await expect(page.getByRole('dialog', { name: 'Go to' })).toBeVisible()
})
