import {test,expect,type Browser} from '@playwright/test'
import {mkdirSync,mkdtempSync,writeFileSync} from 'node:fs'
import {join} from 'node:path'
import {env,kubeconfig,writeAtomic,scratchRoot,selectConnection,connectSelected,openAppMenu} from './fixtures'
import {start,stop,freePort} from './restartApp'
import {auditServer} from './security-audit-fixture'
import {languages,languageNames} from '../../web/src/languages'
async function launch(browser:Browser){
 mkdirSync(scratchRoot,{recursive:true});const root=mkdtempSync(join(scratchRoot,'audit-')),e=env(root),fixture=await auditServer()
 writeAtomic(e.one,kubeconfig('audit','audit').replace(/https:\/\/[^"\s]+/g,`http://127.0.0.1:${fixture.port}`).replace(/ns-audit/g,'blue'))
 const port=await freePort(),url=`http://127.0.0.1:${port}`,page=await browser.newPage({viewport:{width:1440,height:1000}}),app=await start(port,e)
 try{
 await page.goto(url);const token=await page.locator('meta[name=spk-ocular-api-token]').getAttribute('content');const res=await page.request.post(url+'/api/Configurations',{headers:{Authorization:`Bearer ${token}`,Origin:url},data:{command:'import',paths:[e.one]}});expect(res.ok()).toBe(true)
 await page.reload();await selectConnection(page,/^audit\b/);await connectSelected(page)
 const nav=page.getByRole('navigation',{name:'resources'});await nav.getByRole('textbox',{name:'Filter resources'}).fill('Security');await nav.getByRole('button',{name:'Security',exact:true}).click();await nav.getByRole('textbox',{name:'Filter resources'}).fill('')
 const workspace=page.locator('.security-workspace');await expect(workspace.locator('[data-row-id]')).toHaveCount(6)
 return {page,workspace,fixture,close:async()=>{await page.close();await stop(app);await fixture.close()}}
 }catch(e){await page.close();await stop(app);await fixture.close();throw e}
}
test('actual namespaced audit reports preserve external results, coverage, source navigation and cancellation',async({browser})=>{
 const f=await launch(browser),{page,workspace,fixture}=f
 try{
 await workspace.locator('[data-security-audit-status] summary').click();await expect(workspace.locator('[data-security-audit-status]')).toContainText('Incomplete report coverage')
 await workspace.getByRole('button',{name:'Report result',exact:true}).click();await page.getByRole('option',{name:'Fail (reported)',exact:true}).click();await expect(workspace.locator('[data-row-id]')).toHaveCount(2)
 await workspace.getByRole('button',{name:'Explain finding · EXT-001 · config-web',exact:true}).click();const detail=workspace.getByLabel('Explain finding',{exact:true});await expect(detail).toContainText('UID uid-config-web · RV 7');await expect(detail).toContainText('UID old-controller');await expect(detail).toContainText('<script>not executed</script>');await expect(detail).toContainText('Unknown');await expect(detail.locator('a')).toHaveCount(0);await expect(workspace).not.toContainText('PRIVATE')
 const acquisition=fixture.requests.length;expect(fixture.requests.filter(r=>r.path.includes('auditreports')||r.path.includes('assessmentreports')).every(r=>r.path.includes('/namespaces/blue/')&&r.query.watch!=='true')).toBe(true)
 await workspace.getByRole('button',{name:'Open resource · ConfigAuditReport/config-web',exact:true}).first().click();const drawer=workspace.locator('[data-resource-drawer]');await expect(drawer.getByRole('heading',{name:'config-web',exact:true})).toBeVisible();await expect(drawer.locator('[data-object-events-toolbar]')).toBeVisible();await expect.poll(()=>fixture.requests.slice(acquisition).some(r=>r.path==='/apis/aquasecurity.github.io/v1alpha1/namespaces/blue/configauditreports'&&r.query.watch==='true'&&r.query.fieldSelector==='metadata.name=config-web')).toBe(true);await expect.poll(()=>fixture.requests.slice(acquisition).some(r=>r.path==='/api/v1/namespaces/blue/events'&&r.query.watch==='true'&&r.query.fieldSelector==='involvedObject.uid=uid-config-web')).toBe(true);await drawer.getByRole('tab',{name:'YAML',exact:true}).click();await expect(drawer).toContainText('uid-config-web');await drawer.locator('[data-drawer-close]').click()
 expect(fixture.requests.slice(acquisition).filter(r=>r.query.watch==='true').every(r=>(r.path==='/apis/aquasecurity.github.io/v1alpha1/namespaces/blue/configauditreports'&&r.query.fieldSelector==='metadata.name=config-web')||(r.path==='/api/v1/namespaces/blue/events'&&r.query.fieldSelector==='involvedObject.uid=uid-config-web'))).toBe(true)
 for(const [mode,label] of [['denied','Report access denied'],['missing','Report API unavailable'],['empty','No reports; no pass established']]){fixture.setMode(mode);await workspace.getByRole('button',{name:'Refresh',exact:true}).click();await expect(workspace.getByRole('button',{name:'Refresh',exact:true})).toBeEnabled();await expect(workspace.locator('[data-security-audit-status]')).toContainText(label)}
 fixture.setMode('hold');const before=fixture.requests.length;await workspace.getByRole('button',{name:'Refresh',exact:true}).click();await expect.poll(()=>fixture.requests.slice(before).some(r=>r.path.endsWith('/configauditreports'))).toBe(true);await workspace.getByRole('button',{name:'Cancel',exact:true}).click();await expect.poll(()=>fixture.cancelled()).toBeGreaterThan(0)
 fixture.setMode('normal');await workspace.getByRole('button',{name:'Refresh',exact:true}).focus();await page.keyboard.press('Enter');await expect(workspace.locator('[data-row-id]')).toHaveCount(2)
 expect(fixture.requests.every(r=>r.method==='GET')).toBe(true);expect(fixture.requests.filter(r=>/\/(secrets|configmaps|nodes|clusterconfigauditreports|clusterrbacassessmentreports|policyreports)(?:\/|$)/.test(r.path))).toHaveLength(0)
 }finally{writeFileSync(test.info().outputPath('audit-wire.json'),JSON.stringify(fixture.requests,null,2));await f.close()}
})
test('actual audit evidence and coverage in all eight locales at wide and compact widths',async({browser})=>{
 const f=await launch(browser),{page,workspace}=f
 try{
 await workspace.locator('[data-security-audit-status] summary').click();await workspace.getByRole('button',{name:'Explain finding · EXT-001 · config-web',exact:true}).click()
 for(const code of languages){
 await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[code],exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang',code)
 for(const width of [1440,860]){await page.setViewportSize({width,height:1000});expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);expect(await workspace.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true);await workspace.locator('[data-security-explanation]').evaluate(el=>{el.scrollTop=0});await page.screenshot({path:join(scratchRoot,`audit-${code}-${width}.png`)});await workspace.locator('[data-security-explanation]').evaluate(el=>{el.scrollTop=el.scrollHeight});await page.screenshot({path:join(scratchRoot,`audit-${code}-${width}-provenance.png`)})}
 await page.setViewportSize({width:1440,height:1000})
 }
 }finally{writeFileSync(test.info().outputPath('audit-wire.json'),JSON.stringify(f.fixture.requests,null,2));await f.close()}
})
