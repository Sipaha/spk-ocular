import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { t } from '../i18n'
import { SearchIcon } from './icons'

export interface SelectOption {
  value: string
  label: string
  disabled?: boolean
  /** Shown only while not searching, set apart ("All namespaces"). */
  pinned?: boolean
}

/** Owned by one workspace; survives its scope-keyed page, never another target. */
interface SelectSnapshot {
  open?: boolean
  query?: string
  index?: number | null
  scroll?: number
  focus?: 'search' | 'list'
}

export interface SelectMemory {
  read: () => SelectSnapshot
  update: (patch: Partial<SelectSnapshot>) => void
}
export function createSelectMemory(): SelectMemory {
  let snapshot: SelectSnapshot = {}
  return { read: () => snapshot, update: (patch) => { snapshot = { ...snapshot, ...patch } } }
}

interface Multiple {
  selected: string[]
  summary: string
  onToggle: (value: string) => void
  /** An edit confirmation must appear above a closed picker. */
  closeOnToggle?: () => boolean
}

interface Props {
  value: string
  options: SelectOption[]
  /** The picker's name (its button's accessible name and title). */
  label: string
  onChange: (value: string) => void
  /** A search field on top; by default for long lists. Its name. */
  search?: boolean
  searchLabel?: string
  /** The button's look (size): the field it stands in. */
  className?: string
  disabled?: boolean
  memory?: SelectMemory
  multiple?: Multiple
}

const BUTTON = 'select-look max-w-full truncate rounded-md border border-line bg-app py-1 pl-2 pr-7 text-left outline-none focus:border-accent disabled:opacity-50'

/**
 * The app's own select: a button and a list in the app's look (WebKitGTK
 * draws a native select's popup with the system's font and look). Arrows,
 * Home/End, PageUp/PageDown move, Enter or a click chooses, Esc or a click
 * elsewhere closes; a letter jumps to the next option starting with it; with
 * search, typing filters and marks the first found.
 */
export function Select({ value, options, label, onChange, search, searchLabel, className, disabled, memory, multiple }: Props) {
  const [open, setOpenNow] = useState(memory?.read().open ?? false)
  const setOpen = (next: boolean) => {
    memory?.update(next ? { open: true } : { open: false, query: '', index: null, scroll: 0, focus: 'search' })
    setOpenNow(next)
  }
  const button = useRef<HTMLButtonElement>(null)
  const current = options.find((o) => o.value === value)
  const withSearch = search ?? options.length > 12
  const onKey = (e: React.KeyboardEvent) => {
    if (open || e.altKey || e.ctrlKey || e.metaKey) return
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      // Like a native select: the list opens, the keys are not the page's.
      e.preventDefault()
      e.stopPropagation()
      setOpen(true)
    }
  }
  return (
    <>
      <button
        ref={button}
        type="button"
        aria-label={label}
        aria-haspopup="listbox"
        aria-expanded={open}
        title={label}
        disabled={disabled}
        onClick={() => setOpen(!open)}
        onKeyDown={onKey}
        className={[BUTTON, className ?? ''].join(' ')}
      >
        {multiple?.summary ?? current?.label ?? value}
      </button>
      {open && (
        <List
          value={value}
          options={options}
          label={label}
          search={withSearch ? (searchLabel ?? t('select.search')) : null}
          anchor={button}
          onClose={(back) => {
            setOpen(false)
            if (back) button.current?.focus()
          }}
          onChange={onChange}
          memory={memory}
          multiple={multiple}
        />
      )}
    </>
  )
}

interface ListProps {
  value: string
  options: SelectOption[]
  label: string
  search: string | null
  anchor: React.RefObject<HTMLButtonElement | null>
  onClose: (back: boolean) => void
  onChange: (value: string) => void
  memory?: SelectMemory
  multiple?: Multiple
}

