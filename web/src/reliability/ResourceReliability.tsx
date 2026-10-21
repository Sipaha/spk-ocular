import {useCallback,useContext,useState} from 'react'
import type {Client} from '../api/client'
import type {Ref} from '../api/types'
import {ViewConnectionContext} from '../views/useView'
import {useSnapshotRequest} from '../views/useSnapshotRequest'
import {t,type MessageKey} from '../i18n'
import {errorDetail} from '../errors'
import {DateText} from '../components/DateText'
const button='rounded border border-line px-2 py-1 text-xs hover:bg-hover disabled:opacity-40'
const identity=(r:Ref)=>JSON.stringify([r.provider,r.target,r.kind,r.scope??'',r.name,r.uid])
export function ResourceReliability(props:{client:Client;subject:Ref;snapshotKey:unknown;revision?:string;onGo:(ref:Ref)=>void}){
 const connection=useContext(ViewConnectionContext)
 const [parent,setParent]=useState({snapshot:props.snapshotKey,epoch:0})
 const epoch=parent.snapshot===props.snapshotKey?parent.epoch:parent.epoch+1
 if(parent.snapshot!==props.snapshotKey)setParent({snapshot:props.snapshotKey,epoch})
 return <Panel key={JSON.stringify([identity(props.subject),connection,epoch,props.revision])} {...props}/>
}
function Panel({client,subject,onGo}:{client:Client;subject:Ref;onGo:(ref:Ref)=>void}){
 const [enabled,setEnabled]=useState(false),refJSON=JSON.stringify(subject)
 const load=useCallback(async(signal:AbortSignal)=>{
  const ref:Ref=JSON.parse(refJSON),snapshot=await client.resourceReliability(ref,signal)
  if(identity(snapshot.ref)!==identity(ref)||!snapshot.resourceVersion)throw new Error(t('workspace.replaced'))
  return snapshot
 },[client,refJSON])
 const request=useSnapshotRequest(load,identity(subject),{enabled})
 // Retained values are never presented as the current assessment after refresh.
 const snapshot=!request.busy&&!request.cancelled&&!request.error?request.value:null
 return <section data-resource-reliability aria-label={t('reliability.title')} className="min-w-0 space-y-2 wrap-anywhere">
  <h3 className="text-sm font-semibold">{t('reliability.title')}</h3>
  <p className="text-xs text-fg-muted">{t('reliability.notice')}</p>
  <button type="button" className={button} disabled={request.busy} onClick={()=>enabled?request.refresh():setEnabled(true)}>{request.busy?t('app.loading'):t('reliability.load')}</button>
  {request.busy&&<button type="button" className={button+' ml-2'} onClick={request.cancel}>{t('view.loadCancel')}</button>}
  {request.error!=null&&<p role="alert" className="text-xs text-danger">{errorDetail(request.error)}</p>}
  {request.cancelled&&<p className="text-xs text-fg-muted">{t('view.loadCancelled')}</p>}
  {snapshot&&<div className="min-w-0 space-y-3 text-xs">
   <p className="text-fg-muted"><DateText value={new Date(snapshot.capturedAt).toISOString()}/> · resourceVersion <code>{snapshot.resourceVersion}</code></p>
   {snapshot.partial&&<p className="text-warning">{t('diagnosis.partial')}</p>}
   <p>{t('reliability.coverage')}: PDB — {coverage(snapshot.pdbState)}; HPA — {coverage(snapshot.hpaState)}</p>
   {!snapshot.observations.length&&<p>{t('reliability.unknown')}</p>}
   {snapshot.observations.map((o,i)=>{
    const local=o.source.uid&&o.source.provider===subject.provider&&o.source.target===subject.target&&o.source.scope===subject.scope
    const related=identity(o.source)!==identity(subject)
    return <article key={i} className="min-w-0 space-y-1 border-t border-line pt-2" data-reliability-rule={o.rule}>
     <p className={o.outcome==='review'?'font-semibold text-warning':'font-semibold'}>{t(outcomes[o.outcome]??'reliability.unknown')} · {t(codes[o.code]??'reliability.unknown')}</p>
     <p className="break-all font-mono">{o.field}{o.container&&<> · {o.container}</>}{o.value&&<>: {o.value}</>}</p>
     {related&&<p>{local?<button type="button" className={button+' max-w-full break-all text-left'} onClick={()=>onGo(o.source)}>{o.source.kind}/{o.source.name}</button>:<span>{o.source.kind}/{o.source.name}</span>}</p>}
     <p className="break-all text-fg-muted"><code>{o.source.uid}</code> · resourceVersion <code>{o.resourceVersion}</code></p>
     <p className="break-all text-fg-muted"><code>{o.rule}@{o.version}</code></p>
     <a href={o.reference} target="_blank" rel="noreferrer" className="text-accent underline">{t('reliability.ruleReference')}</a>
    </article>
   })}
  </div>}
 </section>
}
const outcomes:Record<string,MessageKey>={observed:'reliability.observed',review:'reliability.review','not-applicable':'reliability.excluded',unknown:'reliability.unknown'}
const codes:Record<string,MessageKey>={invalid:'reliability.invalid',probe:'reliability.probe',probeExcluded:'reliability.probeExcluded',resource:'reliability.resource',resourceMissing:'reliability.resourceMissing',cpuPolicy:'reliability.cpuPolicy',allocationCaveat:'reliability.allocationCaveat',placement:'reliability.placement',replicas:'reliability.replicas',rollout:'reliability.rollout',recreate:'reliability.recreate',pdbCandidate:'reliability.pdbCandidate',pdbSelected:'reliability.pdbSelected',pdbStatus:'reliability.pdbStatus',pdbNone:'reliability.pdbNone',pdbMultiple:'reliability.pdbMultiple',hpaAssociation:'reliability.hpaAssociation',hpaStatus:'reliability.hpaStatus',hpaCPU:'reliability.hpaCPU'}
function coverage(s:string){return s==='complete'?t('reliability.complete'):s==='not-applicable'?t('reliability.excluded'):t('reliability.unknown')}
