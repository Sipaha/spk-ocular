import { lazy, Suspense, useRef } from 'react'
import type { Client } from '../api/client'
import type { TargetRef } from '../api/types'
import { t } from '../i18n'
import { reconfigured, useStore } from '../store'
import { dock, MIN_DOCK, useDock, type DockTab } from './store'

// Heavy parts are lazy chunks: the log viewer (virtual list, ANSI, search
// worker) and the terminal (xterm).
const LogViewer = lazy(() => import('../logs/LogViewer'))
const TerminalView = lazy(() => import('../term/TerminalView'))

interface Props {
  client: Client
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
export function Dock({ client, current, mode, onHeightDone }: Props) {
  const { tabs, active, height } = useDock()
  const targets = useStore((s) => s.view)
  const drag = useRef<{ y: number; h: number } | null>(null)
  if (!tabs.length) return null
  return (
    <section aria-label={t('dock.label')} className="flex shrink-0 flex-col border-t border-line bg-app" style={{ height }}>
      <div
        role="separator"
        aria-orientation="horizontal"
        aria-label={t('logs.resize')}
        className="h-1 shrink-0 cursor-row-resize bg-line/60 hover:bg-accent/60"
        onMouseDown={(e) => {
          e.preventDefault()
          drag.current = { y: e.clientY, h: height }
          const clamp = (h: number) => Math.max(MIN_DOCK, Math.min(window.innerHeight * 0.85, h))
          const move = (ev: MouseEvent) => drag.current && dock.setHeight(clamp(drag.current.h + drag.current.y - ev.clientY))
          const up = (ev: MouseEvent) => {
            if (drag.current) {
              const h = clamp(drag.current.h + drag.current.y - ev.clientY)
              dock.setHeight(h)
              onHeightDone(h)
            }
            drag.current = null
            window.removeEventListener('mousemove', move)
            window.removeEventListener('mouseup', up)
          }
          window.addEventListener('mousemove', move)
          window.addEventListener('mouseup', up)
        }}
      />
      <div role="tablist" className="flex shrink-0 items-end gap-0.5 overflow-x-auto border-b border-line bg-sidebar/60 px-2 pt-1">
        {tabs.map((tb) => (
          <TabHandle
            key={tb.id}
            tab={tb}
            active={tb.id === active}
            foreign={!sameTarget(tb.target, current)}
            stale={tb.kind === 'term' && reconfigured(targets, tb.target.provider, tb.target.id, tb.rev)}
          />
        ))}
      </div>
      <div className="relative min-h-0 flex-1">
        {tabs.map((tb) => (
          <div key={tb.id} role="tabpanel" aria-label={tb.title} hidden={tb.id !== active} className="absolute inset-0">
            <Suspense fallback={<p className="p-3 text-fg-subtle">{t('app.loading')}</p>}>
              {tb.kind === 'logs' ? (
                <LogViewer client={client} subject={tb.ref} active={tb.id === active} />
              ) : (
                <TerminalView client={client} tab={tb} active={tb.id === active} mode={mode} />
              )}
            </Suspense>
          </div>
        ))}
      </div>
    </section>
  )
}

function TabHandle({ tab, active, foreign, stale }: { tab: DockTab; active: boolean; foreign: boolean; stale: boolean }) {
  const tip = [tab.kind === 'term' ? tab.hint : undefined, tab.title].filter(Boolean).join('\n')
  return (
    <div
      role="tab"
      aria-selected={active}
      data-tab-kind={tab.kind}
      className={['group flex max-w-72 items-center gap-1 rounded-t-md border border-b-0 px-2 py-0.5 text-xs', active ? 'border-line bg-app text-fg' : 'border-transparent text-fg-muted hover:text-fg'].join(' ')}
    >
      {tab.kind === 'term' && <span aria-hidden className="font-mono text-[10px] text-fg-subtle">{'>_'}</span>}
      <button className="min-w-0 truncate" onClick={() => dock.activate(tab.id)} title={tip}>
        {tab.title}
      </button>
      {foreign && (
        // Not only a tooltip: a shell in another cluster must be visible as such.
        <span className="shrink-0 rounded bg-warning/15 px-1 text-[10px] text-warning" title={t('dock.otherTarget')}>
          {tab.targetTitle}
        </span>
      )}
      {stale && <Reconfigured />}
      <button className="rounded px-1 text-fg-subtle hover:bg-hover hover:text-fg" onClick={() => dock.close(tab.id)} aria-label={t('logs.closeTab')}>
        ×
      </button>
    </div>
  )
}

/** A live resource still using the configuration it was opened with. */
export function Reconfigured() {
  return (
    <span className="shrink-0 rounded bg-warning/15 px-1 text-[10px] text-warning" title={t('live.reconfiguredHint')}>
      {t('live.reconfigured')}
    </span>
  )
}
