import {vpaState} from '../vpa/ResourceVPA'
import {checkField,checkValue} from '../reliability/presentation'
import {explanation} from '../reliability/ResourceReliability'
import {useCallback,useContext,useState,type ComponentProps,type ReactNode} from 'react'
import type {Client} from '../api/client'
import type {Ref,ScopeSel} from '../api/types'
import {t,classLabel} from '../i18n'
import {ViewConnectionContext} from '../views/useView'
import {useSnapshotRequest} from '../views/useSnapshotRequest'
import {WorkspaceToolbar} from '../components/WorkspaceToolbar'
import {ResourceDrawer} from '../components/ResourceDrawer'
import {DateText} from '../components/DateText'
import {AssessmentIntro} from '../components/AssessmentIntro'
import {mayLeave} from '../edit/guard'
import {errorDetail} from '../errors'
import type {AssessmentCheck,AssessmentCounts} from './types'

type Drawer=Omit<ComponentProps<typeof ResourceDrawer>,'subject'|'onClose'>
type Props={client:Client;target:{provider:string;id:string};scope:ScopeSel;scopePicker:ReactNode;drawer:Drawer}
const button='rounded border border-line px-3 py-1.5 text-sm hover:bg-hover focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-40'
const tools={configuration:{title:'reliability.title',purpose:'reliability.purpose',notice:'reliability.notice',action:'reliability.load'},vpa:{title:'vpa.title',purpose:'vpa.purpose',notice:'vpa.notice',action:'vpa.load'},diagnosis:{title:'diagnosis.title',purpose:'diagnosis.purpose',notice:'diagnosis.notice',action:'diagnosis.run'}} as const
export default function AssessmentWorkspace(props:Props){
 const connection=useContext(ViewConnectionContext)
 const [owner,setOwner]=useState({client:props.client,epoch:0})
 const epoch=owner.epoch+(owner.client===props.client?0:1)
 if(owner.client!==props.client)setOwner({client:props.client,epoch})
 return <Panel key={JSON.stringify([props.target.provider,props.target.id,props.scope,connection,epoch])} {...props}/>
}
function Panel({client,target,scope,scopePicker,drawer}:Props){
 const [check,setCheck]=useState<AssessmentCheck>('configuration'),[enabled,setEnabled]=useState(false),[subject,setSubject]=useState<Ref|null>(null)
 const namespace=scope.mode==='one'?(scope.name??''):''
 const tool=tools[check]
 const load=useCallback(async(signal:AbortSignal)=>{
  const result=await client.assessNamespace({provider:target.provider,target:target.id,namespace,check},signal)
  if(result.namespace!==namespace||result.check!==check||result.resources.some(r=>r.ref.provider!==target.provider||r.ref.target!==target.id||r.ref.scope!==namespace||!r.ref.uid))throw new Error(t('workspace.replaced'))
  return result
 },[client,target.provider,target.id,namespace,check])
 const request=useSnapshotRequest(load,JSON.stringify([target.provider,target.id,namespace,check]),{enabled:enabled&&!!namespace})
 const snapshot=!request.busy&&!request.error&&!request.cancelled?request.value:null
 const totals:AssessmentCounts={observed:0,review:0,unknown:0,excluded:0}
 for(const r of snapshot?.resources??[])for(const key of Object.keys(totals) as (keyof AssessmentCounts)[])totals[key]+=r.counts[key]
 const open=(ref:Ref)=>mayLeave(()=>setSubject(ref))
 return <section aria-label={t('assessment.title')} className="flex min-h-0 flex-1 flex-col">
  <WorkspaceToolbar title={t('assessment.title')}>{scopePicker}</WorkspaceToolbar>
  <div className="flex min-h-0 min-w-0 flex-1">
   <div className="min-w-0 flex-1 overflow-auto p-4">
    <div className="mx-auto max-w-5xl space-y-4">
     <h1 className="text-xl font-semibold tracking-tight">{t('assessment.title')}{namespace&&<span className="ml-2 font-mono text-base font-normal text-fg-muted">{namespace}</span>}</h1>
     <p className="text-sm leading-relaxed">{t('assessment.purpose')}</p>
     <p className="text-xs text-fg-muted">{t('assessment.readOnly')}</p>
     <div className="flex flex-wrap gap-2" role="group" aria-label={t('assessment.title')}>
      {(Object.keys(tools) as AssessmentCheck[]).map(value=><button key={value} className={button+(value===check?' border-accent bg-hover':'')} aria-pressed={value===check} onClick={()=>{if(value!==check)mayLeave(()=>{request.cancel();setEnabled(false);setSubject(null);setCheck(value)})}}>{t(tools[value].title)}</button>)}
     </div>
     <div className="space-y-3 border-y border-line py-4">
      <h2 className="text-base font-semibold">{t(tool.title)}</h2>
      <AssessmentIntro purpose={tool.purpose} notice={tool.notice} extra="assessment.bounds"/>
      {check==='vpa'&&<p className="text-xs text-fg-muted">{t('vpa.timeNotice')}</p>}
      {!namespace&&<p role="status" className="text-sm text-warning">{t('assessment.chooseNamespace')}</p>}
      <div className="flex flex-wrap gap-2"><button className={button} disabled={!namespace||request.busy} onClick={()=>enabled?request.refresh():setEnabled(true)}>{request.busy?t('app.loading'):t(tool.action)}</button>{request.busy&&<button className={button} onClick={request.cancel}>{t('view.loadCancel')}</button>}</div>
     </div>
     {request.error!=null&&<p role="alert" className="text-sm text-danger">{errorDetail(request.error)}</p>}
     {request.cancelled&&<p role="status" className="text-sm text-fg-muted">{t('view.loadCancelled')}</p>}
     {snapshot&&<div className="space-y-4" data-assessment-results>
      <div className="flex flex-wrap items-baseline gap-x-4 gap-y-1"><h3 className="font-semibold">{t('assessment.resources',{count:snapshot.resources.length})}</h3><span className="text-xs text-fg-muted"><DateText value={new Date(snapshot.capturedAt).toISOString()}/></span></div>
      <div className="flex flex-wrap gap-x-6 gap-y-2 text-sm">{(Object.keys(totals) as (keyof AssessmentCounts)[]).map(key=><p key={key} className={key==='review'?'text-warning':''}>{t(`assessment.${key}`)}: <b className="tabular-nums">{totals[key]}</b></p>)}</div>
      <p className="text-xs text-fg-muted">{t('assessment.countsNotice')}</p>{snapshot.partial&&<p className="text-xs text-warning">{t('assessment.partial')}</p>}
      {!snapshot.resources.length&&<p>{t('assessment.empty')}</p>}
      <ul className="divide-y divide-line">{snapshot.resources.map(r=><li key={JSON.stringify(r.ref)} className="flex min-w-0 flex-wrap items-center justify-between gap-3 py-3">
       <div className="min-w-0 space-y-1"><p className="break-all font-medium">{r.ref.kind}/{r.ref.name}</p><p className="text-xs text-fg-muted">{state(r.state)}</p><p className="flex flex-wrap gap-x-3 gap-y-1 text-xs">{(['review','unknown'] as const).map(key=><span key={key} className={key==='review'&&r.counts[key]?'text-warning':''}>{t(`assessment.${key}`)}: {r.counts[key]}</span>)}</p>
        {!!r.highlights.length&&<ul className="mt-2 space-y-2">{r.highlights.map((h,i)=><li key={i} className="max-w-prose text-sm wrap-anywhere">
         <p>{h.container&&<span className="font-mono">{h.container} · </span>}<span className="font-medium">{h.code==='vpa'?`${h.field} · ${t('vpa.target')}`:checkField(h.field)}</span>: {checkValue(h.value,h.code)}</p>
         {h.code==='vpa'&&<p className="text-xs text-fg-muted">{vpaState(h.sourceState??'unknown')} · {t('vpa.freshness')}: {vpaState(h.freshness??'unknown')}</p>}
         {h.code&&h.code!=='vpa'&&<p className="text-xs leading-relaxed text-fg-muted">{explanation(h.code)}</p>}
        </li>)}</ul>}
        {!!r.highlights.length&&<p className="mt-2 text-xs text-fg-muted">{t('assessment.preview')}</p>}
       </div>
       <button className={button+' shrink-0'} onClick={()=>open(r.ref)}>{t('assessment.open')}</button>
      </li>)}</ul>
      <details className="text-xs"><summary className="cursor-pointer">{t('diagnosis.coverage')}</summary><ul className="mt-2 space-y-1">{snapshot.coverage.map(c=><li key={c.kind} className="break-words"><code>{c.kind}</code> · {state(c.state)}</li>)}</ul></details>
     </div>}
    </div>
   </div>
   {subject&&<ResourceDrawer {...drawer} subject={subject} onClose={()=>setSubject(null)}/>}
  </div>
 </section>
}
function state(value:string){
 if(value==='complete')return t('diagnosis.ready')
 if(value==='partial')return t('diagnosis.partial')
 const vpa={ 'no-matches':'vpa.noMatches','not-discovered':'vpa.notDiscovered','unsupported-version':'vpa.unsupportedVersion',ambiguous:'vpa.ambiguous'} as const
 return value in vpa?t(vpa[value as keyof typeof vpa]):classLabel(value)
}
