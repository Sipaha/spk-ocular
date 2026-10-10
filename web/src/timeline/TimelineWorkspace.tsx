import { useEffect, useMemo, useRef, useState, type ComponentProps, type ReactNode } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import type { Client } from '../api/client'
import type { ClusterTimeline, Ref, ScopeSel } from '../api/types'
import { getLanguage, t } from '../i18n'
import { mayLeave } from '../edit/guard'
import { ResourceDrawer } from '../components/ResourceDrawer'
import { Select } from '../components/Select'
import { timelineBounds, timelineDensity, timelineRows } from './model'

type Drawer = Omit<ComponentProps<typeof ResourceDrawer>, 'subject' | 'onClose'>
interface Props {client: Client; target: {provider: string; id: string}; scope: ScopeSel; scopePicker: ReactNode; drawer: Drawer}
const button = 'rounded-md border border-line px-2 py-1 text-xs hover:bg-hover disabled:opacity-40'

export default function TimelineWorkspace({client,target,scope,scopePicker,drawer}: Props) {
 const [snapshot,setSnapshot] = useState<ClusterTimeline|null>(null)
 const [busy,setBusy] = useState(true)
 const [error,setError] = useState('')
 const [revision,setRevision] = useState(0)
 const [query,setQuery] = useState('')
 const [warnings,setWarnings] = useState(false)
 const [kind,setKind] = useState('')
 const [rangeMs,setRangeMs] = useState(0)
 const [eventID,setEventID] = useState('')
 const [subject,setSubject] = useState<Ref|null>(null)
 const scroll = useRef<HTMLDivElement>(null)
 const scopeKey = JSON.stringify(scope)
 const language = getLanguage()
 const date = (at:number, full=false) => at > 0 ? new Date(at).toLocaleString(language,{month:'short',day:'numeric',hour:'2-digit',minute:'2-digit',...(full?{second:'2-digit'}:{})}) : t('timeline.unknownTime')
 useEffect(()=>{
  let live = true
  const abort = new AbortController()
  client.clusterTimeline({provider:target.provider,target:target.id,scope:JSON.parse(scopeKey)},abort.signal).then(value=>{
   if(live){setSnapshot(value);setError('');setBusy(false)}
  },e=>{if(live){setError(e instanceof Error?e.message:String(e));setBusy(false)}})
  return ()=>{live=false;abort.abort()}
 },[client,target.provider,target.id,scopeKey,revision])
 const rows = useMemo(()=>snapshot?timelineRows(snapshot,{query,warnings,kind,rangeMs}):[],[snapshot,query,warnings,kind,rangeMs])
 const bounds = useMemo(()=>timelineBounds(rows,snapshot?.capturedAt??0,rangeMs),[rows,snapshot,rangeMs])
 const bins = useMemo(()=>timelineDensity(rows,bounds.start,bounds.end),[rows,bounds])
 const maxDensity = Math.max(1,...bins.map(b=>b.normal+b.warning))
 const virtual = useVirtualizer({count:rows.length,getScrollElement:()=>scroll.current,estimateSize:()=>48,overscan:8,getItemKey:i=>rows[i].event.id})
 const selected = snapshot?.events.find(e=>e.id===eventID)
 const selectedRow = rows.find(r=>r.event.id===eventID)
 const kinds = [...new Set(snapshot?.events.map(e=>e.subjectKind).filter(Boolean)??[])].sort()
 const openResource = (ref:Ref) => {
  if(subject && subject.uid===ref.uid && subject.kind===ref.kind)return
  mayLeave(()=>setSubject(ref))
 }
 const refresh = () => {setBusy(true);setRevision(r=>r+1)}
 return <section className="timeline-workspace flex min-h-0 flex-1 flex-col" aria-label={t('timeline.title')}>
  <header className="flex min-h-10 shrink-0 flex-wrap items-center gap-2 border-b border-line bg-panel px-3 py-1">
   <h2 className="text-sm font-semibold">{t('timeline.title')}</h2>{scopePicker}
   <button data-resync className={button} disabled={busy} onClick={refresh}>{busy?t('app.loading'):t('graph.refresh')}</button>
   <span className="ml-auto text-xs text-fg-muted">{snapshot?t('timeline.snapshot',{time:date(snapshot.capturedAt,true)}):''}</span>
  </header>
  <div className="relative flex min-h-0 flex-1">
   <div className="flex min-w-0 flex-1 flex-col">
    <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-line px-3 py-2 text-xs">
     <input className="min-w-32 flex-1 rounded-md border border-line bg-panel px-2 py-1 outline-none focus:border-accent" aria-label={t('timeline.search')} placeholder={t('timeline.search')} value={query} onChange={e=>setQuery(e.target.value)} onKeyDown={e=>{if(e.key==='Escape'){e.stopPropagation();setQuery('')}}}/>
     <Select label={t('timeline.kind')} value={kind} options={[{value:'',label:t('timeline.allKinds')},...kinds.map(value=>({value,label:value}))]} onChange={setKind}/>
     <Select label={t('timeline.range')} value={String(rangeMs)} options={[{value:'0',label:t('timeline.allTime')},{value:'900000',label:t('timeline.last15m')},{value:'3600000',label:t('timeline.last1h')},{value:'21600000',label:t('timeline.last6h')},{value:'86400000',label:t('timeline.last24h')}]} onChange={value=>setRangeMs(Number(value))}/>
     <label className="flex items-center gap-1 whitespace-nowrap"><input type="checkbox" checked={warnings} onChange={e=>setWarnings(e.target.checked)}/>{t('timeline.warnings')}</label>
    </div>
    <div className="timeline-axis shrink-0 border-b border-line bg-panel py-2 pr-3">
     <span className="px-3 text-xs text-fg-muted">{t('timeline.workload')}</span>
     <div className="min-w-0">
      <svg viewBox="0 0 480 24" preserveAspectRatio="none" className="h-6 w-full" role="img" aria-label={t('timeline.density')}>
       {bins.map((b,i)=><g key={i}><rect x={i*10} y={24-(b.normal+b.warning)/maxDensity*24} width={8} height={b.normal/maxDensity*24} fill="var(--color-accent)" opacity=".4"/><rect x={i*10} y={24-b.warning/maxDensity*24} width={8} height={b.warning/maxDensity*24} fill="var(--color-warning)"/></g>)}
      </svg>
      <div className="mt-1 flex justify-between gap-2 text-[10px] text-fg-muted">{[0,.5,1].map(f=><span key={f}>{snapshot?date(bounds.start+(bounds.end-bounds.start)*f):'—'}</span>)}</div>
     </div>
    </div>
    {error&&<p role="alert" className="shrink-0 border-b border-line px-3 py-2 text-xs text-danger">{error}{snapshot&&' · '+t('timeline.stale')}</p>}
    <div ref={scroll} data-area="table" data-area-focus tabIndex={0} role="grid" aria-label={t('timeline.events')} aria-rowcount={rows.length} aria-colcount={2} className="min-h-0 flex-1 overflow-auto outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-accent" onKeyDown={e=>{
     if(e.target!==e.currentTarget)return
     if(e.key==='ArrowDown'||e.key==='ArrowUp'){
      e.preventDefault()
      const current=rows.findIndex(r=>r.event.id===eventID)
      const index=Math.max(0,Math.min(rows.length-1,current+(e.key==='ArrowDown'?1:-1)))
      if(rows[index]){setEventID(rows[index].event.id);virtual.scrollToIndex(index)}
     }
    }}>
     {busy&&!snapshot&&<p role="status" className="p-4 text-sm text-fg-muted">{t('timeline.loading')}</p>}
     {!busy&&!rows.length&&<p role="status" className="p-4 text-sm text-fg-muted">{snapshot?.events.length?t('timeline.noMatches'):t('timeline.empty')}</p>}
     <div className="relative" style={{height:virtual.getTotalSize()}}>
      {virtual.getVirtualItems().map(item=>{
       const row=rows[item.index],event=row.event,warning=event.type==='Warning'
       const left=Math.max(0,Math.min(100,(event.firstAt-bounds.start)/(bounds.end-bounds.start)*100))
       const right=Math.max(left,Math.min(100,(event.lastAt-bounds.start)/(bounds.end-bounds.start)*100))
       return <div key={item.key} role="row" aria-rowindex={item.index+1} aria-selected={eventID===event.id} className={'timeline-row absolute left-0 top-0 w-full border-b border-line '+(eventID===event.id?'bg-selected':'hover:bg-hover')} style={{height:item.size,transform:`translateY(${item.start}px)`}}>
        <div role="rowheader" className="min-w-0 px-3 py-1 text-xs" title={row.chain.map(r=>r.kindTitle+'/'+(r.ref.title??r.ref.name)).join(' → ')}>
         <div className="truncate font-medium">{row.root.kindTitle} / {row.root.ref.title??(row.root.ref.name||t('timeline.unknownResource'))}</div>
         <div className="truncate text-[10px] text-fg-muted">{event.subject.scope||t('graph.clusterResources')} · {event.subjectKind}/{event.subject.title??event.subject.name}{row.incomplete?' · '+t('timeline.incompleteChain'):''}</div>
        </div>
        <div role="gridcell" className="relative min-w-0 overflow-hidden pr-3">
         <button className={'absolute inset-y-1 left-0 right-3 rounded-sm px-2 text-left text-xs outline-none focus-visible:ring-1 focus-visible:ring-accent '+(warning?'text-warning':'text-fg-muted')} aria-label={event.reason+' · '+event.subjectKind+'/'+(event.subject.title??event.subject.name)+' · '+date(event.lastAt,true)} onClick={()=>setEventID(event.id)}>
          {event.lastAt>0&&<span aria-hidden="true" className={'absolute top-0 h-full rounded-sm border-l-2 '+(warning?'border-warning bg-warning/15':'border-accent bg-accent/10')} style={{left:left+'%',width:`max(3px, ${right-left}%)`}}/>}
          <span className="relative block truncate">{event.reason||event.type} · {t('timeline.count',{count:event.count})}</span>
         </button>
        </div>
       </div>
      })}
     </div>
    </div>
    {selected&&<div className="max-h-52 shrink-0 overflow-auto border-t border-line bg-panel px-3 py-2 text-xs" aria-label={t('timeline.eventDetails')}>
     <div className="flex flex-wrap items-center gap-2"><strong className={selected.type==='Warning'?'text-warning':''}>{selected.reason||selected.type}</strong><span className="text-fg-muted">{selected.type} · {t('timeline.count',{count:selected.count})}</span><button className={'ml-auto '+button} onClick={()=>setEventID('')}>{t('timeline.closeEvent')}</button></div>
     <p className="mt-1 whitespace-pre-wrap break-words">{selected.message}</p>
     {selected.messageTruncated&&<p className="text-warning">{t('timeline.messageTruncated')}</p>}
     <p className="mt-2 text-fg-muted">{date(selected.firstAt,true)} → {date(selected.lastAt,true)}{selected.timeFallback?' · '+t('timeline.timeFallback'):''}</p>
     {selectedRow&&<p className="mt-1 break-words text-fg-muted">{selectedRow.chain.map(r=>r.kindTitle+'/'+(r.ref.title??r.ref.name)).join(' → ')}</p>}
     <div className="mt-2 flex flex-wrap gap-2">
      <button className={button} disabled={!selected.openable} onClick={()=>openResource(selected.subject)}>{t('timeline.openResource')} · {selected.subjectKind}/{selected.subject.title??selected.subject.name}</button>
      {selected.ref.uid&&<button className={button} onClick={()=>openResource(selected.ref)}>{t('timeline.openEvent')}</button>}
      {selectedRow?.chain.slice(0,-1).map(r=><button key={r.ref.uid} className={button} onClick={()=>openResource(r.ref)}>{r.kindTitle}/{r.ref.title??r.ref.name}</button>)}
     </div>
    </div>}
   </div>
   {subject&&<ResourceDrawer {...drawer} subject={subject} onClose={()=>setSubject(null)}/>}
  </div>
  <footer className="flex shrink-0 flex-wrap gap-x-3 gap-y-1 border-t border-line px-3 py-2 text-xs text-fg-muted">
   <span>{t('timeline.counts',{shown:rows.length,total:snapshot?.events.length??0})}</span>
   <span>{t('timeline.retention')}</span><span>{t('timeline.series')}</span>
   {snapshot?.discovery!=='ready'&&snapshot&&<span className="text-warning">{t('graph.discovery')}</span>}
   {snapshot?.truncated&&<span role="status" className="text-warning">{t('timeline.truncated')}</span>}
  </footer>
  {!!snapshot?.problems.length&&<details className="border-t border-line px-3 py-1 text-xs text-warning"><summary>{t('graph.partial',{count:snapshot.problems.length})}</summary><ul className="max-h-24 overflow-y-auto">{snapshot.problems.map((p,i)=><li key={i}>{p.kind}{p.scope?' / '+p.scope:''} · {p.class}</li>)}</ul></details>}
 </section>
}
