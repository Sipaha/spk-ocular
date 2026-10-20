import {test,expect} from '@playwright/test'
import {spawn} from 'node:child_process'
import {mkdtempSync,mkdirSync,writeFileSync,readFileSync,openSync,closeSync} from 'node:fs'
import {join,resolve} from 'node:path'
import {scratchRoot,noDockerEnv} from './fixtures'
import {freePort,stop} from './restartApp'
import {nodeShellFixture} from './nodeShellFixture'
import {activePanel,expectScreen,focusTerminal} from './synth'

async function launch(page:import('@playwright/test').Page){
  const root=mkdtempSync(join(scratchRoot,'node-shell-')),fixture=await nodeShellFixture(),port=await freePort()
  const home=join(root,'home');mkdirSync(home)
  const kube=join(root,'config.json');writeFileSync(kube,JSON.stringify({apiVersion:'v1',kind:'Config','current-context':'node-fixture',clusters:[{name:'fixture',cluster:{server:fixture.url}}],users:[{name:'fixture',user:{}}],contexts:[{name:'node-fixture',context:{cluster:'fixture',user:'fixture'}}]}))
  const fd=openSync(join(root,'app.log'),'w'),app=spawn(process.env.E2E_BIN??'../../build/bin/spk-ocular',['--browser','--port',String(port)],{env:{...process.env,HOME:home,SPK_OCULAR_HOME:join(root,'data'),KUBECONFIG:kube,...noDockerEnv,HTTP_PROXY:'',HTTPS_PROXY:'',ALL_PROXY:'',http_proxy:'',https_proxy:'',all_proxy:'',DISPLAY:'',WAYLAND_DISPLAY:'',LANGUAGE:'en',LANG:'en_US.UTF-8'},stdio:['ignore',fd,fd]});closeSync(fd)
  const url=`http://127.0.0.1:${port}`
  try{
    await expect.poll(async()=>{try{return(await fetch(url)).ok}catch{return false}},{timeout:20000}).toBe(true)
    const html=await(await fetch(url)).text(),token=html.match(/spk-ocular-api-token" content="([^"]+)"/)![1]
    const rpc=async(method:string,data:unknown={})=>{const r=await page.request.post(`${url}/api/${method}`,{headers:{Authorization:`Bearer ${token}`,Origin:url},data});expect(r.ok(),`${method}: ${await r.text()}`).toBeTruthy();return r.json()}
    await rpc('Configurations',{command:'import',paths:[kube]})
    const groups=(await rpc('ListTargets')).groups,target=groups.find((g:{provider:string})=>g.provider==='kubernetes').targets[0]
    await rpc('SelectTarget',{provider:'kubernetes',id:target.id});await rpc('SetTargetState',{provider:'kubernetes',target:target.id,key:'kind',value:JSON.stringify('nodes')});await rpc('ConnectTarget',{provider:'kubernetes',id:target.id})
    await page.goto(url);const grid=page.getByRole('grid',{name:'resources',exact:true});await expect(grid.getByRole('gridcell',{name:'worker-01',exact:true})).toBeVisible()
    return {root,fixture,app,rpc,async close(){await stop(app);await fixture.close()}}
  }catch(error){await stop(app);await fixture.close();throw error}
}
async function openReview(page:import('@playwright/test').Page,language='en'){
  await page.getByRole('gridcell',{name:'worker-01',exact:true}).click()
  const drawer=page.locator('[data-area=details]');await drawer.getByRole('button',{name:language==='ru'?'Действия':'Actions',exact:true}).click()
  await page.getByRole('menuitem',{name:language==='ru'?'Терминал ноды…':'Node Shell…',exact:true}).click()
  const dialog=page.getByRole('dialog',{name:language==='ru'?'Терминал ноды worker-01':'Node Shell worker-01',exact:true});await expect(dialog.getByRole('button',{name:language==='ru'?'Отмена':'Cancel',exact:true})).toBeFocused();return dialog
}

test('Node Shell reviews privileged access, streams through Kubernetes and deletes its helper on exit and tab close',async({page})=>{
  const run=await launch(page),errors:string[]=[];page.on('pageerror',e=>errors.push(e.message))
  try{
    const d=await openReview(page);await expect(d).toContainText('root access');await expect(d).toContainText('1 hour');expect(run.fixture.pods.size).toBe(0)
    await page.screenshot({path:join(run.root,'review-en.png')})
    const ns=d.getByRole('textbox',{name:'Helper namespace'});await ns.fill('tools');await expect(d.getByRole('button',{name:'Node Shell',exact:true})).toBeDisabled();await d.getByRole('button',{name:'Review',exact:true}).click();await expect(d).toContainText('Create a temporary Pod in tools')
    await d.getByRole('button',{name:'Node Shell',exact:true}).click();await expectScreen(page,'fixture node worker-01 (root)')
    expect(run.fixture.pods.size).toBe(1);const p=[...run.fixture.pods.values()][0];expect(p.spec.nodeName).toBe('worker-01');expect(p.spec.hostPID).toBe(true);expect(p.spec.containers[0].securityContext.privileged).toBe(true);expect(p.spec.automountServiceAccountToken).toBe(false)
    await focusTerminal(page);await page.keyboard.type('hello-node');await page.keyboard.press('Enter');await expectScreen(page,'fixture echo: hello-node')
    await page.screenshot({path:join(run.root,'terminal-en.png')})
    await page.keyboard.type('exit');await page.keyboard.press('Enter');await expect.poll(()=>run.fixture.pods.size).toBe(0);await expect(activePanel(page).getByRole('alert').filter({hasText:'review Node Shell again'})).toContainText('process exited with code 0');await expect(activePanel(page).getByRole('button',{name:'Reconnect',exact:true})).toHaveCount(0)
    expect(run.fixture.execs).toBe(1)
    // Close the ended tab before another independent, explicitly reviewed shell.
    await page.getByRole('tab',{name:/Node Shell · worker-01/}).getByRole('button',{name:'Close tab',exact:true}).click()
    const second=await openReview(page);await second.getByRole('button',{name:'Node Shell',exact:true}).click();await expect.poll(()=>run.fixture.execs).toBe(2);await expect.poll(()=>run.fixture.pods.size).toBe(1)
    await page.getByRole('tab',{name:/Node Shell · worker-01/}).getByRole('button',{name:'Close tab',exact:true}).click();await expect.poll(()=>run.fixture.pods.size).toBe(0)
    expect(run.fixture.calls.filter(c=>c.method==='POST'&&c.path.endsWith('/pods'))).toHaveLength(2);expect(errors).toEqual([])
  }finally{await run.close()}
})

test('Node Shell warns about namespace policy, shows denied creation, and localizes a narrow Russian review',async({page})=>{
  const run=await launch(page)
  try{
    let d=await openReview(page);await d.getByRole('textbox',{name:'Helper namespace'}).fill('blocked');await d.getByRole('button',{name:'Review',exact:true}).click();await expect(d).toContainText('unless the request is exempt');await expect(d.getByRole('button',{name:'Node Shell',exact:true})).toBeEnabled();expect(run.fixture.pods.size).toBe(0)
    await page.keyboard.press('Escape');await run.rpc('SetLanguage',{language:'ru'});await page.reload();d=await openReview(page,'ru');await page.setViewportSize({width:375,height:900});await expect(d).toContainText('root-доступ');expect(await d.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true);await page.screenshot({path:join(run.root,'review-ru-375.png')});await page.keyboard.press('Escape')
    await page.setViewportSize({width:1440,height:900});await run.rpc('SetLanguage',{language:'en'});await page.reload();run.fixture.denyCreate(true)
    d=await openReview(page);await d.getByRole('button',{name:'Node Shell',exact:true}).click();await expect(activePanel(page).getByRole('alert')).toContainText('Forbidden');expect(run.fixture.pods.size).toBe(0);expect(run.fixture.execs).toBe(0)
    expect(run.fixture.calls.filter(c=>c.method==='POST'&&c.path.endsWith('/pods'))).toHaveLength(1)
  }finally{await run.close()}
})


test('Node Shell cleans up after a lost Kubernetes stream and normal application shutdown',async({page})=>{
  const run=await launch(page)
  try{
    let d=await openReview(page);await d.getByRole('button',{name:'Node Shell',exact:true}).click();await expectScreen(page,'fixture node worker-01 (root)')
    run.fixture.dropExecs();await expect.poll(()=>run.fixture.pods.size).toBe(0)
    await expect(activePanel(page).getByRole('button',{name:'Reconnect',exact:true})).toHaveCount(0)
    await page.getByRole('tab',{name:/Node Shell · worker-01/}).getByRole('button',{name:'Close tab',exact:true}).click()
    d=await openReview(page);await d.getByRole('button',{name:'Node Shell',exact:true}).click();await expect.poll(()=>run.fixture.pods.size).toBe(1)
    await stop(run.app);await expect.poll(()=>run.fixture.pods.size).toBe(0)
    expect(run.fixture.calls.filter(c=>c.method==='POST'&&c.path.endsWith('/pods'))).toHaveLength(2)
  }finally{await run.close()}
})

test('Node Shell review uses all eight local catalogues and keeps root access above the effects',async({page})=>{
  const run=await launch(page)
  try{
    for(const language of ['en','ru','zh','es','de','fr','pt','ja']){
      await run.rpc('SetLanguage',{language});await page.reload()
      const local=language==='en'?{ui:{'act.node-shell':'Node Shell','action.menu':'Actions','action.cancel':'Cancel'}}:language==='ru'?{ui:{'act.node-shell':'Терминал ноды','action.menu':'Действия','action.cancel':'Отмена'}}:JSON.parse(readFileSync(resolve(import.meta.dirname,`../../web/src/locales/${language}.json`),'utf8'))
      await page.getByRole('gridcell',{name:'worker-01',exact:true}).click()
      await page.locator('[data-area=details]').getByRole('button',{name:local.ui['action.menu'],exact:true}).click()
      const label=local.ui['act.node-shell'];await page.getByRole('menuitem',{name:label+'…',exact:true}).click()
      const d=page.getByRole('dialog',{name:label+' worker-01',exact:true})
      await expect(d.getByRole('button',{name:local.ui['action.cancel'],exact:true})).toBeFocused()
      const warning=d.locator('section').first();await expect(warning).toHaveClass(/text-warning/)
      if(local.provider)await expect(warning).toContainText(local.provider['kubernetes.nodeShell.privileged'])
      await page.screenshot({path:join(run.root,`review-${language}.png`)})
      await page.keyboard.press('Escape');expect(run.fixture.pods.size).toBe(0)
    }
    expect(run.fixture.calls.filter(c=>c.method==='POST'&&c.path.endsWith('/pods'))).toHaveLength(0)
  }finally{await run.close()}
})


test('a failed helper deletion remains visible after closing its terminal tab',async({page})=>{
  const run=await launch(page)
  try{
    const d=await openReview(page);await d.getByRole('button',{name:'Node Shell',exact:true}).click();await expectScreen(page,'fixture node worker-01 (root)')
    const helper=[...run.fixture.pods.values()][0].metadata.name
    run.fixture.denyDelete(true)
    await page.getByRole('tab',{name:/Node Shell · worker-01/}).getByRole('button',{name:'Close tab',exact:true}).click()
    await expect(page.getByRole('tab',{name:/Node Shell · worker-01/})).toHaveCount(0)
    await expect(page.getByRole('status').filter({hasText:'Could not confirm cleanup'})).toContainText(helper)
    expect(run.fixture.pods.size).toBe(1)
    expect(run.fixture.calls.filter(c=>c.method==='DELETE'&&c.path.endsWith('/'+helper))).toHaveLength(1)
    await page.screenshot({path:join(run.root,'cleanup-failure-after-close.png')})
  }finally{await run.close()}
})
