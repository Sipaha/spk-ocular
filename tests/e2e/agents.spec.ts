import { selectConnection } from './fixtures'
import { connectSelected } from './fixtures'
import { expect, type Page } from '@playwright/test'
import { test } from './fixtures'
import { join } from 'node:path'
import { env, scratchRoot } from './fixtures'
import { token } from './synth'
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { languages } from '../../web/src/languages'
import { execFileSync } from 'node:child_process'

test('journal filter owns pending pages and ignores an older pagination failure', async ({ page }) => {
  const entry = (id: number, agent: string) => ({ id, agent, at: '2026-10-12T12:34:56Z', method: 'ListObjects', phase: 'read', count: 1 })
  let releaseOld: (() => void) | undefined
  let releaseNew: (() => void) | undefined
  let oldStarted = false
  let newStarted = false
  await page.route('**/api/ListAgentAudit', async route => {
    const filter = route.request().postDataJSON()
    if (filter.before) {
      oldStarted = true
      await new Promise<void>(resolve => { releaseOld = resolve })
      await route.fulfill({ status: 500, json: { code: 'Internal', message: 'obsolete pagination failure' } })
    } else if (filter.agent) {
      newStarted = true
      await new Promise<void>(resolve => { releaseNew = resolve })
      await route.fulfill({ json: [entry(700, 'audit-selected')] })
    } else await route.fulfill({ json: Array.from({ length: 200 }, (_, i) => entry(1000 - i, i ? 'audit-old' : 'audit-selected')) })
  })
  try {
    await page.goto('/')
    await page.getByRole('button', { name: 'Agents', exact: true }).click()
    const panel = page.getByRole('dialog', { name: 'Agent access' })
    await panel.getByRole('tab', { name: 'Journal', exact: true }).click()
    const table = panel.getByRole('table', { name: 'Journal' })
    await expect(table.locator('tbody tr')).toHaveCount(200)
    await panel.getByRole('button', { name: 'More', exact: true }).click()
    await expect.poll(() => oldStarted).toBeTruthy()
    await expect(panel.getByRole('button', { name: 'More', exact: true })).toBeDisabled()
    await panel.getByRole('button', { name: 'Agent', exact: true }).click()
    await page.getByRole('option', { name: 'audit-selected', exact: true }).click()
    await expect.poll(() => newStarted).toBeTruthy()
    await expect(table.locator('tbody tr')).toHaveCount(0)
    releaseOld!()
    await expect(panel.getByRole('alert')).toHaveCount(0)
    const captures = join(scratchRoot, 'audit-screenshots')
    mkdirSync(captures, { recursive: true })
    await page.screenshot({ path: join(captures, 'journal-new-filter-pending.png') })
    releaseNew!()
    await expect(table.locator('tbody tr')).toHaveCount(1)
    await expect(table).toContainText('audit-selected')
    await expect(table).not.toContainText('audit-old')
    await expect(panel.getByRole('alert')).toHaveCount(0)
    await page.screenshot({ path: join(captures, 'journal-new-filter-ready.png') })
  } finally { releaseOld?.(); releaseNew?.() }
})

// Real MCP Streamable HTTP lifecycle against the in-process Ocular listener.
// Each call closes its session; assertions inspect tool errors, not HTTP codes.
interface Answer {
  isError: boolean
  body: Record<string, unknown>
}

