import {act,cleanup,render,screen,waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {afterEach,expect,it,vi} from 'vitest'
import {HelmDiagnosisPanel} from './HelmDiagnosisPanel'
import {HelmWorkspace} from './HelmWorkspace'
import {ViewConnectionContext} from '../views/useView'
import {fakeClient} from '../test/fakeClient'
import {resetGuard} from '../edit/guard'
import type {HelmDetail,HelmDiagnosis,HelmRequest,HelmResponse} from './types'
const target={provider:'kubernetes',id:'test',title:'Test'}
const detail:HelmDetail={name:'demo',namespace:'workloads',storageNamespace:'storage',revision:2,chart:'demo',chartVersion:'1',appVersion:'1',status:'failed',updated:'2026-10-21T00:00:00Z',resources:[],values:'{}',computedValues:'{}',manifest:'',hooks:'',notes:'',history:[]}
const diagnosis:HelmDiagnosis={selected:detail,history:[detail,{...detail,revision:1,status:'superseded'}],hooks:[{index:0,name:'check',kind:'Job',apiVersion:'batch/v1',namespace:'workloads',phase:'Failed',startedAt:'0001-01-01T00:00:00Z',completedAt:'0001-01-01T00:00:00Z',events:['post-upgrade'],deletePolicies:['before-hook-creation'],coverage:'supported'}],compareFrom:1,compareTo:2,truncated:false,capturedAt:'2026-10-21T00:00:00Z'}
afterEach(()=>{cleanup();resetGuard()})
it('requires explicit storage and never automatically reads diagnosis, runtime, Flux, logs or operations',async()=>{
 const {client}=fakeClient([target]);client.helm=vi.fn()
 render(<HelmDiagnosisPanel client={client} target={target} detail={{...detail,storageNamespace:undefined}} configRev="rev" readOwner="owner"/>)
 expect(screen.getByText(/Choose an explicit namespace/)).toBeVisible();expect(screen.queryByRole('button',{name:'Load diagnosis'})).toBeNull();expect(client.helm).not.toHaveBeenCalled()
})
it('keeps each read explicit and opens current UID Properties without log acquisition or mutations',async()=>{
 const {client}=fakeClient([target]),onResource=vi.fn();const ref={provider:'kubernetes',target:'test',kind:'batch/jobs',scope:'workloads',name:'check',uid:'current-job'}
 client.helm=vi.fn(async(r:HelmRequest):Promise<HelmResponse>=>{
  if(r.command==='diagnose')return{diagnosis}
  if(r.command==='observe-hook')return{currentHook:{ref,capturedAt:1,evidence:[],coverage:[{source:'current_hook',state:'ready'}],truncated:false}}
  if(r.command==='compare')return{comparison:{from:1,to:2,left:'replicas: 1\n',right:'replicas: 2\n'}}
  if(r.command==='flux-context')return{flux:{candidates:[],coverage:'forbidden',capturedAt:1}}
  throw Error('unexpected '+r.command)
 })
 const user=userEvent.setup();render(<HelmDiagnosisPanel client={client} target={target} detail={detail} configRev="rev" readOwner="owner" onResource={onResource}/>)
 expect(client.helm).not.toHaveBeenCalled();await user.click(screen.getByRole('button',{name:'Load diagnosis'}));await screen.findByText('Recorded hooks');expect(client.helm).toHaveBeenCalledTimes(1)
 await user.click(screen.getByRole('button',{name:'Observe current hook'}));await user.click(await screen.findByRole('button',{name:'batch/jobs/check · Current Properties'}));expect(onResource).toHaveBeenCalledWith(ref)
 await user.click(screen.getByRole('button',{name:'Compare revisions'}));await screen.findByRole('region',{name:'#1 → #2'})
 await user.type(screen.getByRole('textbox',{name:'Controller namespace'}),'controllers');expect(client.helm).toHaveBeenCalledTimes(3)
 await user.click(screen.getByRole('button',{name:'Load Flux context'}));await screen.findByText('Read coverage: forbidden')
 for(const [r] of vi.mocked(client.helm).mock.calls){expect(r).toMatchObject({provider:'kubernetes',target:'test',namespace:'storage',configRev:'rev',readOwner:'owner'});expect(['diagnose','observe-hook','compare','flux-context']).toContain(r.command)}
})
it('cancels reads and rejects late answers across revision and connection replacement',async()=>{
 const {client}=fakeClient([target]);let resolve!:(r:HelmResponse)=>void,signal:AbortSignal|undefined
 client.helm=vi.fn((_r:HelmRequest,s?:AbortSignal)=>{signal=s;return new Promise<HelmResponse>(r=>{resolve=r})})
 const user=userEvent.setup();const {rerender}=render(<ViewConnectionContext value={1}><HelmDiagnosisPanel client={client} target={target} detail={detail} configRev="rev" readOwner="owner"/></ViewConnectionContext>)
 await user.click(screen.getByRole('button',{name:'Load diagnosis'}));await waitFor(()=>expect(client.helm).toHaveBeenCalledTimes(1));await user.click(screen.getByRole('button',{name:'Cancel'}));expect(signal?.aborted).toBe(true)
 await act(async()=>resolve({diagnosis}));expect(screen.queryByText('Recorded hooks')).toBeNull()
 await user.click(screen.getByRole('button',{name:'Load diagnosis'}));await waitFor(()=>expect(client.helm).toHaveBeenCalledTimes(2))
 rerender(<ViewConnectionContext value={2}><HelmDiagnosisPanel client={client} target={target} detail={{...detail,revision:1}} configRev="next" readOwner="next"/></ViewConnectionContext>);expect(signal?.aborted).toBe(true)
 await act(async()=>resolve({diagnosis}));expect(screen.queryByText('Recorded hooks')).toBeNull();expect(screen.getByRole('button',{name:'Load diagnosis'})).toBeEnabled()
})
it('routes cross-storage rows and history through storage and disables existing mutation controls',async()=>{
 const {client}=fakeClient([target])
 client.helm=vi.fn(async(r:HelmRequest):Promise<HelmResponse>=>r.command==='settings'?{settings:{repositories:[],storage:{driver:'secret'}}}:r.command==='list'?{releases:[detail]}:{detail:{...detail,history:[{...detail,revision:1}]},configRev:'rev',readOwner:'owner'})
 const user=userEvent.setup();render(<HelmWorkspace client={client} target={target} scope={{mode:'one',name:'storage'}} scopePicker={null} initialTab="releases"/>)
 await user.click(await screen.findByRole('gridcell',{name:'demo'}));await waitFor(()=>expect(client.helm).toHaveBeenCalledWith(expect.objectContaining({command:'detail',namespace:'storage'}),expect.any(AbortSignal)))
 expect(screen.getByRole('button',{name:'Upgrade'})).toBeDisabled();expect(screen.getByRole('button',{name:'Uninstall'})).toBeDisabled()
 await user.click(screen.getByRole('tab',{name:'History'}));expect(screen.getByRole('button',{name:'Rollback'})).toBeDisabled();await user.click(screen.getByRole('button',{name:'#1 · 1 · failed'}));await waitFor(()=>expect(client.helm).toHaveBeenCalledWith(expect.objectContaining({command:'detail',namespace:'storage',revision:1}),expect.any(AbortSignal)))
})
it('drops previous current-UID evidence after read failure or a fresh stored snapshot of the same revision',async()=>{
 const {client}=fakeClient([target]);let reads=0,observations=0
 const ref={provider:'kubernetes',target:'test',kind:'batch/jobs',scope:'workloads',name:'check',uid:'former-current'}
 client.helm=vi.fn(async(r:HelmRequest):Promise<HelmResponse>=>{
  if(r.command==='diagnose'){reads++;return{diagnosis:{...diagnosis,capturedAt:`2026-10-21T00:00:0${reads}Z`,hooks:[{...diagnosis.hooks[0],name:reads===1?'check':'new-hook'}]}}}
  if(r.command==='observe-hook'){observations++;if(observations===2)throw Error('current hook changed during observation');return{currentHook:{ref,capturedAt:1,evidence:[],coverage:[],truncated:false}}}
  return{}
 })
 const user=userEvent.setup();render(<HelmDiagnosisPanel client={client} target={target} detail={detail} configRev="rev" readOwner="owner"/>)
 await user.click(screen.getByRole('button',{name:'Load diagnosis'}));await user.click(await screen.findByRole('button',{name:'Observe current hook'}));await screen.findByRole('button',{name:'batch/jobs/check · Current Properties'})
 await user.click(screen.getByRole('button',{name:'Observe current hook'}));await screen.findByRole('alert');expect(screen.queryByRole('button',{name:'batch/jobs/check · Current Properties'})).toBeNull()
 await user.click(screen.getByRole('button',{name:'Observe current hook'}));await screen.findByRole('button',{name:'batch/jobs/check · Current Properties'})
 await user.click(screen.getByRole('button',{name:'Load diagnosis'}));await screen.findByText('Job/workloads/new-hook');expect(screen.queryByRole('button',{name:'batch/jobs/check · Current Properties'})).toBeNull();expect(observations).toBe(3);expect(reads).toBe(2)
})
