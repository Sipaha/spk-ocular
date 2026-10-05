import { expect } from '@playwright/test'
import { test } from './fixtures'
import { token } from './synth'
import type { Page as ResourcePage } from '../../web/src/api/types'

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
  const names = await grid.getByRole('row').locator('[role=gridcell]:nth-child(4)').allTextContents()
  expect(names).toEqual(['api', 'db', 'workers'])
  await expect(grid.getByRole('gridcell', { name: /recent/ })).toHaveClass(/text-fg-subtle/)
  await expect(page.getByRole('note', { name: 'Not observed' })).toHaveText('Not observed: Nodes (access denied)')
  await expect(page.getByRole('note', { name: 'Coverage' })).toHaveText('Checked: Services, Workloads, Nodes')
})

test('first opening shows one loading state before the table and coverage', async ({ page }, testInfo) => {
  await page.goto('/')
  await page.request.post('/api/_test/synthetic/reset', { headers: { Authorization: `Bearer ${await token(page)}` } })
  await page.getByRole('option', { name: /^demo\b/ }).click()
  const nav = page.getByRole('navigation', { name: 'resources' })
  await nav.getByRole('button', { name: 'Services', exact: true }).click()
  await expect(page.getByRole('grid', { name: 'resources' })).toBeVisible()

  let problemsView = ''
  let partialSent = false
  let release!: () => void
  const snapshot = new Promise<void>((resolve) => { release = resolve })
  await page.route('**/api/OpenView', async (route) => {
    const response = await route.fetch()
    if (route.request().postDataJSON().query.kind === 'problems') problemsView = (await response.json()).viewId
    await route.fulfill({ response })
  })
  await page.route('**/api/GetRows', async (route) => {
    if (problemsView && route.request().postDataJSON().viewId === problemsView && !partialSent) {
      await snapshot
      const response = await route.fetch()
      const body: ResourcePage = await response.json()
      partialSent = true
      body.status = { ...body.status, state: 'loading', coverage: body.status.coverage?.map((source) => source.source === 'Nodes' ? { source: 'Nodes', state: 'loading' } : source) }
      await route.fulfill({ response, json: body })
      return
    }
    await route.continue()
  })
  try {
    await nav.getByRole('button', { name: 'Problems', exact: true }).click()
    await expect(page.getByRole('heading', { name: 'Problems', exact: true })).toBeVisible()
    await expect(page.getByRole('status', { name: 'Loading…', exact: true })).toHaveText('Loading Problems…')
    await expect(page.getByRole('grid', { name: 'resources' })).toHaveCount(0)
    await expect(page.getByRole('note', { name: 'Not observed' })).toHaveCount(0)
    await expect(page.getByRole('note', { name: 'Coverage', exact: true })).toHaveCount(0)
    await page.screenshot({ path: testInfo.outputPath('problems-initial-loading.png') })
  } finally {
    release()
  }
  await expect(page.getByRole('grid', { name: 'resources' }).getByRole('gridcell', { name: 'api', exact: true })).toBeVisible()
  await expect(page.locator('.resource-toolbar').getByRole('status', { name: 'Loading…', exact: true })).toBeVisible()
  await expect(page.getByRole('note', { name: 'Not observed' })).toHaveCount(0)
  await page.screenshot({ path: testInfo.outputPath('problems-partial-loading.png') })
  await page.getByRole('button', { name: /Read again/ }).click()
  await expect(page.getByRole('status', { name: 'Loading…', exact: true })).toHaveCount(0)
  await expect(page.getByRole('note', { name: 'Not observed' })).toHaveText('Not observed: Nodes (access denied)')
  await page.screenshot({ path: testInfo.outputPath('problems-ready.png') })
})

test("a row's menu is its object's kind's: a workload offers its actions", async ({ page }) => {
  const grid = await openProblems(page)
  await grid.getByRole('gridcell', { name: 'db', exact: true }).click({ button: 'right' })
  const menu = page.getByRole('menu', { name: 'Row actions' })
  await expect(menu.getByRole('menuitem')).toHaveText(['Details', 'Restart', 'Scale…', 'Roll back…', 'Pause rollout', 'Resume', 'Delete', 'Force delete', 'Evacuate', 'Debug…'])
  await menu.getByRole('menuitem', { name: 'Restart' }).click()
  const dialog = page.getByRole('dialog', { name: 'Restart db' })
  await expect(dialog.getByText('Workload', { exact: true })).toBeVisible() // the kind's singular
  await dialog.getByRole('button', { name: 'Cancel' }).click()

  await grid.getByRole('gridcell', { name: 'api', exact: true }).click({ button: 'right' })
  await expect(menu.getByRole('menuitem')).toHaveText(['Details', 'Logs', 'Terminal'])
})
