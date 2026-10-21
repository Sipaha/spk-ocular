import {act,cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react'
import {afterEach,beforeEach,it,expect,vi} from 'vitest'
import {ResourceMetricsHistory} from './ResourceMetricsHistory'
import type {MetricsHistory} from './types'
import {fakeClient} from '../test/fakeClient'
import {ViewConnectionContext} from '../views/useView'
import {setLanguage} from '../i18n'
beforeEach(()=>setLanguage('en'));afterEach(cleanup)
const subject={provider:'kubernetes',target:'t',kind:'pods',scope:'blue',name:'web',uid:'12345678-abcd-1234-abcd-123456789012'}
const snapshot:MetricsHistory={ref:subject,resourceVersion:'7',source:'https://source.invalid',scope:'dedicated-asserted',capturedAt:180000,requestedStart:0,start:0,end:180,step:60,metrics:[{name:'cpu',state:'partial',series:[{container:'app',incarnation:'one',values:[0,null,1,2]}]},{name:'throttling',state:'unavailable',series:[]}],declarations:[{container:'app',cpuRequest:.1,cpuLimit:null,memoryRequest:100,memoryLimit:null}]}
const props=()=>({client:fakeClient([]).client,subject,snapshotKey:'one',revision:'one'})
function configure(){fireEvent.change(screen.getByLabelText('Prometheus base URL'),{target:{value:'https://source.invalid'}});fireEvent.change(screen.getByLabelText('Bearer token (optional, memory only)'),{target:{value:'PRIVATE'}});fireEvent.click(screen.getByRole('checkbox'))}
it('requires explicit source admission, renders real zeros and gaps, and labels current declarations',async()=>{
 const p=props();vi.mocked(p.client.resourceMetricsHistory).mockResolvedValue(snapshot);const view=render(<ResourceMetricsHistory {...p}/>);expect(p.client.resourceMetricsHistory).not.toHaveBeenCalled();expect(screen.getByRole('button',{name:'Load history'})).toBeDisabled();configure();fireEvent.click(screen.getByRole('button',{name:'Load history'}));await screen.findByText(/Points: 3\/4/);expect(screen.getByText('Current declarations')).toBeInTheDocument();expect(screen.getByText(/Unavailable: source/)).toBeInTheDocument();const line=view.container.querySelector('[data-history-metric="cpu"] svg path[stroke-width]');expect(line?.getAttribute('d')?.match(/M/g)).toHaveLength(2);expect(p.client.resourceMetricsHistory).toHaveBeenCalledWith(expect.objectContaining({source:expect.objectContaining({token:'PRIVATE',dedicated:true}),ref:subject}),expect.any(AbortSignal));expect(view.container.textContent).not.toContain('PRIVATE')
})
it('clears credentials and evidence when source changes; never sends the old token to a new URL',async()=>{
 const p=props();vi.mocked(p.client.resourceMetricsHistory).mockResolvedValue(snapshot);render(<ResourceMetricsHistory {...p}/>);configure();fireEvent.click(screen.getByRole('button',{name:'Load history'}));await screen.findByText(/Points: 3\/4/);fireEvent.change(screen.getByLabelText('Prometheus base URL'),{target:{value:'https://new.invalid'}});expect(screen.queryByText(/Points: 3\/4/)).not.toBeInTheDocument();expect(screen.getByLabelText('Bearer token (optional, memory only)')).toHaveValue('');expect(p.client.resourceMetricsHistory).toHaveBeenCalledTimes(1);fireEvent.click(screen.getByRole('button',{name:'Load history'}));await waitFor(()=>expect(p.client.resourceMetricsHistory).toHaveBeenCalledTimes(2));expect(vi.mocked(p.client.resourceMetricsHistory).mock.calls[1][0].source.token).toBe('')
})
it('cancels in-flight reads and rejects late evidence on parent result, revision, UID and connection replacement',async()=>{
 const p=props();let answer:(v:MetricsHistory)=>void=()=>{};let signal:AbortSignal|undefined
 vi.mocked(p.client.resourceMetricsHistory).mockImplementation((_r,s)=>{signal=s;return new Promise(r=>{answer=r})})
 const wrap=(connection:string,extra:Partial<typeof p>={})=><ViewConnectionContext value={connection}><ResourceMetricsHistory {...p} {...extra}/></ViewConnectionContext>;const view=render(wrap('one'))
 for(const [i,extra] of [{snapshotKey:'new'},{revision:'new'},{subject:{...subject,uid:'new'}},{}].entries()){
  configure();fireEvent.click(screen.getByRole('button',{name:'Load history'}));await waitFor(()=>expect(p.client.resourceMetricsHistory).toHaveBeenCalledTimes(i+1));view.rerender(wrap(String(i),extra));expect(signal?.aborted).toBe(true);await act(async()=>answer(snapshot));expect(screen.queryByText(/Points: 3\/4/)).not.toBeInTheDocument();expect(screen.getByLabelText('Bearer token (optional, memory only)')).toHaveValue('')
 }
})
it('hides retained evidence during refresh, cancellation and failures',async()=>{
 const p=props();vi.mocked(p.client.resourceMetricsHistory).mockResolvedValue(snapshot);render(<ResourceMetricsHistory {...p}/>);configure();fireEvent.click(screen.getByRole('button',{name:'Load history'}));await screen.findByText(/Points: 3\/4/)
 vi.mocked(p.client.resourceMetricsHistory).mockImplementation(()=>new Promise(()=>{}));fireEvent.click(screen.getByRole('button',{name:'Load history'}));expect(screen.queryByText(/Points: 3\/4/)).not.toBeInTheDocument();fireEvent.click(screen.getByRole('button',{name:'Cancel'}));expect(screen.queryByText(/Points: 3\/4/)).not.toBeInTheDocument()
 vi.mocked(p.client.resourceMetricsHistory).mockRejectedValue(new Error('Fixture unavailable'));fireEvent.click(screen.getByRole('button',{name:'Load history'}));await screen.findByRole('alert');expect(screen.queryByText(/Points: 3\/4/)).not.toBeInTheDocument()
})

it('clears source credentials and requires new admission when the API client changes under the same Ref',async()=>{
 const p=props();vi.mocked(p.client.resourceMetricsHistory).mockResolvedValue(snapshot);const view=render(<ResourceMetricsHistory {...p}/>);configure();fireEvent.click(screen.getByRole('button',{name:'Load history'}));await screen.findByText(/Points: 3\/4/)
 const replacement=fakeClient([]).client;view.rerender(<ResourceMetricsHistory {...p} client={replacement}/>);expect(screen.getByLabelText('Bearer token (optional, memory only)')).toHaveValue('');expect(screen.getByLabelText('Prometheus base URL')).toHaveValue('');expect(screen.queryByText(/Points: 3\/4/)).not.toBeInTheDocument();await act(async()=>{});expect(replacement.resourceMetricsHistory).not.toHaveBeenCalled();expect(screen.getByRole('button',{name:'Load history'})).toBeDisabled()
})
