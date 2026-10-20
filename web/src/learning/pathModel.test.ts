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
 if(e.check)state=exerciseReducer(topic,state,{type:'support',token:e.token,value:'cause'})
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
   if(e.check)state=exerciseReducer(topic,state,{type:'support',token:e.token,value:'cause'})
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
  for(const action of [{type:'select',id:'pod'},{type:'repair',value:old.correctRepair},{type:'support',value:'cause'},{type:'apply'},{type:'next'}] as const)expect(exerciseReducer(topic,state,{...action,token:old.token})).toBe(state)
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
  for(const group of ['ui','topics','diagnoses','cases','observations','checks','guides','repairFeedback'] as const)expect(Object.keys(local[group]).sort()).toEqual(Object.keys(pathContent.en[group]).sort())
  for(const [key,text] of Object.entries(local.ui)){
   expect(text.trim().length).toBeGreaterThan(1)
   expect([...text.matchAll(/\{\w+\}/g)].map(m=>m[0]).sort()).toEqual([...pathContent.en.ui[key as keyof typeof local.ui].matchAll(/\{\w+\}/g)].map(m=>m[0]).sort())
  }
  for(const entry of Object.values(local.cases)){expect(Object.keys(entry).sort()).toEqual(['goal','hint','supportWhy','why']);for(const text of Object.values(entry)){expect(text.length).toBeGreaterThan(15);expect(text).not.toMatch(/TODO|TBD|lorem ipsum/i)}}
  for(const topic of topics)for(let step=0;step<4;step++){const e=exercise(topic,{...initialExercise,step});expect(local.cases[e.caseId]).toBeDefined();for(const id of e.diagnoses)expect(local.diagnoses[id].length).toBeGreaterThan(10)}
 }
})

it('checks do not repeat the practice order and always cover both causes after reset',()=>{
 for(const topic of topics){
  const practice=[0,1].map(step=>exercise(topic,{...initialExercise,step}).caseId)
  const checks=[2,3].map(step=>exercise(topic,{...initialExercise,step}).caseId)
  expect(checks).toEqual([...practice].reverse())
  const resetChecks=[2,3].map(step=>exercise(topic,{...initialExercise,generation:1,step}).caseId)
  expect(new Set(resetChecks)).toEqual(new Set(practice))
  expect(resetChecks).not.toEqual(checks)
 }
})
it('rejects absent, incidental and stale supporting observations even with a correct diagnosis and repair',()=>{
 for(const topic of topics)for(const step of [2,3]){
  let state={...initialExercise,step};const e=exercise(topic,state)
  for(const key of e.required)state=inspect(topic,state,key)
  state={...state,diagnosis:e.caseId,repair:e.correctRepair}
  expect(apply(topic,state,{type:'apply'})).toMatchObject({fixed:false,result:'support'})
  for(const value of ['context','other']){
   state=exerciseReducer(topic,state,{type:'support',token:e.token,value})
   expect(apply(topic,state,{type:'apply'})).toMatchObject({fixed:false,result:'support'})
  }
  expect(exerciseReducer(topic,state,{type:'support',token:e.token,value:'invented'})).toBe(state)
  const reset=exerciseReducer(topic,state,{type:'reset'})
  expect(exerciseReducer(topic,reset,{type:'support',token:e.token,value:'cause'})).toBe(reset)
  state=exerciseReducer(topic,state,{type:'support',token:e.token,value:'cause'})
  expect(apply(topic,state,{type:'apply'}).fixed).toBe(true)
 }
})
it('requires the validated CPU request view before accepting a scheduling repair',()=>{
 for(const step of [0,3]){
  let state={...initialExercise,step};const e=exercise('resources',state)
  expect(e.caseId).toBe('requests');expect(e.required).toContain('pod:properties')
  expect(e.objects.find(o=>o.id==='pod')!.observations).toContainEqual(['validatedCPU',step?'500m':'250m'])
  for(const key of e.required.filter(key=>key!=='pod:properties'))state=inspect('resources',state,key)
  state={...state,diagnosis:'requests',repair:'requests',support:'cause'}
  expect(apply('resources',state,{type:'apply'})).toMatchObject({fixed:false,result:'evidence'})
  state=inspect('resources',state,'pod:properties')
  expect(apply('resources',state,{type:'apply'}).fixed).toBe(true)
 }
})
it('provides concrete feedback for every wrong repair and complete localized explanations',()=>{
 for(const topic of topics)for(let step=0;step<4;step++){
  const e=exercise(topic,{...initialExercise,step})
  for(const repair of e.repairs.filter(r=>r.id!==e.correctRepair)){
   expect(repair.feedback).toBeTruthy()
   for(const lang of languages){const text=pathContent[lang].repairFeedback[repair.feedback!];expect(text.trim()).not.toBe('');if(lang!=='en')expect(text).not.toBe(pathContent.en.repairFeedback[repair.feedback!])}
  }
  expect(e.supports).toHaveLength(3);expect(new Set(e.supports.map(s=>s.id)).size).toBe(3)
  for(const choice of e.supports)for(const fact of choice.facts){
   expect(Number(!!fact.field)+Number(!!fact.observation)+Number(!!fact.source)).toBe(1)
   expect(fact.value.length).toBeGreaterThan(0)
   if(fact.field)expect(fact.field).not.toMatch(/\.(logs|events)$/)
  }
 }
 for(const lang of languages){
  const local=pathContent[lang]
  for(const id of ['probes','rollout'] as const){
   expect(Object.keys(local.guides[id])).toEqual(Object.keys(pathContent.en.guides[id]))
   for(const [key,value] of Object.entries(local.guides[id])){expect(value.trim()).not.toBe('');if(lang!=='en')expect(value).not.toBe(Object.entries(pathContent.en.guides[id]).find(([k])=>k===key)![1])}
  }
  expect(local.guides.rollout.change).toContain('lesson-revision')
  expect(local.guides.rollout.change).toContain('spec.template')
  for(const value of Object.values(local.checks))expect(value).not.toMatch(/OOMKilled|targetPort|args|selector|secretKeyRef/)
 }
})
it('shows the exact template change and new Pod UID for an environment rollout',()=>{
 const before=exercise('config',initialExercise),after=exercise('config',{...initialExercise,fixed:true})
 expect(after.rollout).toEqual({field:'Deployment.spec.template.metadata.annotations.lesson-revision',before:'∅',after:'"2"',previousUID:before.objects.find(o=>o.id==='pod')!.uid,nextUID:after.objects.find(o=>o.id==='pod')!.uid})
 expect(after.rollout!.nextUID).not.toBe(after.rollout!.previousUID)
 expect(after.objects.find(o=>o.id==='deployment')!.yaml).toContain('lesson-revision: "2"')
})
