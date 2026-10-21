import {useContext,useSyncExternalStore} from 'react'
import type {Client} from '../api/client'
import type {Ref,Row,ScopeSel} from '../api/types'
import {ViewConnectionContext} from '../views/useView'
import {t} from '../i18n'
import {evidenceKey,readEvidence,subscribeEvidence} from './evidence'
import {managerMatches} from './model'
/** This cell never loads inventory. A controller page or explicit properties
 * lookup must have supplied evidence in this exact connection and scope. */
export function FluxWorkloadStatus({client,resource,scope,onGo}:{client:Client;resource:Row;scope:ScopeSel;onGo:(ref:Ref)=>void}){
 const connection=useContext(ViewConnectionContext),key=evidenceKey(resource.ref.provider,resource.ref.target,connection,scope)
 const snapshot=useSyncExternalStore(subscribeEvidence,()=>readEvidence(client,key))
 const matches=managerMatches(resource,snapshot?.objects??[])
 if(!resource.fluxTracking)return <span>—</span>
 if(matches.length!==1)return <span title={t('flux.unverified')}>{t('argo.unknown')}</span>
 const o=matches[0],state=o.state==='Ready'?t('flux.ready'):o.state==='Failed'?t('flux.failed'):o.state==='Suspended'?t('flux.suspended'):o.state==='Reconciling'?t('flux.reconciling'):t('argo.unknown')
 return <button className="text-left text-accent hover:underline" onClick={e=>{e.stopPropagation();onGo(o.ref)}}>{state} · {o.ref.name}</button>
}
