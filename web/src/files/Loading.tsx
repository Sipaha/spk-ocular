import { t } from '../i18n'

export function Loading({ label = t('app.loading'), compact = false }: { label?: string; compact?: boolean }) {
  return <span role="status" className="inline-flex max-w-full min-w-0 items-center gap-2 text-[13px] leading-5 text-fg-muted">
    <span aria-hidden="true" className="h-3.5 w-3.5 shrink-0 animate-spin rounded-full border-2 border-line border-t-accent motion-reduce:animate-none" />
    <span className={compact ? 'sr-only' : 'min-w-0 truncate'} title={label}>{label}</span>
  </span>
}
