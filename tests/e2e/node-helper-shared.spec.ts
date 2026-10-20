import {test,expect} from '@playwright/test'
import {launchNodeTools} from './nodeToolsApp'
import {stop} from './restartApp'

test('node Shell and Files share one helper, keep its durable record during grace and clean it at shutdown',async({page})=>{
 const run=await launchNodeTools(page)
 try{
  const ref={provider:'kubernetes',target:run.target.id,kind:'nodes',name:'worker-01',uid:'node-1'}
  const prepare=async(action:string)=>run.rpc('PrepareAction',{ref,action,params:{}})
  const admit=async(plan:Record<string,any>)=>run.rpc('RunAction',{ref,action:plan.action.id,params:plan.params,expect:plan.expect,configRev:plan.where.configRev})
  const files=async(preparedId:string,command='list')=>run.rpc('Files',{ref,preparedId,instance:'',channel:'',path:'/',command})
  run.fixture.beforeCreate(()=>{const rows=run.helperRows();expect(rows).toHaveLength(1);expect(rows[0].uid).toBe('')})
  const cold=await prepare('node-files');expect(cold.reuse).not.toBe(true)
  const first=(await admit(cold)).files;expect(run.fixture.pods.size).toBe(0);expect(run.helperRows()).toHaveLength(0)
  await files(first.preparedId);expect(run.fixture.pods.size).toBe(1);const helper=[...run.fixture.pods.values()][0];expect(run.helperRows()).toHaveLength(1);expect(run.helperRows()[0].uid).toBe(helper.metadata.uid)
  const warm=await prepare('node-shell');expect(warm.reuse).toBe(true);expect(warm.effects.map((v:{text:string})=>v.text).join(' ')).toContain('no new Pod')
  const shell=(await admit(warm)).terminal
  await files(first.preparedId,'close');expect(run.fixture.pods.size).toBe(1);expect(run.helperRows()).toHaveLength(1)
  await run.rpc('ForgetTerminal',{terminalId:shell.preparedId});expect(run.fixture.pods.size).toBe(1);expect(run.helperRows()).toHaveLength(1)
  const next=await prepare('node-files');expect(next.reuse).toBe(true);const second=(await admit(next)).files;await files(second.preparedId)
  expect(run.fixture.calls.filter(c=>c.method==='POST'&&c.path.endsWith('/pods'))).toHaveLength(1);expect([...run.fixture.pods.values()][0].metadata.uid).toBe(helper.metadata.uid);expect(run.helperRows()).toHaveLength(1)
  await files(second.preparedId,'close');expect(run.fixture.pods.size).toBe(1)
  await stop(run.app);expect(run.fixture.pods.size).toBe(0);expect(run.helperRows()).toHaveLength(0)
  const deletions=run.fixture.calls.filter(c=>c.method==='DELETE'&&c.path.endsWith('/'+helper.metadata.name));expect(deletions).toHaveLength(1);expect(deletions[0].body?.preconditions?.uid).toBe(helper.metadata.uid)
 }finally{await run.close()}
})


test('share one helper until five seconds after its final consumer closes',async({page})=>{
 const run=await launchNodeTools(page)
 try{
  const ref={provider:'kubernetes',target:run.target.id,kind:'nodes',name:'worker-01',uid:'node-1'}
  const open=async()=>{
   const plan=await run.rpc('PrepareAction',{ref,action:'node-files',params:{}})
   const result=await run.rpc('RunAction',{ref,action:plan.action.id,params:plan.params,expect:plan.expect,configRev:plan.where.configRev})
   await run.rpc('Files',{ref,preparedId:result.files.preparedId,instance:'',channel:'',path:'/',command:'list'})
   return result.files.preparedId as string
  }
  const close=async(preparedId:string)=>run.rpc('Files',{ref,preparedId,instance:'',channel:'',path:'/',command:'close'})
  const first=await open(),helper=[...run.fixture.pods.values()][0]
  await close(first)
  // A real idle interval followed by reacquisition must retain the same Pod.
  await page.waitForTimeout(1000)
  const second=await open()
  expect([...run.fixture.pods.values()][0].metadata.uid).toBe(helper.metadata.uid)
  expect(run.fixture.calls.filter(c=>c.method==='POST'&&c.path.endsWith('/pods'))).toHaveLength(1)
  await close(second)
  const closedAt=performance.now()
  expect(run.fixture.pods.size).toBe(1)
  await expect.poll(()=>run.fixture.pods.size,{timeout:10000,intervals:[100]}).toBe(0)
  expect(performance.now()-closedAt).toBeGreaterThanOrEqual(4900)
  await expect.poll(()=>run.helperRows().length).toBe(0)
  const deletions=run.fixture.calls.filter(c=>c.method==='DELETE'&&c.path.endsWith('/'+helper.metadata.name))
  expect(deletions).toHaveLength(1)
  expect(deletions[0].body?.preconditions?.uid).toBe(helper.metadata.uid)
 }finally{await run.close()}
})

test('warm node Shell and Files skip launch confirmation and window transfer retains one helper consumer',async({page})=>{
 const run=await launchNodeTools(page)
 try{
  await page.getByRole('gridcell',{name:'worker-01',exact:true}).click()
  const tools=page.locator('[data-area=details] .drawer-tools')
  await tools.getByRole('button',{name:'Files',exact:true}).click()
  await page.getByRole('dialog',{name:'Files worker-01',exact:true}).getByRole('button',{name:'Files',exact:true}).click()
  const files=page.getByRole('region',{name:'Files',exact:true});await expect(files.getByRole('treeitem',{name:'etc',exact:true})).toBeVisible()
  const helper=[...run.fixture.pods.values()][0]
  const popupWait=page.waitForEvent('popup');await files.getByRole('button',{name:'Open in window',exact:true}).click();const popup=await popupWait
  await expect(popup.getByRole('region',{name:'Files',exact:true}).getByRole('treeitem',{name:'etc',exact:true})).toBeVisible()
  // Remain detached longer than idle expiry: the same logical consumer still owns it.
  await page.waitForTimeout(5500);expect(run.fixture.pods.size).toBe(1);expect(run.helperRows()).toHaveLength(1)
  await tools.getByRole('button',{name:'Node Shell',exact:true}).click()
  await expect(page.getByRole('dialog',{name:'Node Shell worker-01',exact:true})).toHaveCount(0)
  await expect(page.locator('[data-terminal]')).toContainText('fixture node worker-01 (root)')
  await popup.getByRole('button',{name:'Return to bottom panel',exact:true}).click();await expect.poll(()=>popup.isClosed()).toBe(true)
  await page.locator('[data-tab-kind=files] > button').first().click();await page.locator('[data-tab-kind=files][aria-selected=true]').getByRole('button',{name:'Close tab',exact:true}).click()
  expect(run.fixture.pods.size).toBe(1)
  expect([...run.fixture.pods.values()][0].metadata.uid).toBe(helper.metadata.uid)
  expect(run.fixture.calls.filter(c=>c.method==='POST'&&c.path.endsWith('/pods'))).toHaveLength(1)
  await page.locator('[data-tab-kind=term]').getByRole('button',{name:'Close tab',exact:true}).click()
  await expect.poll(()=>run.fixture.pods.size,{timeout:10000}).toBe(0)
  expect(run.helperRows()).toHaveLength(0)
 }finally{await run.close()}
})
