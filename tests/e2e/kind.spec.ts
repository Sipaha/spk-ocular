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
  await expect(row(grid, /^web-/)).toHaveCount(3)
  kubectl('-n', 'ocular-demo', 'scale', 'deploy/web', '--replicas=4')
  try {
    await expect(row(grid, /^web-/)).toHaveCount(4)
  } finally {
    kubectl('-n', 'ocular-demo', 'scale', 'deploy/web', '--replicas=3')
  }
  await expect(row(grid, /^web-/)).toHaveCount(3)
})

test('details: relations, events and yaml of a deployment', async ({ page }) => {
  await openTarget(page, 'kind-ocular-dev')
  const grid = await kindPage(page, 'Deployments')
  await row(grid, 'web').click()
  const drawer = page.getByRole('dialog', { name: 'apps/deployments web' })
  await expect(drawer.getByText('Owns')).toBeVisible()
  await expect(drawer.getByRole('button', { name: /^pods\/web-/ })).toHaveCount(3)
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
