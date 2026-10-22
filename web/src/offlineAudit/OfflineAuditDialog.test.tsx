import {act,fireEvent,render,screen,waitFor} from '@testing-library/react'
import {afterEach,expect,it,vi} from 'vitest'
import OfflineAuditDialog from './OfflineAuditDialog'
import {fixture,storedFixture} from './fixture'
import {MAX_BYTES,type AuditResponse} from './types'
import type {Target} from '../api/types'
import {fakeClient} from '../test/fakeClient'
const target={provider:'kubernetes',id:'fixture-target',configRev:'fixture-revision',title:'Declared target'} as Target
let readers:Reader[]=[]
class Reader{
 result:ArrayBuffer|null=null;onload:(()=>void)|null=null;onerror:(()=>void)|null=null
 abort=vi.fn();readAsArrayBuffer=vi.fn()
 constructor(){readers.push(this)}
 finish(){const b=new TextEncoder().encode(JSON.stringify(fixture()));this.result=new ArrayBuffer(b.length);new Uint8Array(this.result).set(b);this.onload?.()}
}
afterEach(()=>{vi.useRealTimers();vi.unstubAllGlobals();readers=[]})
function setup(client=fakeClient([]).client){vi.stubGlobal('FileReader',Reader);const ui=render(<OfflineAuditDialog client={client} target={target} onClose={vi.fn()}/>);const input=screen.getByLabelText('Choose JSON report') as HTMLInputElement;fireEvent.click(screen.getByRole('checkbox'));return {...ui,input,client}}
const choose=(input:HTMLInputElement,size=100)=>fireEvent.change(input,{target:{files:[new File(['x'.repeat(size)],'PRIVATE-filename.json')]}})
it('requires explicit target context and producer assertion, rejects oversize before FileReader',async()=>{
 const client=fakeClient([]).client;client.offlineAudit=vi.fn(async()=>({reports:[]}));vi.stubGlobal('FileReader',Reader)
 const ui=render(<OfflineAuditDialog client={client} target={null} onClose={vi.fn()}/>);expect(screen.getByRole('checkbox')).toBeDisabled();expect(screen.getByLabelText('Choose JSON report')).toBeDisabled()
 ui.rerender(<OfflineAuditDialog client={client} target={target} onClose={vi.fn()}/>);expect(screen.getByLabelText('Choose JSON report')).toBeDisabled();fireEvent.click(screen.getByRole('checkbox'));choose(screen.getByLabelText('Choose JSON report'),MAX_BYTES+1);expect(readers).toHaveLength(0);expect(screen.getByRole('alert')).toBeInTheDocument()
 await waitFor(()=>expect(vi.mocked(client.offlineAudit).mock.calls.some(c=>c[0].command==='status')).toBe(true));expect(vi.mocked(client.offlineAudit).mock.calls.filter(c=>c[0].command==='import')).toHaveLength(0)
})
it('sends raw bytes only to explicit local import with context and renders typed native evidence literally',async()=>{
 const client=fakeClient([]).client,stored=storedFixture();stored.report.subjects[0].checks[0].id='<script>literal</script>'
 client.offlineAudit=vi.fn(async req=>req.command==='status'?{reports:[]}:{reports:[stored.summary],selected:stored});const {input}=setup(client);choose(input);act(()=>readers[0].finish());await waitFor(()=>expect(screen.getByText('<script>literal</script>')).toBeInTheDocument())
 const req=vi.mocked(client.offlineAudit).mock.calls.find(c=>c[0].command==='import')![0];expect(JSON.parse(atob(req.data!))).toEqual(fixture());expect(req).toMatchObject({target:target.id,configRev:target.configRev,version:'10.2.5'});expect(req).not.toHaveProperty('filename');expect(document.body.textContent).not.toContain('PRIVATE-');expect(screen.queryByRole('link')).not.toBeInTheDocument();expect(screen.getByText(/^Reported failure ·/u)).toBeInTheDocument();expect(screen.getByText(/^Reported pass ·/u)).toBeInTheDocument()
})
it('retains backend reports across panel remount and explicitly forgets rather than persisting source',async()=>{
 const client=fakeClient([]).client,stored=storedFixture();client.offlineAudit=vi.fn(async req=>req.command==='get'?{reports:[stored.summary],selected:stored}:req.command==='forget'?{reports:[]}:{reports:[stored.summary]})
 const ui=setup(client);await waitFor(()=>expect(screen.getByRole('button',{name:'Open report'})).toBeInTheDocument());fireEvent.click(screen.getByRole('button',{name:'Open report'}));await waitFor(()=>expect(screen.getByText('cpuRequestsMissing')).toBeInTheDocument());ui.unmount();setup(client);await waitFor(()=>expect(screen.getByRole('button',{name:'Open report'})).toBeInTheDocument());expect(screen.queryByText('cpuRequestsMissing')).not.toBeInTheDocument();fireEvent.click(screen.getByRole('button',{name:'Forget'}));await waitFor(()=>expect(screen.queryByRole('button',{name:'Open report'})).not.toBeInTheDocument())
})
it('Cancel and unmount abort readers and requests and fence late successful answers',async()=>{
 const client=fakeClient([]).client,stored=storedFixture();let finish!:(r:AuditResponse)=>void;let signal:AbortSignal|undefined
 client.offlineAudit=vi.fn((req,s)=>req.command==='status'?Promise.resolve({reports:[]}):new Promise<AuditResponse>(resolve=>{finish=resolve;signal=s}));const {input,unmount}=setup(client);choose(input);fireEvent.click(screen.getByRole('button',{name:'Cancel'}));expect(readers[0].abort).toHaveBeenCalled();act(()=>readers[0].finish());expect(vi.mocked(client.offlineAudit).mock.calls.filter(c=>c[0].command==='import')).toHaveLength(0)
 choose(input);act(()=>readers[1].finish());await waitFor(()=>expect(finish).toBeDefined());fireEvent.click(screen.getByRole('button',{name:'Cancel'}));expect(signal?.aborted).toBe(true);await act(async()=>finish({reports:[stored.summary],selected:stored}));expect(screen.queryByText('cpuRequestsMissing')).not.toBeInTheDocument();choose(input);unmount();expect(readers[2].abort).toHaveBeenCalled()
})
it('target configuration and Client replacement clear admission and fence previous imports',async()=>{
 const client=fakeClient([]).client;let finish!:(r:AuditResponse)=>void;let signal:AbortSignal|undefined
 client.offlineAudit=vi.fn((req,s)=>req.command==='status'?Promise.resolve({reports:[]}):new Promise<AuditResponse>(resolve=>{finish=resolve;signal=s}));const ui=setup(client);choose(ui.input);act(()=>readers[0].finish());await waitFor(()=>expect(finish).toBeDefined());ui.rerender(<OfflineAuditDialog client={client} target={{...target,configRev:'new-revision'}} onClose={vi.fn()}/>);expect(signal?.aborted).toBe(true);expect(screen.getByRole('checkbox')).not.toBeChecked();await act(async()=>finish({reports:[],selected:storedFixture()}));expect(screen.queryByText('cpuRequestsMissing')).not.toBeInTheDocument()
 fireEvent.click(screen.getByRole('checkbox'));choose(screen.getByLabelText('Choose JSON report'));const next=fakeClient([]).client;next.offlineAudit=vi.fn(async()=>({reports:[]}));ui.rerender(<OfflineAuditDialog client={next} target={target} onClose={vi.fn()}/>);expect(readers[1].abort).toHaveBeenCalled();expect(screen.getByRole('checkbox')).not.toBeChecked();act(()=>readers[1].finish());expect(vi.mocked(next.offlineAudit).mock.calls.filter(c=>c[0].command==='import')).toHaveLength(0)
})
it('failed replacement hides selected values and ignored evidence is not shown as pass',async()=>{
 const client=fakeClient([]).client,stored=storedFixture();stored.report.subjects[0].checks[0].severity='ignore';let reject=false
 client.offlineAudit=vi.fn(async req=>{if(req.command==='status')return {reports:[]};if(reject)throw Error('PRIVATE-ERROR');return {reports:[stored.summary],selected:stored}});const {input}=setup(client);choose(input);act(()=>readers[0].finish());await waitFor(()=>expect(screen.getByText(/^Ignored; not a pass ·/u)).toBeInTheDocument());reject=true;choose(input);act(()=>readers[1].finish());await waitFor(()=>expect(screen.getByRole('alert')).toBeInTheDocument());expect(screen.queryByText('cpuRequestsMissing')).not.toBeInTheDocument();expect(document.body.textContent).not.toContain('PRIVATE-ERROR')
})

