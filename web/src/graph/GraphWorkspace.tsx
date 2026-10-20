import { WorkspaceToolbar } from '../components/WorkspaceToolbar'
import { SnapshotBoundary } from '../components/SnapshotBoundary'
import { ListRefreshProgress } from '../components/ListLoading'
import { useSnapshotRequest } from '../views/useSnapshotRequest'
import { errorDetail } from '../errors'
import { LocationStatus } from '../components/LocationStatus'
import { RefreshButton } from '../components/RefreshButton'
import { useCallback, useEffect, useRef, useState, type ComponentProps, type ReactNode } from 'react'
import type { Client } from '../api/client'
import type { ClusterGraph, Ref } from '../api/types'
import { t } from '../i18n'
import { workspaceEditsHeld as editsHeld, mayLeave, useEditHolder } from '../edit/guard'
import { ResourceDrawer } from '../components/ResourceDrawer'
import { GraphCanvas } from './canvas'
import type { Layout, PlacedNode } from './model'

type Drawer = Omit<ComponentProps<typeof ResourceDrawer>, 'subject' | 'onClose'>
interface Props {
 client: Client
 target: {provider:string;id:string}
 session: number
 connection: number
 scope: import('../api/types').ScopeSel
 scopePicker: ReactNode
 drawer: Drawer
}
const button = 'rounded-md border border-line px-2 py-1 text-xs text-fg-muted hover:bg-hover hover:text-fg disabled:opacity-40'

// Opt-in is deliberately memory-only and tied to the backend incarnation,
// not the saved target or catalog revision. Navigation can remount this page.
const admitted = new WeakMap<Client, Map<string, string>>()
export default function GraphWorkspace(props:Props) {
 const {client,target,session,connection,scopePicker}=props
 useEditHolder()
 const key=JSON.stringify([target.provider,target.id])
 const incarnation=JSON.stringify([session,connection])
 const identity=JSON.stringify([key,incarnation])
 const cached=session>0&&connection>0&&admitted.get(client)?.get(key)===incarnation
 const [requested,setRequested]=useState<string|null>(()=>cached?identity:null)
 // Automatic recovery cannot drop an editor that still owns unsaved work.
 // Once it releases its guard, the replacement connection needs fresh consent.
 const retained=requested!==null&&requested!==identity&&editsHeld()
 if(requested!==null&&requested!==identity&&!retained)setRequested(null)
 const enabled=session>0&&connection>0&&(requested===identity||cached)
 if(!enabled&&!retained)return <section className="graph-workspace flex min-h-0 flex-1 flex-col" aria-label={t('graph.title')}>
  <LocationStatus><span>{t('graph.title')}</span></LocationStatus>
  <WorkspaceToolbar title={t('graph.title')}>{scopePicker}</WorkspaceToolbar>
  <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-4 p-6 text-center">
   <p className="max-w-md text-sm text-fg-muted">{t('graph.loadHint')}</p>
   <button className={button} disabled={session<=0||connection<=0} onClick={()=>{
    let targets=admitted.get(client)
    if(!targets){targets=new Map();admitted.set(client,targets)}
    targets.set(key,incarnation);setRequested(identity)
   }}>{t('graph.load')}</button>
  </div>
 </section>
 return <LoadedGraph key={retained?requested!:identity} {...props}/>
}

