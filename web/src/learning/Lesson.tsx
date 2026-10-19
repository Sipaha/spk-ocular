import {lazy,Suspense,useReducer,useRef,useState} from 'react'
import {t} from '../i18n'
import {ResourceTable} from '../components/ResourceTable'
import {concept} from './catalog'
import {learn} from './content'
import {initialLesson,labObjects,labYAML,lessonReducer,lessonRows} from './model'
import {ResourceExplanation} from './ResourceHelp'
const YamlView=lazy(()=>import('../components/YamlView'))
const button='rounded border border-line px-3 py-1.5 text-xs hover:bg-hover disabled:opacity-40'
/** Authored local state machine. No Client, provider, transport or target access. */
export function Lesson(){
 const [state,dispatch]=useReducer(lessonReducer,initialLesson),[tab,setTab]=useState('properties'),[help,setHelp]=useState(false)
 const focus=useRef<HTMLDivElement>(null),details=useRef<HTMLElement>(null)
 const objects=labObjects(state),selected=objects.find(o=>o.uid===state.selected),owner=objects.find(o=>o.uid===selected?.owner)
 const challenge=state.phase==='challenge'||state.phase==='complete'
 const navigate=(uid:string)=>{dispatch({type:'select',uid});setHelp(false)}
 const rows=lessonRows(state)
 return <div className="flex min-h-full min-w-0 flex-col" data-lesson data-phase={state.phase} ref={focus} tabIndex={-1}>
  <section className="shrink-0 space-y-2 border-b border-line p-4">
   <h3 className="font-semibold">{learn('lesson')}</h3><p className="text-xs leading-relaxed text-fg-muted">{learn('isolation')}</p>
   <p role="status" className="text-sm leading-relaxed">{learn(state.phase==='observe'?'intro':state.phase==='deleted'?'deleted':state.phase==='replaced'?'replaced':state.phase==='challenge'?'challenge':'complete')}</p>
   <div className="flex flex-wrap gap-2">
    {!challenge&&<button className={button} onClick={()=>dispatch({type:'hint'})}>{learn('hint')}</button>}
    {state.phase==='observe'&&<button className={button} disabled={selected?.kind!=='pods'} onClick={()=>{focus.current?.focus();dispatch({type:'delete'});setHelp(false)}}>{learn('remove')}</button>}
    {state.phase==='deleted'&&<button className={button} onClick={()=>{focus.current?.focus();dispatch({type:'reconcile'})}}>{learn('reconcile')}</button>}
    {state.phase==='replaced'&&<button className={button+' text-accent'} onClick={()=>{focus.current?.focus();dispatch({type:'challenge'});setHelp(false);setTab('properties')}}>{learn('check')}</button>}
    <button className={button} onClick={()=>{focus.current?.focus();dispatch({type:'reset'});setTab('properties');setHelp(false)}}>{learn('reset')}</button>
   </div>
   {state.hint&&!challenge&&<p className="border-l-2 border-accent pl-3 text-sm text-fg-muted">{learn('hintText')}</p>}
  </section>
  <div className="grid min-h-0 flex-1 grid-cols-1 md:grid-cols-2">
   <section className="flex min-h-[240px] min-w-0 flex-col border-b border-line md:border-r md:border-b-0" aria-label={learn('concepts')}>
    <ResourceTable columns={[{id:'name',title:t('action.name'),type:'text'},{id:'kind',title:t('action.kind'),type:'text',width:130}]} rows={rows} hideScope filter="" selected={state.selected} onSelect={row=>navigate(row.id)} onOpen={row=>navigate(row.id)}/>
   </section>
   <section className="flex min-h-[280px] min-w-0 flex-col p-4" aria-label={learn('properties')} ref={details} tabIndex={-1}>
    {selected?<>
     <header className="mb-3 flex min-w-0 items-center gap-2"><h4 className="min-w-0 flex-1 break-all text-sm font-semibold">{selected.name}</h4><span className="text-xs text-fg-muted">{concept(selected.kind)?.[1]}</span>{!challenge&&<button className={button} onClick={()=>setHelp(!help)} aria-expanded={help}>{learn('what')}</button>}</header>
     {help?<ResourceExplanation kind={selected.kind} onConcept={id=>{const object=objects.find(o=>o.kind===id);if(object){details.current?.focus();navigate(object.uid)}}}/>:<>
      <div role="tablist" aria-label={selected.name} onKeyDown={event=>{
       const keys=['properties','yaml','events','logs'],index=keys.indexOf(tab)
       const next=event.key==='ArrowRight'?(index+1)%4:event.key==='ArrowLeft'?(index+3)%4:event.key==='Home'?0:event.key==='End'?3:-1
       if(next>=0){event.preventDefault();event.stopPropagation();setTab(keys[next]);event.currentTarget.querySelectorAll<HTMLButtonElement>('[role=tab]')[next]?.focus()}
      }} className="mb-3 flex flex-wrap gap-2">{(['properties','yaml','events','logs'] as const).map(key=><button role="tab" tabIndex={tab===key?0:-1} aria-selected={tab===key} key={key} className={button+(tab===key?' bg-active text-accent':'')} onClick={()=>setTab(key)}>{key==='yaml'?'YAML':learn(key)}</button>)}</div>
      <div role="tabpanel" aria-label={tab==='yaml'?'YAML':learn(tab as 'properties'|'events'|'logs')} className="min-h-[180px] min-w-0 flex-1 text-sm">
       {tab==='properties'&&<dl className="grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-2"><dt className="text-fg-muted">UID</dt><dd className="break-all font-mono text-xs">{selected.uid}</dd><dt className="text-fg-muted">{learn('owner')}</dt><dd>{owner?<button className="break-all text-left text-accent underline-offset-4 hover:underline" data-lesson-owner onClick={()=>{details.current?.focus();dispatch({type:'owner'});setHelp(false)}}>{concept(owner.kind)?.[1]} / {owner.name}</button>:learn('noOwner')}</dd><dt className="text-fg-muted">{selected.kind==='pods'?'status.phase':'spec.replicas'}</dt><dd className="font-mono text-xs">{selected.kind==='pods'?'Running':2}</dd></dl>}
       {tab==='yaml'&&<Suspense fallback={<p>{t('app.loading')}</p>}><div className="h-64 min-w-0 overflow-hidden"><YamlView text={labYAML(selected,objects)} label={`${selected.name} YAML`} documentId={selected.uid}/></div></Suspense>}
       {tab==='events'&&<ul className="space-y-2 font-mono text-xs"><li>Normal · {selected.kind==='pods'?'Started':selected.kind==='apps/replicasets'?'SuccessfulCreate':'ScalingReplicaSet'} · {selected.name}</li>{state.deleted&&<li>SimulatedDelete · {state.deleted}</li>}{state.phase==='replaced'&&<li>SuccessfulCreate · pod-new-{state.generation}</li>}</ul>}
       {tab==='logs'&&(selected.kind==='pods'?<pre className="whitespace-pre-wrap break-all text-xs">{`[simulation] ${selected.name}\napp: started\napp: ready to serve\n`}</pre>:<p className="text-fg-muted">{learn('noLogs')}</p>)}
      </div>
     </>}
    </>:<p className="text-sm text-fg-muted">{learn('select')}</p>}
   </section>
  </div>
  {state.phase==='challenge'&&<form className="space-y-3 border-t border-line p-4" onSubmit={e=>{e.preventDefault();focus.current?.focus();dispatch({type:'verify'})}}>
   <fieldset><legend className="mb-2 text-sm font-semibold">{learn('question')}</legend>{(['controller','restart','service'] as const).map(answer=><label key={answer} className="mb-2 flex items-start gap-2 text-sm"><input className="mt-1" type="radio" name="explanation" value={answer} checked={state.answer===answer} onChange={()=>dispatch({type:'answer',answer})}/><span>{learn(answer==='controller'?'answerController':answer==='restart'?'answerRestart':'answerService')}</span></label>)}</fieldset>
   <button className={button} disabled={!state.answer}>{learn('verify')}</button>{state.result&&<p role="alert" className="text-sm text-warning">{learn(state.result)}</p>}
  </form>}
 </div>
}
