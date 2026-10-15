import { RefreshButton } from '../components/RefreshButton'
import { useEffect, useState, type ComponentProps, type MouseEvent, type ReactNode } from 'react'
import type { Client } from '../api/client'
import type { Column, Row, Ref, ScopeSel, SecurityFinding, SecuritySnapshot } from '../api/types'
import { getLanguage, t } from '../i18n'
import { mayLeave } from '../edit/guard'
import { ResourceTable } from '../components/ResourceTable'
import { columnWidthsKey } from '../components/columnWidths'
import { memoOf, remember, rememberSort } from '../components/pageMemo'
import { persistTargetEntries } from '../components/navigationPersist'
import { showNotice, targetKey } from '../store'
import { ResourceDrawer } from '../components/ResourceDrawer'
import { Select } from '../components/Select'
import { LinkArrowIcon } from '../components/icons'
import { openURL } from '../tunnels/open'
import { filterFindings, findingTitle, recommendation, severities } from './model'

type Drawer = Omit<ComponentProps<typeof ResourceDrawer>, 'subject' | 'onClose'>
interface Props {client: Client; target: {provider: string; id: string}; scope: ScopeSel; scopePicker: ReactNode; drawer: Drawer}
const button='rounded border border-line px-2 py-1 text-xs hover:bg-hover focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-40'
const severityColors={critical:'text-danger',high:'text-warning',medium:'text-fg',low:'text-fg-muted',unknown:'text-fg-subtle'}
export const installURL='https://aquasecurity.github.io/trivy-operator/latest/getting-started/installation/helm/'
export const accessURL='https://aquasecurity.github.io/trivy-operator/latest/tutorials/manage_access_to_security_reports/'
const tableKind='ocular.security'

