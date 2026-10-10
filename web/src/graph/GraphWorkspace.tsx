import { useEffect, useRef, useState, type ComponentProps, type ReactNode } from 'react'
import type { Client } from '../api/client'
import type { ClusterGraph, Ref } from '../api/types'
import { t } from '../i18n'
import { mayLeave } from '../edit/guard'
import { ResourceDrawer } from '../components/ResourceDrawer'
import { GraphCanvas } from './canvas'
import type { Layout, PlacedNode } from './model'

type Drawer = Omit<ComponentProps<typeof ResourceDrawer>, 'subject' | 'onClose'>
interface Props {
 client: Client
 target: {provider:string;id:string}
 scope: import('../api/types').ScopeSel
 scopePicker: ReactNode
 drawer: Drawer
}
const button = 'rounded-md border border-line px-2 py-1 text-xs text-fg-muted hover:bg-hover hover:text-fg disabled:opacity-40'

export default function GraphWorkspace({client,target,scope,scopePicker,drawer}:Props) {
 const [state,setState]=useState<{graph:ClusterGraph;layout:Layout}|null>(null)
 const [error,setError]=useState('')
 const [busy,setBusy]=useState(true)
 const [revision,setRevision]=useState(0)
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
  if(node?.id===selected?.uid && node){engine.current?.focus(node.id);return}
  if(!node&&!selected)return
  mayLeave(()=>{setSelected(node?.ref??null);if(node)engine.current?.focus(node.id)})
 }})
 useEffect(()=>{
  const el=canvas.current
  if(!el)return
  const view=new GraphCanvas(el,node=>select.current(node),setHover,t('graph.clusterResources'))
  engine.current=view
  return()=>{view.destroy();engine.current=null}
 },[])
 useEffect(()=>{
  let live=true
  const abort=new AbortController()
  const worker=new Worker(new URL('./layout.worker.ts',import.meta.url),{type:'module'})
  worker.onerror=()=>{if(live){setError(t('graph.layoutFailed'));setBusy(false)}}
  client.clusterGraph({provider:target.provider,target:target.id,scope:JSON.parse(scopeKey)},abort.signal).then(graph=>{
   if(!live)return
   worker.onmessage=(event:MessageEvent<{layout:Layout}>)=>{
    if(!live)return
    setState({graph,layout:event.data.layout});setError('');setBusy(false)
   }
   worker.postMessage({graph,seq:revision})
  },e=>{if(live){setError(e instanceof Error?e.message:String(e));setBusy(false)}})
  return()=>{live=false;abort.abort();worker.terminate()}
 },[client,target.provider,target.id,scopeKey,revision])
 useEffect(()=>{engine.current?.setClusterLabel(clusterLabel)},[clusterLabel])
 useEffect(()=>{if(state)engine.current?.setGraph(state.graph,state.layout)},[state])
 useEffect(()=>{engine.current?.setOptions(selectedID,routes,filter)},[selectedID,routes,filter,state])
 const results=state&&filter.trim()?state.layout.nodes.filter(n=>[n.ref.title??n.ref.name,n.kindTitle,n.ref.scope??''].some(s=>s.toLocaleLowerCase().includes(filter.trim().toLocaleLowerCase()))).slice(0,12):[]
 const choose=(node:PlacedNode)=>select.current(node)
 return <section className="graph-workspace flex min-h-0 flex-1 flex-col" aria-label={t('graph.title')}>
  <header className="flex min-h-10 shrink-0 flex-wrap items-center gap-2 border-b border-line bg-panel px-3 py-1">
   <h2 className="mr-1 text-sm font-semibold">{t('graph.title')}</h2>
   {scopePicker}
   <button className={button} disabled={busy} onClick={()=>{setBusy(true);setRevision(r=>r+1)}}>{busy?t('app.loading'):t('graph.refresh')}</button>
   <label className="ml-auto flex items-center gap-2 text-xs text-fg-muted"><input type="checkbox" checked={routes} onChange={e=>setRoutes(e.target.checked)}/>{t('graph.routes')}</label>
  </header>
  <div className="relative flex min-h-0 flex-1">
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
    {busy&&!state&&<div role="status" className="pointer-events-none absolute inset-0 grid place-items-center text-sm text-fg-muted">{t('graph.loading')}</div>}
    {!busy&&state&&!state.graph.nodes.length&&<div role="status" className="pointer-events-none absolute inset-0 grid place-items-center text-sm text-fg-muted">{t('graph.empty')}</div>}
    {error&&<div role="alert" className="absolute bottom-14 left-3 right-3 rounded-md border border-line bg-panel p-3 text-xs text-danger">{error}</div>}
   </div>
   {selected&&<ResourceDrawer {...drawer} subject={selected} onClose={()=>setSelected(null)}/>}
  </div>
  <footer className="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-1 border-t border-line px-3 py-2 text-xs text-fg-muted">
   <span>{t('graph.counts',{nodes:state?.graph.nodes.length??0,edges:state?.graph.edges.length??0})}</span>
   <span id="graph-help">{routes?t('graph.declared'):t('graph.help')}</span>
   {state&&state.graph.discovery!=='ready'&&<span role="status">{t('graph.discovery')}</span>}
   {state?.graph.truncated&&<span role="status" className="text-warning">{t('graph.truncated')}</span>}
   <span className="ml-auto max-w-xs truncate" aria-live="polite">{hover?hover.kindTitle+' · '+(hover.ref.title??hover.ref.name):''}</span>
  </footer>
  {!!state?.graph.problems.length&&<details className="border-t border-line px-3 py-1 text-xs text-warning"><summary>{t('graph.partial',{count:state.graph.problems.length})}</summary><ul className="max-h-24 overflow-y-auto">{state.graph.problems.map((p,i)=><li key={i}>{p.kind}{p.scope?' / '+p.scope:''} · {p.class}</li>)}</ul></details>}
 </section>
}
