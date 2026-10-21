import {act,cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react'
import {afterEach,beforeEach,it,expect,vi} from 'vitest'
import {ResourceReliability} from './ResourceReliability'
import type {Reliability} from './types'
import {fakeClient} from '../test/fakeClient'
import {ViewConnectionContext} from '../views/useView'
import {setLanguage} from '../i18n'
beforeEach(()=>setLanguage('en'));afterEach(cleanup)
const subject={provider:'kubernetes',target:'t',kind:'apps/deployments',scope:'blue',name:'web',uid:'root-uid'}
const related={...subject,kind:'policy/poddisruptionbudgets',name:'budget',uid:'pdb-uid'}
const snapshot:Reliability={ref:subject,resourceVersion:'7',capturedAt:Date.parse('2026-10-22T00:00:00Z'),pdbState:'complete',hpaState:'forbidden',partial:false,observations:[{rule:'kubernetes.reliability.pdb',version:1,source:related,resourceVersion:'9',field:'status.disruptionsAllowed',outcome:'unknown',severity:'info',code:'pdbStatus',reference:'https://kubernetes.io/docs/concepts/workloads/pods/disruptions/'}]}
const props=()=>({client:fakeClient([]).client,subject,snapshotKey:'one',revision:'one',onGo:vi.fn()})
it('loads only on explicit request, keeps uncertainty and uses pinned source navigation',async()=>{
 const p=props();vi.mocked(p.client.resourceReliability).mockResolvedValue(snapshot);render(<ResourceReliability {...p}/>);expect(p.client.resourceReliability).not.toHaveBeenCalled()
 fireEvent.click(screen.getByRole('button',{name:'Load configuration checks'}));await screen.findByText('status.disruptionsAllowed');expect(screen.getByText(/HPA — Unknown/)).toBeInTheDocument();expect(screen.getByText('pdb-uid')).toBeInTheDocument();expect(screen.getByText('kubernetes.reliability.pdb@1')).toBeInTheDocument();fireEvent.click(screen.getByRole('button',{name:'policy/poddisruptionbudgets/budget'}));expect(p.onGo).toHaveBeenCalledWith(related)
})
it('hides old evidence immediately on refresh, failure and cancellation',async()=>{
 const p=props();vi.mocked(p.client.resourceReliability).mockResolvedValue(snapshot);render(<ResourceReliability {...p}/>);fireEvent.click(screen.getByRole('button',{name:'Load configuration checks'}));await screen.findByText('status.disruptionsAllowed')
 let reject:(e:Error)=>void=()=>{};vi.mocked(p.client.resourceReliability).mockImplementation(()=>new Promise((_r,j)=>{reject=j}));fireEvent.click(screen.getByRole('button',{name:'Load configuration checks'}));expect(screen.queryByText('status.disruptionsAllowed')).not.toBeInTheDocument();await waitFor(()=>expect(p.client.resourceReliability).toHaveBeenCalledTimes(2));await act(async()=>reject(new Error('Fixture denied')));await screen.findByRole('alert');expect(screen.queryByText('status.disruptionsAllowed')).not.toBeInTheDocument()
 vi.mocked(p.client.resourceReliability).mockResolvedValue(snapshot);fireEvent.click(screen.getByRole('button',{name:'Load configuration checks'}));await screen.findByText('status.disruptionsAllowed');vi.mocked(p.client.resourceReliability).mockImplementation(()=>new Promise(()=>{}));fireEvent.click(screen.getByRole('button',{name:'Load configuration checks'}));fireEvent.click(screen.getByRole('button',{name:'Cancel'}));expect(screen.queryByText('status.disruptionsAllowed')).not.toBeInTheDocument()
})
it('drops late replies and requires fresh admission for new snapshots, connection, UID and scope',async()=>{
 const p=props();let answer:(v:Reliability)=>void=()=>{};let signal:AbortSignal|undefined
 vi.mocked(p.client.resourceReliability).mockImplementation((_r,s)=>{signal=s;return new Promise(r=>{answer=r})})
 const wrap=(connection:string,extra:Partial<typeof p>={})=><ViewConnectionContext value={connection}><ResourceReliability {...p} {...extra}/></ViewConnectionContext>
 const view=render(wrap('one'));fireEvent.click(screen.getByRole('button',{name:'Load configuration checks'}));await waitFor(()=>expect(p.client.resourceReliability).toHaveBeenCalledTimes(1));fireEvent.click(screen.getByRole('button',{name:'Cancel'}));expect(signal?.aborted).toBe(true);await act(async()=>answer(snapshot));expect(screen.queryByText('status.disruptionsAllowed')).not.toBeInTheDocument()
 for(const extra of [{snapshotKey:'two'},{revision:'two'},{subject:{...subject,uid:'new'}},{subject:{...subject,scope:'other'}},{}]){
  fireEvent.click(screen.getByRole('button',{name:'Load configuration checks'}));const count=vi.mocked(p.client.resourceReliability).mock.calls.length;await waitFor(()=>expect(p.client.resourceReliability).toHaveBeenCalledTimes(count+1));view.rerender(wrap(JSON.stringify(extra),extra));expect(signal?.aborted).toBe(true);await act(async()=>answer(snapshot));expect(screen.queryByText('status.disruptionsAllowed')).not.toBeInTheDocument()
 }
})
it('rejects root replacement and foreign source navigation and exposes limited coverage',async()=>{
 const p=props();vi.mocked(p.client.resourceReliability).mockResolvedValue({...snapshot,ref:{...subject,uid:'replacement'}});render(<ResourceReliability {...p}/>);fireEvent.click(screen.getByRole('button',{name:'Load configuration checks'}));await screen.findByRole('alert');expect(screen.queryByText('status.disruptionsAllowed')).not.toBeInTheDocument()
 vi.mocked(p.client.resourceReliability).mockResolvedValue({...snapshot,partial:true,observations:snapshot.observations.map(o=>({...o,source:{...o.source,target:'foreign'}}))});fireEvent.click(screen.getByRole('button',{name:'Load configuration checks'}));await screen.findByText('status.disruptionsAllowed');expect(screen.queryByRole('button',{name:'policy/poddisruptionbudgets/budget'})).not.toBeInTheDocument();expect(screen.getByText('Evidence is incomplete or limited.')).toBeInTheDocument()
})

it('invalidates a failed parent read even when its retained Resource bytes are unchanged',async()=>{
 const p=props();vi.mocked(p.client.resourceReliability).mockResolvedValue(snapshot)
 const retained={...subject};const initial={resource:retained};const view=render(<ResourceReliability {...p} snapshotKey={initial}/>)
 fireEvent.click(screen.getByRole('button',{name:'Load configuration checks'}));await screen.findByText('status.disruptionsAllowed')
 view.rerender(<ResourceReliability {...p} snapshotKey={{resource:retained,error:'Fixture read failed'}}/>);expect(screen.queryByText('status.disruptionsAllowed')).not.toBeInTheDocument();expect(p.client.resourceReliability).toHaveBeenCalledTimes(1)
 fireEvent.click(screen.getByRole('button',{name:'Load configuration checks'}));await screen.findByText('status.disruptionsAllowed')
 view.rerender(<ResourceReliability {...p} snapshotKey={{resource:retained}}/>);expect(screen.queryByText('status.disruptionsAllowed')).not.toBeInTheDocument();expect(p.client.resourceReliability).toHaveBeenCalledTimes(2)
})
