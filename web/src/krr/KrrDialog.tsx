import {useCallback, useEffect, useId, useRef, useState} from 'react'
import {ModalCloseButton, useModalFocus} from '../components/modal'
import {t} from '../i18n'
import {MAX_BYTES, type Allocation, type KrrStored, type KrrSummary, type KrrRequest} from './model'
import type {Client} from '../api/client'
import type {Target} from '../api/types'
import type {KrrResponse,KrrRun,KrrRecovery} from './model'
import {KrrRunner} from './KrrRunner'

type DialogProps={onClose:()=>void;client:Client;target?:Target|null}
/** Replace renderer state on client changes; backend retention is independent. */
export default function KrrDialog(props:DialogProps) {
 const [owner,setOwner]=useState({client:props.client,generation:0})
 if(owner.client!==props.client)setOwner({client:props.client,generation:owner.generation+1})
 return <KrrDialogBody key={owner.generation} {...props}/>
}
/** The backend owns reports; this panel only selects files and renders projections. */
function KrrDialogBody({onClose, client, target}:DialogProps) {
  const title = useId(), box = useRef<HTMLElement>(null), close = useRef<HTMLButtonElement>(null)
  const keyboard = useModalFocus(box, onClose, close)
  const [confirmed, setConfirmed] = useState(false), [stored, setStored] = useState<KrrStored | null>(null)
  const [busy, setBusy] = useState(false), [error, setError] = useState(false)
  const [reports,setReports] = useState<KrrSummary[]>([])
  const [runs,setRuns]=useState<KrrRun[]>([]),[recovery,setRecovery]=useState<KrrRecovery[]>([]),[recoveryUnknown,setRecoveryUnknown]=useState(false)
  const report = stored?.report
  const acceptStatus=useCallback((r:KrrResponse)=>{listRevision.current++;setReports(r.reports);setRuns(r.runs??[]);setRecovery(r.recovery??[]);setRecoveryUnknown(r.recoveryUnknown??false)},[])
  const listRevision = useRef(0)
  const fileInput = useRef<HTMLInputElement>(null)
  const pending = useRef<AbortController | null>(null)
  const reader = useRef<FileReader | null>(null), epoch = useRef(0)
  const invalidate = useCallback(() => { epoch.current++; reader.current?.abort(); reader.current = null; pending.current?.abort(); pending.current = null },[])
  useEffect(() => {
    const controller = new AbortController()
    let timer:ReturnType<typeof setTimeout>|undefined
    const refresh=async()=>{
      const generation=listRevision.current
      try {const r=await client.krr({command:'status'},controller.signal);if(!controller.signal.aborted&&generation===listRevision.current){setReports(r.reports);setRuns(r.runs??[]);setRecovery(r.recovery??[]);setRecoveryUnknown(r.recoveryUnknown??false)}}
      catch {if(!controller.signal.aborted&&generation===listRevision.current)setError(true)}
      finally{if(!controller.signal.aborted)timer=setTimeout(()=>void refresh(),2000)}
    }
    void refresh()
    return () => {controller.abort();if(timer)clearTimeout(timer);invalidate()}
  },[client,invalidate])
  async function request(req:KrrRequest) {
    invalidate();if(req.command!=='cancel')setStored(null);setError(false);setBusy(true)
    const generation=epoch.current,controller=new AbortController();pending.current=controller
    try {const r=await client.krr(req,controller.signal);if(generation!==epoch.current)return;acceptStatus(r);if(req.command!=='cancel')setStored(r.selected??null);close.current?.focus()}
    catch {if(generation===epoch.current)setError(true)}
    finally {if(generation===epoch.current){setBusy(false);pending.current=null;close.current?.focus()}}
  }
  function load(file?: File) {
    invalidate(); setStored(null); setError(false); setBusy(false)
    if (!file) return
    if (!confirmed || file.size <= 0 || file.size > MAX_BYTES) { setError(true); return }
    const generation = epoch.current, next = new FileReader()
    reader.current = next; setBusy(true)
    next.onload = () => {
      if (epoch.current !== generation) return
      reader.current = null; setBusy(false)
      try {
        if (!(next.result instanceof ArrayBuffer)) throw new Error('invalid')
        const bytes = new Uint8Array(next.result)
        if(bytes.length>MAX_BYTES)throw new Error('invalid')
        let binary='';for(const byte of bytes)binary+=String.fromCharCode(byte)
        void request({command:'import',version:'1.31.0',data:btoa(binary)})
      } catch { setStored(null); setError(true) }
    }
    next.onerror = () => { if (epoch.current === generation) { reader.current = null; setBusy(false); setError(true) } }
    next.readAsArrayBuffer(file)
  }
  const value = (v: Allocation) => v.state === 'number' ? String(v.value ?? 0) : t(`krr.${v.state}`)
  return <div className="fixed inset-0 z-50 flex items-start justify-center bg-black/40 pt-[4vh]" onMouseDown={e => { if (e.target === e.currentTarget) onClose() }}>
    <section ref={box} role="dialog" aria-modal="true" aria-labelledby={title} onKeyDown={keyboard}
      className="flex max-h-[92vh] w-[min(960px,94%)] min-w-0 flex-col rounded-lg border border-line bg-panel p-4 text-sm shadow-2xl">
      <header className="mb-3 flex shrink-0 items-start gap-3"><h2 id={title} className="flex-1 text-lg font-semibold">{t('krr.title')}</h2><ModalCloseButton ref={close} label={t('action.close')} onClick={onClose}/></header>
      <div className="min-h-0 space-y-3 overflow-y-auto break-words [overflow-wrap:anywhere]">
        <p className="text-fg-muted">{t('krr.offline')}</p>
        <KrrRunner key={`${target?.provider??''}/${target?.id??''}/${target?.configRev??''}/${target?.connection?.state??''}/${target?.connection?.id??''}`} client={client} target={target} onUpdate={acceptStatus} onSettled={()=>close.current?.focus()}/>
        <p className="text-warning">{t('krr.identity')}</p>
        <label className="flex items-start gap-2"><input type="checkbox" checked={confirmed} onChange={e => { invalidate(); setConfirmed(e.target.checked); setStored(null); setBusy(false); setError(false) }}/><span>{t('krr.confirm')}</span></label>
        <div><button type="button" className="rounded border border-line px-2 py-1 disabled:opacity-50" disabled={!confirmed} onClick={()=>fileInput.current?.click()}>{t('krr.choose')}</button><input ref={fileInput} type="file" hidden aria-label={t('krr.choose')} accept=".json,application/json" disabled={!confirmed} onChange={e => {const file=e.target.files?.[0];e.target.value='';load(file)}}/></div>
        <p className="text-xs text-fg-subtle">{t('krr.bounds')}</p>
        {reports.length>0 && <section aria-label={t('krr.retained')} className="space-y-1"><p>{t('krr.retained')}</p>{reports.map(r=><div key={r.id} className="flex flex-wrap items-center gap-2 text-xs"><span>KRR v{r.version} · {r.rows} · {r.id.slice(0,8)}</span><button className="rounded border border-line px-2 py-1" disabled={busy} onClick={()=>void request({command:'get',id:r.id})}>{t('krr.open')}</button><button className="rounded border border-line px-2 py-1" disabled={busy} onClick={()=>void request({command:'forget',id:r.id})}>{t('krr.clear')}</button></div>)}</section>}
        {runs.length>0&&<section aria-label={t('krr.runs')} className="space-y-2 border-t border-line pt-3"><h3 className="font-semibold">{t('krr.runs')}</h3>{runs.map(run=><article key={run.id} className="space-y-1 text-xs"><p className="font-mono">{run.target} · {run.namespace}/{run.name}</p><p>{t(`krr.run.${run.state}`)}</p>{run.cleanupPending&&<p className="text-danger">{t('krr.cleanupPending')}</p>}{(run.state==='running'||run.state==='cancelling')&&<button className="rounded border border-line px-2 py-1" disabled={busy||run.state==='cancelling'} onClick={()=>void request({command:'cancel',id:run.id})}>{t('krr.cancelRun')}</button>}{run.reportID&&<button className="rounded border border-line px-2 py-1" disabled={busy} onClick={()=>void request({command:'get',id:run.reportID})}>{t('krr.open')}</button>}</article>)}</section>}
        {(recovery.length>0||recoveryUnknown)&&<section role="status" className="space-y-1 text-xs text-danger"><p>{t('krr.recovery')}</p>{recovery.map((r,i)=><p key={i} className="font-mono">{r.target} · {r.namespace}/{r.name}</p>)}{recoveryUnknown&&<p>{t('krr.recoveryUnknown')}</p>}</section>}
        {busy && <div role="status">{t('krr.reading')} <button className="rounded border border-line px-2 py-1" onClick={() => { invalidate(); setBusy(false); setStored(null); close.current?.focus() }}>{t('action.cancel')}</button></div>}
        {error && <p role="alert" className="text-danger">{t('krr.invalid')}</p>}
        {report && <section aria-label={t('krr.results')} className="space-y-3 border-t border-line pt-3">
          <p className="font-semibold">KRR v1.31.0 · simple · {t(stored?.summary.origin==='job'?'krr.pinnedEngine':'krr.asserted')}</p>
          <p className="text-fg-muted">{t('krr.coverage')}</p>
          <p className="text-xs">{t('krr.rounding')}<br/>cpu_min_value (millicores): {value(report.minimums?.[0]??{state:'unknown'})} · memory_min_value (MiB): {value(report.minimums?.[1]??{state:'unknown'})}</p>
          <details><summary className="cursor-pointer">{t('krr.settings')}</summary><dl className="grid grid-cols-[minmax(0,1fr)_max-content] gap-x-3 text-xs">{report.settings.map(s => <div key={s.key} className="contents"><dt className="font-mono">{s.key}</dt><dd>{String(s.value)}</dd></div>)}</dl></details>
          <p>{t('krr.counts', {rows: report.rows.length, errors: report.errors})}</p>
          {!report.rows.length && <p>{t('krr.empty')}</p>}
          {report.rows.map((row, index) => <article key={index} className="space-y-2 border-t border-line pt-3 text-xs">
            <p className="font-mono">{row.cluster ?? t('krr.unknownCluster')} · {row.kind} · {row.namespace}/{row.name} · {row.container}</p>
            <p>{t('krr.severity')}: <code>{row.severity}</code> · HPA: {t(`krr.${row.hpa}`)}</p>
            <dl className="space-y-1">{['requests.cpu', 'requests.memory', 'limits.cpu', 'limits.memory'].map((field, i) => <div key={field} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-2 border-b border-line py-1"><dt className="font-mono">{field} ({i % 2 ? 'bytes' : 'cores'})</dt><dd>{t('krr.current')}: {value(row.current[i])}<br/>{t('krr.proposed')}: {value(row.proposed[i])} · <code>{row.severities[i]}</code></dd></div>)}</dl>
            {(row.reasons.length > 0 || row.warnings.length > 0) && <p>{t('krr.reasons')}: {[...row.reasons, ...row.warnings].map(v => v === 'unknown' ? t('krr.unknown') : v).join(' · ')}</p>}
          </article>)}
          <p className="text-fg-muted">{t('krr.caveats')}</p>
          <button type="button" className="rounded border border-line px-2 py-1" onClick={() => {if(stored)void request({command:'forget',id:stored.summary.id})}}>{t('krr.clear')}</button>
        </section>}
      </div>
    </section>
  </div>
}
