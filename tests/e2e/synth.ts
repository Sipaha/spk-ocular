import { expect, type Page } from '@playwright/test'

// Helpers of the specs against the synthetic provider (playwright.synth.config.ts).

export async function token(page: Page) {
  return (await page.locator('meta[name="spk-ocular-api-token"]').getAttribute('content'))!
}

export type Stats = Record<string, number>

export async function stats(page: Page): Promise<Stats> {
  const res = await page.request.get('/api/_test/stats', { headers: { Authorization: `Bearer ${await token(page)}` } })
  return (await res.json()) as Stats
}

/** Opens the synthetic target's Services and selects object's row. */
export async function selectObject(page: Page, object: string) {
  await page.goto('/')
  await page.getByRole('option', { name: /^demo\b/ }).click()
  await page.getByRole('navigation', { name: 'resources' }).getByRole('button', { name: 'Services', exact: true }).click()
  const grid = page.getByRole('grid', { name: 'resources' })
  await grid.getByRole('gridcell', { name: object, exact: true }).click()
  return grid
}

export const activePanel = (page: Page) => page.locator('[role=tabpanel]:not([hidden])')

/** The visible text of the active terminal (xterm's DOM renderer rows). */
export const screen = (page: Page) => activePanel(page).locator('.xterm-rows')

export async function expectScreen(page: Page, text: string | RegExp, timeout?: number) {
  await expect(screen(page)).toContainText(text, { timeout })
}

/**
 * Counters relative to a baseline: the server outlives single tests (a
 * page that goes away leaves its terminals' reconnect handles — at most 64 —
 * until they are forgotten).
 */
export async function since(page: Page, base: Stats): Promise<Stats> {
  const now = await stats(page)
  return Object.fromEntries(Object.entries(now).map(([k, v]) => [k, v - (base[k] ?? 0)]))
}

/** Clicks into the active terminal (focus). */
export const focusTerminal = (page: Page) => activePanel(page).locator('[data-terminal-host]').click()

/** Changes the synthetic target's configuration (like an edited kubeconfig). */
export async function reconfigure(page: Page) {
  const res = await page.request.post('/api/_test/synthetic/reconfigure', { headers: { Authorization: `Bearer ${await token(page)}` } })
  expect(res.ok()).toBeTruthy()
}
