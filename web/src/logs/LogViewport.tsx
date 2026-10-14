import {ActionIconButton} from '../components/ActionIconButton'
import {ActionIcon} from '../components/icons'
// Virtualized log rows share stable selection and follow state. Rows show
// time/source prefixes and ANSI spans; copying uses the same visible rowText.
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import type { RefObject } from 'react'
import { observeElementRect, useVirtualizer } from '@tanstack/react-virtual'
import { t } from '../i18n'
import type { LogEntry } from './buffer'
import { LEVEL_CLASS } from './levels'
import type { DragSelection } from './logSelection'
import { buildDragSelectionText, pickRowIndexAtY, resolveRowEndpoint } from './logSelection'
import type { Range } from './match'
import { fmtTime, renderMessage, rowText, type RowOpts } from './row'

interface HistoryAnchor { id: number; index: number; inset: number; top: number }

export interface LogViewportProps {
  /** Filtered entries; ids are the stable virtualizer row keys. */
  entries: LogEntry[]
  /** Search matches of an entry (plain-text ranges). */
  rangesFor: (e: LogEntry) => Range[]
  matchIndices: number[]
  safeMatchIndex: number
  rowOpts: RowOpts
  sourceColor: (src: number) => string
  /** Bumped by the filter hook on explicit search navigation → scroll to match. */
  searchNavTick: number
  wordWrap: boolean
  follow: boolean
  setFollow: (follow: boolean) => void
  /**
   * Scroll container ref, owned by the parent so its keyboard shortcuts
   * (Ctrl+A select-all scoping) can reach the same element.
   */
  parentRef: RefObject<HTMLDivElement | null>
  /** Shared "select all is active" flag (set by the parent's Ctrl+A handler). */
  selectAllRef: RefObject<boolean>
  /**
   * Fired when the user starts/stops a mouse drag on the log content. The
   * parent pauses applying new stream chunks while true — any DOM update
   * during the drag would collapse the native text selection.
   */
  onSelectingChange: (selecting: boolean) => void
}

/**
 * LogViewport is the presentational virtual-list half of the log viewer:
 * virtualized rendering, stick-to-bottom follow behaviour, search-match
 * scrolling, select-all visuals and the floating "Follow" button. It owns no
 * data state — entries/search all arrive as props from useLogStream +
 * useLogFilter via LogViewer.
 *
 * Follow model: BREAKING follow happens on user intent (wheel-up — see
 * onWheel), which a programmatic scroll can never emit, so it cannot be eaten
 * by the programmatic-scroll gate no matter how fast chunks arrive. The
 * scroll-position heuristic in onScroll remains for scrollbar/keyboard
 * scrolling and for RE-ARMING follow at the bottom.
 */
