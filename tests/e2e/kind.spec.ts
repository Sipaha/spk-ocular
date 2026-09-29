import { expect, test, type Page } from '@playwright/test'
import { execFileSync } from 'node:child_process'

const kc = process.env.OCULAR_KIND_KUBECONFIG!
const kubectl = (...args: string[]) => execFileSync('kubectl', ['--kubeconfig', kc, '--context', 'kind-ocular-dev', ...args], { encoding: 'utf8' })

async function openTarget(page: Page, name: string) {
  await page.goto('/')
  await page.getByRole('option', { name: new RegExp(`^${name}\\b`) }).click()
  await expect(page.getByRole('navigation', { name: 'resources' })).toBeVisible()
}

async function kindPage(page: Page, kind: string, ns = 'ocular-demo') {
  await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: kind, exact: true }).click()
  const picker = page.getByRole('combobox', { name: 'Namespace' })
  if (ns && (await picker.count())) await picker.selectOption(ns)
  return page.getByRole('grid', { name: 'resources' })
}

const row = (grid: ReturnType<Page['getByRole']>, name: string | RegExp) => grid.getByRole('row').filter({ has: grid.page().getByRole('gridcell', { name, exact: typeof name === 'string' }) })

test('pods show health from the real cluster', async ({ page }) => {
  await openTarget(page, 'kind-ocular-dev')
  const grid = await kindPage(page, 'Pods')
  await expect(row(grid, 'bad-image')).toContainText(/ImagePullBackOff|ErrImagePull/)
  await expect(row(grid, 'bad-image').getByRole('gridcell').nth(2)).toHaveClass(/text-danger/)
  await expect(row(grid, 'unschedulable')).toContainText('Pending')
  await expect(row(grid, /^web-/).first()).toContainText('Running')
  // metrics-server is installed in the fixture: memory shows up
  await expect(row(grid, /^web-/).first()).toContainText(/\d+(\.\d)?Mi/, { timeout: 45_000 })
})

test('scaling a deployment updates the table live', async ({ page }) => {
  await openTarget(page, 'kind-ocular-dev')
  const grid = await kindPage(page, 'Pods')
  // Live = not terminating: a scaled-down pod shows "Terminating" for up to
  // its grace period before its row goes.
  const live = () => row(grid, /^web-/).filter({ hasNotText: 'Terminating' })
  await expect(live()).toHaveCount(3)
  kubectl('-n', 'ocular-demo', 'scale', 'deploy/web', '--replicas=4')
  try {
    await expect(live()).toHaveCount(4)
  } finally {
    kubectl('-n', 'ocular-demo', 'scale', 'deploy/web', '--replicas=3')
  }
  await expect(live()).toHaveCount(3)
  await expect(row(grid, /^web-/)).toHaveCount(3, { timeout: 45_000 }) // and the row is removed
})

test('details: relations, events and yaml of a deployment', async ({ page }) => {
  await openTarget(page, 'kind-ocular-dev')
  const grid = await kindPage(page, 'Deployments')
  await row(grid, 'web').click()
  const drawer = page.getByRole('dialog', { name: 'apps/deployments web' })
  await expect(drawer.getByText('Owns')).toBeVisible()
  await expect(drawer.getByRole('button', { name: /^pods\/web-/ })).toHaveCount(3, { timeout: 45_000 })
  await expect(drawer.getByRole('region', { name: 'Events' }).getByText('ScalingReplicaSet').first()).toBeVisible()
  await drawer.getByRole('tab', { name: 'YAML' }).click()
  await expect(drawer.locator('.cm-editor')).toContainText('kind: Deployment')
  await drawer.getByRole('tab', { name: 'Details' }).click()
  await drawer.getByRole('button', { name: /^pods\/web-/ }).first().click()
  await expect(page.getByRole('dialog', { name: /^pods web-/ }).getByText('Runs on')).toBeVisible()
})

test('secrets never show their values', async ({ page }) => {
  await openTarget(page, 'kind-ocular-dev')
  const grid = await kindPage(page, 'Secrets')
  await expect(row(grid, 'web-credentials')).toContainText('password, username')
  await row(grid, 'web-credentials').click()
  const drawer = page.getByRole('dialog', { name: 'secrets web-credentials' })
  await drawer.getByRole('tab', { name: 'YAML' }).click()
  await expect(drawer.locator('.cm-editor')).toContainText('password: <19 bytes>')
  await expect(page.getByText('not-a-real-password')).toHaveCount(0)
})

