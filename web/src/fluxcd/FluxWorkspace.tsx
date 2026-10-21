import {useCallback,useContext,useEffect,useLayoutEffect,useRef,useState,type ComponentProps,type ReactNode} from 'react'
import type {Client} from '../api/client'
import type {Column,KindDescriptor,Ref,Row,ScopeSel} from '../api/types'
import {t} from '../i18n'
import {memoOf,remember,rememberSort} from '../components/pageMemo'
import {columnWidthsKey} from '../components/columnWidths'
import {persistTargetEntries} from '../components/navigationPersist'
import {showNotice,targetKey} from '../store'
import {errorDetail} from '../errors'
import {localDateTime} from '../dates'
import {mayLeave,useEditHolder,workspaceEditsHeld} from '../edit/guard'
import {ViewConnectionContext} from '../views/useView'
import {useSnapshotRequest} from '../views/useSnapshotRequest'
import {WorkspaceToolbar} from '../components/WorkspaceToolbar'
import {SnapshotBoundary} from '../components/SnapshotBoundary'
import {RefreshButton} from '../components/RefreshButton'
import {ListRefreshProgress} from '../components/ListLoading'
import {ResourceTable} from '../components/ResourceTable'
import {ResourceDrawer} from '../components/ResourceDrawer'
import {LocationStatus} from '../components/LocationStatus'
import {Select} from '../components/Select'
import {FluxSources} from './FluxSources'
import {evidenceKey,shareEvidence} from './evidence'
import {fluxPage,inventoryRef,loadedLink,objectID} from './model'
import type {FluxObject,FluxResource} from './types'

