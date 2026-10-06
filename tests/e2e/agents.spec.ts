import { connectSelected } from './fixtures'
import { expect, type Page } from '@playwright/test'
import { test } from './fixtures'
import { request } from 'node:http'
import { join } from 'node:path'
import { env, scratchRoot } from './fixtures'
import { token } from './synth'

// Agent access end to end (P14): the user grants in the UI, an agent
// talks to the real unix socket (Node http with socketPath), a destructive
// plan waits for the user's "Yes" in the page, the journal shows it all.
// Parcels are the synthetic scoped kind with actions (zones blue, green).

const socketPath = () => join(env(process.env.E2E_SYNTH_ROOT!).dataDir, 'agent.sock')

interface Answer {
  status: number
  body: Record<string, unknown>
}

/** One call of the agent socket as an agent would make it. */
function agent(method: string, body: unknown): Promise<Answer> {
  return new Promise((resolve, reject) => {
    const req = request({ socketPath: socketPath(), path: `/v1/${method}`, method: 'POST', headers: { 'content-type': 'application/json', 'X-Agent-Name': 'e2e-agent' } }, (res) => {
      let data = ''
      res.setEncoding('utf8')
      res.on('data', (c: string) => (data += c))
      res.on('end', () => resolve({ status: res.statusCode ?? 0, body: JSON.parse(data) as Record<string, unknown> }))
    })
    req.on('error', reject)
    req.end(JSON.stringify(body))
  })
}

/** A call of the page's own API (a write carries the page's Origin, as the page does). */
async function post(page: Page, path: string, data: unknown = {}) {
  const res = await page.request.post(path, { headers: { Authorization: `Bearer ${await token(page)}`, Origin: new URL(page.url()).origin }, data })
  expect(res.ok(), `${path}: ${res.status()} ${await res.text()}`).toBeTruthy()
}

const target = { provider: 'synthetic', target: 'demo' }
const parcel = (name: string, zone: string) => ({ ...target, scope: zone, kind: 'parcels', name })

test.afterEach(async ({ page }) => {
  await post(page, '/api/RevokeAllAgentGrants')
  await post(page, '/api/_test/synthetic/reset')
})

