import {useCallback,useContext,useState} from 'react'
import type {Client} from '../api/client'
import type {Ref} from '../api/types'
import type {VPAResources} from './types'
import {ViewConnectionContext} from '../views/useView'
import {useSnapshotRequest} from '../views/useSnapshotRequest'
import {t,type MessageKey} from '../i18n'
import {errorDetail} from '../errors'
import {DateText} from '../components/DateText'
const button='rounded border border-line px-2 py-1 text-xs hover:bg-hover disabled:opacity-40'
const identity=(r:Ref)=>JSON.stringify([r.provider,r.target,r.kind,r.scope??'',r.name,r.uid])
export function ResourceVPA(props:{client:Client;subject:Ref;snapshotKey:unknown;revision?:string;onGo:(ref:Ref)=>void}){
 const connection=useContext(ViewConnectionContext)
 const [parent,setParent]=useState({snapshot:props.snapshotKey,client:props.client,epoch:0})
 const changed=parent.snapshot!==props.snapshotKey||parent.client!==props.client,epoch=parent.epoch+(changed?1:0)
 if(changed)setParent({snapshot:props.snapshotKey,client:props.client,epoch})
 return <Panel key={JSON.stringify([identity(props.subject),connection,epoch,props.revision])} {...props}/>
}
function Panel({client,subject,onGo}:{client:Client;subject:Ref;onGo:(ref:Ref)=>void}){
 const [enabled,setEnabled]=useState(false),refJSON=JSON.stringify(subject)
 const load=useCallback(async(signal:AbortSignal)=>{
  const ref:Ref=JSON.parse(refJSON),snapshot=await client.resourceVPA(ref,signal)
  if(identity(snapshot.ref)!==identity(ref)||!snapshot.resourceVersion)throw new Error(t('workspace.replaced'))
  return snapshot
 },[client,refJSON])
 const request=useSnapshotRequest(load,identity(subject),{enabled})
 const snapshot=enabled&&!request.busy&&!request.cancelled&&!request.error?request.value:null
 const source=(ref:Ref,revision:string)=>{
  const local=ref.provider===subject.provider&&ref.target===subject.target&&ref.scope===subject.scope&&ref.uid
  return <div className="min-w-0 space-y-1"><p>{local?<button type="button" className={button+' max-w-full break-all text-left'} onClick={()=>onGo(ref)}>{ref.kind}/{ref.name}</button>:<span>{ref.kind}/{ref.name}</span>}</p><p className="break-all text-fg-muted"><code>{ref.uid}</code> · resourceVersion <code>{revision}</code></p></div>
 }
 return <section data-resource-vpa aria-label={t('vpa.title')} className="min-w-0 space-y-2 wrap-anywhere">
  <h3 className="text-sm font-semibold">{t('vpa.title')}</h3>
  <p className="text-xs text-fg-muted">{t('vpa.notice')}</p>
  <button type="button" className={button} disabled={request.busy} onClick={()=>enabled?request.refresh():setEnabled(true)}>{request.busy?t('app.loading'):t('vpa.load')}</button>
  {request.busy&&<button type="button" className={button+' ml-2'} onClick={request.cancel}>{t('view.loadCancel')}</button>}
  {request.error!=null&&<p role="alert" className="text-xs text-danger">{errorDetail(request.error)}</p>}
  {request.cancelled&&<p className="text-xs text-fg-muted">{t('view.loadCancelled')}</p>}
  {snapshot&&<div className="min-w-0 space-y-3 text-xs">
   <p className="break-all"><code>{snapshot.ref.uid}</code> · resourceVersion <code>{snapshot.resourceVersion}</code> · <DateText value={new Date(snapshot.capturedAt).toISOString()}/></p>
   <p data-vpa-coverage>{t('vpa.coverage')}: {state(snapshot.state)}</p>
   <p className="text-fg-muted">{t('vpa.identityNotice')}</p>
   <h4 className="font-semibold">{t('vpa.current')}</h4><p className="text-fg-muted">{t('vpa.currentNotice')}</p>
   {snapshot.declarations.map(d=><article key={d.container} className="min-w-0 border-t border-line pt-2"><p className="font-mono">{d.container}</p><Quantities rows={[[t('history.requests'),d.requests],[t('history.limits'),d.limits]]}/></article>)}
   {snapshot.sources.map(s=><article key={s.ref.uid} data-vpa-source className="min-w-0 space-y-2 border-t border-line pt-2">
    {source(s.ref,s.resourceVersion)}
    <p>{s.apiVersion} · {t('vpa.recommender')}: {s.recommender||t('vpa.unknown')} · {t('vpa.producerVersion')}: {t('vpa.unknown')}</p>
    <p>{t('vpa.updateMode')}: {s.updateMode==='unknown'?t('vpa.unknown'):s.updateMode}</p>
    <p>{state(s.state)} · {t('vpa.freshness')}: {state(s.freshness)}</p>
    {s.partial&&<p className="text-warning">{t('vpa.partial')}</p>}
    <p className="text-fg-muted">{t('vpa.timeNotice')}</p>
    {s.conditions.map(c=><p key={c.type} className="break-all"><code>{c.type}: {c.status}</code> · {state(c.freshness)}{c.transitionTime&&<> · {t('vpa.transition')}: <DateText value={c.transitionTime}/></>}</p>)}
    {s.recommendations.map(r=><div key={r.container} data-vpa-recommendation className="min-w-0 space-y-1"><h5 className="font-mono font-semibold">{r.container}</h5><Quantities rows={[["target",r.target],["lowerBound",r.lowerBound],["upperBound",r.upperBound],["uncappedTarget",r.uncappedTarget]]}/></div>)}
    <h5 className="font-semibold">{t('vpa.policy')}</h5><p className="text-fg-muted">{t('vpa.policyNotice')}</p>
    {s.policies.length===0&&<p>{t('vpa.unknown')}</p>}
    {s.policies.map((p,i)=><div key={i} className="min-w-0 space-y-1"><p className="font-mono">{p.container}</p><p>mode: {p.mode==='unknown'?t('vpa.unknown'):p.mode} · controlledValues: {p.controlledValues==='unknown'?t('vpa.unknown'):p.controlledValues}</p><p>controlledResources: {p.controlledResourcesKnown?(p.controlledResources.join(', ')||'∅'):t('vpa.unknown')}</p><Quantities rows={[["minAllowed",p.minAllowed],["maxAllowed",p.maxAllowed]]}/></div>)}
   </article>)}
   <h4 className="font-semibold">{t('vpa.hpa')}</h4><p>{t('vpa.coverage')}: {state(snapshot.hpaState)}</p><p className="text-fg-muted">{t('vpa.hpaNotice')}</p>
   {snapshot.hpaMetrics.map((h,i)=><article key={i} className="min-w-0 space-y-1 border-t border-line pt-2 text-warning">{source(h.ref,h.resourceVersion)}<p>{h.resource} · {h.container||'Resource'} · {h.targetType}</p></article>)}
   <p className="text-fg-muted">{t('vpa.missingNotice')}</p>
  </div>}
 </section>
}
function Quantities({rows}:{rows:[string,VPAResources][]}){
 return <dl className="min-w-0 space-y-1">{rows.map(([name,value])=><div key={name} className="grid min-w-0 grid-cols-[repeat(auto-fit,minmax(min(100%,10rem),1fr))] gap-x-2 gap-y-1"><dt className="min-w-0 wrap-anywhere font-mono">{name}</dt><dd className="flex min-w-0 flex-wrap gap-x-2"><span className="min-w-0 break-all">CPU: <code>{value.cpu??'—'}</code></span><span className="min-w-0 break-all">RAM: <code>{value.memory??'—'}</code></span></dd></div>)}</dl>
}
const states:Record<string,MessageKey>={complete:'vpa.complete','no-matches':'vpa.noMatches','not-discovered':'vpa.notDiscovered','unsupported-version':'vpa.unsupportedVersion',forbidden:'vpa.denied',not_found:'vpa.notDiscovered',scope_limit:'vpa.partial',partial:'vpa.partial',ambiguous:'vpa.ambiguous',unknown:'vpa.unknown','not-checked':'vpa.notChecked',reported:'vpa.reported','no-recommendation':'vpa.noRecommendation','low-confidence':'vpa.lowConfidence','fetching-history':'vpa.fetchingHistory','no-pods':'vpa.noPods','config-unsupported':'vpa.configUnsupported',invalid:'vpa.invalid',stale:'vpa.stale',current:'vpa.currentGeneration'}
function state(value:string){return t(states[value]??'vpa.unknown')}
