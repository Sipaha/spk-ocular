import { connectSelected } from './fixtures'
import { expect, test, type Page } from '@playwright/test'
import { spawn, type ChildProcess } from 'node:child_process'
import { mkdirSync, mkdtempSync } from 'node:fs'
import { createServer } from 'node:net'
import { join } from 'node:path'
import { scratchRoot as scratch, ONE, env, noDockerEnv, writeAtomic } from './fixtures'

// This spec owns the app lifecycle and restarts it on the same data dir.
// It deliberately bypasses fixtures' reset of persisted page snapshots.
const bin = process.env.E2E_BIN ?? '../../build/bin/spk-ocular'

const freePort = (): Promise<number> =>
  new Promise((resolve, reject) => {
    const s = createServer()
    s.once('error', reject)
    s.listen(0, '127.0.0.1', () => {
      const p = (s.address() as { port: number }).port
      s.close((err) => err ? reject(err) : resolve(p))
    })
  })

const stop = async (p: ChildProcess) => {
  if (p.exitCode !== null || p.signalCode !== null || !p.pid) return
  await new Promise<void>((resolve) => {
    const timer = setTimeout(() => p.kill('SIGKILL'), 5000)
    p.once('exit', () => { clearTimeout(timer); resolve() })
    p.kill('SIGTERM')
  })
}

const start = async (port: number, e: ReturnType<typeof env>): Promise<ChildProcess> => {
  const p = spawn(bin, ['--browser', '--port', String(port), '--test-api', '--test-synthetic'], {
    env: {
      ...process.env,
      SPK_OCULAR_HOME: e.dataDir,
      HOME: e.home,
      ...noDockerEnv,
      SPK_OCULAR_TEST_SYNTH_SECOND: '1',
      KUBECONFIG: e.one,
      LANG: 'en_US.UTF-8', LANGUAGE: '', LC_ALL: '', LC_MESSAGES: '',
      HTTPS_PROXY: '', HTTP_PROXY: '', https_proxy: '', http_proxy: '', ALL_PROXY: '', all_proxy: '',
    },
    stdio: ['ignore', 'ignore', 'pipe'],
  })
  let stderr = ''
  let spawnError: Error | undefined
  p.stderr?.on('data', (chunk: Buffer) => { stderr = (stderr + chunk.toString()).slice(-8000) })
  p.once('error', (error) => { spawnError = error })
  try {
    await expect.poll(async () => {
      if (spawnError) throw spawnError
      if (p.exitCode !== null || p.signalCode !== null) throw new Error(`app exited: ${stderr}`)
      try {
        return (await fetch(`http://127.0.0.1:${port}/`, { signal: AbortSignal.timeout(1000) })).ok
      } catch { return false }
    }, { timeout: 20000, message: 'browser app starts' }).toBe(true)
    // This suite covers workspace persistence; dismiss import explicitly before
    // opening its synthetic workspace. Fresh onboarding is tested separately.
    const base=`http://127.0.0.1:${port}`
    const html=await (await fetch(base)).text()
    const token=html.match(/spk-ocular-api-token" content="([^"]+)"/)?.[1]
    const response=await fetch(base+'/api/Configurations',{method:'POST',headers:{Authorization:`Bearer ${token}`,Origin:base,'Content-Type':'application/json'},body:JSON.stringify({command:'dismiss'})})
    expect(response.ok).toBe(true)
    return p
  } catch (error) {
    await stop(p)
    throw new Error(`app did not start: ${stderr}`, { cause: error })
  }
}

test('the page snapshot survives an app restart', async ({ browser }) => {
  mkdirSync(scratch, { recursive: true })
  const root = mkdtempSync(join(scratch, 'memo-e2e-'))
  const e = env(root)
  writeAtomic(e.one, ONE)
  const port = await freePort()
  let app: ChildProcess | undefined
  const page: Page = await browser.newPage()
  const url = `http://127.0.0.1:${port}/`
  try {
    app = await start(port, e)
    await page.goto(url)
    await page.getByRole('option', { name: /^demo\b/ }).click()
    await connectSelected(page)
    await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: /^Synthetic/ }).click()
    await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Services', exact: true }).click()
    const grid = page.getByRole('grid', { name: 'resources' })
    await page.locator('[data-primary-filter]').fill('api')
    await grid.getByRole('columnheader', { name: 'Sources' }).click()
    await grid.getByRole('columnheader', { name: 'Sources' }).click()
    await grid.getByRole('gridcell', { name: 'api', exact: true }).click()
    const drawer = page.getByRole('dialog', { name: 'services api' })
    await expect(drawer.getByRole('heading', { name: 'api' })).toBeVisible()
    const token = await page.locator('meta[name="spk-ocular-api-token"]').getAttribute('content')
    // Wait for the stored state, not a sleep guessed from the debounce.
    await expect.poll(async () => {
      const res = await page.request.post(`${url}api/GetTargetState`, {
        headers: { Authorization: `Bearer ${token}`, Origin: new URL(url).origin }, data: { provider: 'synthetic', target: 'demo' },
      })
      expect(res.ok(), `${res.status()} ${await res.text()}`).toBe(true)
      const state = await res.json()
      return state.pageMemo ? JSON.parse(state.pageMemo) : null
    }).toMatchObject({ sorts: { services: { desc: true } }, page: { filter: 'api', open: { name: 'api' } } })

    // A -> B -> A in one debounce window must not persist B over A.
    await page.locator('[data-primary-filter]').fill('workers')
    await page.locator('[data-primary-filter]').fill('api')
    await page.getByRole('option', { name: /^demo2\b/ }).click() // flush A's pending writer
    await connectSelected(page)
    await page.getByRole('option', { name: /^demo\b/ }).click()
    await connectSelected(page)
    await expect(page.locator('[data-primary-filter]')).toHaveValue('api')
    await stop(app)
    app = await start(port, e)
    await page.goto(url)
    await expect(page.getByRole('button', { name: 'Connect', exact: true })).toBeVisible()
    await expect(page.getByRole('grid', { name: 'resources' })).toHaveCount(0)
    await connectSelected(page)
    await expect(page.locator('[data-primary-filter]')).toHaveValue('api')
    await expect(grid.getByRole('columnheader', { name: 'Sources' })).toHaveAttribute('aria-sort', 'descending')
    await expect(drawer.getByRole('heading', { name: 'api' })).toBeVisible()
    if (process.env.E2E_MEMO_SCREENSHOT) await page.screenshot({ path: process.env.E2E_MEMO_SCREENSHOT, fullPage: true })
  } finally {
    await page.close()
    if (app) await stop(app)
  }
})

