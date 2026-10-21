import { useCallback, useContext, useEffect, useLayoutEffect, useRef, useState, type ComponentProps, type ReactNode } from 'react'
import {isDesktop,type Client} from '../api/client'
import type { Column, KindDescriptor, Ref, Row, ScopeSel } from '../api/types'
import { actionLabel, t } from '../i18n'
import { errorDetail } from '../errors'
import { localDateTime } from '../dates'
import { mayLeave, useEditHolder, workspaceEditsHeld } from '../edit/guard'
import { WorkspaceToolbar } from '../components/WorkspaceToolbar'
import { SnapshotBoundary } from '../components/SnapshotBoundary'
import { ListRefreshProgress } from '../components/ListLoading'
import { RefreshButton } from '../components/RefreshButton'
import { ResourceTable } from '../components/ResourceTable'
import { ResourceDrawer } from '../components/ResourceDrawer'
import { LocationStatus } from '../components/LocationStatus'
import { Select } from '../components/Select'
import { memoOf, remember, rememberSort } from '../components/pageMemo'
import { columnWidthsKey } from '../components/columnWidths'
import { persistTargetEntries } from '../components/navigationPersist'
import { showNotice, targetKey } from '../store'
import { ActionDialog, type ActionRequest } from '../actions/ActionDialog'
import { BulkActionDialog, type BulkRequest } from '../actions/BulkActionDialog'
import {argoUIForward} from './openUI'
import {openURL} from '../tunnels/open'
import {tunnels,tunnelURL} from '../tunnels/store'
import { PortsSection } from '../tunnels/Ports'
import { ViewConnectionContext } from '../views/useView'
import { useSnapshotRequest } from '../views/useSnapshotRequest'
import { evidenceKey, shareEvidence } from './evidence'
import { argoMode } from './pages'
import { ArgoSources } from './ArgoSources'
import { ArgoDashboard } from './ArgoDashboard'
import { ArgoSettings } from './ArgoSettings'
import { attention, objectID, resourceRef } from './model'
import type { ArgoObject, ArgoResource } from './types'

type Drawer = Omit<ComponentProps<typeof ResourceDrawer>, 'subject'|'onClose'>
interface Props {client:Client; target:{provider:string;id:string;configRev?:string};scope:ScopeSel;scopePicker:ReactNode;kinds:KindDescriptor[];drawer:Drawer;workspace?:string;onNavigate?:(id:string,ref?:Ref)=>void;openRequest?:{value:Ref;seq:number}|null}
const button='rounded border border-line px-2 py-1 text-xs hover:bg-hover focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-40'
const modeKey={Application:'argo.applications',ApplicationSet:'argo.sets',AppProject:'argo.projects',dashboard:'argo.dashboard',Repository:'argo.repositories',Cluster:'argo.clusters'} as const
const known=(value:string)=>!value||value==='Unknown'?t('argo.unknown'):value
const statusColor=(value:string)=>value==='Synced'||value==='Healthy'||value==='Succeeded'?'text-success':value==='Degraded'||value==='Failed'||value==='Error'?'text-danger':value==='Unknown'||!value?'text-fg-subtle':'text-warning'