test('a namespace-limited user: denied is explained, own namespace works', async ({ page }) => {
  await openTarget(page, 'ocular-viewer')
  // Listing namespaces is forbidden: the picker becomes a text field with the
  // kubeconfig's default namespace, which works.
  const grid = page.getByRole('grid', { name: 'resources' })
  await expect(row(grid, /^web-/).first()).toBeVisible()
  const input = page.getByRole('textbox', { name: 'Namespace' })
  await input.fill('')
  await input.press('Enter') // empty = all namespaces → denied, not an empty table
  await expect(page.getByRole('alert').filter({ hasText: 'access denied' })).toBeVisible()
  await expect(page.getByText('No objects')).toHaveCount(0)
  await input.fill('ocular-demo')
  await input.press('Enter')
  await expect(row(grid, 'crashloop')).toBeVisible()
})

// Review 2026-09-29: a ConfigMap value change does not change its table row;
// the open YAML must still refresh.
test('open details follow changes the table does not show', async ({ page }) => {
  await openTarget(page, 'kind-ocular-dev')
  const grid = await kindPage(page, 'ConfigMaps')
  await row(grid, 'web-config').click()
  const drawer = page.getByRole('dialog', { name: 'configmaps web-config' })
  await drawer.getByRole('tab', { name: 'YAML' }).click()
  await expect(drawer.locator('.cm-editor')).toContainText('LOG_LEVEL: debug')
  kubectl('-n', 'ocular-demo', 'patch', 'configmap', 'web-config', '--type=merge', '-p', '{"data":{"LOG_LEVEL":"info"}}')
  try {
    await expect(drawer.locator('.cm-editor')).toContainText('LOG_LEVEL: info')
  } finally {
    kubectl('-n', 'ocular-demo', 'patch', 'configmap', 'web-config', '--type=merge', '-p', '{"data":{"LOG_LEVEL":"debug"}}')
  }
})

const logPanel = (page: Page) => page.locator('[role=tabpanel]:not([hidden])')
const logRows = (page: Page) => logPanel(page).locator('[data-log-viewport] [data-index]')

test('logs of a pod from its details; previous of a crash-looping one', async ({ page }) => {
  await openTarget(page, 'kind-ocular-dev')
  const grid = await kindPage(page, 'Pods')
  await row(grid, 'crashloop').click()
  await page.keyboard.press('Enter')
  await page.getByRole('dialog').getByRole('button', { name: 'Logs' }).click()
  await expect(logRows(page).first()).toHaveText('starting', { timeout: 30_000 })
  await logPanel(page).getByRole('button', { name: 'Previous' }).click()
  await expect(logPanel(page).getByLabel('stream state')).toHaveText('Complete', { timeout: 30_000 })
  await expect(logRows(page).first()).toHaveText('starting')
})

test('logs of a deployment: every pod in one stream, live', async ({ page }) => {
  await openTarget(page, 'kind-ocular-dev')
  const grid = await kindPage(page, 'Deployments')
  await row(grid, 'chatter').click()
  await page.keyboard.press('l')
  const panel = logPanel(page)
  await expect(panel.getByLabel('stream state')).toHaveText('Live', { timeout: 30_000 })
  const pods = kubectl('-n', 'ocular-demo', 'get', 'pods', '-l', 'app=chatter', '-o', 'jsonpath={.items[*].metadata.name}').trim().split(/\s+/)
  expect(pods).toHaveLength(2)
  for (const p of pods) await expect(logRows(page).filter({ hasText: `${p} tick` }).first()).toBeAttached({ timeout: 30_000 })
  const before = await logRows(page).last().textContent()
  await expect.poll(async () => logRows(page).last().textContent(), { timeout: 10_000 }).not.toBe(before) // live
  // ANSI colours from the pods reach the page as styles, not escape codes
  await expect(panel.locator('[data-log-viewport]')).not.toContainText('[31m')
  await expect(panel.locator('[data-log-viewport] span[style*="--color-ansi-1"]').first()).toBeAttached()
})

const termScreen = (page: Page) => logPanel(page).locator('.xterm-rows')