async function agent(method: string, args: unknown): Promise<Answer> {
  const config = JSON.parse(readFileSync(join(env(process.env.E2E_SYNTH_ROOT!).dataDir, 'mcp.json'), 'utf8')) as { url: string; token: string }
  const headers: Record<string, string> = { Authorization: `Bearer ${config.token}`, 'Content-Type': 'application/json', Accept: 'application/json, text/event-stream' }
  let session = ''
  const call = async (message: unknown) => {
    const res = await fetch(config.url, { method: 'POST', headers, body: JSON.stringify(message), signal: AbortSignal.timeout(60_000) })
    expect(res.ok, `MCP HTTP ${res.status}`).toBeTruthy()
    const id = res.headers.get('mcp-session-id')
    if (id) { session = id; headers['Mcp-Session-Id'] = id }
    const text = await res.text()
    if (!text) return null
    const messages = res.headers.get('content-type')?.includes('text/event-stream')
      ? text.split(/\r?\n\r?\n/).flatMap(event => {
        const data = event.split(/\r?\n/).filter(line => line.startsWith('data:')).map(line => line.slice(5).trimStart()).join('\n')
        return data ? [JSON.parse(data)] : []
      }) : [JSON.parse(text)]
    const response = messages.find(item => item.id === (message as { id?: number }).id)
    expect(response?.error).toBeUndefined()
    return response?.result
  }
  try {
    const initialized = await call({ jsonrpc: '2.0', id: 1, method: 'initialize', params: { protocolVersion: '2025-11-25', capabilities: {}, clientInfo: { name: 'e2e-agent', version: '1' } } })
    expect(initialized.protocolVersion).toBe('2025-11-25')
    expect(initialized.instructions).toContain('Kubernetes')
    expect(initialized.instructions).toContain('Docker')
    expect(initialized.instructions).toContain('Access')
    headers['MCP-Protocol-Version'] = initialized.protocolVersion
    await call({ jsonrpc: '2.0', method: 'notifications/initialized' })
    const result = await call({ jsonrpc: '2.0', id: 2, method: 'tools/call', params: { name: method, arguments: args } })
    const body = result.structuredContent ?? JSON.parse(result.content.find((item: { type: string }) => item.type === 'text').text)
    return { isError: result.isError === true, body }
  } finally {
    if (session) {
      const closed = await fetch(config.url, { method: 'DELETE', headers, signal: AbortSignal.timeout(5_000) })
      expect(closed.ok).toBeTruthy()
      await closed.body?.cancel()
    }
  }
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
  await post(page, '/api/SetLanguage', { language: '' })
  await post(page, '/api/_test/synthetic/reset')
})

test('explicit client setup preserves settings, rejects stale revisions and foreign entries', async ({ page }) => {
  const home = env(process.env.E2E_SYNTH_ROOT!).home
  const codexPath = join(home, '.codex/config.toml')
  const claudePath = join(home, '.claude/.claude.json')
  mkdirSync(join(home, '.codex'), { recursive: true })
  mkdirSync(join(home, '.claude'), { recursive: true })
  const codexOriginal = '# Keep this comment\n[mcp_servers.other]\nurl = "http://127.0.0.1:1/mcp"\n'
  const claudeOriginal = '{ "unrelated": 9007199254740993, "mcpServers": { "other": { "type": "http", "url": "http://127.0.0.1:1/mcp" } } }'
  writeFileSync(codexPath, codexOriginal, { mode: 0o600 })
  writeFileSync(claudePath, claudeOriginal, { mode: 0o600 })
  await page.goto('/')
  await page.getByRole('button', { name: 'Agents', exact: true }).click()
  const panel = page.getByRole('dialog', { name: 'Agent access' })
  await panel.locator('.agents-connection > summary').click()
  await expect(panel.getByRole('button', { name: 'Set up Codex', exact: true })).toBeEnabled()
  expect(readFileSync(codexPath, 'utf8')).toBe(codexOriginal)
  expect(readFileSync(claudePath, 'utf8')).toBe(claudeOriginal)
  // An external editor changes settings after discovery. The explicit write must fail.
  const changed = codexOriginal + '# Changed after inspection\n'
  writeFileSync(codexPath, changed)
  await panel.getByRole('button', { name: 'Set up Codex', exact: true }).click()
  await expect(panel.locator('.agents-connection').getByRole('alert')).toContainText('Check clients again before retrying')
  expect(readFileSync(codexPath, 'utf8')).toBe(changed)
  await panel.getByRole('button', { name: 'Check clients again' }).click()
  await panel.getByRole('button', { name: 'Set up Codex', exact: true }).click()
  await expect(panel.getByRole('group', { name: 'Codex', exact: true })).toContainText('Ocular settings match')
  expect(readFileSync(codexPath, 'utf8').startsWith(changed)).toBe(true)
  await panel.getByRole('button', { name: 'Set up Claude Code', exact: true }).click()
  await expect(panel.getByRole('group', { name: 'Claude Code', exact: true })).toContainText('Ocular settings match')
  const claude = readFileSync(claudePath, 'utf8')
  expect(claude).toContain('9007199254740993')
  expect(JSON.parse(claude).mcpServers.other.url).toBe('http://127.0.0.1:1/mcp')
  const installed = readFileSync(codexPath, 'utf8')
  await panel.getByRole('button', { name: 'Check clients again' }).click()
  await expect(panel.getByRole('button', { name: /^Set up / })).toHaveCount(0)
  expect(readFileSync(codexPath, 'utf8')).toBe(installed)
  expect(readFileSync(claudePath, 'utf8')).toBe(claude)
  const foreign = '[mcp_servers.ocular]\nurl = "http://127.0.0.1:2/mcp"\n'
  writeFileSync(codexPath, foreign)
  await panel.getByRole('button', { name: 'Check clients again' }).click()
  await expect(panel.getByRole('group', { name: 'Codex', exact: true })).toContainText('An existing ocular entry differs')
  expect(readFileSync(codexPath, 'utf8')).toBe(foreign)
  await panel.locator('.agents-connection-body details > summary').click()
  await expect(panel.getByRole('button', { name: 'Copy for Codex' })).toBeEnabled()
})

