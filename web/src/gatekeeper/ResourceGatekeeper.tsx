import {useCallback,useContext,useState} from 'react'
import type {Client} from '../api/client'
import type {Ref} from '../api/types'
import {ViewConnectionContext} from '../views/useView'
import {useSnapshotRequest} from '../views/useSnapshotRequest'
import {t} from '../i18n'
import {errorDetail} from '../errors'
import {DateText} from '../components/DateText'
const button='rounded border border-line px-2 py-1 text-xs hover:bg-hover disabled:opacity-40'
const identity=(r:Ref)=>JSON.stringify([r.provider,r.target,r.kind,r.scope??'',r.name,r.uid])
export function ResourceGatekeeper(props:{client:Client;subject:Ref;snapshotKey:unknown;revision?:string}){
 const connection=useContext(ViewConnectionContext)
 const [parent,setParent]=useState({snapshot:props.snapshotKey,client:props.client,epoch:0})
 const changed=parent.snapshot!==props.snapshotKey||parent.client!==props.client,epoch=parent.epoch+(changed?1:0)
 if(changed)setParent({snapshot:props.snapshotKey,client:props.client,epoch})
 return <Panel key={JSON.stringify([identity(props.subject),connection,epoch,props.revision])} {...props}/>
}
function Panel({client,subject}:{client:Client;subject:Ref}){
 const [enabled,setEnabled]=useState(false),refJSON=JSON.stringify(subject)
 const load=useCallback(async(signal:AbortSignal)=>{
  const ref:Ref=JSON.parse(refJSON),snapshot=await client.gatekeeperAudit(ref,signal)
  if(identity(snapshot.ref)!==identity(ref)||snapshot.apiVersion!=='constraints.gatekeeper.sh/v1beta1'||!snapshot.resourceVersion)throw new Error(t('workspace.replaced'))
  return snapshot
 },[client,refJSON])
 const request=useSnapshotRequest(load,identity(subject),{enabled})
 const snapshot=enabled&&!request.busy&&!request.cancelled&&!request.error?request.value:null
 return <section data-gatekeeper-audit aria-label={t('gatekeeper.title')} className="min-w-0 space-y-2 wrap-anywhere">
  <h3 className="text-sm font-semibold">{t('gatekeeper.title')}</h3>
  <p className="text-xs text-fg-muted">{t('gatekeeper.notice')}</p>
  <button type="button" className={button} disabled={request.busy} onClick={()=>enabled?request.refresh():setEnabled(true)}>{request.busy?t('app.loading'):t('gatekeeper.load')}</button>
  {request.busy&&<button type="button" className={button+' ml-2'} onClick={request.cancel}>{t('view.loadCancel')}</button>}
  {request.error!=null&&<p role="alert" className="text-xs text-danger">{errorDetail(request.error)}</p>}
  {request.cancelled&&<p className="text-xs text-fg-muted">{t('view.loadCancelled')}</p>}
  {snapshot&&<div className="min-w-0 space-y-3 text-xs">
   <p className="break-all"><code>{snapshot.apiVersion}</code> · UID <code>{snapshot.ref.uid}</code> · RV <code>{snapshot.resourceVersion}</code> · <DateText value={new Date(snapshot.capturedAt).toISOString()}/></p>
   <p><code>status.auditTimestamp</code>: {snapshot.auditTimeState==='reported'&&snapshot.auditTime?<DateText value={snapshot.auditTime}/>:t(snapshot.auditTimeState==='invalid'?'certificate.invalid':'certificate.missing')}</p>
   <p>{t('gatekeeper.counts',{total:snapshot.totalViolations??t('security.unknown'),returned:snapshot.returnedViolations,shown:snapshot.violations.length})}</p>
   <p>{snapshot.coverage==='producer-capped'?t('gatekeeper.capped'):snapshot.coverage==='inconsistent'?t('gatekeeper.inconsistent'):snapshot.coverage==='reported'?t('gatekeeper.reported'):t('security.unknown')}</p>
   {snapshot.partial&&<p className="text-warning">{t('diagnosis.partial')}</p>}
   <p className="text-fg-muted">{t('gatekeeper.identityNotice')}</p>
   {snapshot.violations.map((v,i)=><article key={i} className="min-w-0 space-y-1 border-t border-line pt-2" data-gatekeeper-violation>
    <p className="break-all font-mono">{[v.group,v.version,v.kind,v.namespace,v.name].filter(Boolean).join(' · ')}</p>
    <p><code>enforcementAction</code>: {v.enforcementAction||t('security.unknown')}</p>
    {!!v.enforcementActions.length&&<p><code>enforcementActions</code>: {v.enforcementActions.join(', ')}</p>}
    <p className="whitespace-pre-wrap break-words">{v.message||t('security.unknown')}</p>
   </article>)}
  </div>}
 </section>
}
