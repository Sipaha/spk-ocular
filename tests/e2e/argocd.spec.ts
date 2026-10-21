import {expect,type Page} from '@playwright/test'
import {test,openAppMenu,scratchRoot} from './fixtures'
import {english,argoPage,openArgo} from './argocd-fixtures'
import {join} from 'node:path'
import {languages,languageNames} from '../../web/src/languages'
import type {ArgoSnapshot} from '../../web/src/argocd/types'
test.beforeEach(async({page})=>english(page))
test.afterEach(async({page})=>english(page))
async function selectApp(page:Page,name='storefront'){
 const ws=page.locator('.argo-workspace');const grid=ws.getByRole('grid',{name:'Applications',exact:true})
 await grid.locator('[data-row-id]').filter({hasText:name}).click()
 return ws.getByLabel('Argo CD details',{exact:true})
}
test('native snapshot shows independent axes, sources, conditions, typed pages and attention',async({page})=>{
 const ws=await openArgo(page)
 await expect(ws.getByRole('grid')).toHaveAttribute('aria-rowcount','2')
 const details=await selectApp(page)
 await expect(details).toContainText('Deployment has unavailable replicas')
 await expect(details).toContainText('Compared target revision: 0123456789abcdef')
 await expect(details).toContainText('Requested revision: main')
 await expect(ws.getByRole('grid')).toContainText('Unknown')
 await page.screenshot({path:join(scratchRoot,'argo-applications-en.png')})
 await argoPage(page,'Application Sets');await expect(ws.getByRole('grid')).toHaveAttribute('aria-rowcount','1')
 await argoPage(page,'Projects');await expect(ws.getByRole('grid')).toContainText('commerce')
 await argoPage(page,'Dashboard');await expect(ws.getByLabel('Activity',{exact:true})).toContainText('Sync operation failed')
 await argoPage(page,'Applications')
 await ws.getByRole('textbox',{name:'Search applications, projects and sources…'}).fill('catalog')
 await expect(ws.getByRole('grid')).toHaveAttribute('aria-rowcount','1')
 await selectApp(page,'catalog');await expect(ws.getByLabel('Argo CD details',{exact:true})).toContainText('Local destination not verified')
})
test('projection read failure retains snapshot, coverage distinguishes missing and denied, empty scope stays empty',async({page})=>{
 let last:ArgoSnapshot|null=null,fail=false
 await page.route('**/api/ArgoCD',async route=>{
  if(fail){await route.fulfill({status:503,json:{code:'unavailable',detail:'Argo fixture unavailable'}});return}
  const response=await route.fetch();last=await response.json() as ArgoSnapshot
  last.coverage.push({kind:'Service',scope:'blue',state:'forbidden'},{kind:'AppProject',scope:'blue',state:'missing'});last.truncated=true
  await route.fulfill({response,json:last})
 })
 const ws=await openArgo(page);await expect(ws).toContainText('Incomplete coverage')
 await ws.locator('summary').filter({hasText:'Read coverage'}).click();await expect(ws).toContainText('forbidden');await expect(ws).toContainText('missing')
 fail=true;await ws.locator('[data-resync]').click();await expect(ws.getByRole('alert')).toContainText('Showing an earlier snapshot')
 await expect(ws.getByRole('grid')).toHaveAttribute('aria-rowcount','2')
 fail=false
 const zone=ws.getByRole('textbox',{name:'Zone',exact:true});await zone.fill('other');await zone.press('Enter')
 await expect(ws.getByRole('grid')).toHaveAttribute('aria-rowcount','0');expect(last?.objects).toHaveLength(0)
})
test('all four actions use reviewed shared dialogs and bulk preserves independent outcomes',async({page})=>{
 const writes:string[]=[]
 let unavailable=false
 await page.route('**/api/PrepareAction',route=>{
  const req=route.request().postDataJSON()
  return route.fulfill({json:{where:{ref:req.ref,targetTitle:'demo',endpoint:'https://fixture.test',configRev:'fixture-config'},action:{id:req.action,title:req.action,destructive:req.action.includes('sync'),noAgents:true},params:{},expect:'reviewed-'+req.ref.uid,destructive:req.action.includes('sync'),effects:[{text:'Pinned revision 0123456789abcdef0123456789abcdef01234567; prune reviewed explicitly.'}],warnings:[{text:'Kubernetes patch rights; Argo server RBAC is not evaluated. Controller completion is asynchronous.'}],rights:{state:'allowed',checks:[]},...(unavailable?{unavailable:{text:'Application has a pending operation'}}:{})}})
 })
 await page.route('**/api/RunAction',route=>{const req=route.request().postDataJSON();writes.push(req.action);return req.ref.name==='catalog'?route.fulfill({status:409,json:{code:'conflict',detail:'Application changed after review'}}):route.fulfill({json:{message:{text:'Requested; controller completion is asynchronous.'}}})})
 const ws=await openArgo(page);await selectApp(page)
 for(const label of ['Sync','Sync with prune','Refresh','Hard refresh']){
  const before=writes.length
  await ws.getByRole('button',{name:label,exact:true}).filter({hasText:label}).click()
  const dialog=page.getByRole('dialog').filter({hasText:'Pinned revision'})
  await expect(dialog).toContainText('Kubernetes patch rights')
  await expect(dialog.getByRole('button',{name:label.startsWith('Sync')?'Cancel':label,exact:true})).toBeFocused()
  expect(writes).toHaveLength(before)
  await dialog.getByRole('button',{name:label,exact:true}).click();await expect.poll(()=>writes.length).toBe(before+1)
  await expect(dialog).toHaveCount(0)
 }
 unavailable=true;await ws.getByRole('button',{name:'Sync',exact:true}).click()
 let dialog=page.getByRole('dialog');await expect(dialog).toContainText('pending operation');await expect(dialog.getByRole('button',{name:'Sync',exact:true})).toBeDisabled();await dialog.getByRole('button',{name:'Cancel',exact:true}).click();unavailable=false
 const grid=ws.getByRole('grid');await grid.getByRole('checkbox').first().check()
 await ws.getByRole('button',{name:'Sync · 2',exact:true}).click()
 dialog=page.getByRole('dialog');await expect(dialog).toContainText('storefront');await expect(dialog).toContainText('catalog')
 await expect(dialog.getByRole('button',{name:'Sync 2',exact:true})).toBeEnabled()
 await dialog.getByRole('button',{name:'Sync 2',exact:true}).click()
 await expect(dialog).toContainText('Application changed after review')
 expect(writes.filter(a=>a==='argo-sync')).toHaveLength(3)
 await page.screenshot({path:join(scratchRoot,'argo-bulk-outcomes.png')})
})
test('eight locales and narrow layouts retain a bounded grid and projection details',async({page})=>{
 const ws=await openArgo(page);await selectApp(page)
 for(const language of languages){
  await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[language],exact:true}).click()
  await expect(page.locator('html')).toHaveAttribute('lang',language)
  for(const width of [1440,375]){
   await page.setViewportSize({width,height:900})
   expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBeTruthy()
   expect(await ws.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBeTruthy()
   await expect(ws.getByRole('columnheader')).toHaveCount(9)
   await expect(ws.getByRole('grid')).toHaveAttribute('aria-rowcount','2')
   if(language==='en'||language==='ru')await page.screenshot({path:join(scratchRoot,`argo-${language}-${width}.png`)})
  }
 }
})

