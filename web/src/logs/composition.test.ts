import {beforeEach,expect,it} from 'vitest'
import type {LogComposition,Ref,Target,TargetsView} from '../api/types'
import {fakeClient} from '../test/fakeClient'
import {dock,useDock} from '../dock/store'
import {savedPanels} from '../workspace/snapshot'
import {addLogRoots,clearLogComposition,composedLogClient,compositionAvailable,compositionKey,removeLogRoot,useLogComposition} from './composition'
import {sameLogQuery} from './windowBridge'

const target:Target={provider:'kubernetes',id:'one',title:'One',configRev:'rev1',connection:{id:7,state:'connected',phase:'ready',attempt:1,maxAttempts:1,startedAt:1,attemptStarted:1}}
const pod=(name:string):Ref=>({provider:'kubernetes',target:'one',scope:'ns',kind:'pods',name,uid:'uid-'+name})
const composition=():LogComposition=>({refs:[pod('a'),pod('b')],configRev:'rev1',connectionId:7})
beforeEach(()=>{clearLogComposition();useDock.setState({tabs:[],active:null})})
it('collects explicit references without reads, deduplicates roots and rejects mixed or stale connections atomically',()=>{
 expect(addLogRoots([pod('a'),pod('a')],target)).toBe(true)
 expect(useLogComposition.getState().composition?.refs).toHaveLength(1)
 const before=useLogComposition.getState().composition
 for(const changed of [{...target,id:'other'},{...target,configRev:'rev2'},{...target,connection:{...target.connection!,id:8}}]){
  expect(addLogRoots([pod('b')],changed)).toBe(false)
  expect(useLogComposition.getState().composition).toBe(before)
 }
 expect(addLogRoots(Array.from({length:8},(_,i)=>pod('more'+i)),target)).toBe(false)
 expect(useLogComposition.getState().composition).toBe(before)
 expect(addLogRoots([pod('b')],target)).toBe(true)
 removeLogRoot(0)
 expect(useLogComposition.getState().composition?.refs.map(r=>r.name)).toEqual(['b'])
})
it('opens an immutable set, treats reorder as one identity, and excludes even a malformed pinned composition from persistence',()=>{
 const c=composition();dock.openComposedLogs('One',c)
 c.refs[0].name='mutated';clearLogComposition()
 let tab=useDock.getState().tabs[0]
 expect(tab.kind==='logs'&&tab.composition?.refs[0].name).toBe('a')
 expect(dock.pin(tab.id)).toBe(false)
 dock.openComposedLogs('One',{...composition(),refs:composition().refs.reverse()})
 expect(useDock.getState().tabs).toHaveLength(1)
 expect(useDock.getState().tabs[0]).toMatchObject({focusEpoch:2})
 tab={...useDock.getState().tabs[0],pinned:true}
 expect(savedPanels([tab])).toEqual([])
 dock.openLogs({provider:'kubernetes',id:'one'},'One',pod('ordinary'))
 expect(dock.pin(useDock.getState().active!)).toBe(true)
 expect(savedPanels(useDock.getState().tabs)).toHaveLength(1)
})
it('fences replacement and detached query identity against the exact source set',()=>{
 const c=composition(),view={selected:null,groups:[{provider:'kubernetes',title:'Kubernetes',targets:[target],problems:[]}]} as TargetsView
 expect(compositionAvailable(c,view)).toBe(true)
 expect(compositionAvailable({...c,connectionId:8},view)).toBe(false)
 const q={channel:'*',tailLines:500,compositionKey:compositionKey(c)}
 expect(sameLogQuery(q,{...q,compositionKey:compositionKey({...c,refs:[pod('a'),pod('c')]})})).toBe(false)
 expect(sameLogQuery(q,{...q,compositionKey:compositionKey({...c,refs:[...c.refs].reverse()})})).toBe(true)
})
it('routes both stream and finite export to the composition and preserves destination methods',async()=>{
 const {client:base}=fakeClient([]),c=composition(),client=composedLogClient(base,c)
 expect(await client.logInfo(pod('ignored'))).toMatchObject({defaultChannel:'*',aggregate:true,previous:false})
 expect(base.logInfo).not.toHaveBeenCalled()
 await client.openLogStream(pod('ignored'),{channel:'*',tailLines:500,compositionKey:compositionKey(c)})
 expect(base.openComposedLogStream).toHaveBeenCalledWith(c,{channel:'*',tailLines:500})
 expect(base.openLogStream).not.toHaveBeenCalled()
 const abort=new AbortController();await client.openLogHistory(pod('ignored'),'*',false,abort.signal)
 expect(base.openComposedLogHistory).toHaveBeenCalledWith(c,abort.signal)
 expect(base.openLogHistory).not.toHaveBeenCalled()
 expect(client.streamBase).toBe(base.streamBase)
})
