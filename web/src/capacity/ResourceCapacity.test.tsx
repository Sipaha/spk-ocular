import {act,cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react'
import {afterEach,beforeEach,it,expect,vi} from 'vitest'
import {ResourceCapacity} from './ResourceCapacity'
import type {NodeCapacity} from './types'
import {fakeClient} from '../test/fakeClient'
import {ViewConnectionContext} from '../views/useView'
import {setLanguage} from '../i18n'
beforeEach(()=>setLanguage('en'));afterEach(cleanup)
const subject={provider:'kubernetes',target:'t',kind:'nodes',name:'worker',uid:'node-uid'}
const pod={...subject,kind:'pods',scope:'blue',name:'web',uid:'pod-uid'}
const snapshot:NodeCapacity={ref:subject,namespace:'blue',resourceVersion:'7',capturedAt:Date.parse('2026-10-22T00:00:00Z'),allocatable:{cpu:{state:'declared',value:'4'},memory:{state:'declared',value:'8Gi'}},requests:{cpu:{state:'declared',value:'0'},memory:{state:'unknown'}},coverage:'complete',excludedTerminal:1,unverified:0,pods:[{ref:pod,resourceVersion:'8',requests:{cpu:{state:'declared',value:'0'},memory:{state:'unspecified'}}}]}
const props=()=>({client:fakeClient([]).client,subject,snapshotKey:'one',revision:'one',onGo:vi.fn()})
function namespace(value='blue'){fireEvent.change(screen.getByLabelText('Exact namespace'),{target:{value}})}
function load(){fireEvent.click(screen.getByRole('button',{name:'Load namespace requests'}))}
it('requires explicit single namespace admission, shows scope and missing/zero, and navigates pinned Pods',async()=>{
 const p=props();vi.mocked(p.client.nodeCapacity).mockResolvedValue(snapshot);render(<ResourceCapacity {...p}/>);expect(p.client.nodeCapacity).not.toHaveBeenCalled();expect(screen.getByRole('button',{name:'Load namespace requests'})).toBeDisabled();namespace('blue,red');expect(screen.getByRole('button',{name:'Load namespace requests'})).toBeDisabled();namespace();load();await screen.findByText('pod-uid');expect(p.client.nodeCapacity).toHaveBeenCalledWith({ref:subject,namespace:'blue'},expect.any(AbortSignal));expect(screen.getByText('Not specified')).toBeInTheDocument();expect(screen.getAllByText('0').length).toBe(2);expect(screen.getByText('Unknown')).toBeInTheDocument();expect(screen.getByText(/This namespace is not total Node demand/)).toBeInTheDocument();fireEvent.click(screen.getByRole('button',{name:'blue/web'}));expect(p.onGo).toHaveBeenCalledWith(pod)
})
it('requires new explicit Load after namespace changes and fences late replies',async()=>{
 const p=props();let signal:AbortSignal|undefined,answer:(v:NodeCapacity)=>void=()=>{}
 vi.mocked(p.client.nodeCapacity).mockImplementation((_r,s)=>{signal=s;return new Promise(resolve=>{answer=resolve})});render(<ResourceCapacity {...p}/>);namespace();load();await waitFor(()=>expect(p.client.nodeCapacity).toHaveBeenCalledTimes(1));namespace('red');expect(signal?.aborted).toBe(true);await act(async()=>answer(snapshot));expect(screen.queryByText('pod-uid')).not.toBeInTheDocument();expect(p.client.nodeCapacity).toHaveBeenCalledTimes(1)
})
it('cancels, rejects late replies and clears state on parent/connection/UID replacement',async()=>{
 const p=props();let signal:AbortSignal|undefined,answer:(v:NodeCapacity)=>void=()=>{}
 vi.mocked(p.client.nodeCapacity).mockImplementation((_r,s)=>{signal=s;return new Promise(resolve=>{answer=resolve})})
 const wrap=(connection:string,extra:Partial<typeof p>={})=><ViewConnectionContext value={connection}><ResourceCapacity {...p} {...extra}/></ViewConnectionContext>
 const view=render(wrap('one'));namespace();load();await waitFor(()=>expect(p.client.nodeCapacity).toHaveBeenCalledTimes(1));fireEvent.click(screen.getByRole('button',{name:'Cancel'}));expect(signal?.aborted).toBe(true);await act(async()=>answer(snapshot));expect(screen.queryByText('pod-uid')).not.toBeInTheDocument()
 for(const extra of [{snapshotKey:'two'},{revision:'two'},{subject:{...subject,uid:'new'}},{}]){
  namespace();const count=vi.mocked(p.client.nodeCapacity).mock.calls.length;load();await waitFor(()=>expect(p.client.nodeCapacity).toHaveBeenCalledTimes(count+1));view.rerender(wrap(JSON.stringify(extra),extra));expect(signal?.aborted).toBe(true);await act(async()=>answer(snapshot));expect(screen.queryByText('pod-uid')).not.toBeInTheDocument();expect(screen.getByLabelText('Exact namespace')).toHaveValue('')
 }
})
it('hides old results on refresh/error and rejects response namespace or Node substitution',async()=>{
 const p=props();vi.mocked(p.client.nodeCapacity).mockResolvedValue(snapshot);render(<ResourceCapacity {...p}/>);namespace();load();await screen.findByText('pod-uid')
 vi.mocked(p.client.nodeCapacity).mockRejectedValue(new Error('Fixture unavailable'));load();expect(screen.queryByText('pod-uid')).not.toBeInTheDocument();await screen.findByRole('alert')
 for(const bad of [{...snapshot,namespace:'red'},{...snapshot,ref:{...subject,uid:'other'}}]){vi.mocked(p.client.nodeCapacity).mockResolvedValue(bad);load();await screen.findByRole('alert');expect(screen.queryByText('pod-uid')).not.toBeInTheDocument()}
})
it('leaves foreign or unpinned Pod references inert and displays incomplete coverage',async()=>{
 const p=props();vi.mocked(p.client.nodeCapacity).mockResolvedValue({...snapshot,coverage:'forbidden',pods:[{...snapshot.pods[0],ref:{...pod,target:'other'}}]});render(<ResourceCapacity {...p}/>);namespace();load();await screen.findByText('pod-uid');expect(screen.queryByRole('button',{name:'blue/web'})).not.toBeInTheDocument();expect(screen.getByText(/Incomplete or unavailable/)).toBeInTheDocument()
})

it('requires fresh explicit admission when Client changes even if Ref and connection are unchanged',async()=>{
 const p=props();let signal:AbortSignal|undefined,answer:(v:NodeCapacity)=>void=()=>{}
 vi.mocked(p.client.nodeCapacity).mockImplementation((_r,s)=>{signal=s;return new Promise(resolve=>{answer=resolve})})
 const view=render(<ResourceCapacity {...p}/>);namespace();load();await waitFor(()=>expect(p.client.nodeCapacity).toHaveBeenCalledTimes(1))
 const next=fakeClient([]).client;vi.mocked(next.nodeCapacity).mockResolvedValue(snapshot)
 view.rerender(<ResourceCapacity {...p} client={next}/>);expect(signal?.aborted).toBe(true);await act(async()=>answer(snapshot));expect(next.nodeCapacity).not.toHaveBeenCalled();expect(screen.queryByText('pod-uid')).not.toBeInTheDocument();expect(screen.getByLabelText('Exact namespace')).toHaveValue('');expect(screen.getByRole('button',{name:'Load namespace requests'})).toBeDisabled()
 namespace();load();await screen.findByText('pod-uid');expect(next.nodeCapacity).toHaveBeenCalledTimes(1)
})
