import {describe,it,expect} from 'vitest'
import {initialLesson,labObjects,labYAML,lessonReducer as reduce,type LessonState} from './model'
function replaced(){let s=reduce(initialLesson,{type:'select',uid:'pod-a-0'});s=reduce(s,{type:'delete'});return reduce(s,{type:'reconcile'})}
function ownerChain(s:LessonState){s=reduce(s,{type:'select',uid:labObjects(s).find(o=>o.kind==='pods')!.uid});s=reduce(s,{type:'owner'});return reduce(s,{type:'owner'})}
describe('isolated reconciliation lesson',()=>{
 it('deletion does not alter desired replicas and replacement has a new identity',()=>{
  const old=labObjects(initialLesson).filter(o=>o.kind==='pods')
  const selected=reduce(initialLesson,{type:'select',uid:old[0].uid}),deleted=reduce(selected,{type:'delete'})
  expect(labObjects(deleted).filter(o=>o.kind==='pods')).toHaveLength(1)
  expect(labYAML(labObjects(deleted)[1],labObjects(deleted))).toContain('replicas: 2')
  const next=reduce(deleted,{type:'reconcile'}),pods=labObjects(next).filter(o=>o.kind==='pods')
  expect(pods).toHaveLength(2);expect(pods.some(p=>p.uid===old[0].uid)).toBe(false)
  expect(pods.some(p=>!old.some(o=>o.uid===p.uid))).toBe(true)
  expect(reduce(next,{type:'reconcile'})).toBe(next)
 })
 it('cannot delete a controller or accept a real or stale object UID',()=>{
  expect(reduce(initialLesson,{type:'delete'})).toBe(initialLesson)
  const controller=reduce(initialLesson,{type:'select',uid:'deployment-0'})
  expect(reduce(controller,{type:'delete'})).toBe(controller)
  expect(reduce(controller,{type:'select',uid:'production-pod'})).toBe(controller)
  const reset=reduce(controller,{type:'reset'})
  expect(reduce(reset,{type:'select',uid:'deployment-0'})).toBe(reset)
 })
 it('requires real owner navigation in the check, not direct Deployment selection',()=>{
  let s=reduce(replaced(),{type:'challenge'})
  s=reduce(s,{type:'answer',answer:'controller'})
  s=reduce(s,{type:'select',uid:'deployment-1'})
  expect(reduce(s,{type:'verify'}).result).toBe('findOwner')
  s=ownerChain(s);expect(s.foundOwner).toBe(true)
  expect(reduce(s,{type:'verify'}).phase).toBe('complete')
 })
 it('rejects wrong explanations, disables check hints and clears evidence on reset',()=>{
  let s=reduce(replaced(),{type:'challenge'});expect(reduce(s,{type:'hint'}).hint).toBe(false)
  s=ownerChain(s);s=reduce(s,{type:'answer',answer:'restart'})
  expect(reduce(s,{type:'verify'}).result).toBe('retry')
  const reset=reduce(s,{type:'reset'});expect(reset.phase).toBe('observe');expect(reset.foundOwner).toBe(false);expect(reset.answer).toBe('');expect(reset.hint).toBe(false)
 })
 it('does not allow skipping the simulation and keeps owners in the local scenario',()=>{
  expect(reduce(initialLesson,{type:'challenge'})).toBe(initialLesson)
  for(const s of [initialLesson,replaced(),reduce(replaced(),{type:'challenge'})]){
   const objects=labObjects(s)
   for(const obj of objects)if(obj.owner)expect(objects.some(o=>o.uid===obj.owner)).toBe(true)
  }
 })
})
