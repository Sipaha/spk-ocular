import {openConnections,connectionChoice} from './fixtures'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { expect, type Page } from '@playwright/test'
import { test } from './fixtures'
import { EXTRA, ONE, TWO, env, kubeconfig, writeAtomic } from './fixtures'

const e = env(process.env.E2E_ROOT!)
const option = async (page: Page, name: string) => connectionChoice(page,new RegExp(`^${name}\\b`))
/** The kube contexts (the Docker group has its own). */
const kube = (page: Page) => page.getByRole('rowgroup', { name: 'Kubernetes' }).locator('.connection-choice')

test.afterEach(() => {
  writeAtomic(e.one, ONE)
  writeAtomic(e.two, TWO)
  writeAtomic(e.extra, EXTRA)
})

test('lists explicitly imported contexts without current-context badges', async ({ page }) => {
  await page.goto('/')
  const list = (await openConnections(page))
  await expect(list.getByRole('rowgroup', { name: 'Kubernetes' }).locator('.connection-choice')).toHaveCount(4)
  await expect((await option(page, 'prod'))).not.toContainText('current')
  await expect((await option(page, 'dev'))).not.toContainText('current') // second file's current-context loses
  await expect((await option(page, 'lab'))).toBeVisible() // extra file in ~/.kube
  await expect(page.locator('[data-connection-switcher]')).toContainText('Choose connection')
  await expect(page.getByText('SECRET')).toHaveCount(0)
})

test('lists Docker contexts beside the kube contexts, without current badges, no TLS material or credentials', async ({ page }) => {
  await page.goto('/')
  const docker = (await openConnections(page)).getByRole('rowgroup', { name: 'Docker' })
  await expect(docker.locator('.connection-choice')).toHaveCount(3)
  await expect(docker.getByRole('button', { name: /^default\b/ })).toContainText('unix:///var/run/docker.sock')
  await expect(docker.getByRole('button', { name: /^sock\b/ })).not.toContainText('current')
  await expect(docker.getByRole('button', { name: /^docker-edge\b/ })).toContainText('tcp://edge.example:2376')
  await docker.getByRole('button', { name: /^docker-edge\b/ }).click()
  await page.getByRole('button', { name: 'Connection information' }).click()
  await expect(page.getByRole('dialog', { name: 'Connection information' }).getByRole('heading', { name: 'docker-edge', exact: true })).toBeVisible()
  await expect(page.getByText('TLS, verified')).toBeVisible()
  const html = await page.content()
  expect(html).not.toContain('SECRET')
  expect(html).not.toContain('U0VDUkVULWF1dGg=')
})

const connectionInfo = async (page: Page, name: string) => {
  await page.getByRole('button', { name: 'Connection information' }).click()
  const dialog = page.getByRole('dialog', { name: 'Connection information' })
  await expect(dialog.getByRole('heading', { name, exact: true })).toBeVisible()
  return dialog
}

test('connection information is modal before connecting and restores focus', async ({ page }) => {
  await page.goto('/')
  await (await option(page, 'staging')).click()
  await expect(page.getByRole('button', { name: 'Connect', exact: true })).toBeVisible()
  // Selection and information must not contact the unreachable fixture cluster.
  await expect(page.getByRole('grid', { name: 'resources' })).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Overview', exact: true })).toHaveCount(0)
  const dialog = await connectionInfo(page, 'staging')
  await expect(dialog.getByText('https://staging.example:6443', { exact: true })).toBeVisible()
  await expect(dialog.getByText('ns-staging', { exact: true })).toBeVisible()
  await expect(dialog.getByRole('button', { name: 'Close', exact: true })).toBeFocused()
  await page.keyboard.press('Tab')
  await page.keyboard.press('Shift+Tab')
  await expect(dialog.getByRole('button', { name: 'Close', exact: true })).toBeFocused()
  await page.keyboard.press('Escape')
  await expect(dialog).toHaveCount(0)
  await expect(page.getByRole('button', { name: 'Connection information' })).toBeFocused()
  await expect(page.getByRole('button', { name: 'Connect', exact: true })).toBeVisible()
  await page.reload()
  await expect(page.getByRole('button', { name: 'Connect', exact: true })).toBeVisible()
  await expect(dialog).toHaveCount(0)
  await expect((await option(page, 'staging'))).toHaveAttribute('aria-pressed', 'true')
})

