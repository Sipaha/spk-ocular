import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach,beforeEach,it,expect,vi } from 'vitest'
import type { ComponentProps } from 'react'
import ArgoWorkspace from './ArgoWorkspace'
import {evidenceKey,readEvidence} from './evidence'
import { ViewHub } from '../views/viewSync'
import { fakeClient } from '../test/fakeClient'
import { setLanguage } from '../i18n'
import { ViewConnectionContext } from '../views/useView'
import { holdEdits,resetGuard } from '../edit/guard'
beforeEach(()=>setLanguage('en'))
afterEach(()=>{cleanup();resetGuard()})
function props(){
 const {client}=fakeClient([])
 return {client,target:{provider:'synthetic',id:'demo'},scope:{mode:'all'},workspace:'argocd-applications',scopePicker:null,kinds:[],drawer:{client,target:{provider:'synthetic',id:'demo'},hub:new ViewHub(client)}} satisfies ComponentProps<typeof ArgoWorkspace>
}
it('fences old snapshot responses when the connection incarnation changes',async()=>{
 const p=props();let resolve:(value:Awaited<ReturnType<typeof p.client.argoCD>>)=>void=()=>{}
 vi.mocked(p.client.argoCD).mockImplementationOnce(()=>new Promise(r=>{resolve=r}))
 const view=render(<ViewConnectionContext value="1:1"><ArgoWorkspace {...p}/></ViewConnectionContext>)
 await waitFor(()=>expect(p.client.argoCD).toHaveBeenCalledTimes(1))
 view.rerender(<ViewConnectionContext value="2:2"><ArgoWorkspace {...p}/></ViewConnectionContext>)
 await waitFor(()=>expect(p.client.argoCD).toHaveBeenCalledTimes(2))
 await act(async()=>resolve({objects:[],servers:[],events:[],coverage:[{kind:'Application',scope:'old-namespace',state:'forbidden'}],discovery:'ready',capturedAt:0,truncated:true}))
 expect(screen.queryByText(/old-namespace/)).not.toBeInTheDocument()
 expect(screen.queryByText('Incomplete coverage')).not.toBeInTheDocument()
})
it('retains the mounted workspace until a held editor releases automatic replacement',async()=>{
 const p=props(),view=render(<ViewConnectionContext value="1:1"><ArgoWorkspace {...p}/></ViewConnectionContext>)
 await waitFor(()=>expect(p.client.argoCD).toHaveBeenCalledTimes(1))
 fireEvent.change(screen.getByRole('textbox',{name:'Search applications, projects and sources…'}),{target:{value:'retained filter'}})
 let release=()=>{}
 act(()=>{release=holdEdits({dirty:()=>true,discard:vi.fn()})})
 view.rerender(<ViewConnectionContext value="2:2"><ArgoWorkspace {...p}/></ViewConnectionContext>)
 expect(screen.getByRole('textbox',{name:'Search applications, projects and sources…'})).toHaveValue('retained filter')
 expect(p.client.argoCD).toHaveBeenCalledTimes(1)
 expect(readEvidence(p.client,evidenceKey('synthetic','demo','2:2',p.scope))).toBeNull()
 act(()=>release())
 await waitFor(()=>expect(p.client.argoCD).toHaveBeenCalledTimes(2))
 expect(screen.getByRole('textbox',{name:'Search applications, projects and sources…'})).toHaveValue('')
})
