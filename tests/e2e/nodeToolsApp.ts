import {expect} from '@playwright/test'
import {spawn,execFileSync} from 'node:child_process'
import {mkdtempSync,mkdirSync,writeFileSync,openSync,closeSync} from 'node:fs'
import {join} from 'node:path'
import {scratchRoot,noDockerEnv} from './fixtures'
import {freePort,stop} from './restartApp'
import {nodeShellFixture} from './nodeShellFixture'

export async function launchNodeTools(page:import('@playwright/test').Page){
  const root=mkdtempSync(join(scratchRoot,'node-shell-')),fixture=await nodeShellFixture(),port=await freePort()
  const home=join(root,'home');mkdirSync(home)
  const kube=join(root,'config.json');writeFileSync(kube,JSON.stringify({apiVersion:'v1',kind:'Config','current-context':'node-fixture',clusters:[{name:'fixture',cluster:{server:fixture.url}}],users:[{name:'fixture',user:{}}],contexts:[{name:'node-fixture',context:{cluster:'fixture',user:'fixture'}}]}))
  const appEnv={...process.env,HOME:home,SPK_OCULAR_HOME:join(root,'data'),KUBECONFIG:kube,...noDockerEnv,HTTP_PROXY:'',HTTPS_PROXY:'',ALL_PROXY:'',http_proxy:'',https_proxy:'',all_proxy:'',DISPLAY:'',WAYLAND_DISPLAY:'',LANGUAGE:'en',LANG:'en_US.UTF-8'}
  let launches=0
  function start(){const fd=openSync(join(root,`app-${++launches}.log`),'w'),app=spawn(process.env.E2E_BIN??'../../build/bin/spk-ocular',['--browser','--port',String(port)],{env:appEnv,stdio:['ignore',fd,fd]});closeSync(fd);return app}
  let app=start(),token=''
  const url=`http://127.0.0.1:${port}`
  const ready=async()=>{
    await expect.poll(async()=>{try{return(await fetch(url)).ok}catch{return false}},{timeout:20000}).toBe(true)
    const html=await(await fetch(url)).text();token=html.match(/spk-ocular-api-token" content="([^"]+)"/)![1]
  }
  const rpc=async(method:string,data:unknown={})=>{const r=await page.request.post(`${url}/api/${method}`,{headers:{Authorization:`Bearer ${token}`,Origin:url},data});expect(r.ok(),`${method}: ${await r.text()}`).toBeTruthy();return r.json()}
  // Only this owned fixture's application database is queried; no other tables
  // or application profiles are read. The helper metadata contains no secrets.
  const helperRows=()=>JSON.parse(execFileSync('python3',['-c',`import sqlite3,sys,json
c=sqlite3.connect('file:'+sys.argv[1]+'?mode=ro',uri=True)
c.row_factory=sqlite3.Row
print(json.dumps([dict(r) for r in c.execute('SELECT id,identity_json,uid,lease_until,next_try,delay_ms FROM owned_helpers')]))
c.close()`,join(root,'data','ocular.db')],{encoding:'utf8'})) as {id:string;identity_json:string;uid:string;lease_until:number;next_try:number;delay_ms:number}[]
  try{
    await ready();await rpc('Configurations',{command:'import',paths:[kube]})
    const groups=(await rpc('ListTargets')).groups,target=groups.find((g:{provider:string})=>g.provider==='kubernetes').targets[0]
    await rpc('SelectTarget',{provider:'kubernetes',id:target.id});await rpc('SetTargetState',{provider:'kubernetes',target:target.id,key:'kind',value:JSON.stringify('nodes')});await rpc('ConnectTarget',{provider:'kubernetes',id:target.id})
    await page.goto(url);const grid=page.getByRole('grid',{name:'resources',exact:true});await expect(grid.getByRole('gridcell',{name:'worker-01',exact:true})).toBeVisible()
    return {root,fixture,get app(){return app},rpc,target,helperRows,
      async restartAfterCrash(){
        if(app.exitCode===null&&app.signalCode===null)throw new Error('The owned fixture must have exited before restart')
        // Advance only the expired lease in the stopped fixture's database;
        // backoff timing/persistence are tested separately with a fake clock.
        execFileSync('python3',['-c',`import sqlite3,sys
c=sqlite3.connect(sys.argv[1]);c.execute("UPDATE owned_helpers SET lease_until=0");c.commit();c.close()`,join(root,'data','ocular.db')])
        app=start();await ready();await page.goto(url)
      },
      async close(){await stop(app);await fixture.close()}}
  }catch(error){await stop(app);await fixture.close();throw error}
}
