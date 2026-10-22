import {test,expect,type Browser} from '@playwright/test'
import {mkdirSync,mkdtempSync,writeFileSync} from 'node:fs'
import {join} from 'node:path'
import {env,kubeconfig,writeAtomic,scratchRoot,selectConnection,connectSelected,openAppMenu} from './fixtures'
import {start,stop,freePort} from './restartApp'
import {auditServer} from './security-audit-fixture'
import {languages,languageNames} from '../../web/src/languages'
const globalReport=(path:string)=>/\/(clusterconfigauditreports|clusterrbacassessmentreports)(?:\/|$)/.test(path)
async function launch(browser:Browser){
 mkdirSync(scratchRoot,{recursive:true});const root=mkdtempSync(join(scratchRoot,'cluster-audit-')),e=env(root),fixture=await auditServer(true)
 writeAtomic(e.one,kubeconfig('cluster-audit','cluster-audit').replace(/https:\/\/[^"\s]+/g,`http://127.0.0.1:${fixture.port}`).replace(/ns-cluster-audit/g,'blue'))
 const port=await freePort(),url=`http://127.0.0.1:${port}`,page=await browser.newPage({viewport:{width:1440,height:1000}}),app=await start(port,e)
 try{
 await page.goto(url);const token=await page.locator('meta[name=spk-ocular-api-token]').getAttribute('content');const res=await page.request.post(url+'/api/Configurations',{headers:{Authorization:`Bearer ${token}`,Origin:url},data:{command:'import',paths:[e.one]}});expect(res.ok()).toBe(true)
 await page.reload();await selectConnection(page,/^cluster-audit\b/);await connectSelected(page)
 const nav=page.getByRole('navigation',{name:'resources'});await nav.getByRole('textbox',{name:'Filter resources'}).fill('Security');await nav.getByRole('button',{name:'Security',exact:true}).click();await nav.getByRole('textbox',{name:'Filter resources'}).fill('')
 const workspace=page.locator('.security-workspace');await expect(workspace.locator('[data-row-id]')).toHaveCount(6)
 return {page,workspace,fixture,close:async()=>{await page.close();await stop(app);await fixture.close()}}
 }catch(e){await page.close();await stop(app);await fixture.close();throw e}
}
test('cluster audit reports require opt-in, preserve global source UID and cancel a finite global read',async({browser})=>{
 const f=await launch(browser),{page,workspace,fixture}=f
 try{
 const checkbox=workspace.getByRole('checkbox',{name:'Include cluster-wide reports'});await expect(checkbox).not.toBeChecked();expect(fixture.requests.filter(r=>globalReport(r.path))).toHaveLength(0)
 await workspace.locator('[data-security-audit-status] summary').click();await expect(workspace.locator('[data-security-audit-status]')).toContainText('Not requested')
 await checkbox.check();await expect(workspace.locator('[data-row-id]')).toHaveCount(12)
 const acquired=fixture.requests.filter(r=>globalReport(r.path));expect(acquired).toHaveLength(2);expect(acquired.every(r=>!r.path.includes('/namespaces/')&&r.query.limit==='50'&&r.query.watch===undefined)).toBe(true)
 await workspace.getByRole('textbox',{name:'Find workload, rule, image or CVE'}).fill('cluster-config');await expect(workspace.locator('[data-row-id]')).toHaveCount(3)
 await workspace.getByRole('button',{name:'Explain finding · EXT-001 · cluster-config',exact:true}).click();const detail=workspace.getByLabel('Explain finding',{exact:true});await expect(detail).toContainText('UID uid-cluster-config · RV 7');await expect(detail).toContainText('UID old-controller');await expect(detail).toContainText('<script>not executed</script>');await expect(detail).toContainText('unverified');await expect(detail.locator('a')).toHaveCount(0)
 const boundary=fixture.requests.length
 await workspace.getByRole('button',{name:'Open resource · ClusterConfigAuditReport/cluster-config',exact:true}).first().click();const drawer=workspace.locator('[data-resource-drawer]');await expect(drawer.getByRole('heading',{name:'cluster-config',exact:true})).toBeVisible();await expect(drawer.locator('[data-object-events-toolbar]')).toBeVisible()
 await expect.poll(()=>fixture.requests.slice(boundary).some(r=>r.path==='/apis/aquasecurity.github.io/v1alpha1/clusterconfigauditreports'&&r.query.watch==='true'&&r.query.fieldSelector==='metadata.name=cluster-config')).toBe(true)
 await expect.poll(()=>fixture.requests.slice(boundary).some(r=>r.path==='/api/v1/events'&&r.query.watch==='true'&&r.query.fieldSelector==='involvedObject.uid=uid-cluster-config')).toBe(true)
 await drawer.getByRole('tab',{name:'YAML',exact:true}).click();await expect(drawer).toContainText('uid-cluster-config');await drawer.locator('[data-drawer-close]').click()
 for(const [mode,label] of [['cluster-denied','Report access denied'],['cluster-missing','Report API unavailable'],['cluster-empty','No reports; no pass established']]){fixture.setMode(mode);await workspace.getByRole('button',{name:'Refresh',exact:true}).click();await expect(workspace.getByRole('button',{name:'Refresh',exact:true})).toBeEnabled();await expect(workspace.locator('[data-security-audit-status]')).toContainText(label)}
 fixture.setMode('cluster-hold');const before=fixture.requests.length;await workspace.getByRole('button',{name:'Refresh',exact:true}).click();await expect.poll(()=>fixture.requests.slice(before).some(r=>r.path.endsWith('/clusterconfigauditreports')&&r.query.limit==='50')).toBe(true);await workspace.getByRole('button',{name:'Cancel',exact:true}).click();await expect.poll(()=>fixture.cancelled()).toBeGreaterThan(0)
 fixture.setMode('normal');await checkbox.uncheck();await expect(workspace.getByRole('button',{name:'Refresh',exact:true})).toBeEnabled();const disabled=fixture.requests.length;await workspace.getByRole('button',{name:'Refresh',exact:true}).focus();await page.keyboard.press('Enter');await expect(workspace.getByRole('button',{name:'Refresh',exact:true})).toBeEnabled();expect(fixture.requests.slice(disabled).filter(r=>globalReport(r.path)&&r.query.limit==='50')).toHaveLength(0)
 expect(fixture.requests.every(r=>r.method==='GET')).toBe(true);expect(fixture.requests.filter(r=>/\/(secrets|configmaps|nodes|policyreports)(?:\/|$)/.test(r.path))).toHaveLength(0)
 }finally{writeFileSync(test.info().outputPath('cluster-audit-wire.json'),JSON.stringify(fixture.requests,null,2));await f.close()}
})
test('cluster audit opt-in, scope and evidence in eight locales at wide and compact widths',async({browser})=>{
 const f=await launch(browser),{page,workspace,fixture}=f
 try{
 await workspace.getByRole('checkbox',{name:'Include cluster-wide reports'}).check();await expect(workspace.locator('[data-row-id]')).toHaveCount(12)
 await workspace.locator('[data-security-audit-status] summary').click();await workspace.getByRole('textbox',{name:'Find workload, rule, image or CVE'}).fill('cluster-config');await workspace.getByRole('button',{name:'Explain finding · EXT-001 · cluster-config',exact:true}).click()
 const reads=fixture.requests.filter(r=>globalReport(r.path)).length
 for(const code of languages){
  await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[code],exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang',code)
  for(const width of [1440,860]){await page.setViewportSize({width,height:1000});expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);expect(await workspace.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true);await workspace.locator('[data-security-explanation]').evaluate(el=>{el.scrollTop=0});await page.screenshot({path:join(scratchRoot,`cluster-audit-${code}-${width}.png`)});await workspace.locator('[data-security-explanation]').evaluate(el=>{el.scrollTop=el.scrollHeight});await page.screenshot({path:join(scratchRoot,`cluster-audit-${code}-${width}-provenance.png`)})}
  await page.setViewportSize({width:1440,height:1000})
 }
 expect(fixture.requests.filter(r=>globalReport(r.path))).toHaveLength(reads)
 }finally{writeFileSync(test.info().outputPath('cluster-audit-wire.json'),JSON.stringify(fixture.requests,null,2));await f.close()}
})
