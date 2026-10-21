import {act,renderHook,waitFor} from '@testing-library/react'
import {it,expect,vi} from 'vitest'
import {fakeClient} from '../test/fakeClient'
import type {ViewPresetRegistry} from '../api/types'
import {usePresets} from './usePresets'
const registry=(active=''):ViewPresetRegistry=>({version:1,revision:0,active,presets:active?[{name:active,labels:['team/name'],hidden:[],order:[],filter:''}]:[]})
const deferred=<T,>()=>{let resolve!:(v:T)=>void;const promise=new Promise<T>(r=>{resolve=r});return {promise,resolve}}
it('late read cannot replace another kind and rejected save preserves the active definition until reload',async()=>{
 const client=fakeClient([]).client,first=deferred<ViewPresetRegistry>()
 vi.mocked(client.viewPresets).mockImplementation(req=>req.kind==='pods'?first.promise:Promise.resolve(registry('Services')))
 const {result,rerender}=renderHook(({kind})=>usePresets(client,'kubernetes',kind,true),{initialProps:{kind:'pods'}})
 rerender({kind:'services'});await waitFor(()=>expect(result.current.registry.active).toBe('Services'))
 await act(async()=>first.resolve(registry('Stale pods')));expect(result.current.registry.active).toBe('Services')
 vi.mocked(client.viewPresets).mockRejectedValueOnce(new Error('conflict'))
 await act(async()=>{expect(await result.current.save(registry('Changed'))).toBe(false)})
 expect(result.current.error).toBe('save');expect(result.current.registry.active).toBe('Services')
 act(()=>result.current.reload());await waitFor(()=>expect(result.current.ready).toBe(true));expect(result.current.error).toBe('')
})
it('rapid saves are serialized and a late save cannot relabel another kind',async()=>{
 const client=fakeClient([]).client,pending=deferred<ViewPresetRegistry>()
 vi.mocked(client.viewPresets).mockImplementation(req=>req.registry?pending.promise:Promise.resolve(registry()))
 const {result,rerender}=renderHook(({kind})=>usePresets(client,'kubernetes',kind,true),{initialProps:{kind:'pods'}})
 await waitFor(()=>expect(result.current.ready).toBe(true))
 let saving!:Promise<boolean>
 act(()=>{saving=result.current.save(registry('Team'))})
 await act(async()=>{expect(await result.current.save(registry('Other'))).toBe(false)})
 rerender({kind:'services'});await waitFor(()=>expect(result.current.ready).toBe(true))
 await act(async()=>{pending.resolve(registry('Team'));await saving});expect(result.current.registry.active).toBe('')
})
