import { expect, test, type Page } from '@playwright/test'
import { spawn, type ChildProcess } from 'node:child_process'
import { mkdirSync, mkdtempSync } from 'node:fs'
import { createServer } from 'node:net'
import { join } from 'node:path'
import { ONE, env, noDockerEnv, writeAtomic } from './fixtures'

// P19: the page snapshot (filter/sort/open details) survives an app
// restart — it lives in the app's data dir (target_state "pageMemo"). The
// shared-config webServer cannot restart mid-suite, so this spec owns the
// app lifecycle: it starts the browser-mode synthetic app twice on one
// data dir. `test` comes from @playwright/test, not fixtures: there is no
// webServer here for the clean-page-memo fixture to call.

mkdirSync(join(import.meta.dirname, '.run'), { recursive: true })
const root = mkdtempSync(join(import.meta.dirname, '.run', 'memo-'))
const e = env(root)
const bin = process.env.E2E_BIN ?? '../../build/bin/spk-ocular'

const freePort = (from: number): Promise<number> =>
  new Promise((resolve) => {
    const s = createServer()
    s.once('error', () => resolve(freePort(from + 1)))
    s.listen(from, '127.0.0.1', () => {
      const p = (s.address() as { port: number }).port
      s.close(() => resolve(p))
    })
  })

const start = async (port: number): Promise<ChildProcess> => {
  const p = spawn(bin, ['--browser', '--port', String(port), '--test-api', '--test-synthetic'], {
    env: {
      ...process.env,
      SPK_OCULAR_HOME: e.dataDir,
      HOME: e.home,
      ...noDockerEnv,
      SPK_OCULAR_TEST_SYNTH_SECOND: '1',
      KUBECONFIG: e.one,
      LANG: 'en_US.UTF-8',
      LANGUAGE: '',
      LC_ALL: '',
      LC_MESSAGES: '',
      // Fixture clusters are unreachable by design; no detour through a proxy.
      HTTPS_PROXY: '',
      HTTP_PROXY: '',
      https_proxy: '',
      http_proxy: '',
    },
    stdio: 'ignore',
  })
  const deadline = Date.now() + 20000
  for (;;) {
    try {
      const res = await fetch(`http://127.0.0.1:${port}/`)
      if (res.ok) return p
    } catch {
      // not up yet
    }
    if (Date.now() > deadline) throw new Error(`the app did not start on ${port}`)
    await new Promise((r) => setTimeout(r, 150))
  }
}

const stop = async (p: ChildProcess) => {
  p.kill('SIGTERM')
  const deadline = Date.now() + 10000
  while (p.exitCode === null && Date.now() < deadline) await new Promise((r) => setTimeout(r, 100))
  if (p.exitCode === null) p.kill('SIGKILL')
}

test('the page snapshot survives an app restart', async ({ browser }) => {
  writeAtomic(e.one, ONE)
  const port = await freePort(5193)
  let app = await start(port)
  const page: Page = await browser.newPage()
  const url = `http://127.0.0.1:${port}/`
  try {
    await page.goto(url)
    await page.getByRole('option', { name: /^demo\b/ }).click()
    await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Services', exact: true }).click()
    const grid = page.getByRole('grid', { name: 'resources' })
    // A page as the user left it: a filter, a sort, open details.
    await page.locator('[data-primary-filter]').fill('api')
    await grid.getByRole('columnheader', { name: 'Sources' }).click()
    await grid.getByRole('columnheader', { name: 'Sources' }).click() // descending
    await grid.getByRole('gridcell', { name: 'api', exact: true }).click()
    const drawer = page.getByRole('dialog', { name: 'services api' })
    await expect(drawer.getByRole('heading', { name: 'api' })).toBeVisible()
    // The debounced writer lands within a second of the last change.
    await page.waitForTimeout(1600)

    await stop(app)
    app = await start(port)
    await page.goto(url)
    // The target, its kind and its page come back as they were left.
    await expect(page.locator('[data-primary-filter]')).toHaveValue('api')
    await expect(grid.getByRole('columnheader', { name: 'Sources' })).toHaveAttribute('aria-sort', 'descending')
    await expect(drawer.getByRole('heading', { name: 'api' })).toBeVisible()
  } finally {
    await stop(app)
  }
})
