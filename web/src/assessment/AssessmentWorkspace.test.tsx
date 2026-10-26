import {act,cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react'
import {afterEach,beforeEach,expect,it,vi} from 'vitest'
import AssessmentWorkspace from './AssessmentWorkspace'
import {fakeClient} from '../test/fakeClient'
import {setLanguage} from '../i18n'
import type {AssessmentSnapshot} from './types'
vi.mock('../components/ResourceDrawer',()=>({ResourceDrawer:({subject}:{subject:{uid:string}})=><div data-testid="resource">{subject.uid}</div>}))
beforeEach(()=>setLanguage('en'));afterEach(cleanup)
const ref={provider:'kubernetes',target:'t',scope:'blue',kind:'apps/deployments',name:'web',uid:'uid-web'}
const snapshot:AssessmentSnapshot={namespace:'blue',check:'configuration',capturedAt:0,partial:true,resources:[{ref,state:'partial',counts:{review:2,unknown:1,observed:3,excluded:0},highlights:[]}],coverage:[{kind:'apps/deployments',state:'complete'},{kind:'pods',state:'forbidden'}]}
const props=()=>({client:fakeClient([]).client,target:{provider:'kubernetes',id:'t'},scope:{mode:'one' as const,name:'blue'},scopePicker:<span>blue</span>,drawer:{} as never})
it('explains output, keeps acquisition explicit and opens the exact resource UID from the namespace summary',async()=>{
 const p=props();vi.mocked(p.client.assessNamespace).mockResolvedValue(snapshot);render(<AssessmentWorkspace {...p}/>)
 expect(screen.getByText(/Inspect probes, CPU\/memory/)).toBeVisible()
 fireEvent.click(screen.getByText('Scope and limits'));expect(p.client.assessNamespace).not.toHaveBeenCalled()
 fireEvent.click(screen.getByRole('button',{name:'Load configuration checks'}));await screen.findByText('apps/deployments/web')
 expect(p.client.assessNamespace).toHaveBeenCalledWith({provider:'kubernetes',target:'t',namespace:'blue',check:'configuration'},expect.any(AbortSignal))
 expect(screen.getByText('Resources checked: 1')).toBeVisible();expect(screen.getByText(/Some sources or resources/)).toBeVisible()
 fireEvent.click(screen.getByRole('button',{name:'Open resource'}));expect(screen.getByTestId('resource')).toHaveTextContent('uid-web')
})
it('requires one namespace and never expands an all or empty selection into a scan',()=>{
 const p=props();const v=render(<AssessmentWorkspace {...p} scope={{mode:'all'}}/>);expect(screen.getByRole('button',{name:'Load configuration checks'})).toBeDisabled();expect(screen.getByText('Select one namespace to run checks.')).toBeVisible()
 v.rerender(<AssessmentWorkspace {...p} scope={{mode:'some',names:[]}}/>);expect(screen.getByRole('button',{name:'Load configuration checks'})).toBeDisabled();expect(p.client.assessNamespace).not.toHaveBeenCalled()
})
it('cancels on namespace or tool change, fences late replies and requires fresh admission',async()=>{
 const p=props();let answer:(v:AssessmentSnapshot)=>void=()=>{};let signal:AbortSignal|undefined
 vi.mocked(p.client.assessNamespace).mockImplementation((_r,s)=>{signal=s;return new Promise(r=>{answer=r})})
 const v=render(<AssessmentWorkspace {...p}/>);fireEvent.click(screen.getByRole('button',{name:'Load configuration checks'}));await waitFor(()=>expect(p.client.assessNamespace).toHaveBeenCalledTimes(1))
 v.rerender(<AssessmentWorkspace {...p} scope={{mode:'one',name:'green'}}/>);expect(signal?.aborted).toBe(true);await act(async()=>answer(snapshot));expect(screen.queryByText('apps/deployments/web')).not.toBeInTheDocument()
 fireEvent.click(screen.getByRole('button',{name:'Load configuration checks'}));await waitFor(()=>expect(p.client.assessNamespace).toHaveBeenCalledTimes(2));fireEvent.click(screen.getByRole('button',{name:'CPU & memory recommendations (VPA)'}));expect(signal?.aborted).toBe(true);await act(async()=>answer(snapshot));expect(screen.queryByText('apps/deployments/web')).not.toBeInTheDocument();expect(p.client.assessNamespace).toHaveBeenCalledTimes(2)
})
it('hides stale results on refresh failure and rejects a foreign namespace response',async()=>{
 const p=props();vi.mocked(p.client.assessNamespace).mockResolvedValue(snapshot);render(<AssessmentWorkspace {...p}/>);fireEvent.click(screen.getByRole('button',{name:'Load configuration checks'}));await screen.findByText('apps/deployments/web')
 vi.mocked(p.client.assessNamespace).mockRejectedValue(new Error('Fixture denied'));fireEvent.click(screen.getByRole('button',{name:'Load configuration checks'}));expect(screen.queryByText('apps/deployments/web')).not.toBeInTheDocument();await screen.findByRole('alert')
 vi.mocked(p.client.assessNamespace).mockResolvedValue({...snapshot,resources:[{...snapshot.resources[0],ref:{...ref,scope:'foreign'}}]});fireEvent.click(screen.getByRole('button',{name:'Load configuration checks'}));await screen.findByText(/This object was replaced/);expect(screen.queryByText('apps/deployments/web')).not.toBeInTheDocument()
})

it('keeps VPA producer confidence, freshness and unknown recommendation time next to numerical previews',async()=>{
 const p=props();vi.mocked(p.client.assessNamespace).mockResolvedValue({...snapshot,check:'vpa',resources:[{...snapshot.resources[0],highlights:[{field:'sizing',code:'vpa',value:'CPU: 250m; RAM: —',sourceState:'low-confidence',freshness:'unknown'}]}]})
 render(<AssessmentWorkspace {...p}/>);fireEvent.click(screen.getByRole('button',{name:'CPU & memory recommendations (VPA)'}));fireEvent.click(screen.getByRole('button',{name:'Load VPA recommendations'}))
 await screen.findByText(/CPU: 250m/);expect(screen.getByText(/Producer reports low confidence/)).toBeVisible();expect(screen.getByText(/Observed generation: Unknown/)).toBeVisible();expect(screen.getByText(/sample time, window and history are unknown/)).toBeVisible()
})
