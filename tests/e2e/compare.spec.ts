import {expect,type Page,type Locator} from '@playwright/test'
import {test,connectSelected,scratchRoot} from './fixtures'
import {join} from 'node:path'
import {reconfigure} from './synth'

const drawer=(page:Page,name:string)=>page.getByRole('dialog',{name:'workloads '+name})
async function workloads(page:Page,target='demo'){
 await page.getByRole('option',{name:new RegExp('^'+target+'\\b')}).click()
 await connectSelected(page)
 const nav=page.getByRole('navigation',{name:'resources'})
 await nav.getByRole('button',{name:'Workloads',exact:true}).click()
 const grid=page.getByRole('grid',{name:'resources'})
 await expect(grid.getByRole('gridcell',{name:'web',exact:true})).toBeVisible()
 return grid
}
async function open(grid:Locator,name:string){await grid.getByRole('gridcell',{name,exact:true}).dblclick();await expect(drawer(grid.page(),name).getByRole('button',{name:'Use as comparison baseline',exact:true})).toBeEnabled()}
async function pair(page:Page,expectDiff=true){
 await page.goto('/');const grid=await workloads(page)
 await open(grid,'db');await drawer(page,'db').getByRole('button',{name:'Use as comparison baseline',exact:true}).click()
 await open(grid,'web');await drawer(page,'web').getByRole('button',{name:'Compare with baseline',exact:true}).click()
 const dialog=page.getByRole('dialog',{name:'Compare resources',exact:true})
 if(expectDiff)await expect(dialog.getByRole('region',{name:'Normalized YAML differences'})).toBeVisible()
 return {grid,dialog}
}

test('saved resources compare across admitted targets and pin UID, configuration and connection',async({page})=>{
 const requests:Record<string,unknown>[]=[]
 page.on('request',r=>{if(r.url().endsWith('/api/CompareResources'))requests.push(r.postDataJSON())})
 await page.goto('/');const first=await workloads(page)
 await open(first,'db');await drawer(page,'db').getByRole('button',{name:'Use as comparison baseline',exact:true}).click()
 const second=await workloads(page,'demo2');await open(second,'web');await drawer(page,'web').getByRole('button',{name:'Compare with baseline',exact:true}).click()
 const dialog=page.getByRole('dialog',{name:'Compare resources',exact:true})
 await expect(dialog.getByRole('region',{name:'Normalized YAML differences'})).toBeVisible()
 await expect(dialog).toContainText('demo2')
 await dialog.getByRole('button',{name:'Refresh',exact:true}).click()
 await expect(dialog.getByRole('region',{name:'Normalized YAML differences'})).toBeVisible()
 expect(requests).toHaveLength(2)
 const req=requests[0] as {left:{ref:{target:string;uid:string};configRev:string;connectionId:number};right:{ref:{target:string;uid:string};configRev:string;connectionId:number}}
 expect(req.left.ref.target).toBe('demo');expect(req.right.ref.target).toBe('demo2')
 for(const side of [req.left,req.right]){expect(side.ref.uid).toBeTruthy();expect(side.configRev).toBeTruthy();expect(side.connectionId).toBeGreaterThan(0)}
 await page.screenshot({path:join(scratchRoot,'comparison-cross-target.png')})
 await page.keyboard.press('Escape');await expect(dialog).toHaveCount(0)
 await page.getByRole('button',{name:'Clear baseline',exact:true}).click();await expect(page.locator('[data-comparison-baseline]')).toHaveCount(0)
})

test('comparison, Refresh and Escape preserve YAML drafts and ordinary navigation remains guarded',async({page})=>{
 // The synthetic provider has no edit implementation. Supply only an editor
 // source/capability; actual comparison still reads its saved provider YAML.
 await page.route('**/api/ListKinds',async route=>{const response=await route.fetch();const data=await response.json();for(const kind of data.kinds)if(kind.id==='workloads')kind.editable=true;await route.fulfill({response,json:data})})
 await page.route('**/api/GetEditSource',route=>route.fulfill({json:{ref:route.request().postDataJSON(),text:'name: web\nreplicas: 2\n',base:'read-only-editor-fixture'}}))
 const {dialog,grid}=await pair(page)
 await page.keyboard.press('Escape')
 const detail=drawer(page,'web')
 await detail.getByRole('button',{name:'Edit',exact:true}).click()
 const editor=detail.locator('.cm-content');await editor.click();await page.keyboard.press('Control+End');await page.keyboard.type('\n# comparison draft stays local')
 await detail.getByRole('button',{name:'Compare with baseline',exact:true}).click()
 await expect(dialog).toBeVisible();await expect(dialog).not.toContainText('comparison draft stays local')
 await dialog.getByRole('button',{name:'Refresh',exact:true}).click();await expect(dialog.getByRole('region',{name:'Normalized YAML differences'})).toBeVisible()
 await expect(dialog.getByRole('button',{name:'Refresh',exact:true})).toBeFocused()
 await page.keyboard.press('Shift+Tab');await expect(dialog.locator('[data-comparison-content]')).toBeFocused()
 await page.keyboard.press('Tab');await expect(dialog.getByRole('button',{name:'Refresh',exact:true})).toBeFocused()
 await page.keyboard.press('Escape');await expect(detail.getByRole('button',{name:'Compare with baseline',exact:true})).toBeFocused();await expect(editor).toContainText('# comparison draft stays local')
 await grid.getByRole('gridcell',{name:'db',exact:true}).click()
 const prompt=page.getByRole('alertdialog');await expect(prompt).toBeVisible();await prompt.getByRole('button',{name:'Keep editing',exact:true}).click();await expect(editor).toContainText('# comparison draft stays local')
 await page.screenshot({path:join(scratchRoot,'comparison-draft-guard.png')})
})