test('granted in the UI, an agent reads its zone only; its delete waits for Yes; the journal shows it', async ({ page }) => {
  await page.goto('/')
  await post(page, '/api/_test/synthetic/reset')
  await page.getByRole('option', { name: /^demo\b/ }).click()
  await connectSelected(page)

  // Nothing granted: the agent sees no target.
  expect((await agent('Access', {})).body.targets).toEqual([])

  await page.getByRole('button', { name: 'Agents', exact: true }).click()
  const panel = page.getByRole('dialog', { name: 'Agent access' })
  await expect(panel).toContainText('Agents connect over')
  const editor = panel.getByRole('region', { name: 'demo' })
  // Zones cannot be listed: typed.
  await editor.getByRole('textbox', { name: / name$/ }).fill('blue')
  await editor.getByRole('button', { name: 'Add', exact: true }).last().click()
  const blue = editor.getByRole('region', { name: /blue$/ })
  await expect(blue.getByRole('checkbox', { name: 'Read' })).toBeChecked()
  await blue.getByText('Execution', { exact: true }).click()
  await blue.getByRole('checkbox', { name: 'Delete (destructive)' }).click()
  const save = editor.getByRole('button', { name: 'Save' })
  await expect(save).toBeDisabled()
  await expect(editor.getByRole('status')).toContainText('choose at least one kind')
  await blue.getByRole('group', { name: 'Kinds' }).getByRole('checkbox', { name: 'Parcels' }).click()
  await save.click()
  await expect(panel.getByText('granted: 2')).toBeVisible()
  await panel.getByRole('button', { name: 'Close' }).click()

  // The agent: its grants, the granted zone's parcels, not the other zone's.
  const access = await agent('Access', {})
  expect(JSON.stringify(access.body)).toContain('"verb":"action:delete"')
  const list = await agent('ListObjects', { ...target, kind: 'parcels' })
  expect(list.status).toBe(200)
  const names = (list.body.rows as { ref: { name: string } }[]).map((r) => r.ref.name).sort()
  expect(names).toEqual(['parcel-1', 'parcel-2'])
  expect((await agent('GetObject', { ref: parcel('parcel-3', 'green') })).status).toBe(403)
  // No grant of stamp: refused before the provider is asked.
  expect((await agent('PrepareAction', { ref: parcel('parcel-1', 'blue'), action: 'stamp' })).status).toBe(403)

  // Delete: prepared, run, waits for the user.
  const prep = await agent('PrepareAction', { ref: parcel('parcel-1', 'blue'), action: 'delete' })
  expect(prep.status).toBe(200)
  const run = await agent('RunAction', { planId: prep.body.planId })
  expect(run.body.state).toBe('awaiting_confirmation')

  const dialog = page.getByRole('dialog', { name: 'Agent “e2e-agent” asks you to confirm' })
  await expect(dialog).toContainText('(not verified)')
  await expect(dialog).toContainText('Parcel parcel-1 is removed.')
  await expect(page).toHaveTitle('(1) SPK Ocular')
  await expect(dialog.getByRole('button', { name: 'No' })).toBeFocused()
  await dialog.getByRole('button', { name: 'Yes, run it' }).click()
  await expect(dialog).toBeHidden()
  await expect(page).toHaveTitle('SPK Ocular')

  const done = await agent('GetRun', { runId: run.body.runId })
  expect(done.body.state).toBe('done')
  expect((done.body.result as { message: string }).message).toBe('parcel parcel-1: deleted')
  const after = await agent('ListObjects', { ...target, kind: 'parcels' })
  expect((after.body.rows as { ref: { name: string } }[]).map((r) => r.ref.name)).toEqual(['parcel-2'])

  // The journal: the agent's reads, its refusal and its run.
  await page.getByRole('button', { name: 'Agents', exact: true }).click()
  await panel.getByRole('tab', { name: 'Journal' }).click()
  const journal = panel.getByRole('table', { name: 'Journal' })
  await expect(journal.getByRole('row').filter({ hasText: 'RunAction · action:delete' }).filter({ hasText: 'done' })).toBeVisible()
  await expect(journal.getByRole('row').filter({ hasText: 'waits for your confirmation' })).toBeVisible()
  await expect(journal.getByRole('row').filter({ hasText: 'refused · access denied' }).first()).toBeVisible()
  await expect(journal).toContainText('e2e-agent (not verified)')
})

test('revoked grants take effect at once', async ({ page }) => {
  await page.goto('/')
  await post(page, '/api/SaveAgentGrants', { ...target, grants: [{ scope: { mode: 'one', name: 'green' }, verb: 'read', kinds: null }] })
  const list = await agent('ListObjects', { ...target, kind: 'parcels' })
  expect((list.body.rows as { ref: { name: string } }[]).map((r) => r.ref.name)).toEqual(['parcel-3'])
  await page.getByRole('button', { name: 'Agents', exact: true }).click()
  const panel = page.getByRole('dialog', { name: 'Agent access' })
  await panel.getByRole('button', { name: /^demo(?!2)/ }).click()
  await panel.getByRole('region', { name: 'demo' }).getByRole('button', { name: 'Revoke all of this target' }).click()
  await expect(panel.getByText('granted: 1')).toBeHidden()
  expect((await agent('ListObjects', { ...target, kind: 'parcels' })).status).toBe(403)
})


