import { expect, test, type Locator, type Page } from '@playwright/test'
import { reconfigure, token } from './synth'

// Actions on the synthetic Workloads (web: 2 replicas; db: 1 and an
// autoscaler; paused: restart unavailable), steered by /api/_test/synthetic.

async function post(page: Page, path: string, data: unknown = {}) {
  const res = await page.request.post(path, { headers: { Authorization: `Bearer ${await token(page)}` }, data })
  expect(res.ok(), `${path}: ${res.status()}`).toBeTruthy()
}

const controls = (page: Page, c: Record<string, unknown>) => post(page, '/api/_test/synthetic/controls', c)
const mutate = (page: Page, m: Record<string, unknown>) => post(page, '/api/_test/synthetic/mutate', m)

async function openWorkloads(page: Page) {
  await page.goto('/')
  await post(page, '/api/_test/synthetic/reset')
  await page.getByRole('option', { name: /^demo\b/ }).click()
  await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Workloads', exact: true }).click()
  const grid = page.getByRole('grid', { name: 'resources' })
  await expect(grid.getByRole('row').filter({ hasText: 'web' })).toBeVisible()
  return grid
}

const row = (grid: Locator, name: string) => grid.getByRole('row').filter({ has: grid.page().getByRole('gridcell', { name, exact: true }) })
const cells = async (grid: Locator, name: string) => (await row(grid, name).getByRole('gridcell').allTextContents()).map((s) => s.trim())

test.afterEach(async ({ page }) => {
  await post(page, '/api/_test/synthetic/reset')
})

test('restart from the details: where, what, rights; the row and the status bar follow', async ({ page }) => {
  const grid = await openWorkloads(page)
  await row(grid, 'web').click()
  const drawer = page.getByRole('dialog', { name: 'workloads web' })
  await drawer.getByRole('button', { name: /Actions/ }).click()
  await page.getByRole('menu', { name: 'Actions' }).getByRole('menuitem', { name: 'Restart' }).click()
  const dialog = page.getByRole('dialog', { name: 'Restart web' })
  const where = dialog.getByLabel('where')
  await expect(where).toContainText('Contextdemo')
  await expect(where).toContainText('Serversynthetic.local')
  await expect(where).toContainText('KindWorkloadName') // the kind's singular
  await expect(where).toContainText('Nameweb')
  await expect(dialog).toContainText('Its instances are replaced one by one.')
  await expect(dialog).toContainText('Permission: checked: allowed')
  const confirm = dialog.getByRole('button', { name: 'Restart' })
  await expect(confirm).toBeFocused()
  await confirm.click()
  await expect(dialog).toBeHidden()
  await expect(page.getByRole('status')).toHaveText('workload web: restart requested')
  await expect.poll(() => cells(grid, 'web')).toEqual(['web', '2', '1'])
  // Esc on the menu closes only the menu, not the details.
  await drawer.getByRole('button', { name: /Actions/ }).click()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('menu')).toBeHidden()
  await expect(drawer).toBeVisible()
})

test('scale from the row menu in two steps: the count, then its review', async ({ page }) => {
  const grid = await openWorkloads(page)
  await row(grid, 'db').click({ button: 'right' })
  const menu = page.getByRole('menu', { name: 'Row actions' })
  await expect(menu.getByRole('menuitem')).toHaveText(['Details', 'Restart', 'Scale…', 'Roll back…', 'Pause rollout', 'Resume', 'Delete', 'Evacuate'])
  await menu.getByRole('menuitem', { name: 'Scale…' }).click()
  const dialog = page.getByRole('dialog', { name: 'Scale db' })
  const count = dialog.getByRole('textbox')
  await expect(count).toHaveValue('1')
  await expect(count).toBeFocused()
  await count.fill('3')
  await count.press('Enter') // reviews, never runs
  await expect(dialog).toContainText('1 → 3: 2 instances are added.')
  await expect(dialog).toContainText('An autoscaler may override the count.')
  expect(await cells(grid, 'db')).toEqual(['db', '1', '0'])
  await dialog.getByRole('button', { name: 'Scale' }).click()
  await expect(dialog).toBeHidden()
  await expect.poll(() => cells(grid, 'db')).toEqual(['db', '3', '0'])
})