type Props={client:Client;target:{provider:string;id:string};scope:ScopeSel;scopePicker:ReactNode;kinds:KindDescriptor[];workspace:string;onNavigate:(id:string,ref?:Ref)=>void;openRequest?:{value:Ref;seq:number}|null;drawer:Omit<ComponentProps<typeof ResourceDrawer>,'subject'|'onClose'>}
const button='rounded border border-line px-2 py-1 text-xs hover:bg-hover focus-visible:outline-2 focus-visible:outline-accent disabled:opacity-40'
export const stateLabel=(s:string)=>t(s==='Ready'?'flux.ready':s==='Reconciling'?'flux.reconciling':s==='Suspended'?'flux.suspended':s==='Failed'?'flux.failed':'argo.unknown')
const color=(s:string)=>s==='Ready'?'text-success':s==='Failed'?'text-danger':s==='Unknown'?'text-fg-subtle':'text-warning'
export default function FluxWorkspace(props:Props){
 const connection=useContext(ViewConnectionContext);useEditHolder()
 const identity=JSON.stringify([props.target,props.scope,props.workspace,connection]);const[retained,setRetained]=useState(identity)
 if(retained!==identity&&!workspaceEditsHeld())setRetained(identity)
 return <FluxSession key={retained} {...props} connection={connection}/>
}
function FluxSession({client,target,scope,scopePicker,kinds,workspace,onNavigate,openRequest,drawer,connection}:Props&{connection:string|number}){
 const scopeKey=JSON.stringify(scope),mode=workspace==='fluxcd'?'':workspace.slice('fluxcd-'.length)
 const load=useCallback((signal:AbortSignal)=>client.fluxCD({provider:target.provider,target:target.id,scope:JSON.parse(scopeKey)},signal),[client,target.provider,target.id,scopeKey])
 const request=useSnapshotRequest(load,JSON.stringify([target,scopeKey]),{connectionScoped:false}),snapshot=request.value
 const [snapshotConnection]=useState(connection)
 useEffect(()=>{if(snapshot&&snapshotConnection===connection)shareEvidence(client,evidenceKey(target.provider,target.id,connection,JSON.parse(scopeKey)),snapshot)},[client,target.provider,target.id,connection,snapshotConnection,scopeKey,snapshot])
 const [query,setQuery]=useState(''),[state,setState]=useState('all'),[activity,setActivity]=useState(''),[warnings,setWarnings]=useState(false)
 const selectedRef=openRequest?.value,requestedID=selectedRef?.target===target.id&&selectedRef.provider===target.provider&&selectedRef.kind.endsWith('/'+mode)?JSON.stringify([selectedRef.kind,selectedRef.scope,selectedRef.name,selectedRef.uid]):''
 const [selectedID,setSelectedID]=useState(requestedID),[applied,setApplied]=useState(openRequest?.seq)
 if(applied!==openRequest?.seq){setApplied(openRequest?.seq);setSelectedID(requestedID)}
 const [subject,setSubject]=useState<Ref|null>(null),[resolveError,setResolveError]=useState(''),[busy,setBusy]=useState(false)
 const life=useRef({live:true,connection});useLayoutEffect(()=>{const fence={live:true,connection};life.current=fence;return()=>{fence.live=false}},[connection])
 const objects=snapshot?.objects??[],selected=objects.find(o=>objectID(o)===selectedID)
 const rows=objects.filter(o=>o.ref.kind.endsWith('/'+mode)&&(state==='all'||o.state===state)&&[o.ref.name,o.ref.scope,o.kind,o.message,o.revision].join(' ').toLowerCase().includes(query.toLowerCase()))
 const open=(o:FluxObject)=>mayLeave(()=>onNavigate(fluxPage(o.ref),o.ref))
 const resolve=(o:FluxObject,r:FluxResource)=>{
  const ref=inventoryRef(o,r,kinds,scope),fence=life.current;if(!ref||busy)return
  mayLeave(()=>{if(!fence.live)return;setBusy(true);setResolveError('');void client.getResource(ref).then(resource=>{
   if(!fence.live)return
   const fresh=resource.ref;if(!fresh.uid||fresh.provider!==ref.provider||fresh.target!==ref.target||fresh.kind!==ref.kind||(fresh.scope??'')!==(ref.scope??'')||fresh.name!==ref.name)throw new Error(t('argo.resolveFailed'))
   mayLeave(()=>{if(fence.live)setSubject(fresh)})
  }).catch(e=>{if(fence.live)setResolveError(errorDetail(e))}).finally(()=>{if(fence.live)setBusy(false)})})
 }
 const tableKind='ocular.fluxcd/'+mode,tkey=targetKey(target)
 const cols:Column[]=[{id:'name',title:t('argo.name'),type:'text',width:220},{id:'namespace',title:t('argo.namespace'),type:'text',width:140},{id:'state',title:t('flux.state'),type:'status',width:145},{id:'message',title:t('flux.message'),type:'text',width:320},{id:'revision',title:t('argo.observedRevision'),type:'text',width:180},{id:'age',title:t('argo.age'),type:'age',width:100}]
 const tableRows:Row[]=rows.map(o=>({id:objectID(o),ref:o.ref,health:{state:'unknown'},cells:[{text:o.ref.name},{text:o.ref.scope},{text:stateLabel(o.state)},{text:o.message},{text:o.revision},{time:o.createdAt||undefined}]}))
 const incomplete=!!snapshot&&(snapshot.truncated||snapshot.discovery!=='ready'||snapshot.coverage.some(c=>c.state!=='ready'))
 const totalReady=objects.filter(o=>o.state==='Ready').length
 const states=['Ready','Reconciling','Suspended','Failed','Unknown']
 const stateOptions=[{value:'all',label:t('flux.allStates')},...states.map(value=>({value,label:stateLabel(value)}))]
 return <div className="flux-workspace flex min-h-0 min-w-0 flex-1 flex-col">
  <WorkspaceToolbar title={mode||t('argo.dashboard')}>{scopePicker}<ListRefreshProgress view={request.loading} initial={!snapshot}/><RefreshButton className="ml-auto" data-resync busy={request.busy} onClick={request.refresh}/></WorkspaceToolbar>
  <div className="shrink-0 border-b border-line px-3 py-1"><input aria-label={t('flux.search')} placeholder={t('flux.search')} className="w-full min-w-0 rounded border border-line bg-bg px-2 py-1 text-sm" value={query} onChange={e=>setQuery(e.target.value)}/></div>
  {request.error!=null&&snapshot&&<p className="shrink-0 px-3 text-xs text-warning">{t('argo.stale')}</p>}
  {request.error!=null&&<p role="alert" className="shrink-0 px-3 py-2 text-xs text-danger">{errorDetail(request.error)}</p>}
  <p className="shrink-0 border-b border-line px-3 py-2 text-xs text-fg-muted">{t('flux.notice')}</p>
  <div className="shrink-0 border-b border-line px-3 py-1"><Select value={state} onChange={setState} options={stateOptions} label={t('flux.state')}/></div>
  <SnapshotBoundary request={request}>
   {!mode?<div className="min-h-0 min-w-0 flex-1 overflow-auto p-3">
    <p role="status" className="mb-3 text-sm">{t('flux.ready')}: {totalReady} / {objects.length}{incomplete?' · '+t('argo.partial'):''}</p>
    <div className="grid gap-2 sm:grid-cols-2 xl:grid-cols-3">{kinds.filter(k=>k.workspace?.startsWith('fluxcd-')).map(descriptor=>{const kind=descriptor.title,group=objects.filter(o=>o.ref.kind.endsWith('/'+descriptor.workspace!.slice(7)));return <button key={descriptor.id} className="min-w-0 rounded border border-line bg-panel p-3 text-left hover:bg-hover" onClick={()=>mayLeave(()=>onNavigate(descriptor.id))}><span className="text-sm font-semibold">{kind}</span><p className="mt-2 text-sm">{group.filter(o=>o.state==='Ready').length} / {group.length} · {t('flux.ready')}</p></button>})}</div>
    <section className="mt-4" aria-label={t('argo.attention')}><h3 className="mb-2 text-sm font-semibold">{t('argo.attention')}</h3>{objects.filter(o=>o.state!=='Ready'&&(state==='all'||o.state===state)&&[o.ref.name,o.ref.scope,o.kind,o.message].join(' ').toLowerCase().includes(query.toLowerCase())).map(o=><button key={objectID(o)} className="flex w-full min-w-0 items-start justify-between gap-2 border-b border-line py-2 text-left text-xs hover:bg-hover" onClick={()=>open(o)}><span className="min-w-0 break-words text-accent">{o.kind} / {o.ref.scope}/{o.ref.name}<span className="block text-fg-muted">{o.message}</span></span><span className={color(o.state)}>{stateLabel(o.state)}</span></button>)}</section>
    <section className="mt-4" aria-label={t('argo.activity')}><h3 className="mb-2 text-sm font-semibold">{t('argo.activity')}</h3><input aria-label={t('argo.activitySearch')} value={activity} onChange={e=>setActivity(e.target.value)} className="w-full min-w-0 rounded border border-line bg-bg px-2 py-1 text-xs"/><label className="mt-2 flex items-center gap-2 text-xs"><input type="checkbox" checked={warnings} onChange={e=>setWarnings(e.target.checked)}/>{t('flux.warningsOnly')}</label>{snapshot?.events.filter(e=>(!warnings||e.type==='Warning')&&[e.ref.kind,e.ref.scope,e.ref.name,e.reason,e.message].join(' ').toLowerCase().includes(activity.toLowerCase())).map(e=><div key={e.id} className="border-b border-line py-2 text-xs"><span className="text-fg-muted">{localDateTime(e.at)}</span> · <span className={e.type==='Warning'?'text-warning':''}>{e.reason}</span> · {e.openable?<button className="text-accent hover:underline" onClick={()=>{const o=objects.find(o=>JSON.stringify(o.ref)===JSON.stringify(e.ref));if(o)open(o)}}>{e.ref.scope}/{e.ref.name}</button>:<span>{e.ref.scope}/{e.ref.name}</span>}<p className="mt-1 break-words">{e.message}</p></div>)}</section>
   </div>:<div className="flex min-h-0 min-w-0 flex-1 flex-col"><div className="flex min-w-0 flex-1 flex-col"><ResourceTable initialSort={memoOf(tkey).sorts[tableKind]} onSort={sort=>rememberSort(tkey,tableKind,sort)} initialWidths={memoOf(tkey).columnWidths?.[tableKind]} onWidths={widths=>{remember(tkey,{columnWidths:{...memoOf(tkey).columnWidths,[tableKind]:widths}});void persistTargetEntries(client,target.provider,target.id,{[columnWidthsKey(tableKind)]:JSON.stringify(widths)}).catch(()=>showNotice(t('table.widthsNotSaved')))}} hideScope={scope.mode==='one'} filter={query} ariaLabel={t('flux.resources')} columns={cols} rows={tableRows} selected={selectedID} onSelect={r=>setSelectedID(r.id)} onOpen={r=>setSelectedID(r.id)} renderCell={(_r,c,cell)=>c.id==='state'?<span className={color(objects.find(o=>objectID(o)===_r.id)?.state??'Unknown')}>{cell?.text}</span>:undefined}/></div>
    {selected&&<aside aria-label={t('flux.details')} className="max-h-[45%] min-w-0 shrink-0 overflow-auto border-t border-line bg-panel p-3 text-xs">
     <div className="flex items-start justify-between gap-2"><h3 className="min-w-0 break-words text-sm font-semibold">{selected.kind} / {selected.ref.name}</h3><button className={button} onClick={()=>mayLeave(()=>setSelectedID(''))}>{t('drawer.close')}</button></div>
     <p className={'mt-2 '+color(selected.state)}>{stateLabel(selected.state)}</p><p className="mt-1 break-words">{selected.message}</p><p className="mt-2 text-fg-muted">{t('flux.generation')}: {selected.observedGeneration} / {selected.generation}</p>
     <button className={button+' mt-3'} onClick={()=>mayLeave(()=>setSubject(selected.ref))}>{t('flux.inspect')}</button>
     {selected.truncated&&<p className="mt-2 text-warning">{t('argo.partial')}</p>}
     <h4 className="mt-4 font-semibold">{t('flux.conditions')}</h4>{selected.conditions.map((c,i)=><p key={i} className="mt-2 break-words"><strong>{c.type}: {c.status} · {c.reason}</strong><br/>{c.message}</p>)}
     <FluxSources object={selected}/>
     <h4 className="mt-4 font-semibold">{t('flux.references')}</h4>{selected.links.map((link,i)=>{const target=loadedLink(link,objects,scope,selected.ref);return <p key={i} className="mt-2 break-words">{target?<button className="text-left text-accent hover:underline" onClick={()=>open(target)}>{link.kind} / {link.namespace}/{link.name}</button>:<span className="text-fg-muted">{link.kind} / {link.namespace}/{link.name} · {t('argo.unknown')}</span>}</p>})}
     <h4 className="mt-4 font-semibold">{t('flux.usedBy')}</h4>{objects.filter(o=>o.links.some(l=>loadedLink(l,objects,scope,o.ref)===selected)).map(o=><p key={objectID(o)} className="mt-2"><button className="text-left text-accent hover:underline" onClick={()=>open(o)}>{o.kind} / {o.ref.scope}/{o.ref.name}</button></p>)}
     <h4 className="mt-4 font-semibold">{t('flux.inventory')}</h4>{!selected.localDestination&&<p className="mt-2 text-fg-muted">{t('flux.remote')}</p>}{selected.inventory.map((r,i)=><p key={i} className="mt-2 break-words"><button className="text-left text-accent hover:underline disabled:text-fg-subtle" disabled={busy||!inventoryRef(selected,r,kinds,scope)} onClick={()=>resolve(selected,r)}>{r.kind} / {r.namespace}/{r.name}</button></p>)}{resolveError&&<p role="alert" className="mt-2 text-danger">{resolveError}</p>}
    </aside>}
   </div>}
   {subject&&<ResourceDrawer {...drawer} subject={subject} onClose={()=>mayLeave(()=>setSubject(null))}/>}
  </SnapshotBoundary>
  <details className="shrink-0 border-t border-line px-3 py-2 text-xs"><summary>{t('argo.coverage')}{incomplete?' · '+t('argo.partial'):''}</summary><div className="max-h-32 overflow-auto pt-2">{snapshot?.coverage.map((c,i)=><p key={i}>{c.kind} / {c.scope||t('scope.generic.all')}: {c.state}</p>)}</div></details>
  <LocationStatus><span>Flux / {mode||t('argo.dashboard')}</span><span>{mode?rows.length:objects.length} / {objects.length}</span></LocationStatus>
 </div>
}
