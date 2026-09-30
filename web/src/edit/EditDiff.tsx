import { useMemo, useState } from 'react'
import { t } from '../i18n'
import { diff, hunks, type DiffLine } from './diff'

/** Diff rows shown at once; "show the next" reaches the end. */
export const DIFF_PAGE = 2000
/** Longer lines are folded; "show the whole line" unfolds one. */
export const LONG_LINE = 2000

type Row = { line: DiffLine } | { skipped: number }

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
export function EditDiff({ before, after, label }: { before: string; after: string; label?: string }) {
  const d = useMemo(() => diff(before, after), [before, after])
  const rows = useMemo(() => hunks(d.lines).flatMap((h): Row[] => ('skipped' in h ? [h] : h.lines.map((line) => ({ line })))), [d])
  const added = useMemo(() => d.lines.filter((l) => l.op === '+').length, [d])
  const removed = useMemo(() => d.lines.filter((l) => l.op === '-').length, [d])
  const [shown, setShown] = useState(DIFF_PAGE)
  const [whole, setWhole] = useState<ReadonlySet<number>>(new Set())
  if (!rows.length) return null
  const rest = rows.length - shown
  return (
    <section aria-label={label ?? t('edit.diff')} className="flex min-h-0 flex-col gap-1">
      <h3 className="text-[11px] font-semibold uppercase tracking-wider text-fg-subtle">
        {label ?? t('edit.diff')} <span className="font-normal normal-case tracking-normal">· {t('edit.diffSummary', { removed, added })}</span>
      </h3>
      {d.approximate && <p className="text-xs text-warning">{t('edit.diffApprox')}</p>}
      <div className="overflow-auto rounded-md border border-line font-mono text-[12px] leading-[1.45]">
        {rows.slice(0, shown).map((r, i) =>
          'skipped' in r ? (
            <div key={i} className="border-y border-line bg-sidebar/60 px-2 py-0.5 text-fg-subtle">
              ⋯ {t('edit.diffSkipped', { n: r.skipped })}
            </div>
          ) : (
            <DiffRow key={i} line={r.line} whole={whole.has(i)} onWhole={(on) => setWhole((s) => (on ? new Set(s).add(i) : new Set([...s].filter((x) => x !== i))))} />
          ),
        )}
      </div>
      {rest > 0 && (
        <button type="button" className="self-start rounded-md border border-line px-2 py-0.5 text-xs hover:bg-hover" onClick={() => setShown((n) => n + DIFF_PAGE)}>
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
          <button type="button" className="ml-2 rounded border border-line px-1 font-sans text-[11px] text-fg-muted hover:bg-hover" onClick={() => onWhole(!whole)}>
            {whole ? t('edit.lineFold') : t('edit.lineWhole', { n: line.text.length })}
          </button>
        )}
      </span>
    </div>
  )
}
