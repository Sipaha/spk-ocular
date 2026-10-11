import {useEffect,useRef,useState} from 'react'
import type {Client} from '../api/client'
import type {KindDescriptor,Ref,Target} from '../api/types'
import {ResourceDrawer} from '../components/ResourceDrawer'
import {ToolDialog,type ToolMode} from '../components/ToolDialog'
import {dock,useDock,type PropertiesTab,type RestoredTerminalTab} from '../dock/store'
import {errorDetail} from '../errors'
import {t} from '../i18n'
import {useStore} from '../store'
import type {ViewHub} from '../views/viewSync'

export function currentTarget(ref:Ref,targets:ReturnType<typeof useStore.getState>['view']){return targets?.groups.flatMap(g=>g.targets).find(t=>t.provider===ref.provider&&t.id===ref.target)}
const sourceOf=(t:Target|undefined)=>t?.connection?.state==='connected'?`${t.connection.id}:${t.configRev??''}`:null

export default function PropertiesHost({client,hub,tab}:{client:Client;hub:ViewHub;tab:PropertiesTab}){
 const view=useStore(s=>s.view),target=currentTarget(tab.ref,view),source=sourceOf(target)
 const [admitted,setAdmitted]=useState<string|null>(tab.dormant?null:source)
 const [catalog,setCatalog]=useState<{source:string;kinds:KindDescriptor[]}|null>(null)
 const [error,setError]=useState('')
 const [tool,setTool]=useState<{ref:Ref;mode:ToolMode;source:string|null}|null>(null)
 const active=source!==null&&admitted===source
 useEffect(()=>{
  if(!active||!source)return
  let live=true
  void client.listKinds(tab.target.provider,tab.target.id).then(result=>{if(live)setCatalog({source,kinds:result.kinds})},err=>{if(live)setError(errorDetail(err))})
  return()=>{live=false}
 },[active,source,client,tab.target.provider,tab.target.id])
 if(!active)return <Dormant refName={`${tab.ref.kind} ${tab.ref.scope??''}/${tab.ref.name}`} connected={source!==null} onResume={()=>{setError('');setCatalog(null);setAdmitted(source)}}/>
 if(error)return <div className="p-3"><p role="alert" className="text-danger">{error}</p><button className="text-accent" onClick={()=>{setError('');setAdmitted(null)}}>{t('workspace.resumeProperties')}</button></div>
 const kinds=catalog?.source===source?catalog.kinds:[]
 return <><ResourceDrawer embedded client={client} hub={hub} target={tab.target} subject={tab.ref} initialTab={tab.detailsTab} onTab={value=>dock.propertiesTab(tab.id,value)} onClose={()=>dock.close(tab.id)} hasLogs={id=>!!kinds.find(k=>k.id===id)?.logs} onLogs={ref=>setTool({ref,mode:'logs',source})} eventsKindOf={id=>kinds.find(k=>k.id===id)?.eventsKind} hasExec={id=>!!kinds.find(k=>k.id===id)?.exec} onTerminal={ref=>setTool({ref,mode:'terminal',source})} kindTitleOf={id=>kinds.find(k=>k.id===id)?.singular??id}/>
  {tool&&tool.source===source&&<ToolDialog client={client} subject={tool.ref} mode={tool.mode} onClose={()=>setTool(null)} onOpen={selection=>{setTool(null);if(tool.mode==='logs')dock.openLogs(tab.target,target?.title??tab.targetTitle,selection.ref,selection.channel);else dock.openTerminal(tab.target,target?.title??tab.targetTitle,{ref:selection.ref,instance:selection.instance?.id,channel:selection.channel,command:selection.command})}}/>}
 </>
}
export function Dormant({refName,connected,onResume,terminal=false,busy=false}:{refName:string;connected:boolean;onResume:()=>void;terminal?:boolean;busy?:boolean}){
 return <div className="flex h-full flex-col items-start justify-center gap-2 overflow-auto p-4 text-sm"><strong>{refName}</strong><p className="text-fg-muted">{terminal?t('workspace.terminalEnded'):t('workspace.restoredHint')}</p>{!connected&&<p className="text-warning">{t('workspace.connectFirst')}</p>}<button className="rounded border border-line px-2 py-1 text-xs hover:bg-hover disabled:opacity-50" disabled={!connected||busy} onClick={onResume}>{busy?t('app.loading'):terminal?t('workspace.newTerminal'):t('workspace.resumeProperties')}</button></div>
}
export function RestoredTerminal({client,tab}:{client:Client;tab:RestoredTerminalTab}){
 const view=useStore(s=>s.view),target=currentTarget(tab.ref,view)
 const connected=sourceOf(target)!==null
 const [opened,setOpened]=useState<string|null>(null),[busy,setBusy]=useState(false),[error,setError]=useState('')
 const sequence=useRef(0)
 useEffect(()=>()=>{sequence.current++},[])
 const prepare=async()=>{
  const source=sourceOf(target)
  if(!source||busy)return
  const own=++sequence.current;setBusy(true);setError('')
  try{
   const r=await client.getResource(tab.ref)
   if(own!==sequence.current)return
   if(sourceOf(currentTarget(tab.ref,useStore.getState().view))!==source)throw new Error(t('workspace.connectFirst'))
   if(!r?.ref||r.ref.uid!==tab.ref.uid||r.ref.kind!==tab.ref.kind||r.ref.name!==tab.ref.name||(r.ref.scope??'')!==(tab.ref.scope??''))throw new Error(t('workspace.replaced'))
   setOpened(source)
  }catch(e){if(own===sequence.current)setError(errorDetail(e))}finally{if(own===sequence.current)setBusy(false)}
 }
 return <>{error&&<p role="alert" className="p-2 text-danger">{error}</p>}<Dormant terminal refName={`${tab.ref.kind} ${tab.ref.scope??''}/${tab.ref.name}`} connected={connected} busy={busy} onResume={()=>void prepare()}/>{opened===sourceOf(target)&&opened!==null&&connected&&<ToolDialog client={client} subject={tab.ref} mode="terminal" onClose={()=>setOpened(null)} onOpen={selection=>{setOpened(null);dock.close(tab.id);dock.openTerminal(tab.target,target?.title??tab.targetTitle,{ref:selection.ref,instance:selection.instance?.id,channel:selection.channel,command:selection.command});const state=useDock.getState();const active=state.active;if(active)dock.pin(active)}}/>}</>
}
