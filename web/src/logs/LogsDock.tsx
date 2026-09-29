import { lazy, Suspense, useRef } from 'react'
import type { Client } from '../api/client'
import type { Ref } from '../api/types'
import { t } from '../i18n'

// The viewer (virtual list, ANSI, search worker) is a lazy chunk: the start
// of the app does not pay for it.
const LogViewer = lazy(() => import('./LogViewer'))

export interface LogTab {
  id: string
  ref: Ref
  title: string
}

export const tabId = (r: Ref) => `${r.kind}/${r.scope ?? ''}/${r.name}/${r.uid ?? ''}`

export const MIN_DOCK = 140

interface Props {
  client: Client
  tabs: LogTab[]
  active: string | null
  height: number
  onActivate: (id: string) => void
  onClose: (id: string) => void
  onHeight: (h: number, done: boolean) => void
}

/**
 * The bottom panel: one tab per log stream. Inactive tabs stay mounted
 * (their streams go on, bounded by the buffer); closing a tab ends its
 * stream.
 */
export function LogsDock({ client, tabs, active, height, onActivate, onClose, onHeight }: Props) {
  const drag = useRef<{ y: number; h: number } | null>(null)
  if (!tabs.length) return null
  return (
    <section aria-label={t('logs.open')} className="flex shrink-0 flex-col border-t border-line bg-app" style={{ height }}>
      <div
        role="separator"
        aria-orientation="horizontal"
        aria-label={t('logs.resize')}
        className="h-1 shrink-0 cursor-row-resize bg-line/60 hover:bg-accent/60"
        onMouseDown={(e) => {
          e.preventDefault()
          drag.current = { y: e.clientY, h: height }
          const clamp = (h: number) => Math.max(MIN_DOCK, Math.min(window.innerHeight * 0.85, h))
          const move = (ev: MouseEvent) => drag.current && onHeight(clamp(drag.current.h + drag.current.y - ev.clientY), false)
          const up = (ev: MouseEvent) => {
            if (drag.current) onHeight(clamp(drag.current.h + drag.current.y - ev.clientY), true)
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
          <div
            key={tb.id}
            role="tab"
            aria-selected={tb.id === active}
            className={['group flex max-w-64 items-center gap-1 rounded-t-md border border-b-0 px-2 py-0.5 text-xs', tb.id === active ? 'border-line bg-app text-fg' : 'border-transparent text-fg-muted hover:text-fg'].join(' ')}
          >
            <button className="min-w-0 truncate" onClick={() => onActivate(tb.id)} title={tb.title}>
              {tb.title}
            </button>
            <button className="rounded px-1 text-fg-subtle hover:bg-hover hover:text-fg" onClick={() => onClose(tb.id)} aria-label={t('logs.closeTab')}>
              ×
            </button>
          </div>
        ))}
      </div>
      <div className="relative min-h-0 flex-1">
        {tabs.map((tb) => (
          <div key={tb.id} role="tabpanel" hidden={tb.id !== active} className="absolute inset-0">
            <Suspense fallback={<p className="p-3 text-fg-subtle">{t('app.loading')}</p>}>
              <LogViewer client={client} subject={tb.ref} active={tb.id === active} />
            </Suspense>
          </div>
        ))}
      </div>
    </section>
  )
}