test('reported local resources resolve a fresh UID and refresh/navigation preserve held drafts',async({page})=>{
 const gets:{uid?:string;kind:string;name:string}[]=[]
 page.on('request',r=>{if(r.url().endsWith('/api/GetResource'))gets.push(r.postDataJSON())})
 await page.route('**/api/ListKinds',async route=>{const response=await route.fetch(),data=await response.json();for(const kind of data.kinds)if(kind.id==='crates')kind.editable=true;await route.fulfill({response,json:data})})
 await page.route('**/api/GetEditSource',route=>route.fulfill({json:{ref:route.request().postDataJSON(),text:'name: alpha\n',base:'argo-editor-fixture'}}))
 const ws=await openArgo(page),details=await selectApp(page)
 expect(gets).toHaveLength(0)
 await details.getByRole('button',{name:'Crate / blue/crate-7f3a',exact:true}).click()
 const drawer=ws.locator('[data-resource-drawer]');await expect(drawer.getByRole('heading',{name:'alpha',exact:true})).toBeVisible()
 expect(gets[0].uid).toBeUndefined();expect(gets.slice(1).every(r=>r.uid==='crate-7f3a')).toBeTruthy()
 await drawer.getByRole('tab',{name:'YAML',exact:true}).click();await drawer.getByRole('button',{name:'Edit',exact:true}).click()
 await drawer.locator('.cm-content').click();await page.keyboard.press('Control+End');await page.keyboard.type('\n# retained Argo draft')
 await ws.locator('[data-resync]').click();await expect(ws.locator('[data-resync]')).toBeEnabled()
 await expect(drawer.locator('.cm-content')).toContainText('retained Argo draft')
 await argoPage(page,'Dashboard')
 const confirm=page.getByRole('alertdialog',{name:'Discard edits?',exact:true});await expect(confirm).toBeVisible();await confirm.getByRole('button',{name:'Keep editing',exact:true}).click()
 await expect(drawer.locator('.cm-content')).toContainText('retained Argo draft')
 await page.screenshot({path:join(scratchRoot,'argo-held-draft.png')})
})

