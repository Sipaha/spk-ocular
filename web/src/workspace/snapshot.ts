import type {Ref} from '../api/types'
import type {DockState,DockTab} from '../dock/store'
import type {usePanelWidths} from '../components/PanelResize'

export const WORKSPACE_PROVIDER='ocular'
export const WORKSPACE_TARGET='workspace'
export const WORKSPACE_KEY='workspacePanels'
export const MAX_PINS=8
export const MAX_WORKSPACE_BYTES=4096
export type Widths=ReturnType<typeof usePanelWidths.getState>
export interface SavedPanel {kind:'properties'|'logs'|'terminal';ref:Ref;channel?:string;tab?:'details'|'yaml'}
export interface WorkspaceSnapshot {v:1;panels:SavedPanel[];active:number;height:number;widths:Widths}
const hasControl=(s:string)=>[...s].some(ch=>ch.codePointAt(0)!<32||ch.codePointAt(0)===127)
const bytes=(text:string)=>new TextEncoder().encode(text).length
const cleanRef=(r:Ref):Ref=>({provider:r.provider,target:r.target,kind:r.kind,name:r.name,uid:r.uid,...(r.scope?{scope:r.scope}:{})})
export function savedPanels(tabs:DockTab[]):SavedPanel[]{
 return tabs.filter(t=>t.pinned&&t.kind!=='files'&&!(t.kind==='logs'&&t.composition)).map(t=>t.kind==='properties'?{kind:'properties',ref:cleanRef(t.ref),tab:t.detailsTab??'details'}:t.kind==='logs'?{kind:'logs',ref:cleanRef(t.ref),...(t.channel?{channel:t.channel}:{})}:{kind:'terminal',ref:cleanRef(t.kind==='term'?t.open.ref:t.ref)})
}
export function fitsPins(tabs:DockTab[]){const pins=savedPanels(tabs);return pins.every(p=>validRef(p.ref)&&(!p.channel||p.channel.length<=256))&&pins.length<=MAX_PINS&&bytes(JSON.stringify(pins))<=MAX_WORKSPACE_BYTES-512}
const clamp=(value:number,min:number,max:number)=>Math.min(max,Math.max(min,Number.isFinite(value)?Math.round(value):min))
export function cleanWidths(w:Widths):Widths{return {targets:clamp(w.targets,160,420),navigation:clamp(w.navigation,140,360),details:w.details===null?null:clamp(w.details,280,1400),helmDetails:w.helmDetails===null?null:clamp(w.helmDetails,280,1400),agentsTargets:clamp(w.agentsTargets,160,420)}}
export function serializeWorkspace(dock:DockState,widths:Widths):string|null{
 const pinned=dock.tabs.filter(t=>t.pinned&&t.kind!=='files'&&!(t.kind==='logs'&&t.composition))
 if(!fitsPins(dock.tabs))return null
 const snapshot:WorkspaceSnapshot={v:1,panels:savedPanels(dock.tabs),active:Math.max(0,pinned.findIndex(t=>t.id===dock.active)),height:clamp(dock.height,140,1200),widths:cleanWidths(widths)}
 const text=JSON.stringify(snapshot)
 return bytes(text)<=MAX_WORKSPACE_BYTES?text:null
}
function validRef(r:unknown):r is Ref{
 if(!r||typeof r!=='object'||Array.isArray(r))return false
 const ref=r as Record<string,unknown>
 return ['provider','target','kind','name','uid'].every(k=>typeof ref[k]==='string'&&ref[k]!==''&&(ref[k] as string).length<=1024&&!hasControl(ref[k] as string))&&(ref.scope===undefined||typeof ref.scope==='string'&&ref.scope.length<=1024&&!hasControl(ref.scope))
}
/** Strict, whitelisted metadata only. Extra fields never enter restored tabs. */
export function parseWorkspace(text:string|undefined):WorkspaceSnapshot|null{
 if(!text)return null
 if(bytes(text)>MAX_WORKSPACE_BYTES)throw new Error('invalid workspace')
 let raw:unknown
 try{raw=JSON.parse(text)}catch{throw new Error('invalid workspace')}
 if(!raw||typeof raw!=='object')throw new Error('invalid workspace')
 const s=raw as WorkspaceSnapshot
 if(s.v!==1||!Array.isArray(s.panels)||s.panels.length>MAX_PINS||!Number.isInteger(s.active)||s.active<0||s.active>=Math.max(1,s.panels.length)||!Number.isFinite(s.height)||s.height<140||s.height>1200||!s.widths||typeof s.widths!=='object')throw new Error('invalid workspace')
 for(const key of ['targets','navigation','agentsTargets','details','helmDetails'] as const){const value=s.widths[key];if(!(value===null&&(key==='details'||key==='helmDetails'))&&!(typeof value==='number'&&Number.isFinite(value)))throw new Error('invalid workspace')}
 const panels=s.panels.map(p=>{
  if(!p||!['properties','logs','terminal'].includes(p.kind)||!validRef(p.ref)||p.channel!==undefined&&(typeof p.channel!=='string'||p.channel.length>256||hasControl(p.channel))||p.tab!==undefined&&!['details','yaml'].includes(p.tab))throw new Error('invalid workspace')
  return {kind:p.kind,ref:cleanRef(p.ref),...(p.kind==='logs'&&p.channel?{channel:p.channel}:{}),...(p.kind==='properties'?{tab:p.tab??'details'}:{})} satisfies SavedPanel
 })
 const unique=panels.filter(p=>p.kind!=='terminal').map(p=>JSON.stringify({kind:p.kind,ref:p.ref,channel:p.channel}))
 if(new Set(unique).size!==unique.length)throw new Error('invalid workspace')
 return {v:1,panels,active:s.active,height:clamp(s.height,140,1200),widths:cleanWidths(s.widths)}
}