test('service fields switch within the same snapshots; Secret exclusions and all eight locales are visible',async({page})=>{
 let calls=0
 await page.route('**/api/CompareResources',route=>{calls++;const req=route.request().postDataJSON();return route.fulfill({json:{left:{ref:req.left.ref,yaml:'spec:\n  replicas: 2\n',fullYAML:'metadata:\n  resourceVersion: "10"\nspec:\n  replicas: 2\n',capturedAt:1,omitted:['metadata.resourceVersion'],valuesExcluded:true},right:{ref:req.right.ref,yaml:'spec:\n  replicas: 2\n',fullYAML:'metadata:\n  resourceVersion: "11"\nspec:\n  replicas: 2\n',capturedAt:2,omitted:['metadata.resourceVersion'],valuesExcluded:false}}})})
 const {dialog}=await pair(page,false)
 await expect(dialog).toContainText('No differences in the compared fields')
 await expect(dialog).toContainText('Secret payloads and size masks are excluded')
 await dialog.getByRole('checkbox',{name:'Show service fields and status'}).check();await expect(dialog).toContainText('resourceVersion:');expect(calls).toBe(1)
 await page.keyboard.press('Escape')
 const names={en:'English',ru:'Русский',zh:'简体中文',es:'Español',de:'Deutsch',fr:'Français',pt:'Português (Brasil)',ja:'日本語'}
 for(const [code,name] of Object.entries(names)){
  await page.locator('.language-menu button').click();await page.getByRole('option',{name,exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang',code)
  await page.locator('.drawer-tools button').filter({hasText:/^(Compare with baseline|Сравнить с базой|与基准比较|Comparar con la base|Mit Basis vergleichen|Comparer avec la base|Comparar com a base|基準と比較)$/}).click()
  const modal=page.locator('[aria-modal="true"]');await expect(modal).toContainText('web');await expect(modal.getByRole('checkbox')).toBeVisible()
  await page.screenshot({path:join(scratchRoot,'comparison-'+code+'.png')});await page.keyboard.press('Escape')
 }
 await page.locator('.language-menu button').click();await page.getByRole('option',{name:'English',exact:true}).click()
})

test('late comparison responses cannot reopen a closed dialog',async({page})=>{
 let release:()=>void=()=>{};const blocked=new Promise<void>(resolve=>{release=resolve})
 await page.route('**/api/CompareResources',async route=>{await blocked;try{const req=route.request().postDataJSON();await route.fulfill({json:{left:{ref:req.left.ref,yaml:'late: old\n',fullYAML:'late: old\n',capturedAt:1,omitted:[],valuesExcluded:false},right:{ref:req.right.ref,yaml:'late: new\n',fullYAML:'late: new\n',capturedAt:1,omitted:[],valuesExcluded:false}}})}catch{/* Closed dialog cancels this request. */}})
 await page.goto('/');const grid=await workloads(page);await open(grid,'db');await drawer(page,'db').getByRole('button',{name:'Use as comparison baseline'}).click();await open(grid,'web');await drawer(page,'web').getByRole('button',{name:'Compare with baseline'}).click()
 const dialog=page.getByRole('dialog',{name:'Compare resources',exact:true});await expect(dialog.getByRole('status')).toBeVisible();await page.keyboard.press('Escape');release();await expect(dialog).toHaveCount(0);await expect(page.locator('body')).not.toContainText('late: old')
})

test('Refresh rejects obsolete replies and failed reads never leave a successful diff',async({page})=>{
 let calls=0,release:()=>void=()=>{};const old=new Promise<void>(resolve=>{release=resolve})
 await page.route('**/api/CompareResources',async route=>{
  const call=++calls
  if(call===1)await old
  try{
   if(call===3){await route.fulfill({status:403,json:{code:'forbidden',detail:'comparison read denied'}});return}
   const req=route.request().postDataJSON();const value=call===1?'obsolete':'current'
   await route.fulfill({json:{left:{ref:req.left.ref,yaml:'snapshot: '+value+'-left\n',fullYAML:'snapshot: '+value+'-left\n',capturedAt:1,omitted:[],valuesExcluded:false},right:{ref:req.right.ref,yaml:'snapshot: '+value+'-right\n',fullYAML:'snapshot: '+value+'-right\n',capturedAt:2,omitted:[],valuesExcluded:false}}})
  }catch{/* Refresh cancels the original call. */}
 })
 try{
  const {dialog}=await pair(page,false);await expect.poll(()=>calls).toBe(1)
  await dialog.getByRole('button',{name:'Refresh',exact:true}).click();await expect(dialog.locator('[data-comparison-content]')).toContainText('current-left')
  release();await expect(dialog).not.toContainText('obsolete')
  await dialog.getByRole('button',{name:'Refresh',exact:true}).click();await expect(dialog.getByRole('alert')).toContainText('comparison read denied');await expect(dialog.getByRole('region',{name:'Normalized YAML differences'})).toHaveCount(0)
 }finally{release()}
})


test('reconfigured connections invalidate snapshots and require an explicit resource selection',async({page})=>{
 const {dialog}=await pair(page)
 await reconfigure(page)
 await expect(dialog.getByRole('alert')).toContainText('Connection closed or changed')
 await expect(dialog.getByRole('region',{name:'Normalized YAML differences'})).toHaveCount(0)
 await expect(dialog.getByRole('button',{name:'Refresh',exact:true})).toBeDisabled()
 await page.keyboard.press('Escape')
 await expect(page.locator('[data-comparison-baseline]')).toContainText('Connection closed or changed')
 await page.getByRole('button',{name:'Clear baseline',exact:true}).click()
})