export function LogViewport({
  entries,
  rangesFor,
  matchIndices,
  safeMatchIndex,
  rowOpts,
  sourceColor,
  searchNavTick,
  wordWrap,
  follow,
  setFollow,
  parentRef,
  selectAllRef,
  onSelectingChange,
}: LogViewportProps) {
  // When we programmatically scroll (auto-stick-to-bottom, search recentre,
  // initial backlog landing), the browser still fires onScroll. Without this
  // gate the handler reads "scrollTop=0 + tall content" during the very
  // first paint and immediately disables follow — leaving the new window
  // pinned at the top instead of at the live tail. Pulse-high during the
  // animation frames following a programmatic scroll so the onScroll handler
  // can tell user input from layout-driven scrolling.
  const programmaticScrollRef = useRef(false)
  const followRef = useRef(follow)
  followRef.current = follow
  const committedWrapRef = useRef(wordWrap)
  const historyAnchorRef = useRef<HistoryAnchor | null>(null)
  const wrappingHistoryRef = useRef<HistoryAnchor | null>(null)
  const wrapFrameRef = useRef<number | null>(null)
  const wrapOffsetRef = useRef<number | null>(null)
  const resizeResidualRef = useRef(0)

  // Fixed-row fast path: with wrap OFF every row is exactly one text line
  // high, so per-row dynamic measurement is skipped entirely — no
  // ResizeObservers and no scroll-offset corrections while scrolling up
  // through unmeasured rows (the measure/adjust storm read as "stuttering
  // scroll" a third of the way up a big log). The real row height is probed
  // once from the first rendered row (font/zoom dependent); wrap ON switches
  // back to per-row measureElement for variable heights.
  const [probedRowHeight, setProbedRowHeight] = useState<number | null>(null)
  useEffect(() => {
    if (probedRowHeight !== null) return
    const el = parentRef.current?.querySelector('[data-index]')
    const h = el?.getBoundingClientRect().height ?? 0
    if (h > 0) setProbedRowHeight(h)
    // Re-probe as the list fills: the first renders may have no rows (empty
    // buffer / hidden tab with zero size) — retry until a row measures > 0.
  }, [probedRowHeight, parentRef, entries.length])

  // A new key function invalidates TanStack's measurements for every row.
  // Keep it stable during resize/scroll; new log data still updates the keys.
  const getItemKey = useCallback((i: number) => entries[i]?.id ?? i, [entries])

  // Width does not affect fixed-height rows. Ignore width-only observations so
  // dragging adjacent panels does not rerender the log list for every pixel.
  const observeRect = useCallback<typeof observeElementRect<HTMLDivElement>>((instance, callback) => {
    let lastHeight = -1
    return observeElementRect(instance, rect => {
      if (wordWrap || rect.height !== lastHeight) {
        lastHeight = rect.height
        callback(wordWrap ? rect : { width: 0, height: rect.height })
      }
    })
  }, [wordWrap])
  // eslint-disable-next-line react-hooks/incompatible-library -- useVirtualizer returns are consumed locally, no stale UI risk
  const virtualizer = useVirtualizer({
    count: entries.length,
    observeElementRect: observeRect,
    // Deliver geometry before paint; deferring a resize by one frame lets
    // rows follow the moving top edge before the scroll offset catches up.
    useAnimationFrameWithResizeObserver: false,
    getScrollElement: () => parentRef.current,
    estimateSize: () => probedRowHeight ?? 20,
    overscan: 30,
    // GTK overlay horizontal scrollbars must not cover the newest log row.
    paddingEnd: 24,
    // Key rows by the entry's monotonic id, NOT the buffer index. When the
    // sliding window trims old lines off the front, indices shift but ids
    // don't — React nodes and the measurement cache follow the LINE, so the
    // content under the viewport doesn't jump and text selection survives.
    getItemKey,
    onChange: instance => {
      // Measurements can notify React asynchronously. Commit the matching
      // size and positions now, before paint, so overflow from a newly wrapped
      // row cannot temporarily replace the content's real bottom. Fixed rows
      // retain their unobserved fast path; their positions do not change.
      const content = parentRef.current?.querySelector<HTMLElement>('[data-log-content]')
      if (!content) return
      content.style.height = `${instance.getTotalSize()}px`
      for (const item of instance.getVirtualItems()) {
        const node = instance.elementsCache.get(item.key)
        if (node) node.style.top = `${item.start}px`
      }
    },
  })
  // When a row above the viewport is (re)measured to a different size while
  // the user is reading (not following), compensate the scroll offset so the
  // visible content doesn't shift — kills the upward-scroll jitter of lazy
  // dynamic measurement. (Instance property, not a constructor option, in
  // this virtual-core version.)
  virtualizer.shouldAdjustScrollPositionOnItemSizeChange = (item, _delta, instance) => {
    if (followRef.current) return false
    const anchor = wrappingHistoryRef.current ??
      (wordWrap !== committedWrapRef.current ? historyAnchorRef.current : null)
    // During a format change, use the logical fold rather than an offset that
    // advances after every correction. Otherwise rows below the fold can be
    // mistaken for rows above it and push history all the way to the tail.
    if (anchor) return item.index < anchor.index
    instance.getVirtualItems()
    const current = instance.measurementsCache[item.index]
    return current.end <= (instance.scrollOffset ?? 0)
  }

  // While Ctrl+A "select all" is active, re-apply the full-container selection
  // whenever the virtualized range changes (scroll / new lines) so every
  // freshly-mounted row shows as selected too — the range set once on Ctrl+A
  // only covers the rows that existed at that instant. (Copy already grabs
  // every filtered line via the copy handler; this is the matching visual.)
  const renderedItems = virtualizer.getVirtualItems()
  const renderedRangeKey = renderedItems.length
    ? `${renderedItems[0].index}:${renderedItems[renderedItems.length - 1].index}`
    : ''
  useEffect(() => {
    if (!selectAllRef.current || !parentRef.current) return
    const sel = window.getSelection()
    if (!sel) return
    sel.removeAllRanges()
    const range = document.createRange()
    range.selectNodeContents(parentRef.current)
    sel.addRange(range)
  }, [renderedRangeKey, selectAllRef, parentRef])

  // Mirrors `entries` for synchronous reads from the copy / selectionchange
  // event handlers below (registered once, must see the latest list).
  const entriesRef = useRef(entries)
  entriesRef.current = entries
  const rowOptsRef = useRef(rowOpts)
  rowOptsRef.current = rowOpts
  const textOf = (e: LogEntry) => rowText(e, rowOptsRef.current)

  // The native selection only spans MOUNTED rows: rows scrolled past the
  // overscan window unmount and silently drop out of it, so a copy served
  // from the DOM would contain just the visible slice of a long drag
  // selection. Track the drag's logical bounds — entry id + char offset —
  // which survive unmounting; the copy handler rebuilds the full text from
  // the entry buffer. The anchor is pinned at drag start (the browser
  // re-anchors when the original anchor row unmounts), the focus follows
  // every selectionchange (it sits under the pointer, always mounted).
  const dragSelRef = useRef<DragSelection | null>(null)
  const draggingRef = useRef(false)
  const endDragRef = useRef<(() => void) | null>(null)
  const pointerPositionRef = useRef<{x:number;y:number}|null>(null)

  // Select-all must not outlive the selection itself: once the browser
  // selection collapses or leaves the viewport (click on the toolbar, Esc,
  // focus elsewhere), drop the flag. Without this the re-apply effect above
  // kept re-selecting the whole container on EVERY scrolled range change
  // forever — the "scrolling suddenly becomes slow" bug.
  useEffect(() => {
    const onSelectionChange = () => {
      const sel = window.getSelection()
      const lost = !sel || sel.rangeCount === 0 || sel.isCollapsed || !parentRef.current?.contains(sel.anchorNode)
      if (selectAllRef.current && lost) {
        selectAllRef.current = false
      }
      // Drag-bounds tracking. NOT invalidated here on collapse: engines
      // collapse the DOM selection when selected rows unmount (WebKit does it
      // aggressively), and the whole point of the tracked bounds is to
      // survive that. Invalidation is driven by user actions instead —
      // mousedown outside the viewer, Escape, select-all, a fresh drag.
      if (!draggingRef.current) {
        if (selectAllRef.current) dragSelRef.current = null
        return
      }
      const c = parentRef.current
      if (lost || !c || !sel) return
      const anchor = sel.anchorNode ? resolveRowEndpoint(c, sel.anchorNode, sel.anchorOffset) : null
      const idAt = (p: { index: number; offset: number }) => entriesRef.current[p.index]?.id
      if (dragSelRef.current === null && anchor !== null && idAt(anchor) !== undefined) {
        const id = idAt(anchor)!
        dragSelRef.current = { anchorId: id, anchorOffset: anchor.offset, focusId: id, focusOffset: anchor.offset }
      }
      // Clamp BEFORE tracking the focus: when the engine's hit-test lands the
      // focus rows away from the pointer (WebKitGTK, incl. its drag-autoscroll
      // timer that runs with NO mouse events once the pointer crosses the
      // container edge), re-extend and let the induced selectionchange track
      // the corrected focus — the bogus one must never reach the copy bounds.
      if (clampDragFocusRef.current()) return
      const focus = sel.focusNode ? resolveRowEndpoint(c, sel.focusNode, sel.focusOffset) : null
      if (dragSelRef.current !== null && focus !== null && idAt(focus) !== undefined) {
        dragSelRef.current = { ...dragSelRef.current, focusId: idAt(focus)!, focusOffset: focus.offset }
      }
    }
    document.addEventListener('selectionchange', onSelectionChange)
    return () => document.removeEventListener('selectionchange', onSelectionChange)
  }, [selectAllRef, parentRef])

  // Copy override — the list is virtualized, so a DOM-served copy only ever
  // contains the mounted rows. Two cases hand over buffer-built text instead:
  // Ctrl+A select-all (every filtered line) and a tracked drag selection
  // (exact logical range incl. rows that unmounted mid-drag).
  useEffect(() => {
    const onCopy = (e: ClipboardEvent) => {
      // Never hijack a copy from an editable element (search/filter inputs,
      // dialog fields): the document selection may still hold a stale log
      // range while the user is copying the input's own text.
      const focused = document.activeElement as HTMLElement | null
      if (focused && (focused.tagName === 'INPUT' || focused.tagName === 'TEXTAREA' || focused.isContentEditable)) return
      const sel = window.getSelection()
      if (selectAllRef.current) {
        if (!sel || sel.rangeCount === 0 || !parentRef.current?.contains(sel.anchorNode)) return
        e.preventDefault()
        e.clipboardData?.setData('text/plain', entriesRef.current.map(textOf).join('\n'))
        return
      }
      const ds = dragSelRef.current
      if (!ds) return
      // A live selection belonging to ANOTHER part of the page wins over the
      // tracked log range. A collapsed/empty selection does NOT invalidate
      // it — engines collapse the DOM selection when selected rows unmount —
      // and neither does an anchor DETACHED by a row update (isConnected
      // false ≠ "anchored elsewhere"); both are exactly the cases the
      // tracked bounds exist for.
      if (
        sel && !sel.isCollapsed && sel.anchorNode && sel.anchorNode.isConnected &&
        !parentRef.current?.contains(sel.anchorNode)
      ) return
      const text = buildDragSelectionText(entriesRef.current, ds, textOf)
      if (text === null || text === '') return
      e.preventDefault()
      e.clipboardData?.setData('text/plain', text)
    }
    document.addEventListener('copy', onCopy)
    return () => document.removeEventListener('copy', onCopy)
  }, [selectAllRef, parentRef])

  // Refs so the layout effects don't have to list `virtualizer` (a fresh
  // instance every render) or `matchIndices` (a fresh array every chunk
  // arrival) in their dep arrays — without these, the scroll-to-index calls
  // re-fired on every render and yanked the viewport away from a user who
  // had scrolled up to read older lines.
  const virtualizerRef = useRef(virtualizer)
  virtualizerRef.current = virtualizer
  const clearResizeResidual = useCallback(() => {
    const content = parentRef.current?.querySelector<HTMLElement>('[data-log-content]')
    if (content && resizeResidualRef.current) content.style.top = '0px'
    resizeResidualRef.current = 0
  }, [parentRef])
  const captureHistory = useCallback(() => {
    const el = parentRef.current
    if (!el?.clientHeight || followRef.current || wrappingHistoryRef.current) return
    const offset = el.scrollTop - resizeResidualRef.current
    const item = virtualizerRef.current.getVirtualItemForOffset(offset)
    const entry = item && entriesRef.current[item.index]
    if (item && entry) historyAnchorRef.current = {
      id: entry.id, index: item.index, inset: Math.max(0, offset - item.start),
      top: el.getBoundingClientRect().top,
    }
  }, [parentRef])
  const cancelWrapAnchor = useCallback(() => {
    if (wrapFrameRef.current !== null) cancelAnimationFrame(wrapFrameRef.current)
    wrapFrameRef.current = null
    wrapOffsetRef.current = null
    wrappingHistoryRef.current = null
  }, [])
  const restoreWrapAnchor = useCallback(() => {
    const anchor = wrappingHistoryRef.current, el = parentRef.current
    if (!anchor || !el?.clientHeight) return
    if (followRef.current) { cancelWrapAnchor(); return }
    if (entriesRef.current[anchor.index]?.id !== anchor.id) {
      anchor.index = entriesRef.current.findIndex(e => e.id === anchor.id)
    }
    if (anchor.index < 0) { cancelWrapAnchor(); return }
    virtualizerRef.current.getVirtualItems()
    const item = virtualizerRef.current.measurementsCache[anchor.index]
    const offset = item.start + Math.min(anchor.inset, Math.max(0, item.size - 1)) +
      el.getBoundingClientRect().top - anchor.top
    const changed = wrapOffsetRef.current === null || Math.abs(offset - wrapOffsetRef.current) > 0.5
    const displaced = Math.abs(el.scrollTop - offset) > 0.5
    if (displaced) virtualizerRef.current.scrollToOffset(offset, { align: 'start' })
    // Appends below the anchor must not keep restarting the guard. Only an
    // actual anchor/geometry correction extends its settling interval.
    if (!changed && !displaced && wrapFrameRef.current !== null) return
    wrapOffsetRef.current = offset
    if (wrapFrameRef.current !== null) cancelAnimationFrame(wrapFrameRef.current)
    // Keep layout-driven scroll events out of Follow until the committed row
    // measurements have had two quiet frames, just like resize correction.
    wrapFrameRef.current = requestAnimationFrame(() => {
      wrapFrameRef.current = requestAnimationFrame(() => {
        wrapFrameRef.current = null
        wrapOffsetRef.current = null
        wrappingHistoryRef.current = null
        captureHistory()
      })
    })
  }, [parentRef, captureHistory, cancelWrapAnchor])
  useLayoutEffect(() => {
    if (wordWrap !== committedWrapRef.current) {
      if (!followRef.current && historyAnchorRef.current) {
        wrappingHistoryRef.current = { ...historyAnchorRef.current }
        wrapOffsetRef.current = null
      }
      committedWrapRef.current = wordWrap
      clearResizeResidual()
    }
    if (followRef.current) clearResizeResidual()
    if (wrappingHistoryRef.current) restoreWrapAnchor()
    else captureHistory()
  })
  useEffect(() => cancelWrapAnchor, [cancelWrapAnchor])

  // If the viewport unmounts mid-drag (tab close, window switch), the
  // select-none guard must not linger on <body>.
  useEffect(() => () => { endDragRef.current?.(); document.body.classList.remove('log-select-drag') }, [])

  // Tracked-selection invalidation by user intent: a mousedown anywhere
  // OUTSIDE the viewer, or Escape, ends the logical drag selection. Clicks
  // inside the viewer are handled by the content mousedown itself (fresh
  // drag resets, scrollbar clicks must keep the selection alive).
  useEffect(() => {
    const onDocMouseDown = (ev: MouseEvent) => {
      // Left button only — a right-click means "context menu", and its Copy
      // must still see the tracked selection.
      if (ev.button !== 0) return
      if (draggingRef.current) return
      if (parentRef.current?.contains(ev.target as Node)) return
      dragSelRef.current = null
    }
    const onKeyDown = (ev: KeyboardEvent) => {
      if (ev.key === 'Escape') dragSelRef.current = null
    }
    document.addEventListener('mousedown', onDocMouseDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('mousedown', onDocMouseDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [parentRef])

  // Last known pointer Y of the active drag. Updated by mousemove; consumed
  // by the selectionchange clamp below — the clamp must NOT hang off
  // mousemove alone, because when the pointer crosses the container edge the
  // engine keeps extending the selection from its own drag-autoscroll TIMER
  // with no mouse events in between.
  const lastPointerYRef = useRef<number | null>(null)

  // Engine-bug safety net for drag-selection: WebKitGTK's caret hit-test can
  // resolve a point in the empty space right of a short line (a blank row,
  // or anywhere past the container's right edge) to the FIRST position of
  // the wrapper — the selection visually jumps to the top of the list.
  // Compare the row the ENGINE put the focus in with the row actually under
  // the pointer; if they disagree by more than one row, re-extend the
  // selection to the end of the pointer's row. On correct engines the
  // mismatch never trips, so this stays dormant. Returns true when it
  // corrected the selection (the caller then waits for the induced
  // selectionchange instead of tracking the bogus focus).
  function clampDragFocus(): boolean {
    const pointerY = lastPointerYRef.current
    const c = parentRef.current
    if (pointerY === null || !c) return false
    const sel = window.getSelection()
    if (!sel || sel.rangeCount === 0) return false
    const rowEls = Array.from(c.querySelectorAll<HTMLElement>('[data-index]'))
    const bands = rowEls.map((r) => {
      const b = r.getBoundingClientRect()
      return { index: Number(r.getAttribute('data-index')), top: b.top, bottom: b.bottom }
    })
    // Clamp the pointer into the container box: above the top edge targets the
    // first visible row (native auto-scroll keeps feeding rows in).
    const cb = c.getBoundingClientRect()
    const y = Math.min(Math.max(pointerY, cb.top + 1), cb.bottom - 1)
    const target = pickRowIndexAtY(bands, y)
    if (target === null) return false
    const focus = sel.focusNode ? resolveRowEndpoint(c, sel.focusNode, sel.focusOffset) : null
    if (focus !== null && Math.abs(focus.index - target) <= 1) return false
    const rowEl = rowEls.find((r) => Number(r.getAttribute('data-index')) === target)
    if (!rowEl) return false
    try {
      sel.extend(rowEl, rowEl.childNodes.length)
      return true
    } catch {
      // extend() throws on nodes detached by a concurrent row update — the
      // next selection change retries with fresh nodes.
      return false
    }
  }
  const clampDragFocusRef = useRef(clampDragFocus)
  clampDragFocusRef.current = clampDragFocus

  // Wrap toggle / probe arrival invalidate every cached measurement: wrap-on
  // heights are meaningless after wrap-off (and vice versa), and the probed
  // height replaces the initial 20px estimate for all unmeasured rows.
  useLayoutEffect(() => {
    virtualizerRef.current.measure()
    restoreWrapAnchor()
  }, [wordWrap, probedRowHeight, restoreWrapAnchor])
  const matchIndicesRef = useRef(matchIndices)
  matchIndicesRef.current = matchIndices
  // safeMatchIndexRef mirrors safeMatchIndex so the scroll effect can read it
  // without making it a dependency (which would re-fire on every chunk arrival
  // that shifts matchIndices, hard-pinning the viewport and blocking free scroll).
  const safeMatchIndexRef = useRef(safeMatchIndex)
  safeMatchIndexRef.current = safeMatchIndex

  const programmaticScrollTo = useCallback((idx: number, align: 'start' | 'center' | 'end') => {
    cancelWrapAnchor()
    clearResizeResidual()
    programmaticScrollRef.current = true
    virtualizerRef.current.scrollToIndex(idx, { align })
    // Second pass one frame later: rows measured after the first scroll can
    // shift the target offset (estimate 20px vs real height), leaving an
    // 'end' align short of the actual bottom. Re-issue with fresh measurements,
    // then clear the gate so the onScroll events emitted by both calls have
    // had time to run + be ignored.
    requestAnimationFrame(() => {
      virtualizerRef.current.scrollToIndex(idx, { align })
      requestAnimationFrame(() => { programmaticScrollRef.current = false })
    })
  }, [cancelWrapAnchor, clearResizeResidual])

  // Resume following: jump to the newest line and re-arm follow. Fired by the
  // floating button.
  function resumeFollow() {
    setFollow(true)
    const len = entries.length
    if (len > 0) programmaticScrollTo(len - 1, 'end')
  }

  // Auto-scroll-to-bottom: only when follow is on AND the newest line actually
  // changed. Tracked by the tail entry's ID, not the array length — at the
  // buffer cap the length is constant (trim N + append N) while the content
  // still advances. Plain re-renders (e.g. virtualizer re-measuring an item
  // in view) don't trigger a scroll.
  const lastEntryId = entries.length > 0 ? entries[entries.length - 1].id : -1
  const prevFollowedIdRef = useRef(lastEntryId)
  useLayoutEffect(() => {
    if (follow && lastEntryId >= 0 && lastEntryId !== prevFollowedIdRef.current) {
      programmaticScrollTo(entriesRef.current.length - 1, 'end')
    }
    prevFollowedIdRef.current = lastEntryId
  }, [lastEntryId, follow, programmaticScrollTo])

  // ResizeObserver runs after layout and before paint. Correct the offset in
  // that same delivery, not a later animation frame: a dock height change moves
  // its top edge, and a delayed correction paints the rows in two positions.
  // While reading history, retain their screen position instead of resuming
  // follow. At the scroll bounds the browser clamps to the available history.
  const hasEntries = entries.length > 0
  useEffect(() => {
    const el = parentRef.current
    if (!el || typeof ResizeObserver === 'undefined') return
    let lastTop: number | null = el.clientHeight ? el.getBoundingClientRect().top : null
    let lastWidth = el.clientWidth
    let lastHeight = el.clientHeight
    let lastScrollHeight = el.scrollHeight
    let historyOffset = el.scrollTop - resizeResidualRef.current
    let appliedOffset = el.scrollTop
    let gateFrame: number | null = null
    const ro = new ResizeObserver(() => {
      const top = el.getBoundingClientRect().top
      const width = el.clientWidth, height = el.clientHeight
      const moved = lastTop === null ? 0 : top - lastTop
      const scrollHeight = el.scrollHeight
      const changed = height !== lastHeight || (wordWrap && width !== lastWidth) ||
        (followRef.current && scrollHeight !== lastScrollHeight)
      lastTop = height ? top : null; lastWidth = width; lastHeight = height
      lastScrollHeight = scrollHeight
      // Hidden tabs have no meaningful geometry. Do not carry their zero rect
      // into a restored viewport or move the user's saved history position.
      if (!height || entriesRef.current.length === 0) return
      if (wrappingHistoryRef.current) { restoreWrapAnchor(); return }
      if (el.scrollTop !== appliedOffset) historyOffset = el.scrollTop - resizeResidualRef.current
      if (!changed) return
      programmaticScrollRef.current = true
      const maximum = Math.max(0, el.scrollHeight - height)
      historyOffset = followRef.current ? maximum : Math.max(0, Math.min(maximum, historyOffset + moved))
      el.scrollTop = historyOffset
      appliedOffset = el.scrollTop
      // scrollTop may round to device pixels. Carry the intended offset, not
      // its rounded readback, and keep that subpixel remainder in the content
      // position. Incremental rounding must not accumulate into a line jump.
      resizeResidualRef.current = followRef.current ? 0 : appliedOffset - historyOffset
      const inner = el.querySelector<HTMLElement>('[data-log-content]')
      if (inner) inner.style.top = `${resizeResidualRef.current}px`
      if (gateFrame !== null) cancelAnimationFrame(gateFrame)
      gateFrame = requestAnimationFrame(() => {
        gateFrame = requestAnimationFrame(() => {
          gateFrame = null
          programmaticScrollRef.current = false
          captureHistory()
        })
      })
    })
    ro.observe(el)
    // Wrapping changes the measured content height without resizing the
    // viewport. Keep following through those measurements as well, so the
    // next dock resize cannot expose a stale tail position as a jump.
    const content = el.querySelector('[data-log-content]')
    if (content) ro.observe(content)
    return () => {
      ro.disconnect()
      clearResizeResidual()
      if (gateFrame !== null) {
        cancelAnimationFrame(gateFrame)
        programmaticScrollRef.current = false
      }
    }
  }, [parentRef, wordWrap, hasEntries, restoreWrapAnchor, captureHistory, clearResizeResidual])

  // Search target: scroll only when the user explicitly navigated (the tick
  // bumps on a fresh query or F3 / Enter / ↑↓ button presses). Listening to
  // safeMatchIndex directly used to re-fire on every chunk arrival that
  // shifted matchIndices, hard-pinning the viewport on the current match
  // and making free scrolling impossible while a search was active.
  useEffect(() => {
    if (searchNavTick === 0) return
    const idxs = matchIndicesRef.current
    if (idxs.length === 0) return
    const targetIdx = idxs[safeMatchIndexRef.current]
    if (targetIdx === undefined) return
    programmaticScrollTo(targetIdx, 'center')
  }, [searchNavTick, programmaticScrollTo])

  return (
    <div className="relative min-h-0 flex-1" onKeyDown={event=>{
      if(event.key==='End'&&!event.defaultPrevented&&!event.nativeEvent.isComposing&&!event.ctrlKey&&!event.metaKey&&!event.altKey&&!event.shiftKey){
        event.preventDefault();resumeFollow();parentRef.current?.focus({preventScroll:true})
      }
    }}>
      <div
        ref={parentRef}
        // select-text keeps this subtree selectable while the body-level
        // `log-select-drag` guard (see onMouseDown below) suppresses selection
        // everywhere else during a drag.
        data-log-viewport
        tabIndex={0}
        className="h-full w-full select-text overflow-auto bg-app px-3 py-1 font-mono text-[13px] leading-[18px] outline-none"
        // Wheel-up is unambiguous user intent to stop following. Unlike the
        // onScroll heuristic it cannot be swallowed by the programmatic-scroll
        // gate, which on a bursty stream is high a large fraction of the time
        // (every chunk pulses it) — the old "can't unstick from the bottom" bug.
        onWheel={(e) => {
          cancelWrapAnchor()
          clearResizeResidual()
          if (e.deltaY < 0 && followRef.current) setFollow(false)
          // WebKit can miss mouseup outside its window. A released wheel
          // must not keep running the selection clamp or freezing ingestion.
          if (draggingRef.current && !(e.buttons & 1)) endDragRef.current?.()
          // Extending a native selection scrolls its focus back into view in
          // WebKit. During wheel input, let the browser scroll freely and
          // resume caret correction only after a real pointer movement.
          if (draggingRef.current) lastPointerYRef.current = null
        }}
        onScroll={() => {
          if (!parentRef.current) return
          // Programmatic scroll (auto-stick / search recentre) also fires
          // onScroll. Ignore those — otherwise the initial backlog landing
          // would flip follow off the moment the viewport sat at scrollTop=0
          // with a tall total size, leaving the new window pinned at the
          // start instead of at the live tail.
          if (programmaticScrollRef.current || wrappingHistoryRef.current) return
          const { scrollTop, scrollHeight, clientHeight } = parentRef.current
          // Stick-to-bottom: drop follow when the user scrolls up past
          // ~50px (scrollbar drag / keyboard — wheel is handled above),
          // re-arm it when they scroll back to the bottom.
          const distFromBottom = scrollHeight - scrollTop - clientHeight
          if (distFromBottom > 50) {
            if (followRef.current) setFollow(false)
          } else {
            if (!followRef.current) setFollow(true)
          }
          captureHistory()
        }}
        onKeyDownCapture={e => {
          if (['ArrowUp', 'ArrowDown', 'PageUp', 'PageDown', 'Home', 'End'].includes(e.key)) {
            cancelWrapAnchor(); clearResizeResidual()
          }
        }}
      >
        {entries.length === 0 ? (
          <span className="text-fg-subtle">{t('logs.empty')}</span>
        ) : (
          <div
            data-log-content
            style={{
              height: `${virtualizer.getTotalSize()}px`,
              width: '100%',
              position: 'relative',
            }}
            // mousedown on the actual content (not the scrollbar) starts a
            // manual selection → cancel the Ctrl+A "select all" so copy returns
            // just the user's range, and signal the drag so the parent pauses
            // stream flushes (a DOM update mid-drag collapses the selection).
            // The body-level guard class turns off user-select everywhere
            // OUTSIDE the viewport for the duration of the drag — without it,
            // dragging above the viewport pulls the toolbar and window chrome
            // into the selection. Scrollbar drags land on the parent, not
            // here, so scrolling keeps select-all alive and doesn't pause the
            // stream.
            onPointerDown={(e) => {
              // LEFT button only. A right-click opens the context menu and
              // its mouseup never reaches us — a "drag" started here would
              // stay armed forever: the selection then follows the bare
              // cursor and the per-selectionchange clamp work makes wheel
              // scrolling crawl. The click also must not touch the existing
              // selection state (context-menu Copy needs it intact).
              if (e.button !== 0) return
              const content = e.currentTarget
              const pointerId = e.pointerId
              // Virtual rows can unmount during a wheel gesture. Capture on
              // their stable wrapper so pointerup still reaches the page.
              content.setPointerCapture(pointerId)
              selectAllRef.current = false
              // Fresh drag → new logical anchor (set on the first
              // selectionchange). Shift+click EXTENDS the browser selection
              // from the existing anchor, so keep the tracked one then.
              if (!e.shiftKey || dragSelRef.current === null) dragSelRef.current = null
              draggingRef.current = true
              lastPointerYRef.current = e.clientY
              pointerPositionRef.current = {x:e.clientX,y:e.clientY}
              onSelectingChange(true)
              document.body.classList.add('log-select-drag')
              // mousemove only RECORDS the pointer — the clamp itself runs on
              // selectionchange, which also catches engine-timer updates that
              // arrive without any mouse event. It must NOT infer "drag over"
              // from ev.buttons: the browser synthesizes mousemoves when
              // content scrolls under a held wheel, and those can carry a
              // stale buttons=0.
              const onMove = (ev: MouseEvent) => {
                const previous=pointerPositionRef.current
                if(previous?.x===ev.clientX && previous.y===ev.clientY) return
                pointerPositionRef.current={x:ev.clientX,y:ev.clientY}
                lastPointerYRef.current=ev.clientY
              }
              const endDrag = () => {
                window.removeEventListener('mousemove', onMove)
                window.removeEventListener('mouseup', endDrag, true)
                window.removeEventListener('pointerup', endDrag, true)
                window.removeEventListener('pointercancel', endDrag, true)
                content.removeEventListener('lostpointercapture', endDrag)
                window.removeEventListener('blur', endDrag)
                document.removeEventListener('visibilitychange', endDrag)
                draggingRef.current = false
                endDragRef.current = null
                document.body.classList.remove('log-select-drag')
                onSelectingChange(false)
                if(content.hasPointerCapture(pointerId)) content.releasePointerCapture(pointerId)
              }
              endDragRef.current=endDrag
              window.addEventListener('mousemove', onMove)
              window.addEventListener('mouseup', endDrag, true)
              window.addEventListener('pointerup', endDrag, true)
              window.addEventListener('pointercancel', endDrag, true)
              content.addEventListener('lostpointercapture', endDrag)
              // Missed-mouseup safety nets: releasing the button outside a
              // lost/hidden window never delivers mouseup — a stuck drag
              // would make the selection follow the bare cursor forever.
              window.addEventListener('blur', endDrag)
              document.addEventListener('visibilitychange', endDrag)
            }}
          >
            {renderedItems.map((virtualItem) => {
              const idx = virtualItem.index
              const entry = entries[idx]
              const colorClass = LEVEL_CLASS[entry.level ?? 'UNKNOWN']
              const isCurrentMatch = matchIndices[safeMatchIndex] === idx
              const time = rowOpts.showTime ? fmtTime(entry.ts).padEnd(12) + ' ' : ''

              return (
                <div
                  key={virtualItem.key}
                  data-index={idx}
                  // Measure rows only when wrap can make their heights vary;
                  // wrap-off rows are uniform (see probedRowHeight above).
                  ref={wordWrap ? virtualizer.measureElement : undefined}
                  // Positioned via `top`, NOT `transform: translateY` — with
                  // transforms every row sits at layout-y 0 and only the
                  // visual position differs, and WebKitGTK's caret hit-test
                  // (positionForPoint) is transform-unaware in places, which
                  // broke drag-selection in the desktop webview.
                  style={{
                    position: 'absolute',
                    top: `${virtualItem.start}px`,
                    left: 0,
                    width: '100%',
                  }}
                  className={`${colorClass} ${wordWrap ? 'whitespace-pre-wrap break-all' : 'whitespace-pre'} ${isCurrentMatch ? 'bg-accent/10' : ''}`}
                >
                  {/* The prefixes are text of the row (selection offsets and
                      copies count them: rowText). A blank log line renders
                      an NBSP (renderMessage) for a caret position. */}
                  {time && <span className="text-fg-subtle" title={entry.ts}>{time}</span>}
                  {rowOpts.showSource && <span style={{ color: sourceColor(entry.src) }}>{rowOpts.labelOf(entry.src) + ' '}</span>}
                  {renderMessage(entry, rangesFor(entry), isCurrentMatch)}
                </div>
              )
            })}
          </div>
        )}
      </div>
      {/* Floating follow button — only while NOT following. Click resumes
          follow AND jumps to the newest line. */}
      {!follow && (
        <div className="absolute bottom-3 right-5 z-10"><ActionIconButton onClick={resumeFollow} aria-keyshortcuts="End" label={t('logs.follow')} description={`${t('logs.follow.tooltip')} (End)`} className="bg-panel text-accent"><ActionIcon name="down"/></ActionIconButton></div>
      )}
    </div>
  )
}
