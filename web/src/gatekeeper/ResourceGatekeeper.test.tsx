import {act,cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react'
import {afterEach,beforeEach,it,expect,vi} from 'vitest'
import {ResourceGatekeeper} from './ResourceGatekeeper'
import type {GatekeeperAudit} from './types'
import {fakeClient} from '../test/fakeClient'
import {ViewConnectionContext} from '../views/useView'
import {setLanguage} from '../i18n'
beforeEach(()=>setLanguage('en'));afterEach(cleanup)
const subject={provider:'kubernetes',target:'t',kind:'constraints.gatekeeper.sh/k8srequiredlabels',name:'required-labels',uid:'constraint-uid'}
const snapshot:GatekeeperAudit={ref:subject,apiVersion:'constraints.gatekeeper.sh/v1beta1',resourceVersion:'7',capturedAt:1,auditTimeState:'reported',auditTime:'2026-10-22T00:00:00Z',totalViolations:5,returnedViolations:1,coverage:'producer-capped',partial:true,violations:[{group:'',version:'v1',kind:'Secret',namespace:'foreign',name:'descriptive-secret',message:'literal <script>not executed</script>',enforcementAction:'scoped',enforcementActions:['warn','dryrun']}]}
const props=()=>({client:fakeClient([]).client,subject,snapshotKey:'one',revision:'one'})
it('reads only on explicit load and keeps capped totals, native actions and unresolved subjects distinct',async()=>{
 const p=props();vi.mocked(p.client.gatekeeperAudit).mockResolvedValue(snapshot);const view=render(<ResourceGatekeeper {...p}/>);expect(p.client.gatekeeperAudit).not.toHaveBeenCalled();fireEvent.click(screen.getByRole('button',{name:'Load constraint audit'}));await screen.findByText(/Reported total: 5/);expect(screen.getByText(/Returned: 1/)).toBeInTheDocument();expect(screen.getByText(/Producer reports more violations/)).toBeInTheDocument();expect(screen.getByText('literal <script>not executed</script>')).toBeInTheDocument();expect(view.container.querySelector('script')).toBeNull();expect(screen.getByText(/v1 · Secret · foreign · descriptive-secret/)).toBeInTheDocument();expect(screen.queryByRole('button',{name:/descriptive-secret/})).not.toBeInTheDocument();expect(p.client.gatekeeperAudit).toHaveBeenCalledWith(subject,expect.any(AbortSignal));expect(screen.getByText('enforcementActions').parentElement).toHaveTextContent('enforcementActions: warn, dryrun')
})
it('displays reported zero without calling it pass, and treats absent time and totals as unknown',async()=>{
 const p=props();vi.mocked(p.client.gatekeeperAudit).mockResolvedValue({...snapshot,totalViolations:0,returnedViolations:0,violations:[],coverage:'reported',partial:false,auditTimeState:'missing',auditTime:undefined});render(<ResourceGatekeeper {...p}/>);fireEvent.click(screen.getByRole('button',{name:'Load constraint audit'}));await screen.findByText(/Reported total: 0/);expect(screen.getByText(/zero reported violations does not prove a pass/)).toBeInTheDocument();expect(screen.getByText(/audit completeness remains unverified/)).toBeInTheDocument();expect(screen.getByText('status.auditTimestamp').parentElement).toHaveTextContent('status.auditTimestamp: Not reported')
})
it('hides old evidence on refresh, cancellation and error and fences a late response',async()=>{
 const p=props();vi.mocked(p.client.gatekeeperAudit).mockResolvedValue(snapshot);render(<ResourceGatekeeper {...p}/>);const load=()=>fireEvent.click(screen.getByRole('button',{name:'Load constraint audit'}));load();await screen.findByText(/Reported total: 5/)
 let answer:(v:GatekeeperAudit)=>void=()=>{};let signal:AbortSignal|undefined;vi.mocked(p.client.gatekeeperAudit).mockImplementation((_r,s)=>{signal=s;return new Promise(resolve=>{answer=resolve})});load();expect(screen.queryByText(/Reported total: 5/)).not.toBeInTheDocument();await waitFor(()=>expect(p.client.gatekeeperAudit).toHaveBeenCalledTimes(2));fireEvent.click(screen.getByRole('button',{name:'Cancel'}));expect(signal?.aborted).toBe(true);await act(async()=>answer(snapshot));expect(screen.queryByText(/Reported total: 5/)).not.toBeInTheDocument()
 vi.mocked(p.client.gatekeeperAudit).mockRejectedValue(new Error('safe failure'));load();await screen.findByRole('alert');expect(screen.queryByText(/Reported total: 5/)).not.toBeInTheDocument()
})
it('resets consent on parent, revision, Ref, connection and client replacement without automatic reads',async()=>{
 const p=props();let answer:(v:GatekeeperAudit)=>void=()=>{},signal:AbortSignal|undefined;vi.mocked(p.client.gatekeeperAudit).mockImplementation((_r,s)=>{signal=s;return new Promise(resolve=>{answer=resolve})});const wrap=(connection:string,extra:Partial<typeof p>={})=><ViewConnectionContext value={connection}><ResourceGatekeeper {...p} {...extra}/></ViewConnectionContext>;const view=render(wrap('initial'))
 for(const [i,extra] of [{snapshotKey:'new'},{revision:'new'},{subject:{...subject,uid:'new'}},{}].entries()){
  fireEvent.click(screen.getByRole('button',{name:'Load constraint audit'}));await waitFor(()=>expect(p.client.gatekeeperAudit).toHaveBeenCalledTimes(i+1));view.rerender(wrap(String(i),extra));expect(signal?.aborted).toBe(true);await act(async()=>answer(snapshot));expect(screen.queryByText(/Reported total: 5/)).not.toBeInTheDocument()
 }
 fireEvent.click(screen.getByRole('button',{name:'Load constraint audit'}));await waitFor(()=>expect(p.client.gatekeeperAudit).toHaveBeenCalledTimes(5));const replacement=fakeClient([]).client;view.rerender(wrap('3',{client:replacement}));expect(signal?.aborted).toBe(true);await act(async()=>answer(snapshot));expect(replacement.gatekeeperAudit).not.toHaveBeenCalled()
})
it('rejects source UID, target, API or revision mismatch',async()=>{
 for(const change of [{ref:{...subject,uid:'new'}},{ref:{...subject,target:'other'}},{apiVersion:'foreign/v1'},{resourceVersion:''}]){
  const p=props();vi.mocked(p.client.gatekeeperAudit).mockResolvedValue({...snapshot,...change});const view=render(<ResourceGatekeeper {...p}/>);fireEvent.click(screen.getByRole('button',{name:'Load constraint audit'}));await screen.findByRole('alert');expect(screen.queryByText(/Reported total: 5/)).not.toBeInTheDocument();view.unmount()
 }
})
