import {test,expect,type Browser} from '@playwright/test'
import {mkdirSync,mkdtempSync,writeFileSync} from 'node:fs'
import {join} from 'node:path'
import {env,kubeconfig,writeAtomic,scratchRoot,selectConnection,connectSelected,openAppMenu} from './fixtures'
import {start,stop,freePort} from './restartApp'
import {certificateServer} from './certificate-fixture'
import {languages,languageNames} from '../../web/src/languages'
async function launch(browser:Browser){
 mkdirSync(scratchRoot,{recursive:true});const root=mkdtempSync(join(scratchRoot,'certificate-')),e=env(root),fixture=await certificateServer()
 writeAtomic(e.one,kubeconfig('cert-status','cert-status').replace(/https:\/\/[^"\s]+/g,`http://127.0.0.1:${fixture.port}`).replace(/ns-cert-status/g,'blue'))
 const port=await freePort(),url=`http://127.0.0.1:${port}`,page=await browser.newPage({viewport:{width:1440,height:1000}}),app=await start(port,e)
 try {
 await page.goto(url);const token=await page.locator('meta[name=spk-ocular-api-token]').getAttribute('content')
 const response=await page.request.post(url+'/api/Configurations',{headers:{Authorization:`Bearer ${token}`,Origin:url},data:{command:'import',paths:[e.one]}});expect(response.ok()).toBe(true)
 await page.reload();await selectConnection(page,/^cert-status\b/);await connectSelected(page)
 const nav=page.getByRole('navigation',{name:'resources'});await nav.getByRole('textbox',{name:'Filter resources'}).fill('Certificates');await nav.getByRole('button',{name:'Certificates',exact:true}).click()
 const grid=page.getByRole('grid',{name:'resources'});await grid.getByRole('gridcell',{name:'web-tls',exact:true}).click()
 const drawer=page.locator('[data-resource-drawer]'),panel=drawer.locator('[data-certificate-status]');await expect(panel).toBeVisible()
 return {page,fixture,drawer,panel,close:async()=>{await page.close();await stop(app);await fixture.close()}}
 } catch(error) {await page.close();await stop(app);await fixture.close();throw error}
}
test('real Certificate status keeps reported evidence separate, preserves UID navigation and performs no Secret reads or writes',async({browser})=>{
 const f=await launch(browser),{page,panel,fixture,drawer}=f
 try{
  expect(fixture.requests.filter(r=>r.accept.includes('PartialObjectMetadata')&&r.path.endsWith('/issuers/public'))).toHaveLength(0)
  await panel.getByRole('button',{name:'Load Certificate status'}).click();await expect(panel).toContainText('Reported expiry within 7 days');await expect(panel).toContainText('Stale generation');await expect(panel).toContainText('web.example');await expect(panel.getByRole('button',{name:'networking.k8s.io/ingresses/entry'})).toBeVisible()
  fixture.denyConsumers(true);await panel.getByRole('button',{name:'Load Certificate status'}).click();await expect(panel).toContainText('access denied');await expect(panel).not.toContainText('No matching Ingress TLS references')
  fixture.denyConsumers(false);await panel.getByRole('button',{name:'Load Certificate status'}).click();await expect(panel.getByRole('button',{name:'networking.k8s.io/ingresses/entry'})).toBeVisible();await panel.getByRole('button',{name:'cert-manager.io/issuers/public'}).click();await expect(drawer.getByRole('heading',{name:'public',exact:true})).toBeVisible()
  expect(fixture.requests.every(r=>r.method==='GET')).toBe(true);expect(fixture.requests.filter(r=>/\/secrets(?:\/|$)/.test(r.path))).toHaveLength(0)
 }finally{writeFileSync(test.info().outputPath('certificate-wire.json'),JSON.stringify(fixture.requests,null,2));await f.close()}
})
test('actual Certificate status distinguishes expiry, future, missing and invalid times without automatic reads',async({browser})=>{
 const f=await launch(browser),{page,panel,fixture}=f
 try{
  for(const [name,text] of [['expired','Reported certificate has expired'],['not-yet','Reported certificate is not yet valid'],['missing','Not reported'],['invalid','Invalid reported data']]){
   const before=fixture.requests.filter(r=>r.accept.includes('PartialObjectMetadata')&&r.path.endsWith('/issuers/public')).length
   await f.drawer.getByRole('button',{name:'Close',exact:true}).click()
   await page.getByRole('grid',{name:'resources'}).getByRole('gridcell',{name,exact:true}).click()
   await expect(f.drawer.getByRole('heading',{name,exact:true})).toBeVisible()
   await expect(panel.getByRole('button',{name:'Load Certificate status'})).toBeVisible()
   expect(fixture.requests.filter(r=>r.accept.includes('PartialObjectMetadata')&&r.path.endsWith('/issuers/public'))).toHaveLength(before)
   await panel.getByRole('button',{name:'Load Certificate status'}).click();await expect(panel).toContainText(text)
  }
  expect(fixture.requests.filter(r=>/\/secrets(?:\/|$)/.test(r.path))).toHaveLength(0)
 }finally{await f.close()}
})
test('actual Certificate projection fits eight locales at wide and narrow widths',async({browser})=>{
 const f=await launch(browser),{page,panel,drawer}=f
 try{
  await panel.getByRole('button',{name:'Load Certificate status'}).click();await expect(panel).toContainText('web.example')
  for(const code of languages){
   await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:languageNames[code],exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang',code)
   for(const width of [1440,375]){await page.setViewportSize({width,height:1000});await panel.scrollIntoViewIfNeeded();expect(await drawer.evaluate(e=>e.scrollWidth<=e.clientWidth)).toBe(true);expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);await page.screenshot({path:join(scratchRoot,`certificate-${code}-${width}.png`)});await panel.getByRole('heading',{level:4}).last().scrollIntoViewIfNeeded();await page.screenshot({path:join(scratchRoot,`certificate-${code}-${width}-links.png`)})}
   await page.setViewportSize({width:1440,height:1000})
  }
 }finally{await f.close()}
})
