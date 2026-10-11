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
    {resource.revisionsError ? <p role="alert" className="text-xs text-warning">{resource.revisionsError}</p> : !resource.revisions?.length ? <p className="text-xs text-fg-subtle">{t('revisions.empty')}</p> : <div className="overflow-x-auto rounded border border-line" tabIndex={0} aria-label={t('revisions.title')}>
      <table className="w-full min-w-[400px] table-fixed text-left text-xs" aria-label={t('revisions.title')}>
        <colgroup><col className="w-[90px]"/><col/><col className="w-[48px]"/><col className="w-[80px]"/><col className="w-[60px]"/></colgroup>
        <thead className="bg-hover/50 text-[11px] text-fg-subtle"><tr>
          {(['revision','images','ready','created','actions'] as const).map(column=><th key={column} scope="col" title={t(`revisions.column.${column}`)} className="truncate px-2 py-1.5 font-medium">{t(`revisions.column.${column}`)}</th>)}
        </tr></thead>
        <tbody className="divide-y divide-line">
          {resource.revisions.map(revision=><tr key={revision.ref.uid || revision.ref.name} className="h-8 hover:bg-hover/50" data-deploy-revision>
            <th scope="row" className="px-2 py-1 font-normal"><span className="whitespace-nowrap font-medium tabular-nums">{t('revisions.number',{number:revision.number})}</span>
              {revision.current && <span className="block text-[11px] text-success">{t('revisions.current')}</span>}
            </th>
            <td className="px-2 py-1"><div className="truncate text-fg-muted" title={(revision.images ?? []).join('\n')}>{(revision.images ?? []).join(', ') || '—'}</div>
              {revision.cause && <div className="truncate text-[11px] text-fg-subtle" title={revision.cause}>{revision.cause}</div>}
            </td>
            <td className="px-2 py-1 tabular-nums text-fg-muted" title={t('revisions.replicas',{ready:revision.ready,total:revision.replicas})}>{revision.ready}/{revision.replicas}</td>
            <td className="px-2 py-1 whitespace-nowrap tabular-nums text-fg-subtle" title={new Date(revision.created).toLocaleString(getLanguage())}>{new Date(revision.created).toLocaleDateString(getLanguage())}</td>
            <td className="px-1 py-1"><div className="flex items-center gap-1">
              <button className="flex h-6 w-6 items-center justify-center rounded text-fg-muted hover:bg-hover focus-visible:outline-2 focus-visible:outline-accent" aria-label={t('revisions.view')} title={t('revisions.view')} onClick={()=>setOpened({revision,compare:false})}><svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" aria-hidden="true"><path d="m5 4-4 4 4 4m6-8 4 4-4 4M9.5 2l-3 12"/></svg></button>
              <button className="flex h-6 w-6 items-center justify-center rounded text-fg-muted hover:bg-hover focus-visible:outline-2 focus-visible:outline-accent" aria-label={t('revisions.compare')} title={t('revisions.compare')} onClick={()=>setOpened({revision,compare:true})}><svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4" aria-hidden="true"><path d="M2 2h5v12H2zM9 2h5v12H9zM3.5 5h2m-2 3h2m5-3h2m-1-1v2m-1 4h2"/></svg></button>
            </div></td>
          </tr>)}
        </tbody>
      </table>
    </div>}
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
