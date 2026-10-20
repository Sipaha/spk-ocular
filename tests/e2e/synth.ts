import { expect, type Page } from '@playwright/test'
import { connectSelected,selectConnection } from './fixtures'

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
  await selectConnection(page,/^demo\b/)
  await connectSelected(page)
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


/** Returning closes the page during Enter's keydown, sometimes before its CDP
 * acknowledgement. Require that exact close; all other input errors still fail. */
export async function returnWithWindowShortcut(popup: Page) {
  const closed=popup.waitForEvent('close')
  await popup.keyboard.down('Alt');await popup.keyboard.down('Shift')
  const inputError=await popup.keyboard.down('Enter').then(()=>null,(error:unknown)=>error)
  await closed
  expect(popup.isClosed()).toBe(true)
  if(inputError&&!(inputError instanceof Error&&inputError.message==='keyboard.down: Target page, context or browser has been closed'))throw inputError
}
