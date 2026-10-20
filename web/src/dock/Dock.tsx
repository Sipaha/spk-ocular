import { Menu } from '../actions/Menu'
import { LocationStatus, LocationStatusScope } from '../components/LocationStatus'
import type {ViewHub} from '../views/viewSync'
import {useWorkspaceStatus} from '../workspace/persistence'
import {showNotice} from '../store'
import { PanelResize } from '../components/PanelResize'
import { CloseIcon } from '../components/icons'
import { lazy, Suspense, useRef, useState } from 'react'
import type { Client } from '../api/client'
import type { TargetRef } from '../api/types'
import { t } from '../i18n'
import { reconfigured, useStore } from '../store'
import { dock, MIN_DOCK, useDock, type DockTab } from './store'

// Heavy parts are lazy chunks: the log viewer (virtual list, ANSI, search
// worker) and the terminal (xterm).
const LogHost = lazy(() => import('../logs/LogHost').then(module=>({default:module.LogHost})))
const RestoredLogs=lazy(()=>import('../workspace/RestoredLogs'))
const PropertiesHost=lazy(()=>import('../workspace/PropertiesHost'))
const RestoredTerminal=lazy(()=>import('../workspace/PropertiesHost').then(module=>({default:module.RestoredTerminal})))
const FilesHost=lazy(()=>import('../files/FilesHost'))
const TerminalHost = lazy(() => import('../term/TerminalHost'))

interface Props {
  client: Client
  hub: ViewHub
  /** the selected target: tabs of another one say whose they are */
  current: TargetRef | null
  mode: 'desktop' | 'browser'
  onHeightDone: (h: number) => void
}

const sameTarget = (a: TargetRef, b: TargetRef | null) => !!b && a.provider === b.provider && a.id === b.id

/**
 * The bottom panel: log and terminal tabs. Inactive tabs stay mounted
 * (streams go on, terminals keep their screen); closing a tab ends its
 * stream or terminal.
 */
export function Dock({ client, hub, current, mode, onHeightDone }: Props) {
  const { tabs, active, height } = useDock()
  const targets = useStore((s) => s.view)
  if (!tabs.length) return null
  return (
    <section aria-label={t('dock.label')} data-area="dock" className="flex min-h-0 flex-col border-t border-line bg-app" style={{ height, maxHeight:'85dvh' }}>
      <PanelResize label={t('logs.resize')} axis="height" reverse value={height} min={MIN_DOCK} max={() => window.innerHeight * 0.85} onDone={(h) => { dock.setHeight(h); onHeightDone(h) }} />
      <div role="tablist" className="dock-tabs">
        {tabs.map((tb) => (
          <TabHandle
            key={tb.id}
            tab={tb}
            targetTitle={targets?.groups.flatMap(g=>g.targets).find(t=>t.provider===tb.target.provider&&t.id===tb.target.id)?.title??tb.targetTitle}
            active={tb.id === active}
            foreign={!sameTarget(tb.target, current)}
            stale={tb.kind === 'term' && reconfigured(targets, tb.target.provider, tb.target.id, tb.rev)}
          />
        ))}
      </div>
      <div className="relative min-h-0 flex-1">
        {tabs.map((tb) => (
          <div key={tb.id} role="tabpanel" aria-label={tb.title} hidden={tb.id !== active} className="absolute inset-0">
            <LocationStatusScope active={tb.id === active}>
            {(tb.kind !== 'logs' || tb.dormant) && <LocationStatus><span>{tb.title}</span></LocationStatus>}
            <Suspense fallback={<p className="p-3 text-fg-subtle">{t('app.loading')}</p>}>
              {tb.kind === 'files'? <FilesHost client={client} tab={tb}/> : tb.kind === 'properties'? <PropertiesHost key={tb.openEpoch??0} client={client} hub={hub} tab={tb}/> : tb.kind==='term-restored'? <RestoredTerminal client={client} tab={tb}/> : tb.kind === 'logs' ? (
                tb.dormant ? <RestoredLogs client={client} tab={tb}/> : <LogHost client={client} tab={tb} active={tb.id === active} />
              ) : (
                <TerminalHost client={client} tab={tb} active={tb.id === active} mode={mode} />
              )}
            </Suspense>
            </LocationStatusScope>
          </div>
        ))}
      </div>
    </section>
  )
}