test('contextual permissions preserve other scopes and require explicit grants', async ({ page }) => {
  await page.goto('/')
  await post(page, '/api/SaveAgentGrants', { ...target, grants: [{ scope: { mode: 'one', name: 'green' }, verb: 'read', kinds: ['parcels'] }] })
  await page.getByRole('option', { name: /^demo\b/ }).click()
  await connectSelected(page)
  await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Parcels', exact: true }).click()
  const zone = page.getByRole('textbox', { name: 'Zone', exact: true })
  await zone.fill('blue, green')
  await zone.press('Enter')
  await page.getByRole('button', { name: 'Agent permissions' }).click()
  const panel = page.getByRole('dialog', { name: 'Agent access' })
  const editor = panel.getByRole('region', { name: 'demo', exact: true })
  const blue = editor.getByRole('region', { name: 'Zone blue', exact: true })
  const green = editor.getByRole('region', { name: 'Zone green', exact: true })
  await expect(blue.getByRole('checkbox', { name: 'Read', exact: true })).not.toBeChecked()
  await expect(green.getByRole('checkbox', { name: 'Read', exact: true })).toBeChecked()
  await expect(editor.getByRole('button', { name: 'Save', exact: true })).toBeDisabled()
  expect((await agent('GetObject', { ref: parcel('parcel-1', 'blue') })).status).toBe(403)
  await blue.getByRole('checkbox', { name: 'Read', exact: true }).check()
  await blue.getByText('Execution', { exact: true }).click()
  await page.screenshot({ path: join(scratchRoot, 'agent-permissions.png') })
  await editor.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(editor.getByRole('button', { name: 'Save', exact: true })).toBeDisabled()
  const access = JSON.stringify((await agent('Access', {})).body)
  expect(access).toContain('"kinds":["parcels"]')
  expect((await agent('GetObject', { ref: parcel('parcel-1', 'blue') })).status).toBe(200)
  await panel.getByRole('button', { name: 'Close', exact: true }).click()
  await zone.fill('')
  await zone.press('Enter')
  await page.getByRole('button', { name: 'Agent permissions' }).click()
  await expect(editor.getByRole('region', { name: 'All zones (later ones too)', exact: true }).getByRole('checkbox', { name: 'Read', exact: true })).not.toBeChecked()
  await expect(editor.getByRole('button', { name: 'Save', exact: true })).toBeDisabled()
})

test('permissions layout keeps actions visible, traps focus and fits Russian text', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('option', { name: /^demo\b/ }).click()
  await connectSelected(page)
  await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Parcels', exact: true }).click()
  await expect(page.getByRole('textbox', { name: 'Filter resources', exact: true })).toBeVisible()
  await post(page, '/api/SaveAgentGrants', { ...target, grants: [
    { scope: { mode: 'one', name: 'blue' }, verb: 'read', kinds: null },
    { scope: { mode: 'one', name: 'green' }, verb: 'read', kinds: ['parcels'] },
  ] })
  await page.route('**/api/AppInfo', async (route) => {
    const response = await route.fetch()
    await route.fulfill({ json: { ...await response.json(), language: 'ru' } })
  })
  await page.reload()
  const trigger = page.getByRole('button', { name: 'Права агентов', exact: true })
  await trigger.click()
  const panel = page.getByRole('dialog', { name: 'Доступ агентов', exact: true })
  const save = panel.getByRole('button', { name: 'Сохранить', exact: true })
  await expect(save).toBeInViewport()
  await expect(panel.locator('[data-instruction]')).toBeHidden()
  const revoke = panel.getByRole('button', { name: 'Отозвать всё у цели', exact: true })
  await revoke.focus()
  await page.keyboard.press('Tab')
  await expect(panel.getByRole('tab').first()).toBeFocused()
  await page.mouse.move(0, 0)
  await page.screenshot({ path: join(scratchRoot, 'agent-permissions-ru.png') })
  await page.setViewportSize({ width: 800, height: 600 })
  await expect(save).toBeInViewport()
  expect(await panel.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true)
  await page.screenshot({ path: join(scratchRoot, 'agent-permissions-compact.png') })
  await panel.getByText('Подключение агента', { exact: true }).click()
  await expect(panel.locator('[data-instruction]')).toBeVisible()
  await expect(save).toBeInViewport()
  await page.keyboard.press('Escape')
  await expect(panel).toBeHidden()
  await expect(trigger).toBeFocused()
})

