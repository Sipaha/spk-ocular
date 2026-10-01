import { expect, test, type Page } from '@playwright/test'

async function token(page: Page) {
  return (await page.locator('meta[name="spk-ocular-api-token"]').getAttribute('content'))!
}

async function emit(page: Page, target: string, object: string, event: Record<string, unknown>) {
  const res = await page.request.post('/api/_test/logs/emit', { headers: { Authorization: `Bearer ${await token(page)}` }, data: { target, object, event: { source: -1, ...event } } })
  expect(res.ok()).toBeTruthy()
  return ((await res.json()) as { delivered: number }).delivered
}

// P18: two warm synthetic targets (the test server runs with a second one).
// A's page state and log tab survive A → B → A; the targets' log streams are
// independent; «Close connection» ends A's session, its dot and its tab.
test('two warm targets: A → B → A keeps everything; Close connection ends A', async ({ page }) => {
  await page.goto('/')
  const demo = page.getByRole('option', { name: /^demo\b/ })
  const demo2 = page.getByRole('option', { name: /^demo2\b/ })
  const services = () => page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Services', exact: true })
  const grid = () => page.getByRole('grid', { name: 'resources' })
  const tabs = page.getByRole('tab', { name: /services\/api/ })
  const state = () => page.locator('[role=tabpanel]:not([hidden])').getByLabel('stream state')
  // A tab that is not selected wears its target's badge (foreign): only then
  // the badge text is present.
  const tabAOnB = tabs.filter({ has: page.getByText('demo', { exact: true }) })

  await demo.click()
  await services().click()

  // A's page: a filter, a sort, open details and a log tab.
  await page.locator('[data-primary-filter]').fill('api')
  await grid().getByRole('columnheader', { name: 'Sources' }).click()
  await grid().getByRole('columnheader', { name: 'Sources' }).click() // descending
  await expect(grid().getByRole('columnheader', { name: 'Sources' })).toHaveAttribute('aria-sort', 'descending')
  await grid().getByRole('gridcell', { name: 'api', exact: true }).click()
  const drawer = page.getByRole('dialog', { name: 'services api' })
  await expect(drawer.getByRole('heading', { name: 'api' })).toBeVisible()
  await page.keyboard.press('l')
  await expect(tabs).toHaveCount(1)
  await expect(state()).toHaveText('Live')
  expect(await emit(page, 'demo', 'api', { lines: ['warm line one'] })).toBe(1)
  await expect(page.getByText('warm line one')).toBeVisible()

  // B: the other target; its own log tab streams its own feed.
  await demo2.click()
  await services().click()
  await grid().getByRole('gridcell', { name: 'api', exact: true }).click()
  await page.keyboard.press('l')
  await expect(tabs).toHaveCount(2)
  await expect(tabAOnB).toBeVisible() // A's tab is foreign now: badge "demo"
  await expect(state()).toHaveText('Live')

  // Both streams live: each feed reaches only its own tab, whichever is selected.
  expect(await emit(page, 'demo', 'api', { lines: ['warm line two'] })).toBe(1)
  expect(await emit(page, 'demo2', 'api', { lines: ['a line of B'] })).toBe(1)
  await expect(page.getByText('a line of B')).toBeVisible()
  await expect(page.getByText('warm line two')).toHaveCount(0)
  await tabAOnB.click()
  await expect(page.getByText('warm line two')).toBeVisible()
  await expect(page.getByText('a line of B')).toHaveCount(0)

  // Back to A: the page comes back as it was left.
  await demo.click()
  await expect(page.locator('[data-primary-filter]')).toHaveValue('api')
  await expect(grid().getByRole('columnheader', { name: 'Sources' })).toHaveAttribute('aria-sort', 'descending')
  await expect(drawer.getByRole('heading', { name: 'api' })).toBeVisible()
  await tabs.first().click()
  await expect(page.getByText('warm line one')).toBeVisible()
  await expect(page.getByText('warm line two')).toBeVisible()

  // «Close connection» is not offered on the selected target: from B it is.
  await demo2.click()
  await demo.click({ button: 'right' })
  await page.getByRole('menuitem', { name: 'Close connection' }).click()
  // The dot is gone (no tooltip), the tab ended with the reason.
  await expect(demo).not.toHaveAttribute('title', /Connection open/)
  await tabAOnB.click()
  await expect(state()).toContainText('the connection to the target was closed')
})
