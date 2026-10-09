import { useEffect, useRef, useState } from 'react'
import { isShortcut } from '../keyboard'
import { t, type MessageKey } from '../i18n'
import { useScopeWords } from '../scopeNames'
import { KEYS, type Scope, focusMark, restoreFocus } from '../shortcuts'

const SCOPES: Scope[] = ['global', 'lists', 'table', 'details', 'logs', 'terminal']

/** ?: the keys, from the registry (shortcuts.ts). */
export function HelpDialog({ onClose }: { onClose: () => void }) {
  const [mark] = useState(focusMark)
  const words = useScopeWords()
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    box.current?.focus()
    return () => restoreFocus(mark)
  }, [mark])
  return (
    <div className="fixed inset-0 z-40 flex items-start justify-center bg-black/40 pt-[8vh]" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div
        ref={box}
        role="dialog"
        aria-modal="true"
        aria-label={t('keys.title')}
        tabIndex={-1}
        onKeyDown={(e) => {
          if (e.key === 'Escape' || (!e.altKey && isShortcut(e, 'Slash', { ctrl: false, shift: true }))) {
            e.preventDefault()
            onClose()
          } else if (e.key === 'Tab') {
            e.preventDefault() // nothing else to reach: focus stays
          }
        }}
        className="max-h-[84vh] w-[min(720px,92%)] overflow-y-auto rounded-lg border border-line bg-panel p-4 shadow-2xl outline-none"
      >
        <div className="mb-3 flex items-center">
          <h2 className="font-semibold">{t('keys.title')}</h2>
          <button className="ml-auto rounded px-2 text-lg leading-none text-fg-muted hover:bg-hover hover:text-fg" onClick={onClose} aria-label={t('drawer.close')}>
            ×
          </button>
        </div>
        <div className="grid grid-cols-2 gap-x-6 gap-y-4">
          {SCOPES.map((scope) => (
            <section key={scope} aria-label={t(`keys.scope.${scope}` as MessageKey)}>
              <h3 className="mb-1 text-[12px] font-semibold uppercase tracking-wider text-fg-subtle">{t(`keys.scope.${scope}` as MessageKey)}</h3>
              <dl className="grid grid-cols-[max-content_1fr] gap-x-3 gap-y-1 text-xs">
                {KEYS.filter((k) => k.scope === scope).map((k) => (
                  <div key={k.id} className="contents">
                    <dt>
                      {k.keys.split(' ').map((key) => (
                        <kbd key={key} className="mr-1 rounded border border-line bg-app px-1 font-mono text-[12px] text-fg">
                          {key}
                        </kbd>
                      ))}
                    </dt>
                    <dd className="text-fg-muted">{t(k.help, { scopes: words.plural })}</dd>
                  </div>
                ))}
              </dl>
            </section>
          ))}
        </div>
      </div>
    </div>
  )
}
