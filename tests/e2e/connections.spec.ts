import {openConnections} from './fixtures'
import { selectConnection } from './fixtures'
import { expect, type Page } from '@playwright/test'
import { test,openAppMenu,scratchRoot } from './fixtures'
import {languages,languageNames} from '../../web/src/languages'
import {join} from 'node:path'
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
  await selectConnection(page,/^demo\b/)
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
 const row=page.getByRole('button',{name:/^demo\b/}).and(page.locator('.connection-choice'))
 await page.getByRole('button',{name:'Connect',exact:true}).click()
 await expect(page.getByRole('grid',{name:'resources'})).toBeVisible()
 await openConnections(page)
 await expect(row.locator('[data-connection-state=connected]')).toHaveCSS('background-color','rgb(126, 197, 156)')
 await page.screenshot({path:process.env.OCULAR_SCRATCH_DIR+'/selected-connected-green.png'})
 await row.click({button:'right'})
 await page.getByRole('menuitem',{name:'Disconnect',exact:true}).click()
 await expect(page.getByRole('button',{name:'Connect',exact:true})).toBeVisible()
 await openConnections(page)
 await expect(row).toHaveAttribute('aria-pressed','true')
 await row.click({button:'right'})
 await expect(page.getByRole('menuitem',{name:'Disconnect',exact:true})).toHaveCount(0)
 await page.screenshot({path:process.env.OCULAR_SCRATCH_DIR+'/selected-disconnected.png'})
})


