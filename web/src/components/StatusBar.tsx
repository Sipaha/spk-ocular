import { t } from '../i18n'
import { useStore } from '../store'
import { TunnelsIndicator } from '../tunnels/TunnelsPanel'
import { AgentsIndicator } from '../agents/AgentsPanel'

export function StatusBar({ locationRef }: { locationRef?: (element: HTMLDivElement | null) => void }) {
  const info = useStore((s) => s.info)
  const error = useStore((s) => s.actionError)
  const notice = useStore((s) => s.notice)
  return (
    <footer className="status-bar flex shrink-0 items-center gap-3 border-t border-line bg-sidebar px-3 text-[12px] text-fg-subtle">
      {error && <span role="alert" className="truncate text-danger">{t('error.selectFailed', { error })}</span>}
      <div ref={locationRef} data-location-status className="flex min-w-0 flex-1 items-center gap-3 overflow-x-auto whitespace-nowrap [&>*]:shrink-0" />
      <span role="status" className="min-w-0 truncate text-fg-muted">
        {!error && notice}
      </span>
      <div data-general-status className="ml-auto flex shrink-0 items-center gap-3 whitespace-nowrap">
      <TunnelsIndicator />
      <AgentsIndicator />
      <span>{info ? `${info.name} ${info.version} · ${t(info.mode === 'desktop' ? 'status.desktop' : 'status.browser')}` : ''}</span>
      </div>
    </footer>
  )
}
