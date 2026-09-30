import { t } from '../i18n'
import { useStore } from '../store'
import { TunnelsIndicator } from '../tunnels/TunnelsPanel'

export function StatusBar() {
  const info = useStore((s) => s.info)
  const error = useStore((s) => s.actionError)
  const notice = useStore((s) => s.notice)
  return (
    <footer className="flex h-6 shrink-0 items-center gap-3 border-t border-line bg-sidebar px-3 text-[12px] text-fg-subtle">
      {error && <span role="alert" className="truncate text-danger">{t('error.selectFailed', { error })}</span>}
      <span role="status" className="min-w-0 flex-1 truncate text-fg-muted">
        {!error && notice}
      </span>
      <TunnelsIndicator />
      <span className="ml-auto">{info ? `${info.name} ${info.version} · ${t(info.mode === 'desktop' ? 'status.desktop' : 'status.browser')}` : ''}</span>
    </footer>
  )
}
