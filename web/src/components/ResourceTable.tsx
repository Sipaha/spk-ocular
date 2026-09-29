import { useVirtualizer } from '@tanstack/react-virtual'
import { useMemo, useRef, useState } from 'react'
import type { Cell, Column, HealthState, MetricsView, Row, SortSpec } from '../api/types'
import { formatAge, formatBytes, formatCPU } from '../format'
import { isShortcut } from '../keyboard'
import { Menu, type MenuItem } from '../actions/Menu'
import { t } from '../i18n'
import { useNow } from '../views/useView'

const ROW_H = 28

export interface Sort {
  col: number
  desc: boolean
}

interface Props {
  columns: Column[]
  rows: Row[]
  /** Hide the scope (namespace) column: one scope is shown. */
  hideScope: boolean
  filter: string
  selected: string | null
  onSelect: (row: Row) => void
  onOpen?: (row: Row) => void
  /** L on a row: its logs (kinds that have them). */
  onLogs?: (row: Row) => void
  /** S: a terminal in the default container; Shift+S: the dialog */
  onTerminal?: (row: Row, dialog: boolean) => void
  /** Usage for metric columns (CPU/Memory), by row id. */
  metrics?: MetricsView | null
  /**
   * The context menu of a row (right click, the Menu key, Shift+F10): built
   * for that row when it opens.
   */
  rowMenu?: (row: Row) => MenuItem[]
  /** Delete on the focused table: the selected row. */
  onDelete?: (row: Row) => void
  /** The kind's first sort (else by the first column). */
  defaultSort?: SortSpec
  /** The table of its area (F6 focuses it); not a table inside details. */
  areaFocus?: boolean
}

export const healthText: Record<HealthState, string> = {
  ok: 'text-fg',
  progressing: 'text-accent',
  warning: 'text-warning',
  error: 'text-danger',
  terminating: 'text-fg-subtle',
  unknown: 'text-fg-muted',
}

export function cellText(c: Column, cell: Cell | undefined, now: number): string {
  if (!cell) return ''
  switch (c.type) {
    case 'age':
      return cell.time ? formatAge(now - cell.time) : ''
    case 'cpu':
      return cell.num !== undefined ? formatCPU(cell.num) : ''
    case 'bytes':
      return cell.num !== undefined ? formatBytes(cell.num) : ''
  }
  return cell.text ?? ''
}

/** Sort key: numbers and times numerically, text case-insensitively; empty last. */
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