test('MCP setup copies private configuration explicitly and fits all eight locales', async ({ page }) => {
  await page.goto('/')
  const config = JSON.parse(readFileSync(join(env(process.env.E2E_SYNTH_ROOT!).dataDir, 'mcp.json'), 'utf8')) as { url: string; token: string }
  const captures = join(scratchRoot, 'mcp-setup-screenshots')
  mkdirSync(captures, { recursive: true })
  for (const language of languages) {
    await post(page, '/api/SetLanguage', { language })
    await page.reload()
    await expect(page.locator('html')).toHaveAttribute('lang', language)
    // Open through the actual toolbar; the fixture owns its clipboard.
    const names: Record<string, string> = { en: 'Agents', ru: 'Агенты', zh: "代理", es: "Agentes", de: "Agenten", fr: "Agents", pt: "Agentes", ja: "エージェント" }
    await page.getByRole('button', { name: names[language], exact: true }).click()
    const panel = page.getByRole('dialog')
    await panel.locator('.agents-connection > summary').click()
    await expect(panel).toContainText(config.url)
    expect((await panel.evaluate(el => el.outerHTML)).includes(config.token)).toBe(false)
    await expect(panel.locator('[aria-busy]')).toHaveAttribute('aria-busy', 'false')
    await panel.locator('.agents-connection-body details > summary').click()
    const copies = panel.locator('.agents-connection-body details button')
    await expect(copies).toHaveCount(2)
    expect((await panel.locator('[data-client-config=codex]').textContent())?.includes(config.token)).toBe(true)
    expect((await panel.locator('[data-client-config=claude]').textContent())?.includes(config.token)).toBe(true)
    if (language === 'en') {
      const codex = panel.getByRole('button', { name: 'Copy for Codex', exact: true })
      const claude = panel.getByRole('button', { name: 'Copy for Claude Code', exact: true })
      await codex.click()
      const toml = await page.evaluate(() => navigator.clipboard.readText())
      const parsed = JSON.parse(execFileSync('python3', ['-c', 'import json,sys,tomllib; print(json.dumps(tomllib.loads(sys.stdin.read())))'], { input: toml, encoding: 'utf8' }))
      const setup = parsed.mcp_servers.ocular
      expect(setup.url === config.url && setup.http_headers.Authorization === `Bearer ${config.token}`).toBe(true)
      await expect(codex).toHaveText('Copied')
      await expect(claude).toHaveText('Copy for Claude Code')
      await claude.click()
      const json = await page.evaluate(() => navigator.clipboard.readText())
      const other = JSON.parse(json).mcpServers.ocular
      expect(other.type === 'http' && other.url === config.url && other.headers.Authorization === `Bearer ${config.token}`).toBe(true)
      await expect(claude).toHaveText('Copied')
      await expect(codex).toHaveText('Copy for Codex')
      await page.evaluate(() => navigator.clipboard.writeText(''))
      // Capture the original labels rather than the transient success text.
      await expect(claude).toHaveText('Copy for Claude Code')
    }
    for (const width of [1280, 800]) {
      await page.setViewportSize({ width, height: 820 })
      expect(await panel.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true)
      await panel.locator('.agents-connection-body details > summary').click()
      await expect(copies).toHaveCount(0)
      await panel.locator('.agents-connection').evaluate(el => { el.scrollTop = 0 })
      await page.screenshot({ path: join(captures, `mcp-auto-${language}-${width}.png`) })
      await panel.locator('.agents-connection-body details > summary').click()
      await expect(copies).toHaveCount(2)
      for (const copy of await copies.all()) {
        await copy.evaluate(el => el.scrollIntoView({ block: 'center', inline: 'nearest' }))
        await expect(copy).toBeInViewport({ ratio: 1 })
      }
      // The full credential is deliberately visible in the trusted UI. Mask both
      // raw blocks only for shareable screenshots; assertions above use real text.
      await page.screenshot({ path: join(captures, `mcp-setup-${language}-${width}.png`), mask: [panel.locator('[data-client-config]')] })
    }
    await page.keyboard.press('Escape')
  }
})

