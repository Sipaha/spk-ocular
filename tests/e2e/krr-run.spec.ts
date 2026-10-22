import {test,expect,type Browser,type Locator} from '@playwright/test'
import {mkdirSync,mkdtempSync,writeFileSync} from 'node:fs'
import {join} from 'node:path'
import {env,kubeconfig,writeAtomic,scratchRoot,selectConnection,connectSelected,openAppMenu} from './fixtures'
import {start,stop,freePort} from './restartApp'
import {krrFixture} from './krrFixture'
import {languages,languageNames} from '../../web/src/languages'
async function launch(browser:Browser){
 mkdirSync(scratchRoot,{recursive:true});const root=mkdtempSync(join(scratchRoot,'krr-run-')),profile=env(root),fixture=await krrFixture()
 writeAtomic(profile.one,kubeconfig('krr-runner','krr-runner').replace(/https:\/\/[^"\s]+/g,`http://127.0.0.1:${fixture.port}`).replace(/ns-krr-runner/g,'tools'))
 const port=await freePort(),url=`http://127.0.0.1:${port}`,page=await browser.newPage({viewport:{width:1440,height:1000}}),app=await start(port,profile)
 const commands:string[]=[];page.on('request',r=>{if(r.url().endsWith('/api/KRR'))commands.push(r.postDataJSON().command)})
 try{
  await page.goto(url);const token=await page.locator('meta[name=spk-ocular-api-token]').getAttribute('content')
  const imported=await page.request.post(url+'/api/Configurations',{headers:{Authorization:`Bearer ${token}`,Origin:url},data:{command:'import',paths:[profile.one]}});expect(imported.ok()).toBe(true)
  await page.reload();await selectConnection(page,/^krr-runner\b/);await connectSelected(page)
  return {page,fixture,commands,async open(){await openAppMenu(page);await page.getByRole('button',{name:'KRR report',exact:true}).click();return page.getByRole('dialog',{name:'KRR report',exact:true})},async close(){await page.close();await stop(app);await fixture.close()}}
 }catch(error){await page.close();await stop(app);await fixture.close();throw error}
}
async function prepare(dialog:Locator){
 await dialog.getByLabel('Namespace',{exact:true}).fill('tools');await dialog.getByLabel('Existing ServiceAccount').fill('reader');await dialog.getByLabel('Prometheus base URL (no credentials)').fill('http://metrics.tools.svc:9090')
 await dialog.getByLabel(/I confirm this Prometheus/).check();await dialog.getByLabel(/I reviewed this ServiceAccount/).check();await dialog.getByLabel(/This source requires no extra/).check()
 await dialog.getByRole('button',{name:'Review Job',exact:true}).click();await expect(dialog.getByRole('button',{name:'Start reviewed Job'})).toBeVisible();await expect(dialog).toContainText('namespace UID: ns-tools');await expect(dialog).toContainText('ServiceAccount UID: sa-reader')
}
test('reviewed KRR Job returns a backend report, cleans owned UIDs and survives panel/reload',async({browser})=>{
 const f=await launch(browser),{page,fixture}=f
 try{
  let dialog=await f.open();expect(f.commands.filter(c=>c==='start')).toHaveLength(0);await prepare(dialog)
  expect(fixture.requests.filter(r=>r.method==='POST'&&r.path.endsWith('/jobs'))).toHaveLength(0)
  await dialog.getByRole('button',{name:'Start reviewed Job'}).click();await expect(dialog).toContainText('Report collected; cleanup confirmed');await expect.poll(()=>fixture.remaining()).toEqual({jobs:0,pods:0})
  await dialog.getByRole('region',{name:/Backend runs/}).getByRole('button',{name:'Open report'}).click();await expect(dialog).toContainText('Pinned external engine');await expect(dialog).toContainText('cpu_min_value (millicores): 10');await expect(dialog).toContainText('memory_min_value (MiB): 100');await expect(dialog).toContainText('115343360');await expect(dialog).not.toContainText('PRIVATE-')
  await page.keyboard.press('Escape');dialog=await f.open();await expect(dialog).toContainText('Report collected; cleanup confirmed');await page.reload();dialog=await f.open();await expect(dialog).toContainText('Report collected; cleanup confirmed')
  expect(f.commands.filter(c=>c==='start')).toHaveLength(1)
  const creates=fixture.requests.filter(r=>r.method==='POST'&&r.path.endsWith('/jobs'));expect(creates).toHaveLength(1);expect(creates[0]).toMatchObject({suspended:true,fixedEngine:true})
  const deletes=fixture.requests.filter(r=>r.method==='DELETE');expect(deletes).toHaveLength(2);expect(deletes.every(r=>!!r.uid)).toBe(true)
  expect(fixture.requests.filter(r=>/\/(secrets|exec|configmaps)(?:\/|$)/.test(r.path))).toHaveLength(0)
 }finally{writeFileSync(test.info().outputPath('krr-run-wire.json'),JSON.stringify({requests:fixture.requests,commands:f.commands,remaining:fixture.remaining(),protocolFixtureOnly:true},null,2));await f.close()}
})
test('KRR continues after panel close and explicit cancel cleans the active Job and Pod',async({browser})=>{
 const f=await launch(browser),{page,fixture}=f;fixture.complete(false)
 try{
  let dialog=await f.open();await prepare(dialog);await dialog.getByRole('button',{name:'Start reviewed Job'}).click();await expect(dialog.getByRole('button',{name:'Cancel run'})).toBeVisible()
  await page.keyboard.press('Escape');expect(f.commands.filter(c=>c==='cancel')).toHaveLength(0);expect(fixture.remaining()).toEqual({jobs:1,pods:1})
  await page.reload();dialog=await f.open();await expect(dialog.getByRole('button',{name:'Cancel run'})).toBeVisible();await dialog.getByRole('button',{name:'Cancel run'}).click();await expect(dialog).toContainText('Run cancelled');await expect.poll(()=>fixture.remaining()).toEqual({jobs:0,pods:0});expect(f.commands.filter(c=>c==='start')).toHaveLength(1);expect(f.commands.filter(c=>c==='cancel')).toHaveLength(1)
 }finally{writeFileSync(test.info().outputPath('krr-cancel-wire.json'),JSON.stringify({requests:fixture.requests,commands:f.commands,remaining:fixture.remaining(),protocolFixtureOnly:true},null,2));await f.close()}
})
test('KRR run review and retained report remain readable in eight locales at two widths',async({browser})=>{
 const f=await launch(browser),{page}=f
 try{
  let dialog=await f.open();await prepare(dialog);await dialog.getByRole('button',{name:'Start reviewed Job'}).click();await expect(dialog).toContainText('Report collected; cleanup confirmed');await page.keyboard.press('Escape')
  for(const code of languages){
   await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[code],exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang',code)
   await openAppMenu(page);await page.locator('.app-utilities button').filter({hasText:/KRR/}).click();dialog=page.getByRole('dialog').last()
   const buttons=dialog.getByRole('button');await expect(dialog.locator('article')).toHaveCount(1)
   // Open the backend-owned report from its run entry; no direct run/result RPC.
   await dialog.locator('article').getByRole('button').click();await expect(dialog).toContainText('115343360')
   for(const width of [1440,860]){
    await page.setViewportSize({width,height:1000});await dialog.locator(':scope > div').evaluate(e=>{e.scrollTop=0});await dialog.getByRole('heading',{level:2}).scrollIntoViewIfNeeded();expect(await dialog.evaluate(e=>e.scrollWidth<=e.clientWidth)).toBe(true);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:join(scratchRoot,`krr-run-${code}-${width}.png`)})
    await dialog.locator('article').last().scrollIntoViewIfNeeded();await page.screenshot({path:join(scratchRoot,`krr-run-${code}-${width}-report.png`)})
   }
   expect(await buttons.count()).toBeGreaterThan(0);await page.keyboard.press('Escape');await page.setViewportSize({width:1440,height:1000})
  }
  expect(f.commands.filter(c=>c==='start')).toHaveLength(1)
 }finally{writeFileSync(test.info().outputPath('krr-locales-wire.json'),JSON.stringify({requests:f.fixture.requests,commands:f.commands,remaining:f.fixture.remaining(),protocolFixtureOnly:true},null,2));await f.close()}
})
