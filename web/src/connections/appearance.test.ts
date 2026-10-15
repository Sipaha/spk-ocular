import { describe, expect, it, vi } from 'vitest'
import type { Target } from '../api/types'
import { fakeClient } from '../test/fakeClient'
import { appearanceModel, iconPaths, iconIds, palette, parseAppearance } from './appearance'
const target:Target={provider:'kubernetes',id:'linked:stable',title:'Original name'}

describe('connection appearance persistence',()=>{
 it('assigns once, serializes initial and user writes, and ignores renaming',async()=>{
  const {client}=fakeClient([]),model=appearanceModel(client)
  await Promise.all([model.ensure(target),model.ensure(target)])
  expect(client.getTargetState).toHaveBeenCalledTimes(1)
  expect(client.setTargetState).toHaveBeenCalledTimes(1)
  await model.ensure({...target,title:'New name'})
  expect(client.setTargetState).toHaveBeenCalledTimes(1)
  await model.save(target,{icon:'cat',palette:'teal'})
  expect(client.setTargetState).toHaveBeenLastCalledWith('kubernetes',target.id,'appearance',JSON.stringify({icon:'cat',palette:'teal'}))
  const second=fakeClient([]).client
  vi.mocked(second.getTargetState).mockResolvedValue({appearance:JSON.stringify({icon:'cat',palette:'teal'})})
  const restored=appearanceModel(second);await restored.ensure({...target,title:'New name'})
  expect(Object.values(restored.store.getState().entries)).toEqual([{icon:'cat',palette:'teal'}])
  expect(second.setTargetState).not.toHaveBeenCalled()
 })
 it('does not replace a saved appearance while initialization is pending',async()=>{
  const {client}=fakeClient([]);let resolve!:(value:Record<string,string>)=>void
  vi.mocked(client.getTargetState).mockImplementation(()=>new Promise(r=>{resolve=r}))
  const model=appearanceModel(client),initial=model.ensure(target),save=model.save(target,{icon:'bird',palette:'rose'})
  expect(client.setTargetState).not.toHaveBeenCalled()
  resolve({appearance:JSON.stringify({icon:'cube',palette:'blue'})})
  await Promise.all([initial,save]);expect(Object.values(model.store.getState().entries)).toEqual([{icon:'bird',palette:'rose'}])
  expect(client.setTargetState).toHaveBeenCalledTimes(1)
 })
 it('retains a failed random assignment for explicit retry without pretending it persisted',async()=>{
  const {client}=fakeClient([]);vi.mocked(client.setTargetState).mockRejectedValueOnce(new Error('disk full'))
  const model=appearanceModel(client);await model.ensure(target)
  expect(Object.values(model.store.getState().errors)).toEqual([true])
  const first=vi.mocked(client.setTargetState).mock.calls[0][3]
  await model.ensure(target,true)
  expect(vi.mocked(client.setTargetState).mock.calls[1][3]).toBe(first)
  expect(Object.values(model.store.getState().errors)).toEqual([false])
 })
 it('reserves different initial combinations and validates stored IDs',async()=>{
  const {client}=fakeClient([]),model=appearanceModel(client)
  await Promise.all(Array.from({length:40},(_,i)=>model.ensure({...target,id:String(i)})))
  const values=Object.values(model.store.getState().entries).map(x=>JSON.stringify(x))
  expect(new Set(values).size).toBe(40)
  expect(parseAppearance('{"icon":"__proto__","palette":"blue"}')).toBeNull()
  expect(parseAppearance('{"icon":"cat","palette":"unknown"}')).toBeNull()
  expect(parseAppearance('broken')).toBeNull()
 })
})

it('provides32 distinct vector icons and readable foreground/background pairs in both tones',()=>{
 expect(iconIds).toHaveLength(32)
 expect(new Set(Object.values(iconPaths)).size).toBe(32)
 const luminance=(hex:string)=>hex.slice(1).match(/../g)!.map(c=>parseInt(c,16)/255).map(c=>c<=.04045?c/12.92:((c+.055)/1.055)**2.4).reduce((a,c,i)=>a+c*[.2126,.7152,.0722][i],0)
 for(const colors of Object.values(palette))for(const offset of [0,2]){
  const a=luminance(colors[offset]),b=luminance(colors[offset+1])
  expect((Math.max(a,b)+.05)/(Math.min(a,b)+.05)).toBeGreaterThanOrEqual(4.5)
 }
})
