import { expect, test, type Page } from '@playwright/test'
import { EXTRA, ONE, TWO, env, kubeconfig, writeAtomic } from './fixtures'

const e = env(process.env.E2E_ROOT!)
const option = (page: Page, name: string) => page.getByRole('option', { name: new RegExp(`^${name}\\b`) })
/** The kube contexts (the Docker Compose group has its own). */
const kube = (page: Page) => page.getByRole('region', { name: 'Kubernetes' }).getByRole('option')

test.afterEach(() => {
  writeAtomic(e.one, ONE)
  writeAtomic(e.two, TWO)
  writeAtomic(e.extra, EXTRA)
})

test('lists contexts from KUBECONFIG and ~/.kube, marking current-context', async ({ page }) => {
  await page.goto('/')
  const list = page.getByRole('listbox', { name: 'targets' })
  await expect(list.getByRole('region', { name: 'Kubernetes' }).getByRole('option')).toHaveCount(4)
  await expect(option(page, 'prod')).toContainText('current')
  await expect(option(page, 'dev')).not.toContainText('current') // second file's current-context loses
  await expect(option(page, 'lab')).toBeVisible() // extra file in ~/.kube
  await expect(page.getByText('Pick a context on the left')).toBeVisible()
  await expect(page.getByText('SECRET')).toHaveCount(0)
})

test('lists Docker contexts beside the kube contexts, the current one marked, no TLS material or credentials', async ({ page }) => {
  await page.goto('/')
  const docker = page.getByRole('listbox', { name: 'targets' }).getByRole('region', { name: 'Docker Compose' })
  await expect(docker.getByRole('option')).toHaveCount(3)
  await expect(docker.getByRole('option', { name: /^default\b/ })).toContainText('unix:///var/run/docker.sock')
  await expect(docker.getByRole('option', { name: /^sock\b/ })).toContainText('current')
  await expect(docker.getByRole('option', { name: /^docker-edge\b/ })).toContainText('tcp://edge.example:2376')
  await docker.getByRole('option', { name: /^docker-edge\b/ }).click()
  await page.getByRole('button', { name: 'Overview' }).click()
  await expect(page.getByRole('heading', { name: 'docker-edge', exact: true })).toBeVisible()
  await expect(page.getByText('TLS, verified')).toBeVisible()
  const html = await page.content()
  expect(html).not.toContain('SECRET')
  expect(html).not.toContain('U0VDUkVULWF1dGg=')
})

const overview = async (page: Page, name: string) => {
  await page.getByRole('button', { name: 'Overview' }).click()
  await expect(page.getByRole('heading', { name, exact: true })).toBeVisible()
}

test('selection opens the resources, keeps details in Overview, survives a reload', async ({ page }) => {
  await page.goto('/')
  await option(page, 'staging').click()
  await expect(page.getByRole('heading', { name: 'Pods' })).toBeVisible()
  // Unreachable fixture cluster: an explained error, never an empty table.
  await expect(page.getByRole('alert').filter({ hasText: 'Cannot show' })).toBeVisible({ timeout: 15_000 })
  await overview(page, 'staging')
  await expect(page.getByText('https://staging.example:6443')).toBeVisible()
  await expect(page.getByText('ns-staging')).toBeVisible()
  await page.reload()
  // The target comes back with its last page (Overview) remembered.
  await expect(page.getByRole('heading', { name: 'staging', exact: true })).toBeVisible()
  await expect(option(page, 'staging')).toHaveAttribute('aria-selected', 'true')
})

test('keyboard: / filters, arrows and Enter select', async ({ page }) => {
  await page.goto('/')
  await expect(option(page, 'prod')).toBeVisible()
  await page.keyboard.press('/')
  await page.keyboard.type('lab')
  await expect(page.getByRole('option')).toHaveCount(1)
  await page.keyboard.press('Enter')
  await expect(page.getByRole('heading', { name: 'Pods' })).toBeVisible()
  await expect(option(page, 'lab')).toHaveAttribute('aria-selected', 'true')
  await page.keyboard.press('Escape')
  await expect(page.getByRole('option')).toHaveCount(7) // 4 kube contexts, 3 Docker contexts
})

test('kubeconfig edits show up live, without a reload', async ({ page }) => {
  await page.goto('/')
  await expect(kube(page)).toHaveCount(4)
  writeAtomic(e.one, kubeconfig('prod', 'prod', 'staging', 'qa'))
  await expect(option(page, 'qa')).toBeVisible()
  await expect(kube(page)).toHaveCount(5)
})

test('a broken kubeconfig is a warning; other contexts stay', async ({ page }) => {
  await page.goto('/')
  await expect(kube(page)).toHaveCount(4)
  writeAtomic(e.two, 'apiVersion: v1\nkind: Config\ncontexts: [ {{{\n')
  const warning = page.getByRole('alert').filter({ hasText: 'Could not read' })
  await expect(warning).toContainText('Could not read 1 file(s)')
  await expect(warning).toContainText('two.yaml')
  await expect(kube(page)).toHaveCount(3) // prod, staging, lab
  writeAtomic(e.two, TWO)
  await expect(warning).toHaveCount(0)
  await expect(kube(page)).toHaveCount(4)
})

test('a selected context that disappears is deselected, and comes back', async ({ page }) => {
  await page.goto('/')
  await option(page, 'dev').click()
  await expect(page.getByRole('heading', { name: 'Pods' })).toBeVisible()
  writeAtomic(e.two, kubeconfig('', 'other'))
  await expect(page.getByText('Pick a context on the left')).toBeVisible()
  writeAtomic(e.two, TWO)
  await expect(page.getByRole('heading', { name: 'Pods' })).toBeVisible()
  await expect(option(page, 'dev')).toHaveAttribute('aria-selected', 'true')
})

test('screenshot', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  await page.goto('/')
  await option(page, 'prod').click()
  await overview(page, 'prod')
  await page.screenshot({ path: `${process.env.E2E_ROOT}/../screenshot.png` })
})
