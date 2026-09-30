import { expect, test, type Page } from '@playwright/test'
import { request } from 'node:http'
import { join } from 'node:path'
import { env } from './fixtures'
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
  await panel.getByRole('button', { name: /^demo/ }).click()
  await panel.getByRole('region', { name: 'demo' }).getByRole('button', { name: 'Revoke all of this target' }).click()
  await expect(panel.getByText('granted: 1')).toBeHidden()
  expect((await agent('ListObjects', { ...target, kind: 'parcels' })).status).toBe(403)
})