test('scope set, global favorites and column widths survive an app restart', async ({ browser }) => {
  mkdirSync(scratch, { recursive: true })
  const root = mkdtempSync(join(scratch, 'scopes-restart-'))
  const e = env(root)
  writeAtomic(e.one, ONE)
  const port = await freePort()
  let app: ChildProcess | undefined
  const page = await browser.newPage()
  const url = `http://127.0.0.1:${port}/`
  try {
    app = await start(port, e)
    await page.goto(url)
    await page.getByRole('option', { name: /^demo\b/ }).click()
    await connectSelected(page)
    await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: /^Synthetic/ }).click()
    const input = page.getByRole('textbox', { name: 'Zone', exact: true })
    await input.fill('green, blue')
    await input.press('Enter')
    await expect(input).toHaveValue('blue, green')
    const nameWidth = page.getByRole('grid', { name: 'resources' }).getByRole('separator', { name: 'Resize column Name', exact: true })
    await nameWidth.focus()
    await page.keyboard.press('Home')
    await page.keyboard.press('Shift+ArrowRight')
    await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Crates', exact: true }).hover()
    await page.getByRole('button', { name: 'Add Crates to favorites', exact: true }).click()
    const favorites = page.getByRole('region', { name: 'Favorites', exact: true })
    await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: /^Favorites/ }).click()
    await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Parcels', exact: true }).hover()
    await page.getByRole('button', { name: 'Add Parcels to favorites', exact: true }).click()
    await favorites.getByRole('button', { name: 'Parcels', exact: true }).dragTo(favorites.getByRole('button', { name: 'Crates', exact: true }), { targetPosition: { x: 20, y: 2 } })
    await expect(favorites.locator('.nav-item')).toHaveText(['Parcels', 'Crates'])
    await page.getByRole('option', { name: /^demo2\b/ }).click()
    await connectSelected(page)
    await expect(favorites.getByRole('button', { name: 'Crates', exact: true })).toBeVisible()
    await page.getByRole('option', { name: /^demo\b/ }).click()
    await connectSelected(page)
    await expect(input).toHaveValue('blue, green')
    const tok = await page.locator('meta[name="spk-ocular-api-token"]').getAttribute('content')
    await expect.poll(async () => {
      const res = await page.request.post(`${url}api/GetTargetState`, {
        headers: { Authorization: `Bearer ${tok}`, Origin: new URL(url).origin }, data: { provider: 'synthetic', target: 'demo' },
      })
      const state = await res.json()
      return state.scope ? JSON.parse(state.scope) : null
    }).toEqual({ mode: 'some', names: ['blue', 'green'] })
    await expect.poll(async () => {
      const res = await page.request.post(`${url}api/GetTargetState`, { headers: { Authorization: `Bearer ${tok}`, Origin: new URL(url).origin }, data: { provider: 'synthetic', target: 'demo' } })
      const state = await res.json()
      return state['columnWidths.crates'] ? JSON.parse(state['columnWidths.crates']) : null
    }).toEqual({ name: 98 })
    await expect.poll(async () => {
      const res = await page.request.post(`${url}api/GetFavoriteKinds`, { headers: { Authorization: `Bearer ${tok}`, Origin: new URL(url).origin } })
      return await res.json()
    }).toEqual([{ provider: 'synthetic', kind: 'parcels' }, { provider: 'synthetic', kind: 'crates' }])
    await stop(app)
    app = await start(port, e)
    await page.goto(url)
    await expect(page.getByRole('button', { name: 'Connect', exact: true })).toBeVisible()
    await expect(page.getByRole('grid', { name: 'resources' })).toHaveCount(0)
    await connectSelected(page)
    await expect(input).toHaveValue('blue, green')
    await expect(favorites.getByRole('button', { name: 'Crates', exact: true })).toBeVisible()
    await expect(nameWidth).toHaveAttribute('aria-valuenow', '98')
    await expect(favorites.locator('.nav-item')).toHaveText(['Parcels', 'Crates'])
    const grid = page.getByRole('grid', { name: 'resources' })
    await expect(grid.getByRole('gridcell', { name: 'alpha', exact: true })).toBeVisible()
    await expect(grid.getByRole('gridcell', { name: 'gamma', exact: true })).toBeVisible()
  } finally {
    await page.close()
    if (app) await stop(app)
  }
})

