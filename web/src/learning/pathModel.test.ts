import {describe,it,expect} from 'vitest'
import {yamlLanguage} from '@codemirror/lang-yaml'
import {exercise,exerciseReducer,initialExercise,topics,type Topic,type ExerciseState,type ExerciseAction,type View} from './pathModel'
import {pathContent} from './pathContent'
import {languages} from '../languages'

function apply(topic:Topic,state:ExerciseState,action:Omit<ExerciseAction,'token'>):ExerciseState {
 return exerciseReducer(topic,state,{...action,token:exercise(topic,state).token} as ExerciseAction)
}
function inspect(topic:Topic,state:ExerciseState,key:string){
 const [id,view]=key.split(':');state=exerciseReducer(topic,state,{type:'select',token:exercise(topic,state).token,id})
 return exerciseReducer(topic,state,{type:'view',token:exercise(topic,state).token,view:view as View})
}
function solve(topic:Topic,state:ExerciseState){
 const e=exercise(topic,state)
 for(const key of e.required)state=inspect(topic,state,key)
 state=exerciseReducer(topic,state,{type:'diagnosis',token:e.token,value:e.caseId})
 state=exerciseReducer(topic,state,{type:'repair',token:e.token,value:e.correctRepair})
 return apply(topic,state,{type:'apply'})
}

