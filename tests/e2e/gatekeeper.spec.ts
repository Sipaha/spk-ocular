import {test,expect,type Browser} from '@playwright/test'
import {mkdirSync,mkdtempSync,writeFileSync} from 'node:fs'
import {join} from 'node:path'
import {env,kubeconfig,writeAtomic,scratchRoot,selectConnection,connectSelected,openAppMenu} from './fixtures'
import {start,stop,freePort} from './restartApp'
import {gatekeeperServer} from './gatekeeper-fixture'
import {languages,languageNames} from '../../web/src/languages'
async function launch(browser:Browser){
 mkdirSync(scratchRoot,{recursive:true});const root=mkdtempSync(join(scratchRoot,'gk-')),e=env(root),fixture=await gatekeeperServer()
 writeAtomic(e.one,kubeconfig('gatekeeper','gatekeeper').replace(/https:\/\/[^"\s]+/g,`http://127.0.0.1:${fixture.port}`).replace(/ns-gatekeeper/g,'blue'))
 const port=await freePort(),url=`http://127.0.0.1:${port}`,page=await browser.newPage({viewport:{width:1440,height:1000}}),app=await start(port,e)
 const calls:{name:string}[]=[];page.on('request',request=>{if(request.url().endsWith('/api/GatekeeperAudit'))calls.push(request.postDataJSON())})
 try{
  await page.goto(url);const token=await page.locator('meta[name=spk-ocular-api-token]').getAttribute('content')
  const response=await page.request.post(url+'/api/Configurations',{headers:{Authorization:`Bearer ${token}`,Origin:url},data:{command:'import',paths:[e.one]}});expect(response.ok()).toBe(true)
  await page.reload();await selectConnection(page,/^gatekeeper\b/);await connectSelected(page)
  const nav=page.getByRole('navigation',{name:'resources'});await nav.getByRole('textbox',{name:'Filter resources'}).fill('K8sRequiredLabels');await nav.getByRole('button',{name:'K8sRequiredLabels',exact:true}).click()
  let detailReads=0;page.on('response',response=>{if(response.url().endsWith('/api/GetResource')&&response.request().postDataJSON()?.name==='required-labels')detailReads++})
  await page.getByRole('grid',{name:'resources'}).getByRole('gridcell',{name:'required-labels',exact:true}).click()
  const drawer=page.locator('[data-resource-drawer]'),panel=drawer.locator('[data-gatekeeper-audit]');await expect(panel).toBeVisible();await expect.poll(()=>detailReads).toBeGreaterThanOrEqual(2)
  return {page,fixture,drawer,panel,calls,close:async()=>{await page.close();await stop(app);await fixture.close()}}
 }catch(error){await page.close();await stop(app);await fixture.close();throw error}
}
test('selected constraint audit is explicit, exact-source, capped, read-only and cancellable',async({browser})=>{
 const f=await launch(browser),{page,panel,fixture,calls}=f
 const selected=()=>fixture.requests.filter(r=>r.path.endsWith('/k8srequiredlabels/required-labels'))
 try{
  expect(calls).toHaveLength(0);const before=selected().length
  await panel.getByRole('button',{name:'Load constraint audit'}).focus();await page.keyboard.press('Enter')
  await expect(panel).toContainText('Reported total: 5 · Returned: 1 · Displayed: 1');expect(calls).toHaveLength(1);expect(selected()).toHaveLength(before+2)
  await expect(panel).toContainText('constraint-required-labels');await expect(panel).toContainText('RV 7');await expect(panel).toContainText('Producer reports more violations');await expect(panel).toContainText('scoped');await expect(panel).toContainText('warn, dryrun');await expect(panel).toContainText('Literal <script>not executed</script>');await expect(panel).not.toContainText('PRIVATE');await expect(panel).not.toContainText('UNVERIFIED-SUBJECT-UID');await expect(panel.locator('a')).toHaveCount(0);await expect(panel.locator('script')).toHaveCount(0)
  fixture.deny(true);await panel.getByRole('button',{name:'Load constraint audit'}).click();await expect(panel.getByRole('alert')).toBeVisible();await expect(panel).not.toContainText('Reported total: 5');await expect(panel).not.toContainText('PRIVATE-SERVER');fixture.deny(false)
  fixture.change(true);await panel.getByRole('button',{name:'Load constraint audit'}).click();await expect(panel.getByRole('alert')).toContainText('changed');await expect(panel).not.toContainText('Reported total: 5');fixture.change(false)
  fixture.replace(true);await panel.getByRole('button',{name:'Load constraint audit'}).click();await expect(panel.getByRole('alert')).toContainText('replaced');fixture.replace(false)
  await panel.getByRole('button',{name:'Load constraint audit'}).click();await expect(panel).toContainText('Reported total: 5')
  fixture.hold(true);const heldBefore=selected().length;await panel.getByRole('button',{name:'Load constraint audit'}).click();await expect.poll(()=>selected().length).toBe(heldBefore+1);await panel.getByRole('button',{name:'Cancel',exact:true}).click();await expect.poll(()=>fixture.cancellations.length).toBe(1);expect(fixture.cancellations[0]).toBeLessThan(20_000);await expect(panel).not.toContainText('Reported total: 5');fixture.hold(false)
  expect(fixture.requests.every(r=>r.method==='GET')).toBe(true);expect(fixture.requests.filter(r=>/\/(secrets|configmaps|nodes|log)(?:\/|$)/.test(r.path))).toHaveLength(0);expect(selected().slice(before).every(r=>Object.keys(r.query).length===0)).toBe(true);expect(selected().slice(0,before).every(r=>Object.keys(r.query).length===0||JSON.stringify(r.query)===JSON.stringify({includeObject:"Object"}))).toBe(true)
 }finally{writeFileSync(test.info().outputPath('gatekeeper-wire.json'),JSON.stringify({requests:fixture.requests,cancellations:fixture.cancellations,calls},null,2));await f.close()}
})
test('selected constraint audit keeps zero, missing, inconsistent and bounded evidence distinct and resets object consent',async({browser})=>{
 const f=await launch(browser),{page,panel,drawer,calls}=f
 try{
  for(const [name,text] of [['reported-zero','Reported total: 0 · Returned: 0 · Displayed: 0'],['missing-audit','Reported total: Unknown'],['inconsistent','Reported total and returned list are inconsistent'],['large-projection','Returned: 140 · Displayed: 128']]){
   const before=calls.length;await drawer.getByRole('button',{name:'Close',exact:true}).click();await page.getByRole('grid',{name:'resources'}).getByRole('gridcell',{name,exact:true}).click();await expect(drawer.getByRole('heading',{name,exact:true})).toBeVisible();await expect(panel.getByRole('button',{name:'Load constraint audit'})).toBeVisible();expect(calls).toHaveLength(before);await expect(panel).not.toContainText('Reported total:');await panel.getByRole('button',{name:'Load constraint audit'}).click();await expect(panel).toContainText(text);await expect(panel).toContainText('zero reported violations does not prove a pass')
  }
 }finally{await f.close()}
})
test('selected constraint audit fits eight locales and widths without repeated audit reads',async({browser})=>{
 const f=await launch(browser),{page,panel,drawer,calls}=f
 try{
  await panel.getByRole('button',{name:'Load constraint audit'}).click();await expect(panel).toContainText('descriptive-secret');expect(calls).toHaveLength(1)
  for(const code of languages){
   await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[code],exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang',code)
   for(const width of [1440,860]){
    await page.setViewportSize({width,height:1000});await panel.getByRole('heading',{level:3}).scrollIntoViewIfNeeded();expect(await drawer.evaluate(e=>e.scrollWidth<=e.clientWidth)).toBe(true);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:join(scratchRoot,`gatekeeper-${code}-${width}.png`)});await panel.locator('[data-gatekeeper-violation]').scrollIntoViewIfNeeded();await page.screenshot({path:join(scratchRoot,`gatekeeper-${code}-${width}-provenance.png`)})
   }
   await page.setViewportSize({width:1440,height:1000})
  }
  expect(calls).toHaveLength(1)
 }finally{writeFileSync(test.info().outputPath('gatekeeper-wire.json'),JSON.stringify({requests:f.fixture.requests,calls},null,2));await f.close()}
})
