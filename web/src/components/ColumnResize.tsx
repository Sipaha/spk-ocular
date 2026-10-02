import { useEffect, useRef } from 'react'
import { t } from '../i18n'

export const MIN_COLUMN_WIDTH = 48
export const MAX_COLUMN_WIDTH = 1600

/** One inherited CSS variable moves the header and every virtual row together.
 * Dragging paints once per frame; React and persistence run only on release. */
export function ColumnResize({ column, variable, value, onDone }: { column: string; variable: string; value: number; onDone: (width: number | null) => void }) {
  const drag = useRef<{ grid: HTMLElement; start: number; width: number; next: number; previous: string; frame: number | null; cursor: string; select: string } | null>(null)
  const clamp = (width: number) => Math.round(Math.max(MIN_COLUMN_WIDTH, Math.min(MAX_COLUMN_WIDTH, width)))
  const paint = () => {
    const d = drag.current
    if (!d) return
    d.grid.style.setProperty(variable, `${d.next}px`)
    d.frame = null
  }
  const release = (commit: boolean) => {
    const d = drag.current
    if (!d) return
    if (d.frame !== null) cancelAnimationFrame(d.frame)
    if (commit) d.grid.style.setProperty(variable, `${d.next}px`)
    else if (d.previous) d.grid.style.setProperty(variable, d.previous)
    else d.grid.style.removeProperty(variable)
    document.body.style.cursor = d.cursor
    document.body.style.userSelect = d.select
    drag.current = null
    if (commit) onDone(d.next)
  }
  useEffect(() => () => {
    const d = drag.current
    if (!d) return
    if (d.frame !== null) cancelAnimationFrame(d.frame)
    if (d.previous) d.grid.style.setProperty(variable, d.previous)
    else d.grid.style.removeProperty(variable)
    document.body.style.cursor = d.cursor
    document.body.style.userSelect = d.select
  }, [variable])
  const update = (x: number) => {
    const d = drag.current
    if (!d) return
    d.next = clamp(d.width + x - d.start)
    if (d.frame === null) d.frame = requestAnimationFrame(paint)
  }
  return <div role="separator" tabIndex={0} aria-orientation="vertical"
    aria-label={t('table.resizeColumn', { column })} title={t('table.resizeColumnHint', { column })}
    aria-valuemin={MIN_COLUMN_WIDTH} aria-valuemax={MAX_COLUMN_WIDTH} aria-valuenow={value}
    className="column-resize absolute inset-y-0 right-0 z-10 w-[6px] cursor-col-resize touch-none border-r border-line/60 hover:bg-accent/60 focus:bg-accent/60 outline-none"
    onClick={(e) => e.stopPropagation()}
    onDoubleClick={(e) => { e.preventDefault(); e.stopPropagation(); onDone(null) }}
    onPointerDown={(e) => {
      if (e.button !== 0) return
      e.preventDefault(); e.stopPropagation()
      const grid = e.currentTarget.closest<HTMLElement>('[role=grid]')!
      const width = e.currentTarget.parentElement!.getBoundingClientRect().width
      drag.current = { grid, start: e.clientX, width, next: width, previous: grid.style.getPropertyValue(variable), frame: null, cursor: document.body.style.cursor, select: document.body.style.userSelect }
      document.body.style.cursor = 'col-resize'; document.body.style.userSelect = 'none'
      e.currentTarget.setPointerCapture(e.pointerId)
    }}
    onPointerMove={(e) => update(e.clientX)}
    onPointerUp={(e) => { if (!drag.current) return; update(e.clientX); release(true); e.currentTarget.releasePointerCapture(e.pointerId) }}
    onPointerCancel={() => release(false)} onLostPointerCapture={() => release(false)}
    onKeyDown={(e) => {
      if (e.key === 'Escape' && drag.current) { e.preventDefault(); e.stopPropagation(); release(false); return }
      if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(e.key)) return
      e.preventDefault(); e.stopPropagation()
      const width = e.currentTarget.parentElement!.getBoundingClientRect().width
      onDone(clamp(e.key === 'Home' ? MIN_COLUMN_WIDTH : e.key === 'End' ? MAX_COLUMN_WIDTH : width + (e.key === 'ArrowRight' ? 1 : -1) * (e.shiftKey ? 50 : 10)))
    }} />
}
