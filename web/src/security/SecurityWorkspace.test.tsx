import {act,cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react'
import {afterEach,beforeEach,expect,it,vi} from 'vitest'
import type {ComponentProps} from 'react'
import type {SecuritySnapshot} from '../api/types'
import {fakeClient} from '../test/fakeClient'
import {ViewConnectionContext} from '../views/useView'
import {setLanguage} from '../i18n'
import SecurityWorkspace from './SecurityWorkspace'

beforeEach(()=>setLanguage('en'));afterEach(cleanup)
const props=()=>({client:fakeClient([]).client,target:{provider:'kubernetes',id:'a'},scope:{mode:'one',name:'blue'},scopePicker:null,drawer:{} as ComponentProps<typeof SecurityWorkspace>['drawer']} satisfies ComponentProps<typeof SecurityWorkspace>)
const obsolete:SecuritySnapshot={findings:[],problems:[],discovery:'ready',truncated:false,capturedAt:0,resources:0,reports:0,cveState:'empty',auditSources:[{kind:'OBSOLETE-CLUSTER-EVIDENCE',apiVersion:'test/v1',clusterScoped:true,state:'available',reports:1}]}
it('starts without global consent, sends explicit opt-in, and cancels and hides an unchecked late response',async()=>{
 const p=props(),view=render(<SecurityWorkspace {...p}/>),read=vi.mocked(p.client.clusterSecurity)
 await waitFor(()=>expect(read).toHaveBeenCalledTimes(1));expect(read.mock.calls[0][0]).toEqual({provider:'kubernetes',target:'a',scope:p.scope,includeClusterReports:false,includeClusterPolicyReports:false,policyReportAPI:''})
 let answer:(s:SecuritySnapshot)=>void=()=>{};let signal:AbortSignal|undefined
 read.mockImplementationOnce((_r,s)=>{signal=s;return new Promise(resolve=>{answer=resolve})})
 const checkbox=screen.getByRole('checkbox',{name:'Include cluster-wide reports'})
 fireEvent.click(checkbox);await waitFor(()=>expect(read).toHaveBeenCalledTimes(2));expect(read.mock.calls[1][0].includeClusterReports).toBe(true);expect(checkbox).toBeChecked()
 fireEvent.click(checkbox);await waitFor(()=>expect(read).toHaveBeenCalledTimes(3));expect(signal?.aborted).toBe(true);expect(read.mock.calls[2][0].includeClusterReports).toBe(false)
 await act(async()=>answer(obsolete));expect(view.container).not.toHaveTextContent('OBSOLETE-CLUSTER-EVIDENCE');expect(checkbox).not.toBeChecked()
})

it('selects exactly one namespaced policy API and fences the previous API response',async()=>{
 const p=props(),view=render(<SecurityWorkspace {...p}/>),read=vi.mocked(p.client.clusterSecurity)
 await waitFor(()=>expect(read).toHaveBeenCalledTimes(1));expect(read.mock.lastCall?.[0].policyReportAPI).toBe('')
 let answer:(s:SecuritySnapshot)=>void=()=>{};let signal:AbortSignal|undefined
 read.mockImplementationOnce((_r,s)=>{signal=s;return new Promise(resolve=>{answer=resolve})})
 fireEvent.click(screen.getByRole('button',{name:'Policy report API'}));fireEvent.click(screen.getByRole('option',{name:'wgpolicyk8s.io · PolicyReport'}))
 await waitFor(()=>expect(read.mock.lastCall?.[0].policyReportAPI).toBe('wgpolicy'))
 fireEvent.click(screen.getByRole('button',{name:'Policy report API'}));fireEvent.click(screen.getByRole('option',{name:'openreports.io · Report'}))
 await waitFor(()=>expect(read.mock.lastCall?.[0].policyReportAPI).toBe('openreports'));expect(signal?.aborted).toBe(true)
 await act(async()=>answer(obsolete));expect(view.container).not.toHaveTextContent('OBSOLETE-CLUSTER-EVIDENCE')
 fireEvent.click(screen.getByRole('button',{name:'Policy report API'}));fireEvent.click(screen.getByRole('option',{name:/^Off$/}))
 await waitFor(()=>expect(read.mock.lastCall?.[0].policyReportAPI).toBe(''))
})

it.each(['client','target','connection'] as const)('resets namespaced policy selection on %s replacement and ignores stale evidence',async mode=>{
 const p=props(),read=vi.mocked(p.client.clusterSecurity)
 const wrap=(connection:string,extra:Partial<typeof p>={})=><ViewConnectionContext value={connection}><SecurityWorkspace {...p} {...extra}/></ViewConnectionContext>
 const view=render(wrap('initial'));await waitFor(()=>expect(read).toHaveBeenCalledTimes(1))
 let answer:(s:SecuritySnapshot)=>void=()=>{};let signal:AbortSignal|undefined
 read.mockImplementationOnce((_r,s)=>{signal=s;return new Promise(resolve=>{answer=resolve})})
 fireEvent.click(screen.getByRole('button',{name:'Policy report API'}));fireEvent.click(screen.getByRole('option',{name:'openreports.io · Report'}))
 await waitFor(()=>expect(read.mock.lastCall?.[0].policyReportAPI).toBe('openreports'))
 const client=mode==='client'?fakeClient([]).client:p.client
 view.rerender(wrap(mode==='connection'?'replacement':'initial',{client,...(mode==='target'?{target:{provider:'kubernetes',id:'b'}}:{})}))
 await waitFor(()=>expect(vi.mocked(client.clusterSecurity).mock.lastCall?.[0].policyReportAPI).toBe(''));expect(signal?.aborted).toBe(true)
 await act(async()=>answer(obsolete));expect(view.container).not.toHaveTextContent('OBSOLETE-CLUSTER-EVIDENCE')
 expect(screen.getByRole('button',{name:'Policy report API'})).toHaveTextContent('Off')
 view.rerender(wrap('initial'));await waitFor(()=>expect(read.mock.lastCall?.[0].policyReportAPI).toBe(''))
})
it.each(['client','target','connection'] as const)('resets explicit global consent on %s replacement and rejects late evidence',async mode=>{
 const p=props(),read=vi.mocked(p.client.clusterSecurity)
 const wrap=(connection:string,extra:Partial<typeof p>={})=><ViewConnectionContext value={connection}><SecurityWorkspace {...p} {...extra}/></ViewConnectionContext>
 const view=render(wrap('initial'));await waitFor(()=>expect(read).toHaveBeenCalledTimes(1))
 let answer:(s:SecuritySnapshot)=>void=()=>{};let signal:AbortSignal|undefined
 read.mockImplementationOnce((_r,s)=>{signal=s;return new Promise(resolve=>{answer=resolve})})
 fireEvent.click(screen.getByRole('checkbox',{name:'Include cluster-wide reports'}));await waitFor(()=>expect(read).toHaveBeenCalledTimes(2));expect(read.mock.calls[1][0].includeClusterReports).toBe(true)
 const client=mode==='client'?fakeClient([]).client:p.client
 view.rerender(wrap(mode==='connection'?'replacement':'initial',{client,...(mode==='target'?{target:{provider:'kubernetes',id:'b'}}:{})}))
 await waitFor(()=>expect(vi.mocked(client.clusterSecurity).mock.lastCall?.[0].includeClusterReports).toBe(false));expect(signal?.aborted).toBe(true);expect(screen.getByRole('checkbox',{name:'Include cluster-wide reports'})).not.toBeChecked()
 await act(async()=>answer(obsolete));expect(view.container).not.toHaveTextContent('OBSOLETE-CLUSTER-EVIDENCE')
 // Returning to the old target/incarnation must not resurrect its previous opt-in.
 view.rerender(wrap('initial'));await waitFor(()=>expect(read.mock.lastCall?.[0].includeClusterReports).toBe(false));expect(screen.getByRole('checkbox',{name:'Include cluster-wide reports'})).not.toBeChecked()
})
it('keeps global evidence explicitly labelled when the selected namespace changes',async()=>{
 const p=props(),view=render(<SecurityWorkspace {...p}/>),read=vi.mocked(p.client.clusterSecurity)
 await waitFor(()=>expect(read).toHaveBeenCalledTimes(1));fireEvent.click(screen.getByRole('checkbox',{name:'Include cluster-wide reports'}));await waitFor(()=>expect(read).toHaveBeenCalledTimes(2))
 view.rerender(<SecurityWorkspace {...p} scope={{mode:'one',name:'green'}}/>);await waitFor(()=>expect(read).toHaveBeenCalledTimes(3))
 expect(read.mock.calls[2][0]).toMatchObject({scope:{mode:'one',name:'green'},includeClusterReports:true});expect(screen.getByText(/^Also reads ClusterConfigAuditReport/)).toBeVisible()
})

it('requires independent global policy opt-in, cancels late global evidence and clears consent when API changes',async()=>{
 const p=props(),view=render(<SecurityWorkspace {...p}/>),read=vi.mocked(p.client.clusterSecurity)
 await waitFor(()=>expect(read).toHaveBeenCalledTimes(1))
 const checkbox=screen.getByRole('checkbox',{name:'Include cluster-wide policy reports'})
 expect(checkbox).toBeDisabled();expect(checkbox).not.toBeChecked()
 fireEvent.click(screen.getByRole('button',{name:'Policy report API'}));fireEvent.click(screen.getByRole('option',{name:'wgpolicyk8s.io · PolicyReport'}))
 await waitFor(()=>expect(read.mock.lastCall?.[0].policyReportAPI).toBe('wgpolicy'));expect(checkbox).toBeEnabled();expect(checkbox).not.toBeChecked()
 let answer:(s:SecuritySnapshot)=>void=()=>{};let signal:AbortSignal|undefined
 read.mockImplementationOnce((_r,s)=>{signal=s;return new Promise(resolve=>{answer=resolve})})
 fireEvent.click(checkbox);await waitFor(()=>expect(read.mock.lastCall?.[0].includeClusterPolicyReports).toBe(true))
 fireEvent.click(screen.getByRole('button',{name:'Policy report API'}));fireEvent.click(screen.getByRole('option',{name:'openreports.io · Report'}))
 await waitFor(()=>expect(read.mock.lastCall?.[0]).toMatchObject({policyReportAPI:'openreports',includeClusterPolicyReports:false,includeClusterReports:false}));expect(signal?.aborted).toBe(true)
 await act(async()=>answer(obsolete));expect(view.container).not.toHaveTextContent('OBSOLETE-CLUSTER-EVIDENCE');expect(checkbox).not.toBeChecked()
 fireEvent.click(checkbox);await waitFor(()=>expect(read.mock.lastCall?.[0].includeClusterPolicyReports).toBe(true))
 view.rerender(<SecurityWorkspace {...p} scope={{mode:'one',name:'green'}}/>);await waitFor(()=>expect(read.mock.lastCall?.[0]).toMatchObject({scope:{mode:'one',name:'green'},includeClusterPolicyReports:true}))
 fireEvent.click(checkbox);await waitFor(()=>expect(read.mock.lastCall?.[0].includeClusterPolicyReports).toBe(false))
})
it.each(['client','target','connection'] as const)('resets global policy consent on %s replacement and fences obsolete global evidence',async mode=>{
 const p=props(),read=vi.mocked(p.client.clusterSecurity)
 const wrap=(connection:string,extra:Partial<typeof p>={})=><ViewConnectionContext value={connection}><SecurityWorkspace {...p} {...extra}/></ViewConnectionContext>
 const view=render(wrap('initial'));await waitFor(()=>expect(read).toHaveBeenCalledTimes(1))
 fireEvent.click(screen.getByRole('button',{name:'Policy report API'}));fireEvent.click(screen.getByRole('option',{name:'openreports.io · Report'}))
 await waitFor(()=>expect(read.mock.lastCall?.[0].policyReportAPI).toBe('openreports'))
 let answer:(s:SecuritySnapshot)=>void=()=>{};let signal:AbortSignal|undefined
 read.mockImplementationOnce((_r,s)=>{signal=s;return new Promise(resolve=>{answer=resolve})})
 fireEvent.click(screen.getByRole('checkbox',{name:'Include cluster-wide policy reports'}));await waitFor(()=>expect(read.mock.lastCall?.[0].includeClusterPolicyReports).toBe(true))
 const client=mode==='client'?fakeClient([]).client:p.client
 view.rerender(wrap(mode==='connection'?'replacement':'initial',{client,...(mode==='target'?{target:{provider:'kubernetes',id:'b'}}:{})}))
 await waitFor(()=>expect(vi.mocked(client.clusterSecurity).mock.lastCall?.[0]).toMatchObject({policyReportAPI:'',includeClusterPolicyReports:false}));expect(signal?.aborted).toBe(true)
 await act(async()=>answer(obsolete));expect(view.container).not.toHaveTextContent('OBSOLETE-CLUSTER-EVIDENCE')
 expect(screen.getByRole('checkbox',{name:'Include cluster-wide policy reports'})).not.toBeChecked()
 view.rerender(wrap('initial'));await waitFor(()=>expect(read.mock.lastCall?.[0].includeClusterPolicyReports).toBe(false))
})
