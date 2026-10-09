import { expect, type Page } from '@playwright/test'
import { connectSelected, scratchRoot, test } from './fixtures'
import { stats, token } from './synth'
import { languageNames, languages, type Language } from '../../web/src/languages'
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'

async function choose(page: Page, language: Language) {
  await page.locator('.language-menu button').click()
  await page.getByRole('option', { name: languageNames[language], exact: true }).click()
  await expect(page.locator('html')).toHaveAttribute('lang', language)
}

test.afterEach(async ({ page }) => {
  if (!page.url().startsWith('http')) return
  const response = await page.request.post('/api/SetLanguage', {
    headers: { Authorization: `Bearer ${await token(page)}`, Origin: new URL(page.url()).origin }, data: { language: '' },
  })
  expect(response.ok()).toBeTruthy()
})

test('all eight language menus, persistent choices and live workspace state', async ({ page }) => {
  const captures = join(scratchRoot, 'language-screenshots')
  mkdirSync(captures, { recursive: true })
  let openedViews = 0
  page.on('request', request => { if (new URL(request.url()).pathname === '/api/OpenView') openedViews++ })
  await page.goto('/')
  await page.getByRole('option', { name: /^demo\b/ }).click()
  await connectSelected(page)
  const grid = page.getByRole('grid', { name: 'resources' })
  await expect(grid).toBeVisible()
  await page.locator('[data-primary-filter]').fill('blue')
  const resourcePage = await page.getByRole('heading', { level: 1 }).textContent()
  for (const language of languages) {
    const baseline = await stats(page)
    const openedBefore = openedViews
    const identity = await grid.evaluate(el => { const marker = crypto.randomUUID(); (el as HTMLElement).dataset.languageIdentity = marker; return marker })
    await choose(page, language)
    await expect(grid).toBeVisible()
    await expect(page.locator('[data-primary-filter]')).toHaveValue('blue')
    await expect(page.getByRole('heading', { level: 1 })).toHaveText(resourcePage!)
    const current = await stats(page)
    expect(current.sessions).toBe(baseline.sessions)
    expect(openedViews).toBe(openedBefore)
    await expect(grid).toHaveAttribute('data-language-identity', identity)
    for (const width of [1280, 860]) {
      await page.setViewportSize({ width, height: 820 })
      const header = page.locator('.app-header')
      expect(await header.evaluate(el => el.scrollWidth <= el.clientWidth)).toBeTruthy()
      await expect(page.locator('.language-menu button')).toContainText(languageNames[language])
      expect(await page.locator('.language-menu button').evaluate(el => getComputedStyle(el).fontSize)).toBe('14px')
      await expect(page.locator('button.about-trigger')).toBeInViewport({ ratio: 1 })
      await page.locator('.language-menu button').click()
      const menuLabel = await page.locator('.language-menu button').getAttribute('aria-label')
      await expect(page.getByRole('listbox', { name: menuLabel!, exact: true }).getByRole('option')).toHaveCount(8)
      for (const name of Object.values(languageNames)) {
        const option = page.getByRole('option', { name, exact: true })
        await expect(option).toBeVisible()
        const bounds = await option.boundingBox()
        expect(bounds!.x).toBeGreaterThanOrEqual(0)
        expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width)
      }
      await page.screenshot({ path: join(captures, `${language}-${width}-menu.png`) })
      await page.keyboard.press('Escape')
      await page.screenshot({ path: join(captures, `${language}-${width}.png`) })
    }
    await expect.poll(async () => {
      const response = await page.request.post('/api/GetTargetState', {
        headers: { Authorization: `Bearer ${await token(page)}`, Origin: new URL(page.url()).origin },
        data: { provider: 'synthetic', target: 'demo' },
      })
      expect(response.ok()).toBeTruthy()
      const state = await response.json()
      return JSON.parse(state.pageMemo ?? '{}').page?.filter ?? ''
    }).toBe('blue')
    await page.reload()
    await expect(page.locator('html')).toHaveAttribute('lang', language)
    await expect(grid).toBeVisible()
  }
})

test('ordered browser languages and manual choice work with denied storage', async ({ browser, page }) => {
  await page.goto('/')
  const context = await browser.newContext({ locale: 'fr-FR' })
  await context.addInitScript(() => {
    Object.defineProperty(navigator, 'languages', { get: () => ['fa-IR', 'zh-Hant-TW', 'pt-BR', 'fr-FR'] })
    for (const name of ['localStorage', 'sessionStorage']) {
      Object.defineProperty(window, name, { get: () => { throw new DOMException('Storage denied', 'SecurityError') } })
    }
  })
  const denied = await context.newPage()
  try {
    await denied.goto(page.url())
    await expect(denied.locator('html')).toHaveAttribute('lang', 'pt')
    await choose(denied, 'ru')
    await denied.reload()
    await expect(denied.locator('html')).toHaveAttribute('lang', 'ru')
    await choose(denied, 'pt')
    await expect(denied.locator('html')).toHaveAttribute('lang', 'pt')
    await denied.reload()
    await expect(denied.locator('html')).toHaveAttribute('lang', 'pt')
  } finally {
    await context.close()
  }
})
