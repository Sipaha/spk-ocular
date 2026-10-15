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
  fireEvent.click(await screen.findByRole('button',{name:'New configuration'}))
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


it('configuration dialogs own keyboard events instead of the ancestor target list',async()=>{
 setLanguage('en');const {client}=fakeClient([]),parentKeys=vi.fn()
 vi.mocked(client.configurations).mockResolvedValue({initialized:true,encrypted:true,locked:true,resetRevision:'reviewed',candidates:[],entries:[{id:'retained',name:'Retained',kind:'stored',revision:'r'}]})
 render(<div onKeyDown={parentKeys}><Configurations client={client} onChanged={()=>{}}/></div>)
 await waitFor(()=>expect(client.configurations).toHaveBeenCalled())
 fireEvent.click(await screen.findByRole('button',{name:'New configuration'}))
 fireEvent.click(await screen.findByRole('button',{name:'Reset master password'}))
 const cancel=await screen.findByRole('button',{name:'Cancel'})
 fireEvent.keyDown(cancel,{key:'Enter'})
 fireEvent.keyDown(cancel,{key:'ArrowDown'})
 expect(parentKeys).not.toHaveBeenCalled()
 expect(client.configurations).not.toHaveBeenCalledWith(expect.objectContaining({command:'reset'}))
 fireEvent.click(cancel)
})

it('reviews reset with deletion warning and supports cancellation',async()=>{
 setLanguage('en');const {client}=fakeClient([])
 const state={initialized:true,encrypted:true,locked:true,resetRevision:'reviewed',candidates:[],entries:[{id:'private',name:'Private cluster',kind:'stored' as const,revision:'r'}]}
 vi.mocked(client.configurations).mockResolvedValue(state)
 render(<Configurations client={client} onChanged={()=>{}}/>);
 await waitFor(()=>expect(client.configurations).toHaveBeenCalled())
 fireEvent.click(await screen.findByRole('button',{name:'New configuration'}))
 fireEvent.click(await screen.findByRole('button',{name:'Reset master password'}))
 const confirm=await screen.findByRole('button',{name:'Delete encrypted configurations and reset'})
 expect(screen.getByRole('dialog')).toHaveTextContent('permanently deleted')
 expect(screen.getByRole('dialog')).toHaveTextContent('Private cluster')
 expect(screen.getByRole('button',{name:'Cancel'})).toHaveFocus()
 fireEvent.click(screen.getByRole('button',{name:'Cancel'}))
 expect(client.configurations).not.toHaveBeenCalledWith(expect.objectContaining({command:'reset'}))
 fireEvent.click(await screen.findByRole('button',{name:'New configuration'}))
 fireEvent.click(await screen.findByRole('button',{name:'Reset master password'}))
 await screen.findByRole('button',{name:confirm.textContent!})
 fireEvent.click(screen.getByRole('button',{name:'Delete encrypted configurations and reset'}))
 await waitFor(()=>expect(client.configurations).toHaveBeenCalledWith({command:'reset',expect:'reviewed'}))
})

