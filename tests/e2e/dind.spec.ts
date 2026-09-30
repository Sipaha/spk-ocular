import { expect, test, type Page } from '@playwright/test'
import { execFileSync } from 'node:child_process'
import { pickScope } from './fixtures'
import { expectScreen, stats } from './synth'

// The Compose provider against the isolated test daemon (scripts/dind-seed.sh:
// projects ocular-fixture and ocular-other). Every change of the daemon is
// preceded by scripts/dind-verify.sh.
const host = process.env.OCULAR_DIND_HOST!
const docker = (...args: string[]) => {
  const verified = execFileSync('bash', [process.env.OCULAR_DIND_VERIFY!], { encoding: 'utf8' }).trim()
  if (verified !== host) throw new Error(`the test daemon check says ${verified}, not ${host}`)
  return execFileSync('docker', ['-H', host, ...args], { encoding: 'utf8', env: { ...process.env, DOCKER_HOST: '', DOCKER_CONTEXT: '' } })
}

async function openDind(page: Page, project = 'ocular-fixture') {
  await page.goto('/')
  await page.getByRole('option', { name: /^ocular-dind\b/ }).click()
  await expect(page.getByRole('navigation', { name: 'resources' })).toBeVisible()
  await pickScope(page, 'Project', project)
}

async function kindPage(page: Page, kind: string) {
  await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: kind, exact: true }).click()
  return page.getByRole('grid', { name: 'resources' })
}

const row = (grid: ReturnType<Page['getByRole']>, name: string | RegExp) => grid.getByRole('row').filter({ has: grid.page().getByRole('gridcell', { name, exact: typeof name === 'string' }) })