test('roll back to a chosen revision: the choices, the review of each, the changes', async ({ page }) => {
  const grid = await openWorkloads(page)
  await row(grid, 'web').click({ button: 'right' })
  await page.getByRole('menu', { name: 'Row actions' }).getByRole('menuitem', { name: 'Roll back…' }).click()
  const dialog = page.getByRole('dialog', { name: 'Roll back web' })
  const choices = dialog.getByRole('radiogroup', { name: 'Choose' })
  const radios = choices.getByRole('radio')
  await expect(radios).toHaveCount(3)
  await expect(radios.nth(0)).toBeDisabled() // the current template
  await expect(choices.getByText('current')).toBeVisible()
  await expect(dialog).toContainText('Choose a revision to see what changes.')
  await expect(dialog.getByRole('button', { name: 'Roll back' })).toBeDisabled()
  await expect(radios.nth(1)).toBeFocused()
  await page.keyboard.press('ArrowDown') // revision 1, reviewed at once
  await expect(radios.nth(2)).toBeChecked()
  await expect(dialog.getByRole('region', { name: 'Changes' })).toContainText('image: app:3 → app:1')
  await expect(dialog).toContainText('it becomes revision 4')
  await dialog.getByRole('button', { name: 'Roll back' }).click()
  await expect(dialog).toBeHidden()
  await expect(page.getByRole('status')).toHaveText('workload web: rollback to revision 1 requested')

  await row(grid, 'web').click({ button: 'right' })
  await page.getByRole('menu', { name: 'Row actions' }).getByRole('menuitem', { name: 'Roll back…' }).click()
  await expect(radios.nth(0)).toBeDisabled()
  await expect(dialog.getByRole('radiogroup').locator('label').nth(0)).toContainText('Revision 4')
  await page.keyboard.press('Escape')
  await expect(dialog).toBeHidden()
})

test('pause and resume a rollout; a paused one is not rolled back', async ({ page }) => {
  const grid = await openWorkloads(page)
  const menuItem = async (name: string, item: string) => {
    await row(grid, name).click({ button: 'right' })
    await page.getByRole('menu', { name: 'Row actions' }).getByRole('menuitem', { name: item }).click()
  }
  await menuItem('web', 'Pause rollout')
  let dialog = page.getByRole('dialog', { name: 'Pause rollout web' })
  await expect(dialog).toContainText('Template changes are not rolled out until it is resumed.')
  await dialog.getByRole('button', { name: 'Pause rollout' }).click()
  await expect(page.getByRole('status')).toHaveText('workload web: rollout pause requested')

  await menuItem('web', 'Roll back…')
  dialog = page.getByRole('dialog', { name: 'Roll back web' })
  await expect(dialog.getByRole('alert')).toContainText('web is paused: resume it first')
  for (const r of await dialog.getByRole('radio').all()) await expect(r).toBeDisabled()
  await page.keyboard.press('Escape')

  await menuItem('web', 'Resume')
  dialog = page.getByRole('dialog', { name: 'Resume web' })
  await expect(dialog).toContainText('changes made while paused (if any) are rolled out')
  await dialog.getByRole('button', { name: 'Resume' }).click()
  await expect(page.getByRole('status')).toHaveText('workload web: resume requested')
})

test('Delete on the table: a destructive review with Cancel focused; the row goes', async ({ page }) => {
  const grid = await openWorkloads(page)
  await page.getByRole('textbox', { name: 'Filter rows' }).click()
  await page.keyboard.press('Delete') // in the filter: nothing
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await page.keyboard.press('ArrowDown') // into the table
  await page.keyboard.press('ArrowDown') // db
  await page.keyboard.press('ArrowDown') // paused
  await page.keyboard.press('ArrowDown') // web
  await page.keyboard.press('Delete')
  const dialog = page.getByRole('dialog', { name: 'Delete web' })
  await expect(dialog).toContainText('Its 2 instances are removed too.')
  await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeFocused()
  await expect(dialog.getByRole('button', { name: 'Delete' })).toHaveClass(/bg-danger/)
  await dialog.getByRole('button', { name: 'Delete' }).click()
  await expect(dialog).toBeHidden()
  await expect(row(grid, 'web')).toHaveCount(0)
  await expect(page.getByRole('status')).toHaveText('workload web: deletion requested')
})

