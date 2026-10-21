import {test,expect,type Browser} from '@playwright/test'
import {mkdirSync,mkdtempSync,writeFileSync} from 'node:fs'
import {join} from 'node:path'
import {env,kubeconfig,writeAtomic,scratchRoot,selectConnection,connectSelected,openAppMenu} from './fixtures'
import {start,stop,freePort} from './restartApp'
import {helmDiagnosisServer} from './helm-diagnosis-fixture'
import {languages,languageNames} from '../../web/src/languages'
async function launch(browser:Browser){
 mkdirSync(scratchRoot,{recursive:true});const root=mkdtempSync(join(scratchRoot,'helm-diagnosis-')),e=env(root),fixture=await helmDiagnosisServer()
 writeFileSync(join(scratchRoot,'helm-diagnosis-fixture-data.json'),JSON.stringify(fixture.data))
 writeAtomic(e.one,kubeconfig('helm-diagnosis','helm-diagnosis').replace(/https:\/\/[^"\s]+/g,`http://127.0.0.1:${fixture.port}`).replace(/ns-helm-diagnosis/g,'storage'))
 const port=await freePort(),url=`http://127.0.0.1:${port}`,page=await browser.newPage({viewport:{width:1440,height:1000}}),app=await start(port,e)
 try{
  await page.goto(url);const token=await page.locator('meta[name=spk-ocular-api-token]').getAttribute('content')
  const response=await page.request.post(url+'/api/Configurations',{headers:{Authorization:`Bearer ${token}`,Origin:url},data:{command:'import',paths:[e.one]}});expect(response.ok()).toBe(true)
  await page.reload();await selectConnection(page,/^helm-diagnosis\b/);await connectSelected(page)
  const nav=page.getByRole('navigation',{name:'resources'});await nav.getByRole('textbox',{name:'Filter resources'}).fill('Releases');await nav.getByRole('button',{name:'Releases',exact:true}).click()
  await page.getByRole('grid',{name:'resources'}).getByRole('gridcell',{name:'demo',exact:true}).click()
  const drawer=page.locator('.helm-workspace [data-area=details]')
  await page.getByRole('tab',{name:'History',exact:true}).click();await page.getByRole('button',{name:'#2 · 1.0.0 · failed',exact:true}).click()
  await page.getByRole('tab',{name:'Diagnosis',exact:true}).click();const panel=page.locator('[data-helm-diagnosis]')
  await expect(panel.getByRole('button',{name:'Load diagnosis'})).toBeVisible()
  return {page,panel,drawer,fixture,close:async()=>{await page.close();await stop(app);await fixture.close()}}
 }catch(error){await page.close();await stop(app);await fixture.close();throw error}
}
test('actual Helm diagnosis separates stored history and current UID evidence with safe read-only comparison and Flux context',async({browser})=>{
 const f=await launch(browser),{page,panel,fixture}=f
 try{
  expect(fixture.requests.filter(r=>r.path.endsWith('/jobs/check'))).toHaveLength(0)
  await panel.getByRole('button',{name:'Load diagnosis'}).click();await expect(panel).toContainText('Recorded hooks');await expect(page.getByRole('button',{name:'Upgrade',exact:true})).toBeDisabled()
  await panel.getByRole('button',{name:'Observe current hook'}).click();await expect(panel).toContainText('Current Job failed');await expect(panel.getByRole('button',{name:'pods/check-pod · Current Properties'})).toBeVisible()
  await panel.getByRole('button',{name:'Compare revisions',exact:true}).click();await expect(panel.getByRole('region',{name:'#1 → #2'})).toBeVisible();await expect(panel).not.toContainText('PRIVATE-PASSWORD');await expect(panel).not.toContainText('PRIVATE-ANNOTATION')
  await panel.getByRole('textbox',{name:'Controller namespace'}).fill('controllers');await panel.getByRole('button',{name:'Load Flux context'}).click();await expect(panel).toContainText('reported_match');await expect(panel.getByRole('button',{name:'controllers/managed-demo · Current Properties'})).toBeVisible()
  fixture.denyFlux(true);await panel.getByRole('button',{name:'Load Flux context'}).click();await expect(panel).toContainText('Read coverage: forbidden');fixture.denyFlux(false)
  fixture.missing(true);await panel.getByRole('button',{name:'Observe current hook'}).click();await expect(panel).toContainText('not_found');await expect(panel.getByRole('button',{name:'pods/check-pod · Current Properties'})).toHaveCount(0)
  fixture.missing(false);fixture.denyEvents(true);await panel.getByRole('button',{name:'Observe current hook'}).click();await expect(panel).toContainText('forbidden')
  fixture.denyEvents(false);fixture.replace(true);await panel.getByRole('button',{name:'Observe current hook'}).click();await expect(panel.getByRole('button',{name:'pods/check-pod · Current Properties'})).toHaveCount(0)
  await panel.getByRole('button',{name:'batch/jobs/check · Current Properties'}).click();await expect(page.locator('[data-resource-drawer]').getByRole('heading',{name:'check',exact:true})).toBeVisible()
  expect(fixture.requests.every(r=>r.method==='GET')).toBe(true)
  const secretReads=fixture.requests.filter(r=>/\/secrets(?:\/|$)/.test(r.path));expect(secretReads.length).toBeGreaterThan(0);expect(secretReads.every(r=>r.path.startsWith('/api/v1/namespaces/storage/secrets'))).toBe(true)
  expect(fixture.requests.filter(r=>r.path.includes('dependency-never-read')||r.path.includes('/configmaps')||r.path.endsWith('/log'))).toHaveLength(0)
 }finally{writeFileSync(test.info().outputPath('helm-diagnosis-wire.json'),JSON.stringify(fixture.requests,null,2));await f.close()}
})
test('actual Helm diagnosis fits eight locales at wide and narrow widths',async({browser})=>{
 const f=await launch(browser),{page,panel}=f
 try{
  await panel.getByRole('button',{name:'Load diagnosis'}).click();await expect(panel).toContainText('Recorded hooks');await panel.getByRole('button',{name:'Observe current hook'}).click();await expect(panel).toContainText('Current Job failed');await panel.getByRole('button',{name:'Compare revisions',exact:true}).click();await expect(panel.getByRole('region',{name:'#1 → #2'})).toBeVisible();await panel.getByRole('textbox',{name:'Controller namespace'}).fill('controllers');await panel.getByRole('button',{name:'Load Flux context'}).click();await expect(panel).toContainText('reported_match')
  for(const code of languages){
   await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[code],exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang',code)
   for(const width of [1440,375]){await page.setViewportSize({width,height:1000});await panel.evaluate(e=>e.scrollTop=0);expect(await panel.evaluate(e=>e.scrollWidth<=e.clientWidth)).toBe(true);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:join(scratchRoot,`helm-diagnosis-${code}-${width}.png`)});await panel.evaluate(e=>e.scrollTop=e.scrollHeight);await page.screenshot({path:join(scratchRoot,`helm-diagnosis-${code}-${width}-compare.png`)})}
   await page.setViewportSize({width:1440,height:1000})
  }
 }finally{await f.close()}
})

test('actual Helm history retains first install, pending uncertainty and recorded rollback without invented baseline',async({browser})=>{
 const f=await launch(browser),{page,panel}=f
 try{
  await page.getByRole('tab',{name:'History',exact:true}).click();await page.getByRole('button',{name:'#3 · 1.0.0 · pending-upgrade',exact:true}).click();await page.getByRole('tab',{name:'Diagnosis',exact:true}).click();await panel.getByRole('button',{name:'Load diagnosis'}).click();await expect(panel).toContainText('Pending status does not prove a stuck operation')
  await page.getByRole('tab',{name:'History',exact:true}).click();await page.getByRole('button',{name:'#4 · 1.0.0 · deployed',exact:true}).click();await page.getByRole('tab',{name:'Diagnosis',exact:true}).click();await panel.getByRole('button',{name:'Load diagnosis'}).click();await expect(panel).toContainText('Recorded rollback target: #1')
  await page.getByRole('button',{name:'Close',exact:true}).click();await page.getByRole('grid',{name:'resources'}).getByRole('gridcell',{name:'first-install',exact:true}).click();await page.getByRole('tab',{name:'Diagnosis',exact:true}).click();await panel.getByRole('button',{name:'Load diagnosis'}).click();await expect(panel).toContainText('A first install has no prior baseline');expect(f.fixture.requests.every(r=>r.method==='GET')).toBe(true)
 }finally{await f.close()}
})
