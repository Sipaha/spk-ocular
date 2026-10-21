import {useCallback,useContext,useState} from 'react'
import type {Client} from '../api/client'
import type {Ref,Target} from '../api/types'
import type {HelmDetail,HelmRequest,HelmResponse} from './types'
import type {Diagnosis} from '../diagnosis/types'
import {ViewConnectionContext} from '../views/useView'
import {useSnapshotRequest} from '../views/useSnapshotRequest'
import {Select} from '../components/Select'
import {EditDiff} from '../edit/EditDiff'
import {DateText} from '../components/DateText'
import {t} from '../i18n'

const button='rounded border border-line px-2 py-1 text-xs hover:bg-hover disabled:opacity-40'
type Props={client:Client;target:Target;detail:HelmDetail;configRev:string;readOwner:string;onResource?:(ref:Ref)=>void}
export function HelmDiagnosisPanel(props:Props){
 const connection=useContext(ViewConnectionContext)
 return <Panel key={JSON.stringify([props.target.provider,props.target.id,props.detail.storageNamespace,props.detail.namespace,props.detail.name,props.detail.revision,props.configRev,props.readOwner,connection])} {...props}/>
}
function Panel({client,target,detail,configRev,readOwner,onResource}:Props){
 const [enabled,setEnabled]=useState(false)
 const base=JSON.stringify({provider:target.provider,target:target.id,namespace:detail.storageNamespace,name:detail.name,revision:detail.revision,configRev,readOwner})
 const load=useCallback((signal:AbortSignal)=>client.helm({...JSON.parse(base),command:'diagnose'},signal),[client,base])
 const request=useSnapshotRequest(load,base,{enabled})
 const snapshot=request.value?.diagnosis
 return <section data-helm-diagnosis className="min-h-0 flex-1 space-y-3 overflow-auto p-3 text-xs" aria-label={t('helm.diagnosis')}>
  <p className="text-fg-muted">{t('helm.diagnosisNotice')}</p>
  <p>{t('helm.storageNamespace')}: <code>{detail.storageNamespace||'—'}</code> · {t('helm.namespace')}: <code>{detail.namespace}</code></p>
  {!detail.storageNamespace?<p className="text-warning">{t('helm.explicitStorage')}</p>:<>
   {detail.storageNamespace!==detail.namespace&&<p className="text-warning">{t('helm.crossStorageReadOnly')}</p>}
   <button className={button} disabled={request.busy} onClick={()=>enabled?request.refresh():setEnabled(true)}>{t('helm.loadDiagnosis')}</button>
   <ReadStatus request={request}/>
   {snapshot&&!request.cancelled&&!request.error&&<div key={snapshot.capturedAt} className="space-y-3" inert={request.busy}>
    <p><DateText value={snapshot.capturedAt}/></p>
    {snapshot.truncated&&<p className="text-warning">{t('diagnosis.partial')}</p>}
    <p><Time value={snapshot.selected.updated}/></p>
    <p>#{snapshot.selected.revision} · {snapshot.selected.chart} {snapshot.selected.chartVersion} · <b>{snapshot.selected.status}</b></p>
    {snapshot.selected.status.startsWith('pending-')&&<p className="text-warning">{t('helm.pendingUnknown')}</p>}
    {snapshot.selected.rollbackRevision? <p>{t('helm.recordedRollback')}: #{snapshot.selected.rollbackRevision}</p>:null}
    <details><summary>{t('helm.history')}</summary><ul>{snapshot.history.map(r=><li key={r.revision}>#{r.revision} · {r.status} · {r.chartVersion} · <Time value={r.updated}/>{r.rollbackRevision?` → #${r.rollbackRevision}`:''}</li>)}</ul></details>
    <p className="text-fg-muted">{t('helm.historyUnknown')}</p>
    <h3 className="font-semibold">{t('helm.recordedHooks')}</h3>
    <p className="text-fg-muted">{t('helm.hookAssociationUnknown')}</p>
    {!snapshot.hooks.length&&<p>—</p>}
    {snapshot.hooks.map(h=><div key={h.index} className="space-y-2 rounded border border-line p-2">
     <p className="break-all"><b>{h.kind}/{h.namespace}/{h.name}</b> · {h.apiVersion} · {h.phase||'—'}</p>
     <p><Time value={h.startedAt}/> → <Time value={h.completedAt}/></p>
     <p>{h.events.join(', ')||'—'} · {h.deletePolicies.join(', ')||'—'}</p>
     {h.coverage==='supported'?<ExplicitRead client={client} base={base} command={{command:'observe-hook',hookIndex:h.index}} label={t('helm.observeHook')}>{r=>r.currentHook&&<CurrentEvidence snapshot={r.currentHook} onResource={onResource}/>}</ExplicitRead>:<p className="text-warning">{t('diagnosis.notCovered')}</p>}
    </div>)}
    <Comparison client={client} base={base} revisions={snapshot.history.map(r=>r.revision)} initialFrom={snapshot.compareFrom} initialTo={snapshot.compareTo}/>
    <Flux client={client} base={base} onResource={onResource}/>
   </div>}
  </>}
 </section>
}
function Time({value}:{value:string}){return value&&!value.startsWith('0001-')?<DateText value={value}/>:<>—</>}
function ReadStatus({request}:{request:{busy:boolean;cancelled:boolean;error:unknown;cancel:()=>void}}){return <>
 {request.busy&&<button className={button} onClick={request.cancel}>{t('view.loadCancel')}</button>}
 {request.cancelled&&<p>{t('view.loadCancelled')}</p>}
 {request.error!=null&&<p role="alert" className="break-words text-danger">{request.error instanceof Error?request.error.message:String(request.error)}</p>}
</>}
function ExplicitRead({client,base,command,label,children}:{client:Client;base:string;command:HelmRequest;label:string;children:(r:HelmResponse)=>React.ReactNode}){
 const [enabled,setEnabled]=useState(false),commandKey=JSON.stringify(command)
 const load=useCallback((signal:AbortSignal)=>client.helm({...JSON.parse(base),...JSON.parse(commandKey)},signal),[client,base,commandKey])
 const request=useSnapshotRequest(load,base+commandKey,{enabled})
 return <div className="space-y-2"><button className={button} disabled={request.busy} onClick={()=>enabled?request.refresh():setEnabled(true)}>{label}</button><ReadStatus request={request}/>{request.value&&!request.cancelled&&!request.error&&<div inert={request.busy}>{children(request.value)}</div>}</div>
}
function CurrentEvidence({snapshot,onResource}:{snapshot:Diagnosis;onResource?:Props['onResource']}){
 const refs=[snapshot.ref,...snapshot.evidence.flatMap(e=>[e.ref,...(e.related?[e.related]:[])])].filter(r=>r.uid)
 const unique=[...new Map(refs.map(r=>[JSON.stringify(r),r])).values()]
 return <div className="space-y-2"><p className="text-warning">{t('helm.currentHookNotice')}</p>
  <p>{snapshot.capturedAt>0&&<DateText value={new Date(snapshot.capturedAt).toISOString()}/>}</p>
  <div className="flex flex-wrap gap-2">{unique.map(r=><button key={JSON.stringify(r)} className={button} disabled={!onResource} onClick={()=>onResource?.(r)}>{r.kind}/{r.name} · {t('helm.currentProperties')}</button>)}</div>
  {snapshot.evidence.map((e,i)=><div key={i} className="break-words border-l border-line pl-2"><b>{e.reason}</b><p className="whitespace-pre-wrap">{e.message}</p><code>{e.field}</code></div>)}
  <ul>{snapshot.coverage.map((c,i)=><li className="break-all" key={i}>{c.source} · {c.state}</li>)}</ul>
  {snapshot.truncated&&<p className="text-warning">{t('diagnosis.partial')}</p>}
 </div>
}
function Comparison({client,base,revisions,initialFrom,initialTo}:{client:Client;base:string;revisions:number[];initialFrom:number;initialTo:number}){
 const [from,setFrom]=useState(initialFrom),[to,setTo]=useState(initialTo)
 return <div className="space-y-2"><h3 className="font-semibold">{t('helm.compareRevisions')}</h3><p className="text-fg-muted">{t('helm.compareNotice')}</p>
  <div className="flex flex-wrap gap-2">{([{value:from,set:setFrom,label:t('helm.compareFrom')},{value:to,set:setTo,label:t('helm.compareTo')}] as const).map(s=><label key={s.label}>{s.label} <Select label={s.label} value={String(s.value)} onChange={v=>s.set(Number(v))} options={[{value:"0",label:"—"},...revisions.map(r=>({value:String(r),label:`#${r}`}))]}/></label>)}</div>
  {from>0&&to>0&&from!==to?<ExplicitRead key={`${from}/${to}`} client={client} base={base} command={{command:'compare',revision:from,otherRevision:to}} label={t('helm.compareRevisions')}>{r=>r.comparison&&<EditDiff before={r.comparison.left} after={r.comparison.right} label={`#${r.comparison.from} → #${r.comparison.to}`} exactText/>}</ExplicitRead>:<p>{t('helm.noBaseline')}</p>}
 </div>
}
function Flux({client,base,onResource}:{client:Client;base:string;onResource?:Props['onResource']}){
 const [ns,setNs]=useState('')
 return <div className="space-y-2"><h3 className="font-semibold">{t('helm.fluxContext')}</h3><p className="text-fg-muted">{t('helm.fluxNotice')}</p>
  <label className="flex flex-col gap-1">{t('helm.controllerNamespace')}<input className="helm-input" value={ns} onChange={e=>setNs(e.target.value)} spellCheck={false}/></label>
  {ns&&<ExplicitRead key={ns} client={client} base={base} command={{command:'flux-context',controllerNamespace:ns}} label={t('helm.loadFlux')}>{r=>r.flux&&<div className="space-y-2"><p><DateText value={new Date(r.flux.capturedAt).toISOString()}/></p><p>{t('diagnosis.coverage')}: {r.flux.coverage}</p>{r.flux.candidates.map(c=><div className="space-y-1 break-words" key={c.ref.uid}><p>{c.state} · {c.source}</p><button className={button} disabled={!onResource} onClick={()=>onResource?.(c.ref)}>{c.ref.scope}/{c.ref.name} · {t('helm.currentProperties')}</button>{c.conditions.map((e,i)=><p key={i}>{e.reason}: {e.message}</p>)}</div>)}</div>}</ExplicitRead>}
 </div>
}
