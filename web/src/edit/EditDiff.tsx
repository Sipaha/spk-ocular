import { useMemo, useState } from 'react'
import { t } from '../i18n'
import { diff, hunks, type DiffLine } from './diff'

/** Diff rows shown at once; "show the next" reaches the end. */
export const DIFF_PAGE = 2000
/** Longer lines are folded; "show the whole line" unfolds one. */
export const LONG_LINE = 2000

export const CONTEXT_PAGE = 20
type Row = { line: DiffLine; index: number } | { skipped: number; start: number; end: number; anchor: number; above: boolean; below: boolean }
type Expansion = { head: number; tail: number }

const opClass: Record<DiffLine['op'], string> = {
  ' ': 'text-fg-muted',
  '-': 'bg-danger/10 text-danger',
  '+': 'bg-success/10 text-success',
}

/**
 * The line diff of two texts, computed once: changes with context, in pages
 * of DIFF_PAGE rows, long lines folded. Everything is reachable: a page
 * never hides a change for good.
 */
export function EditDiff({ before, after, label, exactText = false }: { before: string; after: string; label?: string; exactText?: boolean }) {
  const d = useMemo(() => diff(before, after, exactText), [before, after, exactText])
  const blocks = useMemo(() => {
    let offset = 0
    const result: { start: number; end: number; skipped: boolean }[] = []
    for (const hunk of hunks(d.lines)) {
      const start = offset
      offset += 'skipped' in hunk ? hunk.skipped : hunk.lines.length
      result.push({ start, end: offset, skipped: 'skipped' in hunk })
    }
    return result
  }, [d])
  const added = useMemo(() => d.lines.filter((l) => l.op === '+').length, [d])
  const removed = useMemo(() => d.lines.filter((l) => l.op === '-').length, [d])
  const [view, setView] = useState(() => ({ source: d, shown: DIFF_PAGE, whole: new Set<number>(), expanded: new Map<number, Expansion>() }))
  if (view.source !== d) setView({ source: d, shown: DIFF_PAGE, whole: new Set(), expanded: new Map() })
  const rows = useMemo(() => blocks.flatMap((block): Row[] => {
    const lineRows = (start: number, end: number): Row[] => d.lines.slice(start, end).map((line, index) => ({ line, index: start + index }))
    if (!block.skipped) return lineRows(block.start, block.end)
    const expansion = view.expanded.get(block.start) ?? { head: 0, tail: 0 }
    const head = Math.min(expansion.head, block.end - block.start)
    const tail = Math.min(expansion.tail, block.end - block.start - head)
    const start = block.start + head, end = block.end - tail
    return [...lineRows(block.start, start), ...(end > start ? [{ skipped: end - start, start, end, anchor: block.start, above: block.end < d.lines.length, below: block.start > 0 }] : []), ...lineRows(end, block.end)]
  }), [blocks, d, view.expanded])
  const expand = (anchor: number, side: keyof Expansion) => setView(old => {
    const block = blocks.find(block => block.start === anchor)!
    const previous = old.expanded.get(anchor) ?? { head: 0, tail: 0 }
    const count = Math.min(CONTEXT_PAGE, block.end - block.start - previous.head - previous.tail)
    if (count <= 0) return old
    const expanded = new Map(old.expanded)
    expanded.set(anchor, { ...previous, [side]: previous[side] + count })
    return { ...old, expanded, shown: old.shown + count }
  })
  if (!rows.length) return null
  const rest = rows.length - view.shown
  return (
    <section aria-label={label ?? t('edit.diff')} className="flex min-h-0 flex-col gap-1">
      <h3 className="text-[12px] font-semibold uppercase tracking-wider text-fg-subtle">
        {label ?? t('edit.diff')} <span className="font-normal normal-case tracking-normal">· {t('edit.diffSummary', { removed, added })}</span>
      </h3>
      {d.approximate && <p className="text-xs text-warning">{t('edit.diffApprox')}</p>}
      <div className="overflow-auto rounded-md border border-line font-mono text-[13px] leading-[1.45]">
        {rows.slice(0, view.shown).map((r) =>
          'skipped' in r ? (
            <div key={'gap-'+r.anchor} data-diff-context className="flex flex-wrap items-center gap-x-3 gap-y-1 border-y border-line bg-sidebar/60 px-2 py-1 text-fg-subtle">
              <span>⋯ {t('edit.diffSkipped', { n: r.skipped })}</span>
              {r.below && <button type="button" className="rounded border border-line px-2 py-0.5 font-sans text-[12px] text-fg-muted hover:bg-hover focus-visible:outline-2 focus-visible:outline-accent" onClick={() => expand(r.anchor, 'head')}>{t('edit.contextAfter', { n: Math.min(CONTEXT_PAGE, r.skipped) })}</button>}
              {r.above && <button type="button" className="rounded border border-line px-2 py-0.5 font-sans text-[12px] text-fg-muted hover:bg-hover focus-visible:outline-2 focus-visible:outline-accent" onClick={() => expand(r.anchor, 'tail')}>{t('edit.contextBefore', { n: Math.min(CONTEXT_PAGE, r.skipped) })}</button>}
            </div>
          ) : (
            <DiffRow key={r.index} line={r.line} whole={view.whole.has(r.index)} onWhole={(on) => setView(s => ({ ...s, whole: on ? new Set(s.whole).add(r.index) : new Set([...s.whole].filter(index => index !== r.index)) }))} />
          ),
        )}
      </div>
      {rest > 0 && (
        <button type="button" className="self-start rounded-md border border-line px-2 py-0.5 text-xs hover:bg-hover" onClick={() => setView(old => ({ ...old, shown: old.shown + DIFF_PAGE }))}>
          {t('edit.diffMore', { n: Math.min(rest, DIFF_PAGE), total: rest })}
        </button>
      )}
    </section>
  )
}

function DiffRow({ line, whole, onWhole }: { line: DiffLine; whole: boolean; onWhole: (on: boolean) => void }) {
  const long = line.text.length > LONG_LINE
  return (
    <div data-op={line.op} className={['grid grid-cols-[3.5em_3.5em_1.2em_1fr]', opClass[line.op]].join(' ')}>
      <span className="select-none pr-1 text-right text-fg-subtle">{line.a ?? ''}</span>
      <span className="select-none pr-1 text-right text-fg-subtle">{line.b ?? ''}</span>
      <span className="select-none" aria-hidden>
        {line.op}
      </span>
      <span className="min-w-0 whitespace-pre-wrap break-all">
        {long && !whole ? line.text.slice(0, LONG_LINE) + '…' : line.text}
        {long && (
          <button type="button" className="ml-2 rounded border border-line px-1 font-sans text-[12px] text-fg-muted hover:bg-hover" onClick={() => onWhole(!whole)}>
            {whole ? t('edit.lineFold') : t('edit.lineWhole', { n: line.text.length })}
          </button>
        )}
      </span>
    </div>
  )
}
