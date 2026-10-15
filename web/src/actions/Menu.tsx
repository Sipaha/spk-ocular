import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'

export interface MenuItem {
  id: string
  label: string
  hint?: string
  danger?: boolean
  /** A line above the item (a new group). */
  separator?: boolean
  /** Said, not chosen (nothing to do). */
  disabled?: boolean
  onSelect: () => void
}

interface Props {
  items: MenuItem[]
  /** Where the menu opens (viewport coordinates of its top-left corner). */
  at: { x: number; y: number }
  label: string
  onClose: () => void
}

/**
 * A popup menu: arrows, Home/End, Enter/Space, Esc or a click elsewhere to
 * close. Focus goes back to what had it before, also when an item is
 * chosen: a dialog the item opens returns focus there too.
 */
export function Menu({ items, at, label, onClose }: Props) {
  const ref = useRef<HTMLDivElement>(null)
  const [opener] = useState(() => document.activeElement as HTMLElement | null)
  const [pos, setPos] = useState(at)
  const [active, setActive] = useState<string|null>(()=>items[0]?.id??null)

  const close = () => {
    onClose()
    if (opener?.isConnected) opener.focus()
  }

  // Keep it on screen.
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const r = el.getBoundingClientRect()
    setPos({ x: Math.max(4, Math.min(at.x, window.innerWidth - r.width - 4)), y: Math.max(4, Math.min(at.y, window.innerHeight - r.height - 4)) })
  }, [at.x, at.y])

  useEffect(() => {
    ref.current?.querySelector<HTMLElement>('[role=menuitem]')?.focus()
  }, [])

  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose()
    }
    document.addEventListener('mousedown', onDown, true)
    return () => document.removeEventListener('mousedown', onDown, true)
  }, [onClose])

  const onKey = (e: React.KeyboardEvent) => {
    const list = [...(ref.current?.querySelectorAll<HTMLElement>('[role=menuitem]') ?? [])]
    const i = list.indexOf(document.activeElement as HTMLElement)
    const go = (n: number) => list[(n + list.length) % list.length]?.focus()
    switch (e.key) {
      case 'Enter':
      case ' ':
        // Preserve the button's native activation, but keep React portal
        // events out of ancestor target/resource keyboard handlers.
        e.stopPropagation()
        return
      case 'ArrowDown':
        go(i + 1)
        break
      case 'ArrowUp':
        go(i - 1)
        break
      case 'Home':
        go(0)
        break
      case 'End':
        go(list.length - 1)
        break
      case 'Escape':
      case 'Tab':
        close()
        break
      default:
        return
    }
    e.preventDefault()
    e.stopPropagation()
  }

  return createPortal(
    <div
      ref={ref}
      role="menu"
      aria-label={label}
      onKeyDown={onKey}
      onContextMenu={(e) => e.preventDefault()}
      onPointerLeave={()=>setActive(null)}
      className="popup-menu fixed z-50 min-w-44 rounded-md border border-line bg-panel"
      style={{ left: pos.x, top: pos.y }}
    >
      {items.map((it) => (
        <div key={it.id}>
          {it.separator && <div role="separator" className="my-1 border-t border-line" />}
          <button
            role="menuitem"
            title={it.hint}
            // Enter held from what opened the menu: the repeats must not choose.
            onKeyDown={(e) => e.key === 'Enter' && e.repeat && e.preventDefault()}
            // Pointer and keyboard share one explicit highlight, independent
            // of pointer-focus painting in the host webview.
            onFocus={()=>setActive(it.id)}
            onPointerMove={(e) => {setActive(it.id);e.currentTarget.focus({ preventScroll: true })}}
            data-active={active===it.id||undefined}
            aria-disabled={it.disabled || undefined}
            onClick={() => {
              if (it.disabled) return
              close()
              it.onSelect()
            }}
            className={['block w-full px-3 py-1 text-left outline-none', it.disabled ? 'text-fg-subtle' : it.danger ? 'text-danger' : 'text-fg'].join(' ')}
          >
            {it.label}
          </button>
        </div>
      ))}
    </div>, document.body
  )
}
