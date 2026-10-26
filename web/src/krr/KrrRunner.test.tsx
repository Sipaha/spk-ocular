import {act,fireEvent,render,screen,waitFor} from '@testing-library/react'
import {expect,it,vi} from 'vitest'
import KrrDialog from './KrrDialog'
import {fakeClient} from '../test/fakeClient'
import type {KrrResponse,KrrReview,KrrRun} from './model'
import type {Target} from '../api/types'

const target:Target={provider:'kubernetes',id:'fixture',title:'Fixture cluster',configRev:'rev-1',connection:{id:1,state:'connected',phase:'ready',attempt:1,maxAttempts:1,startedAt:1000,attemptStarted:1000}}
const review:KrrReview={options:{namespace:'tools',serviceAccount:'reader',source:'http://metrics.tools.svc:9090',label:'',cluster:'',singleCluster:true,reviewedAccount:true,noSourceAuth:true},expect:'opaque-review',configRev:'rev-1',image:'robustadev/krr@sha256:fixture',namespaceUID:'ns-uid',serviceAccountUID:'sa-uid',name:'ocular-krr-fixture',deadlineSeconds:600}
const run:KrrRun={id:'run-id',state:'running',cleanupPending:false,createdAt:1000,expiresAt:7201000,provider:'kubernetes',target:'fixture',namespace:'tools',name:'ocular-krr-fixture'}
function fill(){
 fireEvent.change(screen.getByLabelText('Namespace'),{target:{value:'tools'}})
 fireEvent.change(screen.getByLabelText('Prometheus base URL (no credentials)'),{target:{value:'http://metrics.tools.svc:9090'}})
}
it('uses the current connection without account assertions before review, then starts exactly the returned contract',async()=>{
 const client=fakeClient([]).client;client.krr=vi.fn(async req=>req.command==='review'?{reports:[],review}:req.command==='start'?{reports:[],runs:[run]}:{reports:[]})
 const ui=render(<KrrDialog client={client} target={target} onClose={vi.fn()}/>)
 expect(screen.getByRole('button',{name:'Review Job'})).toBeDisabled();fill()
 expect(client.krr).not.toHaveBeenCalledWith(expect.objectContaining({command:'start'}),expect.anything())
 fireEvent.click(screen.getByRole('button',{name:'Review Job'}));await waitFor(()=>expect(screen.getByRole('button',{name:'Start reviewed Job'})).toBeInTheDocument())
 expect(screen.getByText(/namespace UID: ns-uid/u)).toBeInTheDocument();expect(document.body.textContent).toContain('64 MiB');expect(document.body.textContent).toContain('history 336 h');expect(document.body.textContent).toContain('step 1.25 h')
 fireEvent.click(screen.getByRole('button',{name:'Start reviewed Job'}));await waitFor(()=>expect(screen.getByRole('button',{name:'Cancel run'})).toBeInTheDocument())
 expect(client.krr).toHaveBeenCalledWith({command:'start',provider:'kubernetes',target:'fixture',review},expect.any(AbortSignal))
 expect(vi.mocked(client.krr).mock.calls.filter(([req])=>req.command==='start')).toHaveLength(1)
 ui.unmount();expect(vi.mocked(client.krr).mock.calls.filter(([req])=>req.command==='cancel')).toHaveLength(0)
})
it('fences a late review on connection incarnation change and preserves backend runs after remount',async()=>{
 const client=fakeClient([]).client;let finish!:(r:KrrResponse)=>void;let signal:AbortSignal|undefined
 client.krr=vi.fn((req,s)=>req.command==='review'?new Promise<KrrResponse>(resolve=>{finish=resolve;signal=s}):Promise.resolve({reports:[],runs:[run]}))
 const ui=render(<KrrDialog client={client} target={target} onClose={vi.fn()}/>);fill();fireEvent.click(screen.getByRole('button',{name:'Review Job'}));await waitFor(()=>expect(finish).toBeDefined())
 const next={...target,connection:{...target.connection!,id:2}}
 ui.rerender(<KrrDialog client={client} target={next} onClose={vi.fn()}/>);expect(signal?.aborted).toBe(true)
 await act(async()=>finish({reports:[],review}));expect(screen.queryByRole('button',{name:'Start reviewed Job'})).not.toBeInTheDocument()
 ui.unmount();render(<KrrDialog client={client} target={next} onClose={vi.fn()}/>);await waitFor(()=>expect(screen.getByRole('button',{name:'Cancel run'})).toBeInTheDocument())
 fireEvent.click(screen.getByRole('button',{name:'Cancel run'}));await waitFor(()=>expect(client.krr).toHaveBeenCalledWith({command:'cancel',id:'run-id'},expect.any(AbortSignal)))
})
it('displays durable pending cleanup even without a selected connection',async()=>{
 const client=fakeClient([]).client;client.krr=vi.fn(async()=>({reports:[],recovery:[{provider:'kubernetes',target:'old-target',namespace:'tools',name:'ocular-krr-old'}],recoveryUnknown:true}))
 render(<KrrDialog client={client} onClose={vi.fn()}/>);await waitFor(()=>expect(screen.getByText(/old-target · tools\/ocular-krr-old/u)).toBeInTheDocument())
 expect(screen.getByText('Cleanup status is incomplete or unavailable.')).toBeInTheDocument()
 expect(screen.queryByRole('button',{name:'Review Job'})).not.toBeInTheDocument()
})
it('client replacement clears selected evidence and rejects late old-client review results',async()=>{
 const first=fakeClient([]).client,next=fakeClient([]).client;let finish!:(r:KrrResponse)=>void
 first.krr=vi.fn(req=>req.command==='review'?new Promise<KrrResponse>(resolve=>{finish=resolve}):Promise.resolve({reports:[]}));next.krr=vi.fn(async()=>({reports:[]}))
 const ui=render(<KrrDialog client={first} target={target} onClose={vi.fn()}/>);fill();fireEvent.click(screen.getByRole('button',{name:'Review Job'}));await waitFor(()=>expect(finish).toBeDefined())
 ui.rerender(<KrrDialog client={next} target={target} onClose={vi.fn()}/>);await act(async()=>finish({reports:[],review}))
 expect(screen.queryByRole('button',{name:'Start reviewed Job'})).not.toBeInTheDocument();expect(screen.getByRole('button',{name:'Review Job'})).toBeDisabled()
})
it('defaults namespace and an unambiguous discovered source without account checkboxes',async()=>{
 const client=fakeClient([]).client;client.krr=vi.fn(async req=>req.command==='setup'?{reports:[],setup:{sources:['http://prometheus.tools.svc:9090'],complete:true}}:req.command==='review'?{reports:[],review}:{reports:[]})
 render(<KrrDialog client={client} target={target} namespace="tools" onClose={vi.fn()}/>);
 await waitFor(()=>expect(screen.getByLabelText('Prometheus base URL (no credentials)')).toHaveValue('http://prometheus.tools.svc:9090'))
 expect(screen.getByLabelText('Namespace')).toHaveValue('tools');expect(screen.queryByRole('checkbox')).not.toBeInTheDocument();expect(screen.queryByLabelText('Existing ServiceAccount')).not.toBeInTheDocument()
 fireEvent.click(screen.getByRole('button',{name:'Review Job'}));await waitFor(()=>expect(screen.getByRole('button',{name:'Start reviewed Job'})).toBeInTheDocument())
 expect(client.krr).toHaveBeenCalledWith(expect.objectContaining({command:'review',options:expect.objectContaining({automatic:true,namespace:'tools',reviewedAccount:false,noSourceAuth:false,singleCluster:false})}),expect.any(AbortSignal))
 fireEvent.click(screen.getByRole('button',{name:'Import JSON'}));expect(screen.getByRole('checkbox')).toBeVisible();expect(screen.queryByRole('button',{name:'Review Job'})).not.toBeInTheDocument()
})
it('does not select from ambiguous or incomplete discovery and permits a manual source',async()=>{
 const client=fakeClient([]).client;client.krr=vi.fn(async req=>req.command==='setup'?{reports:[],setup:{sources:['http://one.svc:9090','http://two.svc:9090'],complete:false}}:{reports:[]})
 render(<KrrDialog client={client} target={target} namespace="tools" onClose={vi.fn()}/>);
 await waitFor(()=>expect(screen.getByText(/Source discovery is incomplete/u)).toBeVisible())
 expect(screen.getByLabelText('Prometheus base URL (no credentials)')).toHaveValue('');expect(screen.getByRole('button',{name:'Review Job'})).toBeDisabled()
 fireEvent.change(screen.getByLabelText('Prometheus base URL (no credentials)'),{target:{value:'http://manual.svc:9090'}})
 expect(screen.getByRole('button',{name:'Review Job'})).toBeEnabled()
})
it('manual changes cancel and fence a pending discovery result',async()=>{
 const client=fakeClient([]).client;let finish!:(r:KrrResponse)=>void;let signal:AbortSignal|undefined
 client.krr=vi.fn((req,s)=>req.command==='setup'?new Promise<KrrResponse>(resolve=>{finish=resolve;signal=s}):Promise.resolve({reports:[]}))
 render(<KrrDialog client={client} target={target} namespace="tools" onClose={vi.fn()}/>);await waitFor(()=>expect(finish).toBeDefined())
 fireEvent.change(screen.getByLabelText('Prometheus base URL (no credentials)'),{target:{value:'http://manual.svc:9090'}});expect(signal?.aborted).toBe(true)
 await act(async()=>finish({reports:[],setup:{sources:['http://late.svc:9090'],complete:true}}))
 expect(screen.getByLabelText('Prometheus base URL (no credentials)')).toHaveValue('http://manual.svc:9090')
})
it('requires both optional label fields and invalidates the review when a source changes',async()=>{
 const client=fakeClient([]).client;client.krr=vi.fn(async req=>req.command==='review'?{reports:[],review}:{reports:[]})
 render(<KrrDialog client={client} target={target} namespace="tools" onClose={vi.fn()}/>);fill()
 fireEvent.click(screen.getByText('Advanced settings'));fireEvent.change(screen.getByLabelText('Exact cluster label name'),{target:{value:'cluster'}})
 expect(screen.getByRole('button',{name:'Review Job'})).toBeDisabled()
 fireEvent.change(screen.getByLabelText('Exact cluster label value'),{target:{value:'my-cluster'}});fireEvent.click(screen.getByRole('button',{name:'Review Job'}));await waitFor(()=>expect(screen.getByRole('button',{name:'Start reviewed Job'})).toBeInTheDocument())
 fireEvent.change(screen.getByLabelText('Prometheus base URL (no credentials)'),{target:{value:'http://changed.svc:9090'}})
 expect(screen.queryByRole('button',{name:'Start reviewed Job'})).not.toBeInTheDocument()
})
