import {useCallback,useContext,useState,useSyncExternalStore} from 'react'
import type {Client} from '../api/client'
import type {Ref,Resource,ScopeSel} from '../api/types'
import {ViewConnectionContext} from '../views/useView'
import {useSnapshotRequest} from '../views/useSnapshotRequest'
import {t} from '../i18n'
import {errorDetail} from '../errors'
import {evidenceKey,readEvidence,subscribeEvidence} from './evidence'
import {managerMatches} from './model'

/** Resolve only within the current selection. Opening Properties never adds
 * an inventory read; the operator explicitly requests this controller lookup. */
export function ArgoManagedBy({client,resource,scope,onGo}:{client:Client;resource:Resource;scope?:ScopeSel;onGo:(ref:Ref)=>void}){
 const connection=useContext(ViewConnectionContext)
 const selection=scope??(resource.ref.scope?{mode:'one' as const,name:resource.ref.scope}:{mode:'some' as const,names:[]})
 return <ManagerLookup key={JSON.stringify([resource.ref,resource.argoTracking,connection,selection])} client={client} resource={resource} scope={selection} connection={connection} onGo={onGo}/>
}
function ManagerLookup({client,resource,scope,onGo,connection}:{client:Client;resource:Resource;scope:ScopeSel;connection:string|number;onGo:(ref:Ref)=>void}){
 const [enabled,setEnabled]=useState(false),scopeKey=JSON.stringify(scope)
 const load=useCallback((signal:AbortSignal)=>client.argoCD({provider:resource.ref.provider,target:resource.ref.target,scope:JSON.parse(scopeKey)},signal),[client,resource.ref.provider,resource.ref.target,scopeKey])
 const request=useSnapshotRequest(load,JSON.stringify([resource.ref,scopeKey]),{enabled})
 const cacheKey=evidenceKey(resource.ref.provider,resource.ref.target,connection,scope)
 const cached=useSyncExternalStore(subscribeEvidence,()=>readEvidence(client,cacheKey))
 const snapshot=request.value??cached
 const matches=managerMatches(resource,snapshot?.objects??[])
 const incomplete=snapshot&&(snapshot.truncated||snapshot.discovery!=='ready'||snapshot.coverage.some(c=>!['ready','empty'].includes(c.state)))
 return <section aria-label={t(matches.length===1?'argo.managedBy':'argo.tracking')}>
  <h3 className="mb-1.5 text-sm font-semibold">{t(matches.length===1?'argo.managedBy':'argo.tracking')}</h3>
  <p className="mb-2 text-xs text-fg-muted">{t('argo.managerNotice')}</p>
  <button className="rounded border border-line px-2 py-1 text-xs hover:bg-hover disabled:opacity-40" disabled={request.busy} onClick={()=>enabled?request.refresh():setEnabled(true)}>{request.busy?t('app.loading'):t('argo.resolveManager')}</button>
  {request.busy&&<button className="ml-2 rounded border border-line px-2 py-1 text-xs" onClick={request.cancel}>{t('view.loadCancel')}</button>}
  {request.cancelled&&<p className="mt-2 text-xs text-fg-muted">{t('view.loadCancelled')}</p>}
  {request.error!=null&&<p role="alert" className="mt-2 text-xs text-danger">{errorDetail(request.error)}</p>}
  {snapshot&&!request.cancelled&&<div className="mt-2 text-xs">{matches.length===1?<button className="break-all text-left text-accent hover:underline" onClick={()=>onGo(matches[0].ref)}>Application / {matches[0].ref.scope}/{matches[0].ref.name}</button>:<p className="text-fg-muted">{t(matches.length>1?'argo.ambiguousManager':'argo.unverifiedManager')}</p>}{incomplete&&<p className="mt-1 text-warning">{t('argo.partial')}</p>}</div>}
 </section>
}
