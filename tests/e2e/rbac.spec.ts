import {expect} from '@playwright/test'
import {test,connectSelected,scratchRoot} from './fixtures'
import {join} from 'node:path'
import type {Page} from '@playwright/test'

async function rbacPage(page:Page){
 await page.goto('/')
 await page.getByRole('option',{name:/^demo\b/}).click();await connectSelected(page)
 const nav=page.getByRole('navigation',{name:'resources'})
 await nav.getByRole('textbox',{name:'Filter resources'}).fill('RBAC explanation')
 await nav.getByRole('button',{name:'RBAC explanation',exact:true}).click()
 await nav.getByRole('textbox',{name:'Filter resources'}).fill('')
 const rbac=page.getByRole('region',{name:'RBAC explanation'})
 const zone=rbac.getByRole('textbox',{name:'Zone',exact:true});await zone.fill('blue');await zone.press('Enter')
 await expect(rbac.getByRole('button',{name:'Refresh',exact:true})).toBeEnabled()
 return rbac
}

test('RBAC provenance, selected accounts and self-only server checks distinguish declarations from decisions',async({page})=>{
 const requests:Record<string,unknown>[]=[]
 page.on('request',r=>{if(r.url().endsWith('/api/CheckAccess'))requests.push(r.postDataJSON())})
 const rbac=await rbacPage(page)
 await expect(rbac).toContainText('demo-operator')
 await expect(rbac).toContainText('RoleBinding/pod-reader')
 await expect(rbac).toContainText('Named resources only: settings')
 await rbac.getByRole('button',{name:'Find declared grant',exact:true}).click()
 await expect(rbac).toContainText('1 matching bindings observed')
 await rbac.getByRole('button',{name:'Check my access with the server',exact:true}).click()
 await expect(rbac).toContainText('Server decision: allowed')
 await rbac.getByRole('button',{name:'Verb',exact:true}).click();await page.getByRole('option',{name:'delete',exact:true}).click()
 await expect(rbac.getByText('Server decision: allowed',{exact:true})).toHaveCount(0)
 await rbac.getByRole('button',{name:'Find declared grant',exact:true}).click()
 await expect(rbac).toContainText('No matching grant observed in this snapshot')
 await rbac.getByRole('button',{name:'Check my access with the server',exact:true}).click()
 await expect(rbac).toContainText('Server decision: not allowed')
 await rbac.getByRole('button',{name:'Inspect permissions for',exact:true}).click();await page.getByRole('option',{name:'blue/alpha',exact:true}).click()
 await expect(rbac).toContainText('system:serviceaccount:blue:crate-7f3a')
 await rbac.getByRole('button',{name:'Check my access with the server',exact:true}).click()
 await expect(rbac).toContainText('Server decision: not allowed')
 expect(requests).toHaveLength(3)
 for(const req of requests){expect(req).not.toHaveProperty('subject');expect(req).not.toHaveProperty('user');expect(req).not.toHaveProperty('groups')}
 await page.screenshot({path:join(scratchRoot,'rbac-account-self-check.png')})
})

test('RBAC incomplete sources and 12000 rule rows stay bounded and fully localized',async({page})=>{
 const ref={provider:'synthetic',target:'demo',kind:'',name:'reader',scope:'blue',uid:'role'}
 await page.route('**/api/RBACSnapshot',route=>route.fulfill({json:{principal:'operator',groups:['team:dev'],accounts:[],grants:[{binding:{...ref,uid:'binding'},bindingKind:'RoleBinding',role:ref,roleKind:'ClusterRole',subjectKind:'Group',subjectName:'team:dev',namespace:'blue',clusterWide:false,rules:Array.from({length:12000},(_,i)=>({verbs:['get'],apiGroups:[''],resources:['pods'],resourceNames:['pod-'+i],nonResourceURLs:[]})),error:'limit',aggregated:true}],problems:[{kind:'clusterrolebindings',class:'forbidden'}],truncated:true,discovery:'partial',capturedAt:Date.now()}}))
 const rbac=await rbacPage(page)
 await expect(rbac).toContainText('RBAC limit reached')
 await expect(rbac).toContainText('Role could not be fully read (limit)')
 expect(await rbac.locator('[data-rbac-grant]').count()).toBeLessThan(35)
 await rbac.getByRole('button',{name:'Find declared grant',exact:true}).click()
 await expect(rbac).toContainText('No matching grant observed in this snapshot')
 await rbac.locator('footer summary').click();await expect(rbac).toContainText('clusterrolebindings · forbidden')
 const names={en:'English',ru:'Русский',zh:'简体中文',es:'Español',de:'Deutsch',fr:'Français',pt:'Português (Brasil)',ja:'日本語'}
 for(const [code,name] of Object.entries(names)){
  await page.locator('.language-menu button').click();await page.getByRole('option',{name,exact:true}).click()
  await expect(page.locator('html')).toHaveAttribute('lang',code)
  await expect(page.locator('[data-rbac-workbench]')).toContainText('operator')
  await page.screenshot({path:join(scratchRoot,'rbac-language-'+code+'.png')})
 }
 await page.locator('.language-menu button').click();await page.getByRole('option',{name:'English',exact:true}).click()
 await page.unrouteAll({behavior:'wait'})
})

