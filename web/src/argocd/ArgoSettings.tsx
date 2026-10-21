import {useCallback,useState,type ReactNode} from 'react'
import type {Client} from '../api/client'
import type {Column,Row,ScopeSel} from '../api/types'
import {t} from '../i18n'
import {errorDetail} from '../errors'
import {localDateTime} from '../dates'
import {useSnapshotRequest} from '../views/useSnapshotRequest'
import {WorkspaceToolbar} from '../components/WorkspaceToolbar'
import {ListRefreshProgress} from '../components/ListLoading'
import {RefreshButton} from '../components/RefreshButton'
import {SnapshotBoundary} from '../components/SnapshotBoundary'
import {ResourceTable} from '../components/ResourceTable'
import {LocationStatus} from '../components/LocationStatus'
import {memoOf,remember,rememberSort} from '../components/pageMemo'
import {columnWidthsKey} from '../components/columnWidths'
import {persistTargetEntries} from '../components/navigationPersist'
import {showNotice,targetKey} from '../store'
const button='rounded border border-line px-3 py-1 text-xs hover:bg-hover disabled:opacity-40'
export function ArgoSettings({client,target,scope,scopePicker,mode}:{client:Client;target:{provider:string;id:string};scope:ScopeSel;scopePicker:ReactNode;mode:'Repository'|'Cluster'}){
 const [enabled,setEnabled]=useState(false),[query,setQuery]=useState(''),[selected,setSelected]=useState('')
 const scopeKey=JSON.stringify(scope)
 const load=useCallback((signal:AbortSignal)=>client.argoCD({provider:target.provider,target:target.id,scope:JSON.parse(scopeKey),settings:mode==='Repository'?'repository':'cluster'},signal),[client,target.provider,target.id,scopeKey,mode])
 const request=useSnapshotRequest(load,JSON.stringify([target.provider,target.id,scopeKey,mode]),{enabled})
 const snapshot=request.value,settings=(snapshot?.settings??[]).filter(s=>s.kind===mode),filtered=settings.filter(s=>[s.name,s.ref.scope,s.url,s.type,s.project].join(' ').toLowerCase().includes(query.toLowerCase()))
 const id=(s:typeof settings[number])=>JSON.stringify([s.ref.scope,s.ref.name,s.ref.uid])
 const detail=settings.find(s=>id(s)===selected)
 const columns:Column[]=[{id:'name',title:t('argo.name'),type:'text',width:220},{id:'namespace',title:t('argo.namespace'),type:'text',width:140},...(mode==='Repository'?[{id:'type',title:t('argo.type'),type:'text' as const,width:90},{id:'url',title:t('argo.url'),type:'text' as const},{id:'project',title:t('argo.project'),type:'text' as const,width:140}]:[{id:'server',title:t('argo.server'),type:'text' as const}]),{id:'age',title:t('argo.age'),type:'age',width:100}]
 const rows:Row[]=filtered.map(s=>({id:id(s),ref:s.ref,health:{state:'unknown'},cells:[{text:s.name},{text:s.ref.scope},...(mode==='Repository'?[{text:s.type||'—'},{text:s.url||'—'},{text:s.project||'—'}]:[{text:s.url||'—'}]),{time:s.createdAt||undefined}]}))
 const title=t(mode==='Repository'?'argo.repositories':'argo.clusters'),key='ocular.argocd.settings/'+mode,tkey=targetKey(target)
 return <section className="argo-workspace argo-settings flex min-h-0 min-w-0 flex-1 flex-col" aria-label={title}>
  <WorkspaceToolbar title={title}>{scopePicker}<ListRefreshProgress view={request.loading} initial={!snapshot}/><RefreshButton disabled={!enabled} busy={request.busy} data-resync onClick={request.refresh}/><span className="ml-auto text-xs text-fg-muted">{snapshot?localDateTime(snapshot.capturedAt):''}</span></WorkspaceToolbar>
  <p className="shrink-0 border-b border-line p-3 text-xs text-fg-muted">{t('argo.settingsNotice')}</p>
  {!enabled?<div className="p-4"><button className={button} onClick={()=>setEnabled(true)}>{t('argo.loadSettings')}</button></div>:<SnapshotBoundary request={request}><div className="flex min-h-0 min-w-0 flex-1 flex-col">
   {request.error!=null&&<p role="alert" className="p-3 text-xs text-danger">{errorDetail(request.error)} {snapshot?t('argo.stale'):''}</p>}
   <div className="border-b border-line p-2"><input className="w-full rounded border border-line bg-panel px-2 py-1 text-xs" value={query} onChange={e=>setQuery(e.target.value)} aria-label={t('argo.settingsSearch')} placeholder={t('argo.settingsSearch')}/></div>
   <div data-area="table" className="flex min-h-0 min-w-0 flex-1"><ResourceTable columns={columns} rows={rows} hideScope={false} filter="" selected={selected||null} areaFocus ariaLabel={title} onSelect={r=>setSelected(r.id)} onOpen={r=>setSelected(r.id)} emptyMessage={snapshot?t('argo.empty'):null} initialSort={memoOf(tkey).sorts[key]} onSort={sort=>rememberSort(tkey,key,sort)} initialWidths={memoOf(tkey).columnWidths?.[key]} onWidths={widths=>{remember(tkey,{columnWidths:{...memoOf(tkey).columnWidths,[key]:widths}});void persistTargetEntries(client,target.provider,target.id,{[columnWidthsKey(key)]:JSON.stringify(widths)}).catch(()=>showNotice(t('table.widthsNotSaved')))}}/></div>
   {detail&&<div className="max-h-48 overflow-auto border-t border-line bg-panel p-3 text-xs" aria-label={t('argo.details')}><div className="flex"><strong>{detail.name}</strong><button className={'ml-auto '+button} onClick={()=>setSelected('')}>{t('drawer.close')}</button></div><p className="mt-2 break-all text-fg-muted">Secret / {detail.ref.scope}/{detail.ref.name}</p><dl className="mt-2 grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 break-all"><dt>{t('argo.namespace')}</dt><dd>{detail.ref.scope}</dd><dt>{t(mode==='Repository'?'argo.url':'argo.server')}</dt><dd>{detail.url||t('argo.unknown')}</dd>{mode==='Repository'&&<><dt>{t('argo.type')}</dt><dd>{detail.type||'—'}</dd><dt>{t('argo.project')}</dt><dd>{detail.project||'—'}</dd></>}<dt>{t('argo.created')}</dt><dd>{detail.createdAt?localDateTime(detail.createdAt):t('argo.unknown')}</dd></dl>{detail.truncated&&<p className="mt-2 text-warning">{t('argo.partial')}</p>}</div>}
   {snapshot&&<details className="shrink-0 border-t border-line p-2 text-xs"><summary>{t('argo.coverage')}{snapshot.truncated?' · '+t('argo.partial'):''}</summary><ul>{snapshot.coverage.map((c,i)=><li key={i}>{c.kind} / {c.scope||'—'} · {c.state}</li>)}</ul></details>}
  </div></SnapshotBoundary>}
  <LocationStatus><span>Argo CD / {title}</span><span>{filtered.length} / {settings.length}</span>{snapshot&&(snapshot.truncated||snapshot.coverage.some(c=>!['ready','empty'].includes(c.state)))&&<span className="text-warning">{t('argo.partial')}</span>}</LocationStatus>
 </section>
}
