import { t } from '../i18n'
import { useStore } from '../store'

export function StatusBar() {
  const info = useStore((s) => s.info)
  const error = useStore((s) => s.actionError)
  return (
    <footer className="flex h-6 shrink-0 items-center gap-3 border-t border-line bg-sidebar px-3 text-[11px] text-fg-subtle">
      {error ? <span role="alert" className="truncate text-danger">{t('error.selectFailed', { error })}</span> : <span className="flex-1" />}
      <span className="ml-auto">{info ? `${info.name} ${info.version} · ${t(info.mode === 'desktop' ? 'status.desktop' : 'status.browser')}` : ''}</span>
    </footer>
  )
}
