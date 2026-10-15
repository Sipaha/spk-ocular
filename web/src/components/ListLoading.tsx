import { useNow } from '../views/useView'
import type { ViewState } from '../views/viewSync'
import { formatBytes } from '../format'
import { t } from '../i18n'

export function ListLoading({ view, label, inline = false }: { view: ViewState & { cancelLoading: () => void }; label?: string; inline?: boolean }) {
  const now = useNow(250)
  const seconds = Math.max(0, Math.floor((now - (view.loadingStartedAt ?? now)) / 1000))
  const bytes = view.status.downloadedBytes
  return <div data-list-loading className={inline ? 'flex shrink-0 items-center gap-2 text-xs text-fg-muted' : 'flex flex-col items-center justify-center gap-3 px-4 py-8 text-sm text-fg-muted'}>
    <div role="status" aria-label={t('app.loading')} className="flex items-center gap-2">
      <span aria-hidden="true" className="h-3.5 w-3.5 shrink-0 animate-spin rounded-full border-2 border-line border-t-accent motion-reduce:animate-none" />
      {!inline && <span>{label ?? t('app.loading')}</span>}
    </div>
    <span data-load-progress className="whitespace-nowrap tabular-nums">{t('view.loadElapsed', { seconds })} · {t('view.loadReceived', { size: bytes === undefined ? '—' : formatBytes(bytes) + 'B' })}</span>
    <button type="button" onClick={view.cancelLoading} className="shrink-0 rounded border border-line px-2 py-1 text-xs hover:bg-hover hover:text-fg">{t('view.loadCancel')}</button>
  </div>
}

export function CancelledList({ retry, pending = false }: { retry: () => void; pending?: boolean }) {
  return <div data-load-cancelled className="flex min-h-0 flex-1 flex-col items-center justify-center gap-3 bg-panel px-4 py-8 text-sm text-fg-muted">
    <p role="status">{t('view.loadCancelled')}</p>
    <button type="button" onClick={retry} disabled={pending} className="rounded border border-line px-3 py-1.5 text-sm hover:bg-hover hover:text-fg">{t('view.loadRetry')}</button>
  </div>
}
