import { expect, type Page } from '@playwright/test'
import { test } from './fixtures'
import { execFileSync } from 'node:child_process'
import { pickOption, pickScope, selects } from './fixtures'

const kc = process.env.OCULAR_KIND_KUBECONFIG!
const kubectl = (...args: string[]) => execFileSync('kubectl', ['--kubeconfig', kc, '--context', 'kind-ocular-dev', ...args], { encoding: 'utf8' })

async function openTarget(page: Page, name: string) {
  await page.goto('/')
  await page.getByRole('option', { name: new RegExp(`^${name}\\b`) }).click()
  await expect(page.getByRole('navigation', { name: 'resources' })).toBeVisible()
}

async function kindPage(page: Page, kind: string, ns = 'ocular-demo') {
  await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: kind, exact: true }).click()
  if (ns && (await page.getByRole('button', { name: 'Namespace', exact: true }).count())) await pickScope(page, 'Namespace', ns)
  return page.getByRole('grid', { name: 'resources' })
}

const row = (grid: ReturnType<Page['getByRole']>, name: string | RegExp) => grid.getByRole('row').filter({ has: grid.page().getByRole('gridcell', { name, exact: typeof name === 'string' }) })

test('pods show health from the real cluster', async ({ page }) => {
  await openTarget(page, 'kind-ocular-dev')
  const grid = await kindPage(page, 'Pods')
  await expect(row(grid, 'bad-image')).toContainText(/ImagePullBackOff|ErrImagePull/)
  await expect(row(grid, 'bad-image').getByRole('gridcell').nth(3)).toHaveClass(/text-danger/) // past the mark's cell
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

// P18: two contexts on the same cluster (the viewer's kubeconfig is a second
// file). The left target stays warm in the background — its dot says so —
// and the return shows the rows at once, not a cold open with a loading
// state.
test('a warm return to a recent target shows rows at once', async ({ page }) => {
  await openTarget(page, 'kind-ocular-dev')
  const grid = await kindPage(page, 'Pods')
  await expect(row(grid, /^web-/).first()).toBeVisible()
  // To B: the second context on the same cluster.
  await openTarget(page, 'ocular-viewer')
  const vgrid = page.getByRole('grid', { name: 'resources' })
  await expect(row(vgrid, /^web-/).first()).toBeVisible()
  // A stays open in the background: the dot's tooltip on its row.
  const admin = page.getByRole('option', { name: /^kind-ocular-dev\b/ })
  await expect(admin).toHaveAttribute('title', /Connection open/)
  // Back to A: the rows are there at once (a cold open re-syncs for seconds;
  // the warm budget is 150 ms, the allowance here is generous already).
  await admin.click()
  await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Pods', exact: true }).click()
  const back = page.getByRole('grid', { name: 'resources' })
  await expect(row(back, /^web-/).first()).toBeVisible({ timeout: 1500 })
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
  // Read while the crash-looping container restarts, the kubelet answers
  // "unable to retrieve container logs for containerd://…" (the previous
  // container was just replaced): read it again then.
  const previous = logPanel(page).getByRole('button', { name: 'Previous' })
  await expect(async () => {
    if ((await previous.getAttribute('aria-pressed')) === 'true') await previous.click()
    await previous.click()
    await expect(logPanel(page).getByLabel('stream state')).toHaveText('Complete', { timeout: 30_000 })
    await expect(logRows(page).first()).toHaveText('starting', { timeout: 2_000 })
  }).toPass({ timeout: 90_000 })
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
  await pickOption(selects(dlg).first(), 'http')
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

// Actions (P4) act on objects of the test's own; the fixture stays as it is.
function ownDeployment(name: string, replicas: number) {
  kubectl('-n', 'ocular-demo', 'create', 'deployment', name, '--image=nginx:1.27-alpine', `--replicas=${replicas}`)
  kubectl('-n', 'ocular-demo', 'rollout', 'status', `deployment/${name}`, '--timeout=120s')
  return () => kubectl('-n', 'ocular-demo', 'delete', 'deployment', name, '--ignore-not-found', '--wait=false')
}

test('restart a deployment from its details: it rolls out and settles', async ({ page }) => {
  const cleanup = ownDeployment('act-restart', 2)
  try {
    await openTarget(page, 'kind-ocular-dev')
    const grid = await kindPage(page, 'Deployments')
    await row(grid, 'act-restart').click()
    const drawer = page.getByRole('dialog', { name: 'apps/deployments act-restart' })
    await drawer.getByRole('button', { name: /Actions/ }).click()
    await page.getByRole('menu', { name: 'Actions' }).getByRole('menuitem', { name: 'Restart' }).click()
    const dialog = page.getByRole('dialog', { name: 'Restart act-restart' })
    await expect(dialog.getByLabel('where')).toContainText('Contextkind-ocular-dev')
    await expect(dialog.getByLabel('where')).toContainText('Namespaceocular-demo')
    await expect(dialog).toContainText('Pods are replaced gradually (rolling update: max unavailable 25%, max surge 25%).')
    await expect(dialog).toContainText('Permission: checked: allowed')
    await dialog.getByRole('button', { name: 'Restart' }).click()
    await expect(page.getByRole('status')).toHaveText('deployment act-restart: restart requested')
    // The row goes through the rollout and comes back healthy.
    await expect(row(grid, 'act-restart')).toHaveAttribute('title', /^RollingOut/, { timeout: 30_000 })
    await expect(row(grid, 'act-restart')).not.toHaveAttribute('title', /^RollingOut/, { timeout: 90_000 })
    expect(kubectl('-n', 'ocular-demo', 'get', 'deployment', 'act-restart', '-o', 'jsonpath={.spec.template.metadata.annotations}')).toContain('kubectl.kubernetes.io/restartedAt')
  } finally {
    cleanup()
  }
})

test('scale a deployment 2 → 1 in two steps', async ({ page }) => {
  const cleanup = ownDeployment('act-scale', 2)
  try {
    await openTarget(page, 'kind-ocular-dev')
    const grid = await kindPage(page, 'Deployments')
    await row(grid, 'act-scale').click({ button: 'right' })
    await page.getByRole('menu', { name: 'Row actions' }).getByRole('menuitem', { name: 'Scale…' }).click()
    const dialog = page.getByRole('dialog', { name: 'Scale act-scale' })
    const count = dialog.getByRole('textbox')
    await expect(count).toHaveValue('2')
    await count.fill('1')
    await count.press('Enter')
    await expect(dialog).toContainText('2 → 1: 1 pod is removed.')
    expect(kubectl('-n', 'ocular-demo', 'get', 'deployment', 'act-scale', '-o', 'jsonpath={.spec.replicas}')).toBe('2') // reviewed only
    await dialog.getByRole('button', { name: 'Scale' }).click()
    await expect(page.getByRole('status')).toHaveText('deployment act-scale: scale 2 → 1 requested')
    await expect(row(grid, 'act-scale')).toContainText('1/1', { timeout: 60_000 })
    expect(kubectl('-n', 'ocular-demo', 'get', 'deployment', 'act-scale', '-o', 'jsonpath={.spec.replicas}')).toBe('1')
  } finally {
    cleanup()
  }
})

test('roll a deployment back to its first revision from the table', async ({ page }) => {
  const cleanup = ownDeployment('act-undo', 1) // revision 1: no env
  try {
    kubectl('-n', 'ocular-demo', 'set', 'env', 'deployment/act-undo', 'V=b') // revision 2
    kubectl('-n', 'ocular-demo', 'rollout', 'status', 'deployment/act-undo', '--timeout=120s')
    await openTarget(page, 'kind-ocular-dev')
    const grid = await kindPage(page, 'Deployments')
    await row(grid, 'act-undo').click({ button: 'right' })
    await page.getByRole('menu', { name: 'Row actions' }).getByRole('menuitem', { name: 'Roll back…' }).click()
    const dialog = page.getByRole('dialog', { name: 'Roll back act-undo' })
    const radios = dialog.getByRole('radiogroup').getByRole('radio')
    await expect(radios).toHaveCount(2)
    await expect(radios.nth(0)).toBeDisabled() // revision 2, the current template
    await dialog.getByRole('radiogroup').getByText('Revision 1').click()
    await expect(dialog.getByRole('region', { name: 'Changes' })).toContainText('containers[nginx].env[V]: removed')
    await expect(dialog).toContainText('The pod template becomes that of revision 1; it becomes revision 3.')
    await expect(dialog).toContainText('Permission: checked: allowed')
    await dialog.getByRole('button', { name: 'Roll back' }).click()
    await expect(page.getByRole('status')).toHaveText('deployment act-undo: rollback to revision 1 requested')
    expect(kubectl('-n', 'ocular-demo', 'get', 'deployment', 'act-undo', '-o', 'jsonpath={.spec.template.spec.containers[0].env}')).toBe('')
    await expect
      .poll(() => kubectl('-n', 'ocular-demo', 'get', 'deployment', 'act-undo', '-o', 'jsonpath={.metadata.annotations.deployment\\.kubernetes\\.io/revision}'), { timeout: 30_000 })
      .toBe('3')
  } finally {
    cleanup()
  }
})

test('debug a pod: the debugger’s terminal opens with its prompt, sees the target, ends with its exit code', async ({ page }) => {
  const cleanup = ownDeployment('act-debug', 1) // the debugger stays in the pod's spec: the pod goes with the deployment
  try {
    const pod = kubectl('-n', 'ocular-demo', 'get', 'pods', '-l', 'app=act-debug', '-o', 'jsonpath={.items[0].metadata.name}').trim()
    await openTarget(page, 'kind-ocular-dev')
    const grid = await kindPage(page, 'Pods')
    await page.getByRole('textbox', { name: 'Filter rows' }).fill('act-debug')
    await row(grid, pod).click({ button: 'right' })
    await page.getByRole('menu', { name: 'Row actions' }).getByRole('menuitem', { name: 'Debug…' }).click()
    const dialog = page.getByRole('dialog', { name: `Debug ${pod}` })
    await expect(dialog.getByRole('textbox', { name: 'Image' })).toHaveValue('busybox:1.36')
    await expect(dialog.getByRole('radio', { name: /nginx/ })).toBeChecked()
    await expect(dialog).toContainText(/Debug container debugger-[a-z0-9]{5} with image busybox:1\.36 is added to pod/)
    await expect(dialog).toContainText('Permission: checked: allowed')
    await dialog.getByRole('button', { name: 'Debug' }).click()
    await expect(dialog).toBeHidden()
    await expect(page.getByRole('tab', { name: new RegExp(`^debugger-[a-z0-9]{5} · ${pod}`) })).toBeVisible()
    await expect(termScreen(page)).toContainText('/ #', { timeout: 60_000 }) // the prompt, no key pressed
    // busybox ash may swallow what is typed right after its prompt (see the terminal test).
    const prompts = async () => ((await termScreen(page).textContent()) ?? '').split('#').length
    await expect(async () => {
      const n = await prompts()
      await page.keyboard.press('Enter')
      await expect.poll(prompts, { timeout: 1000 }).toBeGreaterThan(n)
    }).toPass({ timeout: 15_000 })
    await page.keyboard.type("ps | grep '[n]ginx: master'") // the target's processes (its many workers would scroll it away)
    await page.keyboard.press('Enter')
    await expect(termScreen(page)).toContainText('nginx: master process')
    await page.keyboard.type('exit 3')
    await page.keyboard.press('Enter')
    await expect(logPanel(page).getByRole('alert')).toContainText('process exited with code 3')
    expect(kubectl('-n', 'ocular-demo', 'get', 'pod', pod, '-o', 'jsonpath={.status.ephemeralContainerStatuses[0].state.terminated.exitCode}')).toBe('3')
  } finally {
    cleanup()
  }
})

test('Delete on a pod row: the ReplicaSet creates a new one', async ({ page }) => {
  const cleanup = ownDeployment('act-del', 1)
  try {
    const old = kubectl('-n', 'ocular-demo', 'get', 'pods', '-l', 'app=act-del', '-o', 'jsonpath={.items[0].metadata.name}').trim()
    await openTarget(page, 'kind-ocular-dev')
    const grid = await kindPage(page, 'Pods')
    const filter = page.getByRole('textbox', { name: 'Filter rows' })
    await filter.fill('act-del')
    await expect(row(grid, old)).toBeVisible()
    await filter.press('ArrowDown') // into the table
    await page.keyboard.press('ArrowDown')
    await page.keyboard.press('Delete')
    const dialog = page.getByRole('dialog', { name: `Delete ${old}` })
    await expect(dialog).toContainText(/ReplicaSet act-del-\w+ normally creates a replacement\./)
    await expect(dialog).toContainText('This is not an eviction')
    await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeFocused()
    await dialog.getByRole('button', { name: 'Delete' }).click()
    await expect(page.getByRole('status')).toHaveText(`pod ${old}: deletion requested`)
    await expect(row(grid, /^act-del-/).filter({ hasNotText: old })).toHaveCount(1, { timeout: 60_000 })
    await expect(row(grid, old)).toHaveCount(0, { timeout: 60_000 })
  } finally {
    cleanup()
  }
})

test('bulk: the marked pods of a deployment deleted in one review; each replaced', async ({ page }) => {
  const cleanup = ownDeployment('act-bulk', 3)
  try {
    const old = kubectl('-n', 'ocular-demo', 'get', 'pods', '-l', 'app=act-bulk', '-o', 'jsonpath={.items[*].metadata.name}').trim().split(/\s+/)
    expect(old).toHaveLength(3)
    await openTarget(page, 'kind-ocular-dev')
    const grid = await kindPage(page, 'Pods')
    const filter = page.getByRole('textbox', { name: 'Filter rows' })
    await filter.fill('act-bulk')
    for (const n of old) await expect(row(grid, n)).toBeVisible()
    await filter.press('ArrowDown') // into the table
    await page.keyboard.press('ControlOrMeta+a')
    await expect(page.getByRole('toolbar', { name: 'Marked' })).toContainText('Marked: 3 of 3')
    await page.keyboard.press('Delete')
    const dialog = page.getByRole('dialog', { name: 'Delete 3 objects' })
    await expect(dialog).toContainText('Will run: 3; skipped: 0')
    await expect(dialog).toContainText(/This is not an eviction.* — 3 objects/)
    await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeFocused()
    await dialog.getByRole('button', { name: 'Delete 3' }).click()
    await expect(dialog.getByRole('status')).toHaveText('3 of 3 done')
    await dialog.getByRole('button', { name: 'Close' }).click()
    for (const n of old) await expect(row(grid, n)).toHaveCount(0, { timeout: 90_000 })
    await expect(row(grid, /^act-bulk-/)).toHaveCount(3, { timeout: 90_000 })
  } finally {
    cleanup()
  }
})

test('a user without the right sees it in the review', async ({ page }) => {
  await openTarget(page, 'ocular-viewer')
  const grid = page.getByRole('grid', { name: 'resources' })
  const pod = row(grid, /^web-/).first()
  const name = (await pod.getByRole('gridcell').nth(1).textContent())!.trim() // past the mark's cell
  await pod.click({ button: 'right' })
  await page.getByRole('menu', { name: 'Row actions' }).getByRole('menuitem', { name: 'Delete', exact: true }).click()
  const dialog = page.getByRole('dialog', { name: `Delete ${name}` })
  await expect(dialog).toContainText('Permission: not allowed: you may not delete pods in ocular-demo')
  await expect(dialog.getByRole('button', { name: 'Delete' })).toBeDisabled()
  await dialog.getByRole('button', { name: 'Cancel' }).click()
  expect(kubectl('-n', 'ocular-demo', 'get', 'pod', name, '-o', 'jsonpath={.metadata.name}')).toBe(name)
})

// ---- P8: every listable resource (discovery + server-side Tables)

/** The navigation's discovered kind (API groups and its subgroup expanded). */
async function apiKind(page: Page, sub: string, kind: string, ns?: string) {
  const nav = page.getByRole('navigation', { name: 'resources' })
  const fold = nav.getByRole('region', { name: 'API groups' }).getByRole('button', { name: /^API groups/ })
  if ((await fold.getAttribute('aria-expanded')) === 'false') await fold.click()
  const group = nav.getByRole('group', { name: sub })
  const head = group.getByRole('button', { name: new RegExp(`^${sub.replace(/\./g, '\\.')}`) })
  if ((await head.getAttribute('aria-expanded')) === 'false') await head.click()
  await group.getByRole('button', { name: kind, exact: true }).click()
  await expect(page.getByRole('heading', { name: kind })).toBeVisible()
  if (ns) await pickScope(page, 'Namespace', ns)
  return page.getByRole('grid', { name: 'resources' })
}

test('custom resources: API groups in the navigation, the CRD columns, live changes', async ({ page }) => {
  await openTarget(page, 'kind-ocular-dev')
  const section = page.getByRole('navigation', { name: 'resources' }).getByRole('region', { name: 'API groups' })
  // The rare and the custom: folded by default.
  await expect(section.getByRole('button', { name: /^API groups/ })).toHaveAttribute('aria-expanded', 'false')
  await expect(section.getByRole('button', { name: /^ocular\.dev/ })).toBeHidden()
  const grid = await apiKind(page, 'ocular.dev', 'Widgets', 'ocular-crd')
  // kubectl get's columns (Detail is wide: a fact of the details)
  // The marks' column (P17) comes first, its header a checkbox without text.
  await expect(grid.getByRole('columnheader')).toHaveText(['', /Name/, /Size/, /Phase/, /Ready/, /Since/])
  await expect(row(grid, 'alpha')).toContainText('Running')
  await expect(row(grid, 'beta')).toContainText('Failing')
  // health from the conditions: a dot by the name, the reason on hover
  await expect(row(grid, 'beta').locator('[data-health]')).toHaveAttribute('data-health', 'error')
  await expect(row(grid, 'beta')).toHaveAttribute('title', 'Broken: the widget is broken')
  await expect(row(grid, 'alpha').locator('[data-health]')).toHaveAttribute('data-health', 'ok')
  await expect(row(grid, 'alpha').getByRole('gridcell').last()).toHaveText(/^\d+(s|m|h|d)/)
  kubectl('-n', 'ocular-crd', 'patch', 'widget', 'gamma', '--type=merge', '-p', '{"spec":{"size":11}}')
  try {
    await expect(row(grid, 'gamma').getByRole('gridcell').nth(2)).toHaveText('11') // past the mark's cell
  } finally {
    kubectl('-n', 'ocular-crd', 'patch', 'widget', 'gamma', '--type=merge', '-p', '{"spec":{"size":1}}')
  }
})

test('details of a custom resource: wide columns are facts, the YAML is the object', async ({ page }) => {
  await openTarget(page, 'kind-ocular-dev')
  const grid = await apiKind(page, 'ocular.dev', 'Widgets', 'ocular-crd')
  await row(grid, 'alpha').click()
  const drawer = page.getByRole('dialog', { name: 'ocular.dev/widgets alpha' })
  await expect(drawer.getByText('Detail', { exact: true })).toBeVisible()
  await expect(drawer.getByText('first', { exact: true })).toBeVisible()
  await drawer.getByRole('tab', { name: 'YAML' }).click()
  await expect(drawer.locator('.cm-editor')).toContainText('kind: Widget')
  await expect(drawer.locator('.cm-editor')).not.toContainText('ocularCells')
})

test('the palette opens a custom resource by its short name', async ({ page }) => {
  await openTarget(page, 'kind-ocular-dev')
  await page.keyboard.press('Control+k')
  await page.keyboard.type(':wd')
  await page.keyboard.press('Enter')
  await expect(page.getByRole('heading', { name: 'Widgets' })).toBeVisible()
})

test('built-ins without a described view: Jobs in Workloads with the server columns', async ({ page }) => {
  await openTarget(page, 'kind-ocular-dev')
  const grid = await kindPage(page, 'Jobs', 'ocular-crd')
  await expect(page.getByRole('navigation', { name: 'resources' }).getByRole('region', { name: 'Workloads' }).getByRole('button', { name: 'Jobs', exact: true })).toBeVisible()
  await expect(grid.getByRole('columnheader')).toHaveText(['', /Name/, /Status/, /Completions/, /Duration/, /Age/])
  await expect(row(grid, 'once')).toContainText('Complete')
  await expect(row(grid, 'once')).toContainText('1/1')
})

test('deleting a custom resource with a finalizer: the review says so, deletion waits for it', async ({ page }) => {
  const cleanup = () => {
    try {
      kubectl('-n', 'ocular-crd', 'patch', 'widget', 'delta', '--type=merge', '-p', '{"metadata":{"finalizers":null}}')
    } catch {
      // already gone
    }
    kubectl('-n', 'ocular-crd', 'delete', 'widget', 'delta', '--ignore-not-found', '--wait=true', '--timeout=30s')
  }
  cleanup() // a run that died before its cleanup
  execFileSync('kubectl', ['--kubeconfig', kc, '--context', 'kind-ocular-dev', 'apply', '-f', '-'], {
    input: 'apiVersion: ocular.dev/v1\nkind: Widget\nmetadata: {name: delta, namespace: ocular-crd, finalizers: [ocular.dev/hold]}\nspec: {size: 2}\n',
  })
  try {
    await openTarget(page, 'kind-ocular-dev')
    const grid = await apiKind(page, 'ocular.dev', 'Widgets', 'ocular-crd')
    await row(grid, 'delta').click({ button: 'right' })
    await page.getByRole('menu', { name: 'Row actions' }).getByRole('menuitem', { name: 'Delete', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: 'Delete delta' })
    await expect(dialog).toContainText('Deletion waits for its finalizers: ocular.dev/hold.')
    await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeFocused()
    await dialog.getByRole('button', { name: 'Delete' }).click()
    await expect(page.getByRole('status')).toHaveText('widget delta: deletion requested')
    await expect(row(grid, 'delta').locator('[data-health]')).toHaveAttribute('data-health', 'terminating')
    await expect(row(grid, 'delta')).toHaveAttribute('title', 'Terminating')
    kubectl('-n', 'ocular-crd', 'patch', 'widget', 'delta', '--type=merge', '-p', '{"metadata":{"finalizers":null}}')
    await expect(row(grid, 'delta')).toHaveCount(0)
  } finally {
    cleanup()
  }
})

test('a CRD created while the target is open appears; deleted, its open view says it is no longer served', async ({ page }) => {
  const crd = 'things.e2e.ocular.dev'
  const cleanup = () => kubectl('delete', 'crd', crd, '--ignore-not-found', '--wait=false')
  cleanup()
  try {
    await openTarget(page, 'kind-ocular-dev')
    execFileSync('kubectl', ['--kubeconfig', kc, '--context', 'kind-ocular-dev', 'apply', '-f', '-'], {
      input: `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata: {name: ${crd}}
spec:
  group: e2e.ocular.dev
  scope: Cluster
  names: {plural: things, singular: thing, kind: Thing}
  versions:
  - {name: v1, served: true, storage: true, schema: {openAPIV3Schema: {type: object, x-kubernetes-preserve-unknown-fields: true}}}
`,
    })
    kubectl('wait', '--for=condition=Established', `crd/${crd}`, '--timeout=60s')
    execFileSync('kubectl', ['--kubeconfig', kc, '--context', 'kind-ocular-dev', 'apply', '-f', '-'], { input: 'apiVersion: e2e.ocular.dev/v1\nkind: Thing\nmetadata: {name: t1}\n' })
    // No reload: the CRD watch triggers discovery, the UI lists again.
    const nav = page.getByRole('navigation', { name: 'resources' })
    await expect(nav.getByRole('button', { name: 'Things', exact: true })).toBeVisible({ timeout: 30_000 })
    await nav.getByRole('button', { name: 'Things', exact: true }).click()
    const grid = page.getByRole('grid', { name: 'resources' })
    await expect(row(grid, 't1')).toBeVisible()

    kubectl('delete', 'crd', crd, '--wait=true', '--timeout=60s')
    await expect(page.getByRole('alert')).toContainText('no longer served by the API', { timeout: 30_000 })
    await expect(page.getByRole('heading', { name: 'Things' })).toBeVisible()
    await expect(nav.getByRole('button', { name: 'Things', exact: true })).toHaveCount(0)
  } finally {
    cleanup()
  }
})

/** The editor's text (a short document: every line is rendered). */
async function editorText(drawer: ReturnType<Page['getByRole']>) {
  const lines = await drawer.getByLabel('YAML editor').locator('.cm-line').allTextContents()
  return lines.join('\n') + '\n'
}

/** Replaces the editor's whole text, as typing would. */
async function setEditorText(page: Page, drawer: ReturnType<Page['getByRole']>, text: string) {
  await drawer.getByLabel('YAML editor').locator('.cm-content').click()
  await page.keyboard.press('ControlOrMeta+a')
  await page.keyboard.insertText(text)
}

test('edit a ConfigMap as YAML: reviewed by the server, written once; a refusal is said', async ({ page }) => {
  kubectl('-n', 'ocular-demo', 'create', 'configmap', 'e2e-edit', '--from-literal=a=1', '--from-literal=b=2')
  try {
    await openTarget(page, 'kind-ocular-dev')
    const grid = await kindPage(page, 'ConfigMaps')
    await row(grid, 'e2e-edit').click()
    const drawer = page.getByRole('dialog', { name: 'configmaps e2e-edit' })
    await drawer.getByRole('button', { name: 'Edit' }).click()
    await expect(drawer.getByLabel('YAML editor')).toContainText('a: "1"')
    const text = await editorText(drawer)
    expect(text).not.toContain('manager:') // managedFields are not shown
    await setEditorText(page, drawer, text.replace('a: "1"', 'a: "2"'))
    await page.keyboard.press('ControlOrMeta+Enter')

    const review = page.getByRole('dialog', { name: 'Edit e2e-edit' })
    await expect(review).toContainText('checked by the server without writing')
    await expect(review.getByLabel('where')).toContainText('Contextkind-ocular-dev')
    await expect(review.getByLabel('where')).toContainText('Namespaceocular-demo')
    await expect(review.getByRole('region', { name: 'Changes' })).toContainText('lines removed: 1, added: 1')
    expect(kubectl('-n', 'ocular-demo', 'get', 'configmap', 'e2e-edit', '-o', 'jsonpath={.data.a}')).toBe('1') // reviewed only
    await review.getByRole('button', { name: 'Apply' }).click()
    await expect(page.getByRole('status')).toContainText('e2e-edit: changes written')
    await expect(review).toBeHidden()
    expect(kubectl('-n', 'ocular-demo', 'get', 'configmap', 'e2e-edit', '-o', 'jsonpath={.data.a}')).toBe('2')
    // The details follow the object: the viewer shows what was written.
    await expect(drawer.getByLabel('YAML editor')).toBeHidden()
    await expect(drawer.locator('.cm-editor')).toContainText('a: "2"')

    // An unknown field: the server's strict validation refuses the dry run.
    await drawer.getByRole('button', { name: 'Edit' }).click()
    await expect(drawer.getByLabel('YAML editor')).toContainText('a: "2"')
    await setEditorText(page, drawer, (await editorText(drawer)) + 'datta:\n  x: "1"\n')
    await drawer.getByRole('button', { name: /Review changes/ }).click()
    await expect(review.getByRole('alert')).toContainText('The server refuses this edit')
    await expect(review.getByRole('alert')).toContainText('unknown field')
    await expect(review.getByRole('button', { name: 'Apply' })).toBeDisabled()
    await review.getByRole('button', { name: 'Back to the text' }).click()
    // Leaving with the edits asks first; the safe answer has the focus.
    await drawer.getByRole('button', { name: 'Close' }).click()
    const prompt = page.getByRole('alertdialog', { name: 'Discard edits?' })
    await expect(prompt.getByRole('button', { name: 'Keep editing' })).toBeFocused()
    await prompt.getByRole('button', { name: 'Discard' }).click()
    await expect(drawer).toBeHidden()
    expect(kubectl('-n', 'ocular-demo', 'get', 'configmap', 'e2e-edit', '-o', 'jsonpath={.data}')).not.toContain('datta')
  } finally {
    kubectl('-n', 'ocular-demo', 'delete', 'configmap', 'e2e-edit', '--ignore-not-found', '--wait=false')
  }
})

test('Secret values: show, copy and change a key; the value leaves only in RevealValue', async ({ page }) => {
  const MARKER = 'MARKER-e2e-4a7d-value'
  const b64 = Buffer.from(MARKER).toString('base64')
  // Every answer but RevealValue's, and every event, is checked for the value.
  const bodies: string[] = []
  const reads: Promise<void>[] = []
  page.on('response', (r) => {
    const u = new URL(r.url())
    if (!u.pathname.startsWith('/api/') || u.pathname === '/api/events' || u.pathname === '/api/RevealValue') return
    reads.push(r.text().then((t) => void bodies.push(`${u.pathname} ${t}`), () => {}))
  })
  await page.addInitScript(() => {
    const w = window as unknown as { __sse: string[] }
    w.__sse = []
    const ES = window.EventSource
    window.EventSource = class extends ES {
      constructor(url: string | URL, init?: EventSourceInit) {
        super(url, init)
        this.addEventListener('message', (m) => w.__sse.push(String((m as MessageEvent).data)))
      }
    } as typeof EventSource
  })
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'])
  kubectl('-n', 'ocular-demo', 'create', 'secret', 'generic', 'e2e-values', `--from-literal=pin=${MARKER}`, '--from-literal=user=admin')
  try {
    await openTarget(page, 'kind-ocular-dev')
    const grid = await kindPage(page, 'Secrets')
    await row(grid, 'e2e-values').click()
    const drawer = page.getByRole('dialog', { name: 'secrets e2e-values' })
    const values = drawer.getByRole('region', { name: 'Values' })
    const pin = values.locator('[data-value-key="pin"]')
    await expect(pin).toContainText(`${MARKER.length} B`)
    await expect(drawer).not.toContainText(MARKER)

    await pin.getByRole('button', { name: 'Show' }).click()
    await expect(pin.locator('[data-value]')).toHaveText(MARKER)
    await pin.getByRole('button', { name: 'Hide' }).click()
    await expect(drawer).not.toContainText(MARKER)

    await pin.getByRole('button', { name: 'Copy' }).click()
    await expect(page.getByText('The value of key pin is copied')).toBeVisible()
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(MARKER)
    await expect(drawer).not.toContainText(MARKER)

    await pin.getByRole('button', { name: 'Change' }).click()
    const dialog = page.getByRole('dialog', { name: /^Change value/ })
    await expect(dialog.getByRole('button', { name: /Review/ })).toBeDisabled() // not loaded yet
    await dialog.getByRole('button', { name: 'Load current' }).click()
    await expect(dialog.getByLabel('Value')).toHaveValue(MARKER)
    await dialog.getByLabel('Value').fill('e2e-new')
    await page.keyboard.press('ControlOrMeta+Enter')
    const review = page.getByRole('dialog', { name: /^Change value/ })
    await expect(review).toContainText('checked by the server without writing')
    await expect(review).toContainText(`${MARKER.length} → 7 B`)
    await expect(review.getByLabel('where')).toContainText('Namespaceocular-demo')
    expect(kubectl('-n', 'ocular-demo', 'get', 'secret', 'e2e-values', '-o', 'jsonpath={.data.pin}')).toBe(b64) // reviewed only
    await review.getByRole('button', { name: 'Apply' }).click()
    await expect(page.getByText('Key pin is written')).toBeVisible()
    await expect(review).toBeHidden()
    expect(Buffer.from(kubectl('-n', 'ocular-demo', 'get', 'secret', 'e2e-values', '-o', 'jsonpath={.data.pin}'), 'base64').toString()).toBe('e2e-new')
    await expect(pin).toContainText('7 B')
  } finally {
    kubectl('-n', 'ocular-demo', 'delete', 'secret', 'e2e-values', '--ignore-not-found', '--wait=false')
  }
  await Promise.all(reads)
  const sse = await page.evaluate(() => (window as unknown as { __sse: string[] }).__sse)
  expect(bodies.length).toBeGreaterThan(5)
  for (const s of [...bodies, ...sse]) {
    expect(s).not.toContain(MARKER)
    expect(s).not.toContain(b64)
  }
})

// Node actions (P11) on the drain worker only: its taint keeps everything
// but drain fixtures (and DaemonSets) off it; every test opens it again.
const worker = 'ocular-dev-worker'

function drainFixtures(ns: string) {
  const pod = (app: string) => `metadata: {labels: {app: ${app}, ocular.test/drain: fixture}}
    spec:
      nodeSelector: {ocular.dev/drain: only}
      tolerations: [{key: ocular.dev/drain, operator: Equal, value: only, effect: NoSchedule}]
      terminationGracePeriodSeconds: 1
      containers: [{name: app, image: "registry.k8s.io/pause:3.10", imagePullPolicy: IfNotPresent}]`
  const deploy = (name: string) => `apiVersion: apps/v1
kind: Deployment
metadata: {name: ${name}, namespace: ${ns}}
spec:
  replicas: 1
  selector: {matchLabels: {app: ${name}}}
  template:
    ${pod(name)}
---
`
  kubectl('create', 'namespace', ns)
  execFileSync('kubectl', ['--kubeconfig', kc, '--context', 'kind-ocular-dev', 'apply', '-f', '-'], {
    encoding: 'utf8',
    input:
      deploy('movers') +
      deploy('guarded') +
      `apiVersion: policy/v1
kind: PodDisruptionBudget
metadata: {name: guarded, namespace: ${ns}}
spec: {minAvailable: 1, selector: {matchLabels: {app: guarded}}}
---
apiVersion: v1
kind: Pod
metadata: {name: bare, namespace: ${ns}, labels: {app: bare, ocular.test/drain: fixture}}
spec:
  nodeSelector: {ocular.dev/drain: only}
  tolerations: [{key: ocular.dev/drain, operator: Equal, value: only, effect: NoSchedule}]
  terminationGracePeriodSeconds: 1
  containers: [{name: app, image: "registry.k8s.io/pause:3.10", imagePullPolicy: IfNotPresent}]
`,
  })
  for (const d of ['movers', 'guarded']) kubectl('-n', ns, 'rollout', 'status', `deployment/${d}`, '--timeout=120s')
  kubectl('-n', ns, 'wait', '--for=condition=Ready', 'pod/bare', '--timeout=120s')
  kubectl('-n', ns, 'wait', '--for=jsonpath={.status.expectedPods}=1', 'pdb/guarded', '--timeout=60s')
  // Nothing but this test's fixtures and DaemonSets on the worker.
  const pods = kubectl('get', 'pods', '-A', '--field-selector', `spec.nodeName=${worker}`, '-o', 'jsonpath={range .items[*]}{.metadata.namespace}|{.metadata.ownerReferences[0].kind}{"\\n"}{end}')
  for (const l of pods.trim().split('\n')) {
    const [podNS, owner] = l.split('|')
    if (owner !== 'DaemonSet' && podNS !== ns) throw new Error(`a pod on ${worker} that is not this test's: ${l}`)
  }
  return () => {
    kubectl('uncordon', worker)
    kubectl('delete', 'namespace', ns, '--wait=false')
  }
}

test('cordon and uncordon a node from its row', async ({ page }) => {
  try {
    await openTarget(page, 'kind-ocular-dev')
    const grid = await kindPage(page, 'Nodes', '')
    await row(grid, worker).click({ button: 'right' })
    const menu = page.getByRole('menu', { name: 'Row actions' })
    await expect(menu.getByRole('menuitem')).toHaveText(['Details', 'Cordon', 'Uncordon', 'Drain'])
    await menu.getByRole('menuitem', { name: 'Cordon', exact: true }).click()
    const dialog = page.getByRole('dialog', { name: `Cordon ${worker}` })
    await expect(dialog).toContainText(`No new pods are scheduled on node ${worker}; the pods running there stay.`)
    await expect(dialog).toContainText('Permission: checked: allowed')
    await dialog.getByRole('button', { name: 'Cordon' }).click()
    await expect(page.getByRole('status')).toHaveText(`node ${worker}: cordon requested`)
    await expect(row(grid, worker)).toContainText('SchedulingDisabled')

    await row(grid, worker).click({ button: 'right' })
    await page.getByRole('menu', { name: 'Row actions' }).getByRole('menuitem', { name: 'Cordon', exact: true }).click()
    const again = page.getByRole('dialog', { name: `Cordon ${worker}` })
    await expect(again.getByRole('alert')).toContainText('is already cordoned')
    await again.getByRole('button', { name: 'Cancel' }).click()

    await row(grid, worker).click({ button: 'right' })
    await page.getByRole('menu', { name: 'Row actions' }).getByRole('menuitem', { name: 'Uncordon' }).click()
    await page.getByRole('dialog', { name: `Uncordon ${worker}` }).getByRole('button', { name: 'Uncordon' }).click()
    await expect(row(grid, worker)).not.toContainText('SchedulingDisabled')
  } finally {
    kubectl('uncordon', worker)
  }
})

test('drain a node: its pods listed, evictions requested, a PodDisruptionBudget refusal and a pod left are said', async ({ page }) => {
  const ns = `ocular-drain-${Date.now() % 1_000_000}`
  const cleanup = drainFixtures(ns)
  try {
    await openTarget(page, 'kind-ocular-dev')
    const grid = await kindPage(page, 'Nodes', '')
    await row(grid, worker).click()
    const drawer = page.getByRole('dialog', { name: `nodes ${worker}` })
    await drawer.getByRole('button', { name: /Actions/ }).click()
    await page.getByRole('menu', { name: 'Actions' }).getByRole('menuitem', { name: 'Drain' }).click()
    const dialog = page.getByRole('dialog', { name: `Drain ${worker}` })
    const evicted = dialog.getByRole('region', { name: 'Evicted (2)' })
    await expect(evicted.getByRole('listitem')).toHaveText([new RegExp(`^${ns}/guarded-\\S+ · ReplicaSet guarded-`), new RegExp(`^${ns}/movers-\\S+ · ReplicaSet movers-`)])
    await expect(dialog.getByRole('region', { name: 'Stay: no controller (1)' })).toContainText(`${ns}/bare`)
    await expect(dialog.getByRole('region', { name: /^Left alone \(\d+\)$/ })).toBeVisible()
    await expect(dialog).toContainText(`PodDisruptionBudget ${ns}/guarded allows no disruption now`)
    await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeFocused() // destructive
    await dialog.getByRole('button', { name: 'Drain' }).click()

    await expect(dialog.getByRole('alert')).toContainText('Not everything was done: a part was refused')
    const result = dialog.getByRole('list', { name: 'Result' })
    await expect(result.getByRole('listitem').filter({ hasText: worker })).toHaveText(`${worker}Done`)
    await expect(result.getByRole('listitem').filter({ hasText: `${ns}/movers-` })).toHaveText(/Done$/)
    await expect(result.getByRole('listitem').filter({ hasText: `${ns}/guarded-` })).toContainText('Refused · a PodDisruptionBudget does not allow it now')
    await expect(result.getByRole('listitem').filter({ hasText: `${ns}/bare` })).toHaveText(`${ns}/bareNot run · left: no controller would recreate it`)
    expect(kubectl('get', 'node', worker, '-o', 'jsonpath={.spec.unschedulable}')).toBe('true')
    await expect(row(grid, worker)).toContainText('SchedulingDisabled')
  } finally {
    cleanup()
  }
})

test('a CronJob: suspend and resume from its row, run now names the Job it makes', async ({ page }) => {
  const ns = `ocular-cron-${Date.now() % 1_000_000}`
  kubectl('create', 'namespace', ns)
  execFileSync('kubectl', ['--kubeconfig', kc, '--context', 'kind-ocular-dev', '-n', ns, 'apply', '-f', '-'], {
    encoding: 'utf8',
    input: `apiVersion: batch/v1
kind: CronJob
metadata: {name: yearly}
spec:
  schedule: "0 0 1 1 *"
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: Never
          terminationGracePeriodSeconds: 1
          containers: [{name: app, image: "busybox:1.36", command: ["true"]}]
`,
  })
  try {
    await openTarget(page, 'kind-ocular-dev')
    const grid = await kindPage(page, 'CronJobs', ns)
    await expect(row(grid, 'yearly')).toContainText('False')
    const menuOf = async () => {
      await row(grid, 'yearly').click({ button: 'right' })
      return page.getByRole('menu', { name: 'Row actions' })
    }
    const menu = await menuOf()
    await expect(menu.getByRole('menuitem')).toHaveText(['Details', 'Suspend', 'Resume', 'Run now', 'Delete'])
    await menu.getByRole('menuitem', { name: 'Suspend' }).click()
    const suspend = page.getByRole('dialog', { name: 'Suspend yearly' })
    await expect(suspend).toContainText('No new runs of CronJob yearly start on schedule; Jobs already running go on.')
    await expect(suspend).toContainText('Permission: checked: allowed')
    await suspend.getByRole('button', { name: 'Suspend' }).click()
    await expect(page.getByRole('status')).toHaveText('cronjob yearly: suspend requested')
    await expect(row(grid, 'yearly')).toContainText('True')

    await (await menuOf()).getByRole('menuitem', { name: 'Run now' }).click()
    const run = page.getByRole('dialog', { name: 'Run now yearly' })
    await expect(run).toContainText('The CronJob is suspended: this run starts anyway.')
    const job = /Job (yearly-manual-[a-z0-9]{5}) is created/.exec(await run.innerText())?.[1]
    expect(job).toBeTruthy()
    await run.getByRole('button', { name: 'Run now' }).click()
    await expect(page.getByRole('status')).toHaveText(`Job ${job} created from cronjob yearly`)
    const jobs = await kindPage(page, 'Jobs', ns)
    await expect(row(jobs, job!)).toBeVisible()

    const crons = await kindPage(page, 'CronJobs', ns)
    await row(crons, 'yearly').click({ button: 'right' })
    await page.getByRole('menu', { name: 'Row actions' }).getByRole('menuitem', { name: 'Resume' }).click()
    await page.getByRole('dialog', { name: 'Resume yearly' }).getByRole('button', { name: 'Resume' }).click()
    await expect(row(crons, 'yearly')).toContainText('False')
  } finally {
    kubectl('delete', 'namespace', ns, '--wait=false')
  }
})
