import {test,expect,type Browser} from '@playwright/test'
import {mkdirSync,mkdtempSync,writeFileSync} from 'node:fs'
import {join} from 'node:path'
import {env,kubeconfig,writeAtomic,scratchRoot,selectConnection,connectSelected,openAppMenu} from './fixtures'
import {start,stop,freePort} from './restartApp'
import {vpaServer} from './vpa-fixture'
import {languages,languageNames} from '../../web/src/languages'
async function launch(browser:Browser){
 mkdirSync(scratchRoot,{recursive:true});const root=mkdtempSync(join(scratchRoot,'vpa-')),e=env(root),fixture=await vpaServer()
 writeAtomic(e.one,kubeconfig('vpa','vpa').replace(/https:\/\/[^"\s]+/g,`http://127.0.0.1:${fixture.port}`).replace(/ns-vpa/g,'blue'))
 const port=await freePort(),url=`http://127.0.0.1:${port}`,page=await browser.newPage({viewport:{width:1440,height:1000}}),app=await start(port,e)
 try {
 await page.goto(url);const token=await page.locator('meta[name=spk-ocular-api-token]').getAttribute('content')
 const response=await page.request.post(url+'/api/Configurations',{headers:{Authorization:`Bearer ${token}`,Origin:url},data:{command:'import',paths:[e.one]}});expect(response.ok()).toBe(true)
 await page.reload();await selectConnection(page,/^vpa\b/);await connectSelected(page)
 const nav=page.getByRole('navigation',{name:'resources'});await nav.getByRole('textbox',{name:'Filter resources'}).fill('Deployments');await nav.getByRole('button',{name:'Deployments',exact:true}).click()
 let detailReads=0;page.on('response',response=>{if(response.url().endsWith('/api/GetResource')&&response.request().postDataJSON()?.name==='web')detailReads++})
 const grid=page.getByRole('grid',{name:'resources'});await grid.getByRole('gridcell',{name:'web',exact:true}).click()
 const drawer=page.locator('[data-resource-drawer]'),panel=drawer.locator('[data-resource-vpa]');await expect(panel).toBeVisible();await expect.poll(()=>detailReads).toBeGreaterThanOrEqual(2)
 return {page,fixture,drawer,panel,close:async()=>{await page.close();await stop(app);await fixture.close()}}
 } catch(error) {await page.close();await stop(app);await fixture.close();throw error}
}
test('actual VPA inspection is explicit, source-pinned, read-only, ambiguity-aware and cancellable',async({browser})=>{
 const f=await launch(browser),{page,panel,fixture,drawer}=f
 try{
  expect(fixture.requests.filter(r=>r.path.includes('/verticalpodautoscalers'))).toHaveLength(0)
  await panel.getByRole('button',{name:'Load VPA recommendations'}).click();await expect(panel).toContainText('250m');await expect(panel).toContainText('custom-recommender');await expect(panel).toContainText('Update mode: Off');await expect(panel).toContainText('deploy-web');await expect(panel).toContainText('vpa-sizing');await expect(panel).toContainText('hpa-scale');await expect(panel).not.toContainText('PRIVATE')
  fixture.denyVPA(true);await panel.getByRole('button',{name:'Load VPA recommendations'}).click();await expect(panel.locator('[data-vpa-coverage]')).toContainText('Access denied');await expect(panel.locator('[data-vpa-recommendation]')).toHaveCount(0)
  fixture.denyVPA(false);fixture.vpa.status.observedGeneration=1;await panel.getByRole('button',{name:'Load VPA recommendations'}).click();await expect(panel).toContainText('Known stale generation');await expect(panel.locator('[data-vpa-recommendation]')).toHaveCount(0)
  fixture.vpa.status.observedGeneration=2;const second=JSON.parse(JSON.stringify(fixture.vpa));second.metadata.name='second';second.metadata.uid='vpa-second';fixture.data.verticalpodautoscalers.push(second)
  await panel.getByRole('button',{name:'Load VPA recommendations'}).click();await expect(panel.locator('[data-vpa-coverage]')).toContainText('Multiple matching VPA');await expect(panel.locator('[data-vpa-source]')).toHaveCount(2)
  fixture.holdVPA(true);const before=fixture.requests.filter(r=>r.path.includes('/verticalpodautoscalers')).length;await panel.getByRole('button',{name:'Load VPA recommendations'}).click();await expect.poll(()=>fixture.requests.filter(r=>r.path.includes('/verticalpodautoscalers')).length).toBeGreaterThan(before);await panel.getByRole('button',{name:'Cancel',exact:true}).click();await expect.poll(()=>fixture.cancelled()).toBeGreaterThan(0);await expect(panel.locator('[data-vpa-recommendation]')).toHaveCount(0)
  fixture.holdVPA(false);fixture.data.verticalpodautoscalers.pop();await panel.getByRole('button',{name:'Load VPA recommendations'}).click();await expect(panel).toContainText('250m');expect(fixture.requests.filter(r=>r.path.includes('/verticalpodautoscalers')).every(r=>r.path.includes('/namespaces/blue/')&&r.query.watch!=='true')).toBe(true);const navigationStart=fixture.requests.length;await panel.getByRole('button',{name:'autoscaling.k8s.io/verticalpodautoscalers/sizing'}).click();await expect(drawer.getByRole('heading',{name:'sizing',exact:true})).toBeVisible();await drawer.getByRole('tab',{name:'YAML',exact:true}).click();await expect(drawer).toContainText('vpa-sizing')
  expect(fixture.requests.every(r=>r.method==='GET')).toBe(true);expect(fixture.requests.filter(r=>/\/(secrets|configmaps|nodes|log)(?:\/|$)/.test(r.path))).toHaveLength(0)
  expect(fixture.requests.filter(r=>r.path.includes('/verticalpodautoscalers')).every(r=>r.path.includes('/namespaces/blue/'))).toBe(true);expect(fixture.requests.slice(navigationStart).filter(r=>r.query.watch==='true').every(r=>r.path.includes('/verticalpodautoscalers')&&r.query.fieldSelector==='metadata.name=sizing')).toBe(true)
 }finally{writeFileSync(test.info().outputPath('vpa-wire.json'),JSON.stringify(fixture.requests,null,2));await f.close()}
})
test('actual VPA provenance, quantities and policy fit eight locales at wide and compact widths',async({browser})=>{
 const f=await launch(browser),{page,panel,drawer}=f
 try{
  await panel.getByRole('button',{name:'Load VPA recommendations'}).click();await expect(panel).toContainText('250m')
  for(const code of languages){
   await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[code],exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang',code)
   for(const width of [1440,375]){
    await page.setViewportSize({width,height:1000});await panel.getByRole('heading',{level:3}).scrollIntoViewIfNeeded();expect(await drawer.evaluate(e=>e.scrollWidth<=e.clientWidth)).toBe(true);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:join(scratchRoot,`vpa-${code}-${width}-top.png`)});await panel.locator('[data-vpa-recommendation]').scrollIntoViewIfNeeded();await page.screenshot({path:join(scratchRoot,`vpa-${code}-${width}-recommendation.png`)});await panel.locator('article').last().scrollIntoViewIfNeeded();await page.screenshot({path:join(scratchRoot,`vpa-${code}-${width}-hpa.png`)})
   }
   await page.setViewportSize({width:1440,height:1000})
  }
 }finally{writeFileSync(test.info().outputPath('vpa-wire.json'),JSON.stringify(f.fixture.requests,null,2));await f.close()}
})
