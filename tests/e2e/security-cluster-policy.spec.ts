import {test,expect,type Browser,type Page} from '@playwright/test'
import {mkdirSync,mkdtempSync,writeFileSync} from 'node:fs'
import {join} from 'node:path'
import {env,kubeconfig,writeAtomic,scratchRoot,selectConnection,connectSelected,connectionChoice,openAppMenu} from './fixtures'
import {start,stop,freePort} from './restartApp'
import {auditServer} from './security-audit-fixture'
import {languages,languageNames} from '../../web/src/languages'
const chooseAPI=async(page:Page,label:string)=>{await page.getByRole('button',{name:'Policy report API',exact:true}).click();await page.getByRole('option',{name:label,exact:true}).click()}
async function launch(browser:Browser){
 mkdirSync(scratchRoot,{recursive:true});const root=mkdtempSync(join(scratchRoot,'global-policy-audit-')),e=env(root),fixture=await auditServer(false,true,true)
 writeAtomic(e.one,kubeconfig('policy-audit','policy-audit').replace(/https:\/\/[^"\s]+/g,`http://127.0.0.1:${fixture.port}`).replace(/ns-policy-audit/g,'blue'))
 const port=await freePort(),url=`http://127.0.0.1:${port}`,page=await browser.newPage({viewport:{width:1440,height:1000}}),app=await start(port,e)
 try{
 await page.goto(url);const token=await page.locator('meta[name=spk-ocular-api-token]').getAttribute('content');const res=await page.request.post(url+'/api/Configurations',{headers:{Authorization:`Bearer ${token}`,Origin:url},data:{command:'import',paths:[e.one]}});expect(res.ok()).toBe(true)
 await page.reload();await selectConnection(page,/^policy-audit\b/);await connectSelected(page)
 const nav=page.getByRole('navigation',{name:'resources'});await nav.getByRole('textbox',{name:'Filter resources'}).fill('Security');await nav.getByRole('button',{name:'Security',exact:true}).click();await nav.getByRole('textbox',{name:'Filter resources'}).fill('')
 const workspace=page.locator('.security-workspace');await expect(workspace.locator('[data-row-id]')).toHaveCount(6)
 return {page,workspace,fixture,close:async()=>{await page.close();await stop(app);await fixture.close()}}
 }catch(e){await page.close();await stop(app);await fixture.close();throw e}
}

const globalPath=(path:string)=>/\/(clusterpolicyreports|clusterreports)(?:\/|$)/.test(path)
const finiteGlobal=(requests:{path:string;query:Record<string,string>}[])=>requests.filter(r=>globalPath(r.path)&&r.query.limit==='50'&&r.query.watch!=='true')
const globalLabel='Include cluster-wide policy reports'

test('global policy reports require independent consent, exact cluster scope and chosen API; guards, reset and cancellation remain intact',async({browser})=>{
 const f=await launch(browser),{page,workspace,fixture}=f
 try{
 const checkbox=workspace.getByRole('checkbox',{name:globalLabel,exact:true})
 await expect(checkbox).toBeDisabled();await expect(checkbox).not.toBeChecked();expect(finiteGlobal(fixture.requests)).toHaveLength(0)
 await chooseAPI(page,'wgpolicyk8s.io · PolicyReport');await expect(workspace.locator('[data-row-id]')).toHaveCount(12)
 expect(finiteGlobal(fixture.requests)).toHaveLength(0);await expect(checkbox).toBeEnabled();await checkbox.check();await expect(workspace.locator('[data-row-id]')).toHaveCount(18)
 expect(finiteGlobal(fixture.requests).map(r=>r.path)).toEqual(['/apis/wgpolicyk8s.io/v1beta1/clusterpolicyreports'])
 await expect(workspace.getByRole('checkbox',{name:'Include cluster-wide reports',exact:true})).not.toBeChecked()
 await workspace.getByRole('button',{name:'Source',exact:true}).click();await page.getByRole('option',{name:'Policy reports',exact:true}).click()
 await workspace.getByRole('button',{name:'Report result',exact:true}).click();await page.getByRole('option',{name:'Skipped (reported)',exact:true}).click();await expect(workspace.locator('[data-row-id]')).toHaveCount(2)
 await workspace.getByRole('button',{name:'Explain finding · require-label / check-app · global-wg',exact:true}).click()
 const detail=workspace.getByLabel('Explain finding',{exact:true});await expect(detail).toContainText('allowed-maintenance');await expect(detail).toContainText('Reported scored flag: Unknown');await expect(detail).toContainText('old-node-uid');await expect(detail).toContainText('UID uid-global-wg · RV 7');await expect(detail.locator('a,script')).toHaveCount(0)
 await workspace.getByRole('button',{name:'Report result',exact:true}).click();await page.getByRole('option',{name:'Fail (reported)',exact:true}).click()
 const boundary=fixture.requests.length
 await workspace.getByRole('button',{name:'Open resource · ClusterPolicyReport/global-wg',exact:true}).first().click()
 const drawer=workspace.locator('[data-resource-drawer]');await expect(drawer.getByRole('heading',{name:'global-wg',exact:true})).toBeVisible();await expect(drawer.locator('[data-object-events-toolbar]')).toBeVisible()
 await expect.poll(()=>fixture.requests.slice(boundary).some(r=>r.path==='/apis/wgpolicyk8s.io/v1beta1/clusterpolicyreports'&&r.query.watch==='true'&&r.query.fieldSelector==='metadata.name=global-wg')).toBe(true)
 await expect.poll(()=>fixture.requests.slice(boundary).some(r=>r.path==='/api/v1/events'&&r.query.watch==='true'&&r.query.fieldSelector==='involvedObject.uid=uid-global-wg')).toBe(true)
 await drawer.getByRole('tab',{name:'YAML',exact:true}).click();await expect(drawer).toContainText('uid-global-wg');await drawer.getByRole('button',{name:'Edit',exact:true}).click();await drawer.locator('.cm-content').click();await page.keyboard.press('Control+End');await page.keyboard.type('\n# held global policy draft')
 await chooseAPI(page,'openreports.io · Report');const confirm=page.getByRole('alertdialog',{name:'Discard edits?',exact:true});await expect(confirm).toBeVisible();await confirm.getByRole('button',{name:'Keep editing',exact:true}).click();await expect(drawer.locator('.cm-content')).toContainText('held global policy draft');await expect(checkbox).toBeChecked()
 expect(finiteGlobal(fixture.requests)).toHaveLength(1)
 await chooseAPI(page,'openreports.io · Report');await confirm.getByRole('button',{name:'Discard',exact:true}).click();await expect(drawer).toHaveCount(0);await expect(checkbox).not.toBeChecked();await expect(workspace.getByRole('button',{name:'Open resource · Report/policy-open',exact:true}).first()).toBeVisible()
 expect(finiteGlobal(fixture.requests)).toHaveLength(1)
 await checkbox.check();await expect(workspace.getByRole('button',{name:'Open resource · ClusterReport/global-open',exact:true}).first()).toBeVisible()
 expect(finiteGlobal(fixture.requests).map(r=>r.path)).toEqual(['/apis/wgpolicyk8s.io/v1beta1/clusterpolicyreports','/apis/openreports.io/v1alpha1/clusterreports'])
 await workspace.locator('[data-security-audit-status] summary').click()
 for(const [mode,label] of [['global-policy-denied','Report access denied'],['global-policy-missing','Report API unavailable'],['global-policy-empty','No reports; no pass established']]){
  fixture.setMode(mode);await workspace.getByRole('button',{name:'Refresh',exact:true}).click();await expect(workspace.getByRole('button',{name:'Refresh',exact:true})).toBeEnabled();await expect(workspace.getByText(/^ClusterReport · openreports.io/)).toContainText(label)
  await expect(workspace.getByRole('button',{name:'Open resource · Report/policy-open',exact:true}).first()).toBeVisible()
 }
 fixture.setMode('global-policy-hold');const before=finiteGlobal(fixture.requests).length;await workspace.getByRole('button',{name:'Refresh',exact:true}).click();await expect.poll(()=>finiteGlobal(fixture.requests).length).toBe(before+1)
 await workspace.getByRole('button',{name:'Cancel',exact:true}).click();await expect.poll(()=>fixture.clusterPolicyCancellationDurations.length).toBe(1);expect(fixture.clusterPolicyCancellationDurations[0]).toBeLessThan(20000)
 fixture.setMode('normal');await checkbox.uncheck();await expect(workspace.getByRole('button',{name:'Refresh',exact:true})).toBeEnabled();const off=finiteGlobal(fixture.requests).length;await workspace.getByRole('button',{name:'Refresh',exact:true}).focus();await page.keyboard.press('Enter');await expect(workspace.getByRole('button',{name:'Refresh',exact:true})).toBeEnabled();expect(finiteGlobal(fixture.requests)).toHaveLength(off)
 await checkbox.check();await expect(workspace.getByRole('button',{name:'Open resource · ClusterReport/global-open',exact:true}).first()).toBeVisible();const reconnect=finiteGlobal(fixture.requests).length
 await (await connectionChoice(page,/^policy-audit\b/)).click({button:'right'});await page.getByRole('menuitem',{name:'Disconnect',exact:true}).click();await page.getByRole('button',{name:'Connect',exact:true}).click()
 await expect(page.getByRole('button',{name:'Policy report API',exact:true})).toContainText('Off');await expect(checkbox).not.toBeChecked();await expect(checkbox).toBeDisabled();await expect(workspace.getByRole('button',{name:'Refresh',exact:true})).toBeEnabled();expect(finiteGlobal(fixture.requests)).toHaveLength(reconnect)
 expect(fixture.requests.every(r=>r.method==='GET')).toBe(true);expect(fixture.requests.filter(r=>/\/(secrets|configmaps|nodes|clusterconfigauditreports|clusterrbacassessmentreports)(?:\/|$)/.test(r.path))).toHaveLength(0)
 expect(finiteGlobal(fixture.requests).every(r=>!r.path.includes('/namespaces/'))).toBe(true)
 }finally{writeFileSync(test.info().outputPath('cluster-policy-wire.json'),JSON.stringify({requests:fixture.requests,cancelMilliseconds:fixture.clusterPolicyCancellationDurations},null,2));await f.close()}
})

test('global policy consent, source identity and native evidence fit eight locales at wide and compact widths',async({browser})=>{
 const f=await launch(browser),{page,workspace,fixture}=f
 try{
 await chooseAPI(page,'openreports.io · Report');await workspace.getByRole('checkbox',{name:globalLabel,exact:true}).check();await expect(workspace.locator('[data-row-id]')).toHaveCount(18)
 await workspace.getByRole('textbox',{name:'Find workload, rule, image or CVE'}).fill('global-open')
 await workspace.getByRole('button',{name:'Report result',exact:true}).click();await page.getByRole('option',{name:'Fail (reported)',exact:true}).click();await expect(workspace.locator('[data-row-id]')).toHaveCount(1)
 await workspace.getByRole('button',{name:'Explain finding · require-label / check-app · global-open',exact:true}).click();await workspace.locator('[data-security-audit-status] summary').click()
 const reads=finiteGlobal(fixture.requests).length
 for(const code of languages){
  await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[code],exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang',code)
  for(const width of [1440,860]){
   await page.setViewportSize({width,height:1000});expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);expect(await workspace.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true)
   await expect(workspace.locator('[data-row-id]')).toHaveCount(1);expect((await workspace.locator('[data-area="table"]').boundingBox())!.height).toBeGreaterThan(32)
   await workspace.locator('[data-security-explanation]').evaluate(el=>{el.scrollTop=0});await page.screenshot({path:join(scratchRoot,`cluster-policy-${code}-${width}.png`)})
   await workspace.locator('[data-security-explanation]').evaluate(el=>{el.scrollTop=el.scrollHeight});await page.screenshot({path:join(scratchRoot,`cluster-policy-${code}-${width}-provenance.png`)})
  }
  await page.setViewportSize({width:1440,height:1000})
 }
 expect(finiteGlobal(fixture.requests)).toHaveLength(reads)
 }finally{writeFileSync(test.info().outputPath('cluster-policy-wire.json'),JSON.stringify(fixture.requests,null,2));await f.close()}
})
