import {compositionKey,copyComposition} from '../logs/composition'
import type {LogComposition} from '../api/types'
import {mayLeaveMatching} from '../edit/guard'
import {fitsPins, type WorkspaceSnapshot} from '../workspace/snapshot'
import { create } from 'zustand'
import type { Ref, TargetRef, ExecInstance } from '../api/types'
import { refTitle } from '../refs'

// The bottom panel's tabs live above the per-target Workspace: log tabs
// belong to their target and stay while its session lives (a recent target
// keeps running in the background; the tab says whose it is when it is not
// the current one); terminal tabs belong to the app and stay too. Closing
// the target's session ends its log tabs' streams ("gone").

export interface TermOpen {
  preparedId?: string
  ref: Ref
  instance?: string
  channel?: string
  /** argv; empty = the provider's interactive shell */
  command?: string[]
  /** to the container's own process (a debugger), not a new one */
  attach?: boolean
}

interface TabBase {
  id: string
  target: TargetRef
  targetTitle: string
  title: string
  /** Explicitly retained as metadata across application restarts. */
  pinned?: boolean
  dormant?: boolean
}

export interface LogsTab extends TabBase {
 previous?:boolean
  composition?: LogComposition
  kind: 'logs'
  ref: Ref
  channel?: string
  /** Explicit open requests refocus an already active log tab. */
  focusEpoch?: number
}

export interface TermTab extends TabBase {
  kind: 'term'
  open: TermOpen
  /** a tooltip: context, server, command */
  hint?: string
  /** the target's configRev the terminal was opened with */
  rev?: string
}

export interface PropertiesTab extends TabBase {kind:'properties';ref:Ref;detailsTab?:'details'|'yaml';openEpoch?:number}
export interface RestoredTerminalTab extends TabBase {kind:'term-restored';ref:Ref}
export interface FilesTab extends TabBase {kind:'files';ref:Ref;selectedInstance?:ExecInstance;preparedId?:string}
export type DockTab = LogsTab | TermTab | PropertiesTab | RestoredTerminalTab | FilesTab

export interface DockState {
  tabs: DockTab[]
  active: string | null
  height: number
}

export const DEFAULT_DOCK = 320
export const MIN_DOCK = 140

export const useDock = create<DockState>(() => ({ tabs: [], active: null, height: DEFAULT_DOCK }))

const refKey = (r: Ref) => `${r.kind}/${r.scope ?? ''}/${r.name}/${r.uid ?? ''}`
const targetKey = (t: TargetRef) => `${t.provider}/${t.id}`
let termSeq = 0

function activate(tab: DockTab) {
  useDock.setState((s) => ({ tabs: s.tabs.some((x) => x.id === tab.id) ? s.tabs.map(x=>x.id===tab.id?{...tab,pinned:x.pinned,dormant:false}:x) : [...s.tabs, tab], active: tab.id }))
}

export type TabCloseScope = 'one' | 'all' | 'left' | 'right'

const closeBlocks=new Map<string,()=>boolean|Promise<boolean>>()
export function holdDockClose(id:string,blocked:()=>boolean|Promise<boolean>){closeBlocks.set(id,blocked);return()=>{if(closeBlocks.get(id)===blocked)closeBlocks.delete(id)}}
function closeTabs(id:string,scope:TabCloseScope){
  const tabs=useDock.getState().tabs,anchor=tabs.findIndex(t=>t.id===id)
  if(anchor<0)return
  const ids=new Set(tabs.filter((_,i)=>scope==='one'?i===anchor:scope==='left'?i<anchor:scope==='right'?i>anchor:true).map(t=>t.id))
  const results=[...ids].map(id=>closeBlocks.get(id)?.())
  const finish=(blocked:(boolean|undefined)[])=>{if(!blocked.some(Boolean))mayLeaveMatching(()=>removeTabs(ids),h=>!!h.scope&&ids.has(h.scope))}
  if(results.some(r=>r instanceof Promise))return Promise.all(results).then(finish)
  finish(results as (boolean|undefined)[])
}
function removeTabs(ids:ReadonlySet<string>) {
  useDock.setState(state => {
    const tabs = state.tabs.filter(tab=>!ids.has(tab.id))
    if (tabs.length === state.tabs.length) return state
    const activeIndex = state.tabs.findIndex(tab => tab.id === state.active)
    const active = tabs.some(tab => tab.id === state.active) ? state.active : (tabs.find(tab => state.tabs.indexOf(tab) >= activeIndex) ?? tabs.at(-1))?.id ?? null
    return { tabs, active }
  })
}

