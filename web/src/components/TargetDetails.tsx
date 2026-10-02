import { useEffect, useId, useRef, useState } from 'react'
import type { Target } from '../api/types'
import { detailLabel, providerText, t } from '../i18n'
import { focusMark, restoreFocus } from '../shortcuts'
import { CloseIcon, EyeIcon, InfoIcon, ProviderIcon } from './icons'

export function EmptyWorkspace() {
  return (
    <div className="empty-workspace">
      <div className="empty-workspace-content">
        <div className="connection-icon"><EyeIcon className="h-7 w-7" /></div>
        <p className="mb-2 text-xl font-semibold tracking-tight">{t('main.pick')}</p>
        <p className="text-sm text-fg-muted">{t('main.pickHint')}</p>
      </div>
    </div>
  )
}

/** Keyed by target in the header: changing/disappearing targets closes its info. */
export function TargetInfoButton({ target }: { target: Target }) {
  const [open, setOpen] = useState(false)
  return <>
    <button type="button" className="context-info-trigger" aria-label={t('target.info')}
      title={t('target.info')} aria-haspopup="dialog" aria-expanded={open} onClick={() => setOpen(true)}>
      <InfoIcon className="h-4 w-4" />
    </button>
    {open && <TargetDetails target={target} onClose={() => setOpen(false)} />}
  </>
}

function TargetDetails({ target, onClose }: { target: Target; onClose: () => void }) {
  const [mark] = useState(focusMark)
  const close = useRef<HTMLButtonElement>(null)
  const facts = useRef<HTMLDListElement>(null)
  const titleId = useId()
  useEffect(() => {
    close.current?.focus()
    return () => restoreFocus(mark)
  }, [mark])
  return (
    <div className="fixed inset-0 z-40 flex items-start justify-center bg-black/40 pt-[8vh]"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) { e.preventDefault(); onClose() }
      }}>
      <section role="dialog" aria-modal="true" aria-labelledby={titleId}
        className="flex max-h-[84vh] w-[min(720px,92%)] min-w-0 flex-col rounded-lg border border-line bg-panel p-4 shadow-2xl"
        onKeyDown={(e) => {
          if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onClose() }
          else if (e.key === 'Tab') {
            e.preventDefault()
            // Two stops: close and the keyboard-scrollable, selectable facts.
            ;(document.activeElement === close.current ? facts.current : close.current)?.focus()
          }
        }}>
        <header className="mb-4 flex shrink-0 items-start gap-3">
          <div className="connection-icon shrink-0"><ProviderIcon provider={target.provider} className="h-6 w-6" /></div>
          <div className="min-w-0 flex-1">
            <h2 id={titleId} className="mb-1 text-xs text-fg-subtle">{t('target.info')}</h2>
            <h3 className="text-lg font-semibold break-all">{target.title}</h3>
            {target.current && <span title={providerText('target.currentHint', target.provider)} className="text-xs text-fg-muted">{t('target.current')}</span>}
          </div>
          <button ref={close} type="button" onClick={onClose} aria-label={t('drawer.close')}
            className="shrink-0 rounded p-1 text-fg-muted hover:bg-hover hover:text-fg">
            <CloseIcon className="h-4 w-4" />
          </button>
        </header>
        <dl ref={facts} tabIndex={0} aria-label={t('target.info')}
          className="connection-facts min-h-0 overflow-y-auto">
          {(target.details ?? []).map((d) => (
            <div key={d.key} className="contents">
              <dt className="text-fg-muted">{detailLabel(d.key)}</dt>
              <dd className="min-w-0 font-mono text-[13px] break-all whitespace-pre-line">{d.value}</dd>
            </div>
          ))}
        </dl>
      </section>
    </div>
  )
}
