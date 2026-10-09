import { expect } from '@playwright/test'
import { test, connectSelected, scratchRoot } from './fixtures'
import { token } from './synth'
import { languages, languageNames } from '../../web/src/languages'
import { join } from 'node:path'

test.afterEach(async ({ page }) => {
  if (!page.url().startsWith('http')) return
  const reset = await page.request.post('/api/SetLanguage', { headers: { Authorization: `Bearer ${await token(page)}`, Origin: new URL(page.url()).origin }, data: { language: '' } })
  expect(reset.ok()).toBeTruthy()
})

test('About preserves the workspace, shows the running build and opens the author profile in another tab', async ({ page, context }) => {
  await page.goto('/')
  await page.getByRole('option', { name: /^demo\b/ }).click()
  await connectSelected(page)
  const grid = page.getByRole('grid', { name: 'resources' })
  await expect(grid).toBeVisible()
  const original = await grid.elementHandle()
  const info = await page.request.post('/api/AppInfo', { headers: { Authorization: `Bearer ${await token(page)}`, Origin: new URL(page.url()).origin }, data: {} })
  const build = await info.json()
  const trigger = page.getByRole('button', { name: 'About SPK Ocular' })
  await trigger.click()
  const dialog = page.getByRole('dialog', { name: 'About SPK Ocular' })
  await expect(dialog).toContainText(build.version)
  await expect(dialog).toContainText('Pavel Simonov')
  await expect(dialog).not.toContainText('Citeck')
  await expect(dialog.getByRole('link', { name: 'Apache License 2.0' })).toHaveAttribute('href', 'https://github.com/Sipaha/spk-ocular/blob/master/LICENSE')
  await expect(dialog.getByRole('button', { name: 'Close' })).toBeFocused()
  await page.keyboard.press('Shift+Tab')
  await expect(dialog.getByRole('link', { name: 'About the author' })).toBeFocused()
  await page.keyboard.press('Tab')
  await expect(dialog.getByRole('button', { name: 'Close' })).toBeFocused()
  await context.route('https://sipaha.github.io/**', route => route.fulfill({ contentType: 'text/html', body: '<title>Isolated author link fixture</title>' }))
  const opened = page.waitForEvent('popup')
  await dialog.getByRole('link', { name: 'About the author' }).click()
  const profile = await opened
  await profile.waitForURL('https://sipaha.github.io/about/en/')
  await profile.close()
  await page.keyboard.press('Escape')
  await expect(dialog).toHaveCount(0)
  await expect(trigger).toBeFocused()
  expect(await grid.evaluate((current, before) => current === before, original)).toBe(true)
})

test('About has complete native-language text and explicit author URLs at both window sizes', async ({ page }) => {
  await page.goto('/')
  const errors: string[] = []
  page.on('pageerror', e => errors.push(e.message))
  for (const language of languages) {
    await page.locator('.language-menu button').click()
    await page.getByRole('option', { name: languageNames[language], exact: true }).click()
    await expect(page.locator('html')).toHaveAttribute('lang', language)
    await page.locator('button.about-trigger').click()
    const dialog = page.getByRole('dialog', { name: /SPK Ocular/ })
    await expect(dialog).not.toContainText('Citeck')
    const profile = dialog.locator('a[href*="/about/"]')
    await expect(profile).toHaveAttribute('href', `https://sipaha.github.io/about/${language === 'ru' ? '?lang=ru' : language + '/'}`)
    for (const width of [860,1280]) {
      await page.setViewportSize({ width, height: 800 })
      expect(await page.locator('.app-header').evaluate(e => e.scrollWidth <= e.clientWidth)).toBe(true)
      expect(await dialog.evaluate(e => e.scrollWidth <= e.clientWidth)).toBe(true)
      await expect(profile).toBeInViewport()
      await expect(dialog.locator('img')).toHaveJSProperty('naturalWidth', 256)
      await page.screenshot({ path: join(scratchRoot, `about-${language}-${width}.png`) })
    }
    await page.keyboard.press('Escape')
    await expect(dialog).toHaveCount(0)
  }
  expect(errors).toEqual([])
})
