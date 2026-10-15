import { selectConnection, openAppMenu } from './fixtures'
import {expect,type Page} from '@playwright/test'
import {test,connectSelected,scratchRoot} from './fixtures'
import {join} from 'node:path'
import {languages,languageNames} from '../../web/src/languages'

async function openSecurity(page:Page) {
 await page.goto('/')
 await selectConnection(page,/^demo\b/);await connectSelected(page)
 const nav=page.getByRole('navigation',{name:'resources'})
 await nav.getByRole('textbox',{name:'Filter resources'}).fill('Security')
 await nav.getByRole('button',{name:'Security',exact:true}).click()
 await nav.getByRole('textbox',{name:'Filter resources'}).fill('')
 const workspace=page.locator('.security-workspace')
 const zone=workspace.getByRole('textbox',{name:'Zone',exact:true});await zone.fill('blue');await zone.press('Enter')
 await expect(workspace.getByRole('button',{name:'Check again',exact:true})).toBeEnabled()
 return workspace
}

test('security configuration, missing scanner guidance, filters and guarded resource navigation',async({page})=>{
 await page.route('**/api/ListKinds',async route=>{const response=await route.fetch();const data=await response.json();for(const kind of data.kinds)if(kind.id==='crates')kind.editable=true;await route.fulfill({response,json:data})})
 await page.route('**/api/GetEditSource',route=>route.fulfill({json:{ref:route.request().postDataJSON(),text:'name: alpha\n',base:'security-editor-fixture'}}))
 const workspace=await openSecurity(page)
 await expect(workspace.locator('[data-security-cve-status]')).toContainText('Install Trivy Operator')
 await expect(workspace.getByRole('link',{name:'Trivy Operator installation'})).toHaveAttribute('href','https://aquasecurity.github.io/trivy-operator/latest/getting-started/installation/helm/')
 const table=workspace.getByRole('grid',{name:'Finding'})
 await expect(table.locator('[data-row-id]')).toHaveCount(5)
 const search=workspace.getByRole('textbox',{name:'Find workload, rule, image or CVE'})
 await search.fill('alpha');await expect(table.locator('[data-row-id]')).toHaveCount(2)
 await table.getByRole('button',{name:/Explain finding · root/}).click()
 await expect(workspace.getByLabel('Explain finding',{exact:true})).toContainText('runAsNonRoot: true')
 await table.getByRole('button',{name:/Open resource · Crate/}).first().focus();await page.keyboard.press('Enter')
 const drawer=workspace.locator('[data-resource-drawer]');await expect(drawer.getByRole('heading',{name:'alpha',exact:true})).toBeVisible()
 await drawer.getByRole('tab',{name:'YAML',exact:true}).click();await drawer.getByRole('button',{name:'Edit',exact:true}).click()
 await drawer.locator('.cm-content').click();await page.keyboard.press('Control+End');await page.keyboard.type('\n# held security draft')
 await search.fill('beta')
 await table.getByRole('button',{name:/Open resource · Crate/}).first().click()
 const confirm=page.getByRole('alertdialog',{name:'Discard edits?',exact:true});await expect(confirm).toBeVisible()
 await confirm.getByRole('button',{name:'Keep editing',exact:true}).click()
 await expect(drawer.locator('.cm-content')).toContainText('held security draft')
 await drawer.locator('[data-drawer-close]').click()
 await expect(confirm).toBeVisible()
 await confirm.getByRole('button',{name:'Keep editing',exact:true}).click()
 await expect(drawer.locator('.cm-content')).toContainText('held security draft')
 const close=drawer.locator('[data-drawer-close]')
 const box=(await close.boundingBox())!,svg=(await close.locator('svg').boundingBox())!
 expect(box.width).toBe(28);expect(box.height).toBe(28);expect(svg.width).toBe(20);expect(svg.height).toBe(20)
 expect(Math.abs(svg.x+svg.width/2-box.x-box.width/2)).toBeLessThanOrEqual(0.5)
 expect(Math.abs(svg.y+svg.height/2-box.y-box.height/2)).toBeLessThanOrEqual(0.5)
 await workspace.getByRole('button',{name:'Check again',exact:true}).click();await expect(workspace.getByRole('button',{name:'Check again',exact:true})).toBeEnabled()
 await expect(drawer.locator('.cm-content')).toContainText('held security draft')
 await page.screenshot({path:join(scratchRoot,'security-held-draft.png')})
})

