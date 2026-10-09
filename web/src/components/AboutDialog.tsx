import { useEffect, useId, useRef, useState } from 'react'
import type { AppInfo } from '../api/types'
import { getLanguage, t } from '../i18n'
import type { Language } from '../languages'
import { focusMark, restoreFocus } from '../shortcuts'
import { openURL } from '../tunnels/open'

export function localizedSiteURL(site: 'about' | 'spk-ocular', language: Language): string {
  return `https://sipaha.github.io/${site}/${language === 'ru' ? '?lang=ru' : language + '/'}`
}

/** App information never replaces the active workspace or its held edits. */
export function AboutDialog({ info, onClose }: { info: AppInfo | null; onClose: () => void }) {
  const title = useId()
  const box = useRef<HTMLElement | null>(null)
  const close = useRef<HTMLButtonElement | null>(null)
  const [mark] = useState(focusMark)
  const [error, setError] = useState('')
  const alive = useRef(true)
  const language = getLanguage()
  useEffect(() => {
    alive.current = true
    close.current?.focus()
    return () => { alive.current = false; restoreFocus(mark) }
  }, [mark])
  const external = (event: React.MouseEvent<HTMLAnchorElement>) => {
    if (info?.mode !== 'desktop') return
    event.preventDefault()
    setError('')
    void openURL(event.currentTarget.href, 'desktop').catch(e => {
      if (alive.current) setError(t('about.linkFailed', { error: e instanceof Error ? e.message : String(e) }))
    })
  }
  const link = 'text-accent underline-offset-4 hover:underline focus-visible:underline'
  return <div className="fixed inset-0 z-40 flex items-start justify-center bg-black/40 pt-[8vh]" onMouseDown={event => {
    if (event.target === event.currentTarget) { event.preventDefault(); onClose() }
  }}>
    <section ref={box} role="dialog" aria-modal="true" aria-labelledby={title}
      className="about-dialog flex max-h-[84vh] w-[min(640px,92%)] min-w-0 flex-col rounded-lg border border-line bg-panel p-4 text-sm shadow-2xl"
      onKeyDown={event => {
        if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); onClose() }
        else if (event.key === 'Tab') {
          const controls = [...(box.current?.querySelectorAll<HTMLElement>('button:not(:disabled), a[href]') ?? [])]
          const index = controls.indexOf(document.activeElement as HTMLElement)
          event.preventDefault()
          controls[(index + (event.shiftKey ? controls.length - 1 : 1)) % controls.length]?.focus()
        }
      }}>
      <header className="mb-4 flex shrink-0 items-start gap-3">
        <img src="./icon.svg" alt="" width="48" height="48" className="h-12 w-12 shrink-0" />
        <div className="min-w-0 flex-1"><h2 id={title} className="mb-1 text-lg font-semibold">{t('about.title')}</h2><p className="text-fg-muted">{t('about.description')}</p></div>
        <button ref={close} type="button" className="rounded px-2 text-lg leading-none text-fg-muted hover:bg-hover hover:text-fg" aria-label={t('action.close')} onClick={onClose}>×</button>
      </header>
      <div className="min-h-0 overflow-y-auto">
        <dl className="mb-4 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-4 gap-y-2">
          <dt className="text-fg-subtle">{t('about.version')}</dt><dd className="break-all font-mono">{info?.version ?? '—'}</dd>
          <dt className="text-fg-subtle">{t('about.license')}</dt><dd><a className={link} href="https://github.com/Sipaha/spk-ocular/blob/master/LICENSE" target="_blank" rel="noopener noreferrer" onClick={external}>Apache License 2.0</a></dd>
        </dl>
        <nav className="mb-5 flex flex-wrap gap-x-5 gap-y-2" aria-label="SPK Ocular">
          <a className={link} href={localizedSiteURL('spk-ocular', language)} target="_blank" rel="noopener noreferrer" onClick={external}>{t('about.website')}</a>
          <a className={link} href="https://github.com/Sipaha/spk-ocular" target="_blank" rel="noopener noreferrer" onClick={external}>{t('about.source')}</a>
        </nav>
        <section className="border-t border-line pt-4">
          <h3 className="mb-2 text-xs text-fg-subtle">{t('about.author')}</h3>
          <p className="mb-2 font-semibold">{language === 'ru' ? 'Павел Симонов' : 'Pavel Simonov'} <span className="font-normal text-fg-subtle">· Sipaha</span></p>
          <a className={link} href={localizedSiteURL('about', language)} target="_blank" rel="noopener noreferrer" onClick={external}>{t('about.authorProfile')}</a>
        </section>
        {error && <p role="alert" className="mt-3 text-danger">{error}</p>}
      </div>
    </section>
  </div>
}
