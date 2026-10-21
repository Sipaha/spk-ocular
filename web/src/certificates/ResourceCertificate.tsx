import {useCallback,useContext,useState} from 'react'
import type {Client} from '../api/client'
import type {Ref} from '../api/types'
import type {CertificateTime} from './types'
import {ViewConnectionContext} from '../views/useView'
import {useSnapshotRequest} from '../views/useSnapshotRequest'
import {t,classLabel,type MessageKey} from '../i18n'
import {errorDetail} from '../errors'
import {DateText} from '../components/DateText'
const button='rounded border border-line px-2 py-1 text-xs hover:bg-hover disabled:opacity-40'
const identity=(r:Ref)=>JSON.stringify([r.provider,r.target,r.kind,r.scope??'',r.name,r.uid])
export function ResourceCertificate(props:{client:Client;subject:Ref;onGo:(ref:Ref)=>void}){
 const connection=useContext(ViewConnectionContext)
 return <Panel key={JSON.stringify([identity(props.subject),connection])} {...props}/>
}
function Panel({client,subject,onGo}:{client:Client;subject:Ref;onGo:(ref:Ref)=>void}){
 const [enabled,setEnabled]=useState(false),refJSON=JSON.stringify(subject)
 const load=useCallback(async(signal:AbortSignal)=>{
  const ref:Ref=JSON.parse(refJSON),snapshot=await client.certificateStatus(ref,signal)
  if(identity(snapshot.ref)!==identity(ref))throw new Error(t('workspace.replaced'))
  return snapshot
 },[client,refJSON])
 const request=useSnapshotRequest(load,identity(subject),{enabled}),snapshot=request.value
 const link=(ref:Ref,issuer=false)=>{
  const local=ref.uid&&ref.provider===subject.provider&&ref.target===subject.target&&(ref.scope===subject.scope||(issuer&&ref.kind==='cert-manager.io/clusterissuers'&&!ref.scope))
  return local?<button type="button" className={button+' max-w-full break-all text-left'} onClick={()=>onGo(ref)}>{ref.kind}/{ref.name}</button>:<span className="break-all font-mono">{ref.kind}/{ref.name}</span>
 }
 const time=(v:CertificateTime)=>v.state==='reported'&&v.value?<DateText value={v.value}/>:t(v.state==='invalid'?'certificate.invalid':'certificate.missing')
 return <section data-certificate-status aria-label={t('certificate.title')} className="min-w-0 space-y-2 wrap-anywhere">
  <h3 className="text-sm font-semibold">{t('certificate.title')}</h3>
  <p className="text-xs text-fg-muted">{t('certificate.notice')}</p>
  <button type="button" className={button} disabled={request.busy} onClick={()=>enabled?request.refresh():setEnabled(true)}>{request.busy?t('app.loading'):t('certificate.load')}</button>
  {request.busy&&<button type="button" className={button+' ml-2'} onClick={request.cancel}>{t('view.loadCancel')}</button>}
  {request.error!=null&&<p role="alert" className="text-xs text-danger">{errorDetail(request.error)}</p>}
  {request.cancelled&&<p className="text-xs text-fg-muted">{t('view.loadCancelled')}</p>}
  {snapshot&&!request.cancelled&&<div className="min-w-0 space-y-2 text-xs" aria-busy={request.busy}>
   <p className="text-fg-muted">{t('certificate.captured')}: <DateText value={new Date(snapshot.capturedAt).toISOString()}/></p>
   {snapshot.truncated&&<p className="text-warning">{t('diagnosis.partial')}</p>}
   <p className={snapshot.validity==='expired'||snapshot.validity==='within_7_days'?'text-danger':'text-fg'}>{t(validityLabels[snapshot.validity]??'certificate.unknown')}</p>
   <p className="text-fg-muted">{t('certificate.thresholds')}</p>
   <dl className="min-w-0 space-y-2">
    <div className="flex flex-wrap gap-x-3 gap-y-1"><dt className="max-w-full basis-40 text-fg-muted">status.notBefore</dt><dd className="min-w-0 max-w-full basis-56 grow">{time(snapshot.notBefore)}</dd></div>
    <div className="flex flex-wrap gap-x-3 gap-y-1"><dt className="max-w-full basis-40 text-fg-muted">status.notAfter</dt><dd className="min-w-0 max-w-full basis-56 grow">{time(snapshot.notAfter)}</dd></div>
    <div className="flex flex-wrap gap-x-3 gap-y-1"><dt className="max-w-full basis-40 text-fg-muted">status.renewalTime</dt><dd className="min-w-0 max-w-full basis-56 grow">{time(snapshot.renewal)}{snapshot.renewal.state==='reported'&&snapshot.renewal.value&&Date.parse(snapshot.renewal.value)<=snapshot.capturedAt&&<span className="block text-warning">{t('certificate.renewalDue')}</span>}</dd></div>
    <div className="flex flex-wrap gap-x-3 gap-y-1"><dt className="max-w-full basis-40 text-fg-muted">spec.secretName</dt><dd className="break-all font-mono">{snapshot.secretName||t('certificate.missing')}</dd></div>
   </dl>
   <h4 className="font-semibold">{t('certificate.conditions')}</h4>
   {!snapshot.conditions.length&&<p>{t('certificate.missing')}</p>}
   {snapshot.conditions.map(c=><p key={c.type} className="break-all"><code>{c.type}={c.status}</code> · {t(c.freshness==='current'?'certificate.current':c.freshness==='stale'?'certificate.stale':'certificate.freshnessUnknown')}{c.reason&&<code className="block text-fg-muted">{c.reason}</code>}</p>)}
   <h4 className="font-semibold">{t('certificate.requested')}</h4>
   {!snapshot.requestedNames.length&&<p>{t('certificate.missing')}</p>}
   <ul className="space-y-1">{snapshot.requestedNames.map((n,i)=><li key={i} className="break-all"><code>{n.type}: {n.value}</code></li>)}</ul>
   <h4 className="font-semibold">{t('certificate.issuer')}</h4>
   <p>{stateLabel(snapshot.issuerState)}</p>{snapshot.issuer&&link(snapshot.issuer,true)}
   <h4 className="font-semibold">{t('certificate.consumers')}</h4>
   <p>{stateLabel(snapshot.consumerState)}</p>
   <p className="text-fg-muted">{t('certificate.consumerNotice')}</p>
   {snapshot.consumerState==='ready'&&!snapshot.consumers.length&&<p>{t('certificate.noConsumers')}</p>}
   <ul className="space-y-1">{snapshot.consumers.map((r,i)=><li key={i}>{link(r)}</li>)}</ul>
  </div>}
 </section>
}
function stateLabel(state:string){return state==='ready'?t('diagnosis.ready'):state==='invalid'?t('certificate.invalid'):classLabel(state)}
const validityLabels:Record<string,MessageKey>={valid:'certificate.valid',expired:'certificate.expired',not_yet_valid:'certificate.notYet',within_7_days:'certificate.seven',within_30_days:'certificate.thirty',missing:'certificate.missing',invalid:'certificate.invalid',unknown:'certificate.unknown'}