function LoadedGraph({client,target,scope,scopePicker,drawer}:Props) {
 const [selected,setSelected]=useState<Ref|null>(null)
 const [routes,setRoutes]=useState(false)
 const [filter,setFilter]=useState('')
 const [hover,setHover]=useState<PlacedNode|null>(null)
 const canvas=useRef<HTMLCanvasElement>(null)
 const engine=useRef<GraphCanvas|null>(null)
 const scopeKey=JSON.stringify(scope)
 const clusterLabel=t('graph.clusterResources')
 const selectedID=selected?.uid??null
 const select=useRef<(node:PlacedNode|null)=>void>(()=>{})
 useEffect(()=>{select.current=node=>{
  if(node?.id===selected?.uid && node)return
  if(!node&&!selected)return
  mayLeave(()=>setSelected(node?.ref??null))
 }})
 useEffect(()=>{
  const el=canvas.current
  if(!el)return
  const view=new GraphCanvas(el,node=>select.current(node),setHover,t('graph.clusterResources'))
  engine.current=view
  return()=>{view.destroy();engine.current=null}
 },[])
 const load=useCallback((signal:AbortSignal)=>new Promise<{graph:ClusterGraph;layout:Layout}>((resolve,reject)=>{
  const worker=new Worker(new URL('./layout.worker.ts',import.meta.url),{type:'module'})
  const cleanup=()=>{signal.removeEventListener('abort',abort);worker.terminate()}
  const abort=()=>{cleanup();reject(new DOMException('Graph loading cancelled','AbortError'))}
  signal.addEventListener('abort',abort,{once:true})
  if(signal.aborted){abort();return}
  worker.onerror=()=>{cleanup();reject(new Error(t('graph.layoutFailed')))}
  client.clusterGraph({provider:target.provider,target:target.id,scope:JSON.parse(scopeKey)},signal).then(graph=>{
   if(signal.aborted)return
   worker.onmessage=(event:MessageEvent<{layout:Layout}>)=>{if(signal.aborted)return;cleanup();resolve({graph,layout:event.data.layout})}
   worker.postMessage({graph,seq:0})
  },error=>{cleanup();reject(error)})
 }),[client,target.provider,target.id,scopeKey])
 // Admission owns connection identity; a held editor must not admit a replacement session.
 const request=useSnapshotRequest(load,JSON.stringify([target.provider,target.id,scopeKey]),{connectionScoped:false})
 const state=request.value,busy=request.busy,error=request.error?errorDetail(request.error):''
 useEffect(()=>{engine.current?.setClusterLabel(clusterLabel)},[clusterLabel])
 useEffect(()=>{if(state)engine.current?.setGraph(state.graph,state.layout)},[state])
 useEffect(()=>{engine.current?.setOptions(selectedID,routes,filter)},[selectedID,routes,filter,state])
 const results=state&&filter.trim()?state.layout.nodes.filter(n=>[n.ref.title??n.ref.name,n.kindTitle,n.ref.scope??''].some(s=>s.toLocaleLowerCase().includes(filter.trim().toLocaleLowerCase()))).slice(0,12):[]
 const choose=(node:PlacedNode)=>select.current(node)
 return <section className="graph-workspace flex min-h-0 flex-1 flex-col" aria-label={t('graph.title')}>
  <WorkspaceToolbar title={t('graph.title')}>
   {scopePicker}
   <ListRefreshProgress view={request.loading} initial={!state}/>
   <RefreshButton data-resync busy={busy} onClick={request.refresh} />
   <label className="ml-auto flex items-center gap-2 text-xs text-fg-muted"><input type="checkbox" checked={routes} onChange={e=>setRoutes(e.target.checked)}/>{t('graph.routes')}</label>
  </WorkspaceToolbar>
  <SnapshotBoundary request={request}>
  <div className="relative flex min-h-0 min-w-0 flex-1">
   <div className="relative min-w-0 flex-1 overflow-hidden">
    <canvas ref={canvas} data-area="table" data-area-focus tabIndex={0} role="application" aria-label={t('graph.canvas')} aria-describedby="graph-help" className="block h-full w-full touch-none outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-accent"/>
    <div className="absolute left-3 top-3 w-64 max-w-[calc(100%_-_24px)]">
     <input className="w-full rounded-md border border-line bg-panel px-3 py-2 text-xs text-fg shadow-sm outline-none focus:border-accent" aria-label={t('graph.search')} placeholder={t('graph.search')} value={filter} onChange={e=>setFilter(e.target.value)} onKeyDown={e=>{if(e.key==='Enter'&&results[0]){e.preventDefault();choose(results[0])}if(e.key==='Escape'){e.preventDefault();e.stopPropagation();setFilter('')}}}/>
     {results.length>0&&<ul className="mt-1 max-h-72 overflow-y-auto rounded-md border border-line bg-panel shadow-lg" aria-label={t('graph.searchResults')}>{results.map(node=><li key={node.id}><button className="flex w-full flex-col px-3 py-2 text-left text-xs hover:bg-hover" onClick={()=>choose(node)}><span className="text-fg">{node.ref.title??node.ref.name}</span><span className="text-fg-muted">{node.kindTitle} · {node.ref.scope||t('graph.clusterResources')}</span></button></li>)}</ul>}
    </div>
    <div className="absolute bottom-3 right-3 flex max-w-[calc(100%_-_24px)] flex-wrap gap-1 rounded-md border border-line bg-panel p-1">
     <button className={button} aria-label={t('graph.zoomOut')} onClick={()=>engine.current?.zoom(1/1.3)}>−</button>
     <button className={button} onClick={()=>engine.current?.fit()}>{t('graph.fit')}</button>
     <button className={button} aria-label={t('graph.zoomIn')} onClick={()=>engine.current?.zoom(1.3)}>+</button>
    </div>
    {!busy&&state&&!state.graph.nodes.length&&<div role="status" className="pointer-events-none absolute inset-0 grid place-items-center text-sm text-fg-muted">{t('graph.empty')}</div>}
    {error&&<div role="alert" className="absolute bottom-14 left-3 right-3 rounded-md border border-line bg-panel p-3 text-xs text-danger">{error}</div>}
   </div>
   {selected&&<ResourceDrawer {...drawer} subject={selected} onClose={()=>setSelected(null)}/>}
  </div>
  </SnapshotBoundary>
  <LocationStatus><span>{t('graph.title')}</span>
   <span>{t('graph.counts',{nodes:state?.graph.nodes.length??0,edges:state?.graph.edges.length??0})}</span>
   <span id="graph-help">{routes?t('graph.declared'):t('graph.help')}</span>
   {state&&state.graph.discovery!=='ready'&&<span role="status">{t('graph.discovery')}</span>}
   {state?.graph.truncated&&<span role="status" className="text-warning">{t('graph.truncated')}</span>}
   <span className="ml-auto max-w-xs truncate" aria-live="polite">{hover?hover.kindTitle+' · '+(hover.ref.title??hover.ref.name):''}</span>
  {!!state?.graph.problems.length&&<details className="border-t border-line px-3 py-1 text-xs text-warning"><summary>{t('graph.partial',{count:state.graph.problems.length})}</summary><ul className="max-h-24 overflow-y-auto">{state.graph.problems.map((p,i)=><li key={i}>{p.kind}{p.scope?' / '+p.scope:''} · {p.class}</li>)}</ul></details>}
  </LocationStatus>

 </section>
}