test('a terminal in a pod: a shell, output, the exit code', async ({ page }) => {
  await openTarget(page, 'kind-ocular-dev')
  const grid = await kindPage(page, 'Pods')
  await row(grid, /^web-/).first().click()
  await page.keyboard.press('s')
  await expect(page.getByRole('tab', { name: /^nginx · web-/ })).toBeVisible()
  await expect(termScreen(page)).toContainText('#', { timeout: 30_000 }) // the prompt
  // busybox ash swallows what is typed right after its prompt (it races its
  // own cursor-position query, ESC[6n; kubectl exec -it loses it the same
  // way): settle with empty lines until one gives a new prompt.
  const prompts = async () => ((await termScreen(page).textContent()) ?? '').split('#').length
  await expect(async () => {
    const n = await prompts()
    await page.keyboard.press('Enter')
    await expect.poll(prompts, { timeout: 1000 }).toBeGreaterThan(n)
  }).toPass({ timeout: 15_000 })
  await page.keyboard.type('echo "sum=$((40+2))"')
  await page.keyboard.press('Enter')
  await expect(termScreen(page)).toContainText('sum=42')
  await page.keyboard.type('exit 3')
  await page.keyboard.press('Enter')
  await expect(logPanel(page).getByRole('alert')).toContainText('process exited with code 3')
})

test('a terminal needs pods/exec: denied is said in the tab', async ({ page }) => {
  await openTarget(page, 'ocular-viewer')
  const grid = page.getByRole('grid', { name: 'resources' })
  await row(grid, /^web-/).first().click()
  await page.keyboard.press('s')
  await expect(logPanel(page).getByRole('alert')).toContainText('access denied')
})

test('a tunnel to a service: the runner reaches nginx through it', async ({ page }) => {
  await openTarget(page, 'kind-ocular-dev')
  const grid = await kindPage(page, 'Services')
  await row(grid, 'web').click()
  await page.keyboard.press('Enter')
  const ports = page.getByRole('dialog', { name: 'services web' }).getByRole('region', { name: 'Ports' })
  await ports.getByRole('listitem').filter({ hasText: '80' }).getByRole('button', { name: 'Forward' }).click()
  const dlg = page.getByRole('dialog', { name: 'Forward a port' })
  await dlg.getByRole('combobox').selectOption('http')
  await dlg.getByRole('button', { name: 'Forward' }).click()
  const tunnel = page.getByRole('region', { name: 'Port forwards' }).getByRole('listitem', { name: 'services/web:80' })
  await expect(tunnel).toContainText('connected')
  await expect(tunnel).toContainText(/via web-[\w-]+:80/)
  const addr = (await tunnel.locator('[data-address]').textContent())!.split(/\s+/)[0]
  const res = await fetch(`http://${addr}/`)
  expect(res.status).toBe(200)
  expect(await res.text()).toContain('Welcome to nginx')
  await tunnel.getByRole('button', { name: 'Stop' }).click()
  await expect(tunnel).toHaveCount(0)
  await expect(fetch(`http://${addr}/`)).rejects.toThrow()
})

test('a narrow window scrolls the table sideways, never the page', async ({ page }) => {
  await page.setViewportSize({ width: 1000, height: 800 })
  await openTarget(page, 'kind-ocular-dev')
  const grid = (await kindPage(page, 'Pods', '')).first() // the drawer's events are a grid too
  await row(grid, /^web-/).first().click()
  await expect(page.getByRole('dialog', { name: /^pods web-/ })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBe(0)
  await grid.locator('[data-table-scroll]').evaluate((e) => (e.scrollLeft = 150))
  await expect.poll(() => grid.evaluate((g) => g.children[0].scrollLeft)).toBe(150) // the header follows
  // Scrolled to the end, the last column is reachable and its header sits over it.
  await grid.locator('[data-table-scroll]').evaluate((e) => (e.scrollLeft = e.scrollWidth))
  await expect
    .poll(() =>
      grid.evaluate((g) => {
        const heads = g.querySelectorAll('[role=columnheader]')
        const cells = g.querySelector('[data-table-scroll] [role=row]')!.querySelectorAll('[role=gridcell]')
        const h = heads[heads.length - 1].getBoundingClientRect()
        const c = cells[cells.length - 1].getBoundingClientRect()
        const view = g.querySelector('[data-table-scroll]')!.getBoundingClientRect()
        return [Math.round(h.left - c.left), Math.round(h.right - c.right), c.right <= view.right + 1]
      }),
    )
    .toEqual([0, 0, true])
})
