import { expect, test } from '@playwright/test'
import { token } from './synth'

// The synthetic Problems view: rows point at Services and Workloads, one is
// evidence, one source could not be observed.

async function openProblems(page: import('@playwright/test').Page) {
  await page.goto('/')
  await page.request.post('/api/_test/synthetic/reset', { headers: { Authorization: `Bearer ${await token(page)}` } })
  await page.getByRole('option', { name: /^demo\b/ }).click()
  await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Problems', exact: true }).click()
  const grid = page.getByRole('grid', { name: 'resources' })
  await expect(grid.getByRole('gridcell', { name: 'api', exact: true })).toBeVisible()
  return grid
}

test('the worst first; evidence quieter; what could not be observed is said', async ({ page }) => {
  const grid = await openProblems(page)
  const names = await grid.getByRole('row').locator('[role=gridcell]:nth-child(3)').allTextContents()
  expect(names).toEqual(['api', 'db', 'workers'])
  await expect(grid.getByRole('gridcell', { name: /recent/ })).toHaveClass(/text-fg-subtle/)
  await expect(page.getByRole('note', { name: 'Coverage' })).toHaveText('Not observed: Nodes (access denied)')
})

test("a row's menu is its object's kind's: a workload offers its actions", async ({ page }) => {
  const grid = await openProblems(page)
  await grid.getByRole('gridcell', { name: 'db', exact: true }).click({ button: 'right' })
  const menu = page.getByRole('menu', { name: 'Row actions' })
  await expect(menu.getByRole('menuitem')).toHaveText(['Details', 'Restart', 'Scale…', 'Delete'])
  await menu.getByRole('menuitem', { name: 'Restart' }).click()
  const dialog = page.getByRole('dialog', { name: 'Restart db' })
  await expect(dialog.getByText('Workload', { exact: true })).toBeVisible() // the kind's singular
  await dialog.getByRole('button', { name: 'Cancel' }).click()

  await grid.getByRole('gridcell', { name: 'api', exact: true }).click({ button: 'right' })
  await expect(menu.getByRole('menuitem')).toHaveText(['Details', 'Logs', 'Terminal'])
})
