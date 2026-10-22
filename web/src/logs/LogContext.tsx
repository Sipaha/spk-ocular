import type { Ref, LogComposition } from '../api/types'
import { t } from '../i18n'
import { columnLabel } from '../presentation'
import { refTitle } from '../refs'

/** Identify the log source; period controls remain in the toolbar. */
export function LogContext({ subject, composition, connectionName, loading = false }: { subject: Ref; composition?:LogComposition; connectionName?: string; loading?: boolean }) {
  const scopeLabel = columnLabel(subject.provider === 'kubernetes' ? 'Namespace' : subject.provider === 'compose' ? 'Project' : 'Scope')
  return <div data-log-context className="flex shrink-0 flex-wrap items-baseline gap-x-3 gap-y-0.5 border-b border-line px-3 py-1 text-xs text-fg-subtle [overflow-wrap:anywhere]">
    <span>{t('logs.connection')}: <span className="text-fg">{connectionName || '—'}</span></span>
    {!composition&&subject.scope && <span>{scopeLabel}: <span className="text-accent">{subject.scope}</span></span>}
    {composition?<span className="text-accent">{t('logs.composition.count',{count:composition.refs.length})}</span>:<span title={`${subject.kind}/${subject.name}`}>{subject.kind}: <span className="text-accent">{refTitle(subject)}</span></span>}
    <span className="ml-auto flex h-3.5 w-3.5 shrink-0">{loading && <span data-log-loading role="progressbar" aria-label={t('app.loading')} className="h-3.5 w-3.5 animate-spin rounded-full border-2 border-line border-t-accent motion-reduce:animate-none" />}</span>
  </div>
}
