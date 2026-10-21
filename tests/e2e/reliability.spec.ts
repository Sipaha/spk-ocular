import {test,expect,type Browser} from '@playwright/test'
import {mkdirSync,mkdtempSync,writeFileSync} from 'node:fs'
import {join} from 'node:path'
import {env,kubeconfig,writeAtomic,scratchRoot,selectConnection,connectSelected,openAppMenu} from './fixtures'
import {start,stop,freePort} from './restartApp'
import {reliabilityServer} from './reliability-fixture'
import {languages,languageNames} from '../../web/src/languages'
async function launch(browser:Browser){
 mkdirSync(scratchRoot,{recursive:true});const root=mkdtempSync(join(scratchRoot,'reliability-')),e=env(root),fixture=await reliabilityServer()
 writeAtomic(e.one,kubeconfig('reliability','reliability').replace(/https:\/\/[^"\s]+/g,`http://127.0.0.1:${fixture.port}`).replace(/ns-reliability/g,'blue'))
 const port=await freePort(),url=`http://127.0.0.1:${port}`,page=await browser.newPage({viewport:{width:1440,height:1000}}),app=await start(port,e)
 try {
 await page.goto(url);const token=await page.locator('meta[name=spk-ocular-api-token]').getAttribute('content')
 const response=await page.request.post(url+'/api/Configurations',{headers:{Authorization:`Bearer ${token}`,Origin:url},data:{command:'import',paths:[e.one]}});expect(response.ok()).toBe(true)
 await page.reload();await selectConnection(page,/^reliability\b/);await connectSelected(page)
 const nav=page.getByRole('navigation',{name:'resources'});await nav.getByRole('textbox',{name:'Filter resources'}).fill('Deployments');await nav.getByRole('button',{name:'Deployments',exact:true}).click()
 let detailReads=0;page.on('response',response=>{if(response.url().endsWith('/api/GetResource')&&response.request().postDataJSON()?.name==='web')detailReads++})
 const grid=page.getByRole('grid',{name:'resources'});await grid.getByRole('gridcell',{name:'web',exact:true}).click()
 const drawer=page.locator('[data-resource-drawer]'),panel=drawer.locator('[data-resource-reliability]');await expect(panel).toBeVisible();await expect.poll(()=>detailReads).toBeGreaterThanOrEqual(2)
 return {page,fixture,drawer,panel,close:async()=>{await page.close();await stop(app);await fixture.close()}}
 } catch(error) {await page.close();await stop(app);await fixture.close();throw error}
}
test('actual provider reliability is opt-in, preserves stale PDB uncertainty and guarded source identity',async({browser})=>{
 const f=await launch(browser),{page,panel,fixture,drawer}=f
 try{
  expect(fixture.requests.filter(r=>r.path.includes('/poddisruptionbudgets'))).toHaveLength(0)
  await panel.getByRole('button',{name:'Load configuration checks'}).click();await expect(panel).toContainText('25% → 0');await expect(panel).toContainText('25% → 1');await expect(panel).toContainText('Template-label candidate');await expect(panel).toContainText('Unknown / incomplete');await expect(panel).not.toContainText('PRIVATE')
  fixture.denyPDB(true);await panel.getByRole('button',{name:'Load configuration checks'}).click();await expect(panel).toContainText('PDB — Unknown / incomplete');await expect(panel.getByRole('button',{name:'policy/poddisruptionbudgets/budget'})).toHaveCount(0)
  fixture.denyPDB(false);await panel.getByRole('button',{name:'Load configuration checks'}).click();await panel.getByRole('button',{name:'policy/poddisruptionbudgets/budget'}).first().click();await expect(drawer.getByRole('heading',{name:'budget',exact:true})).toBeVisible();await drawer.getByRole('tab',{name:'YAML',exact:true}).click();await expect(drawer).toContainText('pdb-budget')
  expect(fixture.requests.every(r=>r.method==='GET')).toBe(true);expect(fixture.requests.filter(r=>/\/(secrets|configmaps|nodes|metrics|log)(?:\/|$)/.test(r.path))).toHaveLength(0)
 }finally{writeFileSync(test.info().outputPath('reliability-wire.json'),JSON.stringify(fixture.requests,null,2));await f.close()}
})
test('actual reliability fits eight locales at wide and compact widths',async({browser})=>{
 const f=await launch(browser),{page,panel,drawer}=f
 try{
  await panel.getByRole('button',{name:'Load configuration checks'}).click();await expect(panel).toContainText('25% → 0')
  for(const code of languages){
   await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[code],exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang',code)
   for(const width of [1440,375]){await page.setViewportSize({width,height:1000});await panel.getByRole('heading',{level:3}).scrollIntoViewIfNeeded();expect(await drawer.evaluate(e=>e.scrollWidth<=e.clientWidth)).toBe(true);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:join(scratchRoot,`reliability-${code}-${width}.png`)});await panel.locator('[data-reliability-rule="kubernetes.reliability.hpa"]').last().scrollIntoViewIfNeeded();await page.screenshot({path:join(scratchRoot,`reliability-${code}-${width}-links.png`)})}
   await page.setViewportSize({width:1440,height:1000})
  }
 }finally{await f.close()}
})

test('reliability inspection retains a held YAML draft through the ordinary leave guard',async({browser})=>{
 const f=await launch(browser),{page,panel,drawer}=f
 try{
  await panel.getByRole('button',{name:'Load configuration checks'}).click();await expect(panel).toContainText('25% → 0')
  await drawer.getByRole('tab',{name:'YAML',exact:true}).click();await drawer.getByRole('button',{name:'Edit',exact:true}).click();await drawer.locator('.cm-content').click();await page.keyboard.press('Control+End');await page.keyboard.type('\n# retained reliability draft')
  await drawer.getByRole('tab',{name:'Details',exact:true}).click();const prompt=page.getByRole('alertdialog',{name:'Discard edits?',exact:true});await expect(prompt).toBeVisible();await prompt.getByRole('button',{name:'Keep editing',exact:true}).click();await expect(drawer.locator('.cm-content')).toContainText('retained reliability draft');await expect(drawer.getByRole('heading',{name:'web',exact:true})).toBeVisible()
  expect(f.fixture.requests.every(r=>r.method==='GET')).toBe(true)
 }finally{await f.close()}
})
