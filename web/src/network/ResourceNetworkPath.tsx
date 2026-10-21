import type {ReactNode} from 'react'
import {useCallback,useContext,useState} from 'react'
import type {Client} from '../api/client'
import type {Ref} from '../api/types'
import type {NetworkCheck,NetworkRoute} from './types'
import {ViewConnectionContext} from '../views/useView'
import {useSnapshotRequest} from '../views/useSnapshotRequest'
import {t,classLabel} from '../i18n'
import type {MessageKey} from '../i18n'
import {errorDetail} from '../errors'
import {DateText} from '../components/DateText'
import {HealthDot} from '../components/ResourceTable'

const button='rounded border border-line px-2 py-1 text-xs hover:bg-hover disabled:opacity-40'
const identity=(r:Ref)=>JSON.stringify([r.provider,r.target,r.kind,r.scope??'',r.name,r.uid])
export function ResourceNetworkPath(props:{client:Client;subject:Ref;onGo:(ref:Ref)=>void}){
 const connection=useContext(ViewConnectionContext)
 return <Panel key={JSON.stringify([identity(props.subject),connection])} {...props}/>
}
function Panel({client,subject,onGo}:{client:Client;subject:Ref;onGo:(ref:Ref)=>void}){
 const [enabled,setEnabled]=useState(false),refJSON=JSON.stringify(subject)
 const load=useCallback(async(signal:AbortSignal)=>{
  const ref:Ref=JSON.parse(refJSON),snapshot=await client.resourceNetworkPath(ref,signal)
  if(identity(snapshot.ref)!==identity(ref))throw new Error(t('workspace.replaced'))
  return snapshot
 },[client,refJSON])
 const request=useSnapshotRequest(load,identity(subject),{enabled}),snapshot=request.value
 const link=(ref:Ref)=>ref.uid&&ref.provider===subject.provider&&ref.target===subject.target&&ref.scope===subject.scope?<button type="button" className={button+' max-w-full break-all text-left'} onClick={()=>onGo(ref)}>{ref.kind}/{ref.name}</button>:<span className="break-all font-mono">{ref.kind}/{ref.name||'—'}</span>
 return <section aria-label={t('network.title')} className="min-w-0 space-y-2 wrap-anywhere">
  <h3 className="text-sm font-semibold">{t('network.title')}</h3>
  <p className="text-xs text-fg-muted">{t('network.notice')}</p>
  <div className="flex gap-2"><button type="button" className={button} disabled={request.busy} onClick={()=>enabled?request.refresh():setEnabled(true)}>{request.busy?t('app.loading'):t('network.load')}</button>{request.busy&&<button type="button" className={button} onClick={request.cancel}>{t('view.loadCancel')}</button>}</div>
  {request.error!=null&&<p role="alert" className="text-xs text-danger">{errorDetail(request.error)}</p>}
  {request.cancelled&&<p className="text-xs text-fg-muted">{t('view.loadCancelled')}</p>}
  {snapshot&&!request.cancelled&&<div className="space-y-3 text-xs" aria-busy={request.busy}>
   <p className="text-fg-muted"><DateText value={new Date(snapshot.capturedAt).toISOString()}/></p>
   {(snapshot.truncated||snapshot.discovery!=='ready'||snapshot.coverage.some(c=>c.state!=='ready'&&c.state!=='absent'))&&<p className="text-warning">{t('diagnosis.partial')}</p>}
   {!snapshot.routes.length&&<p>{t('network.empty')}</p>}
   {snapshot.routes.map((route,i)=><article key={i} className="min-w-0 space-y-2 rounded border border-line p-2">
    <h4 className="break-all text-sm font-semibold">{route.route?(route.route.defaultBackend?'defaultBackend':`${route.route.host||'*'}${route.route.path} · ${route.route.pathType||'—'}`):route.service.name}</h4>
    <p className="break-all font-mono text-fg-muted">{route.field}</p>
    <div>{link(route.service)}</div>
    {route.mode&&<p>{modeLabel(route)}{route.mode==='headless'&&!route.selector&&` · ${t('network.selectorless')}`}{route.selector&&<code className="ml-2 break-all">{route.selector}</code>}{route.externalName&&<code className="ml-2 break-all">{route.externalName}</code>}</p>}
    {route.port>0&&<p className="break-all font-mono">{route.portName&&`${route.portName} · `}{route.port}/{route.protocol} → targetPort {route.targetPort}</p>}
    <Checks checks={route.checks} link={link}/>
    <h5 className="wrap-anywhere font-semibold">{t('network.endpoints')} · {route.endpoints.length}</h5>
    {route.endpoints.map((e,j)=><div key={j} className="min-w-0 space-y-1 border-l-2 border-line pl-2">
     <div>{link(e.slice)}</div><p className="break-all font-mono">{e.addresses.join(', ')||'—'}{e.port>0&&` : ${e.port}`} </p>
     <p className="wrap-anywhere font-mono">ready={condition(e.ready)} · serving={condition(e.serving)} · terminating={condition(e.terminating)}</p>
     <Checks checks={e.checks} link={link}/>
     {e.pod&&<div>{link(e.pod)}</div>}{e.owners.map((owner,k)=><div key={k}>{link(owner)}</div>)}
    </div>)}
   </article>)}
   <details><summary className="cursor-pointer font-semibold">{t('diagnosis.coverage')}</summary>
    <p className="mt-2">{t('network.discovery')}: {snapshot.discovery==='ready'?t('diagnosis.ready'):classLabel(snapshot.discovery)}</p>
    <ul className="mt-1 space-y-1">{snapshot.coverage.map((c,i)=><li key={i} className="break-all"><code>{c.source}</code> · {c.state==='ready'?t('diagnosis.ready'):c.state==='partial'?t('diagnosis.partial'):classLabel(c.state==='absent'?'not_found':c.state)}</li>)}</ul>
   </details>
  </div>}
 </section>
}
function condition(value:boolean|null){return value==null?'?':String(value)}
function modeLabel(route:NetworkRoute){switch(route.mode){case 'selectorless':return t('network.selectorless');case 'headless':return t('network.headless');case 'externalName':return 'ExternalName';default:return t('network.selector')}}
function Checks({checks,link}:{checks:NetworkCheck[];link:(r:Ref)=>ReactNode}){return <ul className="space-y-1">{checks.map((c,i)=><li key={i} className="wrap-anywhere"><HealthDot state={c.state}/>{t(checkLabels[c.code]??'network.backend_unknown')}<code className="mt-0.5 block break-all text-fg-muted">{c.field}</code>{c.ref&&link(c.ref)}</li>)}</ul>}

const checkLabels:Record<string,MessageKey>={selector_mismatch:'network.selector_mismatch',backend_unknown:'network.backend_unknown',backend_missing:'network.backend_missing',backend_unsupported:'network.backend_unsupported',external_name:'network.external_name',service_port_missing:'network.service_port_missing',protocol_unsupported:'network.protocol_unsupported',selector_empty:'network.selector_empty',named_port_missing:'network.named_port_missing',slice_owner_mismatch:'network.slice_owner_mismatch',slice_port_missing:'network.slice_port_missing',address_unsupported:'network.address_unsupported',readiness_unknown:'network.readiness_unknown',endpoint_not_ready:'network.endpoint_not_ready',publish_not_ready:'network.publish_not_ready',target_port_mismatch:'network.target_port_mismatch',pod_unverified:'network.pod_unverified',slices_empty:'network.slices_empty',endpoints_empty:'network.endpoints_empty'}
