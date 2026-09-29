import { expect, test, type Page } from '@playwright/test'
import { createServer, type Server } from 'node:net'
import { selectObject, since, stats } from './synth'

// Tunnels to the synthetic provider's ports (in-process HTTP servers). The
// request goes through the runner's own HTTP client, not the page.

async function forwardDialog(page: Page, object: string, port: number) {
  await selectObject(page, object)
  await page.keyboard.press('Enter')
  const ports = page.getByRole('dialog', { name: `services ${object}` }).getByRole('region', { name: 'Ports' })
  await ports.getByRole('listitem').filter({ hasText: String(port) }).getByRole('button', { name: 'Forward' }).click()
  return page.getByRole('dialog', { name: 'Forward a port' })
}

function isFree(port: number) {
  return new Promise<boolean>((ok) => {
    const s = createServer()
    s.once('error', () => ok(false))
    s.listen(port, '127.0.0.1', () => s.close(() => ok(true)))
  })
}

const panel = (page: Page) => page.getByRole('region', { name: 'Port forwards' })

test('forward, request through the tunnel, stop', async ({ page }) => {
  await page.goto('/')
  const base = await stats(page)
  await selectObject(page, 'api')
  await page.keyboard.press('Enter')
  const ports = page.getByRole('dialog', { name: 'services api' }).getByRole('region', { name: 'Ports' })
  await expect(ports.getByRole('listitem').filter({ hasText: '53' })).toContainText('cannot be forwarded')
  await page.keyboard.press('Escape')

  const dlg = await forwardDialog(page, 'api', 80)
  await expect(dlg.getByRole('combobox')).toHaveValue('http') // the port says it speaks http
  await dlg.getByRole('button', { name: 'Forward' }).click()
  await expect(dlg).toHaveCount(0)

  const row = panel(page).getByRole('listitem', { name: 'services/api:80' })
  await expect(row).toContainText('connected')
  await expect(row.getByRole('button', { name: 'Open' })).toBeVisible()
  const addr = (await row.locator('[data-address]').textContent())!.split(/\s+/)[0]
  expect(addr).toMatch(/^127\.0\.0\.1:\d+$/)
  expect(Number(addr.split(':')[1])).not.toBe(80) // a privileged remote port: any free local one

  const res = await fetch(`http://${addr}/through`)
  expect(await res.text()).toBe('hello from api:80 /through\n')
  await expect(row).toContainText('1 total')
  await expect(page.getByRole('button', { name: '⇄ 1' })).toHaveAttribute('title', 'Port forwards')
  expect(await since(page, base)).toMatchObject({ forwards: 1, syn_upstreams: 1, syn_handles: 1 })

  await row.getByRole('button', { name: 'Stop' }).click()
  await expect(row).toHaveCount(0)
  await expect.poll(async () => since(page, base)).toMatchObject({ forwards: 0, syn_upstreams: 0, syn_handles: 0, syn_streams: 0 })
  await expect(fetch(`http://${addr}/`)).rejects.toThrow()
})

test('a local port that is taken is an error in the dialog', async ({ page }) => {
  const busy: Server = createServer()
  await new Promise<void>((ok) => busy.listen(0, '127.0.0.1', ok))
  const taken = (busy.address() as { port: number }).port
  const free9090 = await isFree(9090)
  await page.goto('/')
  const base = await stats(page)
  try {
    const dlg = await forwardDialog(page, 'workers', 9090)
    await dlg.getByLabel('Local port').fill(String(taken))
    await dlg.getByRole('button', { name: 'Forward' }).click()
    await expect(dlg.getByRole('alert')).toContainText(`local port ${taken} is in use`)
    expect(await since(page, base)).toMatchObject({ forwards: 0, syn_handles: 0 })
    // another port works from the same dialog
    await dlg.getByLabel('Local port').fill('')
    await dlg.getByRole('button', { name: 'Forward' }).click()
    const row = panel(page).getByRole('listitem', { name: 'services/workers:9090' })
    await expect(row).toContainText('connected')
    await expect(row.getByRole('button', { name: 'Open' })).toHaveCount(0) // not known to be a web port
    const addr = (await row.locator('[data-address]').textContent())!.split(/\s+/)[0]
    expect(addr).toBe(free9090 ? '127.0.0.1:9090' : expect.stringMatching(/^127\.0\.0\.1:\d+$/)) // the same if free
    expect(await (await fetch(`http://${addr}/m`)).text()).toBe('hello from workers:9090 /m\n')
    await row.getByRole('button', { name: 'Stop' }).click()
    await expect.poll(async () => since(page, base)).toMatchObject({ forwards: 0, syn_upstreams: 0, syn_handles: 0 })
  } finally {
    busy.close()
  }
})
