import { useLayoutEffect, useRef } from 'react'
import { t } from '../i18n'
import { discardEdits, keepEditing, leaveAsked, useLeaveAsked } from './guard'

const btn = 'rounded-md px-3 py-1 outline-none focus:ring-2 focus:ring-accent focus:ring-offset-2 focus:ring-offset-panel'

/** "Discard edits?" — asked by mayLeave; the safe answer has the focus. */
export function DiscardPrompt() {
  const asked = useLeaveAsked()
  if (!asked) return null
  return <Prompt />
}

function Prompt() {
  const keep = useRef<HTMLButtonElement>(null)
  const box = useRef<HTMLDivElement>(null)
  useLayoutEffect(() => keep.current?.focus(), [])
  // While asked, focus stays here: whatever closes along with the request
  // (the palette giving focus back) must not take it behind the question.
  // Answered, the answer places it (the editor, the next page). A layout
  // effect: in place before the closing palette's passive cleanup runs.
  useLayoutEffect(() => {
    const onFocus = (e: FocusEvent) => {
      if (leaveAsked() && box.current && !box.current.contains(e.target as Node)) keep.current?.focus()
    }
    document.addEventListener('focusin', onFocus, true)
    return () => document.removeEventListener('focusin', onFocus, true)
  }, [])
  const onKey = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      keepEditing()
    } else if (e.key === 'Enter' && e.repeat) {
      // Held from the key that asked: never an answer.
      e.preventDefault()
      e.stopPropagation()
    } else if (e.key === 'Tab') {
      const list = [...(box.current?.querySelectorAll<HTMLElement>('button') ?? [])]
      const i = list.indexOf(document.activeElement as HTMLElement)
      e.preventDefault()
      list[(i + (e.shiftKey ? list.length - 1 : 1)) % list.length]?.focus()
    }
  }
  return (
    <div className="fixed inset-0 z-40 flex items-start justify-center bg-black/40 pt-32" onMouseDown={(e) => e.target === e.currentTarget && keepEditing()}>
      <div
        ref={box}
        role="alertdialog"
        aria-modal="true"
        aria-label={t('edit.discardTitle')}
        aria-describedby="discard-text"
        onKeyDown={onKey}
        className="flex w-[min(420px,92%)] flex-col gap-3 rounded-lg border border-line bg-panel p-4 shadow-2xl"
      >
        <h2 className="font-semibold">{t('edit.discardTitle')}</h2>
        <p id="discard-text" className="text-fg-muted">
          {t('edit.discardText')}
        </p>
        <div className="flex justify-end gap-2">
          <button type="button" className={`${btn} text-danger hover:bg-hover`} onClick={discardEdits}>
            {t('edit.discard')}
          </button>
          <button ref={keep} type="button" className={`${btn} bg-accent text-accent-fg`} onClick={keepEditing}>
            {t('edit.keep')}
          </button>
        </div>
      </div>
    </div>
  )
}