const ref={provider:'synthetic',target:'demo',kind:'crates',name:'crate-7f3a',title:'alpha',scope:'blue',uid:'crate-7f3a'}
function snapshot(cveState='available') {return {findings:[{id:'report/cve',ref,kindTitle:'VulnerabilityReport',category:'cve',severity:'high',rule:'CVE-TEST-1',title:'Example library vulnerability',evidence:'VulnerabilityReport',workload:'ReplicaSet/api',container:'main',package:'libexample',installed:'1.0',fixed:'1.1',image:'app:v1@sha256:example',scanner:'Trivy 0.69.3',scannedAt:Date.now()-86400000}],problems:[],discovery:'ready',truncated:false,capturedAt:Date.now(),resources:1,reports:1,cveState}}

test('security CVE evidence, source date, access states and error recovery',async({page})=>{
 let value=snapshot(),fail=false
 await page.route('**/api/ClusterSecurity',route=>fail?route.fulfill({status:503,json:{code:'unavailable',detail:'fixture unavailable'}}):route.fulfill({json:value}))
 const workspace=await openSecurity(page)
 await workspace.getByRole('button',{name:/Explain finding · CVE-TEST-1/}).click()
 const explain=workspace.getByLabel('Explain finding',{exact:true})
 await expect(explain).toContainText('Installed package version: 1.0');await expect(explain).toContainText('Fixed package version: 1.1')
 await expect(explain).toContainText('Trivy 0.69.3');await expect(explain).toContainText('verify the image digest and scan time')
 await page.screenshot({path:join(scratchRoot,'security-cve-evidence.png')})
 for(const [state,text] of [['missing','Install Trivy Operator'],['empty','No reports'],['denied','get/list access'],['error','could not be read'],['partial','coverage is incomplete'],['not-requested','Select at least one namespace']]) {
  value={...snapshot(state),findings:[],reports:0}
  await workspace.getByRole('button',{name:'Check again',exact:true}).click()
  await expect(workspace.getByRole('button',{name:'Check again',exact:true})).toBeEnabled()
  await expect(workspace.locator('[data-security-cve-status]')).toContainText(text)
  if(state==='denied') await expect(workspace.getByRole('link',{name:'Trivy Operator installation'})).toHaveCount(0)
 }
 fail=true;await workspace.getByRole('button',{name:'Check again',exact:true}).click()
 await expect(workspace.getByRole('alert')).toContainText('Previous snapshot; refresh failed')
 fail=false;value=snapshot();await workspace.getByRole('button',{name:'Check again',exact:true}).click()
 await expect(workspace.getByRole('alert')).toHaveCount(0)
 await expect(workspace.locator('[data-row-id]')).toHaveCount(1)
 await page.unrouteAll({behavior:'wait'})
})

