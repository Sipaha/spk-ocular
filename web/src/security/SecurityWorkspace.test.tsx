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
 await waitFor(()=>expect(read).toHaveBeenCalledTimes(1));expect(read.mock.calls[0][0]).toEqual({provider:'kubernetes',target:'a',scope:p.scope,includeClusterReports:false})
 let answer:(s:SecuritySnapshot)=>void=()=>{};let signal:AbortSignal|undefined
 read.mockImplementationOnce((_r,s)=>{signal=s;return new Promise(resolve=>{answer=resolve})})
 const checkbox=screen.getByRole('checkbox',{name:'Include cluster-wide reports'})
 fireEvent.click(checkbox);await waitFor(()=>expect(read).toHaveBeenCalledTimes(2));expect(read.mock.calls[1][0].includeClusterReports).toBe(true);expect(checkbox).toBeChecked()
 fireEvent.click(checkbox);await waitFor(()=>expect(read).toHaveBeenCalledTimes(3));expect(signal?.aborted).toBe(true);expect(read.mock.calls[2][0].includeClusterReports).toBe(false)
 await act(async()=>answer(obsolete));expect(view.container).not.toHaveTextContent('OBSOLETE-CLUSTER-EVIDENCE');expect(checkbox).not.toBeChecked()
})
it.each(['client','target','connection'] as const)('resets explicit global consent on %s replacement and rejects late evidence',async mode=>{
 const p=props(),read=vi.mocked(p.client.clusterSecurity)
 const wrap=(connection:string,extra:Partial<typeof p>={})=><ViewConnectionContext value={connection}><SecurityWorkspace {...p} {...extra}/></ViewConnectionContext>
 const view=render(wrap('initial'));await waitFor(()=>expect(read).toHaveBeenCalledTimes(1))
 let answer:(s:SecuritySnapshot)=>void=()=>{};let signal:AbortSignal|undefined
 read.mockImplementationOnce((_r,s)=>{signal=s;return new Promise(resolve=>{answer=resolve})})
 fireEvent.click(screen.getByRole('checkbox'));await waitFor(()=>expect(read).toHaveBeenCalledTimes(2));expect(read.mock.calls[1][0].includeClusterReports).toBe(true)
 const client=mode==='client'?fakeClient([]).client:p.client
 view.rerender(wrap(mode==='connection'?'replacement':'initial',{client,...(mode==='target'?{target:{provider:'kubernetes',id:'b'}}:{})}))
 await waitFor(()=>expect(vi.mocked(client.clusterSecurity).mock.lastCall?.[0].includeClusterReports).toBe(false));expect(signal?.aborted).toBe(true);expect(screen.getByRole('checkbox')).not.toBeChecked()
 await act(async()=>answer(obsolete));expect(view.container).not.toHaveTextContent('OBSOLETE-CLUSTER-EVIDENCE')
 // Returning to the old target/incarnation must not resurrect its previous opt-in.
 view.rerender(wrap('initial'));await waitFor(()=>expect(read.mock.lastCall?.[0].includeClusterReports).toBe(false));expect(screen.getByRole('checkbox')).not.toBeChecked()
})
it('keeps global evidence explicitly labelled when the selected namespace changes',async()=>{
 const p=props(),view=render(<SecurityWorkspace {...p}/>),read=vi.mocked(p.client.clusterSecurity)
 await waitFor(()=>expect(read).toHaveBeenCalledTimes(1));fireEvent.click(screen.getByRole('checkbox'));await waitFor(()=>expect(read).toHaveBeenCalledTimes(2))
 view.rerender(<SecurityWorkspace {...p} scope={{mode:'one',name:'green'}}/>);await waitFor(()=>expect(read).toHaveBeenCalledTimes(3))
 expect(read.mock.calls[2][0]).toMatchObject({scope:{mode:'one',name:'green'},includeClusterReports:true});expect(screen.getByText(/across the entire cluster/)).toBeVisible()
})