export function ResourceTable({ columns, rows, hideScope, filter, selected, onSelect, onOpen, onLogs, onTerminal, metrics, rowMenu, onDelete, defaultSort, areaFocus }: Props) {
  const now = useNow(10_000)
  const [menu, setMenu] = useState<{ items: MenuItem[]; at: { x: number; y: number } } | null>(null)
  const openMenu = (r: Row, at: { x: number; y: number }) => {
    const items = rowMenu?.(r) ?? []
    if (items.length) setMenu({ items, at })
  }
  const [sort, setSort] = useState<Sort>(() => {
    const col = defaultSort ? columns.findIndex((c) => c.id === defaultSort.column) : -1
    return col < 0 ? { col: 0, desc: false } : { col, desc: !!defaultSort?.desc }
  })
  // Ties: the kind's second key (ascending), then the name.
  const thenCol = defaultSort?.then ? columns.findIndex((c) => c.id === defaultSort.then) : -1
  const visibleCols = useMemo(
    () => columns.map((c, i) => ({ c, i })).filter(({ c }) => !(hideScope && c.scopeColumn)),
    [columns, hideScope],
  )
  // Metric columns take their values from the metrics poll, not the row.
  const cellOf = useMemo(() => {
    const values = metrics?.status === 'ok' ? metrics.values : null
    return (r: Row, i: number): Cell | undefined => {
      const c = columns[i]
      if (!c?.metric) return r.cells[i]
      const u = values?.[r.id]
      if (!u) return undefined
      return { num: c.type === 'cpu' ? u.cpu : u.memory }
    }
  }, [metrics, columns])
  const sorted = useMemo(() => {
    const f = filter.trim()
    const list = f ? rows.filter((r) => matchesRow(r, f)) : rows.slice()
    const col = columns[sort.col]
    if (col) {
      const then = thenCol >= 0 && thenCol !== sort.col ? columns[thenCol] : null
      list.sort((a, b) => {
        const d = cmp(col, cellOf(a, sort.col), cellOf(b, sort.col))
        if (d) return sort.desc ? -d : d
        return (then ? cmp(then, cellOf(a, thenCol), cellOf(b, thenCol)) : 0) || a.ref.name.localeCompare(b.ref.name)
      })
    }
    return list
  }, [rows, filter, sort, columns, cellOf, thenCol])

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

  const template = visibleCols.map(({ c }) => (c.width ? `${c.width}px` : 'minmax(120px, 1fr)')).join(' ')
  // The columns' least width, set on the header and the rows: Chromium does
  // not count the overflowing tracks of the (absolute) rows fully, so the
  // last columns could not be scrolled to.
  const minWidth = visibleCols.reduce((sum, { c }) => sum + (c.width || 120), 0)

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
      onSelect(sorted[next])
      virt.scrollToIndex(next, { align: 'auto' })
    } else if (onLogs && i >= 0 && isShortcut(e, 'KeyL', { ctrl: false, shift: false }) && !e.altKey) {
      e.preventDefault()
      onLogs(sorted[i])
    } else if (onTerminal && i >= 0 && isShortcut(e, 'KeyS', { ctrl: false }) && !e.altKey) {
      e.preventDefault()
      onTerminal(sorted[i], e.shiftKey)
    } else if (e.key === 'Enter' && !e.repeat && i >= 0 && onOpen) {
      e.preventDefault()
      onOpen(sorted[i])
    } else if (e.key === 'Delete' && !e.repeat && i >= 0 && onDelete) {
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
    <div className="flex min-h-0 min-w-0 flex-1 flex-col" role="grid" aria-rowcount={sorted.length} aria-label="resources">
      {/* The header scrolls sideways with the rows (a narrow window), never the page. */}
      <div ref={headRef} className="shrink-0 overflow-hidden border-b border-line bg-sidebar [scrollbar-gutter:stable]">
        <div className="grid text-[11px] font-semibold uppercase tracking-wide text-fg-subtle" style={{ gridTemplateColumns: template, minWidth }} role="row">
          {visibleCols.map(({ c, i }) => (
            <button
              key={c.id}
              role="columnheader"
              aria-sort={sort.col === i ? (sort.desc ? 'descending' : 'ascending') : 'none'}
              onClick={() => setSort((s) => ({ col: i, desc: s.col === i ? !s.desc : false }))}
              className={['truncate px-3 py-1.5 text-left hover:text-fg', isNumeric(c) ? 'text-right' : ''].join(' ')}
            >
              {c.title}
              {sort.col === i && <span className="ml-1">{sort.desc ? '↓' : '↑'}</span>}
            </button>
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
        <div style={{ height: virt.getTotalSize(), minWidth, position: 'relative' }}>
          {virt.getVirtualItems().map((vi) => {
            const r = sorted[vi.index]
            const isSel = r.id === selected
            return (
              <div
                key={vi.key}
                role="row"
                aria-selected={isSel}
                data-row-id={r.id}
                onClick={() => {
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
                className={['absolute left-0 grid w-full cursor-default items-center border-b border-line/40', isSel ? 'bg-active' : 'hover:bg-hover'].join(' ')}
                style={{ top: vi.start, height: ROW_H, gridTemplateColumns: template, minWidth }}
              >
                {visibleCols.map(({ c, i }) => {
                  const cell = cellOf(r, i)
                  return (
                    <span
                      key={c.id}
                      role="gridcell"
                      className={[
                        'truncate px-3',
                        isNumeric(c) ? 'text-right font-mono text-[12px]' : '',
                        c.type === 'status' ? (cell?.muted ? 'text-fg-subtle' : healthText[r.health.state]) : '',
                        i === 0 ? 'font-medium' : '',
                      ].join(' ')}
                    >
                      {c.type === 'status' && <HealthDot state={r.health.state} muted={cell?.muted} />}
                      {cellText(c, cell, now)}
                    </span>
                  )
                })}
              </div>
            )
          })}
        </div>
      </div>
      {menu && <Menu items={menu.items} at={menu.at} label={t('row.menu')} onClose={() => setMenu(null)} />}
    </div>
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
      className={['mr-1.5 inline-block h-2 w-2 rounded-full align-middle', muted ? 'border border-fg-subtle' : dotColor[state]].join(' ')}
    />
  )
}
