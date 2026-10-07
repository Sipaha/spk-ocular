import { t } from '../i18n'

/** Loading decorates an existing layout; the caller makes stale controls inert. */
export function LoadingOverlay({ label = t('app.loading') }: { label?: string }) {
  return <div className="pointer-events-none absolute inset-0 z-20 flex items-start justify-center bg-app/65 pt-6" role="status" aria-live="polite" data-loading-overlay>
    <span className="flex items-center gap-2 rounded border border-line bg-panel px-3 py-1.5 text-sm text-fg-muted">
      <span aria-hidden className="h-3 w-3 animate-spin rounded-full border border-line border-t-accent motion-reduce:animate-none" />{label}
    </span>
  </div>
}
