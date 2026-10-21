import {expect, type Page} from '@playwright/test'
import {test, scratchRoot, selectConnection, connectSelected} from './fixtures'
import {english} from './argocd-fixtures'
import {join} from 'node:path'
import {languages} from '../../web/src/languages'
import {argo as baseArgo, flux as baseFlux, context as baseContext} from '../../web/src/management/fixtures'

// Authored review projections exercise the real dialogs without mutating a
// controller or requiring a cluster. Backend metadata has separate provider tests.
async function fixture(page:Page) {
 let configRev = '', reads = 0, writes = 0, prepares = 0
 await page.route('**/api/ListTargets',async route=>{const response=await route.fetch(),value=await response.json();configRev=value.groups.flatMap((g:{targets:{id:string;configRev:string}[]})=>g.targets).find((t:{id:string})=>t.id==='demo')?.configRev??'';await route.fulfill({response})})
 await page.route('**/api/ListKinds',async route=>{const response=await route.fetch(),value=await response.json();for(const k of value.kinds)if(k.id==='workloads')k.editable=true;await route.fulfill({response,json:value})})
 await page.route('**/api/GetEditSource',route=>route.fulfill({json:{ref:route.request().postDataJSON(),text:'name: web\nreplicas: 2\n',base:'gitops-draft-fixture'}}))
 const management=(ref:typeof baseContext.ref)=>({...baseContext,ref,argoTracking:{...baseContext.argoTracking!,group:'',kind:'Workload'},fluxTracking:{...baseContext.fluxTracking!,group:'',kind:'Workload'}})
 await page.route('**/api/PrepareEdit',route=>{const req=route.request().postDataJSON();return route.fulfill({json:{where:{provider:'synthetic',target:'demo',targetTitle:'demo',configRev,ref:req.ref},before:req.original,after:req.edited,checked:true,changed:true,rights:{state:'allowed'},management:management(req.ref),token:'authored-review-no-write'}})})
 await page.route('**/api/PrepareAction',async route=>{prepares++;const response=await route.fetch(),value=await response.json();if(value.action.id==='scale')value.management=management(value.where.ref);await route.fulfill({response,json:value})})
 const mapRef=(ref:{kind:string;scope?:string;name:string;uid?:string})=>({...ref,provider:'synthetic',target:'demo'})
 await page.route('**/api/ArgoCD',route=>{reads++;expect(route.request().postDataJSON()).toEqual({provider:'synthetic',target:'demo',scope:{mode:'one',name:'gitops'}});return route.fulfill({json:{...baseArgo,objects:baseArgo.objects.map(o=>({...o,ref:mapRef(o.ref),resources:['web','db'].map(name=>({...o.resources[0],group:'',kind:'Workload',namespace:'',name}))}))}})})
 await page.route('**/api/FluxCD',route=>{reads++;expect(route.request().postDataJSON().scope).toEqual({mode:'one',name:'gitops'});return route.fulfill({json:{...baseFlux,objects:baseFlux.objects.map(o=>({...o,ref:mapRef(o.ref),inventory:['web','db'].map(name=>({...o.inventory[0],group:'',kind:'Workload',namespace:'',name}))}))}})})
 page.on('request',request=>{if(/\/api\/(RunEdit|RunAction|StartForward)$/.test(request.url()))writes++})
 await page.goto('/');await selectConnection(page,/^demo\b/);await connectSelected(page)
 await page.getByRole('navigation',{name:'resources'}).getByRole('button',{name:'Workloads',exact:true}).click()
 const grid=page.getByRole('grid',{name:'resources',exact:true});await expect(grid.getByRole('gridcell',{name:'web',exact:true})).toBeVisible()
 return {grid,reads:()=>reads,writes:()=>writes,prepares:()=>prepares}
}
test.beforeEach(async({page})=>english(page));test.afterEach(async({page})=>english(page))
test('YAML review loads explicit controller context, pins exact properties and retains its draft',async({page})=>{
 const f=await fixture(page);await f.grid.getByRole('gridcell',{name:'web',exact:true}).click()
 const drawer=page.locator('[data-area=details]');await drawer.getByRole('button',{name:'Edit',exact:true}).click()
 const editor=drawer.locator('.cm-content');await editor.click();await page.keyboard.press('Control+End');await page.keyboard.type('\n# gitops-held-draft');await editor.press('Control+Enter')
 const review=page.getByRole('dialog',{name:'Edit web',exact:true});await expect(review).toBeVisible();expect(f.reads()).toBe(0)
 await review.getByRole('textbox',{name:'Controller namespace'}).fill('gitops');await review.getByRole('button',{name:'Load controller context'}).click()
 await expect(review).toContainText('ComparisonError: Manifest comparison failed');await expect(review).toContainText('Repository fetch failed');expect(f.reads()).toBe(2)
 await review.getByRole('button',{name:/Pin properties · argoproj/}).click();await expect(review).toBeVisible();await expect(review).toContainText('gitops-held-draft')
 await review.getByRole('button',{name:'Back to the text'}).click();await expect(editor).toContainText('gitops-held-draft');expect(f.writes()).toBe(0)
 await expect(page.locator('[data-area=dock]')).toContainText('commerce')
 await page.screenshot({path:join(scratchRoot,'management-retained-draft.png')})
})
test('scale uses shared context in single and independent bulk reviews without running actions',async({page})=>{
 const f=await fixture(page);const web=f.grid.getByRole('row').filter({has:page.getByRole('gridcell',{name:'web',exact:true})})
 await web.click({button:'right'});await page.getByRole('menuitem',{name:'Scale…',exact:true}).click()
 const single=page.getByRole('dialog',{name:'Scale web',exact:true});await single.getByRole('textbox',{name:'Replicas'}).fill('4');await single.getByRole('textbox',{name:'Replicas'}).press('Enter');await expect(single.getByRole('region',{name:'GitOps review context'})).toBeVisible();expect(f.reads()).toBe(0)
 const beforeEnter=f.prepares();await single.getByRole('textbox',{name:'Controller namespace'}).press('Enter');expect(f.prepares()).toBe(beforeEnter);
 await single.getByRole('button',{name:'Load controller context'}).click();await expect(single).toContainText('Manifest comparison failed');await single.getByRole('button',{name:'Cancel',exact:true}).click()
 await web.getByRole('checkbox').click();const db=f.grid.getByRole('row').filter({has:page.getByRole('gridcell',{name:'db',exact:true})});await db.getByRole('checkbox').click();await db.click({button:'right'});await page.getByRole('menuitem',{name:'Scale…',exact:true}).click()
 const bulk=page.getByRole('dialog',{name:'Scale 2 objects',exact:true});await bulk.getByRole('textbox',{name:'Replicas'}).fill('4');await bulk.getByRole('textbox',{name:'Replicas'}).press('Enter')
 await bulk.getByRole('button',{name:'db',exact:true}).click();await expect(bulk.getByRole('region',{name:'GitOps review context'})).toBeVisible();await expect(bulk).toContainText('An autoscaler may override the count.')
 await bulk.getByRole('button',{name:'Load controller context'}).click();await expect(bulk).toContainText('Manifest comparison failed');expect(f.writes()).toBe(0)
 await page.screenshot({path:join(scratchRoot,'management-bulk-scale.png')})
})
test('review context fits eight languages at wide and narrow widths',async({page})=>{
 await fixture(page)
 for(const code of languages){
  await page.setViewportSize({width:1440,height:1000})
  const root=await page.request.get('/'),html=await root.text(),token=html.match(/spk-ocular-api-token" content="([^"]+)"/)?.[1]
  const saved=await page.request.post('/api/SetLanguage',{headers:{Authorization:'Bearer '+token,Origin:new URL(root.url()).origin},data:{language:code}});expect(saved.ok()).toBeTruthy()
  await page.reload();await expect(page.locator('html')).toHaveAttribute('lang',code)
  // The first ellipsis item in this fixture is Scale; resource identifiers
  // remain stable across locales. No action is ever run.
  await page.getByRole('grid').getByRole('row').filter({has:page.getByRole('gridcell',{name:'web',exact:true})}).click({button:'right'})
  await page.getByRole('menuitem').filter({hasText:/…/}).first().click()
  const dialog=page.locator('[role=dialog][aria-modal=true]')
  await dialog.getByRole('textbox').fill('4');await dialog.getByRole('textbox').press('Enter')
  const panel=dialog.locator('section').filter({has:page.locator('input')})
  await panel.locator('button').first().click();await expect(dialog).toContainText('Manifest comparison failed')
  for(const width of [1440,375]){
   await page.setViewportSize({width,height:1000})
   expect(await dialog.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true)
   expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
   await panel.locator('h3').scrollIntoViewIfNeeded()
   expect(await panel.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true)
   const frame=await panel.boundingBox(),input=await panel.locator('input').boundingBox()
   expect(input!.x).toBeGreaterThanOrEqual(frame!.x)
   expect(input!.x+input!.width).toBeLessThanOrEqual(frame!.x+frame!.width)
   await page.screenshot({path:join(scratchRoot,`management-${code}-${width}.png`)})
   await panel.getByText('ComparisonError: Manifest comparison failed').scrollIntoViewIfNeeded()
   await page.screenshot({path:join(scratchRoot,`management-conditions-${code}-${width}.png`)})
  }
 }
})
