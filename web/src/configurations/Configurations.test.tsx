import { act, fireEvent, render, screen, waitFor, cleanup } from '@testing-library/react'
import { describe, expect, it, vi, afterEach } from 'vitest'
import { fakeClient } from '../test/fakeClient'
import { createRef } from 'react'
import { Configurations, type ConfigurationActions } from './Configurations'
import { holdEdits, resetGuard } from '../edit/guard'
import { setLanguage } from '../i18n'

afterEach(()=>{cleanup();resetGuard()})
describe('configuration setup',()=>{
 it('starts with unchecked candidates and imports only the user selection',async()=>{
  setLanguage('en');const {client}=fakeClient([])
  vi.mocked(client.configurations).mockResolvedValueOnce({initialized:false,encrypted:false,locked:false,candidates:[{path:'/fixture/a',contexts:['a'],selected:false},{path:'/fixture/b',contexts:['b'],selected:false}],entries:[]})
  render(<Configurations client={client} onChanged={()=>{}}/>)
  const boxes=await screen.findAllByRole('checkbox');expect(boxes[0]).not.toBeChecked();expect(boxes[1]).not.toBeChecked()
  fireEvent.click(boxes[1]);fireEvent.click(screen.getByRole('button',{name:'Add selected'}))
  await waitFor(()=>expect(client.configurations).toHaveBeenCalledWith({command:'import',paths:['/fixture/b']}))
 })
 it('does not show the YAML editor before master password setup succeeds',async()=>{
  setLanguage('en');const {client}=fakeClient([])
  render(<Configurations client={client} onChanged={vi.fn()}/>);await waitFor(()=>expect(client.configurations).toHaveBeenCalled())
  fireEvent.click(screen.getByRole('button',{name:'Add kubeconfig'}));fireEvent.click(await screen.findByRole('menuitem',{name:'New configuration'}))
  expect(screen.queryByRole('textbox',{name:'Kubeconfig YAML'})).not.toBeInTheDocument()
  const save=await screen.findByRole('button',{name:'Create master password'});expect(save).toBeDisabled()
  fireEvent.change(screen.getByLabelText('Master password',{exact:true}),{target:{value:'x'}})
  fireEvent.change(screen.getByLabelText('Repeat master password',{exact:true}),{target:{value:'wrong'}});expect(save).toBeDisabled()
  fireEvent.change(screen.getByLabelText('Repeat master password',{exact:true}),{target:{value:'x'}});fireEvent.click(save)
  expect(await screen.findByRole('textbox',{name:'Kubeconfig YAML'})).toBeVisible()
 })
})

it('requires explicit removal review and refuses to discard resource edits',async()=>{
 setLanguage('en');const {client}=fakeClient([])
 vi.mocked(client.configurations).mockResolvedValue({initialized:true,encrypted:true,locked:true,candidates:[],entryId:'stored-id',entries:[{id:'stored-id',name:'Production',kind:'stored',revision:'reviewed-revision'}]})
 const ref=createRef<ConfigurationActions>()
 render(<Configurations ref={ref} client={client} onChanged={()=>{}}/>)
 await act(async()=>ref.current?.openTarget({provider:'kubernetes',id:'stored:stored-id:prod',title:'Production'},'remove'))
 await waitFor(()=>expect(screen.getByRole('button',{name:'Cancel'})).toHaveFocus())
 expect(client.configurations).not.toHaveBeenCalledWith(expect.objectContaining({command:'remove'}))
 const release=holdEdits({dirty:()=>true,discard:vi.fn()})
 fireEvent.click(screen.getByRole('button',{name:'Remove configuration'}))
 expect(screen.getByRole('alert')).toHaveTextContent('Save or discard open resource edits')
 expect(client.configurations).not.toHaveBeenCalledWith(expect.objectContaining({command:'remove'}))
 release();fireEvent.click(screen.getByRole('button',{name:'Remove configuration'}))
 await waitFor(()=>expect(client.configurations).toHaveBeenCalledWith({command:'remove',id:'stored-id',expect:'reviewed-revision'}))
})


it('unlocks only on Connect, resumes once, and Cancel never starts a connection',async()=>{
 setLanguage('en');const {client}=fakeClient([])
 const locked={initialized:true,encrypted:true,locked:true,candidates:[],entries:[]}
 vi.mocked(client.configurations).mockResolvedValue(locked)
 const ref=createRef<ConfigurationActions>(),resume=vi.fn(async()=>{}),changed=vi.fn(async()=>{})
 render(<Configurations ref={ref} client={client} onChanged={changed}/>);
 await waitFor(()=>expect(client.configurations).toHaveBeenCalled())
 expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
 expect(screen.queryByRole('button',{name:/Stored configurations are locked/})).not.toBeInTheDocument()
 const target={provider:'kubernetes',id:'stored:id:prod',title:'Production',locked:true}
 act(()=>ref.current?.unlockTarget(target,resume))
 fireEvent.click(screen.getByRole('button',{name:'Cancel'}))
 expect(resume).not.toHaveBeenCalled()
 act(()=>ref.current?.unlockTarget(target,resume))
 vi.mocked(client.configurations).mockRejectedValueOnce(new Error('incorrect master password'))
 fireEvent.change(screen.getByLabelText('Master password',{exact:true}),{target:{value:'wrong'}})
 fireEvent.click(screen.getByRole('button',{name:'Unlock'}))
 expect(await screen.findByRole('alert')).toHaveTextContent('incorrect master password')
 expect(resume).not.toHaveBeenCalled()
 vi.mocked(client.configurations).mockResolvedValue({...locked,locked:false})
 fireEvent.change(screen.getByLabelText('Master password',{exact:true}),{target:{value:'correct'}})
 fireEvent.click(screen.getByRole('button',{name:'Unlock'}))
 await waitFor(()=>expect(resume).toHaveBeenCalledTimes(1))
 expect(changed).toHaveBeenCalledTimes(1)
 expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
})


it('reviews reset with deletion warning and supports cancellation',async()=>{
 setLanguage('en');const {client}=fakeClient([])
 const state={initialized:true,encrypted:true,locked:true,resetRevision:'reviewed',candidates:[],entries:[{id:'private',name:'Private cluster',kind:'stored' as const,revision:'r'}]}
 vi.mocked(client.configurations).mockResolvedValue(state)
 render(<Configurations client={client} onChanged={()=>{}}/>);
 await waitFor(()=>expect(client.configurations).toHaveBeenCalled())
 fireEvent.click(screen.getByRole('button',{name:'Add kubeconfig'}))
 fireEvent.click(await screen.findByRole('menuitem',{name:'Reset master password'}))
 const confirm=await screen.findByRole('button',{name:'Delete encrypted configurations and reset'})
 expect(screen.getByRole('dialog')).toHaveTextContent('permanently deleted')
 expect(screen.getByRole('dialog')).toHaveTextContent('Private cluster')
 expect(screen.getByRole('button',{name:'Cancel'})).toHaveFocus()
 fireEvent.click(screen.getByRole('button',{name:'Cancel'}))
 expect(client.configurations).not.toHaveBeenCalledWith(expect.objectContaining({command:'reset'}))
 fireEvent.click(screen.getByRole('button',{name:'Add kubeconfig'}))
 fireEvent.click(await screen.findByRole('menuitem',{name:'Reset master password'}))
 await screen.findByRole('button',{name:confirm.textContent!})
 fireEvent.click(screen.getByRole('button',{name:'Delete encrypted configurations and reset'}))
 await waitFor(()=>expect(client.configurations).toHaveBeenCalledWith({command:'reset',expect:'reviewed'}))
})