test('Shift+F10 opens the row menu at the selected row; Esc returns to the table', async ({ page }) => {
  const grid = await openWorkloads(page)
  await page.getByRole('textbox', { name: 'Filter rows' }).click()
  await page.keyboard.press('ArrowDown')
  await page.keyboard.press('ArrowDown')
  await page.keyboard.press('Shift+F10')
  const menu = page.getByRole('menu', { name: 'Row actions' })
  await expect(menu.getByRole('menuitem', { name: 'Details' })).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(menu).toBeHidden()
  await expect(grid.locator('[data-table-scroll]')).toBeFocused()
})

test('paused: restart is unavailable and explained', async ({ page }) => {
  const grid = await openWorkloads(page)
  await row(grid, 'paused').click({ button: 'right' })
  await page.getByRole('menu').getByRole('menuitem', { name: 'Restart' }).click()
  const dialog = page.getByRole('dialog', { name: 'Restart paused' })
  await expect(dialog.getByRole('alert')).toHaveText('Not possible now: paused is paused: resume it first')
  await expect(dialog.getByRole('button', { name: 'Restart' })).toBeDisabled()
})

test('denied rights: said in the review, the action cannot be confirmed', async ({ page }) => {
  const grid = await openWorkloads(page)
  await controls(page, { rights: 'denied' })
  await row(grid, 'web').click({ button: 'right' })
  await page.getByRole('menu').getByRole('menuitem', { name: 'Restart' }).click()
  const dialog = page.getByRole('dialog', { name: 'Restart web' })
  await expect(dialog).toContainText('Permission: not allowed: you may not restart workloads (synthetic)')
  await expect(dialog.getByRole('button', { name: 'Restart' })).toBeDisabled()
})

test('a failed run stays in the dialog and changes nothing', async ({ page }) => {
  const grid = await openWorkloads(page)
  await controls(page, { fail: 'internal' })
  await row(grid, 'web').click({ button: 'right' })
  await page.getByRole('menu').getByRole('menuitem', { name: 'Restart' }).click()
  const dialog = page.getByRole('dialog', { name: 'Restart web' })
  await dialog.getByRole('button', { name: 'Restart' }).click()
  await expect(dialog.getByRole('alert')).toHaveText('Failed · internal error: the run failed (synthetic)')
  await expect(dialog.getByRole('button', { name: 'Restart' })).toHaveCount(0)
  expect(await cells(grid, 'web')).toEqual(['web', '2', '0'])
  await dialog.getByRole('button', { name: 'Close' }).click()
  await expect(dialog).toBeHidden()
})

test('an unknown outcome: check before repeating; the object did change', async ({ page }) => {
  const grid = await openWorkloads(page)
  await controls(page, { fail: 'unknown', delay_ms: 400 })
  await row(grid, 'web').click({ button: 'right' })
  await page.getByRole('menu').getByRole('menuitem', { name: 'Delete' }).click()
  const dialog = page.getByRole('dialog', { name: 'Delete web' })
  await dialog.getByRole('button', { name: 'Delete' }).click()
  // Running: Esc does not close, the buttons wait.
  await expect(dialog).toContainText('Requesting…')
  await page.keyboard.press('Escape')
  await expect(dialog).toBeVisible()
  await expect(dialog.getByRole('alert')).toContainText('outcome is not known. Check the object before repeating.')
  await expect(dialog.getByRole('button', { name: 'Delete' })).toHaveCount(0)
  await expect(row(grid, 'web')).toHaveCount(0)
})

test('another actor changes the object after the review: review again', async ({ page }) => {
  const grid = await openWorkloads(page)
  await row(grid, 'web').click({ button: 'right' })
  await page.getByRole('menu').getByRole('menuitem', { name: 'Scale…' }).click()
  const dialog = page.getByRole('dialog', { name: 'Scale web' })
  await dialog.getByRole('textbox').fill('3')
  await dialog.getByRole('textbox').press('Enter')
  await expect(dialog).toContainText('2 → 3')
  await mutate(page, { object: 'web', replicas: 7 })
  await expect.poll(() => cells(grid, 'web')).toEqual(['web', '7', '0'])
  await dialog.getByRole('button', { name: 'Scale' }).click()
  await expect(dialog.getByRole('alert')).toContainText('changed since this was reviewed')
  expect(await cells(grid, 'web')).toEqual(['web', '7', '0'])
  await dialog.getByRole('button', { name: 'Review again' }).click()
  await expect(dialog).toContainText('7 → 3: 4 instances are removed.')
  await dialog.getByRole('button', { name: 'Scale' }).click()
  await expect(dialog).toBeHidden()
  await expect.poll(() => cells(grid, 'web')).toEqual(['web', '3', '0'])
})

