import {test,expect,type Browser,type Page} from '@playwright/test'
import {mkdirSync,mkdtempSync,writeFileSync} from 'node:fs'
import {join} from 'node:path'
import {env,kubeconfig,writeAtomic,scratchRoot,selectConnection,connectSelected,connectionChoice,openAppMenu} from './fixtures'
import {start,stop,freePort} from './restartApp'
import {auditServer} from './security-audit-fixture'
import {languages,languageNames} from '../../web/src/languages'
const policyPath=(path:string)=>/\/(wgpolicyk8s.io|openreports.io)\/.+\/(policyreports|reports)(?:\/|$)/.test(path)
const chooseAPI=async(page:Page,label:string)=>{await page.getByRole('button',{name:'Namespaced policy reports',exact:true}).click();await page.getByRole('option',{name:label,exact:true}).click()}
async function launch(browser:Browser){
 mkdirSync(scratchRoot,{recursive:true});const root=mkdtempSync(join(scratchRoot,'policy-audit-')),e=env(root),fixture=await auditServer(false,true)
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

test('policy reports use one explicit discovered API, preserve verdicts and guards, reset on reconnect and cancel a finite read',async({browser})=>{
 const f=await launch(browser),{page,workspace,fixture}=f
 try{
 expect(fixture.requests.filter(r=>policyPath(r.path))).toHaveLength(0)
 await expect(page.getByRole('button',{name:'Namespaced policy reports',exact:true})).toContainText('Off')
 await chooseAPI(page,'wgpolicyk8s.io · PolicyReport');await expect(workspace.locator('[data-row-id]')).toHaveCount(12)
 const first=fixture.requests.filter(r=>policyPath(r.path));expect(first).toHaveLength(1);expect(first[0].path).toBe('/apis/wgpolicyk8s.io/v1beta1/namespaces/blue/policyreports');expect(first[0].query).toEqual({limit:'50'})
 await workspace.getByRole('button',{name:'Source',exact:true}).click();await page.getByRole('option',{name:'Policy reports',exact:true}).click();await expect(workspace.locator('[data-row-id]')).toHaveCount(6)
 await workspace.getByRole('button',{name:'Report result',exact:true}).click();await page.getByRole('option',{name:'Skipped (reported)',exact:true}).click();await expect(workspace.locator('[data-row-id]')).toHaveCount(1)
 await workspace.getByRole('button',{name:/Explain finding/}).click();const detail=workspace.getByLabel('Explain finding',{exact:true});await expect(detail).toContainText('allowed-maintenance');await expect(detail).toContainText('Reported scored flag: Unknown');await expect(detail).toContainText('root-producer');await expect(detail.locator('a,script')).toHaveCount(0)
 await workspace.getByRole('button',{name:'Report result',exact:true}).click();await page.getByRole('option',{name:'Fail (reported)',exact:true}).click();await expect(workspace.locator('[data-row-id]')).toHaveCount(1)
 await workspace.getByRole('button',{name:/Explain finding/}).click();await expect(detail).toContainText('UID uid-policy-wg · RV 7');await expect(detail).toContainText('old-subject');await expect(detail).toContainText('<script>not executed</script>');await expect(detail).not.toContainText('PRIVATE')
 const boundary=fixture.requests.length
 await workspace.getByRole('button',{name:'Open resource · PolicyReport/policy-wg',exact:true}).first().click();const drawer=workspace.locator('[data-resource-drawer]');await expect(drawer.getByRole('heading',{name:'policy-wg',exact:true})).toBeVisible();await expect(drawer.locator('[data-object-events-toolbar]')).toBeVisible()
 await expect.poll(()=>fixture.requests.slice(boundary).some(r=>r.path==='/apis/wgpolicyk8s.io/v1beta1/namespaces/blue/policyreports'&&r.query.watch==='true'&&r.query.fieldSelector==='metadata.name=policy-wg')).toBe(true)
 await expect.poll(()=>fixture.requests.slice(boundary).some(r=>r.path==='/api/v1/namespaces/blue/events'&&r.query.watch==='true'&&r.query.fieldSelector==='involvedObject.uid=uid-policy-wg')).toBe(true)
 await drawer.getByRole('tab',{name:'YAML',exact:true}).click();await expect(drawer).toContainText('uid-policy-wg');await drawer.getByRole('button',{name:'Edit',exact:true}).click();await drawer.locator('.cm-content').click();await page.keyboard.press('Control+End');await page.keyboard.type('\n# held policy draft')
 await chooseAPI(page,'openreports.io · Report');const confirm=page.getByRole('alertdialog',{name:'Discard edits?',exact:true});await expect(confirm).toBeVisible();await confirm.getByRole('button',{name:'Keep editing',exact:true}).click();await expect(drawer.locator('.cm-content')).toContainText('held policy draft');expect(fixture.requests.filter(r=>r.path.includes('openreports.io')&&r.query.limit==='50')).toHaveLength(0)
 await chooseAPI(page,'openreports.io · Report');await confirm.getByRole('button',{name:'Discard',exact:true}).click();await expect(drawer).toHaveCount(0);await expect(workspace.getByRole('button',{name:'Open resource · Report/policy-open',exact:true}).first()).toBeVisible()
 const selected=fixture.requests.filter(r=>policyPath(r.path)&&r.query.limit==='50');expect(selected).toHaveLength(2);expect(selected[1].path).toBe('/apis/openreports.io/v1alpha1/namespaces/blue/reports')
 await workspace.locator('[data-security-audit-status] summary').click()
 for(const [mode,label] of [['policy-denied','Report access denied'],['policy-missing','Report API unavailable'],['policy-empty','No reports; no pass established']]){fixture.setMode(mode);await workspace.getByRole('button',{name:'Refresh',exact:true}).click();await expect(workspace.getByRole('button',{name:'Refresh',exact:true})).toBeEnabled();await expect(workspace.locator('[data-security-audit-status]')).toContainText(label)}
 fixture.setMode('policy-hold');const before=fixture.requests.length;await workspace.getByRole('button',{name:'Refresh',exact:true}).click();await expect.poll(()=>fixture.requests.slice(before).some(r=>r.path.endsWith('/reports')&&r.query.limit==='50')).toBe(true);await workspace.getByRole('button',{name:'Cancel',exact:true}).click();await expect.poll(()=>fixture.policyCancellationDurations.length).toBe(1);expect(fixture.policyCancellationDurations[0]).toBeLessThan(20000)
 fixture.setMode('normal');await chooseAPI(page,'Off');await expect(workspace.getByRole('button',{name:'Refresh',exact:true})).toBeEnabled();const off=fixture.requests.length;await workspace.getByRole('button',{name:'Refresh',exact:true}).focus();await page.keyboard.press('Enter');await expect(workspace.getByRole('button',{name:'Refresh',exact:true})).toBeEnabled();expect(fixture.requests.slice(off).filter(r=>policyPath(r.path)&&r.query.limit==='50')).toHaveLength(0)
 await chooseAPI(page,'openreports.io · Report');await expect(workspace.getByRole('button',{name:'Open resource · Report/policy-open',exact:true}).first()).toBeVisible();const reconnect=fixture.requests.filter(r=>policyPath(r.path)&&r.query.limit==='50').length
 await (await connectionChoice(page,/^policy-audit\b/)).click({button:'right'});await page.getByRole('menuitem',{name:'Disconnect',exact:true}).click();await page.getByRole('button',{name:'Connect',exact:true}).click();await expect(page.getByRole('button',{name:'Namespaced policy reports',exact:true})).toContainText('Off');await expect(workspace.getByRole('button',{name:'Refresh',exact:true})).toBeEnabled();expect(fixture.requests.filter(r=>policyPath(r.path)&&r.query.limit==='50')).toHaveLength(reconnect)
 expect(fixture.requests.every(r=>r.method==='GET')).toBe(true);expect(fixture.requests.filter(r=>/\/(secrets|configmaps|nodes|clusterpolicyreports|clusterreports|clusterconfigauditreports|clusterrbacassessmentreports)(?:\/|$)/.test(r.path))).toHaveLength(0)
 }finally{writeFileSync(test.info().outputPath('policy-wire.json'),JSON.stringify({requests:fixture.requests,cancelMilliseconds:fixture.policyCancellationDurations},null,2));await f.close()}
})

test('policy API choice, native results and provenance fit eight locales at wide and compact widths',async({browser})=>{
 const f=await launch(browser),{page,workspace,fixture}=f
 try{
 await chooseAPI(page,'openreports.io · Report');await expect(workspace.locator('[data-row-id]')).toHaveCount(12)
 await workspace.getByRole('button',{name:'Source',exact:true}).click();await page.getByRole('option',{name:'Policy reports',exact:true}).click()
 await workspace.getByRole('button',{name:'Report result',exact:true}).click();await page.getByRole('option',{name:'Fail (reported)',exact:true}).click();await workspace.getByRole('button',{name:/Explain finding/}).click();await workspace.locator('[data-security-audit-status] summary').click()
 const reads=fixture.requests.filter(r=>policyPath(r.path)).length
 for(const code of languages){
  await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[code],exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang',code)
  for(const width of [1440,860]){await page.setViewportSize({width,height:1000});expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);expect(await workspace.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true);await expect(workspace.locator('[data-row-id]')).toHaveCount(1);expect((await workspace.locator('[data-area="table"]').boundingBox())!.height).toBeGreaterThan(32);await workspace.locator('[data-security-explanation]').evaluate(el=>{el.scrollTop=0});await page.screenshot({path:join(scratchRoot,`policy-${code}-${width}.png`)});await workspace.locator('[data-security-explanation]').evaluate(el=>{el.scrollTop=el.scrollHeight});await page.screenshot({path:join(scratchRoot,`policy-${code}-${width}-provenance.png`)})}
  await page.setViewportSize({width:1440,height:1000})
 }
 expect(fixture.requests.filter(r=>policyPath(r.path))).toHaveLength(reads)
 }finally{writeFileSync(test.info().outputPath('policy-wire.json'),JSON.stringify(fixture.requests,null,2));await f.close()}
})