function List({ value, options, label, search, anchor, onClose, onChange, memory, multiple }: ListProps) {
  const id = useId()
  const [query, setQueryNow] = useState(memory?.read().query ?? '')
  const [index, setIndexNow] = useState<number | null>(memory?.read().index ?? null)
  const setQuery = (next: string) => { memory?.update({ query: next }); setQueryNow(next) }
  const setIndex = (next: number | null) => { memory?.update({ index: next }); setIndexNow(next) }
  const [pos, setPos] = useState<{ left: number; top?: number; bottom?: number; minWidth: number; maxHeight: number } | null>(null)
  const box = useRef<HTMLDivElement>(null)
  const list = useRef<HTMLDivElement>(null)
  const input = useRef<HTMLInputElement>(null)
  const restoredScroll = useRef(memory?.read().scroll ?? 0)
  const close = useRef(onClose)
  useEffect(() => {
    close.current = onClose
  })

  const q = query.trim().toLowerCase()
  const items = useMemo(() => (q ? options.filter((o) => !o.pinned && o.label.toLowerCase().includes(q)) : options), [options, q])
  const firstEnabled = (from: number, step: 1 | -1) => {
    const n = items.length
    for (let k = 0; k < n; k++) {
      const i = (((from + k * step) % n) + n) % n
      if (!items[i].disabled) return i
    }
    return -1
  }
  const initial = q ? firstEnabled(0, 1) : Math.max(0, items.findIndex((o) => o.value === value))
  const active = items.length ? Math.min(index ?? initial, items.length - 1) : -1

  // Under the button (above it when there is no room), fixed: a dialog's
  // scrolling box does not cut it off.
  useLayoutEffect(() => {
    const place = () => {
      const r = anchor.current?.getBoundingClientRect()
      if (!r) return
      const below = window.innerHeight - r.bottom - 8
      const above = r.top - 8
      const up = below < 200 && above > below
      setPos({
        left: Math.max(4, Math.min(r.left, window.innerWidth - Math.max(r.width, 160) - 4)),
        ...(up ? { bottom: window.innerHeight - r.top + 4 } : { top: r.bottom + 4 }),
        minWidth: r.width,
        maxHeight: Math.max(120, Math.min(360, up ? above : below)),
      })
    }
    place()
    window.addEventListener('resize', place)
    return () => window.removeEventListener('resize', place)
  }, [anchor])

  useEffect(() => {
    ;(memory?.read().focus === 'list' ? list.current : input.current ?? list.current)?.focus()
    const onDown = (e: MouseEvent) => {
      const el = e.target as Node
      if (box.current?.contains(el) || anchor.current?.contains(el)) return
      close.current(false)
    }
    // Scrolling what the button is in moves it away: close. Other boxes
    // scrolling (live logs beside it) leave it open.
    const onScroll = (e: Event) => {
      const el = e.target as Node
      if (box.current?.contains(el)) return
      // The focus it had goes back to the button (not to nowhere).
      if (anchor.current && el.contains?.(anchor.current)) close.current(!!box.current?.contains(document.activeElement))
    }
    document.addEventListener('mousedown', onDown, true)
    document.addEventListener('scroll', onScroll, true)
    return () => {
      document.removeEventListener('mousedown', onDown, true)
      document.removeEventListener('scroll', onScroll, true)
    }
  }, [anchor, memory])
  useLayoutEffect(() => {
    if (restoredScroll.current && list.current) {
      list.current.scrollTop = restoredScroll.current
      if (pos) restoredScroll.current = 0
      return
    }
    list.current?.querySelector(`[data-index="${active}"]`)?.scrollIntoView?.({ block: 'nearest' })
  }, [active, pos])

  const choose = (o: SelectOption) => {
    if (o.disabled) return
    onClose(true)
    if (multiple || o.value !== value) onChange(o.value)
  }
  const toggle = (o: SelectOption) => {
    if (!multiple || o.disabled || o.pinned) return
    if (multiple.closeOnToggle?.()) onClose(true)
    multiple.onToggle(o.value)
  }
  const onKey = (e: React.KeyboardEvent) => {
    if (e.nativeEvent.isComposing) return
    const n = items.length
    const at = (i: number) => {
      if (n && i >= 0) setIndex(i)
    }
    const page = (step: 1 | -1) => {
      let i = active
      for (let k = 0; k < 10; k++) {
        const j = i + step
        if (j < 0 || j >= n) break
        i = j
      }
      at(items[i]?.disabled ? firstEnabled(i, step) : i)
    }
    if (e.key === 'ArrowDown') at(firstEnabled(active + 1, 1))
    else if (e.key === 'ArrowUp') at(firstEnabled(active - 1, -1))
    else if (e.key === 'Home') at(firstEnabled(0, 1))
    else if (e.key === 'End') at(firstEnabled(n - 1, -1))
    else if (e.key === 'PageDown') page(1)
    else if (e.key === 'PageUp') page(-1)
    else if (e.key === ' ' && multiple && e.target !== input.current) {
      if (active >= 0) toggle(items[active])
    } else if (e.key === 'Enter' || (e.key === ' ' && !search)) {
      if (active >= 0) choose(items[active])
    } else if (e.key === 'Escape') onClose(true)
    else if (e.key === 'Tab') {
      // The Tab goes on from the button: focus it now, the key's default
      // (or a dialog's focus trap) moves on from there.
      onClose(true)
      return
    } else if (!search && e.key.length === 1 && !e.altKey && !e.ctrlKey && !e.metaKey) {
      // A letter: the next option starting with it.
      const c = e.key.toLowerCase()
      for (let k = 1; k <= n; k++) {
        const i = (active + k) % n
        if (!items[i].disabled && items[i].label.toLowerCase().startsWith(c)) {
          at(i)
          break
        }
      }
    } else if (search && (e.key.length === 1 || e.key === 'Backspace' || e.key === 'Delete' || e.key.startsWith('Arrow'))) {
      // Typing in the search field: its own, and no shortcut of the page.
      e.stopPropagation()
      return
    } else return
    e.preventDefault()
    // The list's keys are its own: not a dialog's Esc, not a shortcut.
    e.stopPropagation()
  }

  const optId = (i: number) => `${id}-opt-${i}`
  return (
    <div
      ref={box}
      onKeyDown={onKey}
      style={pos ? { position: 'fixed', left: pos.left, top: pos.top, bottom: pos.bottom, minWidth: pos.minWidth } : { position: 'fixed', left: 0, top: 0, opacity: 0, pointerEvents: 'none' }}
      className={['z-50 flex max-w-[min(90vw,32rem)] flex-col overflow-hidden rounded-md border border-line bg-panel text-[14px] text-fg shadow-xl', search ? 'w-72' : ''].join(' ')}
    >
      {search && (
        <label className="flex items-center gap-2 border-b border-line px-2 py-1.5">
          <SearchIcon className="h-3.5 w-3.5 shrink-0 text-fg-subtle" />
          <input
            ref={input}
            onFocus={() => { memory?.update({ focus: 'search' }) }}
            role="combobox"
            aria-label={search}
            aria-expanded="true"
            aria-controls={`${id}-list`}
            aria-activedescendant={active >= 0 ? optId(active) : undefined}
            value={query}
            onChange={(e) => {
              setQuery(e.target.value)
              setIndex(null)
            }}
            placeholder={search}
            spellCheck={false}
            autoComplete="off"
            className="w-full bg-transparent outline-none placeholder:text-fg-subtle focus-visible:outline-none"
          />
        </label>
      )}
      <div
        ref={list}
        id={`${id}-list`}
        role="listbox"
        onFocus={() => { memory?.update({ focus: 'list' }) }}
        aria-multiselectable={multiple ? true : undefined}
        aria-label={label}
        tabIndex={multiple ? 0 : search ? undefined : -1}
        onScroll={(e) => { memory?.update({ scroll: e.currentTarget.scrollTop }) }}
        aria-activedescendant={!search && active >= 0 ? optId(active) : undefined}
        style={{ maxHeight: pos?.maxHeight ?? 320 }}
        className="overflow-y-auto py-1 outline-none focus-visible:outline-none"
      >
        {items.map((o, i) => (
          <div
            key={`${o.pinned ? 'p' : 'o'}:${o.value}`}
            id={optId(i)}
            data-index={i}
            role="option"
            aria-label={o.label}
            aria-selected={multiple ? (o.pinned ? value === '' : multiple.selected.includes(o.value)) : i === active}
            aria-disabled={o.disabled || undefined}
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => choose(o)}
            onMouseMove={() => i !== active && !o.disabled && setIndex(i)}
            className={[
              'flex items-center gap-2 px-2 py-1',
              o.disabled ? 'cursor-default text-fg-subtle' : 'cursor-pointer',
              i === active ? 'bg-active text-fg' : o.disabled ? '' : 'text-fg-muted',
              o.pinned ? 'italic' : '',
            ].join(' ')}
          >
            {multiple && !o.pinned ? (
              <span className="relative flex h-3.5 w-3.5 shrink-0 items-center justify-center">
                <input type="checkbox" aria-label={o.label} checked={multiple.selected.includes(o.value)} disabled={o.disabled} tabIndex={-1}
                  onClick={(e) => e.stopPropagation()} onChange={() => toggle(o)}
                  className="scope-checkbox absolute inset-0 m-0 h-full w-full cursor-pointer appearance-none rounded-sm border border-fg-subtle bg-transparent hover:border-fg-muted checked:border-accent checked:bg-accent checked:hover:border-accent disabled:cursor-default" />
                {multiple.selected.includes(o.value) && <svg aria-hidden="true" viewBox="0 0 10 10" className="pointer-events-none relative h-2.5 w-2.5 text-accent-fg" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M2 5.2 4.2 7.4 8 2.8" />
                </svg>}
              </span>
            ) : (
              <span aria-hidden="true" className="w-4 shrink-0 text-accent">{o.value === value ? '✓' : ''}</span>
            )}
            <span className="min-w-0 truncate">{o.label}</span>
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