it('expires already displayed renderer evidence and retained summaries without another API read',async()=>{
 vi.useFakeTimers();const now=Date.now();const client=fakeClient([]).client,stored=storedFixture();stored.summary.expiresAt=now+1000
 client.offlineAudit=vi.fn(async req=>req.command==='get'?{reports:[stored.summary],selected:stored}:{reports:[stored.summary]})
 vi.stubGlobal('FileReader',Reader);render(<OfflineAuditDialog client={client} target={target} onClose={vi.fn()}/>);await act(async()=>{})
 fireEvent.click(screen.getByRole('button',{name:'Open report'}));await act(async()=>{});expect(screen.getByText('cpuRequestsMissing')).toBeInTheDocument();const before=vi.mocked(client.offlineAudit).mock.calls.length
 await act(async()=>{vi.advanceTimersByTime(1001)});expect(screen.queryByText('cpuRequestsMissing')).not.toBeInTheDocument();expect(screen.queryByRole('button',{name:'Open report'})).not.toBeInTheDocument();expect(vi.mocked(client.offlineAudit).mock.calls).toHaveLength(before)
})
it('rejects a get response that expires while in flight instead of reviving old evidence',async()=>{
 vi.useFakeTimers();const stored=storedFixture();stored.summary.expiresAt=Date.now()+1000;const client=fakeClient([]).client;let finish!:(r:AuditResponse)=>void
 client.offlineAudit=vi.fn(req=>req.command==='get'?new Promise<AuditResponse>(resolve=>{finish=resolve}):Promise.resolve({reports:[stored.summary]}));render(<OfflineAuditDialog client={client} target={target} onClose={vi.fn()}/>);await act(async()=>{});fireEvent.click(screen.getByRole('button',{name:'Open report'}));await act(async()=>{vi.advanceTimersByTime(1001)});await act(async()=>finish({reports:[stored.summary],selected:stored}));expect(screen.queryByText('cpuRequestsMissing')).not.toBeInTheDocument();expect(screen.getByRole('alert')).toBeInTheDocument();expect(screen.queryByRole('button',{name:'Open report'})).not.toBeInTheDocument()
})
