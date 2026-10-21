import {useCallback,useContext,useState,useSyncExternalStore} from 'react'
import type {Client} from '../api/client'
import type {Ref,Resource,ScopeSel} from '../api/types'
import {ViewConnectionContext} from '../views/useView'
import {useSnapshotRequest} from '../views/useSnapshotRequest'
import {t} from '../i18n'
import {errorDetail} from '../errors'
import {evidenceKey,readEvidence,subscribeEvidence} from './evidence'
import {managerMatches} from './model'
export function FluxManagedBy({client,resource,scope,onGo}:{client:Client;resource:Resource;scope?:ScopeSel;onGo:(ref:Ref)=>void}){
 const connection=useContext(ViewConnectionContext),selection=scope??(resource.ref.scope?{mode:'one' as const,name:resource.ref.scope}:{mode:'some' as const,names:[]})
 return <Lookup key={JSON.stringify([resource.ref,resource.fluxTracking,connection,selection])} client={client} resource={resource} scope={selection} connection={connection} onGo={onGo}/>
}
function Lookup({client,resource,scope,onGo,connection}:{client:Client;resource:Resource;scope:ScopeSel;connection:string|number;onGo:(ref:Ref)=>void}){
 const[enabled,setEnabled]=useState(false),scopeKey=JSON.stringify(scope)
 const load=useCallback((signal:AbortSignal)=>client.fluxCD({provider:resource.ref.provider,target:resource.ref.target,scope:JSON.parse(scopeKey)},signal),[client,resource.ref.provider,resource.ref.target,scopeKey])
 const request=useSnapshotRequest(load,JSON.stringify([resource.ref,scopeKey]),{enabled})
 const cacheKey=evidenceKey(resource.ref.provider,resource.ref.target,connection,scope)
 const cached=useSyncExternalStore(subscribeEvidence,()=>readEvidence(client,cacheKey)),snapshot=request.value??cached
 const matches=managerMatches(resource,snapshot?.objects??[])
 return <section aria-label={t('flux.tracking')}><h3 className="mb-1.5 text-sm font-semibold">{t('flux.tracking')}</h3><p className="mb-2 text-xs text-fg-muted">{t('flux.managerNotice')}</p><button className="rounded border border-line px-2 py-1 text-xs hover:bg-hover disabled:opacity-40" disabled={request.busy} onClick={()=>enabled?request.refresh():setEnabled(true)}>{request.busy?t('app.loading'):t('flux.resolveManager')}</button>{request.busy&&<button className="ml-2 text-xs" onClick={request.cancel}>{t('view.loadCancel')}</button>}{request.error!=null&&<p role="alert" className="mt-2 text-xs text-danger">{errorDetail(request.error)}</p>}{request.cancelled&&<p className="mt-2 text-xs">{t('view.loadCancelled')}</p>}{snapshot&&!request.cancelled&&<div className="mt-2 text-xs">{matches.length===1?<button className="text-left text-accent hover:underline" onClick={()=>onGo(matches[0].ref)}>{matches[0].kind} / {matches[0].ref.scope}/{matches[0].ref.name}</button>:<p className="text-fg-muted">{t('flux.unverified')}</p>}{(snapshot.truncated||snapshot.discovery!=='ready'||snapshot.coverage.some(c=>c.state!=='ready'))&&<p className="text-warning">{t('argo.partial')}</p>}</div>}</section>
}
