import {act,cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react'
import {afterEach,beforeEach,it,expect,vi} from 'vitest'
import {ResourceDiagnosis} from './ResourceDiagnosis'
import {fakeClient} from '../test/fakeClient'
import {ViewConnectionContext} from '../views/useView'
import {setLanguage} from '../i18n'
import type {Diagnosis} from './types'
const subject={provider:'kubernetes',target:'a',kind:'pods',scope:'blue',name:'p',uid:'uid'}
const snapshot:Diagnosis={ref:subject,capturedAt:1,truncated:true,coverage:[{source:'secrets/key',state:'keys_unchecked'},{source:'configmaps/denied',state:'forbidden'}],evidence:[{ref:subject,source:subject,rule:'container',field:'status.containerStatuses[0].lastState.terminated',state:'unknown',reason:'Error',message:'exitCode=137',container:'app',previous:true},{ref:subject,source:{...subject,kind:'events',name:'probe',uid:'event'},rule:'event',field:'message',state:'unknown',reason:'Unhealthy',message:'Probe failed'}]}
beforeEach(()=>setLanguage('en'));afterEach(cleanup)
function props(){return {client:fakeClient([]).client,subject,onGo:vi.fn(),onLogs:vi.fn()}}
it('reads only on demand, preserves independent evidence and admits exact previous-container logs',async()=>{
 const p=props();vi.mocked(p.client.diagnoseResource).mockResolvedValue({...snapshot,truncated:false})
 vi.mocked(p.client.logInfo).mockResolvedValue({channels:[{id:'app',title:'app'}],defaultChannel:'app',aggregate:false,previous:true})
 render(<ResourceDiagnosis {...p}/>);expect(p.client.diagnoseResource).not.toHaveBeenCalled()
 fireEvent.click(screen.getByRole('button',{name:'Collect evidence'}));await screen.findByText('exitCode=137')
 expect(screen.getByText('Evidence is incomplete or limited.')).toBeInTheDocument();expect(screen.queryByText('OOMKilled')).not.toBeInTheDocument();expect(screen.getByText('Probe failed')).toBeInTheDocument()
 fireEvent.click(screen.getByText('Read coverage'));expect(screen.getByText('Object keys were not checked')).toBeInTheDocument()
 fireEvent.click(screen.getByRole('button',{name:'events/probe'}));expect(p.onGo).toHaveBeenCalledWith(snapshot.evidence[1].source)
 fireEvent.click(screen.getByRole('button',{name:'Previous logs'}));await waitFor(()=>expect(p.onLogs).toHaveBeenCalledWith(subject,'app',true))
 expect(p.client.logInfo).toHaveBeenCalledWith(subject)
})
it('cancels and fences late replies; changing connection or UID requires a fresh explicit request',async()=>{
 const p=props();let answer:(v:Diagnosis)=>void=()=>{};let signal:AbortSignal|undefined
 vi.mocked(p.client.diagnoseResource).mockImplementation((_ref,s)=>{signal=s;return new Promise(r=>{answer=r})})
 const view=render(<ViewConnectionContext value="one"><ResourceDiagnosis {...p}/></ViewConnectionContext>)
 fireEvent.click(screen.getByRole('button',{name:'Collect evidence'}));await waitFor(()=>expect(p.client.diagnoseResource).toHaveBeenCalledTimes(1))
 fireEvent.click(screen.getByRole('button',{name:'Cancel'}));expect(signal?.aborted).toBe(true)
 await act(async()=>answer(snapshot));expect(screen.queryByText('exitCode=137')).not.toBeInTheDocument()
 fireEvent.click(screen.getByRole('button',{name:'Collect evidence'}));await waitFor(()=>expect(p.client.diagnoseResource).toHaveBeenCalledTimes(2))
 view.rerender(<ViewConnectionContext value="two"><ResourceDiagnosis {...p}/></ViewConnectionContext>);expect(signal?.aborted).toBe(true)
 await act(async()=>answer(snapshot));expect(screen.queryByText('exitCode=137')).not.toBeInTheDocument();expect(p.client.diagnoseResource).toHaveBeenCalledTimes(2)
 view.rerender(<ViewConnectionContext value="two"><ResourceDiagnosis {...p} subject={{...subject,uid:'replacement'}}/></ViewConnectionContext>);expect(p.client.diagnoseResource).toHaveBeenCalledTimes(2)
})
it('rejects mismatched UID snapshots and previous logs without admission; late metadata cannot navigate after unmount',async()=>{
 const p=props();vi.mocked(p.client.diagnoseResource).mockResolvedValue({...snapshot,ref:{...subject,uid:'wrong'}})
 const view=render(<ResourceDiagnosis {...p}/>);fireEvent.click(screen.getByRole('button',{name:'Collect evidence'}));await screen.findByRole('alert');expect(screen.queryByText('exitCode=137')).not.toBeInTheDocument()
 vi.mocked(p.client.diagnoseResource).mockResolvedValue(snapshot);fireEvent.click(screen.getByRole('button',{name:'Collect evidence'}));await screen.findByText('exitCode=137')
 vi.mocked(p.client.logInfo).mockResolvedValue({channels:[{id:'app',title:'app'}],defaultChannel:'app',aggregate:false,previous:false})
 fireEvent.click(screen.getByRole('button',{name:'Previous logs'}));await screen.findByText('The requested container logs are unavailable.');expect(p.onLogs).not.toHaveBeenCalled()
 let resolve:(v:Awaited<ReturnType<typeof p.client.logInfo>>)=>void=()=>{}
 vi.mocked(p.client.logInfo).mockImplementation(()=>new Promise(r=>{resolve=r}))
 fireEvent.click(screen.getByRole('button',{name:'Previous logs'}));view.unmount()
 await act(async()=>resolve({channels:[{id:'app',title:'app'}],defaultChannel:'app',aggregate:false,previous:true}));expect(p.onLogs).not.toHaveBeenCalled()
})
