import {expect,type Page} from '@playwright/test'
import {test,selectConnection,connectSelected,openAppMenu,scratchRoot} from './fixtures'
import {english} from './argocd-fixtures'
import {languages,languageNames} from '../../web/src/languages'
import {join} from 'node:path'
import type {Diagnosis} from '../../web/src/diagnosis/types'
import type {Ref} from '../../web/src/api/types'

test.beforeEach(async({page})=>english(page));test.afterEach(async({page})=>english(page))
function evidence(ref:Ref):Diagnosis {
 return {ref,capturedAt:Date.UTC(2026,9,21,10),truncated:true,coverage:[{source:'secrets/credential',state:'keys_unchecked'},{source:'configmaps/settings',state:'forbidden'}],evidence:[
 {ref,source:ref,rule:'health',field:'status',state:'warning',reason:'Pending',message:'Waiting for first consumer'},
 {ref,source:ref,rule:'container',field:'status.containerStatuses[0].lastState.terminated',state:'unknown',reason:'Error',message:'exitCode=137',container:'main',previous:true},
 {ref,source:{...ref,kind:'events',name:'probe',uid:'event-uid'},rule:'event',field:'message',state:'unknown',reason:'Unhealthy',message:'Warning; readiness probe failed'},
 ]}
}
async function open(page:Page){
 await page.route('**/api/GetResource',async route=>{const response=await route.fetch(),value=await response.json();value.diagnosisAvailable=true;await route.fulfill({response,json:value})})
 await page.goto('/');await selectConnection(page,/^demo\b/);await connectSelected(page)
 await page.getByRole('navigation',{name:'resources'}).getByRole('button',{name:'Problems',exact:true}).click()
 await page.getByRole('grid',{name:'resources'}).getByRole('gridcell',{name:'api',exact:true}).click()
 const drawer=page.locator('[data-resource-drawer]');await expect(drawer.getByLabel('Diagnosis',{exact:true})).toBeVisible();return drawer
}
test('explicit diagnosis retains separate observations, coverage and guarded resource navigation',async({page})=>{
 let reads=0
 await page.route('**/api/DiagnoseResource',route=>{reads++;return route.fulfill({json:evidence(route.request().postDataJSON())})})
 const drawer=await open(page),panel=drawer.getByLabel('Diagnosis',{exact:true});expect(reads).toBe(0)
 await panel.getByRole('button',{name:'Collect evidence'}).click();await expect(panel).toContainText('exitCode=137');expect(reads).toBe(1)
 await expect(panel).not.toContainText('OOMKilled');await expect(panel).toContainText('readiness probe failed')
 await panel.getByText('Read coverage',{exact:true}).click();await expect(panel).toContainText('Object keys were not checked');await expect(panel).toContainText('access denied')
 await page.screenshot({path:join(scratchRoot,'diagnosis-en-wide.png')})
 // Resource navigation goes through the inspector's existing dirty-edit guard.
 await page.route('**/api/GetResource',async route=>{const ref=route.request().postDataJSON();if(ref.kind==='events')await route.fulfill({json:{ref,health:{state:'ok'},facts:[],yaml:'kind: Event\n',relations:[]}});else await route.fallback()})
 await panel.getByRole('button',{name:'events/probe'}).click();await expect(drawer.getByRole('heading',{name:'probe',exact:true})).toBeVisible()
})
test('cancelled diagnosis ignores a late reply and refresh failure retains prior evidence',async({page})=>{
 let slow=false,fail=false,started=false,release=()=>{};const gate=new Promise<void>(r=>{release=r})
 await page.route('**/api/DiagnoseResource',async route=>{if(slow){started=true;await gate}await route.fulfill({json:fail?{code:'forbidden',detail:'Fixture denied'}:evidence(route.request().postDataJSON()),status:fail?400:200})})
 const panel=(await open(page)).getByLabel('Diagnosis',{exact:true})
 await panel.getByRole('button',{name:'Collect evidence'}).click();await expect(panel).toContainText('exitCode=137')
 fail=true;await panel.getByRole('button',{name:'Collect evidence'}).click();await expect(panel.getByRole('alert')).toContainText('Fixture denied');await expect(panel).toContainText('exitCode=137')
 fail=false;slow=true;await panel.getByRole('button',{name:'Collect evidence'}).click();await expect.poll(()=>started).toBe(true);await panel.getByRole('button',{name:'Cancel',exact:true}).click();release()
 await expect(panel).not.toContainText('exitCode=137');await expect(panel).toContainText('Request cancelled')
 await page.unrouteAll({behavior:'wait'})
})
test('diagnosis fits eight languages at wide and narrow widths',async({page})=>{
 await page.route('**/api/DiagnoseResource',route=>route.fulfill({json:evidence(route.request().postDataJSON())}))
 const drawer=await open(page);await drawer.getByRole('button',{name:'Collect evidence'}).click();await expect(drawer).toContainText('exitCode=137')
 for(const code of languages){
  await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[code],exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang',code)
  for(const width of [1440,860]){await page.setViewportSize({width,height:900});expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);expect(await drawer.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true);await page.screenshot({path:join(scratchRoot,`diagnosis-${code}-${width}.png`)})}
 }
})

test('diagnosis navigation preserves a draft acquired after evidence was collected',async({page})=>{
 await page.route('**/api/DiagnoseResource',route=>route.fulfill({json:evidence(route.request().postDataJSON())}))
 await page.route('**/api/ListKinds',async route=>{const response=await route.fetch(),value=await response.json();for(const kind of value.kinds)if(kind.id==='services')kind.editable=true;await route.fulfill({response,json:value})})
 await page.route('**/api/GetEditSource',route=>route.fulfill({json:{ref:route.request().postDataJSON(),text:'name: api\n',base:'diagnosis-draft-fixture'}}))
 const drawer=await open(page);await drawer.getByRole('button',{name:'Collect evidence'}).click();await expect(drawer).toContainText('exitCode=137')
 await drawer.getByRole('tab',{name:'YAML',exact:true}).click();await drawer.getByRole('button',{name:'Edit',exact:true}).click();await drawer.locator('.cm-content').click();await page.keyboard.press('Control+End');await page.keyboard.type('\n# retained diagnosis draft')
 // Returning to Details is guarded too; keep the draft mounted while opening
 // the explicit evidence action from the retained inspector component.
 await drawer.getByRole('tab',{name:'Details',exact:true}).click()
 const prompt=page.getByRole('alertdialog',{name:'Discard edits?',exact:true});await expect(prompt).toBeVisible();await prompt.getByRole('button',{name:'Keep editing',exact:true}).click();await expect(drawer.locator('.cm-content')).toContainText('retained diagnosis draft')
 await expect(drawer.getByRole('heading',{name:'api',exact:true})).toBeVisible()
})
test('previous-container evidence opens a distinct admitted log query',async({page})=>{
 let requested:{channel?:string;previous?:boolean}|undefined
 await page.route('**/api/DiagnoseResource',route=>route.fulfill({json:evidence(route.request().postDataJSON())}))
 await page.route('**/api/LogInfo',route=>route.fulfill({json:{channels:[{id:'main',title:'main'}],defaultChannel:'main',aggregate:false,previous:true}}))
 await page.route('**/api/OpenLogStream',route=>{requested=route.request().postDataJSON().query;return route.fulfill({status:400,json:{code:'unsupported',detail:'Fixture stream boundary'}})})
 const drawer=await open(page);await drawer.getByRole('button',{name:'Collect evidence'}).click();await drawer.getByRole('button',{name:'Previous logs',exact:true}).click()
 await expect.poll(()=>requested).toMatchObject({channel:'main',previous:true})
})