test('named groups union their access and both switches persist without deleting permissions', async ({ page }) => {
  await page.goto('/')
  await page.getByRole('option', { name: /^demo\b/ }).click()
  await connectSelected(page)
  await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Parcels', exact: true }).click()
  const zone = page.getByRole('textbox', { name: 'Zone', exact: true })
  await zone.fill('blue')
  await zone.press('Enter')
  await page.getByRole('button', { name: 'Agent permissions', exact: true }).click()
  const panel = page.getByRole('dialog', { name: 'Agent access', exact: true })
  const blue = panel.getByRole('region', { name: 'Zone blue', exact: true })
  let broad = blue.getByRole('region', { name: 'Group 1', exact: true })
  await broad.getByRole('textbox', { name: 'Group name' }).fill('Observation')
  broad = blue.getByRole('region', { name: 'Observation', exact: true })
  await broad.getByRole('checkbox', { name: 'Read', exact: true }).check()
  await blue.getByRole('button', { name: 'Add group', exact: true }).click()
  let limited = blue.getByRole('region', { name: 'Group 2', exact: true })
  await limited.getByRole('textbox', { name: 'Group name' }).fill('Parcels only')
  limited = blue.getByRole('region', { name: 'Parcels only', exact: true })
  await limited.getByRole('checkbox', { name: 'Read', exact: true }).check()
  await limited.getByRole('button', { name: 'Read: Kinds', exact: true }).click()
  const picker = limited.getByRole('group', { name: 'Kinds', exact: true })
  await picker.getByRole('checkbox', { name: 'All kinds', exact: true }).uncheck()
  await picker.getByRole('checkbox', { name: 'Parcels', exact: true }).check()
  await picker.getByRole('button', { name: 'Done', exact: true }).click()
  const save = panel.getByRole('button', { name: 'Save', exact: true })
  const persist = async () => {
    await expect(save).toBeEnabled()
    await save.click()
    await expect(save).toBeDisabled()
  }
  const read = (kind: string) => agent('ListObjects', { ...target, scope: 'blue', kind })
  await persist()
  expect((await read('crates')).status).toBe(200)
  expect((await read('parcels')).status).toBe(200)
  await page.mouse.move(0, 0)
  await page.screenshot({ path: join(scratchRoot, 'agent-named-groups.png') })
  await limited.getByRole('switch', { name: 'Enable group Parcels only' }).click()
  await persist()
  expect((await read('crates')).status).toBe(200)
  await limited.getByRole('switch', { name: 'Enable group Parcels only' }).click()
  await broad.getByRole('switch', { name: 'Enable group Observation' }).click()
  await persist()
  expect((await read('crates')).status).toBe(403)
  expect((await read('parcels')).status).toBe(200)
  await blue.getByRole('switch', { name: 'Access to Zone blue' }).click()
  await persist()
  expect((await read('parcels')).status).toBe(403)
  await page.reload()
  await page.getByRole('button', { name: 'Agents', exact: true }).click()
  await expect(blue.getByRole('switch', { name: 'Access to Zone blue' })).not.toBeChecked()
  await expect(broad.getByRole('switch', { name: 'Enable group Observation' })).not.toBeChecked()
  await expect(limited.getByRole('switch', { name: 'Enable group Parcels only' })).toBeChecked()
  await expect(limited.getByRole('checkbox', { name: 'Read', exact: true })).toBeChecked()
  await blue.getByRole('switch', { name: 'Access to Zone blue' }).click()
  await persist()
  expect((await read('parcels')).status).toBe(200)
  expect((await read('crates')).status).toBe(403)
})
