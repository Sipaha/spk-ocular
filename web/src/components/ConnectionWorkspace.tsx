import { useEffect, useState } from 'react'
import type { Target } from '../api/types'
import { classLabel, t } from '../i18n'
import { PanelResize, usePanelWidths } from './PanelResize'

export function ConnectionWorkspace({ target, pending, onConnect, onCancel }: {
  target: Target
  pending?: 'starting' | 'cancelling'
  onConnect: () => void
  onCancel: () => void
}) {
  const width = usePanelWidths((s) => s.navigation)
  const status = target.connection
  const active = status?.state === 'connecting' || !!pending
  const cancelling = pending === 'cancelling'
  const [now, setNow] = useState(() => Date.now())
  useEffect(() => {
    if (!active) return
    const timer = setInterval(() => setNow(Date.now()), 250)
    return () => clearInterval(timer)
  }, [active])
  // A new attempt can arrive before the first timer tick after an idle period.
  // Never calculate its countdown from a clock older than that attempt.
  const currentTime = Math.max(now, status?.startedAt ?? 0, status?.attemptStarted ?? 0)
  const elapsed = status ? Math.max(0, Math.floor(((status.finishedAt || currentTime) - status.startedAt) / 1000)) : 0
  const retrySeconds = Math.max(0, Math.ceil(((status?.retryAt ?? 0) - currentTime) / 1000))
  const endpoint = target.details?.find((d) => d.key === 'server' || d.key === 'host')?.value
  const phase = cancelling ? t('connection.cancelling') : active ?
    status?.phase === 'retry_wait' ? t('connection.retryWait', { seconds: retrySeconds }) :
      status?.phase === 'checking' ? t('connection.checking') : t('connection.opening') :
    status?.state === 'failed' ? t('connection.failed') :
      status?.state === 'cancelled' ? t('connection.cancelled') : t('connection.disconnected')
  return <div className="flex min-h-0 flex-1">
    <div className="relative shrink-0" style={{ width, maxWidth: '25vw' }}>
      <nav aria-label="resources" data-area="nav" className="resource-nav">
        <div className="px-1 py-1">
          <p className="mb-3 text-xs text-fg-subtle">{active ? t('connection.connecting') : t('connection.disconnected')}</p>
          <button type="button" data-area-focus data-nav-item disabled={cancelling}
            onClick={active ? onCancel : onConnect}
            className="w-full rounded-md border border-line px-3 py-1.5 text-left text-fg hover:bg-hover disabled:opacity-50">
            {active ? t('connection.cancel') : t('connection.connect')}
          </button>
        </div>
      </nav>
      <PanelResize label={t('panels.navigation')} value={width} min={140} max={() => Math.min(360, window.innerWidth * 0.25)} onDone={(navigation) => usePanelWidths.setState({ navigation })} />
    </div>
    <main className="flex min-w-0 flex-1 items-center justify-center overflow-y-auto p-6">
      <section aria-label={t('connection.status')} className="w-full max-w-md text-sm">
        <h1 className="mb-1 break-words text-sm font-semibold">{target.title}</h1>
        {endpoint && <p className="mb-5 break-all text-xs text-fg-subtle">{endpoint}</p>}
        <p role="status" className="flex items-center gap-2 text-fg-muted">
          {active && <span aria-hidden className="h-3 w-3 shrink-0 animate-spin rounded-full border border-line border-t-accent" />}
          {phase}
        </p>
        {status && status.state !== 'disconnected' && status.attempt > 0 && <p className="mt-2 text-xs text-fg-subtle">
          {t('connection.attempt', { attempt: status.attempt, max: status.maxAttempts })} · {t('connection.elapsed', { seconds: elapsed })}
        </p>}
        {status?.error && <div role={status.state === 'failed' ? 'alert' : undefined} className="mt-4 border-l border-line pl-3 text-xs">
          <p className="mb-1 text-fg-muted">{t('connection.lastError')}{status.errorClass ? ` · ${classLabel(status.errorClass)}` : ''}</p>
          <p className="break-words whitespace-pre-wrap text-fg-subtle">{status.error}</p>
        </div>}
        {!active && status?.state !== 'failed' && <p className="mt-3 text-xs text-fg-subtle">{t('connection.hint')}</p>}
      </section>
    </main>
  </div>
}