test('RBAC access response cannot survive changing the requested resource',async({page})=>{
 let started:()=>void=()=>{};const pending=new Promise<void>(resolve=>{started=resolve})
 let release:()=>void=()=>{};const ready=new Promise<void>(resolve=>{release=resolve})
 await page.route('**/api/CheckAccess',async route=>{started();await ready;try{await route.fulfill({json:{attributes:route.request().postDataJSON().attributes,state:'allowed',reason:'late reply',evaluationError:'',checkedAt:Date.now()}})}catch{/* The edited form aborts its obsolete request. */}})
 const rbac=await rbacPage(page)
 await rbac.getByRole('button',{name:'Check my access with the server',exact:true}).click();await pending
 await rbac.getByRole('textbox',{name:'API resource (plural)',exact:true}).fill('secrets');release()
 await expect(rbac.getByRole('button',{name:'Check my access with the server',exact:true})).toBeEnabled()
 await expect(rbac.getByText('Server decision: allowed',{exact:true})).toHaveCount(0)
 await expect(rbac).not.toContainText('late reply')
 await page.unrouteAll({behavior:'wait'})
})

test('ServiceAccount permission dialog and Forbidden resource checks use standard guarded properties',async({page})=>{
 const rbac=await rbacPage(page)
 await page.getByRole('navigation',{name:'resources'}).getByRole('button',{name:'Crates',exact:true}).click()
 await page.route('**/api/GetResource',async route=>{
  const req=route.request().postDataJSON()
  if(req.name==='crate-7f3a'){
   const response=await route.fetch(),resource=await response.json()
   await route.fulfill({json:{...resource,rbacSubject:resource.ref}})
  }else await route.fulfill({status:403,json:{code:'forbidden',detail:'read forbidden'}})
 })
 const grid=page.getByRole('grid',{name:'resources'})
 await grid.getByRole('gridcell',{name:'alpha',exact:true}).dblclick()
 const details=page.locator('[data-area=details]')
 await details.getByRole('button',{name:'Permissions',exact:true}).click()
 const dialog=page.getByRole('dialog',{name:'RBAC explanation',exact:true})
 await expect(dialog).toContainText('system:serviceaccount:blue:crate-7f3a')
 await dialog.focus();await page.keyboard.press('Tab');await expect(dialog.getByRole('button',{name:'Close',exact:true})).toBeFocused()
 await page.keyboard.press('Shift+Tab');await expect(dialog.locator('[data-area=table]')).toBeFocused()
 await page.keyboard.press('Tab');await expect(dialog.getByRole('button',{name:'Close',exact:true})).toBeFocused()
 await dialog.press('Escape');await expect(dialog).toHaveCount(0)
 await expect(details.getByRole('button',{name:'Permissions',exact:true})).toBeFocused()
 await details.getByRole('button',{name:'Close',exact:true}).click()
 await grid.getByRole('gridcell',{name:'beta',exact:true}).dblclick()
 await expect(details).toContainText('read forbidden')
 // Synthetic providers deliberately do not offer a Kubernetes Forbidden explanation.
 await expect(details.getByRole('button',{name:'Check this Forbidden',exact:true})).toHaveCount(0)
 await page.screenshot({path:join(scratchRoot,'rbac-permissions-properties.png')})
 await page.unrouteAll({behavior:'wait'})
 await expect(rbac).toHaveCount(0)
})