it.each([false,true])('New configuration unlocks directly without connecting (stored entries: %s)',async(hasStored)=>{
 setLanguage('en');const {client}=fakeClient([])
 const state={initialized:true,encrypted:true,locked:true,candidates:[],entries:hasStored?[{id:'retained',name:'Retained',kind:'stored' as const,revision:'r'}]:[]}
 vi.mocked(client.configurations).mockResolvedValue(state)
 render(<Configurations client={client} onChanged={()=>{}}/>)
 await waitFor(()=>expect(client.configurations).toHaveBeenCalled())
 fireEvent.click(await screen.findByRole('button',{name:'New configuration'}))
 expect(await screen.findByLabelText('Master password',{exact:true})).toHaveFocus()
 expect(screen.queryByText('Press Connect on a protected connection to unlock configurations.')).not.toBeInTheDocument()
 expect(client.connectTarget).not.toHaveBeenCalled()
 fireEvent.click(screen.getByRole('button',{name:'Cancel'}))
 expect(client.configurations).not.toHaveBeenCalledWith(expect.objectContaining({command:'unlock'}))
 fireEvent.click(await screen.findByRole('button',{name:'New configuration'}))
 await screen.findByLabelText('Master password',{exact:true})
 vi.mocked(client.configurations).mockRejectedValueOnce(new Error('incorrect master password'))
 fireEvent.change(screen.getByLabelText('Master password',{exact:true}),{target:{value:'wrong'}})
 fireEvent.click(screen.getByRole('button',{name:'Unlock'}))
 expect(await screen.findByRole('alert')).toHaveTextContent('incorrect master password')
 expect(screen.queryByRole('textbox',{name:'Kubeconfig YAML'})).not.toBeInTheDocument()
 vi.mocked(client.configurations).mockResolvedValue({...state,locked:false})
 fireEvent.change(screen.getByLabelText('Master password',{exact:true}),{target:{value:'correct'}})
 fireEvent.click(screen.getByRole('button',{name:'Unlock'}))
 expect(await screen.findByRole('textbox',{name:'Kubeconfig YAML'})).toBeVisible()
 expect(client.connectTarget).not.toHaveBeenCalled()
})


it('resets an empty encrypted vault without a deletion review using the fresh revision',async()=>{
 setLanguage('en');const {client}=fakeClient([])
 const state={initialized:true,encrypted:true,locked:true,resetRevision:'fresh-empty',candidates:[],entries:[{id:'linked',name:'External',kind:'linked' as const,revision:'r'}]}
 vi.mocked(client.configurations).mockImplementation(async req=>req.command==='reset'?{...state,encrypted:false,locked:false}:state)
 render(<Configurations client={client} onChanged={()=>{}}/>)
 fireEvent.click(await screen.findByRole('button',{name:'New configuration'}));fireEvent.click(await screen.findByRole('button',{name:'Reset master password'}))
 await waitFor(()=>expect(client.configurations).toHaveBeenCalledWith({command:'reset',expect:'fresh-empty'}))
 await waitFor(()=>expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
 expect(screen.queryByText(/permanently deleted/)).not.toBeInTheDocument()
})
it('does not retry an empty reset after its revision becomes stale',async()=>{
 setLanguage('en');const {client}=fakeClient([]),changed=vi.fn()
 vi.mocked(client.configurations).mockImplementation(async req=>{if(req.command==='reset')throw new Error('configuration storage changed; review reset again');return{initialized:true,encrypted:true,locked:true,resetRevision:'empty-before-add',candidates:[],entries:[]}})
 render(<Configurations client={client} onChanged={changed}/>)
 fireEvent.click(await screen.findByRole('button',{name:'New configuration'}));fireEvent.click(await screen.findByRole('button',{name:'Reset master password'}))
 expect(await screen.findByRole('alert')).toHaveTextContent('configuration storage changed')
 expect(vi.mocked(client.configurations).mock.calls.filter(([request])=>request.command==='reset')).toEqual([[{command:'reset',expect:'empty-before-add'}]])
 expect(screen.queryByRole('button',{name:'Delete encrypted configurations and reset'})).not.toBeInTheDocument()
})
it('blocks empty reset while resource edits are held',async()=>{
 setLanguage('en');const {client}=fakeClient([])
 vi.mocked(client.configurations).mockResolvedValue({initialized:true,encrypted:true,locked:true,resetRevision:'empty',candidates:[],entries:[]})
 const release=holdEdits({dirty:()=>true,discard:()=>{},focus:()=>{}})
 render(<Configurations client={client} onChanged={()=>{}}/>)
 fireEvent.click(await screen.findByRole('button',{name:'New configuration'}));fireEvent.click(await screen.findByRole('button',{name:'Reset master password'}))
 expect(await screen.findByRole('alert')).toHaveTextContent('Save or discard')
 expect(client.configurations).not.toHaveBeenCalledWith(expect.objectContaining({command:'reset'}));release()
})
