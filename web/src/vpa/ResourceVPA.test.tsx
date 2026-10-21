import {act,cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react'
import {afterEach,beforeEach,it,expect,vi} from 'vitest'
import {ResourceVPA} from './ResourceVPA'
import type {VPAInspection} from './types'
import {fakeClient} from '../test/fakeClient'
import {ViewConnectionContext} from '../views/useView'
import {setLanguage} from '../i18n'
beforeEach(()=>setLanguage('en'));afterEach(cleanup)
const subject={provider:'kubernetes',target:'t',kind:'apps/deployments',scope:'blue',name:'web',uid:'controller-uid'}
const vpa={...subject,kind:'autoscaling.k8s.io/verticalpodautoscalers',name:'sizing',uid:'vpa-uid'}
const snapshot:VPAInspection={ref:subject,resourceVersion:'7',capturedAt:0,state:'ambiguous',hpaState:'forbidden',declarations:[{container:'app',requests:{cpu:'100m'},limits:{}}],hpaMetrics:[],sources:[{ref:vpa,resourceVersion:'3',apiVersion:'autoscaling.k8s.io/v1',updateMode:'Off',state:'reported',freshness:'unknown',partial:false,conditions:[],policies:[],recommendations:[{container:'app',target:{cpu:'250m',memory:'96Mi'},lowerBound:{cpu:'0'},upperBound:{},uncappedTarget:{}}]}]}
const props=()=>({client:fakeClient([]).client,subject,snapshotKey:'one',revision:'one',onGo:vi.fn()})
it('loads explicitly, preserves external provenance, unknowns, Off and multiple-source caveats',async()=>{
 const p=props();vi.mocked(p.client.resourceVPA).mockResolvedValue(snapshot);render(<ResourceVPA {...p}/>);expect(p.client.resourceVPA).not.toHaveBeenCalled();fireEvent.click(screen.getByRole('button',{name:'Load VPA recommendations'}));await screen.findByText('250m');expect(screen.getByText('0')).toBeInTheDocument();expect(screen.getByText(/Multiple matching VPA/)).toBeInTheDocument();expect(screen.getByText(/Access denied/)).toBeInTheDocument();expect(screen.getByText(/targetRef has no UID/)).toBeInTheDocument();expect(screen.getByText(/sample time, window and history are unknown/)).toBeInTheDocument();expect(screen.getByText('Update mode: Off')).toBeInTheDocument();fireEvent.click(screen.getByRole('button',{name:'autoscaling.k8s.io/verticalpodautoscalers/sizing'}));expect(p.onGo).toHaveBeenCalledWith(vpa);expect(p.client.resourceVPA).toHaveBeenCalledWith(subject,expect.any(AbortSignal))
})
it('hides retained values while refreshing, cancelled or failed; rejects a mismatched subject',async()=>{
 const p=props();vi.mocked(p.client.resourceVPA).mockResolvedValue(snapshot);render(<ResourceVPA {...p}/>);fireEvent.click(screen.getByRole('button',{name:'Load VPA recommendations'}));await screen.findByText('250m')
 let signal:AbortSignal|undefined;vi.mocked(p.client.resourceVPA).mockImplementation((_r,s)=>{signal=s;return new Promise(()=>{})});fireEvent.click(screen.getByRole('button',{name:'Load VPA recommendations'}));expect(screen.queryByText('250m')).not.toBeInTheDocument();await waitFor(()=>expect(p.client.resourceVPA).toHaveBeenCalledTimes(2));fireEvent.click(screen.getByRole('button',{name:'Cancel'}));expect(signal?.aborted).toBe(true);expect(screen.queryByText('250m')).not.toBeInTheDocument()
 vi.mocked(p.client.resourceVPA).mockResolvedValue({...snapshot,ref:{...subject,uid:'replacement'}});fireEvent.click(screen.getByRole('button',{name:'Load VPA recommendations'}));await screen.findByRole('alert');expect(screen.queryByText('250m')).not.toBeInTheDocument()
})
it('destroys admission on parent, revision, Ref, client and connection replacement and fences late replies',async()=>{
 const p=props();let answer:(value:VPAInspection)=>void=()=>{};let signal:AbortSignal|undefined
 vi.mocked(p.client.resourceVPA).mockImplementation((_r,s)=>{signal=s;return new Promise(resolve=>{answer=resolve})})
 const wrap=(connection:string,extra:Partial<typeof p>={})=><ViewConnectionContext value={connection}><ResourceVPA {...p} {...extra}/></ViewConnectionContext>;const view=render(wrap('initial'))
 for(const [i,extra] of [{snapshotKey:'new'},{revision:'new'},{subject:{...subject,uid:'new'}},{}].entries()){
  fireEvent.click(screen.getByRole('button',{name:'Load VPA recommendations'}));await waitFor(()=>expect(p.client.resourceVPA).toHaveBeenCalledTimes(i+1));view.rerender(wrap(String(i),extra));expect(signal?.aborted).toBe(true);await act(async()=>answer(snapshot));expect(screen.queryByText('250m')).not.toBeInTheDocument()
 }
 fireEvent.click(screen.getByRole('button',{name:'Load VPA recommendations'}));await waitFor(()=>expect(p.client.resourceVPA).toHaveBeenCalledTimes(5));const replacement=fakeClient([]).client;view.rerender(wrap('3',{client:replacement}));expect(signal?.aborted).toBe(true);await act(async()=>answer(snapshot));expect(replacement.resourceVPA).not.toHaveBeenCalled();expect(screen.queryByText('250m')).not.toBeInTheDocument()
})
it('renders known-stale sources without recommendations and never enables foreign-source navigation',async()=>{
 const p=props();vi.mocked(p.client.resourceVPA).mockResolvedValue({...snapshot,sources:[{...snapshot.sources[0],ref:{...vpa,target:'foreign'},freshness:'stale',state:'stale',recommendations:[]}]});render(<ResourceVPA {...p}/>);fireEvent.click(screen.getByRole('button',{name:'Load VPA recommendations'}));await screen.findByText(/Known stale generation/);expect(screen.queryByText('250m')).not.toBeInTheDocument();expect(screen.queryByRole('button',{name:'autoscaling.k8s.io/verticalpodautoscalers/sizing'})).not.toBeInTheDocument();expect(p.onGo).not.toHaveBeenCalled()
})
