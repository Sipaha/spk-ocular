import { columnLabel } from '../presentation'
import { useVirtualizer } from '@tanstack/react-virtual'
import { useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import { ColumnResize } from './ColumnResize'
import type { Cell, Column, HealthState, MetricsView, Row, SortSpec } from '../api/types'
import { formatAge, formatBytes, formatCPU } from '../format'
import { isShortcut } from '../keyboard'
import { Menu, type MenuItem } from '../actions/Menu'
import { t } from '../i18n'
import { useNow } from '../views/useView'

const ROW_H = 32

export interface Sort {
  col: number
  desc: boolean
}

interface Props {
  emptyMessage?: ReactNode
  columns: Column[]
  rows: Row[]
  /** Hide the scope (namespace) column: one scope is shown. */
  hideScope: boolean
  filter: string
  selected: string | null
  /** One-shot reveal from navigation (a fresh object can reveal the same row again). */
  reveal?: { id: string } | null
  onSelect: (row: Row) => void
  onOpen?: (row: Row) => void
  /** L on a row: its logs (kinds that have them). */
  onLogs?: (row: Row) => void
  /** S: a terminal in the default container; Shift+S: the dialog */
  onTerminal?: (row: Row, dialog: boolean) => void
  /** Usage for metric columns (CPU/Memory), by row id. */
  metrics?: MetricsView | null
  /** The rows in view (not the overscan), in order, whenever they change. */
  onVisibleRows?: (ids: string[]) => void
  /**
   * The context menu of a row (right click, the Menu key, Shift+F10): built
   * for that row when it opens.
   */
  rowMenu?: (row: Row) => MenuItem[] | { label: string; items: MenuItem[] }
  /** Delete on the focused table: the selected row (none: only marks). */
  onDelete?: (row: Row | undefined) => void
  /** The kind's first sort (else by the first column). */
  defaultSort?: SortSpec
  /** The sort the table was left with (a column id), before defaultSort; onSort hears each chosen. */
  initialSort?: { col: string; desc: boolean }
  onSort?: (s: { col: string; desc: boolean }) => void
  initialWidths?: Record<string, number>
  onWidths?: (widths: Record<string, number>) => void
  /** The table of its area (F6 focuses it); not a table inside details. */
  areaFocus?: boolean
  /**
   * The marked rows (for an action on several), apart from the cursor
   * (selected): with onMarked the table has a column of checkboxes.
   */
  marked?: ReadonlySet<string>
  onMarked?: (next: ReadonlySet<string>) => void
}

const MARK_W = 32

export const healthText: Record<HealthState, string> = {
  ok: 'text-fg',
  progressing: 'text-accent',
  warning: 'text-warning',
  error: 'text-danger',
  terminating: 'text-fg-subtle',
  unknown: 'text-fg-muted',
}

/** A metric's cell: partial — a sum some parts are missing from. */
export type MetricCell = Cell & { partial?: boolean }

export function cellText(c: Column, cell: MetricCell | undefined, now: number): string {
  if (!cell) return ''
  const atLeast = cell.partial ? '≥ ' : ''
  switch (c.type) {
    case 'age':
      return cell.time ? formatAge(now - cell.time) : ''
    case 'cpu':
      return cell.num !== undefined ? atLeast + formatCPU(cell.num) : ''
    case 'bytes':
      return cell.num !== undefined ? atLeast + formatBytes(cell.num) : ''
  }
  return cell.text ?? ''
}

/** How many values of a metric an answer has (0 while none: a sort by it waits for them). */
function metricKnown(m: MetricsView | null | undefined, c: Column | undefined): number {
  if (m?.status !== 'ok' || !c?.metric) return 0
  let n = 0
  for (const u of Object.values(m.values)) if ((c.type === 'cpu' ? u.cpu : u.memory) !== undefined) n++
  return n
}

/** An order-independent fingerprint of a set of row ids. */
function idSetKey(rows: Row[]): string {
  let x = 0
  let sum = 0
  for (const r of rows) {
    let h = 2166136261
    for (let i = 0; i < r.id.length; i++) h = Math.imul(h ^ r.id.charCodeAt(i), 16777619)
    x ^= h
    sum = (sum + (h >>> 0)) % 4294967296
  }
  return `${rows.length}:${x >>> 0}:${sum}`
}

/** A cell with nothing to show (an unknown metric, an empty text). */
const isEmpty = (c: Cell | undefined) => !c || (c.num === undefined && c.time === undefined && !c.text)

/** Sort key: numbers and times numerically, text case-insensitively (empty: see isEmpty). */
function cmp(c: Column, a: Cell | undefined, b: Cell | undefined): number {
  if (c.type === 'age') return (b?.time ?? -Infinity) - (a?.time ?? -Infinity) // older = bigger age
  const an = a?.num
  const bn = b?.num
  if (an !== undefined || bn !== undefined) return (an ?? -Infinity) - (bn ?? -Infinity)
  return (a?.text ?? '').localeCompare(b?.text ?? '', undefined, { numeric: true, sensitivity: 'base' })
}

export function matchesRow(r: Row, f: string): boolean {
  if (!f) return true
  const needle = f.toLowerCase()
  return r.cells.some((c) => (c.text ?? '').toLowerCase().includes(needle)) || (r.health.reason ?? '').toLowerCase().includes(needle)
}

export function ResourceTable({ emptyMessage, columns, rows, hideScope, filter, selected, reveal, onSelect, onOpen, onLogs, onTerminal, metrics, onVisibleRows, rowMenu, onDelete, defaultSort, initialSort, onSort, initialWidths, onWidths, areaFocus, marked, onMarked }: Props) {
  const now = useNow(10_000)
  const [widths, setWidths] = useState<Record<string, number>>(initialWidths ?? {})
  const resizeColumn = (id: string, width: number | null) => {
    const next = { ...widths }
    if (width === null) delete next[id]
    else next[id] = width
    setWidths(next)
    onWidths?.(next)
  }
  const [menu, setMenu] = useState<{ items: MenuItem[]; label: string; at: { x: number; y: number } } | null>(null)
  const openMenu = (r: Row, at: { x: number; y: number }) => {
    const m = rowMenu?.(r) ?? []
    const { items, label } = Array.isArray(m) ? { items: m, label: t('row.menu') } : m
    if (items.length) setMenu({ items, label, at })
  }
  const [sort, setSort] = useState<Sort & { clicks: number }>(() => {
    const left = initialSort ? columns.findIndex((c) => c.id === initialSort.col) : -1
    if (left >= 0) return { col: left, desc: initialSort!.desc, clicks: 0 }
    const col = defaultSort ? columns.findIndex((c) => c.id === defaultSort.column) : -1
    return col < 0 ? { col: 0, desc: false, clicks: 0 } : { col, desc: !!defaultSort?.desc, clicks: 0 }
  })
  const sortCol = columns[sort.col]?.id
  useEffect(() => {
    if (sort.clicks && sortCol) onSort?.({ col: sortCol, desc: sort.desc })
    // eslint-disable-next-line react-hooks/exhaustive-deps -- told of the user's choices, not of a new onSort
  }, [sortCol, sort.desc, sort.clicks])
  // Ties: the kind's second key (ascending), then the name.
  const thenCol = defaultSort?.then ? columns.findIndex((c) => c.id === defaultSort.then) : -1
  const hasStatus = columns.some((c) => c.type === 'status')
  const visibleCols = useMemo(
    () => columns.map((c, i) => ({ c, i })).filter(({ c }) => !(hideScope && c.scopeColumn)),
    [columns, hideScope],
  )
  // Metric columns take their values from the metrics poll, not the row.
  const cellOf = useMemo(() => {
    const values = metrics?.status === 'ok' ? metrics.values : null
    return (r: Row, i: number): MetricCell | undefined => {
      const c = columns[i]
      if (!c?.metric) return r.cells[i]
      const u = values?.[r.id]
      const num = c.type === 'cpu' ? u?.cpu : u?.memory
      if (num === undefined) return undefined // unknown, never 0
      return { num, partial: c.type === 'cpu' ? u?.cpuPartial : u?.memoryPartial }
    }
  }, [metrics, columns])
  // Sorting by a metric orders by the values known when the sort was
  // chosen (or the set of rows or the filter changed): new samples update
  // the cells, not the order — else rows would move under the reader and
  // the visible (asked-for) rows with them, in a loop.
  const rowSet = useMemo(() => idSetKey(rows), [rows])
  const frozen = useRef<{ key: string; cells: Map<string, MetricCell | undefined> } | null>(null)
  const sortCell = useMemo(() => {
    const col = columns[sort.col]
    if (!col?.metric) return cellOf
    const key = `${sort.col}:${sort.desc}:${sort.clicks}:${rowSet}:${filter}`
    // Until some value is known (the sort chosen before the first sample)
    // the snapshot is retaken: filling it once is not a loop.
    const empty = frozen.current && ![...frozen.current.cells.values()].some((c) => c !== undefined)
    if (frozen.current?.key !== key || empty) frozen.current = { key, cells: new Map(rows.map((r) => [r.id, cellOf(r, sort.col)])) }
    const cells = frozen.current.cells
    return (r: Row, i: number) => (i === sort.col ? cells.get(r.id) : cellOf(r, i))
    // eslint-disable-next-line react-hooks/exhaustive-deps -- cellOf (new metrics) re-sorts only an empty snapshot
  }, [columns, sort, rowSet, filter, rows, metricKnown(metrics, columns[sort.col])])
  const sorted = useMemo(() => {
    const f = filter.trim()
    const list = f ? rows.filter((r) => matchesRow(r, f)) : rows.slice()
    const col = columns[sort.col]
    if (col) {
      const then = thenCol >= 0 && thenCol !== sort.col ? columns[thenCol] : null
      list.sort((a, b) => {
        const ca = sortCell(a, sort.col)
        const cb = sortCell(b, sort.col)
        // Empty last in both directions: an unknown is not a zero.
        const ea = isEmpty(ca)
        if (ea !== isEmpty(cb)) return ea ? 1 : -1
        const d = cmp(col, ca, cb)
        if (d) return sort.desc ? -d : d
        return (then ? cmp(then, sortCell(a, thenCol), sortCell(b, thenCol)) : 0) || a.ref.name.localeCompare(b.ref.name)
      })
    }
    return list
  }, [rows, filter, sort, columns, sortCell, thenCol])

  const scrollRef = useRef<HTMLDivElement>(null)
  const headRef = useRef<HTMLDivElement>(null)
  const virt = useVirtualizer({
    count: sorted.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ROW_H,
    overscan: 12,
    getItemKey: (i) => sorted[i].id,
    // Until the scroll element is measured (and in jsdom, which has no
    // layout) render a screenful rather than nothing.
    initialRect: { width: 1000, height: 800 },
  })

  const revealed = useRef<typeof reveal>(null)
  useEffect(() => {
    if (!reveal || revealed.current === reveal) return
    const index = sorted.findIndex((r) => r.id === reveal.id)
    if (index < 0) return
    virt.scrollToIndex(index, { align: 'auto' })
    revealed.current = reveal
  }, [reveal, sorted, virt])

  const items = virt.getVirtualItems()
  // The rows in view, without the overscan (what metrics are asked for).
  const first = virt.range?.startIndex ?? 0
  const last = virt.range?.endIndex ?? -1
  const shown = useRef('')
  useEffect(() => {
    if (!onVisibleRows) return
    const ids = sorted.slice(first, last + 1).map((r) => r.id)
    const key = ids.join('\n')
    if (key === shown.current) return
    shown.current = key
    onVisibleRows(ids)
  }, [first, last, sorted, onVisibleRows])

  // A flexible name — the row's identity (a container's or pod's name, long
  // and alike in its prefix) — gets a double share, the others one.
  const least = (c: Column) => c.width || (c.id === 'name' ? 180 : 120)
  const marking = !!onMarked
  const template = (marking ? `${MARK_W}px ` : '') + visibleCols
    .map(({ c, i }) => `var(--column-${i}, ${c.width ? `${c.width}px` : c.id === 'name' ? 'minmax(180px, 2fr)' : 'minmax(120px, 1fr)'})`)
    .join(' ')
  // The columns' least width, set on the header and the rows: Chromium does
  // not count the overflowing tracks of the (absolute) rows fully, so the
  // last columns could not be scrolled to.
  const minWidth = `calc(${[`${marking ? MARK_W : 0}px`, ...visibleCols.map(({ c, i }) => `var(--column-${i}, ${least(c)}px)`)].join(' + ')})`
  const widthStyle = Object.fromEntries(columns.flatMap((c, i) => widths[c.id] ? [[`--column-${i}`, `${widths[c.id]}px`]] : [])) as CSSProperties

  // Marks: a click with Ctrl toggles one, with Shift marks the range from
  // the last toggled row (the anchor) in the shown order.
  const anchor = useRef<string | null>(null)
  const marks = marked ?? noMarks
  const setMarks = (next: Set<string>) => onMarked?.(next)
  const toggle = (id: string) => {
    const next = new Set(marks)
    if (!next.delete(id)) next.add(id)
    anchor.current = id
    setMarks(next)
  }
  const markRange = (from: string | null, to: string) => {
    const a = sorted.findIndex((r) => r.id === from)
    const b = sorted.findIndex((r) => r.id === to)
    if (a < 0 || b < 0) return toggle(to)
    const next = new Set(marks)
    for (let k = Math.min(a, b); k <= Math.max(a, b); k++) next.add(sorted[k].id)
    setMarks(next)
  }
  const shownMarked = marking ? sorted.reduce((n, r) => n + (marks.has(r.id) ? 1 : 0), 0) : 0
  const allState = shownMarked === 0 ? 'false' : shownMarked === sorted.length ? 'true' : 'mixed'
  const focusTable = () => scrollRef.current?.focus({ preventScroll: true })
  const markAll = () => setMarks(allState === 'true' ? new Set() : new Set(sorted.map((r) => r.id)))

  const onKey = (e: React.KeyboardEvent) => {
    if (!sorted.length) return
    const i = sorted.findIndex((r) => r.id === selected)
    // A page: the rows that fit, less one kept for context.
    const page = Math.max(1, Math.floor((scrollRef.current?.clientHeight ?? 0) / ROW_H) - 1)
    const last = sorted.length - 1
    const to =
      e.key === 'ArrowDown' ? i + 1 : e.key === 'ArrowUp' ? (i < 0 ? 0 : i - 1) : e.key === 'PageDown' ? (i < 0 ? page : i + page) : e.key === 'PageUp' ? i - page : e.key === 'Home' ? 0 : e.key === 'End' ? last : null
    if (to !== null && !e.altKey && !e.ctrlKey && !e.metaKey) {
      e.preventDefault()
      const next = Math.max(0, Math.min(last, to))
      // Shift+arrows mark the row left and the row reached.
      if (marking && e.shiftKey && i >= 0 && (e.key === 'ArrowDown' || e.key === 'ArrowUp')) {
        setMarks(new Set([...marks, sorted[i].id, sorted[next].id]))
        anchor.current = sorted[next].id
      }
      onSelect(sorted[next])
      virt.scrollToIndex(next, { align: 'auto' })
    } else if (marking && e.key === ' ' && !e.repeat && i >= 0 && !e.ctrlKey && !e.altKey && !e.metaKey) {
      e.preventDefault()
      toggle(sorted[i].id)
    } else if (marking && isShortcut(e, 'KeyA', { ctrl: true, shift: false }) && !e.altKey) {
      e.preventDefault()
      setMarks(new Set(sorted.map((r) => r.id)))
    } else if (marking && e.key === 'Escape' && marks.size > 0) {
      // One Esc, one effect: the marks go, details stay.
      e.preventDefault()
      setMarks(new Set())
    } else if (onLogs && i >= 0 && isShortcut(e, 'KeyL', { ctrl: false, shift: false }) && !e.altKey) {
      e.preventDefault()
      onLogs(sorted[i])
    } else if (onTerminal && i >= 0 && isShortcut(e, 'KeyS', { ctrl: false }) && !e.altKey) {
      e.preventDefault()
      onTerminal(sorted[i], e.shiftKey)
    } else if (e.key === 'Enter' && !e.repeat && i >= 0 && onOpen) {
      e.preventDefault()
      onOpen(sorted[i])
    } else if (e.key === 'Delete' && !e.repeat && (i >= 0 || (marking && marks.size > 0)) && onDelete) {
      e.preventDefault()
      onDelete(sorted[i])
    } else if (rowMenu && i >= 0 && (e.key === 'ContextMenu' || (e.key === 'F10' && e.shiftKey))) {
      e.preventDefault()
      const el = [...(scrollRef.current?.querySelectorAll<HTMLElement>('[data-row-id]') ?? [])].find((x) => x.dataset.rowId === sorted[i].id)
      const box = el?.getBoundingClientRect() ?? scrollRef.current?.getBoundingClientRect()
      openMenu(sorted[i], { x: (box?.left ?? 0) + 24, y: (box?.bottom ?? 0) })
    }
  }

  return (
    <div className="resource-grid flex min-h-0 min-w-0 flex-1 flex-col" style={widthStyle} role="grid" aria-rowcount={sorted.length} aria-label="resources">
      {/* The header scrolls sideways with the rows (a narrow window), never the page. */}
      <div ref={headRef} className="resource-grid-header shrink-0 overflow-hidden border-b border-line [scrollbar-gutter:stable]">
        <div className="grid" style={{ gridTemplateColumns: template, minWidth }} role="row">
          {marking && (
            <span role="columnheader" className="flex items-center justify-center">
              <MarkBox state={allState} label={t('table.markAll')} onToggle={() => (markAll(), focusTable())} />
            </span>
          )}
          {visibleCols.map(({ c, i }) => (
            <div key={c.id} className="relative min-w-0">
            <button
              role="columnheader"
              aria-sort={sort.col === i ? (sort.desc ? 'descending' : 'ascending') : 'none'}
              onClick={() => setSort((s) => ({ col: i, desc: s.col === i ? !s.desc : false, clicks: s.clicks + 1 }))}
              className={['h-full w-full truncate px-3 py-1.5 text-left hover:text-fg', isNumeric(c) ? 'text-right' : ''].join(' ')}
            >
              {columnLabel(c.title)}
              {sort.col === i && <span className="ml-1">{sort.desc ? '↓' : '↑'}</span>}
            </button>
            <ColumnResize column={columnLabel(c.title)} variable={`--column-${i}`} value={widths[c.id] ?? least(c)} onDone={(width) => resizeColumn(c.id, width)} />
            </div>
          ))}
        </div>
      </div>
      <div
        ref={scrollRef}
        tabIndex={0}
        data-area-focus={areaFocus ? '' : undefined}
        onKeyDown={onKey}
        onScroll={(e) => headRef.current && (headRef.current.scrollLeft = e.currentTarget.scrollLeft)}
        className="min-h-0 flex-1 overflow-auto outline-none [scrollbar-gutter:stable]"
        data-table-scroll
      >
        {sorted.length === 0 && emptyMessage && <div data-table-empty className="px-4 py-6 text-center text-fg-subtle" role="status">{emptyMessage}</div>}
        <div style={{ height: virt.getTotalSize(), minWidth, position: 'relative' }}>
          {items.map((vi) => {
            const r = sorted[vi.index]
            const isSel = r.id === selected
            const isMarked = marking && marks.has(r.id)
            return (
              <div
                key={vi.key}
                role="row"
                aria-selected={isSel}
                data-row-id={r.id}
                data-marked={isMarked ? '' : undefined}
                onClick={(e) => {
                  if (marking && (e.ctrlKey || e.metaKey)) return toggle(r.id)
                  if (marking && e.shiftKey) {
                    markRange(anchor.current ?? selected, r.id)
                    anchor.current = r.id
                    return
                  }
                  onSelect(r)
                  onOpen?.(r)
                }}
                onContextMenu={
                  rowMenu &&
                  ((e) => {
                    e.preventDefault()
                    onSelect(r)
                    scrollRef.current?.focus({ preventScroll: true })
                    openMenu(r, { x: e.clientX, y: e.clientY })
                  })
                }
                title={r.health.message ? `${r.health.reason}: ${r.health.message}` : r.health.reason}
                className={['resource-row absolute left-0 grid w-full cursor-default items-center border-b border-line/40', isSel ? 'bg-active' : isMarked ? 'bg-marked' : 'hover:bg-hover'].join(' ')}
                style={{ top: vi.start, height: ROW_H, gridTemplateColumns: template, minWidth }}
              >
                {marking && (
                  <span role="gridcell" className="flex items-center justify-center">
                    <MarkBox state={isMarked ? 'true' : 'false'} label={t('table.mark', { name: r.ref.name })} onToggle={() => (toggle(r.id), focusTable())} />
                  </span>
                )}
                {visibleCols.map(({ c, i }) => {
                  const cell = cellOf(r, i)
                  const text = cellText(c, cell, now)
                  // No status column (a table of the server's columns): the
                  // row's health is a dot by its first cell.
                  const dotHere = c.type === 'status' || (!hasStatus && i === 0)
                  return (
                    <span
                      key={c.id}
                      role="gridcell"
                      title={(dotHere && r.health.message ? `${text}\n${r.health.message}` : text) || undefined}
                      className={[
                        'truncate px-3',
                        isNumeric(c) ? 'text-right font-mono text-[13px]' : '',
                        c.type === 'status' ? (cell?.muted ? 'text-fg-subtle' : healthText[r.health.state]) : '',
                        !hasStatus && i === 0 && r.health.state === 'terminating' ? healthText.terminating : '',
                        i === 0 ? 'font-medium' : '',
                      ].join(' ')}
                    >
                      {dotHere && <HealthDot state={r.health.state} muted={c.type === 'status' && cell?.muted} />}
                      {text}
                    </span>
                  )
                })}
              </div>
            )
          })}
        </div>
      </div>
      {menu && <Menu items={menu.items} at={menu.at} label={menu.label} onClose={() => setMenu(null)} />}
    </div>
  )
}

const noMarks: ReadonlySet<string> = new Set()

/** A mark's checkbox: not a tab stop (the table's keys mark), a click is its own (the row does not open). */
function MarkBox({ state, label, onToggle }: { state: 'true' | 'false' | 'mixed'; label: string; onToggle: () => void }) {
  return (
    <span
      role="checkbox"
      aria-checked={state}
      aria-label={label}
      onClick={(e) => {
        e.stopPropagation()
        onToggle()
      }}
      onMouseDown={(e) => e.preventDefault()} // the table keeps the focus
      className={[
        'flex h-3.5 w-3.5 cursor-pointer items-center justify-center rounded-sm border',
        state === 'false' ? 'border-fg-subtle hover:border-fg-muted' : 'border-accent bg-accent text-accent-fg',
      ].join(' ')}
    >
      {state !== 'false' && (
        <svg aria-hidden viewBox="0 0 10 10" className="h-2.5 w-2.5" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
          <path d={state === 'true' ? 'M2 5.2 4.2 7.4 8 2.8' : 'M2.5 5h5'} />
        </svg>
      )}
    </span>
  )
}

function isNumeric(c: Column) {
  return c.type === 'number' || c.type === 'cpu' || c.type === 'bytes' || c.type === 'ratio' || c.type === 'age'
}

const dotColor: Record<HealthState, string> = {
  ok: 'bg-success',
  progressing: 'bg-accent',
  warning: 'bg-warning',
  error: 'bg-danger',
  terminating: 'bg-fg-subtle',
  unknown: 'bg-fg-muted',
}

/** muted: a ring, not a dot — evidence rather than a current state. */
export function HealthDot({ state, muted }: { state: HealthState; muted?: boolean }) {
  return (
    <span
      aria-hidden
      data-health={state}
      className={['mr-1.5 inline-block h-2 w-2 rounded-full align-middle', muted ? 'border border-fg-subtle' : dotColor[state]].join(' ')}
    />
  )
}