test('resource sections fold globally and keep their state after restart, while details show name before kind', async ({ browser }) => {
  mkdirSync(scratch, { recursive: true })
  const root = mkdtempSync(join(scratch, 'sections-restart-'))
  const e = env(root)
  writeAtomic(e.one, ONE)
  const port = await freePort()
  let app: ChildProcess | undefined
  const page = await browser.newPage({ viewport: { width: 1280, height: 820 } })
  const url = `http://127.0.0.1:${port}/`
  try {
    app = await start(port, e)
    await page.goto(url)
    await page.getByRole('option', { name: /^demo\b/ }).click()
    await connectSelected(page)
    const nav = page.getByRole('navigation', { name: 'resources' })
    const synthetic = nav.getByRole('button', { name: /^Synthetic/ })
    const favorites = nav.getByRole('button', { name: /^Favorites/ })
    const health = nav.getByRole('button', { name: /^Health/ })
    for (const header of [synthetic, favorites, health]) {
      await expect(header).toHaveAttribute('aria-expanded', 'false')
      await header.click()
      await expect(header).toHaveAttribute('aria-expanded', 'true')
    }
    await nav.getByRole('button', { name: 'Services', exact: true }).hover()
    await nav.getByRole('button', { name: 'Add Services to favorites', exact: true }).click()
    await synthetic.click()
    await favorites.focus()
    await page.keyboard.press('ArrowLeft')
    await health.click()
    for (const header of [synthetic, favorites, health]) await expect(header).toHaveAttribute('aria-expanded', 'false')
    await expect(nav.getByRole('button', { name: 'Parcels', exact: true })).toHaveCount(0)
    await expect(nav.getByRole('button', { name: 'Services', exact: true })).toHaveCount(0)
    await expect(nav.getByRole('button', { name: 'Crates', exact: true })).toHaveAttribute('aria-current', 'page')
    await page.getByRole('option', { name: /^demo2\b/ }).click()
    await connectSelected(page)
    for (const header of [synthetic, favorites, health]) await expect(header).toHaveAttribute('aria-expanded', 'false')
    const filter = page.getByRole('textbox', { name: 'Filter resources', exact: true })
    await filter.fill('Services')
    await expect(favorites).toHaveAttribute('aria-expanded', 'true')
    await filter.press('Enter')
    await expect(page.getByRole('heading', { name: 'Services', exact: true, level: 1 })).toBeVisible()
    await filter.fill('')
    await expect(favorites).toHaveAttribute('aria-expanded', 'false')
    await page.getByRole('option', { name: /^demo\b/ }).click()
    await connectSelected(page)
    await page.getByRole('grid', { name: 'resources' }).getByRole('gridcell', { name: 'alpha', exact: true }).click()
    const drawer = page.getByRole('dialog', { name: 'crates alpha' })
    const name = drawer.getByRole('heading', { name: 'alpha', exact: true })
    const kind = drawer.locator('.drawer-kind')
    await expect(name).toBeVisible()
    const verifyTitleOrder = async () => {
      const n = await name.boundingBox()
      const k = await kind.boundingBox()
      expect(n).toBeTruthy()
      expect(k).toBeTruthy()
      expect(n!.x + n!.width).toBeLessThanOrEqual(k!.x)
      expect(Math.abs(n!.y - k!.y)).toBeLessThan(10)
    }
    await verifyTitleOrder()
    const tok = await page.locator('meta[name="spk-ocular-api-token"]').getAttribute('content')
    await expect.poll(async () => {
      const res = await page.request.post(`${url}api/GetNavSections`, { headers: { Authorization: `Bearer ${tok}`, Origin: new URL(url).origin } })
      expect(res.ok()).toBe(true)
      return res.json()
    }).toEqual({ 'group:Synthetic': false, 'group:Health': false, favorites: false })
    await page.screenshot({ path: join(root, 'sections-details.png'), fullPage: true })
    await stop(app)
    app = await start(port, e)
    await page.goto(url)
    await expect(page.getByRole('button', { name: 'Connect', exact: true })).toBeVisible()
    await expect(page.getByRole('grid', { name: 'resources' })).toHaveCount(0)
    await connectSelected(page)
    for (const header of [synthetic, favorites, health]) await expect(header).toHaveAttribute('aria-expanded', 'false')
    await expect(nav.getByRole('button', { name: 'Parcels', exact: true })).toHaveCount(0)
    await expect(nav.getByRole('button', { name: 'Services', exact: true })).toHaveCount(0)
    await page.getByRole('grid', { name: 'resources' }).getByRole('gridcell', { name: 'alpha', exact: true }).click()
    await expect(name).toBeVisible()
    await page.setViewportSize({ width: 960, height: 720 })
    await verifyTitleOrder()
    await page.screenshot({ path: join(root, 'sections-details-narrow.png'), fullPage: true })
    await drawer.getByRole('button', { name: 'Close', exact: true }).click()
    await synthetic.focus()
    await page.keyboard.press('ArrowRight')
    await expect(nav.getByRole('button', { name: 'Parcels', exact: true })).toBeVisible()
  } finally {
    await page.close()
    if (app) await stop(app)
  }
})