test('granted in the UI, an agent reads its zone only; its delete waits for Yes; the journal shows it', async ({ page }) => {
  await page.goto('/')
  await post(page, '/api/_test/synthetic/reset')
  await selectConnection(page,/^demo\b/)
  await connectSelected(page)

  // Nothing granted: the agent sees no target.
  expect((await agent('Access', {})).body.targets).toEqual([])

  await page.getByRole('button', { name: 'Agents', exact: true }).click()
  const panel = page.getByRole('dialog', { name: 'Agent access' })
  await expect(panel).toContainText('MCP endpoint:')
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
  expect(list.isError).toBe(false)
  const names = (list.body.rows as { ref: { name: string } }[]).map((r) => r.ref.name).sort()
  expect(names).toEqual(['parcel-1', 'parcel-2'])
  expect((await agent('GetObject', { ref: parcel('parcel-3', 'green') })).body.code).toBe('forbidden')
  // No grant of stamp: refused before the provider is asked.
  expect((await agent('PrepareAction', { ref: parcel('parcel-1', 'blue'), action: 'stamp' })).body.code).toBe('forbidden')

  // Delete: prepared, run, waits for the user.
  const prep = await agent('PrepareAction', { ref: parcel('parcel-1', 'blue'), action: 'delete' })
  expect(prep.isError).toBe(false)
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
  expect((await agent('ListObjects', { ...target, kind: 'parcels' })).body.code).toBe('forbidden')
})