export default function SecurityWorkspace({client,target,scope,scopePicker,drawer}: Props) {
 const [saved,setSaved]=useState<{key:string;value:SecuritySnapshot}|null>(null)
 const [busy,setBusy]=useState(true)
 const [error,setError]=useState('')
 const [linkError,setLinkError]=useState('')
 const [revision,setRevision]=useState(0)
 const [query,setQuery]=useState('')
 const [category,setCategory]=useState('')
 const [severity,setSeverity]=useState('')
 const [selectedID,setSelectedID]=useState('')
 const [subject,setSubject]=useState<Ref|null>(null)
 const [mode,setMode]=useState<'desktop'|'browser'>('browser')
 const scopeKey=JSON.stringify(scope)
 const key=target.provider+'/'+target.id+'/'+scopeKey
 const snapshot=saved?.key===key?saved.value:null
 const date=(at:number)=>at>0?new Date(at).toLocaleString(getLanguage()):t('security.unknown')
 useEffect(()=>{let live=true;client.appInfo().then(info=>{if(live)setMode(info.mode)},()=>{});return()=>{live=false}},[client])
 useEffect(()=>{
  let live=true;const abort=new AbortController()
  client.clusterSecurity({provider:target.provider,target:target.id,scope:JSON.parse(scopeKey)},abort.signal).then(value=>{
   if(live){setSaved({key,value});setError('');setBusy(false)}
  },e=>{if(live){setError(e instanceof Error?e.message:String(e));setBusy(false)}})
  return()=>{live=false;abort.abort()}
 },[client,target.provider,target.id,scopeKey,key,revision])
 const rows=filterFindings(snapshot?.findings??[],query,category,severity)
 const tkey=targetKey(target)
 const findings=new Map(rows.map(f=>[f.id,f]))
 const columns:Column[]=[
  {id:'severity',title:t('security.severity'),type:'status',width:128},
  {id:'name',title:t('security.workload'),type:'text',width:240},
  {id:'finding',title:t('security.finding'),type:'text'},
  {id:'namespace',title:t('security.namespace'),type:'text',width:140},
  {id:'rule',title:t('security.rule'),type:'text',width:180},
  {id:'container',title:t('security.container'),type:'text',width:140},
  {id:'source',title:t('security.source'),type:'text',width:160},
  {id:'open',title:t('security.open'),type:'text',width:64},
 ]
 const tableRows:Row[]=rows.map(f=>({id:f.id,ref:f.ref,health:{state:'unknown'},cells:[
  {text:t(`security.${f.severity}`),num:severities.indexOf(f.severity)},
  {text:f.kindTitle+'/'+(f.ref.title??f.ref.name)}, {text:findingTitle(f)},
  {text:f.ref.scope||t('graph.clusterResources')}, {text:f.rule}, {text:f.container||'—'},
  {text:f.scanner||t(`security.${f.category}`)}, {text:''},
 ]}))
 const selected=rows.find(f=>f.id===selectedID)
 const counts=Object.fromEntries(severities.map(level=>[level,(snapshot?.findings??[]).filter(f=>f.severity===level).length]))
 const open=(ref:Ref)=>mayLeave(()=>setSubject(ref))
 const changeFilter=(act:()=>void)=>act()
 const external=(event:MouseEvent<HTMLAnchorElement>)=>{
  if(mode!=='desktop')return
  event.preventDefault();setLinkError('')
  void openURL(event.currentTarget.href,mode).catch(e=>setLinkError(t('about.linkFailed',{error:e instanceof Error?e.message:String(e)})))
 }
 const explanation=(f:SecurityFinding)=><div className="max-h-52 overflow-auto border-t border-line bg-panel px-3 py-2 text-xs" aria-label={t('security.explain')}>
  <div className="flex items-center gap-3"><strong>{findingTitle(f)}</strong><button className={'ml-auto '+button} onClick={()=>setSelectedID('')}>{t('drawer.close')}</button></div>
  <p className="mt-2 break-words"><span className="text-fg-muted">{t('security.evidence')}: </span>{f.evidence}</p>
  {f.category==='cve'?<>
   {f.workload&&<p className="mt-1 text-fg-muted">{t('security.workload')}: {f.workload}</p>}
   <p className="mt-1 break-words">{f.image} · {f.package}</p>
   <p className="mt-1">{t('security.installed')}: {f.installed||'—'} · {t('security.fixed')}: {f.fixed||t('security.noFix')}</p>
   <p className="mt-1 text-fg-muted">{f.scanner} · {t('security.reportAge',{time:date(f.scannedAt??0)})}</p>
   <p className="mt-1 text-fg-muted">{t('security.cveFreshness')}</p>
  </>:<p className="mt-2"><span className="text-fg-muted">{t('security.recommendation')}: </span>{recommendation(f)}</p>}
  {f.category==='network'&&<p className="mt-1 text-fg-muted">{t('security.networkNotice')}</p>}
  {f.category==='rbac'&&<p className="mt-1 text-fg-muted">{t('security.rbacNotice')}</p>}
 </div>
 return <section className="security-workspace flex min-h-0 flex-1 flex-col" aria-label={t('security.title')}>
  <header className="flex min-h-10 shrink-0 flex-wrap items-center gap-2 border-b border-line bg-panel px-3 py-1">
   <h2 className="text-sm font-semibold">{t('security.title')}</h2>{scopePicker}
   <RefreshButton data-resync busy={busy} onClick={()=>{setBusy(true);setRevision(r=>r+1)}} />
   <span className="ml-auto text-xs text-fg-muted">{snapshot?t('security.snapshot',{time:date(snapshot.capturedAt)}):''}</span>
  </header>
  <div className="relative flex min-h-0 flex-1">
   <div className="flex min-w-0 flex-1 flex-col">
    <div className="grid shrink-0 grid-cols-2 gap-2 border-b border-line p-3 lg:grid-cols-4" aria-label={t('security.severity')}>
     <div className="rounded border border-line bg-panel px-3 py-2"><p className="text-xs text-fg-muted">{t('security.resources')}</p><strong className="text-xl tabular-nums">{snapshot?snapshot.resources:'—'}</strong></div>
     <div className="rounded border border-line bg-panel px-3 py-2"><p className="text-xs text-fg-muted">{t('security.reports')}</p><strong className="text-xl tabular-nums">{snapshot?snapshot.reports:'—'}</strong></div>
     {(['critical','high'] as const).map(level=><button key={level} className={'rounded border border-line bg-panel px-3 py-2 text-left hover:bg-hover focus-visible:outline-2 focus-visible:outline-accent '+severityColors[level]} onClick={()=>changeFilter(()=>setSeverity(level))}><p className="text-xs">{t(`security.${level}`)}</p><strong className="text-xl tabular-nums">{snapshot?counts[level]:'—'}</strong></button>)}
    </div>
    <p className="shrink-0 px-3 py-2 text-xs text-fg-muted">{t('security.notice')}</p>
    {snapshot&&snapshot.cveState!=='available'&&<div role="status" data-security-cve-status className="shrink-0 border-y border-line bg-panel px-3 py-2 text-xs">
     <p>{t(`security.cve.${snapshot.cveState}`)}</p>
     {(snapshot.cveState==='missing'||snapshot.cveState==='empty'||snapshot.cveState==='denied')&&<div className="mt-1 flex flex-wrap gap-x-4 gap-y-1">
      {snapshot.cveState!=='denied'&&<a href={installURL} target="_blank" rel="noopener noreferrer" onClick={external} className="text-accent hover:underline">{t('security.install')}</a>}
      <a href={accessURL} target="_blank" rel="noopener noreferrer" onClick={external} className="text-accent hover:underline">{t('security.access')}</a>
     </div>}
    </div>}
    {linkError&&<p role="alert" className="px-3 py-1 text-xs text-danger">{linkError}</p>}
    <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-line px-3 py-2 text-xs">
     <input className="min-w-40 flex-1 rounded border border-line bg-panel px-2 py-1 outline-none focus:border-accent" aria-label={t('security.search')} placeholder={t('security.search')} value={query} onChange={e=>changeFilter(()=>setQuery(e.target.value))} onKeyDown={e=>{if(e.key==='Escape'){e.stopPropagation();changeFilter(()=>setQuery(''))}}}/>
     <Select label={t('security.source')} value={category} options={[{value:'',label:t('security.all')},...(['configuration','rbac','network','cve'] as const).map(value=>({value,label:t(`security.${value}`)}))]} onChange={value=>changeFilter(()=>setCategory(value))}/>
     <Select label={t('security.severity')} value={severity} options={[{value:'',label:t('security.allSeverities')},...severities.map(value=>({value,label:t(`security.${value}`)}))]} onChange={value=>changeFilter(()=>setSeverity(value))}/>
    </div>
    {error&&<p role="alert" className="shrink-0 border-b border-line px-3 py-2 text-xs text-danger">{error}{snapshot?' · '+t('security.stale'):''}</p>}
    <div data-area="table" className="flex min-h-0 min-w-0 flex-1">
     <ResourceTable columns={columns} rows={tableRows} hideScope={false} filter="" selected={selectedID||null}
      ariaLabel={t('security.finding')} areaFocus defaultSort={{column:'severity'}}
      initialWidths={memoOf(tkey).columnWidths?.[tableKind]} initialSort={memoOf(tkey).sorts[tableKind]}
      onSort={sort=>rememberSort(tkey,tableKind,sort)}
      onWidths={widths=>{
       remember(tkey,{columnWidths:{...memoOf(tkey).columnWidths,[tableKind]:widths}})
       void persistTargetEntries(client,target.provider,target.id,{[columnWidthsKey(tableKind)]:JSON.stringify(widths)}).catch(()=>showNotice(t('table.widthsNotSaved')))
      }}
      onSelect={row=>setSelectedID(row.id)} onOpen={row=>{if(row.ref.kind&&row.ref.uid)open(row.ref)}}
      emptyMessage={busy&&!snapshot?t('app.loading'):!snapshot?null:snapshot.findings.length?t('security.noMatches'):(snapshot.truncated||snapshot.problems.length||snapshot.discovery!=='ready'?t('security.incompleteEmpty'):t('security.empty'))}
      renderCell={(row,column)=>{
       const f=findings.get(row.id)!
       const openLabel=t('security.open')+' · '+f.kindTitle+'/'+(f.ref.title??f.ref.name)
       if(column.id==='severity')return <span className={severityColors[f.severity]}>{t(`security.${f.severity}`)}</span>
       if(column.id==='name')return <button disabled={!f.ref.kind||!f.ref.uid} className="block w-full truncate text-left outline-none hover:text-accent focus-visible:text-accent disabled:opacity-40" aria-label={openLabel} onClick={e=>{e.stopPropagation();open(f.ref)}}>{f.kindTitle+'/'+(f.ref.title??f.ref.name)}</button>
       if(column.id==='finding')return <button className="block w-full truncate text-left outline-none hover:text-accent focus-visible:text-accent" aria-label={t('security.explain')+' · '+f.rule+' · '+f.ref.name} onClick={e=>{e.stopPropagation();setSelectedID(f.id)}}>{findingTitle(f)}</button>
       if(column.id==='rule')return <span className="font-mono text-[13px]">{f.rule}</span>
       if(column.id==='open')return <button disabled={!f.ref.kind||!f.ref.uid} aria-label={openLabel} title={openLabel} className="flex h-6 w-6 items-center justify-center rounded text-fg-muted hover:bg-hover hover:text-accent focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-40" onClick={e=>{e.stopPropagation();open(f.ref)}}><LinkArrowIcon className="h-[14px] w-[14px]"/></button>
       return undefined
      }}/>
    </div>
    {selected&&explanation(selected)}
    <footer className="flex shrink-0 flex-wrap items-center gap-2 border-t border-line px-3 py-2 text-xs text-fg-muted">
     <span>{t('security.counts',{shown:rows.length,total:snapshot?.findings.length??0})}</span>
     {snapshot?.discovery!=='ready'&&snapshot&&<span className="text-warning">{t('graph.discovery')}</span>}
     {snapshot?.truncated&&<span role="status" className="text-warning">{t('security.truncated')}</span>}

    </footer>
    {!!snapshot?.problems.length&&<details className="shrink-0 border-t border-line px-3 py-1 text-xs text-warning"><summary>{t('graph.partial',{count:snapshot.problems.length})}</summary><ul className="max-h-24 overflow-y-auto">{snapshot.problems.map((p,i)=><li key={i}>{p.kind}{p.scope?' / '+p.scope:''} · {p.class}</li>)}</ul></details>}
   </div>
   {subject&&<ResourceDrawer {...drawer} subject={subject} onClose={()=>setSubject(null)}/>}
  </div>
 </section>
}
