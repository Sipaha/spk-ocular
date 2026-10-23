import {test,expect,type Browser} from '@playwright/test'
import {mkdirSync,mkdtempSync,writeFileSync,readFileSync,existsSync} from 'node:fs'
import {join} from 'node:path'
import {env,kubeconfig,writeAtomic,scratchRoot,selectConnection,openAppMenu} from './fixtures'
import {start,stop,freePort} from './restartApp'
import {capacityServer} from './capacity-fixture'
import {fixture} from '../../web/src/offlineAudit/fixture'
import {languages,languageNames} from '../../web/src/languages'
async function launch(browser:Browser){
 mkdirSync(scratchRoot,{recursive:true});const root=mkdtempSync(join(scratchRoot,'offline-audit-')),profile=env(root),source=await capacityServer()
 writeAtomic(profile.one,kubeconfig('audit','audit','other').replace(/https:\/\/[^"\s]+/g,`http://127.0.0.1:${source.port}`))
 const port=await freePort(),url=`http://127.0.0.1:${port}`,app=await start(port,profile),page=await browser.newPage({viewport:{width:1440,height:1000}})
 const requests:{path:string;command?:string;bytes?:number}[]=[],responses:string[]=[]
 page.on('request',r=>{const path=new URL(r.url()).pathname;if(path==='/api/OfflineAudit'){const b=r.postDataJSON();requests.push({path,command:b.command,bytes:b.data?Buffer.from(b.data,'base64').length:0})}else requests.push({path})})
 page.on('response',r=>{if(r.url().endsWith('/api/OfflineAudit'))void r.text().then(t=>responses.push(t)).catch(()=>{})})
 try{
  await page.goto(url);const token=await page.locator('meta[name=spk-ocular-api-token]').getAttribute('content');const imported=await page.request.post(url+'/api/Configurations',{headers:{Authorization:`Bearer ${token}`,Origin:url},data:{command:'import',paths:[profile.one]}});expect(imported.ok()).toBe(true)
  await page.reload();await selectConnection(page,/^audit\b/);await expect(page.getByRole('button',{name:'Connect',exact:true})).toBeVisible()
  return {page,source,requests,responses,close:async()=>{await page.close();await stop(app);await source.close();for(const n of ['ocular.db','ocular.db-wal']){const p=join(profile.dataDir,n);if(existsSync(p)){const b=readFileSync(p);for(const literal of ['PRIVATE-','cpuRequestsMissing','readinessProbeMissing'])expect(b.includes(Buffer.from(literal))).toBe(false)}}}}
 }catch(e){await page.close();await stop(app);await source.close();throw e}
}
const data=()=>({name:'PRIVATE-audit.json',mimeType:'application/json',buffer:Buffer.from(JSON.stringify(fixture()))})
async function open(page:Awaited<ReturnType<typeof launch>>['page']){await openAppMenu(page);await page.locator('.app-utilities').getByRole('button',{name:'Polaris audit report',exact:true}).click();return page.getByRole('dialog',{name:'Polaris audit report',exact:true})}
test('offline Polaris import is explicit and bounded, retains only projection, never connects or resolves names',async({browser})=>{
 const f=await launch(browser),{page}=f
 try{
  let dialog=await open(page);const input=dialog.getByLabel('Choose JSON report');await expect(input).toBeDisabled();await dialog.getByRole('checkbox').check()
  const choose=dialog.getByRole('button',{name:'Choose JSON report',exact:true})
  await expect(input).toBeEnabled();await expect(choose).toBeEnabled();await choose.focus();await expect(choose).toBeFocused()
  const chooser=page.waitForEvent('filechooser');const activate=choose.press('Enter');await (await chooser).setFiles(data());await activate
  const evidence=dialog.getByRole('region',{name:'Imported evidence'});await expect(evidence).toContainText('Reported objects: 1 · Check occurrences: 3');await expect(evidence).toContainText('cpuRequestsMissing');await expect(evidence).toContainText('Reported failure');await expect(evidence).toContainText('Reported pass');await expect(evidence).toContainText('Coverage is unknown');await expect(dialog).not.toContainText('PRIVATE-');await expect(dialog.locator('a,script')).toHaveCount(0)
  const imports=()=>f.requests.filter(r=>r.command==='import').length;const before=imports();await input.setInputFiles({...data(),buffer:Buffer.alloc((1<<20)+1,32)});await expect(dialog.getByRole('alert')).toBeVisible();expect(imports()).toBe(before);await expect(dialog).not.toContainText('cpuRequestsMissing')
  await input.setInputFiles({...data(),buffer:Buffer.from('{"PRIVATE-error":')});await expect(dialog.getByRole('alert')).toBeVisible();await expect(dialog).not.toContainText('cpuRequestsMissing')
  const empty={...fixture(),AuditTime:'0001-01-01T00:00:00Z',Results:[]};await input.setInputFiles({...data(),buffer:Buffer.from(JSON.stringify(empty))});await expect(evidence).toContainText('No reported checks');await expect(evidence).toContainText('Reported audit time: Unknown')
  await input.setInputFiles(data());await expect(evidence).toContainText('cpuRequestsMissing');await page.keyboard.press('Escape');await expect(dialog).not.toBeVisible();dialog=await open(page);await expect(dialog.getByRole('checkbox')).not.toBeChecked();await expect(dialog).not.toContainText('cpuRequestsMissing');await dialog.getByRole('button',{name:'Open report'}).first().click();await expect(dialog).toContainText('cpuRequestsMissing')
  await page.keyboard.press('Escape');await selectConnection(page,/^other\b/);dialog=await open(page);await expect(dialog.getByRole('checkbox')).not.toBeChecked();await dialog.getByRole('button',{name:'Open report'}).first().click();await expect(dialog).toContainText('another target or configuration context');await expect(dialog).toContainText('cpuRequestsMissing');await page.keyboard.press('Escape')
  await page.reload();dialog=await open(page);await expect(dialog).not.toContainText('cpuRequestsMissing');await dialog.getByRole('button',{name:'Open report'}).first().click();await expect(dialog).toContainText('cpuRequestsMissing');await dialog.getByRole('button',{name:'Forget',exact:true}).first().click();await expect(dialog).not.toContainText('cpuRequestsMissing')
  expect(f.source.requests).toHaveLength(0);expect(f.responses.join('')).not.toContain('PRIVATE-');expect(f.requests.filter(r=>/ConnectTarget|GetResource|NodeCapacity|KRR$/.test(r.path))).toHaveLength(0)
 }finally{writeFileSync(test.info().outputPath('offline-audit-wire.json'),JSON.stringify({requests:f.requests,clusterRequests:f.source.requests,localImportOnly:true,realInfrastructure:false},null,2));await f.close()}
})
test('offline Polaris evidence remains readable in eight locales at wide and compact widths',async({browser})=>{
 const f=await launch(browser),{page}=f
 try{
  for(const code of languages){
   await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[code],exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang',code)
   await openAppMenu(page);await page.locator('.app-utilities button').filter({hasText:/Polaris/u}).click();const dialog=page.getByRole('dialog').last();await dialog.getByRole('checkbox').check();await dialog.locator('input[type=file]').setInputFiles(data());await expect(dialog).toContainText('cpuRequestsMissing')
   for(const width of [1440,860]){
    await page.setViewportSize({width,height:1000});await dialog.locator(':scope > div').evaluate(e=>{e.scrollTop=0});expect(await dialog.evaluate(e=>e.scrollWidth<=e.clientWidth)).toBe(true);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:join(scratchRoot,`offline-audit-${code}-${width}.png`)});await dialog.locator('article').last().scrollIntoViewIfNeeded();await page.screenshot({path:join(scratchRoot,`offline-audit-${code}-${width}-checks.png`)})
   }
   await page.keyboard.press('Escape');await page.setViewportSize({width:1440,height:1000})
  }
  expect(f.source.requests).toHaveLength(0)
 }finally{await f.close()}
})

test('renderer expiry removes displayed audit evidence without polling or reviving expired replies',async({browser})=>{
 const f=await launch(browser),{page}=f
 try{
  await page.clock.install();const dialog=await open(page);await dialog.getByRole('checkbox').check();await dialog.locator('input[type=file]').setInputFiles(data());await expect(dialog).toContainText('cpuRequestsMissing');const calls=f.requests.filter(r=>r.path==='/api/OfflineAudit').length
  await page.clock.fastForward(2*60*60*1000+1000);await expect(dialog).not.toContainText('cpuRequestsMissing');await expect(dialog.getByRole('button',{name:'Open report'})).toHaveCount(0);expect(f.requests.filter(r=>r.path==='/api/OfflineAudit')).toHaveLength(calls);expect(f.source.requests).toHaveLength(0)
 }finally{await f.close()}
})
