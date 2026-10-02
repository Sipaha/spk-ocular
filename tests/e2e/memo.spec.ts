import { expect, test, type Page } from '@playwright/test'
import { spawn, type ChildProcess } from 'node:child_process'
import { mkdirSync, mkdtempSync } from 'node:fs'
import { createServer } from 'node:net'
import { join } from 'node:path'
import { ONE, env, noDockerEnv, writeAtomic } from './fixtures'

// This spec owns the app lifecycle and restarts it on the same data dir.
// It deliberately bypasses fixtures' reset of persisted page snapshots.
const scratch = join(import.meta.dirname, '../../../.agents/tmp')
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
    await page.getByRole('option', { name: /^demo\b/ }).click()
    await expect(page.locator('[data-primary-filter]')).toHaveValue('api')
    await stop(app)
    app = await start(port, e)
    await page.goto(url)
    await expect(page.locator('[data-primary-filter]')).toHaveValue('api')
    await expect(grid.getByRole('columnheader', { name: 'Sources' })).toHaveAttribute('aria-sort', 'descending')
    await expect(drawer.getByRole('heading', { name: 'api' })).toBeVisible()
    if (process.env.E2E_MEMO_SCREENSHOT) await page.screenshot({ path: process.env.E2E_MEMO_SCREENSHOT, fullPage: true })
  } finally {
    await page.close()
    if (app) await stop(app)
  }
})
