import { useEffect, useRef } from 'react'
import { setLayoutResizing } from './layoutResize'
import { create } from 'zustand'

/** Widths live above targets and drawers, so navigation does not reset the layout. */
export const usePanelWidths = create<{ targets: number; navigation: number; details: number | null; helmDetails: number | null; agentsTargets: number }>(() => ({ targets: 240, navigation: 192, details: null, helmDetails: null, agentsTargets: 210 }))

interface Props {
  label: string
  axis?: 'width' | 'height'
  /** Left/top edge: dragging towards the origin grows the panel. */
  reverse?: boolean
  /** Place the hit area beyond the edge, leaving the panel scrollbar usable. */
  outside?: boolean
  value: number
  min: number
  max: () => number
  onDone: (size: number) => void
}

/** Resize the parent panel once per frame; commit React state only on release. */
export function PanelResize({ label, axis = 'width', reverse = false, outside = false, value, min, max, onDone }: Props) {
  const drag = useRef<{ panel: HTMLElement; start: number; size: number; next: number; frame: number | null; cursor: string; select: string; handle: HTMLElement; focus: HTMLElement | null } | null>(null)
  const clamp = (size: number) => Math.max(Math.min(min, max()), Math.min(max(), size))
  const paint = () => {
    const d = drag.current
    if (!d) return
    d.panel.style[axis] = `${d.next}px`
    d.frame = null
  }
  const release = (commit: boolean) => {
    const d = drag.current
    if (!d) return
    if (d.frame !== null) cancelAnimationFrame(d.frame)
    d.panel.style[axis] = `${commit ? d.next : d.size}px`
    document.body.style.cursor = d.cursor
    document.body.style.userSelect = d.select
    drag.current = null
    setLayoutResizing(false)
    if (commit) onDone(d.next)
    if (document.activeElement === d.handle) {
      if (d.focus?.isConnected) d.focus.focus({preventScroll:true})
      else d.handle.blur()
    }
  }
  useEffect(() => () => {
    const d = drag.current
    if (!d) return
    if (d.frame !== null) cancelAnimationFrame(d.frame)
    document.body.style.cursor = d.cursor
    document.body.style.userSelect = d.select
    setLayoutResizing(false)
  }, [])
  const update = (x: number, y: number) => {
    const d = drag.current
    if (!d) return
    d.next = clamp(d.size + ((axis === 'width' ? x : y) - d.start) * (reverse ? -1 : 1))
    if (d.frame === null) d.frame = requestAnimationFrame(paint)
  }
  return <div
    role="separator" tabIndex={0} aria-label={label}
    aria-orientation={axis === 'width' ? 'vertical' : 'horizontal'}
    aria-valuemin={min} aria-valuenow={Math.round(value)}
    className={axis === 'width'
      ? `absolute inset-y-0 z-20 w-1 cursor-col-resize touch-none hover:bg-accent/60 focus-visible:bg-accent/60 outline-none ${outside ? (reverse ? 'right-full' : 'left-full') : (reverse ? 'left-0' : 'right-0')}`
      : 'h-1 shrink-0 cursor-row-resize touch-none bg-line/60 hover:bg-accent/60 focus-visible:bg-accent/60 outline-none'}
    onPointerDown={(e) => {
      if (e.button !== 0) return
      e.preventDefault()
      const focus = document.activeElement instanceof HTMLElement ? document.activeElement : null
      e.currentTarget.focus({ preventScroll: true })
      const panel = e.currentTarget.parentElement!
      const size = panel.getBoundingClientRect()[axis]
      drag.current = { panel, start: axis === 'width' ? e.clientX : e.clientY, size, next: size, frame: null, cursor: document.body.style.cursor, select: document.body.style.userSelect, handle:e.currentTarget, focus }
      setLayoutResizing(true)
      document.body.style.cursor = axis === 'width' ? 'col-resize' : 'row-resize'
      document.body.style.userSelect = 'none'
      e.currentTarget.setPointerCapture(e.pointerId)
    }}
    onPointerMove={(e) => update(e.clientX, e.clientY)}
    onPointerUp={(e) => { if (!drag.current) return; update(e.clientX, e.clientY); release(true); e.currentTarget.releasePointerCapture(e.pointerId) }}
    onPointerCancel={() => release(false)}
    onLostPointerCapture={() => release(false)}
    onKeyDown={(e) => {
      if (e.key === 'Escape' && drag.current) { e.preventDefault(); e.stopPropagation(); release(false); return }
      const decrease = axis === 'width' ? 'ArrowLeft' : 'ArrowUp'
      const increase = axis === 'width' ? 'ArrowRight' : 'ArrowDown'
      if (![decrease, increase, 'Home', 'End'].includes(e.key)) return
      e.preventDefault()
      e.stopPropagation()
      const panel = e.currentTarget.parentElement!
      const size = panel.getBoundingClientRect()[axis]
      const next = clamp(e.key === 'Home' ? min : e.key === 'End' ? max() : size + (e.key === increase ? 20 : -20) * (reverse ? -1 : 1))
      panel.style[axis] = `${next}px`
      onDone(next)
    }}
  />
}
