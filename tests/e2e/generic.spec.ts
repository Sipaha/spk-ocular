import { expect, test, type Page } from '@playwright/test'
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
