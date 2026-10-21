import {act,cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react'
import {afterEach,beforeEach,it,expect,vi} from 'vitest'
import {ResourceNetworkPath} from './ResourceNetworkPath'
import {snapshot,subject} from './fixtures'
import type {NetworkPath} from './types'
import {fakeClient} from '../test/fakeClient'
import {ViewConnectionContext} from '../views/useView'
import {setLanguage} from '../i18n'
beforeEach(()=>setLanguage('en'));afterEach(cleanup)
const props=()=>({client:fakeClient([]).client,subject,onGo:vi.fn()})
it('loads only explicitly, keeps paths independent and permits only exact local UID links',async()=>{
 const p=props();vi.mocked(p.client.resourceNetworkPath).mockResolvedValue(snapshot)
 render(<ResourceNetworkPath {...p}/>);expect(p.client.resourceNetworkPath).not.toHaveBeenCalled()
 fireEvent.click(screen.getByRole('button',{name:'Load declared path'}));await screen.findByText('Endpoint is marked not ready.')
 expect(screen.queryByText('Evidence is incomplete or limited.')).not.toBeInTheDocument()
 expect(screen.getByText('web.example/shop · Prefix')).toBeInTheDocument();expect(screen.getByText('web.example/missing · Exact')).toBeInTheDocument();expect(screen.getByText('Referenced Service was not found.')).toBeInTheDocument()
 expect(screen.queryByRole('button',{name:'services/missing'})).not.toBeInTheDocument()
 fireEvent.click(screen.getByRole('button',{name:'pods/web-1'}));expect(p.onGo).toHaveBeenCalledWith(snapshot.routes[0].endpoints[0].pod)
 expect(screen.getByText('ready=false · serving=true · terminating=true')).toBeInTheDocument()
})
it('unknown readiness and partial/denied reads are explicit and foreign refs remain inert',async()=>{
 const p=props();const data=structuredClone(snapshot);data.coverage=[{source:'pods',state:'forbidden'}];data.routes[0].endpoints[0].ready=null;data.routes[0].endpoints[0].pod!.target='foreign';data.routes[0].endpoints[0].checks=[{code:'readiness_unknown',state:'unknown',field:'endpoints[].conditions.ready'}]
 vi.mocked(p.client.resourceNetworkPath).mockResolvedValue(data);render(<ResourceNetworkPath {...p}/>);fireEvent.click(screen.getByRole('button',{name:'Load declared path'}));await screen.findByText('Readiness is unspecified; this is not evidence of failure.')
 expect(screen.getByText('Evidence is incomplete or limited.')).toBeInTheDocument();expect(screen.queryByRole('button',{name:'pods/web-1'})).not.toBeInTheDocument();expect(screen.getByText('ready=? · serving=true · terminating=true')).toBeInTheDocument()
})
it('cancels and fences late replies and requires new admission for replacement connections and UIDs',async()=>{
 const p=props();let answer:(v:NetworkPath)=>void=()=>{};let signal:AbortSignal|undefined
 vi.mocked(p.client.resourceNetworkPath).mockImplementation((_r,s)=>{signal=s;return new Promise(r=>{answer=r})})
 const view=render(<ViewConnectionContext value="one"><ResourceNetworkPath {...p}/></ViewConnectionContext>)
 fireEvent.click(screen.getByRole('button',{name:'Load declared path'}));await waitFor(()=>expect(p.client.resourceNetworkPath).toHaveBeenCalledTimes(1));fireEvent.click(screen.getByRole('button',{name:'Cancel'}));expect(signal?.aborted).toBe(true);await act(async()=>answer(snapshot));expect(screen.queryByText('Endpoint is marked not ready.')).not.toBeInTheDocument()
 fireEvent.click(screen.getByRole('button',{name:'Load declared path'}));await waitFor(()=>expect(p.client.resourceNetworkPath).toHaveBeenCalledTimes(2));view.rerender(<ViewConnectionContext value="two"><ResourceNetworkPath {...p}/></ViewConnectionContext>);expect(signal?.aborted).toBe(true);await act(async()=>answer(snapshot));expect(screen.queryByText('Endpoint is marked not ready.')).not.toBeInTheDocument();expect(p.client.resourceNetworkPath).toHaveBeenCalledTimes(2)
 view.rerender(<ViewConnectionContext value="two"><ResourceNetworkPath {...p} subject={{...subject,uid:'replacement'}}/></ViewConnectionContext>);expect(p.client.resourceNetworkPath).toHaveBeenCalledTimes(2)
})
it('rejects an identity-mismatched snapshot; a failed refresh retains dated prior observations',async()=>{
 const p=props();vi.mocked(p.client.resourceNetworkPath).mockResolvedValue({...snapshot,ref:{...subject,uid:'wrong'}})
 render(<ResourceNetworkPath {...p}/>);fireEvent.click(screen.getByRole('button',{name:'Load declared path'}));await screen.findByRole('alert');expect(screen.queryByText('Endpoint is marked not ready.')).not.toBeInTheDocument()
 vi.mocked(p.client.resourceNetworkPath).mockResolvedValue(snapshot);fireEvent.click(screen.getByRole('button',{name:'Load declared path'}));await screen.findByText('Endpoint is marked not ready.')
 vi.mocked(p.client.resourceNetworkPath).mockRejectedValue(new Error('Fixture denied'));fireEvent.click(screen.getByRole('button',{name:'Load declared path'}));await screen.findByRole('alert');expect(screen.getByText('Endpoint is marked not ready.')).toBeInTheDocument()
})
