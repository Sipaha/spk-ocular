import {act,cleanup,fireEvent,render,screen,waitFor} from '@testing-library/react'
import {afterEach,beforeEach,expect,it,vi} from 'vitest'
import type {ComponentProps} from 'react'
import GraphWorkspace from './GraphWorkspace'
import {fakeClient} from '../test/fakeClient'
import {holdEdits,resetGuard} from '../edit/guard'
import {setLanguage} from '../i18n'

vi.mock('./canvas',()=>({GraphCanvas:class{
 destroy(){} setClusterLabel(){} setGraph(){} setOptions(){} zoom(){} fit(){}
}}))
const worker=vi.fn()
beforeEach(()=>{
 setLanguage('en')
 vi.stubGlobal('Worker',class{
  constructor(){worker()}
  terminate(){} postMessage(){}
 })
})
afterEach(()=>{cleanup();resetGuard();vi.unstubAllGlobals();worker.mockClear()})
function props(){
 const {client}=fakeClient([])
 vi.mocked(client.clusterGraph).mockResolvedValue({nodes:[],edges:[],problems:[],truncated:false,discovery:'ready',capturedAt:0})
 return {client,target:{provider:'synthetic',id:'demo'},session:1,connection:1,scope:{mode:'all'},scopePicker:null,drawer:{} as ComponentProps<typeof GraphWorkspace>['drawer']} satisfies ComponentProps<typeof GraphWorkspace>
}
it('requires fresh admission when explicit connection changes even if an agent retains the backend session',async()=>{
 const p=props(),view=render(<GraphWorkspace {...p}/>)
 expect(p.client.clusterGraph).not.toHaveBeenCalled();expect(worker).not.toHaveBeenCalled()
 fireEvent.click(screen.getByRole('button',{name:'Load graph'}))
 await waitFor(()=>expect(p.client.clusterGraph).toHaveBeenCalledTimes(1))
 view.rerender(<GraphWorkspace {...p} connection={2}/>)
 expect(screen.getByRole('button',{name:'Load graph'})).toBeVisible()
 expect(view.container.querySelector('canvas')).toBeNull();expect(p.client.clusterGraph).toHaveBeenCalledTimes(1)
 fireEvent.click(screen.getByRole('button',{name:'Load graph'}))
 await waitFor(()=>expect(p.client.clusterGraph).toHaveBeenCalledTimes(2))
})
it('keeps the mounted graph while unsaved edits are held, then gates its replacement session',async()=>{
 const p=props(),view=render(<GraphWorkspace {...p}/>)
 fireEvent.click(screen.getByRole('button',{name:'Load graph'}))
 await waitFor(()=>expect(p.client.clusterGraph).toHaveBeenCalledTimes(1))
 const canvas=view.container.querySelector('canvas')!
 let release=()=>{}
 act(()=>{release=holdEdits({dirty:()=>true,discard:vi.fn()})})
 view.rerender(<GraphWorkspace {...p} session={2} connection={2}/>)
 expect(view.container.querySelector('canvas')).toBe(canvas)
 expect(screen.queryByRole('button',{name:'Load graph'})).not.toBeInTheDocument()
 expect(p.client.clusterGraph).toHaveBeenCalledTimes(1)
 act(()=>release())
 expect(screen.getByRole('button',{name:'Load graph'})).toBeVisible()
 expect(view.container.querySelector('canvas')).toBeNull()
 expect(p.client.clusterGraph).toHaveBeenCalledTimes(1)
})
