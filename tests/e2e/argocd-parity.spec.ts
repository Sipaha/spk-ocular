import {expect,type Page} from '@playwright/test'
import {test,scratchRoot,openAppMenu} from './fixtures'
import {english,argoPage,openArgo} from './argocd-fixtures'
import {join} from 'node:path'
import {languages,languageNames} from '../../web/src/languages'
test.beforeEach(async({page})=>english(page));test.afterEach(async({page})=>english(page))
async function appDetails(page:Page){const ws=page.locator('.argo-workspace');await ws.getByRole('grid',{name:'Applications',exact:true}).locator('[data-row-id]').filter({hasText:'storefront'}).click();return ws.getByLabel('Argo CD details',{exact:true})}
test('Argo CD is a peer of Helm with independent pages and nested registered settings',async({page})=>{
 const ws=await openArgo(page),nav=page.getByRole('navigation',{name:'resources'}),argo=nav.getByRole('region',{name:'Argo CD',exact:true})
 await expect(argo.getByRole('button',{name:'Applications',exact:true})).toHaveAttribute('aria-current','page')
 await expect(nav.getByRole('region',{name:'Cluster',exact:true}).getByRole('button',{name:'Argo CD',exact:true})).toHaveCount(0)
 await expect(ws.getByRole('button',{name:'Application Sets',exact:true})).toHaveCount(0)
 await argoPage(page,'Dashboard');await expect(ws.getByRole('grid')).toHaveCount(0);await expect(page.getByRole('contentinfo')).toContainText('4 / 4');await expect(ws.getByLabel('Needs attention',{exact:true})).toContainText('storefront');await expect(ws.getByLabel('Activity',{exact:true})).toContainText('OperationCompleted')
 await ws.getByRole('textbox',{name:'Filter activity…'}).fill('no-such-event');await expect(ws.getByLabel('Activity',{exact:true})).not.toContainText('OperationCompleted');await ws.getByRole('textbox',{name:'Filter activity…'}).fill('sync')
 await expect(ws.getByLabel('Activity',{exact:true})).toContainText('Failure')
 await ws.getByLabel('Needs attention',{exact:true}).getByRole('button',{name:/blue\/storefront/}).click();await expect(argo.getByRole('button',{name:'Applications',exact:true})).toHaveAttribute('aria-current','page');await expect(ws.getByLabel('Argo CD details',{exact:true})).toContainText('Requested revision: main')
 await argoPage(page,'Application Sets');await expect(ws.getByRole('columnheader')).toHaveCount(4);await expect(ws.getByRole('grid')).toContainText('list')
 await argoPage(page,'Projects');await expect(ws.getByRole('columnheader')).toHaveCount(5);await expect(ws.getByRole('grid')).toContainText('Synthetic controller fixture')
 await argoPage(page,'Repositories');await expect(ws.getByRole('button',{name:'Load registered settings'})).toBeVisible();await expect(argo.getByRole('group',{name:'Settings',exact:true})).toBeVisible()
 await page.screenshot({path:join(scratchRoot,'argo-navigation-pages.png')})
})
test('registered repository and cluster descriptors require separate explicit loads and never open Secret values',async({page})=>{
 const requests:string[]=[],reads:string[]=[]
 page.on('request',r=>{if(r.url().endsWith('/api/ArgoCD')&&r.postDataJSON().settings)requests.push(r.postDataJSON().settings);if(/\/api\/(GetResource|GetValues|RevealValue|GetEditSource)$/.test(r.url()))reads.push(r.url())})
 const ws=await openArgo(page);await argoPage(page,'Repositories');expect(requests).toEqual([])
 await ws.getByRole('button',{name:'Load registered settings'}).click();await expect(ws.getByRole('grid',{name:'Repositories'})).toHaveAttribute('aria-rowcount','1');expect(requests).toEqual(['repository'])
 await ws.getByRole('grid').locator('[data-row-id]').click();await expect(ws.getByLabel('Argo CD details',{exact:true})).toContainText('https://git.example.test/commerce.git');await expect(ws.getByLabel('Argo CD details',{exact:true})).toContainText('Secret / blue/commerce');expect(reads).toEqual([])
 await page.screenshot({path:join(scratchRoot,'argo-repositories.png')})
 await argoPage(page,'Clusters');expect(requests).toEqual(['repository']);await ws.getByRole('button',{name:'Load registered settings'}).click();await expect(ws.getByRole('grid',{name:'Clusters'})).toContainText('https://production.example.test');expect(requests).toEqual(['repository','cluster']);expect(reads).toEqual([])
 await page.screenshot({path:join(scratchRoot,'argo-registered-clusters.png')})
 const zone=ws.getByRole('textbox',{name:'Zone',exact:true});await zone.fill('other');await zone.press('Enter');await expect(ws.getByRole('button',{name:'Load registered settings'})).toBeVisible();expect(requests).toHaveLength(2)
 await ws.getByRole('button',{name:'Load registered settings'}).click();await expect(ws.getByRole('grid')).toHaveAttribute('aria-rowcount','0');expect(requests).toEqual(['repository','cluster','cluster'])
})
test('dashboard batch selection preselects out-of-sync apps and refresh selects all before review',async({page})=>{
 const ws=await openArgo(page);await argoPage(page,'Dashboard')
 await ws.getByRole('button',{name:'Sync',exact:true}).click();let selection=ws.getByRole('region',{name:'Applications to review'});await expect(selection.getByRole('checkbox')).toHaveCount(2);await expect(selection.locator('input[type=checkbox]:checked')).toHaveCount(1)
 await selection.getByRole('button',{name:'Close',exact:true}).click();await ws.getByRole('button',{name:'Refresh',exact:true}).filter({hasText:'Refresh'}).click();selection=ws.getByRole('region',{name:'Applications to review'});await expect(selection.locator('input[type=checkbox]:checked')).toHaveCount(2)
 await page.screenshot({path:join(scratchRoot,'argo-dashboard-batch.png')})
})
test('a late resource answer rechecks an editor acquired during the lookup',async({page})=>{
 let release=()=>{},started=false;const gate=new Promise<void>(resolve=>{release=resolve})
 await page.route('**/api/ArgoCD',async route=>{const response=await route.fetch(),value=await response.json();if(!route.request().postDataJSON().settings){const app=value.objects.find((o:{ref:{name:string}})=>o.ref.name==='storefront');if(app)app.resources.push({...app.resources[0],name:'crate-91c2'})}await route.fulfill({response,json:value})})
 await page.route('**/api/ListKinds',async route=>{const response=await route.fetch(),value=await response.json();for(const k of value.kinds)if(k.id==='crates')k.editable=true;await route.fulfill({response,json:value})})
 await page.route('**/api/GetResource',async route=>{const ref=route.request().postDataJSON(),response=await route.fetch();if(ref.kind==='crates'&&ref.name==='crate-91c2'&&!ref.uid){started=true;await gate}await route.fulfill({response})})
 await page.route('**/api/GetEditSource',route=>route.fulfill({json:{ref:route.request().postDataJSON(),text:'name: alpha\n',base:'argo-pending-edit-fixture'}}))
 const ws=await openArgo(page),detail=await appDetails(page)
 await detail.getByRole('button',{name:'Crate / blue/crate-7f3a',exact:true}).click();const drawer=ws.locator('[data-resource-drawer]');await expect(drawer.getByRole('heading',{name:'alpha',exact:true})).toBeVisible()
 await detail.getByRole('button',{name:'Crate / blue/crate-91c2',exact:true}).click();await expect.poll(()=>started).toBe(true)
 await drawer.getByRole('tab',{name:'YAML',exact:true}).click();await drawer.getByRole('button',{name:'Edit',exact:true}).click();await drawer.locator('.cm-content').click();await page.keyboard.press('Control+End');await page.keyboard.type('\n# acquired during lookup')
 release();const prompt=page.getByRole('alertdialog',{name:'Discard edits?',exact:true});await expect(prompt).toBeVisible();await prompt.getByRole('button',{name:'Keep editing',exact:true}).click();await expect(drawer.getByRole('heading',{name:'alpha',exact:true})).toBeVisible();await expect(drawer.locator('.cm-content')).toContainText('acquired during lookup');await page.unrouteAll({behavior:'wait'})
})
test('ordinary properties link back only to a matching loaded Application and retain namespace selection',async({page})=>{
 let inventory=0
 page.on('request',r=>{if(r.url().endsWith('/api/ArgoCD'))inventory++})
 await page.route('**/api/GetResource',async route=>{const response=await route.fetch(),value=await response.json();if(value.ref.kind==='crates')value.argoTracking={application:'storefront',method:value.ref.name==='crate-7f3a'?'annotation':'label',group:'',kind:'Crate',installation:''};await route.fulfill({response,json:value})})
 await openArgo(page);const before=inventory
 const nav=page.getByRole('navigation',{name:'resources'});await nav.getByRole('button',{name:'Crates',exact:true}).click();const grid=page.getByRole('grid',{name:'resources',exact:true});await grid.locator('[data-row-id]').filter({hasText:'alpha'}).dblclick()
 let drawer=page.locator('[data-resource-drawer]');await expect(drawer.getByRole('button',{name:'Application / blue/storefront',exact:true})).toBeVisible();expect(inventory).toBe(before)
 await drawer.getByRole('button',{name:'Application / blue/storefront',exact:true}).click();const ws=page.locator('.argo-workspace');await expect(ws.getByLabel('Argo CD details',{exact:true})).toContainText('Application / storefront');await expect(ws.getByRole('textbox',{name:'Zone',exact:true})).toHaveValue('blue')
 await nav.getByRole('button',{name:'Crates',exact:true}).click();await page.locator('[data-resource-drawer] [data-drawer-close]').click();await page.getByRole('grid',{name:'resources',exact:true}).locator('[data-row-id]').filter({hasText:'beta'}).dblclick();drawer=page.locator('[data-resource-drawer]');await expect(drawer.getByRole('heading',{name:'beta',exact:true})).toBeVisible();await expect(drawer.getByRole('button',{name:'Application / blue/storefront',exact:true})).toHaveCount(0);await expect(drawer).toContainText('could not be verified')
 await page.screenshot({path:join(scratchRoot,'argo-unverified-instance-label.png')})
})
test('dashboard and nested Settings fit all eight locales at wide and narrow widths',async({page})=>{
 const ws=await openArgo(page)
 const language=async(code:typeof languages[number])=>{await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[code],exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang',code)}
 for(const code of languages){
  for(const view of ['Dashboard','Repositories']){
   await page.setViewportSize({width:1440,height:900});await language('en');await argoPage(page,view)
   if(view==='Repositories')await ws.getByRole('button',{name:'Load registered settings'}).click()
   await language(code)
   for(const width of [1440,375]){await page.setViewportSize({width,height:900});expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBeTruthy();expect(await ws.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBeTruthy();if(code==='en'||code==='ru')await page.screenshot({path:join(scratchRoot,`argo-${view.toLowerCase()}-${code}-${width}.png`)})}
  }
 }
})

test('Open Argo CD UI selects HTTPS and reuses the exact current Service forward',async({page})=>{
 let configRev='',starts=0,forwards:unknown[]=[]
 await page.addInitScript(()=>{const state=window as unknown as {argoOpened:string[]};state.argoOpened=[];window.open=(url)=>{state.argoOpened.push(String(url));return null}})
 page.on('response',async r=>{if(r.url().endsWith('/api/ListTargets')){const value=await r.json();configRev=value.groups.flatMap((g:{targets:{id:string;configRev:string}[]})=>g.targets).find((t:{id:string})=>t.id==='demo')?.configRev??''}})
 await page.route('**/api/ArgoCD',async route=>{const response=await route.fetch(),value=await response.json();value.servers=[{provider:'synthetic',target:'demo',kind:'services',scope:'blue',name:'argocd-server',uid:'ui-service-current'}];await route.fulfill({response,json:value})})
 await page.route('**/api/ForwardInfo',route=>route.fulfill({json:{ports:[{port:80,protocol:'TCP',scheme:'http',supported:true},{port:443,protocol:'TCP',scheme:'https',supported:true}]}}))
 await page.route('**/api/ListForwards',route=>route.fulfill({json:forwards}))
 await page.route('**/api/StartForward',route=>{starts++;const req=route.request().postDataJSON();expect(req.ref.uid).toBe('ui-service-current');expect(req.port).toBe(443);expect(req.scheme).toBe('https');const forward={id:'argo-ui-current',target:{provider:'synthetic',target:'demo',targetTitle:'demo',ref:req.ref,configRev,port:443},localPort:54433,addresses:['127.0.0.1:54433'],ipv6:'unavailable',scheme:'https',state:'ready',conns:0,served:0,rejected:0,failed:0,bytesIn:0,bytesOut:0,started:new Date().toISOString()};forwards=[forward];return route.fulfill({json:forward})})
 const ws=await openArgo(page);await argoPage(page,'Dashboard');await expect.poll(()=>configRev).not.toBe('')
 const open=ws.getByRole('button',{name:'Open Argo CD UI',exact:true});await open.click();await expect.poll(()=>starts).toBe(1);await expect.poll(()=>page.evaluate(()=>(window as unknown as {argoOpened:string[]}).argoOpened)).toEqual(['https://127.0.0.1:54433/'])
 await expect(open).toBeEnabled();await open.click();await expect.poll(()=>page.evaluate(()=>(window as unknown as {argoOpened:string[]}).argoOpened.length)).toBe(2);expect(starts).toBe(1)
})

test('late Argo UI port resolution cannot create a forward after a namespace transition',async({page})=>{
 let release=()=>{},started=false,writes=0;const gate=new Promise<void>(resolve=>{release=resolve})
 await page.route('**/api/ArgoCD',async route=>{const response=await route.fetch(),value=await response.json();if(value.objects.length)value.servers=[{provider:'synthetic',target:'demo',kind:'services',scope:'blue',name:'argocd-server',uid:'service-current'}];await route.fulfill({response,json:value})})
 await page.route('**/api/ForwardInfo',async route=>{started=true;await gate;await route.fulfill({json:{ports:[{port:443,protocol:'TCP',scheme:'https',supported:true}]}})})
 await page.route('**/api/StartForward',route=>{writes++;return route.abort()})
 const ws=await openArgo(page);await argoPage(page,'Dashboard');await ws.getByRole('button',{name:'Open Argo CD UI',exact:true}).click();await expect.poll(()=>started).toBe(true)
 const zone=ws.getByRole('textbox',{name:'Zone',exact:true});await zone.fill('other');await zone.press('Enter');await expect(ws.locator('[data-resync]')).toBeEnabled();release();await page.unrouteAll({behavior:'wait'});expect(writes).toBe(0)
})
