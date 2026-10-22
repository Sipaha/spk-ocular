import {useCallback,useEffect,useId,useRef,useState} from 'react'
import type {Client} from '../api/client'
import type {Target} from '../api/types'
import {ModalCloseButton,useModalFocus} from '../components/modal'
import {t} from '../i18n'
import {MAX_BYTES,type AuditRequest,type AuditStored,type AuditSummary} from './types'

type Props={client:Client;target:Target|null;onClose:()=>void}
/** Target/configuration/client replacement destroys admission and file readers. */
export default function OfflineAuditDialog(props:Props){
 const [owner,setOwner]=useState({client:props.client,epoch:0})
 if(owner.client!==props.client)setOwner({client:props.client,epoch:owner.epoch+1})
 return <DialogBody key={`${owner.epoch}/${props.target?.provider??''}/${props.target?.id??''}/${props.target?.configRev??''}`} {...props}/>
}
function DialogBody({client,target,onClose}:Props){
 const title=useId(),box=useRef<HTMLElement>(null),close=useRef<HTMLButtonElement>(null),fileInput=useRef<HTMLInputElement>(null)
 const keyboard=useModalFocus(box,onClose,close)
 const [confirmed,setConfirmed]=useState(false),[busy,setBusy]=useState(false),[error,setError]=useState(false)
 const [reports,setReports]=useState<AuditSummary[]>([]),[stored,setStored]=useState<AuditStored|null>(null),[subject,setSubject]=useState(0)
 const listRevision=useRef(0)
 const epoch=useRef(0),pending=useRef<AbortController|null>(null),reader=useRef<FileReader|null>(null)
 const invalidate=useCallback(()=>{epoch.current++;pending.current?.abort();pending.current=null;reader.current?.abort();reader.current=null},[])
 const eligible=target?.provider==='kubernetes'&&!!target.configRev
 useEffect(()=>{
  const controller=new AbortController(),generation=listRevision.current
  void client.offlineAudit({command:'status'},controller.signal).then(r=>{if(!controller.signal.aborted&&listRevision.current===generation)setReports(r.reports.filter(report=>report.expiresAt>Date.now()))}).catch(()=>{if(!controller.signal.aborted&&listRevision.current===generation)setError(true)})
  return()=>{controller.abort();invalidate()}
 },[client,invalidate])
 // Renderer copies have the same deadline as backend-owned projections.
 // A local timer removes displayed evidence without polling a source or API.
 useEffect(()=>{
  const deadlines=[...reports.map(r=>r.expiresAt),...(stored?[stored.summary.expiresAt]:[])]
  if(!deadlines.length)return
  const timer=setTimeout(()=>{
   const now=Date.now()
   setReports(rows=>rows.filter(r=>r.expiresAt>now))
   setStored(value=>value&&value.summary.expiresAt>now?value:null)
  },Math.max(0,Math.min(2**31-1,Math.min(...deadlines)-Date.now())))
  return()=>clearTimeout(timer)
 },[reports,stored])
 async function request(req:AuditRequest){
  invalidate();listRevision.current++;setStored(null);setSubject(0);setError(false);setBusy(true)
  const generation=epoch.current,controller=new AbortController();pending.current=controller
  try{
   const r=await client.offlineAudit(req,controller.signal)
   if(generation!==epoch.current)return
   const now=Date.now()
   setReports(r.reports.filter(report=>report.expiresAt>now))
   if(r.selected&&r.selected.summary.expiresAt<=now){setError(true);return}
   setStored(r.selected??null)
  }catch{if(generation===epoch.current)setError(true)}
  finally{if(generation===epoch.current){pending.current=null;setBusy(false);close.current?.focus()}}
 }
 function load(file?:File){
  invalidate();setStored(null);setSubject(0);setError(false);setBusy(false)
  if(!file)return
  if(!confirmed||!eligible||file.size<=0||file.size>MAX_BYTES){setError(true);return}
  const generation=epoch.current,next=new FileReader();reader.current=next;setBusy(true)
  next.onload=()=>{
   if(epoch.current!==generation)return
   reader.current=null
   if(!(next.result instanceof ArrayBuffer)||next.result.byteLength>MAX_BYTES){setBusy(false);setError(true);return}
   const bytes=new Uint8Array(next.result);let binary='';for(const b of bytes)binary+=String.fromCharCode(b)
   void request({command:'import',version:'10.2.5',target:target.id,configRev:target.configRev,data:btoa(binary)})
  }
  next.onerror=()=>{if(epoch.current===generation){reader.current=null;setBusy(false);setError(true)}}
  next.readAsArrayBuffer(file)
 }
 const selected=stored?.report.subjects[subject]
 const verdict=(check:{severity:string;success:boolean})=>check.severity==='ignore'?'ignored':check.success?'passed':'failed'
 return <div className="fixed inset-0 z-50 flex items-start justify-center bg-black/40 pt-[4vh]" onMouseDown={e=>{if(e.target===e.currentTarget)onClose()}}>
  <section ref={box} role="dialog" aria-modal="true" aria-labelledby={title} onKeyDown={keyboard} className="flex max-h-[92vh] w-[min(960px,94%)] min-w-0 flex-col rounded-lg border border-line bg-panel p-4 text-sm shadow-2xl">
   <header className="mb-3 flex shrink-0 items-start gap-3"><h2 id={title} className="flex-1 text-lg font-semibold">{t('audit.title')}</h2><ModalCloseButton ref={close} label={t('action.close')} onClick={onClose}/></header>
   <div className="min-h-0 space-y-3 overflow-y-auto break-words [overflow-wrap:anywhere]">
    <p className="text-fg-muted">{t('audit.notice')}</p>
    <p className="text-warning">{t('audit.identity')}</p>
    <p>{t('audit.context')}: <span className="font-mono">{eligible?target.title:t('audit.selectTarget')}</span></p>
    <label className="flex items-start gap-2"><input type="checkbox" checked={confirmed} disabled={!eligible} onChange={e=>{invalidate();setConfirmed(e.target.checked);setStored(null);setBusy(false);setError(false)}}/><span>{t('audit.confirm')}</span></label>
    <div><button className="rounded border border-line px-2 py-1 disabled:opacity-50" disabled={!confirmed||!eligible||busy} onClick={()=>fileInput.current?.click()}>{t('audit.choose')}</button><input ref={fileInput} type="file" hidden accept=".json,application/json" aria-label={t('audit.choose')} disabled={!confirmed||!eligible||busy} onChange={e=>{const file=e.target.files?.[0];e.target.value='';load(file)}}/></div>
    <p className="text-xs text-fg-subtle">{t('audit.bounds')}</p>
    {busy&&<p role="status">{t('audit.reading')} <button className="rounded border border-line px-2 py-1" onClick={()=>{invalidate();setStored(null);setBusy(false);close.current?.focus()}}>{t('action.cancel')}</button></p>}
    {error&&<p role="alert" className="text-danger">{t('audit.invalid')}</p>}
    {reports.length>0&&<section aria-label={t('audit.retained')} className="space-y-1"><h3 className="font-semibold">{t('audit.retained')}</h3>{reports.map(r=><div key={r.id} className="flex flex-wrap items-center gap-2 text-xs"><span className="font-mono">Polaris v{r.version} · {r.target} · {r.objects} / {r.checks} · {r.id.slice(0,8)}</span><button className="rounded border border-line px-2 py-1" disabled={busy} onClick={()=>void request({command:'get',id:r.id})}>{t('audit.open')}</button><button className="rounded border border-line px-2 py-1" disabled={busy} onClick={()=>void request({command:'forget',id:r.id})}>{t('audit.forget')}</button></div>)}</section>}
    {stored&&<section aria-label={t('audit.results')} className="space-y-3 border-t border-line pt-3">
     <h3 className="font-semibold">Polaris v10.2.5 · JSON 1.0 · {t('audit.asserted')}</h3>
     <p className="font-mono">{t('audit.context')}: {stored.summary.target}</p>
     {(stored.summary.target!==target?.id||stored.summary.configRev!==target?.configRev)&&<p className="text-warning">{t('audit.archived')}</p>}
     <p>{t('audit.auditTime')}: {stored.report.auditTime??t('audit.unknown')} · {t('audit.sourceType')}: <code>{stored.report.sourceType}</code></p>
     <p className="text-warning">{t('audit.coverage')}</p>
     <p className="text-fg-muted">{t('audit.projection')}</p>
     <p>{t('audit.counts',{objects:stored.report.subjects.length,checks:stored.report.checks})}</p>
     {stored.report.subjects.length===0?<p>{t('audit.empty')}</p>:<>
      <label className="block">{t('audit.subject')}<select aria-label={t('audit.subject')} value={subject} onChange={e=>setSubject(Number(e.target.value))} className="mt-1 block w-full min-w-0 rounded border border-line bg-panel p-1">{stored.report.subjects.map((r,i)=><option key={i} value={i}>{i+1}. {r.kind} · {r.namespace||t('audit.unscoped')}/{r.name}</option>)}</select></label>
      {selected&&<div className="space-y-2 text-xs"><p>{t('audit.uidUnknown')}</p>{selected.checks.length===0?<p>{t('audit.empty')}</p>:<div className="space-y-2">{selected.checks.map((c,i)=><article key={i} className="space-y-1 border-t border-line py-2"><p className="font-mono">{c.id}</p><p>{t(`audit.${verdict(c)}`)} · <code>{c.severity}</code> · <code>{c.category}</code></p><p>{t(`audit.scope.${c.scope}`)}{c.container&&<> · <code>{c.container}</code></>}</p></article>)}</div>}</div>}
     </>}
    </section>}
   </div>
  </section>
 </div>
}