test('contextual permissions preserve other scopes and require explicit grants', async ({ page }) => {
  await page.goto('/')
  await post(page, '/api/SaveAgentGrants', { ...target, grants: [{ scope: { mode: 'one', name: 'green' }, verb: 'read', kinds: ['parcels'] }] })
  await selectConnection(page,/^demo\b/)
  await connectSelected(page)
  await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Parcels', exact: true }).click()
  const zone = page.getByRole('textbox', { name: 'Zone', exact: true })
  await zone.fill('blue, green')
  await zone.press('Enter')
  await page.getByRole('button', { name: 'Agent permissions' }).click()
  const panel = page.getByRole('dialog', { name: 'Agent access' })
  const targetsPanel = panel.locator('.agents-targets')
  const targetsWidth = (await targetsPanel.boundingBox())!.width
  const resize = targetsPanel.getByRole('separator', { name: 'Resize targets panel', exact: true })
  await resize.press('ArrowRight')
  expect((await targetsPanel.boundingBox())!.width).toBeCloseTo(targetsWidth + 20, 0)
  const split = (await resize.boundingBox())!
  await page.mouse.move(split.x + 2, split.y + 100)
  await page.mouse.down()
  await page.mouse.move(split.x + 62, split.y + 100, { steps: 8 })
  await page.mouse.up()
  expect((await targetsPanel.boundingBox())!.width).toBeCloseTo(targetsWidth + 80, 0)
  await page.screenshot({ path: join(scratchRoot, 'agents-resized-targets.png') })
  const editor = panel.getByRole('region', { name: 'demo', exact: true })
  const blue = editor.getByRole('region', { name: 'Zone blue', exact: true })
  const green = editor.getByRole('region', { name: 'Zone green', exact: true })
  await expect(blue.getByRole('checkbox', { name: 'Read', exact: true })).not.toBeChecked()
  await expect(green.getByRole('checkbox', { name: 'Read', exact: true })).toBeChecked()
  await expect(editor.getByRole('button', { name: 'Save', exact: true })).toBeDisabled()
  expect((await agent('GetObject', { ref: parcel('parcel-1', 'blue') })).body.code).toBe('forbidden')
  await blue.getByRole('checkbox', { name: 'Read', exact: true }).check()
  await blue.getByText('Execution', { exact: true }).click()
  await page.screenshot({ path: join(scratchRoot, 'agent-permissions.png') })
  await editor.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(editor.getByRole('button', { name: 'Save', exact: true })).toBeDisabled()
  const access = JSON.stringify((await agent('Access', {})).body)
  expect(access).toContain('"kinds":["parcels"]')
  expect((await agent('GetObject', { ref: parcel('parcel-1', 'blue') })).isError).toBe(false)
  await panel.getByRole('button', { name: 'Close', exact: true }).click()
  await zone.fill('')
  await zone.press('Enter')
  await page.getByRole('button', { name: 'Agent permissions' }).click()
  await expect(editor.getByRole('region', { name: 'All zones (later ones too)', exact: true }).getByRole('checkbox', { name: 'Read', exact: true })).not.toBeChecked()
  await expect(editor.getByRole('button', { name: 'Save', exact: true })).toBeDisabled()
})