test('security bounds the DOM, preserves all eight locales and fits narrow viewports',async({page})=>{
 const value=snapshot('missing')
 value.findings=Array.from({length:12000},(_,i)=>({...value.findings[0],id:'finding-'+i,rule:'CVE-TEST-'+i}))
 await page.route('**/api/ClusterSecurity',route=>route.fulfill({json:value}))
 const workspace=await openSecurity(page)
 await expect(workspace.getByRole('grid')).toHaveAttribute('aria-rowcount','12000')
 expect(await workspace.locator('[data-row-id]').count()).toBeLessThan(100)
 await workspace.locator('[data-table-scroll]').evaluate(el=>{el.scrollTop=32000})
 await expect(workspace.locator('[data-row-id]').first()).not.toHaveAttribute('data-row-id','finding-0')
 for(const language of languages) {
  await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[language],exact:true}).click()
  await expect(page.locator('html')).toHaveAttribute('lang',language)
  expect(await workspace.locator('[data-row-id]').count()).toBeLessThan(100)
  await expect(workspace.getByRole('columnheader')).toHaveCount(8)
  for(const width of [1440,860]) {
   await page.setViewportSize({width,height:900})
   expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBeTruthy()
   expect(await workspace.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBeTruthy()
   if(language==='en'||language==='ru') await page.screenshot({path:join(scratchRoot,`security-${language}-${width}.png`)})
  }
 }
 await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:'English',exact:true}).click()
 await page.setViewportSize({width:1280,height:900})
 await page.unrouteAll({behavior:'wait'})
})

test('security shared-grid columns resize, cancel, reset and persist without changing selection or sorting',async({page})=>{
 const workspace=await openSecurity(page),grid=workspace.getByRole('grid',{name:'Finding'})
 const column=grid.getByRole('columnheader',{name:'Resource / workload',exact:true})
 const handle=grid.getByRole('separator',{name:'Resize column Resource / workload',exact:true})
 const before=(await column.boundingBox())!.width
 const severity=grid.getByRole('columnheader',{name:/^Severity/}),sort=await severity.getAttribute('aria-sort')
 const first=grid.locator('[data-row-id]').first();const initialID=await first.getAttribute('data-row-id')
 let writes=0
 page.on('request',r=>{if(r.url().endsWith('/api/SetTargetState')&&r.postDataJSON()?.key==='columnWidths.ocular.security')writes++})
 const grip=(await handle.boundingBox())!
 await page.mouse.move(grip.x+grip.width/2,grip.y+grip.height/2);await page.mouse.down()
 await page.mouse.move(grip.x+grip.width/2+100,grip.y+grip.height/2,{steps:8})
 await expect.poll(async()=>Math.round((await column.boundingBox())!.width)).toBe(Math.round(before+100))
 expect(writes).toBe(0)
 const cell=first.getByRole('gridcell').nth(1)
 expect(Math.abs((await cell.boundingBox())!.width-(await column.boundingBox())!.width)).toBeLessThan(1)
 await page.keyboard.press('Escape');await page.mouse.up()
 await expect.poll(async()=>Math.round((await column.boundingBox())!.width)).toBe(Math.round(before))
 expect(writes).toBe(0)
 await handle.focus();await page.keyboard.press('Shift+ArrowRight')
 await expect.poll(()=>writes).toBe(1)
 await expect.poll(async()=>Math.round((await column.boundingBox())!.width)).toBe(Math.round(before+50))
 await expect(severity).toHaveAttribute('aria-sort',sort!)
 await expect(first).toHaveAttribute('data-row-id',initialID!)
 await expect(workspace.locator('[data-resource-drawer]')).toHaveCount(0)
 await page.reload()
 await expect(page.locator('.security-workspace')).toBeVisible()
 await expect.poll(async()=>Math.round((await page.getByRole('columnheader',{name:'Resource / workload',exact:true}).boundingBox())!.width)).toBe(Math.round(before+50))
 await page.getByRole('separator',{name:'Resize column Resource / workload',exact:true}).dblclick()
 await expect.poll(async()=>Math.round((await page.getByRole('columnheader',{name:'Resource / workload',exact:true}).boundingBox())!.width)).toBe(Math.round(before))
 const scroller=page.locator('.security-workspace [data-table-scroll]')
 await scroller.focus();await page.keyboard.press('ArrowDown')
 await expect(page.locator('.security-workspace [data-row-id][aria-selected=true]')).toHaveCount(1)
 await page.keyboard.press('Enter')
 await expect(page.locator('.security-workspace [data-resource-drawer]')).toBeVisible()
 await page.screenshot({path:join(scratchRoot,'security-shared-grid.png')})
})
