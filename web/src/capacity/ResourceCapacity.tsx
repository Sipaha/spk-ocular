import {useCallback,useContext,useState} from 'react'
import type {Client} from '../api/client'
import type {Ref} from '../api/types'
import {ViewConnectionContext} from '../views/useView'
import {useSnapshotRequest} from '../views/useSnapshotRequest'
import {t} from '../i18n'
import {DateText} from '../components/DateText'
import {errorDetail} from '../errors'
import type {CapacityQuantity} from './types'
const button='rounded border border-line px-2 py-1 text-xs hover:bg-hover disabled:opacity-40'
const identity=(r:Ref)=>JSON.stringify([r.provider,r.target,r.kind,r.scope??'',r.name,r.uid])
export function ResourceCapacity(props:{client:Client;subject:Ref;snapshotKey:unknown;revision?:string;onGo:(ref:Ref)=>void}){
 const connection=useContext(ViewConnectionContext)
 const [parent,setParent]=useState({snapshot:props.snapshotKey,client:props.client,epoch:0})
 const same=parent.snapshot===props.snapshotKey&&parent.client===props.client
 const epoch=same?parent.epoch:parent.epoch+1
 if(!same)setParent({snapshot:props.snapshotKey,client:props.client,epoch})
 return <Panel key={JSON.stringify([identity(props.subject),connection,epoch,props.revision])} {...props}/>
}
function Panel({client,subject,onGo}:{client:Client;subject:Ref;onGo:(ref:Ref)=>void}){
 const [namespace,setNamespace]=useState(''),[enabled,setEnabled]=useState(false),refJSON=JSON.stringify(subject)
 const valid=/^[a-z0-9](?:[-a-z0-9]*[a-z0-9])?$/.test(namespace)&&namespace.length<=63
 const load=useCallback(async(signal:AbortSignal)=>{
  const ref:Ref=JSON.parse(refJSON),snapshot=await client.nodeCapacity({ref,namespace},signal)
  if(identity(snapshot.ref)!==identity(ref)||snapshot.namespace!==namespace||!snapshot.resourceVersion)throw new Error(t('workspace.replaced'))
  return snapshot
 },[client,refJSON,namespace])
 const request=useSnapshotRequest(load,JSON.stringify([identity(subject),namespace]),{enabled})
 const snapshot=!request.busy&&!request.cancelled&&!request.error?request.value:null
 return <section data-node-capacity aria-label={t('capacity.title')} className="min-w-0 space-y-2 wrap-anywhere">
  <h3 className="text-sm font-semibold">{t('capacity.title')}</h3>
  <p className="text-xs text-fg-muted">{t('capacity.notice')}</p>
  <label className="flex min-w-0 flex-col gap-1 text-xs">{t('capacity.namespace')}<input className="w-full min-w-0 rounded border border-line bg-panel px-2 py-1" value={namespace} maxLength={63} autoComplete="off" spellCheck={false} onChange={e=>{setEnabled(false);setNamespace(e.target.value)}}/></label>
  <button type="button" className={button} disabled={!valid||request.busy} onClick={()=>enabled?request.refresh():setEnabled(true)}>{request.busy?t('app.loading'):t('capacity.load')}</button>
  {request.busy&&<button type="button" className={button+' ml-2'} onClick={request.cancel}>{t('view.loadCancel')}</button>}
  {request.error!=null&&<p role="alert" className="text-xs text-danger">{errorDetail(request.error)}</p>}
  {request.cancelled&&<p className="text-xs text-fg-muted">{t('view.loadCancelled')}</p>}
  {snapshot&&<div className="min-w-0 space-y-3 text-xs">
   <p className="text-fg-muted"><DateText value={new Date(snapshot.capturedAt).toISOString()}/> · resourceVersion <code>{snapshot.resourceVersion}</code></p>
   <p>{t('capacity.coverage')}: {snapshot.coverage==='complete'?t('capacity.complete'):t('capacity.incomplete')} <code>{snapshot.coverage}</code></p>
   <p>{t('capacity.excluded')}: {snapshot.excludedTerminal} · {t('capacity.unverified')}: {snapshot.unverified}</p>
   <p>{t('capacity.quantities')}</p>
   <dl className="grid grid-cols-[minmax(0,1fr)_auto_auto] gap-x-3 gap-y-1">
    <dt/><dd>CPU</dd><dd>RAM</dd>
    <dt>{t('capacity.allocatable')}</dt><dd><Quantity q={snapshot.allocatable.cpu}/></dd><dd><Quantity q={snapshot.allocatable.memory}/></dd>
    <dt>{t('capacity.requests')}</dt><dd><Quantity q={snapshot.requests.cpu}/></dd><dd><Quantity q={snapshot.requests.memory}/></dd>
   </dl>
   <p className="text-warning">{t('capacity.limits')}</p>
   <p>{t('capacity.pods')}: {snapshot.pods.length}</p>
   {snapshot.pods.map((p,i)=>{
    const local=p.ref.provider===subject.provider&&p.ref.target===subject.target&&p.ref.kind==='pods'&&p.ref.scope===namespace&&p.ref.uid
    return <article key={i} className="min-w-0 space-y-1 border-t border-line pt-2">
     {local?<button type="button" className={button+' max-w-full break-all text-left'} onClick={()=>onGo(p.ref)}>{p.ref.scope}/{p.ref.name}</button>:<p>{p.ref.scope}/{p.ref.name}</p>}
     <p><code>{p.ref.uid}</code> · resourceVersion <code>{p.resourceVersion}</code></p>
     <p>CPU: <Quantity q={p.requests.cpu}/> · RAM: <Quantity q={p.requests.memory}/></p>
    </article>
   })}
  </div>}
 </section>
}
function Quantity({q}:{q:CapacityQuantity}){return <span className="font-mono">{q.state==='declared'&&q.value!==undefined?q.value:q.state==='unspecified'?t('capacity.unspecified'):t('capacity.unknown')}</span>}