describe.each(topics)('%s authored learning module',topic=>{
 it('requires evidence, correct diagnosis, bounded repair and observation in all four tasks',()=>{
  let state={...initialExercise}
  const identities=new Set<string>(),names=new Set<string>()
  for(let step=0;step<4;step++){
   const e=exercise(topic,state);expect(e.check).toBe(step>=2);expect(e.objects.length).toBeGreaterThanOrEqual(2)
   for(const obj of e.objects){expect(identities.has(obj.uid)).toBe(false);identities.add(obj.uid)}
   names.add(e.name)
   expect(apply(topic,state,{type:'next'})).toBe(state)
   state=exerciseReducer(topic,state,{type:'diagnosis',token:e.token,value:e.caseId});state=exerciseReducer(topic,state,{type:'repair',token:e.token,value:e.correctRepair})
   const noEvidence=apply(topic,state,{type:'apply'});expect(noEvidence.fixed).toBe(false);expect(noEvidence.result).toBe('evidence')
   for(const key of e.required)state=inspect(topic,state,key)
   state=exerciseReducer(topic,state,{type:'diagnosis',token:e.token,value:e.diagnoses.find(d=>d!==e.caseId)!})
   expect(apply(topic,state,{type:'apply'}).result).toBe('diagnosis')
   state=exerciseReducer(topic,state,{type:'diagnosis',token:e.token,value:e.caseId})
   state=exerciseReducer(topic,state,{type:'repair',token:e.token,value:e.repairs.find(r=>r.id!==e.correctRepair)!.id})
   expect(apply(topic,state,{type:'apply'}).result).toBe('repair')
   state=solve(topic,state);expect(state.fixed).toBe(true)
   expect(apply(topic,state,{type:'next'}).result).toBe('observe')
   const result=exercise(topic,state);expect(result.token).toBe(e.token)
   const before=e.objects.find(o=>o.id==='pod')!,after=result.objects.find(o=>o.id==='pod')!
   if(topic==='network')expect(after.uid).toBe(before.uid);else expect(after.uid).not.toBe(before.uid)
   state=inspect(topic,state,result.resultEvidence);state=apply(topic,state,{type:'next'})
   expect(state.complete).toBe(step===3)
  }
  expect(names.size).toBe(2)
  expect(apply(topic,state,{type:'next'})).toBe(state)
 })
 it('rejects stale identities/actions after reset and between tasks; checks never expose hints',()=>{
  let state={...initialExercise};const old=exercise(topic,state)
  state=exerciseReducer(topic,state,{type:'reset'})
  expect(exercise(topic,state).token).not.toBe(old.token)
  expect(exercise(topic,state).objects[0].uid).not.toBe(old.objects[0].uid)
  for(const action of [{type:'select',id:'pod'},{type:'repair',value:old.correctRepair},{type:'apply'},{type:'next'}] as const)expect(exerciseReducer(topic,state,{...action,token:old.token})).toBe(state)
  state={...state,step:2};expect(apply(topic,state,{type:'hint'})).toBe(state)
  expect(exerciseReducer(topic,state,{type:'select',token:exercise(topic,state).token,id:'live-production-uid'})).toBe(state)
 })
 it('ships syntactically valid read-only YAML and internally resolvable evidence for both states',()=>{
  for(let step=0;step<4;step++)for(const fixed of [false,true]){
   const e=exercise(topic,{...initialExercise,step,fixed})
   expect(e.diagnoses).toContain(e.caseId);expect(e.repairs.filter(r=>r.id===e.correctRepair)).toHaveLength(1)
   for(const key of [...e.required,e.resultEvidence]){const [id,view]=key.split(':');const obj=e.objects.find(o=>o.id===id);expect(obj).toBeDefined();if(view==='logs')expect(obj?.logs).toBeTruthy();if(view==='events')expect(obj?.events).toBeTruthy()}
   for(const obj of e.objects){for(const [key]of obj.properties)expect(key).toMatch(/^(metadata\.|spec\.|status\.|data\.|ports\[|endpoints\[|type$)/);for(const [key]of obj.observations)expect(pathContent.en.observations[key]).toBeTruthy();const errors:number[]=[];yamlLanguage.parser.parse(obj.yaml).iterate({enter(node){if(node.type.isError)errors.push(node.from)}});expect(errors,obj.yaml).toEqual([]);expect(obj.yaml).toContain(`uid: ${obj.uid}`)}
  }
 })
})

it('repairs the actual authored fields without inventing runtime observations',()=>{
 const get=(topic:Topic,step:number,fixed=false)=>exercise(topic,{...initialExercise,step,fixed})
 expect(get('network',0).objects.find(o=>o.id==='endpoints')!.observations).toContainEqual(['readyAddresses','[]'])
 expect(get('network',0,true).objects.find(o=>o.id==='endpoints')!.observations).toContainEqual(['readyAddresses','192.0.2.10'])
 expect(get('network',1).objects.find(o=>o.id==='service')!.yaml).toContain('targetPort: 8081')
 expect(get('network',1,true).objects.find(o=>o.id==='service')!.yaml).toContain('targetPort: 8080')
 expect(get('runtime',0).objects.find(o=>o.id==='pod')!.logs).toBeNull()
 expect(get('runtime',1).objects.find(o=>o.id==='pod')!.logs).toContain('unknown option --servve')
 expect(get('config',0,true).objects.find(o=>o.id==='pod')!.observations).toContainEqual(['environment','production'])
 expect(get('config',1,true).objects.find(o=>o.id==='deployment')!.yaml).toContain('key: PASSWORD')
 const secret=get('config',1).objects.find(o=>o.id==='secret')!;expect(secret.yaml).not.toMatch(/^(data|stringData):/m)
 expect(get('resources',0,true).objects.find(o=>o.id==='deployment')!.yaml).toContain('cpu: 250m')
 expect(get('resources',1,true).objects.find(o=>o.id==='deployment')!.yaml).toContain('memory: 256Mi')
})

it('ships complete path content and exact placeholder contracts in all eight languages',()=>{
 for(const lang of languages){
  const local=pathContent[lang]
  for(const group of ['ui','topics','diagnoses','cases','observations'] as const)expect(Object.keys(local[group]).sort()).toEqual(Object.keys(pathContent.en[group]).sort())
  for(const [key,text] of Object.entries(local.ui)){
   expect(text.trim().length).toBeGreaterThan(1)
   expect([...text.matchAll(/\{\w+\}/g)].map(m=>m[0]).sort()).toEqual([...pathContent.en.ui[key as keyof typeof local.ui].matchAll(/\{\w+\}/g)].map(m=>m[0]).sort())
  }
  for(const entry of Object.values(local.cases)){expect(Object.keys(entry).sort()).toEqual(['goal','hint','why']);for(const text of Object.values(entry)){expect(text.length).toBeGreaterThan(15);expect(text).not.toMatch(/TODO|TBD|lorem ipsum/i)}}
  for(const topic of topics)for(let step=0;step<4;step++){const e=exercise(topic,{...initialExercise,step});expect(local.cases[e.caseId]).toBeDefined();for(const id of e.diagnoses)expect(local.diagnoses[id].length).toBeGreaterThan(10)}
 }
})
