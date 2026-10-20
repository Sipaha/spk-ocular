import {useId,useRef,useState} from 'react'
import {ResourceTable} from '../components/ResourceTable'
import {t} from '../i18n'
import {concept,type ConceptId} from './catalog'
import {learn} from './content'
import {pathCopy,pathText} from './pathContent'
import {exercise,exerciseReducer,exerciseRows,initialExercise,topics,type ExerciseAction,type ExerciseState,type Topic,type View,type Fact} from './pathModel'
import {ResourceExplanation} from './ResourceHelp'
import {LearningYaml} from './LearningYaml'
const button='rounded border border-line px-3 py-1.5 text-xs hover:bg-hover disabled:opacity-40'
const status=(state:ExerciseState)=>state.complete?'completed':state.step||state.seen.length||state.diagnosis||state.repair||state.hint?'inProgress':'notStarted'

/** All progress and authored observations belong to this open library only. */
export function Course({introComplete,onIntro}:{introComplete:boolean;onIntro:()=>void}){
 const [states,setStates]=useState(()=>Object.fromEntries(topics.map(topic=>[topic,{...initialExercise}])) as Record<Topic,ExerciseState>)
 const [active,setActive]=useState<Topic|null>(null)
 const box=useRef<HTMLDivElement>(null)
 const done=Number(introComplete)+topics.filter(topic=>states[topic].complete).length
 const open=(topic:Topic|null)=>{box.current?.focus();setActive(topic)}
 return <div ref={box} tabIndex={-1} className="min-h-full min-w-0 outline-none" data-course>
  <header className="space-y-2 border-b border-line p-4">
   <div className="flex flex-wrap items-center justify-between gap-2"><h3 className="font-semibold">{pathText('course')}</h3><span role="status" className="text-xs text-fg-muted" data-course-progress>{pathText('progress',{done,total:6})}</span></div>
   <p className="text-xs leading-relaxed text-fg-muted">{pathText('scope')}</p><p className="text-xs text-fg-muted">{pathText('memory')}</p>
   {active&&<button className={button} onClick={()=>open(null)}>{pathText('overview')}</button>}
  </header>
  {active?<ExercisePanel key={active} topic={active} state={states[active]} send={action=>setStates(current=>({...current,[active]:exerciseReducer(active,current[active],action)}))}/>:<ol className="divide-y divide-line" aria-label={pathText('overview')}>
   <li className="flex flex-wrap items-center gap-3 p-4"><span aria-hidden className="text-fg-muted">1.</span><div className="min-w-0 flex-1"><h4 className="text-sm font-semibold">{learn('lesson')}</h4><p className="text-xs text-fg-muted">{pathText(introComplete?'completed':'notStarted')}</p></div><button className={button} aria-label={`${pathText('open')}: ${learn('lesson')}`} onClick={onIntro}>{pathText('open')}</button></li>
   {topics.map((topic,index)=><li key={topic} className="flex flex-wrap items-center gap-3 p-4" data-course-topic={topic}><span aria-hidden className="text-fg-muted">{index+2}.</span><div className="min-w-0 flex-1"><h4 className="text-sm font-semibold">{pathCopy().topics[topic]}</h4><p className="text-xs text-fg-muted">{pathText(status(states[topic]))}</p></div><button className={button} aria-label={`${pathText('open')}: ${pathCopy().topics[topic]}`} onClick={()=>open(topic)}>{pathText('open')}</button></li>)}
  </ol>}
 </div>
}
function ExercisePanel({topic,state,send}:{topic:Topic;state:ExerciseState;send:(action:ExerciseAction)=>void}){
 const current=exercise(topic,state),copy=pathCopy(),selected=current.objects.find(o=>o.id===state.selected)
 const [help,setHelp]=useState<ConceptId|null>(null)
 const box=useRef<HTMLDivElement>(null),tabsId=useId()
 const act=(action:ExerciseAction)=>{box.current?.focus();send(action);setHelp(null)}
 const visit=(view:View)=>{send({type:'view',token:current.token,view});setHelp(null)}
 const viewLabel=(view:View)=>view==='yaml'?'YAML':learn(view)
 const feedback=state.result==='support'?copy.cases[current.caseId].supportWhy:state.result==='repair'?(copy.repairFeedback[current.repairs.find(r=>r.id===state.repair)?.feedback??'replicas']):pathText(state.result==='evidence'?'evidenceError':state.result==='diagnosis'?'diagnosisError':'observe')
 const guide=topic==='probes'?'probes':current.caseId==='environment'?'rollout':null
 const factLabel=(fact:Fact)=>fact.observation?copy.observations[fact.observation]:fact.source?`Pod · ${learn(fact.source)}`:fact.field
 if(state.complete)return <section className="space-y-3 p-4" data-task-lesson={topic} data-phase="complete" ref={box} tabIndex={-1}><h4 className="font-semibold">{copy.topics[topic]}</h4><p role="status">{pathText('complete')}</p><button className={button} onClick={()=>act({type:'reset'})}>{learn('reset')}</button></section>
 return <section ref={box} tabIndex={-1} className="min-w-0 outline-none" data-task-lesson={topic} data-case={current.caseId} data-phase={state.fixed?'fixed':current.check?'check':'practice'} data-token={current.token}>
  <header className="space-y-3 border-b border-line p-4">
   <div className="flex flex-wrap items-center justify-between gap-2"><h4 className="font-semibold">{copy.topics[topic]}</h4><span className="text-xs text-fg-muted">{pathText('step',{step:state.step+1,total:4})} · {pathText(current.check?'check':'practice')}</span></div>
   <p className="text-sm leading-relaxed">{current.check?copy.checks[topic]:copy.cases[current.caseId].goal}</p>
   <div className="flex flex-wrap gap-2">{!current.check&&!state.fixed&&<button className={button} onClick={()=>act({type:'hint',token:current.token})}>{pathText('hint')}</button>}<button className={button} onClick={()=>act({type:'reset'})}>{learn('reset')}</button></div>
   {state.hint&&!current.check&&!state.fixed&&<p className="border-l-2 border-accent pl-3 text-sm text-fg-muted">{copy.cases[current.caseId].hint}</p>}
   {!current.check&&guide&&<ConceptGuide key={current.token} id={guide}/>}
   {topic==='config'&&<p className="text-xs text-fg-muted">{pathText('noSecrets')}</p>}
  </header>
  <div className="grid min-w-0 grid-cols-1 md:grid-cols-[minmax(220px,2fr)_minmax(0,3fr)]">
   <section className="flex min-h-[250px] min-w-0 flex-col border-b border-line md:border-r md:border-b-0" aria-label={copy.topics[topic]}>
    <ResourceTable columns={[{id:'name',title:t('action.name'),type:'text'},{id:'kind',title:t('action.kind'),type:'text',width:130}]} rows={exerciseRows(current)} hideScope filter="" selected={state.selected} onSelect={row=>{send({type:'select',token:current.token,id:row.id});setHelp(null)}} onOpen={row=>{send({type:'select',token:current.token,id:row.id});setHelp(null)}}/>
   </section>
   <section className="min-w-0 p-4" aria-label={learn('properties')}>
    {selected?<>
     <div className="mb-3 flex min-w-0 flex-wrap items-center gap-2"><h5 className="min-w-0 basis-full break-all text-sm font-semibold sm:flex-1 sm:basis-auto">{selected.name}</h5><span className="text-xs text-fg-muted">{concept(selected.kind)?.[1]}</span>{!current.check&&<button className={button} onClick={()=>setHelp(help?null:selected.kind)} aria-expanded={!!help}>{pathText('more')}</button>}</div>
     {help?<>{guide&&<ConceptGuide id={guide} expanded/>}<ResourceExplanation key={help} kind={help} onConcept={id=>{box.current?.focus();setHelp(id)}}/></>:<>
      <div role="tablist" aria-label={selected.name} className="mb-3 flex flex-wrap gap-2" onKeyDown={event=>{
       const views:View[]=['properties','yaml','events','logs'],index=views.indexOf(state.view),next=event.key==='ArrowRight'?(index+1)%4:event.key==='ArrowLeft'?(index+3)%4:event.key==='Home'?0:event.key==='End'?3:-1
       if(next>=0){event.preventDefault();event.stopPropagation();visit(views[next]);event.currentTarget.querySelectorAll<HTMLButtonElement>('[role=tab]')[next]?.focus()}
      }}>{(['properties','yaml','events','logs'] as const).map(view=><button key={view} id={`${tabsId}-${view}`} role="tab" aria-selected={state.view===view} aria-controls={`${tabsId}-panel`} tabIndex={state.view===view?0:-1} className={button+(state.view===view?' bg-active text-accent':'')} onClick={()=>visit(view)}>{viewLabel(view)}</button>)}</div>
      <div id={`${tabsId}-panel`} role="tabpanel" aria-labelledby={`${tabsId}-${state.view}`} className="min-h-[180px] min-w-0">
       {state.view==='properties'&&<><dl className="grid grid-cols-1 gap-y-1 text-xs sm:grid-cols-[minmax(0,1fr)_minmax(0,1fr)] sm:gap-x-3 sm:gap-y-2">{[['UID',selected.uid],...selected.properties].map(([key,value])=><div key={key} className="contents"><dt className="break-all font-mono text-fg-muted">{key}</dt><dd className="mb-2 break-all font-mono sm:mb-0">{value}</dd></div>)}</dl>{selected.observations.length>0&&<section className="mt-4 space-y-2 border-t border-line pt-3"><h6 className="text-xs font-semibold text-fg-muted">{pathText('observations')}</h6><dl className="space-y-2 text-xs">{selected.observations.map(([key,value])=><div key={key} className="grid min-w-0 grid-cols-1 gap-1 sm:grid-cols-2 sm:gap-3"><dt className="text-fg-muted">{copy.observations[key]}</dt><dd className="break-all font-mono">{value}</dd></div>)}</dl></section>}</>}
       {state.view==='yaml'&&<div className="h-64 min-w-0 overflow-hidden"><LearningYaml text={selected.yaml} label={`${selected.name} YAML`} documentId={selected.uid}/></div>}
       {state.view==='events'&&(selected.events?<pre className="whitespace-pre-wrap break-all text-xs">{selected.events}</pre>:<p className="text-xs text-fg-muted">{pathText('noEvents')}</p>)}
       {state.view==='logs'&&(selected.logs?<pre className="whitespace-pre-wrap break-all text-xs">{selected.logs}</pre>:<p className="text-xs text-fg-muted">{pathText('noLogs')}</p>)}
      </div>
     </>}
    </>:<p className="text-sm text-fg-muted">{learn('select')}</p>}
   </section>
  </div>
  <div className="space-y-3 border-t border-line p-4">
   {state.fixed?<><p role="status" className="text-sm">{pathText('fixed')}</p><p className="text-sm leading-relaxed text-fg-muted">{copy.cases[current.caseId].why}</p>{current.rollout&&<section className="space-y-2 border border-line p-3 text-xs" data-rollout-result><h5 className="font-semibold">{pathText('templateChange')}</h5><code className="block whitespace-pre-wrap break-all">{current.rollout.field} : {current.rollout.before} → {current.rollout.after}</code><h5 className="font-semibold">{pathText('podReplacement')}</h5><code className="block whitespace-pre-wrap break-all">{current.rollout.previousUID} → {current.rollout.nextUID}</code></section>}<button className={button} onClick={()=>act({type:'next',token:current.token})}>{pathText(state.step===3?'finish':'next')}</button></>:<>
    <p className="text-xs text-fg-muted">{pathText('evidence')}</p>
    <ul className="space-y-1 text-xs" data-required-evidence>{current.required.map(key=>{const [id,view]=key.split(':'),obj=current.objects.find(o=>o.id===id)!;return <li key={key} className={state.seen.includes(key)?'text-success':'text-fg-muted'} data-evidence={key} data-inspected={state.seen.includes(key)}>{state.seen.includes(key)?'✓':'○'} {concept(obj.kind)?.[1]} / {obj.name} · {viewLabel(view as View)}</li>})}</ul>
    <form className="space-y-3" onSubmit={event=>{event.preventDefault();act({type:'apply',token:current.token})}}>
     <fieldset className="space-y-2"><legend className="mb-2 text-sm font-semibold">{pathText('diagnosis')}</legend>{current.diagnoses.map(id=><label key={id} className="flex items-start gap-2 text-sm"><input className="mt-1 shrink-0" type="radio" name={`${current.token}-diagnosis`} value={id} checked={state.diagnosis===id} onChange={()=>send({type:'diagnosis',token:current.token,value:id})}/><span>{copy.diagnoses[id]}</span></label>)}</fieldset>
     {current.check&&<fieldset className="space-y-2" data-support><legend className="mb-2 text-sm font-semibold">{pathText('support')}</legend>{current.supports.map(option=><label key={option.id} className="flex min-w-0 items-start gap-2 rounded border border-line p-2 text-xs"><input className="mt-1 shrink-0" type="radio" name={`${current.token}-support`} value={option.id} checked={state.support===option.id} onChange={()=>send({type:'support',token:current.token,value:option.id})}/><span className="min-w-0 space-y-1">{option.facts.map((fact,index)=><span key={index} className="block min-w-0 break-all"><span className={fact.observation||fact.source?'text-fg-muted':'font-mono text-fg-muted'}>{factLabel(fact)}</span> : <code className="inline-block max-w-full align-top whitespace-pre-wrap break-normal [overflow-wrap:anywhere]">{fact.value}</code></span>)}</span></label>)}</fieldset>}
     <fieldset className="space-y-2"><legend className="mb-2 text-sm font-semibold">{pathText('repair')}</legend>{current.repairs.map(option=><label key={option.id} className="flex min-w-0 items-start gap-2 text-sm"><input className="mt-1 shrink-0" type="radio" name={`${current.token}-repair`} value={option.id} checked={state.repair===option.id} onChange={()=>send({type:'repair',token:current.token,value:option.id})}/><code className="min-w-0 whitespace-pre-wrap break-all text-xs">{option.patch}</code></label>)}</fieldset>
     <button className={button} disabled={!state.diagnosis||!state.repair}>{pathText('apply')}</button>
    </form>
   </>}
   {state.result&&<p role="alert" className="text-sm text-warning">{feedback}</p>}
  </div>
 </section>
}

function ConceptGuide({id,expanded=false}:{id:'probes'|'rollout';expanded?:boolean}){
 const guide=pathCopy().guides[id]
 return <details open={expanded||undefined} className="rounded border border-line p-3 text-xs" data-concept-guide={id}><summary className="cursor-pointer font-semibold">{guide.title}</summary><div className="mt-2 space-y-2 leading-relaxed">{Object.entries(guide).filter(([key])=>key!=='title').map(([key,text])=><p key={key}>{text}</p>)}</div></details>
}
