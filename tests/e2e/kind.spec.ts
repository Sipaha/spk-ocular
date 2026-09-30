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

test('a user without the right sees it in the review', async ({ page }) => {
  await openTarget(page, 'ocular-viewer')
  const grid = page.getByRole('grid', { name: 'resources' })
  const pod = row(grid, /^web-/).first()
  const name = (await pod.getByRole('gridcell').first().textContent())!.trim()
  await pod.click({ button: 'right' })
  await page.getByRole('menu', { name: 'Row actions' }).getByRole('menuitem', { name: 'Delete' }).click()
  const dialog = page.getByRole('dialog', { name: `Delete ${name}` })
  await expect(dialog).toContainText('Permission: not allowed: you may not delete pods in ocular-demo')
  await expect(dialog.getByRole('button', { name: 'Delete' })).toBeDisabled()
  await dialog.getByRole('button', { name: 'Cancel' }).click()
  expect(kubectl('-n', 'ocular-demo', 'get', 'pod', name, '-o', 'jsonpath={.metadata.name}')).toBe(name)
})

// ---- P8: every listable resource (discovery + server-side Tables)

/** The navigation's discovered kind (its API group subgroup expanded). */
async function apiKind(page: Page, sub: string, kind: string, ns?: string) {
  const nav = page.getByRole('navigation', { name: 'resources' })
  const group = nav.getByRole('group', { name: sub })
  const head = group.getByRole('button', { name: new RegExp(`^${sub.replace(/\./g, '\\.')}`) })
  if ((await head.getAttribute('aria-expanded')) === 'false') await head.click()
  await group.getByRole('button', { name: kind, exact: true }).click()
  await expect(page.getByRole('heading', { name: kind })).toBeVisible()
  if (ns) await page.getByRole('combobox', { name: 'Namespace' }).selectOption(ns)
  return page.getByRole('grid', { name: 'resources' })
}

test('custom resources: API groups in the navigation, the CRD columns, live changes', async ({ page }) => {
  await openTarget(page, 'kind-ocular-dev')
  const section = page.getByRole('navigation', { name: 'resources' }).getByRole('region', { name: 'API groups' })
  await expect(section.getByRole('button', { name: /^ocular\.dev/ })).toHaveAttribute('aria-expanded', 'false')
  const grid = await apiKind(page, 'ocular.dev', 'Widgets', 'ocular-crd')
  // kubectl get's columns (Detail is wide: a fact of the details)
  await expect(grid.getByRole('columnheader')).toHaveText([/Name/, /Size/, /Phase/, /Ready/, /Since/])
  await expect(row(grid, 'alpha')).toContainText('Running')
  await expect(row(grid, 'beta')).toContainText('Failing')
  // health from the conditions: a dot by the name, the reason on hover
  await expect(row(grid, 'beta').locator('[data-health]')).toHaveAttribute('data-health', 'error')
  await expect(row(grid, 'beta')).toHaveAttribute('title', 'Broken: the widget is broken')
  await expect(row(grid, 'alpha').locator('[data-health]')).toHaveAttribute('data-health', 'ok')
  await expect(row(grid, 'alpha').getByRole('gridcell').last()).toHaveText(/^\d+(s|m|h|d)/)
  kubectl('-n', 'ocular-crd', 'patch', 'widget', 'gamma', '--type=merge', '-p', '{"spec":{"size":11}}')
  try {
    await expect(row(grid, 'gamma').getByRole('gridcell').nth(1)).toHaveText('11')
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

test('built-ins without a described view: Jobs with the server columns', async ({ page }) => {
  await openTarget(page, 'kind-ocular-dev')
  const grid = await apiKind(page, 'batch', 'Jobs', 'ocular-crd')
  await expect(grid.getByRole('columnheader')).toHaveText([/Name/, /Status/, /Completions/, /Duration/, /Age/])
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
    await page.getByRole('menu', { name: 'Row actions' }).getByRole('menuitem', { name: 'Delete' }).click()
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
