import { useVirtualizer } from '@tanstack/react-virtual'
import { useMemo, useRef, useState } from 'react'
import type { Cell, Column, HealthState, MetricsView, Row } from '../api/types'
import { formatAge, formatBytes, formatCPU } from '../format'
import { isShortcut } from '../keyboard'
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
  /** Usage for metric columns (CPU/Memory), by row id. */
  metrics?: MetricsView | null
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

export function ResourceTable({ columns, rows, hideScope, filter, selected, onSelect, onOpen, onLogs, metrics }: Props) {
  const now = useNow(10_000)
  const [sort, setSort] = useState<Sort>({ col: 0, desc: false })
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
      list.sort((a, b) => {
        const d = cmp(col, cellOf(a, sort.col), cellOf(b, sort.col)) || a.ref.name.localeCompare(b.ref.name)
        return sort.desc ? -d : d
      })
    }
    return list
  }, [rows, filter, sort, columns, cellOf])

  const scrollRef = useRef<HTMLDivElement>(null)
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

  const onKey = (e: React.KeyboardEvent) => {
    if (!sorted.length) return
    const i = sorted.findIndex((r) => r.id === selected)
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault()
      const next = e.key === 'ArrowDown' ? Math.min(sorted.length - 1, i + 1) : Math.max(0, i < 0 ? 0 : i - 1)
      onSelect(sorted[next])
      virt.scrollToIndex(next, { align: 'auto' })
    } else if (onLogs && i >= 0 && isShortcut(e, 'KeyL', { ctrl: false, shift: false }) && !e.altKey) {
      e.preventDefault()
      onLogs(sorted[i])
    } else if (e.key === 'Enter' && i >= 0 && onOpen) {
      e.preventDefault()
      onOpen(sorted[i])
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col" role="grid" aria-rowcount={sorted.length} aria-label="resources">
      <div className="grid shrink-0 border-b border-line bg-sidebar text-[11px] font-semibold uppercase tracking-wide text-fg-subtle" style={{ gridTemplateColumns: template }} role="row">
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
      <div ref={scrollRef} tabIndex={0} onKeyDown={onKey} className="min-h-0 flex-1 overflow-auto outline-none" data-table-scroll>
        <div style={{ height: virt.getTotalSize(), position: 'relative' }}>
          {virt.getVirtualItems().map((vi) => {
            const r = sorted[vi.index]
            const isSel = r.id === selected
            return (
              <div
                key={vi.key}
                role="row"
                aria-selected={isSel}
                onClick={() => {
                  onSelect(r)
                  onOpen?.(r)
                }}
                title={r.health.message ? `${r.health.reason}: ${r.health.message}` : r.health.reason}
                className={['absolute left-0 grid w-full cursor-default items-center border-b border-line/40', isSel ? 'bg-active' : 'hover:bg-hover'].join(' ')}
                style={{ top: vi.start, height: ROW_H, gridTemplateColumns: template }}
              >
                {visibleCols.map(({ c, i }) => (
                  <span
                    key={c.id}
                    role="gridcell"
                    className={[
                      'truncate px-3',
                      isNumeric(c) ? 'text-right font-mono text-[12px]' : '',
                      c.type === 'status' ? healthText[r.health.state] : '',
                      i === 0 ? 'font-medium' : '',
                    ].join(' ')}
                  >
                    {c.type === 'status' && <HealthDot state={r.health.state} />}
                    {cellText(c, cellOf(r, i), now)}
                  </span>
                ))}
              </div>
            )
          })}
        </div>
      </div>
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

export function HealthDot({ state }: { state: HealthState }) {
  return <span aria-hidden className={['mr-1.5 inline-block h-2 w-2 rounded-full align-middle', dotColor[state]].join(' ')} />
}
