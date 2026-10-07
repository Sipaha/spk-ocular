import type { Client } from '../api/client'
import { LanguageMenu } from './LanguageMenu'
import type { Target } from '../api/types'
import { t } from '../i18n'
import { openPalette } from '../palette/store'
import { ProviderIcon, SearchIcon } from './icons'
import { TargetInfoButton } from './TargetDetails'

/** App-wide navigation stays visible while individual panels scroll. */
export function AppHeader({ target, onHelp, onAbout, client }: { target: Target | null; onHelp: () => void; onAbout: () => void; client: Client }) {
  return (
    <header className="app-header">
      <button type="button" className="app-brand flex items-center gap-2" onClick={onAbout} aria-label={t('about.title')} title={t('about.title')} aria-haspopup="dialog"><img src="./icon.svg" width="24" height="24" alt="" className="h-6 w-6" />SPK Ocular</button>
      <div className="app-context" aria-label={t('shell.context')}>
        {target ? <>
          <ProviderIcon provider={target.provider} className="h-4 w-4 shrink-0 text-fg-muted" />
          <span className="truncate" title={target.title}>{target.title}</span>
          <TargetInfoButton key={`${target.provider}/${target.id}`} target={target} />
        </> : <span className="text-fg-muted">{t('shell.workspace')}</span>}
      </div>
      <button className="command-trigger" onClick={openPalette} aria-label={`${t('palette.label')} (Ctrl+K)`}>
        <SearchIcon className="h-4 w-4 shrink-0" />
        <span className="truncate">{t('shell.search')}</span>
        <kbd>Ctrl K</kbd>
      </button>
      <LanguageMenu client={client} />
      <button className="help-trigger" onClick={onHelp} aria-label={t('keys.title')} title={`${t('keys.title')} (?)`}>?</button>
    </header>
  )
}