test('multiple Argo UI services require explicit selection and existing reviewed forwarding',async({page})=>{
 const infos:string[]=[]
 let forwards=0
 await page.route('**/api/ArgoCD',async route=>{const response=await route.fetch(),value=await response.json();value.servers=['server-east','server-west'].map(name=>({provider:'synthetic',target:'demo',kind:'services',scope:'blue',name,uid:'uid-'+name}));await route.fulfill({response,json:value})})
 await page.route('**/api/ForwardInfo',route=>{infos.push(route.request().postDataJSON().name);return route.fulfill({json:{ports:[{port:443,name:'https',protocol:'TCP',supported:true,scheme:'https'}]}})})
 await page.route('**/api/StartForward',route=>{const req=route.request().postDataJSON();expect(req.ref.name).toBe('server-west');expect(req.ref.uid).toBe('uid-server-west');expect(req.scheme).toBe('https');forwards++;return route.fulfill({json:{id:'fixture-argo-forward',ref:req.ref,localPort:54433,port:443,scheme:'https',state:'ready'}})})
 const ws=await openArgo(page);expect(infos).toHaveLength(0);expect(forwards).toBe(0)
 await ws.locator('summary').filter({hasText:'Argo CD UI service'}).click()
 const choose=ws.getByRole('button',{name:'Argo CD UI service',exact:true});await choose.click();await page.getByRole('option',{name:'blue/server-west',exact:true}).click()
 await expect.poll(()=>infos).toEqual(['server-west']);expect(forwards).toBe(0)
 await ws.getByRole('button',{name:'Forward',exact:true}).click()
 const dialog=page.getByRole('dialog');await expect(dialog.getByRole('textbox',{name:'Remote port',exact:true})).toHaveValue('443')
 await dialog.getByRole('button',{name:'Forward',exact:true}).click();await expect.poll(()=>forwards).toBe(1)
})

test('late resource lookup cannot open details in a replacement namespace',async({page})=>{
 let release=()=>{},started=false
 const gate=new Promise<void>(resolve=>{release=resolve})
 await page.route('**/api/GetResource',async route=>{started=true;await gate;await route.fulfill({json:{ref:{...route.request().postDataJSON(),uid:'replacement-uid'},health:{state:'ok'},facts:[],yaml:'',relations:[]}})})
 const ws=await openArgo(page),details=await selectApp(page)
 await details.getByRole('button',{name:'Crate / blue/crate-7f3a',exact:true}).click();await expect.poll(()=>started).toBe(true)
 const zone=ws.getByRole('textbox',{name:'Zone',exact:true});await zone.fill('other');await zone.press('Enter')
 await expect(ws.getByRole('grid')).toHaveAttribute('aria-rowcount','0')
 release();await page.unrouteAll({behavior:'wait'})
 await expect(ws.locator('[data-resource-drawer]')).toHaveCount(0)
})

test('large projections virtualize rows and cancelled reads cannot replace the current snapshot',async({page})=>{
 let delayed=false,release=()=>{}
 const gate=new Promise<void>(resolve=>{release=resolve})
 await page.route('**/api/ArgoCD',async route=>{
  const response=await route.fetch(),value:ArgoSnapshot=await response.json()
  if(delayed){await gate;value.objects=[]}else if(value.objects.length){const app=value.objects[0];value.objects=Array.from({length:12000},(_,i)=>({...app,ref:{...app.ref,name:'app-'+i,uid:'app-'+i}}))}
  await route.fulfill({response,json:value})
 })
 const ws=await openArgo(page),grid=ws.getByRole('grid')
 await expect(grid).toHaveAttribute('aria-rowcount','12000');expect(await grid.locator('[data-row-id]').count()).toBeLessThan(100)
 await grid.locator('[data-table-scroll]').evaluate(el=>{el.scrollTop=32000})
 await expect(grid.locator('[data-row-id]').first()).not.toContainText('app-0')
 delayed=true;await ws.locator('[data-resync]').click()
 await ws.getByRole('button',{name:'Cancel',exact:true}).click()
 await expect(ws.locator('[data-load-cancelled]')).toBeVisible()
 delayed=false;release()
 await ws.getByRole('button',{name:'Retry',exact:true}).click()
 await expect(grid).toHaveAttribute('aria-rowcount','12000')
 await page.unrouteAll({behavior:'wait'})
})
