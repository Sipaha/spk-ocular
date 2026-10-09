import { lazy, Suspense, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import type { Client } from '../api/client'
import type { Resource, ResourceRevision } from '../api/types'
import { getLanguage, t } from '../i18n'
import { EditDiff } from '../edit/EditDiff'
import { focusMark, restoreFocus } from '../shortcuts'
import { Select } from './Select'
const YamlView = lazy(()=>import('./YamlView'))

export function DeploymentRevisions({client,resource}:{client:Client;resource:Resource}) {
  const [opened,setOpened]=useState<{revision:ResourceRevision;compare:boolean}|null>(null)
  if (!resource.revisionsAvailable) return null
  return <section aria-label={t('revisions.title')}>
    <h3 className="mb-1.5 text-[12px] font-semibold uppercase tracking-wider text-fg-subtle">{t('revisions.title')}</h3>
    {resource.revisionsError ? <p role="alert" className="text-xs text-warning">{resource.revisionsError}</p> : !resource.revisions?.length ? <p className="text-xs text-fg-subtle">{t('revisions.empty')}</p> : <ul className="divide-y divide-line rounded-md border border-line">
      {resource.revisions.map(revision=><li key={revision.ref.uid || revision.ref.name} className="flex flex-col gap-1 px-2 py-2" data-deploy-revision>
        <div className="flex flex-wrap items-center gap-2"><strong className="text-[13px]">{t('revisions.number',{number:revision.number})}</strong>
          {revision.current && <span className="rounded bg-success/10 px-1.5 py-0.5 text-[11px] text-success">{t('revisions.current')}</span>}
          <span className="ml-auto text-[11px] text-fg-subtle" title={revision.created}>{new Date(revision.created).toLocaleString(getLanguage())}</span>
        </div>
        <div className="flex items-center gap-2 text-xs"><span className="min-w-0 flex-1 truncate text-fg-muted" title={(revision.images ?? []).join('\n')}>{(revision.images ?? []).join(', ')}</span><span className="shrink-0 text-fg-subtle">{t('revisions.replicas',{ready:revision.ready,total:revision.replicas})}</span></div>
        {revision.cause && <p className="truncate text-xs text-fg-subtle" title={revision.cause}>{revision.cause}</p>}
        <div className="flex gap-2"><button className="rounded border border-line px-2 py-0.5 text-xs text-fg-muted hover:bg-hover" onClick={()=>setOpened({revision,compare:false})}>{t('revisions.view')}</button><button className="rounded border border-line px-2 py-0.5 text-xs text-fg-muted hover:bg-hover" onClick={()=>setOpened({revision,compare:true})}>{t('revisions.compare')}</button></div>
      </li>)}
    </ul>}
    {resource.revisionsTruncated && <p className="mt-1 text-xs text-warning">{t('revisions.truncated')}</p>}
    {opened && <RevisionDialog key={opened.revision.ref.uid || opened.revision.ref.name} client={client} resource={resource} revision={opened.revision} initialCompare={opened.compare} onClose={()=>setOpened(null)}/>}
  </section>
}
function RevisionDialog({client,resource,revision,initialCompare,onClose}:{client:Client;resource:Resource;revision:ResourceRevision;initialCompare:boolean;onClose:()=>void}) {
  // The revision and comparison snapshot belong to this opening, not background updates.
  const [source]=useState(resource)
  const [compare,setCompare]=useState(initialCompare)
  const [baseline,setBaseline]=useState('current')
  const [text,setText]=useState<string|null>(null)
  const [before,setBefore]=useState<string|null>(source.templateYAML ?? null)
  const [error,setError]=useState('')
  const [baselineError,setBaselineError]=useState('')
  const box=useRef<HTMLElement>(null)
  const [mark]=useState(focusMark)
  useEffect(()=>{box.current?.focus();return()=>restoreFocus(mark)},[mark])
  useEffect(()=>{
    let live=true
    void client.getResource(revision.ref).then(result=>{if(live) {if(result.templateYAML === undefined) setError(t('revisions.unavailable'));else setText(result.templateYAML)}},failure=>{if(live)setError(failure instanceof Error?failure.message:String(failure))})
    return()=>{live=false}
  },[client,revision.ref])
  useEffect(()=>{
    if(baseline === 'current') return
    const selected=source.revisions?.find(item=>(item.ref.uid || item.ref.name) === baseline)
    if(!selected) return
    let live=true
    void client.getResource(selected.ref).then(result=>{if(live){if(result.templateYAML === undefined)setBaselineError(t('revisions.unavailable'));else setBefore(result.templateYAML)}},failure=>{if(live)setBaselineError(failure instanceof Error?failure.message:String(failure))})
    return()=>{live=false}
  },[client,source,baseline])
  return createPortal(<div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-3" onMouseDown={event=>{if(event.target === event.currentTarget)onClose()}}>
    <section ref={box} role="dialog" aria-modal="true" aria-label={t('revisions.title')} tabIndex={-1} className="flex h-[min(850px,calc(100dvh-24px))] w-[min(1400px,calc(100vw-24px))] flex-col border border-line bg-panel outline-none" onKeyDown={event=>{
      if(event.key==='Escape'){event.stopPropagation();onClose()}
      if(event.key==='Tab'){
        const controls=[...(box.current?.querySelectorAll<HTMLElement>('button:not(:disabled),[tabindex="0"],.cm-content') ?? [])]
        const first=controls[0],last=controls.at(-1)
        if(event.shiftKey&&(document.activeElement===first||document.activeElement===box.current)){event.preventDefault();last?.focus()}
        else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first?.focus()}
      }
    }}>
      <header className="flex h-10 shrink-0 items-center gap-3 border-b border-line px-3"><strong className="text-sm">{t('revisions.number',{number:revision.number})}</strong><span className="min-w-0 flex-1 truncate text-xs text-fg-muted" title={revision.ref.name}>{revision.ref.name}</span><button className="rounded px-2 text-lg hover:bg-hover" aria-label={t('drawer.close')} onClick={onClose}>×</button></header>
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-line px-3 py-2 text-xs"><span className="text-fg-subtle">{t('revisions.template')}</span><button className={`rounded border border-line px-2 py-1 ${!compare?'bg-hover':''}`} onClick={()=>setCompare(false)}>{t('revisions.view')}</button><button className={`rounded border border-line px-2 py-1 ${compare?'bg-hover':''}`} onClick={()=>setCompare(true)}>{t('revisions.compare')}</button>
        {compare&&<><span className="ml-2 text-fg-subtle">{t('revisions.baseline')}</span><Select label={t('revisions.baseline')} value={baseline} options={[{value:'current',label:t('revisions.deployment')},...(source.revisions ?? []).filter(item=>item.ref.uid!==revision.ref.uid).map(item=>({value:item.ref.uid||item.ref.name,label:t('revisions.number',{number:item.number})}))]} className="text-xs" onChange={value=>{setBaseline(value);setBaselineError('');setBefore(value==='current'?source.templateYAML ?? null:null)}}/></>}
      </div>
      <div className="min-h-0 flex-1 overflow-auto" data-revision-content>
        {error || (compare&&baselineError) ? <p role="alert" className="p-3 text-sm text-danger">{error||baselineError}</p> : text===null || (compare&&before===null) ? <p role="status" className="p-3 text-sm text-fg-subtle">{t('app.loading')}</p> : compare && before===text ? <p className="p-3 text-sm text-fg-subtle">{t('revisions.identical')}</p> : compare ? <div className="p-3"><EditDiff key={`${baseline}:${text}`} before={before!} after={text} label={t('revisions.compare')}/></div> : <Suspense fallback={<pre className="p-3 font-mono text-[13px]">{text}</pre>}><YamlView text={text} label={t('revisions.template')}/></Suspense>}
      </div>
    </section>
  </div>,document.body)
}
