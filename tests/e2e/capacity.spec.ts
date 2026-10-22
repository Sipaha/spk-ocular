import {test,expect,type Browser} from '@playwright/test'
import {mkdirSync,mkdtempSync,writeFileSync} from 'node:fs'
import {join} from 'node:path'
import {env,kubeconfig,writeAtomic,scratchRoot,selectConnection,connectSelected,openAppMenu} from './fixtures'
import {start,stop,freePort} from './restartApp'
import {capacityServer} from './capacity-fixture'
import {languages,languageNames} from '../../web/src/languages'
async function launch(browser:Browser){
 mkdirSync(scratchRoot,{recursive:true});const root=mkdtempSync(join(scratchRoot,'capacity-')),e=env(root),fixture=await capacityServer()
 writeAtomic(e.one,kubeconfig('capacity','capacity').replace(/https:\/\/[^"\s]+/g,`http://127.0.0.1:${fixture.port}`).replace(/ns-capacity/g,'blue'))
 const port=await freePort(),url=`http://127.0.0.1:${port}`,page=await browser.newPage({viewport:{width:1440,height:1000}}),app=await start(port,e)
 try{
  await page.goto(url);const token=await page.locator('meta[name=spk-ocular-api-token]').getAttribute('content')
  const imported=await page.request.post(url+'/api/Configurations',{headers:{Authorization:`Bearer ${token}`,Origin:url},data:{command:'import',paths:[e.one]}});expect(imported.ok()).toBe(true)
  await page.reload();await selectConnection(page,/^capacity\b/);await connectSelected(page)
  const nav=page.getByRole('navigation',{name:'resources'});await nav.getByRole('textbox',{name:'Filter resources'}).fill('Nodes');await nav.getByRole('button',{name:'Nodes',exact:true}).click()
  let detailReads=0;page.on('response',r=>{if(r.url().endsWith('/api/GetResource')&&r.request().postDataJSON()?.name==='worker')detailReads++})
  await page.getByRole('grid',{name:'resources'}).getByRole('gridcell',{name:'worker',exact:true}).click()
  const drawer=page.locator('[data-resource-drawer]'),panel=drawer.locator('[data-node-capacity]');await expect(panel).toBeVisible();await expect.poll(()=>detailReads).toBeGreaterThanOrEqual(2)
  return {page,drawer,panel,fixture,close:async()=>{await page.close();await stop(app);await fixture.close()}}
 }catch(error){await page.close();await stop(app);await fixture.close();throw error}
}
test('actual capacity snapshot is explicit and namespace-scoped with denial, cancel and UID-safe navigation',async({browser})=>{
 const f=await launch(browser),{page,panel,drawer,fixture}=f
 const acquisitions=()=>fixture.requests.filter(r=>r.path==='/api/v1/namespaces/blue/pods'&&r.query.limit==='32')
 try{
  expect(acquisitions()).toHaveLength(0);await expect(panel.getByRole('button',{name:'Load namespace requests'})).toBeDisabled();await panel.getByLabel('Exact namespace').fill('blue');expect(acquisitions()).toHaveLength(0)
  await panel.getByRole('button',{name:'Load namespace requests'}).click();await expect(panel).toContainText('510m');await expect(panel).toContainText('136Mi');await expect(panel).toContainText('Complete bounded read');await expect(panel).not.toContainText('PRIVATE');expect(acquisitions()).toHaveLength(1)
  fixture.denyPods(true);await panel.getByRole('button',{name:'Load namespace requests'}).click();await expect(panel).toContainText('forbidden');await expect(panel).not.toContainText('510m');await expect(panel.getByRole('button',{name:'blue/web'})).toHaveCount(0)
  fixture.denyPods(false);fixture.holdPods(true);const before=fixture.cancelled();await panel.getByRole('button',{name:'Load namespace requests'}).click();await expect.poll(()=>acquisitions().length).toBe(3);await panel.getByRole('button',{name:'Cancel',exact:true}).click();await expect.poll(()=>fixture.cancelled()).toBeGreaterThan(before);await expect(panel).not.toContainText('510m')
  fixture.holdPods(false);await panel.getByRole('button',{name:'Load namespace requests'}).click();await expect(panel).toContainText('510m');await panel.getByRole('button',{name:'blue/web'}).click();await expect(drawer.getByRole('heading',{name:'web',exact:true})).toBeVisible();await drawer.getByRole('tab',{name:'YAML',exact:true}).click();await expect(drawer).toContainText('pod-web')
  expect(fixture.requests.every(r=>r.method==='GET')).toBe(true);expect(fixture.requests.filter(r=>/\/(secrets|configmaps|log)(?:\/|$)/.test(r.path))).toHaveLength(0)
 }finally{writeFileSync(test.info().outputPath('capacity-wire.json'),JSON.stringify({requests:fixture.requests,cancelled:fixture.cancelled(),realInfrastructure:false},null,2));await f.close()}
})
test('capacity declaration ledger fits eight locales at two widths',async({browser})=>{
 const f=await launch(browser),{page,panel,drawer}=f
 try{
  await panel.getByLabel('Exact namespace').fill('blue');await panel.getByRole('button',{name:'Load namespace requests'}).click();await expect(panel).toContainText('510m')
  for(const code of languages){
   await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[code],exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang',code)
   for(const width of [1440,860]){
    await page.setViewportSize({width,height:1000});await panel.getByRole('heading',{level:3}).scrollIntoViewIfNeeded();expect(await drawer.evaluate(e=>e.scrollWidth<=e.clientWidth)).toBe(true);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:join(scratchRoot,`capacity-${code}-${width}.png`)});await panel.locator('article').last().scrollIntoViewIfNeeded();await page.screenshot({path:join(scratchRoot,`capacity-${code}-${width}-pods.png`)})
   };await page.setViewportSize({width:1440,height:1000})
  }
 }finally{await f.close()}
})
