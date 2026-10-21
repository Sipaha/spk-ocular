import {expect,type Page} from '@playwright/test'
import {test,scratchRoot,selectConnection,connectSelected,openAppMenu} from './fixtures'
import {english} from './argocd-fixtures'
import {join} from 'node:path'
import {languages,languageNames} from '../../web/src/languages'
const groups:Record<string,string>={GitRepository:'Sources',OCIRepository:'Sources',HelmRepository:'Sources',HelmChart:'Sources',Bucket:'Sources',ExternalArtifact:'Sources',Kustomization:'Workloads',HelmRelease:'Workloads',ImageRepository:'Image automation',ImagePolicy:'Image automation',ImageUpdateAutomation:'Image automation',Alert:'Notifications',Provider:'Notifications',Receiver:'Notifications'}
test.beforeEach(async({page})=>english(page));test.afterEach(async({page})=>english(page))
async function fluxPage(page:Page,name:string){
 const nav=page.getByRole('navigation',{name:'resources'}),region=nav.getByRole('region',{name:'Flux',exact:true}),head=region.getByRole('button',{name:/^Flux \(/})
 if(await head.getAttribute('aria-expanded')!=='true')await head.click()
 if(groups[name]){const sub=region.getByRole('group',{name:groups[name],exact:true}).getByRole('button',{name:new RegExp('^'+groups[name])});if(await sub.getAttribute('aria-expanded')!=='true')await sub.click()}
 await region.getByRole('button',{name,exact:true}).click({position:{x:8,y:14}})
 return page.locator('.flux-workspace')
}
async function openFlux(page:Page,name='Dashboard'){
 await page.goto('/');await selectConnection(page,/^demo\b/);await connectSelected(page)
 const ws=await fluxPage(page,name),scope=ws.getByRole('textbox',{name:'Zone',exact:true});await scope.fill('blue');await scope.press('Enter');await expect(ws.locator('[data-resync]')).toBeEnabled();return ws
}
test('Flux dashboard shows controller states, attention and related warning activity',async({page})=>{
 const ws=await openFlux(page)
 await expect(ws.getByRole('status')).toContainText('10 / 14')
 const attention=ws.getByLabel('Needs attention',{exact:true});await expect(attention).toContainText('delivery');await expect(attention).toContainText('Suspended')
 await ws.getByRole('button',{name:'Reconciliation',exact:true}).click();await page.getByRole('option',{name:'Failed',exact:true}).click();await expect(attention).toContainText('delivery');await expect(attention).not.toContainText('web-release')
 const activity=ws.getByLabel('Activity',{exact:true});await expect(activity).toContainText('HealthCheckFailed');await activity.getByRole('textbox').fill('absent');await expect(activity).not.toContainText('HealthCheckFailed');await activity.getByRole('textbox').fill('delivery')
 await page.screenshot({path:join(scratchRoot,'flux-dashboard-en-1440.png')})
 await attention.getByRole('button',{name:/Kustomization \/ blue\/delivery/}).click();await expect(ws.getByLabel('Flux details',{exact:true})).toContainText('HealthCheckFailed')
})
test('every served Flux kind has a bounded typed list and source/dependent navigation',async({page})=>{
 const ws=await openFlux(page,'Kustomization')
 for(const kind of Object.keys(groups)){await fluxPage(page,kind);await expect(ws.getByRole('grid',{name:'Flux resources'})).toHaveAttribute('aria-rowcount','1');await expect(ws.getByRole('columnheader')).toHaveCount(6)}
 await fluxPage(page,'Kustomization');await ws.getByRole('grid').locator('[data-row-id]').click();const detail=ws.getByLabel('Flux details',{exact:true})
 await detail.getByRole('button',{name:'GitRepository / blue/gitrepository',exact:true}).click();await expect(detail).toContainText('GitRepository / gitrepository');await expect(detail.getByRole('link',{name:/main@sha1:/})).toHaveAttribute('href','https://github.com/example/commerce/commit/0123456789abcdef0123456789abcdef01234567')
 await detail.getByRole('button',{name:'Kustomization / blue/delivery',exact:true}).click();await expect(detail).toContainText('Kustomization / delivery')
 await detail.getByRole('button',{name:'Crate / blue/crate-7f3a',exact:true}).click();await expect(ws.locator('[data-resource-drawer]').getByRole('heading',{name:'alpha',exact:true})).toBeVisible()
 await page.screenshot({path:join(scratchRoot,'flux-resource-source-inventory.png')})
})
test('navigation requires detected APIs and retains generic Flux resources beside dedicated pages',async({page})=>{
 await page.route('**/api/ListKinds',async route=>{const response=await route.fetch(),value=await response.json();for(const k of value.kinds)if(k.id.includes('.toolkit.fluxcd.io/'))k.hidden=false;value.kinds.push({id:'argoproj.io/workflows',title:'Workflows',singular:'Workflow',group:'API groups',subgroup:'argoproj.io',scoped:true,columns:[]});await route.fulfill({response,json:value})})
 await openFlux(page);const nav=page.getByRole('navigation',{name:'resources'})
 const custom=nav.getByRole('region',{name:'Custom Resources',exact:true});await custom.getByRole('button',{name:/^Custom Resources \(/}).click();await expect(custom.getByRole('button',{name:'Workflows',exact:true})).toBeVisible();const sources=custom.getByRole('group',{name:'source.toolkit.fluxcd.io',exact:true});await sources.getByRole('button',{name:/^source\.toolkit\.fluxcd\.io/}).click();await expect(sources.getByRole('button',{name:'GitRepository',exact:true})).toBeVisible()
 await page.unrouteAll({behavior:'wait'});await page.route('**/api/ListKinds',async route=>{const response=await route.fetch(),value=await response.json();value.kinds=value.kinds.filter((k:{workspace?:string;id:string})=>!k.workspace?.startsWith('fluxcd')&&!k.workspace?.startsWith('argocd')&&!k.id.includes('.toolkit.fluxcd.io/')&&!k.id.startsWith('argoproj.io/'));await route.fulfill({response,json:value})})
 await page.reload();await expect(nav.getByRole('region',{name:'Flux',exact:true})).toHaveCount(0);await expect(nav.getByRole('region',{name:'Argo CD',exact:true})).toHaveCount(0)
})
test('Flux errors retain observations, explicit empty scope stays empty, and cancellation ignores late answers',async({page})=>{
 let fail=false,slow=false,started=false,release=()=>{};const gate=new Promise<void>(r=>{release=r})
 await page.route('**/api/FluxCD',async route=>{if(fail){await route.fulfill({status:400,json:{code:'forbidden',detail:'Fixture denied'}});return}const response=await route.fetch();if(slow){started=true;await gate}await route.fulfill({response})})
 const ws=await openFlux(page,'Kustomization');fail=true;await ws.locator('[data-resync]').click();await expect(ws.getByRole('alert')).toContainText('Fixture denied');await expect(ws.getByRole('grid')).toHaveAttribute('aria-rowcount','1')
 fail=false;slow=true;await ws.locator('[data-resync]').click();await expect.poll(()=>started).toBe(true);await ws.getByRole('button',{name:'Cancel',exact:true}).click();release();await expect(ws.getByRole('grid')).toBeHidden();await expect(ws.getByRole('button',{name:/Retry/})).toBeVisible()
 slow=false;const zone=ws.getByRole('textbox',{name:'Zone',exact:true});await zone.fill('outside');await zone.press('Enter');await expect(ws.getByRole('grid')).toHaveAttribute('aria-rowcount','0')
})
test('Flux labels do not prove a manager; loaded inventory links the exact current resource back',async({page})=>{
 let reads=0;page.on('request',r=>{if(r.url().endsWith('/api/FluxCD'))reads++})
 await page.route('**/api/GetResource',async route=>{const response=await route.fetch(),value=await response.json();if(value.ref.kind==='crates')value.fluxTracking={group:'',kind:'Crate',candidates:[{kind:'Kustomization',namespace:'blue',name:'delivery'}]};await route.fulfill({response,json:value})})
 const ws=await openFlux(page,'Kustomization');await ws.getByRole('grid').locator('[data-row-id]').click();await ws.getByLabel('Flux details',{exact:true}).getByRole('button',{name:'Crate / blue/crate-7f3a',exact:true}).click();const drawer=ws.locator('[data-resource-drawer]');await expect(drawer.getByRole('heading',{name:'alpha',exact:true})).toBeVisible();const before=reads
 await expect(drawer.getByLabel('Flux tracking',{exact:true}).getByRole('button',{name:'Kustomization / blue/delivery',exact:true})).toBeVisible();expect(reads).toBe(before)
 await drawer.getByLabel('Flux tracking',{exact:true}).getByRole('button',{name:'Kustomization / blue/delivery',exact:true}).click();await expect(ws.getByLabel('Flux details',{exact:true})).toContainText('Kustomization / delivery')
})
test('Flux dashboard and details fit eight languages at wide and narrow widths',async({page})=>{
 const ws=await openFlux(page)
 const language=async(code:typeof languages[number])=>{await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[code],exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang',code)}
 for(const code of languages){for(const view of ['Dashboard','Kustomization']){
  await page.setViewportSize({width:1440,height:900});await language('en');await fluxPage(page,view);if(view!=='Dashboard')await ws.getByRole('grid').locator('[data-row-id]').click();await language(code);if(code==='ru')await expect(page.getByRole('navigation',{name:'resources'}).getByRole('region',{name:'Flux',exact:true}).getByRole('group',{name:'Источники',exact:true})).toBeVisible()
  for(const width of [1440,375]){await page.setViewportSize({width,height:900});await expect(ws).toBeVisible();expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);expect(await ws.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true);if(code==='en'||code==='ru')await page.screenshot({path:join(scratchRoot,`flux-${view.toLowerCase()}-${code}-${width}.png`)})}
 }}
})

test('a late Flux inventory lookup cannot discard an editor acquired while resolving the UID',async({page})=>{
 let started=false,release=()=>{};const gate=new Promise<void>(r=>{release=r})
 await page.route('**/api/FluxCD',async route=>{const response=await route.fetch(),value=await response.json();const k=value.objects.find((o:{kind:string})=>o.kind==='Kustomization');k.inventory.push({...k.inventory[0],name:'crate-91c2'});await route.fulfill({response,json:value})})
 await page.route('**/api/ListKinds',async route=>{const response=await route.fetch(),value=await response.json();for(const k of value.kinds)if(k.id==='crates')k.editable=true;await route.fulfill({response,json:value})})
 await page.route('**/api/GetResource',async route=>{const ref=route.request().postDataJSON(),response=await route.fetch();if(ref.kind==='crates'&&ref.name==='crate-91c2'&&!ref.uid){started=true;await gate}await route.fulfill({response})})
 await page.route('**/api/GetEditSource',route=>route.fulfill({json:{ref:route.request().postDataJSON(),text:'name: alpha\n',base:'flux-draft-fixture'}}))
 const ws=await openFlux(page,'Kustomization');await ws.getByRole('grid').locator('[data-row-id]').click();const detail=ws.getByLabel('Flux details',{exact:true})
 await detail.getByRole('button',{name:'Crate / blue/crate-7f3a',exact:true}).click();const drawer=ws.locator('[data-resource-drawer]');await expect(drawer.getByRole('heading',{name:'alpha',exact:true})).toBeVisible()
 await detail.getByRole('button',{name:'Crate / blue/crate-91c2',exact:true}).click();await expect.poll(()=>started).toBe(true)
 await drawer.getByRole('tab',{name:'YAML',exact:true}).click();await drawer.getByRole('button',{name:'Edit',exact:true}).click();await drawer.locator('.cm-content').click();await page.keyboard.press('Control+End');await page.keyboard.type('\n# retained Flux lookup draft')
 release();const prompt=page.getByRole('alertdialog',{name:'Discard edits?',exact:true});await expect(prompt).toBeVisible();await prompt.getByRole('button',{name:'Keep editing',exact:true}).click();await expect(drawer.getByRole('heading',{name:'alpha',exact:true})).toBeVisible();await expect(drawer.locator('.cm-content')).toContainText('retained Flux lookup draft');await page.unrouteAll({behavior:'wait'})
})
