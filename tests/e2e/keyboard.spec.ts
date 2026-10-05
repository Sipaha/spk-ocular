import { connectSelected } from './fixtures'
import { expect } from '@playwright/test'
import { test } from './fixtures'

// The keyboard against the synthetic provider: areas (F6), views by arrows,
// the help (?), a table page, and the app-wide palette beside a terminal.

test('F6 goes round the areas; views by arrows; ? shows the keys', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('option', { name: /^demo\b/ }).click()
  await connectSelected(page)
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

test('the app header opens the palette while a terminal keeps Ctrl+K', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('option', { name: /^demo\b/ }).click()
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
  await page.getByRole('option', { name: /^demo\b/ }).click()
  await connectSelected(page)
  await expect(page.getByLabel('Current target', { exact: true })).toHaveText('demo')

  const go = page.getByRole('button', { name: 'Go to (Ctrl+K)' })
  await go.click()
  await expect(page.getByRole('dialog', { name: 'Go to' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(go).toBeFocused()
  const help = page.getByRole('button', { name: 'Keyboard', exact: true })
  await help.click()
  await expect(page.getByRole('dialog', { name: 'Keyboard' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(help).toBeFocused()

  await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Services', exact: true }).click()
  await page.getByRole('gridcell', { name: 'api', exact: true }).click()
  await page.getByRole('dialog', { name: 'services api' }).getByRole('button', { name: 'Logs', exact: true }).click()
  const panel = page.getByRole('region', { name: 'Bottom panel' })
  await expect(panel.getByLabel('line count')).toHaveText('5 of 6 lines')
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
