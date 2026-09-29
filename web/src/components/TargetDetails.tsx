import { detailLabel, t } from '../i18n'
import { selectedTarget, useStore } from '../store'
import { EyeIcon } from './icons'

export function TargetDetails() {
  const target = useStore((s) => selectedTarget(s.view))
  if (!target) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-2 text-fg-subtle">
        <EyeIcon className="h-10 w-10 opacity-40" />
        <p className="text-sm text-fg-muted">{t('main.pick')}</p>
        <p className="text-xs">{t('main.pickHint')}</p>
      </div>
    )
  }
  return (
    <div className="mx-auto max-w-4xl px-8 py-6">
      <header className="mb-5 flex items-baseline gap-3">
        <h1 className="truncate text-xl font-semibold">{target.title}</h1>
        {target.current && (
          <span title={t('target.currentHint')} className="rounded bg-panel px-1.5 py-0.5 text-xs text-fg-muted">
            {t('target.current')}
          </span>
        )}
      </header>
      <dl className="grid grid-cols-[max-content_1fr] gap-x-6 gap-y-2 rounded-lg border border-line bg-panel/40 p-4">
        {(target.details ?? []).map((d) => (
          <div key={d.key} className="contents">
            <dt className="text-fg-muted">{detailLabel(d.key)}</dt>
            <dd className="min-w-0 font-mono text-[12px] break-all whitespace-pre-line">{d.value}</dd>
          </div>
        ))}
      </dl>
    </div>
  )
}