test('the context configuration changes after the review: review again', async ({ page }) => {
  const grid = await openWorkloads(page)
  await row(grid, 'web').click({ button: 'right' })
  await page.getByRole('menu').getByRole('menuitem', { name: 'Restart' }).click()
  const dialog = page.getByRole('dialog', { name: 'Restart web' })
  await expect(dialog.getByRole('button', { name: 'Restart' })).toBeEnabled()
  await reconfigure(page)
  await dialog.getByRole('button', { name: 'Restart' }).click()
  await expect(dialog.getByRole('alert')).toHaveText('the configuration of demo changed since the action was reviewed; review it again')
  expect(await cells(grid, 'web')).toEqual(['web', '2', '0'])
  await dialog.getByRole('button', { name: 'Review again' }).click()
  await dialog.getByRole('button', { name: 'Restart' }).click()
  await expect(dialog).toBeHidden()
  await expect.poll(() => cells(grid, 'web')).toEqual(['web', '2', '1'])
})

test('a plan with long lists and a run of many parts: every name reachable, the dialog stays when not all was done', async ({ page }) => {
  const grid = await openWorkloads(page)
  await controls(page, { items: 120, refuse: 1 })
  await row(grid, 'web').click({ button: 'right' })
  await page.getByRole('menu').getByRole('menuitem', { name: 'Evacuate' }).click()
  const dialog = page.getByRole('dialog', { name: 'Evacuate web' })
  // Only the plan scrolls: what and where stay above it, the buttons below.
  const framed = async () => {
    await expect(dialog.getByRole('heading', { name: /Evacuate · web/ })).toBeInViewport({ ratio: 1 })
    await expect(dialog.getByRole('definition').filter({ hasText: /^web$/ })).toBeInViewport({ ratio: 1 })
    await expect(dialog.getByRole('button', { name: /^(Cancel|Close)$/ })).toBeInViewport({ ratio: 1 })
  }
  const moved = dialog.getByRole('region', { name: 'Moved (120)' })
  await expect(moved.getByRole('listitem')).toHaveCount(50)
  await framed()
  await moved.getByRole('button', { name: 'Show 50 more (70 left)' }).click()
  await moved.getByRole('button', { name: 'Show 20 more (20 left)' }).click()
  await expect(moved.getByRole('listitem')).toHaveCount(120)
  await expect(moved.getByRole('listitem').last()).toHaveText('web-120')
  await moved.getByRole('listitem').last().scrollIntoViewIfNeeded()
  await framed()
  await expect(dialog.getByRole('button', { name: 'Evacuate' })).toBeInViewport({ ratio: 1 })
  const left = dialog.getByRole('region', { name: 'Left alone (1)' })
  await expect(left.getByRole('listitem')).toHaveCount(0)
  await left.getByRole('button', { name: 'Left alone (1)' }).click()
  await expect(left.getByRole('listitem')).toHaveText('web-helper · it stays')

  await dialog.getByRole('button', { name: 'Evacuate' }).click()
  await expect(dialog.getByRole('alert')).toContainText('Not everything was done: a part was refused')
  await expect(dialog.getByRole('alert')).toBeInViewport({ ratio: 1 }) // the outcome is brought into view below the long plan
  await framed()
  const result = dialog.getByRole('list', { name: 'Result' })
  await expect(result.getByRole('listitem')).toHaveCount(50)
  await dialog.getByRole('button', { name: 'Show 50 more (71 left)' }).click()
  await dialog.getByRole('button', { name: 'Show 21 more (21 left)' }).click()
  await expect(result.getByRole('listitem').nth(119)).toHaveText('web-120Refused · refused (synthetic)')
  await expect(result.getByRole('listitem').nth(120)).toHaveText('web-helperNot run · left alone')
  await result.getByRole('listitem').nth(120).scrollIntoViewIfNeeded()
  await framed()
  await expect.poll(() => cells(grid, 'web')).toEqual(['web', '2', '1'])
})
