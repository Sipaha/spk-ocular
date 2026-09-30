import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { t } from '../i18n'
import { SearchIcon } from './icons'

interface Props {
  /** The chosen name; '' is all. */
  value: string
  names: string[]
  /** The picker's name ("Namespace"). */
  label: string
  /** The '' choice ("All namespaces"). */
  allLabel: string
  onChange: (name: string) => void
}

/**
 * A scope picker with search: typing filters the names and marks the first
 * found, arrows move the mark, Enter chooses, Esc or a click elsewhere
 * closes. Not a native select: WebKitGTK draws its popup with the system's
 * font and look, and it cannot search.
 */
export function ScopeSelect({ value, names, label, allLabel, onChange }: Props) {
  const [open, setOpen] = useState(false)
  const button = useRef<HTMLButtonElement>(null)
  return (
    <div className="relative">
      <button
        ref={button}
        type="button"
        aria-label={label}
        aria-haspopup="listbox"
        aria-expanded={open}
        title={label}
        onClick={() => setOpen((o) => !o)}
        className="select-look max-w-64 truncate rounded-md border border-line bg-app py-1 pl-2 pr-7 text-left outline-none focus:border-accent"
      >
        {value || allLabel}
      </button>
      {open && (
        <ScopeList
          value={value}
          names={names}
          label={label}
          allLabel={allLabel}
          onClose={(back) => {
            setOpen(false)
            if (back) button.current?.focus()
          }}
          onChange={onChange}
          button={button}
        />
      )}
    </div>
  )
}

interface Item {
  name: string
  text: string
}

function ScopeList(props: Props & { onClose: (back: boolean) => void; button: React.RefObject<HTMLButtonElement | null> }) {
  const { value, names, label, allLabel, onChange, onClose, button } = props
  const [query, setQuery] = useState('')
  const [index, setIndex] = useState<number | null>(null)
  const box = useRef<HTMLDivElement>(null)
  const list = useRef<HTMLDivElement>(null)
  const input = useRef<HTMLInputElement>(null)

  const items = useMemo<Item[]>(() => {
    const q = query.trim().toLowerCase()
    const found = names.filter((n) => n.toLowerCase().includes(q)).map((n) => ({ name: n, text: n }))
    // Searching: the first found is what Enter takes, so "all" is not in the way.
    return q ? found : [{ name: '', text: allLabel }, ...found]
  }, [names, query, allLabel])
  const at = index ?? (query.trim() ? 0 : Math.max(0, items.findIndex((i) => i.name === value)))
  const active = items.length ? Math.min(at, items.length - 1) : -1

  useEffect(() => input.current?.focus(), [])
  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      const el = e.target as Node
      if (box.current?.contains(el) || button.current?.contains(el)) return
      onClose(false)
    }
    document.addEventListener('mousedown', onDown, true)
    return () => document.removeEventListener('mousedown', onDown, true)
  }, [onClose, button])
  useLayoutEffect(() => {
    list.current?.querySelector(`[data-index="${active}"]`)?.scrollIntoView?.({ block: 'nearest' })
  }, [active])

  const choose = (it: Item) => {
    onClose(true)
    if (it.name !== value) onChange(it.name)
  }
  const onKey = (e: React.KeyboardEvent) => {
    if (e.nativeEvent.isComposing) return
    const n = items.length
    const move = (to: number) => {
      if (n) setIndex(((to % n) + n) % n)
    }
    if (e.key === 'ArrowDown') move(active + 1)
    else if (e.key === 'ArrowUp') move(active - 1)
    else if (e.key === 'PageDown') move(Math.min(n - 1, active + 10))
    else if (e.key === 'PageUp') move(Math.max(0, active - 10))
    else if (e.key === 'Enter') {
      if (active >= 0) choose(items[active])
    } else if (e.key === 'Escape') onClose(true)
    else if (e.key === 'Tab') onClose(false)
    else return
    if (e.key !== 'Tab') e.preventDefault()
    // The picker's keys are its own: not the drawer's Esc, not a shortcut.
    e.stopPropagation()
  }

  return (
    <div ref={box} onKeyDown={onKey} className="absolute left-0 top-full z-30 mt-1 flex w-72 flex-col overflow-hidden rounded-md border border-line bg-panel shadow-xl">
      <label className="flex items-center gap-2 border-b border-line px-2 py-1.5">
        <SearchIcon className="h-3.5 w-3.5 shrink-0 text-fg-subtle" />
        <input
          ref={input}
          role="combobox"
          aria-label={t('scope.search', { scope: label.toLowerCase() })}
          aria-expanded="true"
          aria-controls="scope-list"
          aria-activedescendant={active >= 0 ? `scope-opt-${active}` : undefined}
          value={query}
          onChange={(e) => {
            setQuery(e.target.value)
            setIndex(null)
          }}
          placeholder={t('scope.search', { scope: label.toLowerCase() })}
          spellCheck={false}
          autoComplete="off"
          className="w-full bg-transparent outline-none placeholder:text-fg-subtle focus-visible:outline-none"
        />
      </label>
      <div ref={list} id="scope-list" role="listbox" aria-label={label} className="max-h-80 overflow-y-auto py-1">
        {items.map((it, i) => (
          <div
            key={it.name || '\u0000all'}
            id={`scope-opt-${i}`}
            data-index={i}
            role="option"
            aria-selected={i === active}
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => choose(it)}
            onMouseMove={() => i !== active && setIndex(i)}
            className={['flex cursor-pointer items-center gap-2 truncate px-2 py-1', i === active ? 'bg-active text-fg' : 'text-fg-muted', it.name ? '' : 'italic'].join(' ')}
          >
            <span aria-hidden="true" className="w-3 shrink-0 text-accent">{it.name === value ? '✓' : ''}</span>
            <span className="min-w-0 truncate">{it.text}</span>
          </div>
        ))}
        {!items.length && (
          <p role="status" className="px-3 py-2 text-fg-subtle">
            {t('palette.empty')}
          </p>
        )}
      </div>
    </div>
  )
}
