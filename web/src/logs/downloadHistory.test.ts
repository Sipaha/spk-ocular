import {beforeEach,describe,expect,it,vi} from 'vitest'
import {fakeClient,k8s} from '../test/fakeClient'
import {downloadLogHistory} from './downloadHistory'
import {openLogDownload} from './save'
import type {Ref} from '../api/types'
vi.mock('./save',async original=>({...await original<typeof import('./save')>(),openLogDownload:vi.fn()}))
const subject:Ref={provider:'kubernetes',target:'dev',kind:'pods',scope:'ns',name:'app',uid:'old-uid'}
const source={k:'source',id:1,key:'old-uid/app',label:'app/main'}
const complete={k:'end',reason:'done'}
function response(frames:unknown[],finish=true){
 let controller!:ReadableStreamDefaultController<Uint8Array>
 const body=new ReadableStream<Uint8Array>({start:c=>{controller=c},cancel:()=>{}})
 controller.enqueue(new TextEncoder().encode(frames.map(f=>JSON.stringify(f)+'\n').join('')))
 if(finish)controller.close()
 return new Response(body,{status:200})
}
function setup(frames:unknown[]){
 const writer={write:vi.fn(async(text:string)=>{void text}),close:vi.fn(async()=> 'history.log'),abort:vi.fn(async()=>{})}
 vi.mocked(openLogDownload).mockResolvedValue(writer)
 const {client}=fakeClient([k8s('dev')]);vi.stubGlobal('fetch',vi.fn(async()=>response(frames)))
 return {writer,client}
}
beforeEach(()=>{vi.unstubAllGlobals();vi.clearAllMocks()})
describe('full retained log history download',()=>{
 it('exports all history, outside the viewport/buffer, preserving channel, previous selection, Unicode and timestamp provenance',async()=>{
  const tuples=Array.from({length:2000},(_,i)=>['2026-10-11T01:00:00Z',i===3?'\x1b[32mпривет\x1b[0m':`history ${i}`])
  const {writer,client}=setup([source,{k:'lines',s:1,l:tuples},complete])
  const result=await downloadLogHistory(client,subject,'selected',true,new AbortController().signal,'window')
  expect(client.openLogHistory).toHaveBeenCalledWith(subject,'selected',true,expect.any(AbortSignal))
  expect(client.openLogStream).not.toHaveBeenCalled()
  const text=writer.write.mock.calls.map(call=>call[0]).join('')
  expect(text).toContain('history 0\n');expect(text).toContain('history 1999\n');expect(text).toContain('[app/main] привет\n');expect(text).not.toContain('\x1b')
  expect(result).toEqual({path:'history.log',lines:2000,bytes:new TextEncoder().encode(text).length})
  expect(writer.close).toHaveBeenCalledOnce();expect(writer.abort).not.toHaveBeenCalled()
 })
 it('does not open a source when the destination chooser is cancelled',async()=>{
  const {client}=setup([]);vi.mocked(openLogDownload).mockResolvedValue(null)
  expect(await downloadLogHistory(client,subject,'',false,new AbortController().signal)).toBeNull()
  expect(client.openLogHistory).not.toHaveBeenCalled();expect(fetch).not.toHaveBeenCalled()
 })
 it.each([
  ['EOF without completion',[source,{k:'lines',s:1,l:[['','prefix']]}]],
  ['source warning',[source,{k:'state',s:1,state:'truncated',msg:'history truncated'},complete]],
  ['owner closed',[source,{k:'end',reason:'gone'}]],
  ['deleted source',[source,{k:'state',s:1,state:'ended',class:'not_found',msg:'pod deleted'},complete]],
  ['unknown source state',[source,{k:'state',s:1,state:'unexpected'},complete]],
  ['line was cut',[source,{k:'lines',s:1,l:[['','cut',1]]},complete]],
  ['unknown flags',[source,{k:'lines',s:1,l:[['','unknown',4]]},complete]],
  ['invalid timestamp',[source,{k:'lines',s:1,l:[['not a timestamp','text']]},complete]],
  ['missing source',[{k:'lines',s:1,l:[['','orphan']]},complete]],
  ['unknown frame',[source,{k:'unexpected'},complete]],
  ['empty source set',[complete]],
  ['bytes after end',[source,complete,{k:'lines',s:1,l:[['','late']]}]],
 ] as const)('aborts the staged destination for %s',async(_name,frames)=>{
  const {writer,client}=setup([...frames])
  await expect(downloadLogHistory(client,subject,'',false,new AbortController().signal)).rejects.toThrow()
  expect(writer.abort).toHaveBeenCalled();expect(writer.close).not.toHaveBeenCalled()
 })
 it('cancels a pending read and aborts the destination, never committing received prefixes',async()=>{
  const {writer,client}=setup([])
  vi.stubGlobal('fetch',vi.fn(async()=>response([source,{k:'lines',s:1,l:[['','prefix']]}],false)))
  const controller=new AbortController()
  const pending=downloadLogHistory(client,subject,'',false,controller.signal)
  const rejected=expect(pending).rejects.toMatchObject({name:'AbortError'})
  await vi.waitFor(()=>expect(writer.write).toHaveBeenCalled())
  controller.abort();await rejected
  expect(writer.close).not.toHaveBeenCalled();expect(writer.abort).toHaveBeenCalled()
 })
 it('cancellation while the chooser is open prevents a late history read',async()=>{
  const {writer,client}=setup([])
  let choose!:(value:typeof writer)=>void
  vi.mocked(openLogDownload).mockReturnValue(new Promise(resolve=>{choose=resolve}))
  const controller=new AbortController(),pending=downloadLogHistory(client,subject,'',false,controller.signal)
  const rejected=expect(pending).rejects.toMatchObject({name:'AbortError'})
  controller.abort();choose(writer);await rejected
  expect(client.openLogHistory).not.toHaveBeenCalled();expect(writer.abort).toHaveBeenCalled()
 })
 it('aborts a failed disk write and reports server export limits without saving a prefix',async()=>{
  const {writer,client}=setup([source,{k:'lines',s:1,l:[['','entry']]},complete])
  writer.write.mockRejectedValue(new Error('disk full'))
  await expect(downloadLogHistory(client,subject,'',false,new AbortController().signal)).rejects.toThrow('disk full')
  expect(writer.close).not.toHaveBeenCalled();expect(writer.abort).toHaveBeenCalled()
  writer.write.mockResolvedValue(undefined)
  vi.stubGlobal('fetch',vi.fn(async()=>response([source,{k:'end',reason:'error',class:'limit'}])))
  await expect(downloadLogHistory(client,subject,'',false,new AbortController().signal)).rejects.toThrow('History export limit')
  expect(writer.close).not.toHaveBeenCalled()
 })
})