test('permissions layout keeps actions visible, traps focus and fits Russian text', async ({ page }) => {
  await page.goto('/')
  await selectConnection(page,/^demo\b/)
  await connectSelected(page)
  await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Parcels', exact: true }).click()
  await expect(page.getByRole('textbox', { name: 'Filter resources', exact: true })).toBeVisible()
  await post(page, '/api/SaveAgentGrants', { ...target, grants: [
    { scope: { mode: 'one', name: 'blue' }, verb: 'read', kinds: null },
    { scope: { mode: 'one', name: 'green' }, verb: 'read', kinds: ['parcels'] },
  ] })
  await page.route('**/api/AppInfo', async (route) => {
    const response = await route.fetch()
    await route.fulfill({ json: { ...await response.json(), language: 'ru', languagePreference: 'ru' } })
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
  await expect(panel.getByRole('button', { name: 'Проверить клиентов снова' })).toBeVisible()
  await expect(save).toBeInViewport()
  await page.keyboard.press('Escape')
  await expect(panel).toBeHidden()
  await expect(trigger).toBeFocused()
})

test('named groups union their access and both switches persist without deleting permissions', async ({ page }) => {
  await page.goto('/')
  await selectConnection(page,/^demo\b/)
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
  const allKinds=picker.getByRole('checkbox',{name:'All kinds',exact:true}),parcels=picker.getByRole('checkbox',{name:'Parcels',exact:true})
  await expect(parcels).toBeDisabled()
  await page.screenshot({path:join(scratchRoot,'agent-checkbox-disabled-kinds.png')})
  await allKinds.focus();await allKinds.press('Space');await expect(allKinds).not.toBeChecked()
  await expect(parcels).toBeEnabled()
  await parcels.focus();await parcels.press('Space');await expect(parcels).toBeChecked()
  await page.screenshot({path:join(scratchRoot,'agent-checkbox-active-keyboard.png')})
  await page.emulateMedia({forcedColors:'active'});await expect(parcels).toBeChecked()
  await page.screenshot({path:join(scratchRoot,'agent-checkbox-forced-colors.png')})
  await page.emulateMedia({forcedColors:'none'})
  await picker.getByRole('button', { name: 'Done', exact: true }).click()
  const save = panel.getByRole('button', { name: 'Save', exact: true })
  const persist = async () => {
    await expect(save).toBeEnabled()
    await save.click()
    await expect(save).toBeDisabled()
  }
  const read = (kind: string) => agent('ListObjects', { ...target, scope: 'blue', kind })
  await persist()
  expect((await read('crates')).isError).toBe(false)
  expect((await read('parcels')).isError).toBe(false)
  await page.mouse.move(0, 0)
  await page.screenshot({ path: join(scratchRoot, 'agent-named-groups.png') })
  await limited.getByRole('switch', { name: 'Enable group Parcels only' }).click()
  await persist()
  expect((await read('crates')).isError).toBe(false)
  await limited.getByRole('switch', { name: 'Enable group Parcels only' }).click()
  await broad.getByRole('switch', { name: 'Enable group Observation' }).click()
  await persist()
  expect((await read('crates')).body.code).toBe('forbidden')
  expect((await read('parcels')).isError).toBe(false)
  await blue.getByRole('switch', { name: 'Access to Zone blue' }).click()
  await persist()
  expect((await read('parcels')).body.code).toBe('forbidden')
  await page.reload()
  await page.getByRole('button', { name: 'Agents', exact: true }).click()
  await expect(blue.getByRole('switch', { name: 'Access to Zone blue' })).not.toBeChecked()
  await expect(broad.getByRole('switch', { name: 'Enable group Observation' })).not.toBeChecked()
  await expect(limited.getByRole('switch', { name: 'Enable group Parcels only' })).toBeChecked()
  await expect(limited.getByRole('checkbox', { name: 'Read', exact: true })).toBeChecked()
  await blue.getByRole('switch', { name: 'Access to Zone blue' }).click()
  await persist()
  expect((await read('parcels')).isError).toBe(false)
  expect((await read('crates')).body.code).toBe('forbidden')
})

test('exec is explicitly granted, reviews exact input, runs once and retains its scope', async ({ page }) => {
  await page.goto('/')
  await post(page, '/api/_test/synthetic/reset')
  await selectConnection(page,/^demo\b/)
  await connectSelected(page)
  const ref = { ...parcel('parcel-1', 'blue'), uid: 'uid-parcel-1' }
  expect((await agent('GetExecInfo', { ref })).body.code).toBe('forbidden')
  await page.getByRole('button', { name: 'Agents', exact: true }).click()
  const panel = page.getByRole('dialog', { name: 'Agent access' })
  const editor = panel.getByRole('region', { name: 'demo' })
  await editor.getByRole('textbox', { name: / name$/ }).fill('blue')
  await editor.getByRole('button', { name: 'Add', exact: true }).last().click()
  const blue = editor.getByRole('region', { name: /blue$/ })
  await blue.getByRole('checkbox', { name: 'Read', exact: true }).uncheck()
  await blue.getByText('Execution', { exact: true }).click()
  await blue.getByRole('checkbox', { name: 'Container commands (exec) (destructive)', exact: true }).check()
  await expect(editor.getByRole('button', { name: 'Save', exact: true })).toBeEnabled()
  await expect(blue.getByRole('group', { name: 'Kinds', exact: true })).toHaveCount(0)
  await expect(blue.getByRole('button', { name: 'Container commands (exec): Kinds', exact: true })).toHaveCount(0)
  await expect(blue.getByRole('checkbox', { name: 'without confirmation', exact: true })).not.toBeChecked()
  const captures = join(scratchRoot, 'exec-screenshots')
  mkdirSync(captures, { recursive: true })
  await page.screenshot({ path: join(captures, 'exec-grant.png') })
  await editor.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(panel.getByText('granted: 1', { exact: true })).toBeVisible()
  await panel.getByRole('button', { name: 'Close', exact: true }).click()
  const info = await agent('GetExecInfo', { ref })
  expect(info.isError).toBe(false)
  const instances = info.body.instances as { id: string }[]
  expect(instances).toHaveLength(1)
  const prep = await agent('PrepareExec', { ref, instance: instances[0].id, channel: 'main', command: ['stdin'], stdin: 'finite input: привет\n', maxBytes: 4096 })
  expect(prep.isError).toBe(false)
  expect(prep.body.confirmation).toBe('user')
  const run = await agent('RunExec', { planId: prep.body.planId })
  expect(run.body.state).toBe('awaiting_confirmation')
  const dialog = page.getByRole('dialog', { name: 'Agent “e2e-agent” asks you to confirm' })
  await expect(dialog).toBeVisible()
  await expect(dialog.getByRole('button', { name: 'No', exact: true })).toBeFocused()
  await expect(dialog.getByLabel('Command', { exact: true })).toHaveText('[\n  "stdin"\n]')
  await expect(dialog.getByLabel('Standard input', { exact: true })).toHaveText(JSON.stringify('finite input: привет\n'))
  await expect(dialog).toContainText('Output limit: 4096 bytes; duration: 300 seconds. No automatic retry.')
  await page.screenshot({ path: join(captures, 'exec-confirmation.png') })
  for (const language of languages) {
    await post(page, '/api/SetLanguage', { language })
    await page.reload()
    await expect(page.locator('html')).toHaveAttribute('lang', language)
    const localized = page.getByRole('dialog')
    await expect(localized).toHaveCount(1)
    await expect(localized.locator('pre').first()).toHaveText('[\n  "stdin"\n]')
    for (const width of [1280, 860]) {
      await page.setViewportSize({ width, height: 820 })
      expect(await localized.evaluate(el => el.scrollWidth <= el.clientWidth)).toBeTruthy()
      await expect(localized.getByRole('button').last()).toBeInViewport({ ratio: 1 })
      await page.screenshot({ path: join(captures, `exec-confirmation-${language}-${width}.png`) })
    }
  }
  await post(page, '/api/SetLanguage', { language: 'en' })
  await page.reload()
  await expect(page.locator('html')).toHaveAttribute('lang', 'en')
  await dialog.getByRole('button', { name: 'Yes, run it', exact: true }).click()
  await expect(dialog).toBeHidden()
  let result: Answer | undefined
  await expect.poll(async () => { result = await agent('GetRun', { runId: run.body.runId }); return result.body.state }).toBe('done')
  const output = result!.body.exec as { stdoutBase64: string; complete: boolean; exitCode: number }
  expect(Buffer.from(output.stdoutBase64, 'base64').toString('utf8')).toBe('finite input: привет\n')
  expect(output.complete).toBe(true)
  expect(output.exitCode).toBe(0)
  const maxPrep=await agent('PrepareExec',{ref,instance:instances[0].id,channel:'main',command:['stdin'],maxSeconds:1800})
  expect(maxPrep.isError).toBe(false)
  const maxRun=await agent('RunExec',{planId:maxPrep.body.planId});expect(maxRun.body.state).toBe('awaiting_confirmation')
  await expect(dialog).toContainText('duration: 1800 seconds.');await expect(dialog.getByRole('button',{name:'No',exact:true})).toBeFocused();await page.screenshot({path:join(captures,'exec-confirmation-maximum.png')});await dialog.getByRole('button',{name:'No',exact:true}).click();expect((await agent('GetRun',{runId:maxRun.body.runId})).body.state).toBe('rejected')
  expect((await agent('RunExec', { planId: prep.body.planId })).body.code).toBe('not_found')
  expect((await agent('GetObject', { ref })).body.code).toBe('forbidden')
  expect((await agent('GetExecInfo', { ref: { ...parcel('parcel-3', 'green'), uid: 'uid-parcel-3' } })).body.code).toBe('forbidden')
  await post(page, '/api/RevokeAllAgentGrants')
  expect((await agent('GetRun', { runId: run.body.runId })).body.code).toBe('forbidden')
})
