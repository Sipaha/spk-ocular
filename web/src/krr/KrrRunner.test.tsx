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
 fireEvent.change(screen.getByLabelText('Existing ServiceAccount'),{target:{value:'reader'}})
 fireEvent.change(screen.getByLabelText('Prometheus base URL (no credentials)'),{target:{value:'http://metrics.tools.svc:9090'}})
 fireEvent.click(screen.getByLabelText(/I confirm this Prometheus/u))
 fireEvent.click(screen.getByLabelText(/I reviewed this ServiceAccount/u))
 fireEvent.click(screen.getByLabelText(/This source requires no extra/u))
}
it('requires explicit existing connection and assertions before review, then starts exactly the returned contract',async()=>{
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