export const dock = {
  pin(id:string):boolean {
    const state=useDock.getState();const tab=state.tabs.find(t=>t.id===id);if(tab?.kind==='files'||tab?.kind==='logs'&&tab.composition)return false;const tabs=state.tabs.map(t=>t.id===id?{...t,pinned:!t.pinned}:t);if(!fitsPins(tabs))return false;useDock.setState({tabs});return true
  },
  openProperties(target:TargetRef,targetTitle:string,ref:Ref,detailsTab:'details'|'yaml'='details'):boolean {
    const tab:PropertiesTab={kind:'properties',id:`properties:${targetKey(target)}:${refKey(ref)}`,target,targetTitle,ref:{...ref,provider:target.provider,target:target.id},title:`${ref.kind.split('/').pop()}/${refTitle(ref)}`,detailsTab,pinned:true};
    const state=useDock.getState();const tabs=state.tabs.some(t=>t.id===tab.id)?state.tabs.map(t=>t.id===tab.id?{...tab,openEpoch:t.kind==='properties'?(t.openEpoch??0)+1:1}:t):[...state.tabs,tab];if(!fitsPins(tabs))return false;useDock.setState({tabs,active:tab.id});return true
  },
  propertiesTab(id:string,detailsTab:string){if(detailsTab!=='details'&&detailsTab!=='yaml')return;const current=useDock.getState().tabs.find(t=>t.id===id);if(current?.kind!=='properties'||current.detailsTab===detailsTab)return;useDock.setState(s=>({tabs:s.tabs.map(t=>t.id===id&&t.kind==='properties'?{...t,detailsTab}:t)}))},
  resumeLogs(id:string){useDock.setState(s=>({tabs:s.tabs.map(t=>t.id===id&&t.kind==='logs'?{...t,dormant:false}:t)}))},
  restore(snapshot:WorkspaceSnapshot){
    const state=useDock.getState();const restored:DockTab[]=snapshot.panels.map((p,i)=>{const target={provider:p.ref.provider,id:p.ref.target};const common={target,targetTitle:p.ref.target,ref:p.ref,title:`${p.ref.kind.split('/').pop()}/${p.ref.name}`,pinned:true,dormant:true};return p.kind==='logs'?{...common,kind:'logs',id:`logs:${targetKey(target)}:${refKey(p.ref)}${p.channel?`:${p.channel}`:''}`,channel:p.channel}:p.kind==='properties'?{...common,kind:'properties',id:`properties:${targetKey(target)}:${refKey(p.ref)}`,detailsTab:p.tab}: {...common,kind:'term-restored',id:`restored-terminal:${i}:${targetKey(target)}:${refKey(p.ref)}`}});
    const tabs=[...state.tabs.map(t=>restored.some(r=>r.id===t.id)?{...t,pinned:true}:t),...restored.filter(t=>!state.tabs.some(old=>old.id===t.id))];useDock.setState({tabs,active:state.active??restored[snapshot.active]?.id??null})
  },
  /** One log tab per object, target and explicit channel; reopening activates it. */
  openLogs(target: TargetRef, targetTitle: string, ref: Ref, channel?: string, previousInstance?:boolean) {
    const id = `logs:${targetKey(target)}:${refKey(ref)}${channel ? `:${channel}` : ''}${previousInstance ? ':previous' : ''}`
    const previous = useDock.getState().tabs.find(tab => tab.id === id)
    activate({ kind: 'logs', id, previous:previousInstance, focusEpoch: (previous?.kind === 'logs' ? previous.focusEpoch ?? 0 : 0) + 1, target, targetTitle, ref:{...ref,provider:target.provider,target:target.id}, channel, title: `${ref.kind.split('/').pop()}/${refTitle(ref)}` })
  },
  openComposedLogs(targetTitle:string,composition:LogComposition) {
    const c=copyComposition(composition),ref=c.refs[0]
    if(c.refs.length<2||c.refs.length>8||!ref)return
    const id=`composed-logs:${compositionKey(c)}`
    const previous=useDock.getState().tabs.find(tab=>tab.id===id)
    activate({kind:'logs',id,composition:c,focusEpoch:(previous?.kind==='logs'?previous.focusEpoch??0:0)+1,target:{provider:ref.provider,id:ref.target},targetTitle,ref,channel:'*',title:`${c.refs.length} · ${c.refs.map(r=>r.name).join(', ')}`})
  },
  /** Files capabilities and editor contents remain memory-only. */
  openFiles(target:TargetRef,targetTitle:string,ref:Ref,selectedInstance?:ExecInstance,preparedId?:string) {
    activate({kind:'files',id:`files:${targetKey(target)}:${++termSeq}`,target,targetTitle,ref:{...ref,provider:target.provider,target:target.id},selectedInstance,preparedId,title:`${ref.kind.split('/').pop()}/${refTitle(ref)}`})
  },
  /** Every open is a new terminal (two shells in one pod are normal). */
  openTerminal(target: TargetRef, targetTitle: string, open: TermOpen, title = refTitle(open.ref)) {
    termSeq++
    activate({ kind: 'term', id: `term:${targetKey(target)}:${refKey(open.ref)}:${termSeq}`, target, targetTitle, open:{...open,ref:{...open.ref,provider:target.provider,target:target.id}}, title })
  },
  update(id: string, patch: Partial<Pick<TermTab, 'title' | 'hint' | 'rev'>>) {
    useDock.setState((s) => ({ tabs: s.tabs.map((t) => (t.id === id ? { ...t, ...patch } : t)) }))
  },
  activate(id: string) {
    useDock.setState({ active: id })
  },
  close(id: string) { return closeTabs(id, 'one') },
  closeGroup(id: string, scope: TabCloseScope) { return closeTabs(id, scope) },
  setHeight(h: number) {
    useDock.setState({ height: h })
  },
}
