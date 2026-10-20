import {afterEach,beforeEach,describe,expect,it,vi} from 'vitest'
import {ToolWindowBridge} from './windowBridge'
vi.mock('../api/client',()=>({isDesktop:()=>false}))
const channels=new Set<FakeChannel>()
let dropNextSnapshot=false
class FakeChannel {
 onmessage:((e:{data:unknown})=>void)|null=null
 constructor(){channels.add(this)}
 postMessage(data:unknown){if(dropNextSnapshot&&(data as {type:string}).type==='snapshot-update'){dropNextSnapshot=false;return}for(const peer of channels)if(peer!==this)peer.onmessage?.({data})}
 close(){channels.delete(this)}
}
const popup={closed:false,close:vi.fn(),focus:vi.fn()}
beforeEach(()=>{dropNextSnapshot=false;vi.stubGlobal('BroadcastChannel',FakeChannel);vi.stubGlobal('open',vi.fn(()=>popup));popup.closed=false;window.history.replaceState({},'','/');vi.useFakeTimers()})
afterEach(()=>{vi.useRealTimers();vi.unstubAllGlobals();channels.clear()})
async function guestFor(owner:ToolWindowBridge){
 const url=vi.mocked(window.open).mock.calls.at(-1)![0] as string
 window.history.replaceState({},'',url)
 const guest=new ToolWindowBridge(owner.id,'guest','files');await guest.start();return guest
}
describe('tool window generations',()=>{
 it('rejects input and close messages from an old guest after reopening',async()=>{
  const owner=new ToolWindowBridge('id','owner','files');await owner.start();await owner.open('Files')
  const old=await guestFor(owner),seen:string[]=[];owner.listen(m=>seen.push(m.type))
  await old.post('state',{text:'first'});expect(seen).toEqual(['state'])
  await owner.close();await owner.open('Files');const current=await guestFor(owner)
  await old.post('closed');await old.post('state',{text:'stale'});expect(seen).toEqual(['state'])
  await current.post('state',{text:'current'});expect(seen).toEqual(['state','state'])
  old.stop();current.stop();owner.stop()
 })
 it('coalesces queued snapshots and flushes the newest state before return',async()=>{
  const owner=new ToolWindowBridge('id','owner','files');await owner.start();await owner.open('Files');const guest=await guestFor(owner)
  const seen:unknown[]=[];owner.listen(m=>seen.push(m.payload))
  void guest.update('state',1);void guest.update('state',2);void guest.update('state',3)
  await guest.flush();await guest.post('return','finished')
  expect(seen).toEqual([1,3,'finished']);guest.stop();owner.stop()
 })
})


it('delivers a newer queued status after an unacknowledged startup snapshot fails',async()=>{
 const owner=new ToolWindowBridge('id','owner','shell');await owner.start();await owner.open('Shell')
 const guest=await guestFor(owner),seen:unknown[]=[];owner.listen(m=>seen.push(m.payload))
 dropNextSnapshot=true
 const first=guest.update('status','opening');const rejected=expect(first).rejects.toThrow('did not acknowledge')
 void guest.update('status','ended').catch(()=>{})
 await vi.advanceTimersByTimeAsync(5000);await rejected;await guest.flush()
 expect(seen).toEqual(['ended']);guest.stop();owner.stop()
})