test('disconnected coast and main Connect fit all languages without reading the resource catalog',async({page})=>{
 const view=await (await post(page,'/api/ListTargets')).json(),target=view.groups.find((g:{provider:string})=>g.provider==='kubernetes').targets[0]
 const reads:string[]=[]
 page.on('request',request=>{if(/\/api\/(ListKinds|ListScopes|OpenView|ConnectTarget)$/.test(request.url())&&(request.postDataJSON()?.target===target.id||request.postDataJSON()?.id===target.id))reads.push(request.url())})
 await post(page,'/api/SelectTarget',{provider:'kubernetes',id:target.id});await page.reload()
 const scene=page.locator('.connection-workspace'),main=scene.locator('main'),nav=scene.getByRole('navigation',{name:'resources'})
 await expect(scene).toBeVisible();await expect(nav.locator('button')).toHaveCount(0);await expect(nav.locator('[aria-disabled=true]')).toHaveCount(9)
 expect(await main.evaluate(el=>getComputedStyle(el,'::before').backgroundImage)).toContain('disconnected-mountains')
 expect(await nav.evaluate(el=>getComputedStyle(el,'::before').backgroundImage)).toContain('disconnected-lighthouse')
 expect(await main.evaluate(el=>getComputedStyle(el).backgroundColor)).toBe('rgb(21, 24, 30)')
 expect(await nav.evaluate(el=>getComputedStyle(el).backgroundColor)).toBe('rgb(25, 30, 38)')
 expect(await scene.evaluate(el=>getComputedStyle(el,'::before').backgroundImage)).toBe('none')
 await page.setViewportSize({width:1600,height:900})
 const grip=scene.getByRole('separator',{name:'Resize resource navigation',exact:true})
 const fixedArt=()=>nav.evaluate(el=>Object.fromEntries(['::before','::after'].map(pseudo=>{const s=getComputedStyle(el,pseudo);return [pseudo,{width:s.width,height:s.height,left:s.left,bottom:s.bottom,backgroundSize:s.backgroundSize}]})))
 const initialArt=await fixedArt()
 expect(initialArt['::before']).toMatchObject({width:'512px',height:'342px',left:'-55px',bottom:'0px'})
 expect(await nav.evaluate(el=>getComputedStyle(el,'::after').backgroundImage)).toBe('none')
 for(const width of [140,232,360,192]){
  const area=(await nav.boundingBox())!,handle=(await grip.boundingBox())!
  await page.mouse.move(handle.x+handle.width/2,handle.y+handle.height/2);await page.mouse.down()
  await page.mouse.move(handle.x+handle.width/2+width-area.width,handle.y+handle.height/2,{steps:5});await page.mouse.up()
  await expect.poll(async()=>(await nav.boundingBox())!.width).toBe(width)
  expect(await fixedArt()).toEqual(initialArt)
  await expect(nav).toHaveCSS('overflow-x','clip');await expect(nav).toHaveCSS('overflow-y','clip')
  await nav.screenshot({path:join(scratchRoot,`lighthouse-fixed-${width}.png`)})
 }
 for(const height of [820,554,360]){
  await page.setViewportSize({width:1440,height:900})
  await scene.evaluate((el,h)=>{el.style.flex='none';el.style.height=h+'px'},height)
  await expect(nav.locator('.connection-preview-sections')).toHaveCSS('overflow-y','clip')
  expect(await nav.evaluate(el=>el.scrollHeight<=el.clientHeight)).toBe(true)
  await expect(nav.locator('[aria-disabled=true]').last()).toBeVisible()
 }
 await scene.evaluate(el=>{el.style.removeProperty('flex');el.style.removeProperty('height')})
 for(const language of languages){
  await page.setViewportSize({width:1280,height:900})
  await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[language],exact:true}).click()
  await expect(page.locator('html')).toHaveAttribute('lang',language)
  await expect(main.getByRole('button')).toHaveCount(1)
  const action=(await main.getByRole('button').boundingBox())!,area=(await main.boundingBox())!
  expect(action.x).toBeGreaterThanOrEqual(area.x);expect(action.x+action.width).toBeLessThanOrEqual(area.x+area.width)
  await page.screenshot({path:join(scratchRoot,`disconnected-${language}.png`)})
 }
 await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames.en,exact:true}).click()
 for(const width of [860,375]){
  await page.setViewportSize({width,height:620})
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
  await expect(main.getByRole('button',{name:'Connect',exact:true})).toBeVisible()
  expect(await main.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true)
  expect(await nav.evaluate(el=>el.scrollHeight<=el.clientHeight)).toBe(true)
  await page.screenshot({path:join(scratchRoot,`disconnected-narrow-${width}.png`)})
 }
 expect(reads).toEqual([])
 await page.locator('[data-connection-switcher]').focus();await page.keyboard.press('F6');await expect(nav).toBeFocused();await page.keyboard.press('F6');await expect(main.getByRole('button',{name:'Connect',exact:true})).toBeFocused()
 const failedView=structuredClone(view)
 failedView.selected={provider:'kubernetes',id:target.id}
 const item=failedView.groups.find((g:{provider:string})=>g.provider==='kubernetes').targets.find((t:{id:string})=>t.id===target.id)
 item.title='A very long connection title with a precise environment name and regional suffix that must remain readable'
 item.details=[{key:'server',value:'https://'+('very-long-endpoint.'.repeat(10))+'example.invalid:6443'}]
 item.open=false;item.connection={id:998,state:'failed',phase:'failed',attempt:3,maxAttempts:3,startedAt:Date.now()-8000,finishedAt:Date.now(),attemptStarted:Date.now()-1000,errorClass:'unavailable',error:('Credential helper unavailable: an intentionally long synthetic explanation.\n').repeat(20)}
 await page.route('**/api/ListTargets',route=>route.fulfill({json:failedView}))
 await page.reload();await expect(main.getByRole('alert')).toBeVisible();await expect(main.getByRole('button',{name:'Connect',exact:true})).toBeEnabled()
 expect(await main.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true)
 await main.getByRole('button',{name:'Connect',exact:true}).scrollIntoViewIfNeeded()
 await page.screenshot({path:join(scratchRoot,'disconnected-long-error.png')})
 expect(reads).toEqual([])
 await page.unrouteAll({behavior:'wait'})
 // A real retained dock reduces the disconnected navigation's height.
 await page.setViewportSize({width:1440,height:900})
 await post(page,'/api/_test/synthetic/select')
 await page.getByRole('button',{name:'Connect',exact:true}).click()
 await page.getByRole('grid',{name:'resources'}).getByRole('gridcell',{name:'api',exact:true}).click()
 await page.getByRole('dialog',{name:'services api',exact:true}).getByRole('button',{name:'Logs',exact:true}).click()
 const panel=page.getByRole('region',{name:'Bottom panel',exact:true})
 await expect(panel).toBeVisible()
 const picker=await openConnections(page)
 await picker.locator('.connection-choice').filter({has:page.getByText(target.title,{exact:true})}).click()
 await expect(scene).toBeVisible();await expect(panel).toBeVisible()
 expect(await nav.evaluate(el=>el.scrollHeight<=el.clientHeight)).toBe(true)
 await expect(nav.locator('.connection-preview-sections')).toHaveCSS('overflow-y','clip')
 await expect(nav.locator('[aria-disabled=true]').last()).toBeVisible()
 await page.screenshot({path:join(scratchRoot,'disconnected-with-dock.png')})
 await panel.getByRole('button',{name:'Close tab',exact:true}).click()
})