test('keyboard: target filter and Enter select without connecting', async ({ page }) => {
  await page.goto('/')
  await expect((await option(page, 'prod'))).toBeVisible()
  await (await option(page, 'prod')).click()
  await expect(page.getByRole('button', { name: 'Connect', exact: true })).toBeVisible()
  await expect(page.getByRole('textbox', { name: 'Filter rows' })).toHaveCount(0)
  await openConnections(page)
  await page.getByRole('combobox', { name: 'Search connections…', exact: true }).focus()
  await page.keyboard.type('lab')
  await expect(page.locator('.connection-choice')).toHaveCount(1)
  await page.keyboard.press('Enter')
  await expect(page.getByRole('button', { name: 'Connect', exact: true })).toBeVisible()
  await expect((await option(page, 'lab'))).toHaveAttribute('aria-pressed', 'true')
  await page.keyboard.press('Escape')
  await openConnections(page)
  await expect(page.locator('.connection-choice')).toHaveCount(7) // 4 kube contexts, 3 Docker contexts
})

test('kubeconfig edits show up live, without a reload', async ({ page }) => {
  await page.goto('/')
  await openConnections(page)
  await expect(kube(page)).toHaveCount(4)
  writeAtomic(e.one, kubeconfig('prod', 'prod', 'staging', 'qa'))
  await expect((await option(page, 'qa'))).toBeVisible()
  await openConnections(page)
  await expect(kube(page)).toHaveCount(5)
})

test('a broken kubeconfig is a warning; other contexts stay', async ({ page }) => {
  await page.goto('/')
  await openConnections(page)
  await expect(kube(page)).toHaveCount(4)
  writeAtomic(e.two, 'apiVersion: v1\nkind: Config\ncontexts: [ {{{\n')
  const warning = page.getByRole('alert').filter({ hasText: 'Could not read' })
  await expect(warning).toContainText('Could not read 1 file(s)')
  await expect(warning).toContainText('two.yaml')
  await openConnections(page)
  await expect(kube(page)).toHaveCount(3) // prod, staging, lab
  writeAtomic(e.two, TWO)
  await expect(warning).toHaveCount(0)
  await openConnections(page)
  await expect(kube(page)).toHaveCount(4)
})

test('a selected context that disappears is deselected, and comes back', async ({ page }) => {
  await page.goto('/')
  await (await option(page, 'dev')).click()
  await expect(page.getByRole('button', { name: 'Connect', exact: true })).toBeVisible()
  writeAtomic(e.two, kubeconfig('', 'other'))
  await expect(page.locator('[data-connection-switcher]')).toContainText('Choose connection')
  writeAtomic(e.two, TWO)
  await expect(page.getByRole('button', { name: 'Connect', exact: true })).toBeVisible()
  await expect((await option(page, 'dev'))).toHaveAttribute('aria-pressed', 'true')
})

test('screenshot', async ({ page }) => {
  const icon = await page.request.get('/icon.svg')
  expect(icon.ok()).toBeTruthy()
  expect(await icon.body()).toEqual(readFileSync(join(import.meta.dirname, '../../internal/appfiles/icons/icon.svg')))
  await page.setViewportSize({ width: 1280, height: 800 })
  await page.goto('/')
  await (await option(page, 'prod')).click()
  await connectionInfo(page, 'prod')
  await page.screenshot({ path: `${process.env.E2E_ROOT}/../screenshot.png` })
})