test('services of a project with their health', async ({ page }) => {
  await openDind(page)
  const grid = page.getByRole('grid', { name: 'resources' }) // services is the first view
  await expect(page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Services', exact: true })).toHaveAttribute('aria-current', /.+/)
  await expect(row(grid, 'web')).toContainText('1/1')
  await expect(row(grid, 'sick')).toContainText('Unhealthy', { timeout: 30_000 })
  await expect(row(grid, 'logger')).toContainText('2/2')
  await expect(row(grid, 'done')).toContainText('Completed')
  await expect(row(grid, 'fail')).toContainText('Exited')
})

test('containers, details with relations and the inspect YAML', async ({ page }) => {
  await openDind(page)
  const grid = await kindPage(page, 'Containers')
  await expect(row(grid, 'ocular-fixture-oneoff')).toBeVisible()
  await row(grid, 'ocular-fixture-web-1').click()
  const drawer = page.getByRole('dialog', { name: /ocular-fixture-web-1/ })
  await expect(drawer.getByRole('button', { name: /ocular-ext-net/ })).toBeVisible()
  await expect(drawer.getByRole('button', { name: /ocular-ext-vol/ })).toBeVisible()
  await drawer.getByRole('tab', { name: 'YAML' }).click()
  // The editor draws only what is in view: scroll down to the label.
  const cm = drawer.locator('.cm-editor')
  await expect
    .poll(async () => {
      if ((await cm.textContent())?.includes('com.docker.compose.service: web')) return true
      await cm.locator('.cm-scroller').evaluate((el) => el.scrollBy(0, 200))
      return false
    })
    .toBe(true)
})

test('a stop and a start on the daemon show live; Read again keeps the rows', async ({ page }) => {
  await openDind(page, 'ocular-other')
  const grid = await kindPage(page, 'Containers')
  const idle = row(grid, 'ocular-other-idle-1')
  await expect(idle).toContainText('running')
  docker('stop', '-t', '0', 'ocular-other-idle-1')
  try {
    await expect(idle).toContainText('exited')
  } finally {
    docker('start', 'ocular-other-idle-1')
  }
  await expect(idle).toContainText('running')
  // Read again reaches the provider and reads the containers anew (a list)
  const lists = (await stats(page)).feed_lists
  const resync = page.waitForResponse((r) => r.url().endsWith('/api/ResyncView'))
  await page.getByRole('button', { name: /Read again/ }).click()
  expect((await resync).status()).toBe(200)
  await expect.poll(async () => (await stats(page)).feed_lists).toBeGreaterThan(lists)
  await expect(idle).toContainText('running')
  await expect(page.getByRole('alert')).toHaveCount(0)
})

const logPanel = (page: Page) => page.locator('[role=tabpanel]:not([hidden])')
const logRows = (page: Page) => logPanel(page).locator('[data-log-viewport] [data-index]')

test('logs of a service: both replicas, stdout and stderr, live', async ({ page }) => {
  await openDind(page)
  const grid = await kindPage(page, 'Services')
  await row(grid, 'logger').click()
  await page.keyboard.press('l')
  const panel = logPanel(page)
  await expect(panel.getByLabel('stream state')).toHaveText('Live', { timeout: 30_000 })
  // Source labels lose their common prefix: "ocular-fixture-logger-2 (stderr)" shows as "2 (stderr)".
  await expect(logRows(page).filter({ hasText: /^\s*1\s+line \d+/ }).first()).toBeAttached({ timeout: 30_000 })
  await expect(logRows(page).filter({ hasText: /^\s*2\s+line \d+/ }).first()).toBeAttached()
  await expect(logRows(page).filter({ hasText: /2 \(stderr\)\s*err \d+/ }).first()).toBeAttached()
  await expect(panel.getByRole('combobox').first()).toHaveValue('*') // stdout and stderr by default
  // live: a line newer than any shown now arrives (the logger prints one a second)
  const newest = async () => {
    const texts = await logRows(page).allInnerTexts()
    return Math.max(0, ...texts.map((t) => Number(/^\s*1\s+line (\d+)/.exec(t)?.[1] ?? 0)))
  }
  const seen = await newest()
  await expect.poll(newest, { timeout: 15_000 }).toBeGreaterThan(seen)
})

test('a terminal in a replica of a service: the shell answers', async ({ page }) => {
  await openDind(page)
  const grid = await kindPage(page, 'Services')
  await row(grid, 'logger').click()
  await page.keyboard.press('s')
  await expect(page.getByRole('tab', { name: /logger/ })).toBeVisible()
  await expectScreen(page, '# ', 30_000) // busybox sh's prompt (root)
  await page.keyboard.type('echo ocular-$((6*7))')
  await page.keyboard.press('Enter')
  await expectScreen(page, 'ocular-42')
})

test('CPU and Memory of services and containers', async ({ page }) => {
  await openDind(page)
  const services = await kindPage(page, 'Services')
  await expect(services.getByRole('columnheader', { name: /CPU/ })).toBeVisible()
  await expect(services.getByRole('columnheader', { name: /Memory/ })).toBeVisible()
  // the last two cells: cores (or millicores), then bytes
  const usage = async (r: ReturnType<typeof row>) => {
    await expect(r.getByRole('gridcell').nth(-2)).toHaveText(/^\d+(\.\d+)?m?$/, { timeout: 30_000 })
    await expect(r.getByRole('gridcell').nth(-1)).toHaveText(/^\d+(\.\d+)?(Ki|Mi|Gi)$/)
  }
  await usage(row(services, 'logger'))
  const containers = await kindPage(page, 'Containers')
  await usage(row(containers, 'ocular-fixture-logger-1'))
  await expect(page.getByRole('note', { name: 'CPU/Memory' })).toHaveCount(0) // nothing to explain
})

test('stop and start a service from its row menu: its container, the plan, the sum', async ({ page }) => {
  await openDind(page, 'ocular-other')
  const grid = await kindPage(page, 'Services')
  const idle = row(grid, 'idle')
  await expect(idle).toContainText('1/1')
  try {
    await idle.click({ button: 'right' })
    await page.getByRole('menu').getByRole('menuitem', { name: /^Stop/ }).click()
    const dialog = page.getByRole('dialog', { name: 'Stop idle' })
    await expect(dialog).toContainText('Stops ocular-other-idle-1 with SIGTERM; if it has not exited after 10 s, it is killed (SIGKILL).')
    await expect(dialog).toContainText('The Docker Engine takes no preconditions')
    await expect(dialog).toContainText('Permission: could not be checked')
    await dialog.getByRole('button', { name: 'Stop' }).click()
    await expect(dialog).toBeHidden({ timeout: 30_000 }) // sleep as PID 1 ignores SIGTERM: killed after 10 s
    await expect(page.getByRole('status')).toHaveText('Stop idle: 1 of 1 done')
    await expect(idle).toContainText('0/1')

    await idle.click({ button: 'right' })
    await page.getByRole('menu').getByRole('menuitem', { name: /^Start/ }).click()
    const start = page.getByRole('dialog', { name: 'Start idle' })
    await expect(start).toContainText('Starts ocular-other-idle-1.')
    await start.getByRole('button', { name: 'Start' }).click()
    await expect(start).toBeHidden()
    await expect(page.getByRole('status')).toHaveText('Start idle: 1 of 1 done')
    await expect(idle).toContainText('1/1')
  } finally {
    docker('start', 'ocular-other-idle-1')
  }
})

test('a terminal in a chosen replica runs in that container', async ({ page }) => {
  await openDind(page)
  const grid = await kindPage(page, 'Services')
  await row(grid, 'logger').click()
  await page.keyboard.press('Shift+S')
  const dlg = page.getByRole('dialog', { name: 'Open a terminal' })
  await dlg.getByRole('combobox').first().selectOption({ label: 'ocular-fixture-logger-2' })
  await dlg.getByRole('button', { name: 'Open' }).click()
  await expectScreen(page, '# ', 30_000)
  const hostname = docker('inspect', '-f', '{{.Config.Hostname}}', 'ocular-fixture-logger-2').trim()
  await page.keyboard.type('echo host=$(hostname)')
  await page.keyboard.press('Enter')
  await expectScreen(page, `host=${hostname}`)
})

// The dialog's handling of a run of several parts, on a real plan (the
// logger's two replicas); the run's answer is the page's own (nothing is
// sent to the daemon), as a refused second replica would give it.
test('a service run of several parts: each part in the dialog, the sum in a notice', async ({ page }) => {
  await openDind(page)
  const grid = await kindPage(page, 'Services')
  let ran = 0
  await page.route('**/api/RunAction', async (route) => {
    ran++
    await route.fulfill({
      json: {
        message: '1 of 2 containers restarted; 1 refused',
        outcome: 'refused',
        parts: [
          { id: 'a', title: 'ocular-fixture-logger-1', outcome: 'done', message: 'container ocular-fixture-logger-1 restarted' },
          { id: 'b', title: 'ocular-fixture-logger-2', outcome: 'refused', message: 'the Docker Engine refused: cannot restart' },
        ],
      },
    })
  })
  await row(grid, 'logger').click({ button: 'right' })
  await page.getByRole('menu').getByRole('menuitem', { name: /^Restart/ }).click()
  const dialog = page.getByRole('dialog', { name: 'Restart logger' })
  await expect(dialog).toContainText('Restarts ocular-fixture-logger-1')
  await expect(dialog).toContainText('Restarts ocular-fixture-logger-2')
  await dialog.getByRole('button', { name: 'Restart' }).click()
  await expect(dialog.getByRole('alert')).toContainText('Stopped at a refusal')
  const parts = dialog.getByRole('list', { name: 'Result' }).getByRole('listitem')
  await expect(parts).toHaveText(['ocular-fixture-logger-1Done', 'ocular-fixture-logger-2Refused · the Docker Engine refused: cannot restart'])
  await expect(page.getByRole('status')).toHaveText('Restart logger: 1 of 2 done; 1 refused')
  await expect(dialog.getByRole('button', { name: 'Restart' })).toHaveCount(0)
  await dialog.getByRole('button', { name: 'Close' }).click()
  await expect(dialog).toBeHidden()
  expect(ran).toBe(1)
})
