import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { Client } from '../api/client'
import type { ExecInfo, ExecInstance, Ref } from '../api/types'
import { useInstancePicker } from './useInstancePicker'

const ref: Ref = {provider:'kubernetes',target:'ctx',scope:'ns',kind:'apps/deployments',name:'web',uid:'deployment-uid'}
const instance = (name: string): ExecInstance => ({id:`${name}/uid-${name}`,title:name,ready:true,channels:[{id:'app',title:'app',running:true}],defaultChannel:'app'})
const info: ExecInfo = {instances:[instance('pod-1'),instance('pod-2')],defaultInstance:'pod-1/uid-pod-1',instanceLabel:{key:'kubernetes.level.pod',text:'Pod'}}
function Harness({client,context='ctx',openExec,openLogs}:{client:Client;context?:string;openExec:(instance:ExecInstance)=>void;openLogs:(ref:Ref)=>void}) {
 const picker=useInstancePicker(client,context)
 return <><button onClick={()=>picker.pickExec(ref,openExec)}>Exec</button><button onClick={()=>picker.pickLogs(ref,openLogs)}>Logs</button>{picker.popup}</>
}
function setup(exec:Client['execInfo']=vi.fn(async()=>info), log:Client['logInfo']=vi.fn(async()=>({channels:[],defaultChannel:'',aggregate:true,selectChannels:true,previous:false,instances:info.instances.map(i=>({title:i.title,ref:{...ref,kind:'pods',name:i.title,uid:i.id}}))}))) {
 const client={execInfo:exec,logInfo:log} as unknown as Client
 const openExec=vi.fn(),openLogs=vi.fn()
 const props={client,openExec,openLogs}
 return {...props,user:userEvent.setup(),...render(<Harness {...props}/>)}
}
describe('tool instance selection',()=>{
 it('opens nothing until a Pod is chosen, passes the explicit instance, and supports keyboard selection',async()=>{
  const s=setup();await s.user.click(screen.getByRole('button',{name:'Exec'}))
  const list=await screen.findByRole('listbox',{name:'Pod'})
  expect(s.openExec).not.toHaveBeenCalled()
  expect(within(list).getByRole('option',{name:'pod-2'})).toBeVisible()
  await s.user.keyboard('{ArrowDown}{Enter}')
  expect(s.openExec).toHaveBeenCalledExactlyOnceWith(info.instances[1])
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
 })
 it('offers concrete UID-bearing log sources and All Pods',async()=>{
  const s=setup();await s.user.click(screen.getByRole('button',{name:'Logs'}))
  await s.user.click(await screen.findByRole('option',{name:'pod-2'}))
  expect(s.openLogs).toHaveBeenLastCalledWith({...ref,kind:'pods',name:'pod-2',uid:info.instances[1].id})
  await s.user.click(screen.getByRole('button',{name:'Logs'}))
  await s.user.click(await screen.findByRole('option',{name:'All Pods'}))
  expect(s.openLogs).toHaveBeenLastCalledWith(ref)
 })
 it('opens Docker stdout/stderr streams directly instead of treating them as containers',async()=>{
  const s=setup(vi.fn(async()=>info),vi.fn(async()=>({channels:[{id:'stdout',title:'stdout'},{id:'stderr',title:'stderr'}],defaultChannel:'*',aggregate:false,previous:false,instances:[]})))
  await s.user.click(screen.getByRole('button',{name:'Logs'}))
  await waitFor(()=>expect(s.openLogs).toHaveBeenCalledExactlyOnceWith(ref,'*'))
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
 })
 it('opens a single instance directly',async()=>{
  const only=instance('only');const s=setup(vi.fn(async()=>({...info,instances:[only]})))
  await s.user.click(screen.getByRole('button',{name:'Exec'}))
  await waitFor(()=>expect(s.openExec).toHaveBeenCalledExactlyOnceWith(only))
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
 })
 it('Escape cancels a pending response and restores focus',async()=>{
  let resolve!:(info:ExecInfo)=>void
  const s=setup(vi.fn(()=>new Promise<ExecInfo>(r=>{resolve=r})))
  const button=screen.getByRole('button',{name:'Exec'})
  await s.user.click(button);expect(screen.queryByRole('listbox')).not.toBeInTheDocument();expect(screen.queryByText('Loading')).not.toBeInTheDocument()
  await s.user.keyboard('{Escape}');expect(button).toHaveFocus()
  await act(async()=>resolve({...info,instances:[instance('only')]}))
  expect(s.openExec).not.toHaveBeenCalled();expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
 })
 it('context changes hide the dropdown and invalidate pending responses',async()=>{
  let resolve!:(info:ExecInfo)=>void
  const s=setup(vi.fn(()=>new Promise<ExecInfo>(r=>{resolve=r})))
  await s.user.click(screen.getByRole('button',{name:'Exec'}))
  s.rerender(<Harness client={s.client} openExec={s.openExec} openLogs={s.openLogs} context="other"/>)
  await act(async()=>resolve({...info,instances:[instance('only')]}))
  expect(s.openExec).not.toHaveBeenCalled();expect(screen.queryByRole('listbox')).not.toBeInTheDocument()
 })
})
