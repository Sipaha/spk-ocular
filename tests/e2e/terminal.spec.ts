import { expect, type Page } from '@playwright/test'
import { test } from './fixtures'
import { activePanel, expectScreen, focusTerminal, screen, selectObject, since, stats } from './synth'
import { pickOption, selects } from './fixtures'

// The echo terminal of the synthetic provider (internal/providers/synthetic/live.go):
// typed lines are answered "you said: …", "flood N", "exit N", "size CxR" on resize.

async function openShell(page: Page, object = 'api') {
  await selectObject(page, object)
  await page.keyboard.press('s')
  // "size CxR" lines arrive asynchronously, so no prompt is waited for
  await expectScreen(page, `synthetic terminal on ${object === 'api' ? 'api' : 'worker-1'}`)
}

async function run(page: Page, line: string) {
  await page.keyboard.type(line)
  await page.keyboard.press('Enter')
}

const sizes = async (page: Page) => [...((await screen(page).textContent()) ?? '').matchAll(/size (\d+)x(\d+)/g)].map((m) => ({ cols: Number(m[1]), rows: Number(m[2]) }))

test('typing, output and the size the terminal opened with', async ({ page }) => {
  await openShell(page)
  // xterm resolves the shared CSS tokens; production CSS must keep all
  // sixteen ANSI colours, including ones referenced only at runtime.
  const colors = await page.evaluate(() => {
    const css = getComputedStyle(document.documentElement)
    return Array.from({ length: 16 }, (_, i) => css.getPropertyValue(`--color-ansi-${i}`).trim())
  })
  expect(colors.filter(Boolean)).toHaveLength(16)
  const appBackground = await page.locator('body').evaluate((el) => getComputedStyle(el).backgroundColor)
  await expect(activePanel(page).locator('.xterm-scrollable-element')).toHaveCSS('background-color', appBackground)
  await run(page, 'hello there')
  await expectScreen(page, 'you said: hello there')
  const [first] = await sizes(page)
  expect(first.cols).toBeGreaterThan(40)
  expect(first.rows).toBeGreaterThan(3)
  await expect(page.getByRole('tab', { name: /main · api/ })).toBeVisible()
})

test('changing the dock height resizes the terminal', async ({ page }) => {
  await openShell(page)
  const before = (await sizes(page)).at(-1)!
  // the dock height is remembered: grow a short dock, shrink a tall one
  const dy = before.rows < 20 ? -150 : 150
  const sep = page.getByRole('separator', { name: 'Resize the bottom panel' })
  const box = (await sep.boundingBox())!
  await page.mouse.move(box.x + box.width / 2, box.y + 1)
  await page.mouse.down()
  await page.mouse.move(box.x + box.width / 2, box.y + 1 + dy, { steps: 5 })
  await page.mouse.up()
  await expect.poll(async () => Math.abs((await sizes(page)).at(-1)!.rows - before.rows)).toBeGreaterThan(5)
  expect((await sizes(page)).at(-1)!.cols).toBe(before.cols)
})

test('a flood stops at Ctrl+C and the terminal stays responsive', async ({ page }) => {
  await openShell(page)
  await run(page, 'flood 100000000')
  await expectScreen(page, /flood line \d+ of 100000000/)
  await page.keyboard.press('Control+c')
  await expectScreen(page, /\^C\s*\$/, 10_000)
  await run(page, 'still here')
  await expectScreen(page, 'you said: still here')
  expect(await screen(page).textContent()).not.toContain('flood done')
})

test('an exit code is shown; Reconnect starts the shell again', async ({ page }) => {
  await openShell(page)
  await run(page, 'exit 3')
  const alert = activePanel(page).getByRole('alert')
  await expect(alert).toContainText('process exited with code 3')
  await expect.poll(async () => (await stats(page)).terminals).toBe(0)
  await alert.getByRole('button', { name: 'Reconnect' }).click()
  await expectScreen(page, 'reconnected')
  await expect(alert).toHaveCount(0)
  // xterm exposes only visible rows; the old greeting can be in scrollback.
  // A greeting AFTER the reconnect marker proves the new shell has started.
  await expectScreen(page, /reconnected[\s\S]*synthetic terminal on api/)
  await run(page, 'again')
  await expectScreen(page, 'you said: again')
})

test('a custom command is not run again without asking', async ({ page }) => {
  await selectObject(page, 'workers')
  await page.keyboard.press('Shift+S')
  const dlg = page.getByRole('dialog', { name: 'Open a terminal' })
  await pickOption(selects(dlg).first(), /^worker-2/)
  await dlg.getByPlaceholder(/interactive shell/).fill('exit 5')
  await dlg.getByRole('button', { name: 'Open' }).click()
  const alert = activePanel(page).getByRole('alert')
  await expect(alert).toContainText('process exited with code 5')
  await alert.getByRole('button', { name: 'Reconnect' }).click()
  await expect(alert).toContainText('Run again:')
  await expect(alert.locator('code')).toHaveText('exit 5')
  await alert.getByRole('button', { name: 'Run' }).click()
  await expectScreen(page, 'reconnected')
  await expect(alert).toContainText('process exited with code 5')
  await expect(page.getByRole('tab', { name: /main · worker-2/ })).toBeVisible()
})

test('a terminal survives selecting another target and says whose it is', async ({ page }) => {
  await page.goto('/')
  const base = await stats(page)
  await openShell(page)
  await page.getByRole('option', { name: /^prod\b/ }).click()
  const tab = page.getByRole('tab', { name: /main · api/ })
  await expect(tab).toBeVisible()
  await expect(tab.getByText('demo', { exact: true })).toBeVisible() // the foreign-target badge
  await focusTerminal(page)
  await run(page, 'from prod')
  await expectScreen(page, 'you said: from prod')
  expect(await since(page, base)).toMatchObject({ terminals: 1, syn_execs: 1 })
})

test('closing the tab ends the command and frees the terminal', async ({ page }) => {
  await page.goto('/')
  // The last test's terminal hangs up when its page goes: count from after that.
  await expect.poll(async () => (await stats(page)).syn_execs).toBe(0)
  const base = await stats(page)
  await openShell(page)
  await expect.poll(async () => since(page, base)).toMatchObject({ terminals: 1, syn_execs: 1, syn_handles: 2 }) // the kept prototype + the running copy
  await page.getByRole('tab', { name: /main · api/ }).getByRole('button', { name: 'Close tab' }).click()
  await expect.poll(async () => since(page, base)).toMatchObject({ terminals: 0, syn_execs: 0, syn_handles: 0 })
})
