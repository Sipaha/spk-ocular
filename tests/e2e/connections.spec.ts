import { expect, type Page } from '@playwright/test'
import { test } from './fixtures'
import { stats, token } from './synth'

async function post(page: Page, path: string, data: unknown = {}) {
  const response = await page.request.post(path, { headers: { Authorization: 'Bearer ' + await token(page), Origin: new URL(page.url()).origin }, data })
  expect(response.ok(), path + ': ' + response.status()).toBeTruthy()
  return response
}

test.beforeEach(async ({ page }) => {
  await page.goto('/')
  // Only the disposable synthetic fixture: clear its old warm session.
  await post(page, '/api/SelectTarget', { provider: 'synthetic', id: 'demo2' })
  await post(page, '/api/CloseTarget', { provider: 'synthetic', id: 'demo' })
  await post(page, '/api/_test/synthetic/reset')
  await post(page, '/api/_test/synthetic/select')
  await page.reload()
  await expect(page.getByRole('button', { name: 'Connect', exact: true })).toBeVisible()
})

test.afterEach(async ({ page }) => {
  const view = await (await post(page, '/api/ListTargets')).json()
  const target = view.groups.flatMap((g: { targets: { provider: string; id: string; connection?: { id: number } }[] }) => g.targets)
    .find((t: { provider: string; id: string }) => t.provider === 'synthetic' && t.id === 'demo')
  if (target?.connection) await post(page, '/api/CancelConnectTarget', { provider: 'synthetic', id: 'demo', attempt: target.connection.id })
  await post(page, '/api/_test/synthetic/reset')
})

test('selection and reload do not connect; Connect opens resources explicitly', async ({ page }, testInfo) => {
  const resources: string[] = []
  page.on('request', (request) => {
    if (/\/api\/(ListKinds|ListScopes|OpenView)$/.test(request.url()) && request.postDataJSON()?.target === 'demo') resources.push(request.url())
  })
  await page.getByRole('option', { name: /^demo\b/ }).click()
  await expect(page.getByRole('button', { name: 'Connect', exact: true })).toBeVisible()
  await page.reload()
  await expect(page.getByRole('button', { name: 'Connect', exact: true })).toBeVisible()
  expect((await stats(page)).syn_connects).toBe(0)
  expect(resources).toEqual([])
  await page.screenshot({ path: testInfo.outputPath('connect-idle.png') })
  await page.getByRole('button', { name: 'Connect', exact: true }).click()
  await expect(page.getByRole('grid', { name: 'resources' }).getByRole('gridcell', { name: 'api', exact: true })).toBeVisible()
  expect((await stats(page)).syn_connects).toBe(1)
})

test('Cancel stops a pending connection and a new explicit attempt can succeed', async ({ page }, testInfo) => {
  await post(page, '/api/_test/synthetic/controls', { connect_delay_ms: 10000 })
  await page.getByRole('button', { name: 'Connect', exact: true }).click()
  await expect(page.getByText('Authenticating and checking the server…')).toBeVisible()
  await expect(page.getByText(/Attempt 1 of 3/)).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('connect-pending.png') })
  await page.getByRole('button', { name: 'Cancel', exact: true }).click()
  await expect(page.getByRole('button', { name: 'Connect', exact: true })).toBeVisible()
  await expect.poll(async () => (await stats(page)).syn_connecting).toBe(0)
  expect((await stats(page)).syn_connects).toBe(1)
  await page.reload()
  await expect(page.getByText('Connection cancelled')).toBeVisible()
  expect((await stats(page)).syn_connects).toBe(1)
  await post(page, '/api/_test/synthetic/controls', {})
  await page.getByRole('button', { name: 'Connect', exact: true }).click()
  await expect(page.getByRole('grid', { name: 'resources' }).getByRole('gridcell', { name: 'api', exact: true })).toBeVisible()
})

test('temporary failures show retry progress and recover on the third attempt', async ({ page }, testInfo) => {
  await post(page, '/api/_test/synthetic/controls', { connect_delay_ms: 100, connect_failures: 2 })
  await page.getByRole('button', { name: 'Connect', exact: true }).click()
  await expect(page.getByText('synthetic connection unavailable')).toBeVisible()
  await expect(page.getByText(/Retrying in/)).toBeVisible()
  await page.screenshot({ path: testInfo.outputPath('connect-retry.png') })
  await expect(page.getByRole('grid', { name: 'resources' }).getByRole('gridcell', { name: 'api', exact: true })).toBeVisible()
  expect((await stats(page)).syn_connects).toBe(3)
})

test('three failed attempts stop and reload does not start another', async ({ page }, testInfo) => {
  await post(page, '/api/_test/synthetic/controls', { connect_failures: 99 })
  await page.getByRole('button', { name: 'Connect', exact: true }).click()
  await expect(page.getByText('Could not connect', { exact: true })).toBeVisible()
  await expect(page.getByText(/Attempt 3 of 3/)).toBeVisible()
  await expect(page.getByRole('button', { name: 'Connect', exact: true })).toBeVisible()
  expect((await stats(page)).syn_connects).toBe(3)
  await page.screenshot({ path: testInfo.outputPath('connect-failed.png') })
  await page.reload()
  await expect(page.getByText('Could not connect', { exact: true })).toBeVisible()
  expect((await stats(page)).syn_connects).toBe(3)
})


test('Disconnect is available on the selected connected target and disappears after closing',async({page})=>{
 const row=page.getByRole('region',{name:'Synthetic (test)',exact:true}).getByRole('option',{name:/^demo\b/})
 await page.getByRole('button',{name:'Connect',exact:true}).click()
 await expect(page.getByRole('grid',{name:'resources'})).toBeVisible()
 await row.click({button:'right'})
 await page.getByRole('menuitem',{name:'Disconnect',exact:true}).click()
 await expect(page.getByRole('button',{name:'Connect',exact:true})).toBeVisible()
 await expect(row).toHaveAttribute('aria-selected','true')
 await row.click({button:'right'})
 await expect(page.getByRole('menuitem',{name:'Disconnect',exact:true})).toHaveCount(0)
 await page.screenshot({path:process.env.OCULAR_SCRATCH_DIR+'/selected-disconnected.png'})
})
