import { useEffect, useState } from 'react'
import type { Target } from '../api/types'
import { classLabel, t } from '../i18n'
import { PanelResize, usePanelWidths } from './PanelResize'
import { groupLabel } from '../presentation'
import { CloseIcon, ProviderIcon } from './icons'
import { ResourceGroupIcon } from './ResourceGroupIcon'

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
  const preview = target.provider === 'kubernetes'
    ? ['Workloads', 'Network', 'Config', 'Storage', 'Helm', 'Cluster', 'Access Control', 'Health', 'API groups']
    : target.provider === 'compose' ? ['Compose', 'Engine'] : []
  const providerTitle = target.provider === 'kubernetes' ? 'Kubernetes' : target.provider === 'compose' ? 'Docker' : target.provider
  return <div className="connection-workspace flex min-h-0 flex-1">
    <div className="relative shrink-0" style={{ width, maxWidth: '25vw' }}>
      <nav aria-label="resources" data-area="nav" tabIndex={-1} className="resource-nav connection-preview">
        <p className="connection-preview-provider">{providerTitle}</p>
        <ul aria-label={t('connection.hint')} className="connection-preview-sections" style={{flexBasis:preview.length * 40}}>
          {preview.map(group => <li key={group} aria-disabled="true" title={`${groupLabel(target.provider, group)} · ${t('connection.hint')}`}>
            <ResourceGroupIcon group={group} /><span>{groupLabel(target.provider, group)}</span>
          </li>)}
        </ul>
      </nav>
      <PanelResize label={t('panels.navigation')} value={width} min={140} max={() => Math.min(360, window.innerWidth * 0.25)} onDone={(navigation) => usePanelWidths.setState({ navigation })} />
    </div>
    <main data-area="table" tabIndex={-1} className="connection-main min-h-0 min-w-0 flex-1 overflow-y-auto">
      <section aria-label={t('connection.status')} className="connection-status-content">
        <div className="connection-scene-mark" aria-hidden="true"><ProviderIcon provider={target.provider} className="h-12 w-12" /><span><PlugIcon disconnected /></span></div>
        <h1>{target.title}</h1>
        {endpoint && <p className="connection-endpoint">{endpoint}</p>}
        <div className="connection-controls">
        <p role="status" className="connection-phase">
          {active && <span aria-hidden className="h-3 w-3 shrink-0 animate-spin rounded-full border border-line border-t-accent" />}
          {phase}
        </p>
        <div className="connection-detail">
        {status && status.state !== 'disconnected' && status.attempt > 0 && <p className="connection-attempt">
          {t('connection.attempt', { attempt: status.attempt, max: status.maxAttempts })} · {t('connection.elapsed', { seconds: elapsed })}
        </p>}
        {!active && status?.state !== 'failed' && <p className="connection-hint">{t('connection.hint')}</p>}
        </div>
        <button type="button" data-area-focus disabled={cancelling} onClick={active ? onCancel : onConnect} className="connection-primary-action">
          {active ? <CloseIcon /> : <PlugIcon />}{active ? t('connection.cancel') : t('connection.connect')}
        </button>
        {status?.error && <div className="connection-error-space"><div role={status.state === 'failed' ? 'alert' : undefined} className="connection-last-error">
          <p className="mb-1 font-medium text-fg-muted">{t('connection.lastError')}{status.errorClass ? ` · ${classLabel(status.errorClass)}` : ''}</p>
          <p className="break-words whitespace-pre-wrap text-fg-muted">{status.error}</p>
        </div></div>}
        </div>
      </section>
    </main>
  </div>
}

function PlugIcon({disconnected=false}:{disconnected?:boolean}) {
  return <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round"><path d="m8 3 3 3m5 5 3 3M6 10l4-4 8 8-4 4a5.7 5.7 0 0 1-8-8ZM6 18l-3 3" />{disconnected && <path d="m4 4 16 16" />}</svg>
}