function TabHandle({ tab, targetTitle, active, foreign, stale }: { tab: DockTab; targetTitle:string; active: boolean; foreign: boolean; stale: boolean }) {
  const ready=useWorkspaceStatus(s=>s.ready)
  const titleRef=useRef<HTMLButtonElement>(null)
  const [menu,setMenu]=useState<{x:number;y:number}|null>(null)
  const tabs=useDock(s=>s.tabs),index=tabs.findIndex(item=>item.id===tab.id)
  const openMenu=(at:{x:number;y:number})=>{titleRef.current?.focus({preventScroll:true});setMenu(at)}
  const tip = [tab.kind==='files'?t('files.title'):tab.kind==='properties'?t('workspace.propertiesLabel'):tab.kind==='logs'?t('logs.open'):t('term.open'),tab.kind === 'term' ? tab.hint : undefined, tab.title].filter(Boolean).join('\n')
  return (
    <>
    <div
      role="tab"
      aria-selected={active}
      data-tab-kind={tab.kind}
      onClick={() => dock.activate(tab.id)}
      onContextMenu={event=>{event.preventDefault();event.stopPropagation();openMenu({x:event.clientX,y:event.clientY})}}
      onKeyDown={event=>{
        if(event.key==='ContextMenu'||(event.shiftKey&&event.key==='F10')){
          event.preventDefault();event.stopPropagation()
          const rect=event.currentTarget.getBoundingClientRect();openMenu({x:rect.left,y:rect.bottom})
        }
      }}
      className={['dock-tab group flex max-w-72 items-center gap-1 rounded-t-md border border-b-0 px-2 py-0.5 text-xs', active ? 'border-line bg-app text-fg' : 'border-transparent text-fg-muted hover:text-fg'].join(' ')}
    >
      {tab.kind === 'properties'&&<svg aria-hidden width="13" height="13" className="shrink-0 text-fg-subtle" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4"><path d="M4 2h6l3 3v9H4V2Zm5 0v4h4M6 8h5M6 11h5"/></svg>}
      {(tab.kind === 'term'||tab.kind==='term-restored') && <span aria-hidden className="font-mono text-[11px] text-fg-subtle">{'>_'}</span>}
      <button ref={titleRef} aria-keyshortcuts="Shift+F10" className="min-w-0 truncate" title={tip} data-area-focus={active ? '' : undefined}>
        {tab.title}
      </button>
      {foreign && (
        // Not only a tooltip: a shell in another cluster must be visible as such.
        <span className="shrink-0 rounded bg-warning/15 px-1 text-[11px] text-warning" title={t('dock.otherTarget')}>
          {targetTitle}
        </span>
      )}
      {stale && <Reconfigured />}
      {tab.kind!=='files'&&<button className={`flex h-6 w-6 shrink-0 items-center justify-center rounded hover:bg-hover ${tab.pinned?'text-accent':'text-fg-muted'}`} disabled={!ready||!(tab.kind==='term'?tab.open.ref.uid:tab.ref.uid)} aria-label={t('workspace.keep')} aria-pressed={!!tab.pinned} title={tab.pinned?t('workspace.unpin'):t('workspace.keep')} onClick={e=>{e.stopPropagation();if(!dock.pin(tab.id))showNotice(t('workspace.limit'))}}><svg aria-hidden width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4"><path d="M5 2h6l-1 5 3 3H3l3-3-1-5ZM8 10v4"/></svg></button>}
      <button className="flex h-6 w-6 shrink-0 items-center justify-center rounded text-fg-muted hover:bg-hover hover:text-fg" onClick={(e) => { e.stopPropagation(); dock.close(tab.id) }} aria-label={t('logs.closeTab')} title={t('logs.closeTab')}>
        <CloseIcon className="h-4 w-4" />
      </button>
    </div>
    {menu&&<Menu label={t('dock.label')} at={menu} onClose={()=>setMenu(null)} items={[
      {id:'close',label:t('drawer.close'),onSelect:()=>dock.close(tab.id)},
      {id:'close-all',label:t('dock.closeAll'),onSelect:()=>dock.closeGroup(tab.id,'all')},
      {id:'close-left',label:t('dock.closeLeft'),disabled:index<=0,onSelect:()=>dock.closeGroup(tab.id,'left')},
      {id:'close-right',label:t('dock.closeRight'),disabled:index<0||index>=tabs.length-1,onSelect:()=>dock.closeGroup(tab.id,'right')},
    ]}/>}
    </>
  )
}

/** A live resource still using the configuration it was opened with. */
export function Reconfigured() {
  return (
    <span className="shrink-0 rounded bg-warning/15 px-1 text-[11px] text-warning" title={t('live.reconfiguredHint')}>
      {t('live.reconfigured')}
    </span>
  )
}
