import {test,expect,type Browser} from '@playwright/test'
import {mkdirSync,mkdtempSync,writeFileSync} from 'node:fs'
import {join} from 'node:path'
import {env,kubeconfig,writeAtomic,scratchRoot,selectConnection,connectSelected,openAppMenu} from './fixtures'
import {start,stop,freePort} from './restartApp'
import {metricsHistoryFixture,historyUID} from './metrics-history-fixture'
import {languages,languageNames} from '../../web/src/languages'
async function launch(browser:Browser){
 mkdirSync(scratchRoot,{recursive:true});const root=mkdtempSync(join(scratchRoot,'metrics-history-')),e=env(root),fixture=await metricsHistoryFixture()
 writeAtomic(e.one,kubeconfig('history','history').replace(/https:\/\/[^"\s]+/g,`http://127.0.0.1:${fixture.kube.port}`).replace(/ns-history/g,'blue'))
 const port=await freePort(),url=`http://127.0.0.1:${port}`,page=await browser.newPage({viewport:{width:1440,height:1000}}),app=await start(port,e)
 try{
  await page.goto(url);const token=await page.locator('meta[name=spk-ocular-api-token]').getAttribute('content');const response=await page.request.post(url+'/api/Configurations',{headers:{Authorization:`Bearer ${token}`,Origin:url},data:{command:'import',paths:[e.one]}});expect(response.ok()).toBe(true)
  await page.reload();await selectConnection(page,/^history\b/);await connectSelected(page)
  const nav=page.getByRole('navigation',{name:'resources'});await nav.getByRole('textbox',{name:'Filter resources'}).fill('Pods');await nav.getByRole('button',{name:'Pods',exact:true}).click()
  let reads=0;page.on('response',r=>{if(r.url().endsWith('/api/GetResource')&&r.request().postDataJSON()?.name==='web')reads++})
  await page.getByRole('grid',{name:'resources'}).getByRole('gridcell',{name:'web',exact:true}).click()
  const drawer=page.locator('[data-resource-drawer]'),panel=drawer.locator('[data-metrics-history]');await expect(panel).toBeVisible();await expect.poll(()=>reads).toBeGreaterThanOrEqual(2)
  return {page,fixture,drawer,panel,close:async()=>{writeFileSync(test.info().outputPath('metrics-history-wire.json'),JSON.stringify({kube:fixture.kube.requests,source:fixture.requests},null,2));await page.close();await stop(app);await fixture.close()}}
 }catch(error){await page.close();await stop(app);await fixture.close();throw error}
}
async function configure(f:Awaited<ReturnType<typeof launch>>){await f.panel.getByLabel('Prometheus base URL').fill(f.fixture.sourceURL);await f.panel.getByLabel('Bearer token (optional, memory only)').fill('fixture-history-token');await f.panel.getByLabel('Exact cluster value').fill('alpha');await f.panel.getByRole('button',{name:'Load history',exact:true}).click();await expect(f.panel.locator('[data-history-metric="cpu"] svg')).toBeVisible()}
test('actual Pod history is explicit, UID-scoped, gap-aware and keeps denied counters unavailable',async({browser})=>{
 const f=await launch(browser)
 try{
  expect(f.fixture.requests).toHaveLength(0);await configure(f);await expect(f.panel).toContainText(historyUID);await expect(f.panel).not.toContainText('PRIVATE');await expect(f.panel).not.toContainText('fixture-history-token');expect(f.fixture.requests).toHaveLength(3)
  expect(f.fixture.requests.every(r=>r.authorized&&r.method==='GET'&&r.query.includes(historyUID)&&r.query.includes('cluster="alpha"')&&r.step==='60')).toBe(true)
  f.fixture.setMode('denied');await f.panel.getByRole('button',{name:'Load history',exact:true}).click();await expect(f.panel.locator('[data-history-metric="throttling"]')).toContainText('Source denied access.');await expect(f.panel.locator('[data-history-metric="throttling"] svg')).toHaveCount(0)
  f.fixture.setMode('foreign');await f.panel.getByRole('button',{name:'Load history',exact:true}).click();await expect(f.panel.locator('[data-history-metric="cpu"]')).toContainText('Unavailable:');await expect(f.panel.locator('svg')).toHaveCount(0)
  f.fixture.setMode('hold');await f.panel.getByRole('button',{name:'Load history',exact:true}).click();await expect.poll(()=>f.fixture.requests.length).toBeGreaterThan(9);await f.panel.getByRole('button',{name:'Cancel',exact:true}).click();await expect.poll(()=>f.fixture.cancelled()).toBeGreaterThan(0);await expect(f.panel.locator('svg')).toHaveCount(0)
  expect(f.fixture.kube.requests.every(r=>r.method==='GET')).toBe(true);expect(f.fixture.kube.requests.filter(r=>/\/(secrets|configmaps|nodes|metrics|log)(?:\/|$)/.test(r.path))).toHaveLength(0)
 }finally{await f.close()}
})
test('actual Pod history charts and source controls fit eight locales wide and compact',async({browser})=>{
 const f=await launch(browser)
 try{
  await configure(f)
  for(const code of languages){
   await openAppMenu(f.page);await f.page.locator('.language-menu button').click();await f.page.getByRole('option',{name:languageNames[code],exact:true}).click();await expect(f.page.locator('html')).toHaveAttribute('lang',code)
   for(const width of [1440,375]){await f.page.setViewportSize({width,height:1000});await f.panel.getByRole('heading',{level:3}).scrollIntoViewIfNeeded();expect(await f.drawer.evaluate(e=>e.scrollWidth<=e.clientWidth)).toBe(true);expect(await f.page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await f.page.screenshot({path:join(scratchRoot,`metrics-history-${code}-${width}.png`)});await f.panel.locator('[data-history-metric="throttling"]').scrollIntoViewIfNeeded();await f.page.screenshot({path:join(scratchRoot,`metrics-history-${code}-${width}-charts.png`)})}
   await f.page.setViewportSize({width:1440,height:1000})
  }
 }finally{await f.close()}
})
