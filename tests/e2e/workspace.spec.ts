import {expect,type Page} from '@playwright/test'
import {test,connectSelected,scratchRoot} from './fixtures'
import {join} from 'node:path'
async function web(page:Page){
 await page.goto('/');await page.getByRole('option',{name:/^demo\b/}).click();await connectSelected(page)
 await page.getByRole('navigation',{name:'resources'}).getByRole('button',{name:'Workloads',exact:true}).click()
 await page.getByRole('grid',{name:'resources'}).getByRole('gridcell',{name:'web',exact:true}).click()
 return page.locator('[data-area=details]')
}
const shelf=(page:Page)=>page.locator('[data-area=dock]')

test('pinning read-only properties and Escape preserve a main YAML draft and all eight languages',async({page})=>{
 await page.route('**/api/ListKinds',async route=>{const response=await route.fetch();const data=await response.json();for(const kind of data.kinds)if(kind.id==='workloads')kind.editable=true;await route.fulfill({response,json:data})})
 await page.route('**/api/GetEditSource',route=>route.fulfill({json:{ref:route.request().postDataJSON(),text:'name: web\nreplicas: 2\n',base:'read-only-editor-fixture'}}))
 const details=await web(page);await details.getByRole('button',{name:'Edit',exact:true}).click()
 const editor=details.locator('.cm-content');await editor.click();await page.keyboard.press('Control+End');await page.keyboard.type('\n# PRIVATE_UNSAVED_DRAFT')
 await details.getByRole('button',{name:'Pin properties',exact:true}).click()
 const pinned=shelf(page).locator('[data-resource-drawer]');await expect(pinned).toBeVisible();await expect(pinned.getByRole('button',{name:'Edit',exact:true})).toHaveCount(0);await expect(pinned).not.toContainText('PRIVATE_UNSAVED_DRAFT')
 await pinned.getByRole('tab',{name:'YAML',exact:true}).click();await expect(page.getByRole('alertdialog')).toHaveCount(0);await pinned.locator('.cm-content').click();await page.keyboard.press('Escape');await expect(pinned).toHaveCount(0);await expect(editor).toContainText('PRIVATE_UNSAVED_DRAFT')
 await details.getByRole('button',{name:'Pin properties',exact:true}).click();await expect(pinned).toBeVisible()
 const names={en:'English',ru:'Русский',zh:'简体中文',es:'Español',de:'Deutsch',fr:'Français',pt:'Português (Brasil)',ja:'日本語'}
 for(const [code,name] of Object.entries(names)){
  await page.locator('.language-menu button').click();await page.getByRole('option',{name,exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang',code);await expect(pinned).toContainText('web');await expect(shelf(page).locator('[aria-pressed=true]')).toHaveCount(1)
  await page.screenshot({path:join(scratchRoot,'workspace-'+code+'.png')})
 }
 await page.locator('.language-menu button').click();await page.getByRole('option',{name:'English',exact:true}).click()
 await shelf(page).getByRole('button',{name:'Close tab',exact:true}).click();await expect(editor).toContainText('PRIVATE_UNSAVED_DRAFT')
})

test('load and save failures preserve state and offer explicit retry/reset',async({page})=>{
 let fail=true
 await page.route('**/api/GetTargetState',async route=>{const req=route.request().postDataJSON();if(req.provider==='ocular'&&fail){await route.fulfill({status:500,json:{code:'internal',detail:'workspace fixture read failure'}})}else await route.continue()})
 await page.goto('/');await expect(page.locator('[data-workspace-error]')).toContainText('Saving is paused')
 fail=false;await page.getByRole('button',{name:'Retry workspace saving/loading',exact:true}).click();await expect(page.locator('[data-workspace-error]')).toHaveCount(0)
 await page.route('**/api/SetTargetState',async route=>{const req=route.request().postDataJSON();if(req.key==='workspacePanels')await route.fulfill({status:500,json:{code:'internal',detail:'workspace fixture save failure'}});else await route.continue()})
 const details=await web(page);await details.getByRole('button',{name:'Pin properties',exact:true}).click();await expect(page.locator('[data-workspace-error]')).toContainText('could not be saved');await expect(shelf(page).locator('[data-resource-drawer]')).toBeVisible()
 await page.unroute('**/api/SetTargetState');await page.getByRole('button',{name:'Retry workspace saving/loading',exact:true}).click();await expect(page.locator('[data-workspace-error]')).toHaveCount(0)
})

test('saved pin limit is visible and refused additions preserve all existing panels',async({page})=>{
 const details=await web(page);await details.getByRole('button',{name:'Pin properties',exact:true}).click()
 const token=await page.locator('meta[name="spk-ocular-api-token"]').getAttribute('content'),origin=new URL(page.url()).origin
 const headers={Authorization:'Bearer '+token,Origin:origin}
 await expect.poll(async()=>{const r=await page.request.post('/api/GetTargetState',{headers,data:{provider:'ocular',target:'workspace'}});return !!(await r.json()).workspacePanels}).toBe(true)
 const response=await page.request.post('/api/GetTargetState',{headers,data:{provider:'ocular',target:'workspace'}}),saved=JSON.parse((await response.json()).workspacePanels)
 saved.panels=Array.from({length:8},(_,i)=>({kind:'properties',ref:{provider:'synthetic',target:'demo',kind:'workloads',name:'old-'+i,uid:'old-uid-'+i},tab:'details'}));saved.active=0
 const r=await page.request.post('/api/SetTargetState',{headers,data:{provider:'ocular',target:'workspace',key:'workspacePanels',value:JSON.stringify(saved)}});expect(r.ok()).toBe(true)
 await page.reload();await expect(shelf(page).locator('[data-tab-kind=properties]')).toHaveCount(8);await page.getByRole('grid',{name:'resources'}).getByRole('gridcell',{name:'web',exact:true}).click()
 await page.locator('[data-area=details]').getByRole('button',{name:'Pin properties',exact:true}).click();await expect(page.getByRole('status')).toContainText('at most 8 panels within 4096 bytes');await expect(shelf(page).locator('[data-tab-kind=properties]')).toHaveCount(8)
 await shelf(page).locator('[data-tab-kind=properties]').first().getByRole('button',{name:'Keep after restart',exact:true}).click();await page.locator('[data-area=details]').getByRole('button',{name:'Pin properties',exact:true}).click();await expect(shelf(page).locator('[data-tab-kind=properties]')).toHaveCount(9);await expect(shelf(page).locator('[aria-pressed=true]')).toHaveCount(8)
})