export default function ArgoWorkspace(props:Props) {
 const connection=useContext(ViewConnectionContext)
 useEditHolder()
 const identity=JSON.stringify([props.target.provider,props.target.id,props.scope,props.workspace,connection])
 const [retained,setRetained]=useState(identity)
 if(retained!==identity&&!workspaceEditsHeld())setRetained(identity)
 return props.workspace==='argocd-repositories'||props.workspace==='argocd-clusters'?<ArgoSettings key={retained} {...props} mode={props.workspace==='argocd-repositories'?'Repository':'Cluster'}/>:<ArgoSession key={retained} {...props} connection={connection}/>
}
function ArgoSession({client,target,scope,scopePicker,kinds,drawer,connection,workspace,onNavigate,openRequest}:Props & {connection:string|number}) {
 const mode=argoMode(workspace??'argocd')
 const [query,setQuery]=useState('')
 const [onlyAttention,setOnlyAttention]=useState(false)
 const refKind=mode==='Application'?'argoproj.io/applications':mode==='ApplicationSet'?'argoproj.io/applicationsets':mode==='AppProject'?'argoproj.io/appprojects':''
 const selectedRef=openRequest?.value.kind===refKind?openRequest.value:undefined
 const requestedID=selectedRef?.target===target.id&&selectedRef.provider===target.provider?JSON.stringify([selectedRef.kind,selectedRef.scope,selectedRef.name,selectedRef.uid]):''
 const [selectedID,setSelectedID]=useState(requestedID)
 const [applied,setApplied]=useState(openRequest?.seq)
 if(applied!==openRequest?.seq){setApplied(openRequest?.seq);setSelectedID(requestedID)}
 const [marked,setMarked]=useState<ReadonlySet<string>>(new Set())
 const [subject,setSubject]=useState<Ref|null>(null)
 const [action,setAction]=useState<ActionRequest|null>(null)
 const [bulk,setBulk]=useState<BulkRequest|null>(null)
 const [server,setServer]=useState('')
 const [uiOpen,setUIOpen]=useState(false),[uiContext,setUIContext]=useState<string|number|null>(null),[uiError,setUIError]=useState('')
 const uiBusy=uiContext===connection
 const openingUI=useRef<object|null>(null)
 const [resolveError,setResolveError]=useState('')
 const lifetime=useRef({live:true,connection})
 const [resolvingContext,setResolvingContext]=useState<{connection:string|number}|null>(null)
 const resolving=resolvingContext?.connection===connection
 const scopeKey=JSON.stringify(scope)
 const key=JSON.stringify([target.provider,target.id,scopeKey])
 const resolvingID=useRef(0)
 useLayoutEffect(()=>{const fence={live:true,connection};lifetime.current=fence;return()=>{fence.live=false}},[connection])
 const load=useCallback((signal:AbortSignal)=>client.argoCD({provider:target.provider,target:target.id,scope:JSON.parse(scopeKey)},signal),[client,target.provider,target.id,scopeKey])
 const request=useSnapshotRequest(load,key,{connectionScoped:false})
 const [snapshotConnection]=useState(connection)
 const snapshot=request.value
 useEffect(()=>{if(snapshot&&snapshotConnection===connection)shareEvidence(client,evidenceKey(target.provider,target.id,connection,JSON.parse(scopeKey)),snapshot)},[client,target.provider,target.id,connection,snapshotConnection,scopeKey,snapshot])
 const objects=snapshot?.objects??[]
 const rows=objects.filter(o=>o.kind===mode&&(!onlyAttention||attention(o))&&[o.ref.name,o.ref.scope,o.project,o.destination,...o.sources.map(s=>s.repository)].join(' ').toLowerCase().includes(query.toLowerCase()))
 const byID=new Map(rows.map(o=>[objectID(o),o]))
 const selected=objects.find(o=>objectID(o)===selectedID)
 const actions=selected&&mode==='Application'?(drawer.actionsOf?.(selected.ref.kind)??[]).filter(a=>a.id.startsWith('argo-')):[]
 const tableKind='ocular.argocd/'+mode,tkey=targetKey(target)
 const definitions:Record<string,Column>={name:{id:'name',title:t('argo.name'),type:'text',width:220},namespace:{id:'namespace',title:t('argo.namespace'),type:'text',width:140},project:{id:'project',title:t('argo.project'),type:'text',width:140},sync:{id:'sync',title:t('argo.sync'),type:'status',width:120},health:{id:'health',title:t('argo.health'),type:'status',width:120},destination:{id:'destination',title:t('argo.destination'),type:'text'},revision:{id:'revision',title:t('argo.observedRevision'),type:'text',width:150},age:{id:'age',title:t('argo.age'),type:'age',width:100},generators:{id:'generators',title:t('argo.generators'),type:'text'},description:{id:'description',title:t('argo.description'),type:'text'},repositories:{id:'repositories',title:t('argo.sourceRepos'),type:'number',width:130}}
 const columnIDs=mode==='ApplicationSet'?['name','namespace','generators','age']:mode==='AppProject'?['name','namespace','description','repositories','age']:['name','namespace','sync','health','project','destination','revision','age']
 const columns=columnIDs.map(id=>definitions[id])
 const tableRows:Row[]=rows.map(o=>{
 const revision=o.sources.map(s=>s.observedRevision).filter(Boolean).join(', ')
 const values:Record<string,Row['cells'][number]>={name:{text:o.ref.name},namespace:{text:o.ref.scope},project:{text:o.project||'—'},sync:{text:known(o.sync)},health:{text:known(o.health)},destination:{text:[o.destination,o.destinationNamespace].filter(Boolean).join(' / ')||'—'},revision:{text:revision||t('argo.unknown')},age:{time:o.createdAt||undefined},generators:{text:o.generators.join(', ')||'—'},description:{text:o.description||'—'},repositories:{text:(o.truncated?'≥ ':'')+o.sources.length,num:o.sources.length}}
 return {id:objectID(o),ref:o.ref,health:{state:'unknown'},cells:columnIDs.map(id=>values[id])}
 })
 const resolve=(o:ArgoObject,r:ArgoResource)=>{
  const fence=lifetime.current
  const ref=resourceRef(o,r,kinds,scope)
  if(!ref||resolving)return
  mayLeave(()=>{
   if(!fence.live)return
   const id=++resolvingID.current;setResolvingContext(fence);setResolveError('')
   void client.getResource(ref).then(resource=>{
    if(!fence.live||id!==resolvingID.current)return
    const fresh=resource.ref
    if(!fresh.uid||fresh.provider!==ref.provider||fresh.target!==ref.target||fresh.kind!==ref.kind||(fresh.scope??'')!==(ref.scope??'')||fresh.name!==ref.name)throw new Error(t('argo.resolveFailed'))
    mayLeave(()=>{if(fence.live&&id===resolvingID.current)setSubject(fresh)})
   }).catch(e=>{if(fence.live&&id===resolvingID.current)setResolveError(errorDetail(e))}).finally(()=>{if(fence.live&&id===resolvingID.current)setResolvingContext(null)})
  })
 }
 const currentServer=snapshot?.servers.find(s=>JSON.stringify(s)===server)
 const openUI=(explicit?:Ref)=>{
  const serviceCoverage=snapshot?.coverage.filter(c=>c.kind==='Service')??[]
  const only=snapshot?.servers.length===1&&!snapshot.truncated&&snapshot.discovery==='ready'&&serviceCoverage.length>0&&serviceCoverage.every(c=>c.state==='ready')?snapshot.servers[0]:undefined
  const chosen=explicit??currentServer??only
  setUIOpen(true)
  if(!chosen)return
  setServer(JSON.stringify(chosen))
  const fence=lifetime.current
  if(openingUI.current===fence)return
  openingUI.current=fence;setUIContext(connection);setUIError('')
  const current=()=>fence.live
  void argoUIForward(client,chosen,target.configRev,current).then(async forward=>{
   if(!forward||!current())return
   tunnels.showPanel(true)
   await openURL(tunnelURL(forward),isDesktop()?'desktop':'browser')
   if(current())setUIOpen(false)
  }).catch(e=>{if(current())setUIError(errorDetail(e))}).finally(()=>{if(openingUI.current===fence)openingUI.current=null;if(current())setUIContext(null)})
 }

 return <section className="argo-workspace flex min-h-0 min-w-0 flex-1 flex-col" aria-label="Argo CD">
  <WorkspaceToolbar title={t(modeKey[mode])}>{scopePicker}<ListRefreshProgress view={request.loading} initial={!snapshot}/><RefreshButton busy={request.busy} data-resync onClick={request.refresh}/><span className="ml-auto text-xs text-fg-muted">{snapshot?localDateTime(snapshot.capturedAt):''}</span></WorkspaceToolbar>
  <SnapshotBoundary request={request}>
   <div className="relative flex min-h-0 min-w-0 flex-1">
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
     <p className="shrink-0 border-b border-line px-3 py-2 text-xs text-fg-muted">{t('argo.notice')}</p>
     {request.error!=null&&<p role="alert" className="px-3 py-2 text-xs text-danger">{errorDetail(request.error)} {snapshot?t('argo.stale'):''}</p>}
     {mode==='dashboard'?<ArgoDashboard snapshot={snapshot} onNavigate={onNavigate} onUI={()=>openUI()} uiBusy={uiBusy} onObject={ref=>setSelectedID(JSON.stringify([ref.kind,ref.scope,ref.name,ref.uid]))} onBulk={req=>mayLeave(()=>setBulk(req))} actions={drawer.actionsOf?.('argoproj.io/applications')??[]}/>:<>

      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-line p-2 text-xs">
       <input aria-label={t('argo.search')} placeholder={t('argo.search')} className="min-w-28 flex-1 rounded border border-line bg-panel px-2 py-1" value={query} onChange={e=>setQuery(e.target.value)} onKeyDown={e=>{if(e.key==='Escape'){e.stopPropagation();setQuery('')}}}/>
       {mode==='Application'&&<label className="flex items-center gap-1"><input type="checkbox" checked={onlyAttention} onChange={e=>setOnlyAttention(e.target.checked)}/>{t('argo.attention')}</label>}
       {actions.map(a=><button key={a.id} className={button} onClick={()=>{
        const items=marked.size?rows.filter(o=>marked.has(objectID(o))):selected?[selected]:[]
        if(items.length)mayLeave(()=>{if(items.length===1)setAction({ref:items[0].ref,action:a,kindTitle:'Application'});else setBulk({items:items.map(o=>({id:objectID(o),ref:o.ref})),action:a,kindTitleOf:()=> 'Application'})})
       }}>{actionLabel(a)}{marked.size>1?' · '+marked.size:''}</button>)}
      </div>
      <div data-area="table" className="flex min-h-24 min-w-0 flex-1">
       <ResourceTable key={tableKind} columns={columns} rows={tableRows} hideScope={false} filter="" selected={selectedID||null} marked={mode==='Application'?marked:undefined} onMarked={mode==='Application'?setMarked:undefined} areaFocus ariaLabel={t(modeKey[mode])}
        onSelect={row=>setSelectedID(row.id)} onOpen={row=>setSelectedID(row.id)}
        initialSort={memoOf(tkey).sorts[tableKind]} onSort={sort=>rememberSort(tkey,tableKind,sort)} initialWidths={memoOf(tkey).columnWidths?.[tableKind]} onWidths={widths=>{remember(tkey,{columnWidths:{...memoOf(tkey).columnWidths,[tableKind]:widths}});void persistTargetEntries(client,target.provider,target.id,{[columnWidthsKey(tableKind)]:JSON.stringify(widths)}).catch(()=>showNotice(t('table.widthsNotSaved')))}}
        emptyMessage={snapshot?t('argo.empty'):null} renderCell={(row,col,cell)=>['sync','health'].includes(col.id)?<span className={statusColor(byID.get(row.id)?.[col.id as 'sync'|'health']??'')}>{cell?.text}</span>:undefined}/>
      </div>

     </>}
      {selected&&<div className="max-h-[45%] shrink-0 overflow-auto border-t border-line bg-panel p-3 text-xs" aria-label={t('argo.details')}>
       <div className="flex items-center gap-2"><strong>{selected.kind} / {selected.ref.name}</strong><button className={'ml-auto '+button} onClick={()=>setSelectedID('')}>{t('drawer.close')}</button></div>
       {selected.description&&<p className="mt-2 break-words">{selected.description}</p>}
       {selected.kind==='Application'&&<p className="mt-2 break-words">{t('argo.destination')}: {selected.destination} / {selected.destinationNamespace} · {selected.localDestination?t('argo.local'):t('argo.remote')}</p>}
       {selected.reconciledAt>0&&<p className="mt-1 text-fg-muted">{t('argo.reconciled')}: {localDateTime(selected.reconciledAt)}</p>}
       {selected.operationMessage&&<p className="mt-2 whitespace-pre-wrap break-words">{selected.operationMessage}</p>}
       {!!selected.generators.length&&<p className="mt-2">{t('argo.generators')}: {selected.generators.join(', ')}</p>}
       <ArgoSources sources={selected.sources} application={selected.kind==='Application'}/>
       {selected.conditions.map((c,i)=><p key={i} className="mt-2 whitespace-pre-wrap break-words text-warning"><strong>{c.type}</strong> · {c.message}</p>)}
       {!!selected.resources.length&&<><h3 className="mt-3 font-semibold">{t('argo.resources')}</h3><p className="mt-1 text-fg-muted">{t('argo.resolveNotice')}</p><ul className="mt-1">{selected.resources.map((r,i)=><li key={i} className="flex flex-wrap items-baseline gap-2 border-b border-line py-1"><button className="break-all text-left text-accent hover:underline disabled:text-fg-muted disabled:no-underline" disabled={resolving||!resourceRef(selected,r,kinds,scope)} onClick={()=>resolve(selected,r)}>{r.kind} / {r.namespace?r.namespace+'/':''}{r.name}</button><span className={statusColor(r.sync)}>{known(r.sync)}</span><span className={statusColor(r.health)}>{known(r.health)}</span>{r.message&&<span className="break-words">{r.message}</span>}</li>)}</ul></>}
       {selected.truncated&&<p className="mt-2 text-warning">{t('argo.partial')}</p>}
       {resolveError&&<p role="alert" className="mt-2 text-danger">{resolveError}</p>}
      </div>}
     {!!snapshot?.servers.length&&<details open={uiOpen} onToggle={e=>setUIOpen(e.currentTarget.open)} className="shrink-0 border-t border-line p-2 text-xs"><summary>{t('argo.ui')}</summary><p className="my-2 text-fg-muted">{t('argo.uiNotice')}</p><Select label={t('argo.ui')} value={server} options={[{value:'',label:t('argo.chooseServer')},...snapshot.servers.map(s=>({value:JSON.stringify(s),label:s.scope+'/'+s.name}))]} onChange={setServer}/>{currentServer&&uiOpen&&!uiBusy&&<div className="mt-2"><button className={button} disabled={uiBusy} onClick={()=>openUI(currentServer)}>{t('argo.openUI')}</button><PortsSection key={server} client={client} subject={currentServer}/></div>}</details>}
     {uiError&&<p role="alert" className="px-3 py-2 text-xs text-danger">{uiError}</p>}
     {!!snapshot&&<details className="shrink-0 border-t border-line p-2 text-xs"><summary className={snapshot.truncated||snapshot.discovery!=='ready'||snapshot.coverage.some(c=>!['ready','empty'].includes(c.state))?'text-warning':'text-fg-muted'}>{t('argo.coverage')}{snapshot.truncated?' · '+t('argo.partial'):''} · {snapshot.discovery}</summary><ul className="max-h-24 overflow-auto">{snapshot.coverage.map((c,i)=><li key={i}>{c.kind} / {c.scope||'—'} · {c.state}</li>)}</ul></details>}
    </div>
    {subject&&<ResourceDrawer {...drawer} subject={subject} onClose={()=>setSubject(null)}/>}
   </div>
  </SnapshotBoundary>
  <LocationStatus><span>Argo CD / {t(modeKey[mode])}</span><span>{mode==='dashboard'?objects.length:rows.length} / {mode==='dashboard'?objects.length:objects.filter(o=>o.kind===mode).length}</span>{snapshot&&(snapshot.truncated||snapshot.discovery!=='ready'||snapshot.coverage.some(c=>!['ready','empty'].includes(c.state)))&&<span className="text-warning">{t('argo.partial')}</span>}</LocationStatus>
  {action&&<ActionDialog client={client} req={action} onClose={()=>{setAction(null);request.refresh()}}/>}
  {bulk&&<BulkActionDialog client={client} req={bulk} onClose={()=>{setBulk(null);request.refresh()}} onDone={ids=>setMarked(old=>new Set([...old].filter(id=>!ids.includes(id))))}/>}
 </section>
}
