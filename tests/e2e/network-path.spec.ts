import {expect,type Page} from '@playwright/test'
import {test,selectConnection,connectSelected,openAppMenu,scratchRoot} from './fixtures'
import {english} from './argocd-fixtures'
import {languages,languageNames} from '../../web/src/languages'
import {snapshot} from '../../web/src/network/fixtures'
import type {Ref} from '../../web/src/api/types'
import {join} from 'node:path'

test.beforeEach(async({page})=>english(page));test.afterEach(async({page})=>english(page))
// These authored projections isolate inspector interactions. The real provider
// resolver is exercised separately against fake Kubernetes HTTP/dynamic clients.
function observation(ref:Ref){
 const data=structuredClone(snapshot);data.ref=ref
 const map=(r:Ref)=>({...r,provider:ref.provider,target:ref.target,scope:ref.scope})
 for(const r of data.routes){r.service=map(r.service);for(const e of r.endpoints){e.slice=map(e.slice);if(e.pod)e.pod=map(e.pod);e.owners=e.owners.map(map)}}
 return data
}
async function open(page:Page,available=true){
 await page.route('**/api/GetResource',async route=>{const ref=route.request().postDataJSON();if(ref.kind==='pods'||ref.kind==='discovery.k8s.io/endpointslices')return route.fulfill({json:{ref,health:{state:'ok'},facts:[],yaml:'kind: Pod\n',relations:[]}});const response=await route.fetch(),value=await response.json();value.networkPathAvailable=available;await route.fulfill({response,json:value})})
 await page.goto('/');await selectConnection(page,/^demo\b/);await connectSelected(page)
 await page.getByRole('navigation',{name:'resources'}).getByRole('button',{name:'Problems',exact:true}).click()
 await page.getByRole('grid',{name:'resources'}).getByRole('gridcell',{name:'api',exact:true}).click()
 const drawer=page.locator('[data-resource-drawer]');if(available)await expect(drawer.getByLabel('Declared network path',{exact:true})).toBeVisible();return drawer
}
test('declared path is explicit, preserves independent failures and opens exact UID properties without writes',async({page})=>{
 let reads=0,writes=0,opened:Ref|undefined
 page.on('request',req=>{if(/\/api\/(RunAction|RunEdit|StartForward|StartTerminal)$/.test(req.url()))writes++;if(req.url().endsWith('/api/GetResource'))opened=req.postDataJSON()})
 await page.route('**/api/ResourceNetworkPath',route=>{reads++;return route.fulfill({json:observation(route.request().postDataJSON())})})
 const drawer=await open(page),panel=drawer.getByLabel('Declared network path',{exact:true});expect(reads).toBe(0)
 await panel.getByRole('button',{name:'Load declared path'}).click();await expect(panel).toContainText('Endpoint is marked not ready.');await expect(panel).toContainText('web.example/missing');await expect(panel).toContainText('Referenced Service was not found.');expect(reads).toBe(1)
 await expect(panel.getByRole('button',{name:'services/missing'})).toHaveCount(0)
 await page.screenshot({path:join(scratchRoot,'network-path-en-wide.png')})
 await panel.getByRole('button',{name:'pods/web-1'}).click();await expect(drawer.getByRole('heading',{name:'web-1',exact:true})).toBeVisible();expect(opened?.uid).toBe('pod-uid');expect(writes).toBe(0)
})
test('cancels a pending snapshot and fences its delayed response',async({page})=>{
 let started=false,release=()=>{};const gate=new Promise<void>(r=>{release=r})
 await page.route('**/api/ResourceNetworkPath',async route=>{started=true;await gate;await route.fulfill({json:observation(route.request().postDataJSON())})})
 const panel=(await open(page)).getByLabel('Declared network path',{exact:true});await panel.getByRole('button',{name:'Load declared path'}).click();await expect.poll(()=>started).toBe(true);await panel.getByRole('button',{name:'Cancel',exact:true}).click();release()
 await expect(panel).toContainText('Request cancelled');await expect(panel).not.toContainText('Endpoint is marked not ready.');await page.unrouteAll({behavior:'wait'})
})
test('declared paths and coverage fit all eight languages at wide and narrow widths',async({page})=>{
 await page.route('**/api/ResourceNetworkPath',route=>{const data=observation(route.request().postDataJSON());data.coverage[0].state='forbidden';data.truncated=true;return route.fulfill({json:data})})
 const drawer=await open(page);await drawer.getByRole('button',{name:'Load declared path'}).click();await expect(drawer).toContainText('10.0.0.1')
 const panel=drawer.locator('section').filter({has:page.locator('article')});await panel.locator('summary').click()
 for(const code of languages){
  await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[code],exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang',code)
  for(const width of [1440,375]){await page.setViewportSize({width,height:1000});expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);const dimensions=await drawer.evaluate(el=>({fits:el.scrollWidth<=el.clientWidth,overflow:[...el.querySelectorAll('*')].filter(e=>e.clientWidth>0&&e.scrollWidth>e.clientWidth).map(e=>[e.tagName,e.className,e.clientWidth,e.scrollWidth,e.textContent?.slice(0,100)])}));expect(dimensions.fits,JSON.stringify({code,width,...dimensions})).toBe(true);await panel.evaluate(el=>{let parent=el.parentElement;while(parent){if(getComputedStyle(parent).overflowY==='auto'){parent.scrollTop=0;break}parent=parent.parentElement}});await page.screenshot({path:join(scratchRoot,`network-path-${code}-${width}-top.png`)});await panel.locator('summary').scrollIntoViewIfNeeded();await page.screenshot({path:join(scratchRoot,`network-path-${code}-${width}-coverage.png`)})}
 }
})

test('narrow Service port controls fit independently of network snapshots',async({page})=>{
 const drawer=await open(page,false);await expect(drawer.getByLabel('Declared network path',{exact:true})).toHaveCount(0)
 await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames.es,exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang','es');await page.setViewportSize({width:375,height:1000})
 const dimensions=await drawer.evaluate(el=>({width:el.clientWidth,scrollWidth:el.scrollWidth}));expect(dimensions.scrollWidth).toBeLessThanOrEqual(dimensions.width)
 await page.screenshot({path:join(scratchRoot,'network-path-baseline-ports-es-375.png')})
})
