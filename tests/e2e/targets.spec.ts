import { expect, test, type Page } from '@playwright/test'
import { EXTRA, ONE, TWO, env, kubeconfig, writeAtomic } from './fixtures'

const e = env(process.env.E2E_ROOT!)
const option = (page: Page, name: string) => page.getByRole('option', { name: new RegExp(`^${name}\\b`) })

test.afterEach(() => {
  writeAtomic(e.one, ONE)
  writeAtomic(e.two, TWO)
  writeAtomic(e.extra, EXTRA)
})

test('lists contexts from KUBECONFIG and ~/.kube, marking current-context', async ({ page }) => {
  await page.goto('/')
  const list = page.getByRole('listbox', { name: 'targets' })
  await expect(list.getByRole('option')).toHaveCount(4)
  await expect(option(page, 'prod')).toContainText('current')
  await expect(option(page, 'dev')).not.toContainText('current') // second file's current-context loses
  await expect(option(page, 'lab')).toBeVisible() // extra file in ~/.kube
  await expect(page.getByText('Pick a context on the left')).toBeVisible()
  await expect(page.getByText('SECRET')).toHaveCount(0)
})

test('selection shows details and survives a reload', async ({ page }) => {
  await page.goto('/')
  await option(page, 'staging').click()
  await expect(page.getByRole('heading', { name: 'staging' })).toBeVisible()
  await expect(page.getByText('https://staging.example:6443')).toBeVisible()
  await expect(page.getByText('ns-staging')).toBeVisible()
  await page.reload()
  await expect(page.getByRole('heading', { name: 'staging' })).toBeVisible()
  await expect(option(page, 'staging')).toHaveAttribute('aria-selected', 'true')
})

test('keyboard: / filters, arrows and Enter select', async ({ page }) => {
  await page.goto('/')
  await expect(option(page, 'prod')).toBeVisible()
  await page.keyboard.press('/')
  await page.keyboard.type('la')
  await expect(page.getByRole('option')).toHaveCount(1)
  await page.keyboard.press('Enter')
  await expect(page.getByRole('heading', { name: 'lab' })).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(page.getByRole('option')).toHaveCount(4)
})

test('kubeconfig edits show up live, without a reload', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByRole('option')).toHaveCount(4)
  writeAtomic(e.one, kubeconfig('prod', 'prod', 'staging', 'qa'))
  await expect(option(page, 'qa')).toBeVisible()
  await expect(page.getByRole('option')).toHaveCount(5)
})

test('a broken kubeconfig is a warning; other contexts stay', async ({ page }) => {
  await page.goto('/')
  await expect(page.getByRole('option')).toHaveCount(4)
  writeAtomic(e.two, 'apiVersion: v1\nkind: Config\ncontexts: [ {{{\n')
  await expect(page.getByRole('alert')).toContainText('Could not read 1 file(s)')
  await expect(page.getByRole('alert')).toContainText('two.yaml')
  await expect(page.getByRole('option')).toHaveCount(3) // prod, staging, lab
  writeAtomic(e.two, TWO)
  await expect(page.getByRole('alert')).toHaveCount(0)
  await expect(page.getByRole('option')).toHaveCount(4)
})

test('a selected context that disappears is deselected, and comes back', async ({ page }) => {
  await page.goto('/')
  await option(page, 'dev').click()
  await expect(page.getByRole('heading', { name: 'dev' })).toBeVisible()
  writeAtomic(e.two, kubeconfig('', 'other'))
  await expect(page.getByText('Pick a context on the left')).toBeVisible()
  writeAtomic(e.two, TWO)
  await expect(page.getByRole('heading', { name: 'dev' })).toBeVisible()
})

test('screenshot', async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 800 })
  await page.goto('/')
  await option(page, 'prod').click()
  await expect(page.getByRole('heading', { name: 'prod' })).toBeVisible()
  await page.screenshot({ path: `${process.env.E2E_ROOT}/../screenshot.png` })
})
