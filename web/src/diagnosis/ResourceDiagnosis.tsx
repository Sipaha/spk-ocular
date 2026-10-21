import {useCallback,useContext,useEffect,useRef,useState} from 'react'
import type {Client} from '../api/client'
import type {Ref} from '../api/types'
import type {DiagnosisEvidence} from './types'
import {t,classLabel} from '../i18n'
import {ViewConnectionContext} from '../views/useView'
import {useSnapshotRequest} from '../views/useSnapshotRequest'
import {errorDetail} from '../errors'
import {DateText} from '../components/DateText'
import {HealthDot} from '../components/ResourceTable'

const button='rounded border border-line px-2 py-1 text-xs hover:bg-hover disabled:opacity-40'
const identity=(ref:Ref)=>JSON.stringify([ref.provider,ref.target,ref.kind,ref.scope??'',ref.name,ref.uid])
export function ResourceDiagnosis(props:{client:Client;subject:Ref;onGo:(ref:Ref)=>void;onLogs:(ref:Ref,channel:string,previous:boolean)=>void}){
 const connection=useContext(ViewConnectionContext)
 return <Panel key={JSON.stringify([identity(props.subject),connection])} {...props}/>
}
function Panel({client,subject,onGo,onLogs}:{client:Client;subject:Ref;onGo:(ref:Ref)=>void;onLogs:(ref:Ref,channel:string,previous:boolean)=>void}){
 const [enabled,setEnabled]=useState(false),refJSON=JSON.stringify(subject)
 const load=useCallback(async(signal:AbortSignal)=>{
  const ref:Ref=JSON.parse(refJSON),snapshot=await client.diagnoseResource(ref,signal)
  if(identity(snapshot.ref)!==identity(ref))throw new Error(t('workspace.replaced'))
  return snapshot
 },[client,refJSON])
 const request=useSnapshotRequest(load,identity(subject),{enabled})
 const snapshot=request.value
 return <section aria-label={t('diagnosis.title')} className="min-w-0 space-y-2">
  <h3 className="text-sm font-semibold">{t('diagnosis.title')}</h3>
  <p className="text-xs text-fg-muted">{t('diagnosis.notice')}</p>
  <div className="flex gap-2"><button className={button} disabled={request.busy} onClick={()=>enabled?request.refresh():setEnabled(true)}>{request.busy?t('app.loading'):t('diagnosis.run')}</button>{request.busy&&<button className={button} onClick={request.cancel}>{t('view.loadCancel')}</button>}</div>
  {request.error!=null&&<p role="alert" className="text-xs text-danger">{errorDetail(request.error)}</p>}
  {request.cancelled&&<p className="text-xs text-fg-muted">{t('view.loadCancelled')}</p>}
  {snapshot&&!request.cancelled&&<div className="space-y-3 text-xs" aria-busy={request.busy}>
   <p className="text-fg-muted"><DateText value={new Date(snapshot.capturedAt).toISOString()}/></p>
   {(snapshot.truncated||snapshot.coverage.some(c=>c.state!=='ready'))&&<p className="text-warning">{t('diagnosis.partial')}</p>}
   {(['observations','events'] as const).map(group=><div key={group}><h4 className="mb-1 font-semibold">{t(group==='events'?'diagnosis.events':'diagnosis.observations')}</h4>
    <ul className="space-y-2">{snapshot.evidence.filter(e=>(e.rule==='event')===(group==='events')).map((e,i)=><li key={`${e.source.uid}/${e.field}/${i}`} className="min-w-0 rounded border border-line p-2 break-words">
     <p><HealthDot state={e.state}/><b>{e.reason||classLabel(e.state)}</b>{e.container&&<span className="ml-2 font-mono">{e.container}</span>}</p>
     {e.message&&<p className="mt-1 whitespace-pre-wrap text-fg-muted">{e.message}</p>}
     <p className="mt-1 break-all font-mono text-fg-muted">{e.source.kind}/{e.source.scope}/{e.source.name} · {e.field}</p>
     {e.at!=null&&e.at>0&&<DateText value={new Date(e.at).toISOString()}/>}
     <div className="mt-1 flex flex-wrap gap-2">
      {e.source.uid&&identity(e.source)!==identity(subject)&&<button className={button} onClick={()=>onGo(e.source)}>{e.source.kind}/{e.source.name}</button>}
      {e.related?.uid&&<button className={button} onClick={()=>onGo(e.related!)}>{e.related.kind}/{e.related.name}</button>}
      {e.container&&<EvidenceLogs key={identity(e.ref)+e.container+!!e.previous} client={client} evidence={e} onOpen={onLogs}/>}
     </div>
    </li>)}</ul>
    {group==='events'&&!snapshot.evidence.some(e=>e.rule==='event')&&<p className="text-fg-muted">{t('drawer.noEvents')}</p>}
   </div>)}
   <details><summary className="cursor-pointer font-semibold">{t('diagnosis.coverage')}</summary><ul className="mt-2 space-y-1">{snapshot.coverage.map((c,i)=><li key={i} className="break-all"><code>{c.source}</code> · <span className={c.state==='ready'?'text-fg-muted':'text-warning'}>{c.state==='ready'?t('diagnosis.ready'):c.state==='keys_unchecked'?t('diagnosis.keysUnchecked'):c.state==='not_covered'?t('diagnosis.notCovered'):c.state==='partial'?t('diagnosis.partial'):classLabel(c.state)}</span></li>)}</ul></details>
  </div>}
 </section>
}
function EvidenceLogs({client,evidence:e,onOpen}:{client:Client;evidence:DiagnosisEvidence;onOpen:(ref:Ref,channel:string,previous:boolean)=>void}){
 const [busy,setBusy]=useState(false),[error,setError]=useState(''),generation=useRef(0)
 const logKey=JSON.stringify([identity(e.ref),e.container,!!e.previous])
 useEffect(()=>()=>{generation.current++},[client,logKey])
 const open=async()=>{
  const id=++generation.current;setBusy(true);setError('')
  try{
   const info=await client.logInfo(e.ref)
   if(id!==generation.current)return
   if(!info.channels.some(c=>c.id===e.container)||(e.previous&&!info.previous)){setError(t('diagnosis.logsUnavailable'));return}
   onOpen(e.ref,e.container!,!!e.previous)
  }catch(err){if(id===generation.current)setError(errorDetail(err))}
  finally{if(id===generation.current)setBusy(false)}
 }
 return <><button className={button} disabled={busy} onClick={()=>void open()}>{t(e.previous?'diagnosis.previousLogs':'diagnosis.currentLogs')}</button>{error&&<p role="alert" className="text-danger">{error}</p>}</>
}
