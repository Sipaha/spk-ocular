import { ApiError } from '../api/client'
import type { ActionPart, ActionResult } from '../api/types'
import { classLabel, t } from '../i18n'
import { errorDetail } from '../errors'

/** What became of a run (or of its review), for the user. */
export type Outcome =
  | { type: 'prepareFailed'; text: string }
  | { type: 'conflict'; text: string }
  | { type: 'unknown'; text: string }
  | { type: 'failed'; text: string }
  /** An answer after the timeout said it was done. */
  | { type: 'lateDone'; text: string }
  /** Not every part was done: one was refused, its outcome is unknown, or it was left. */
  | { type: 'partial'; text: string; parts: ActionPart[]; unknown: boolean }


// A visible focus on every button: WebKitGTK does not show :focus-visible
// for focus set by script (the initial focus, the Tab trap), and which
// button Enter presses must be seen.
export const btn = 'rounded-md px-3 py-1 outline-none focus:ring-2 focus:ring-accent focus:ring-offset-2 focus:ring-offset-panel disabled:opacity-50'

export class RunTimeout extends Error {}

export const partClass: Record<ActionPart['outcome'], string> = {
  done: 'text-success',
  refused: 'text-danger',
  unknown: 'text-warning',
  skipped: 'text-fg-subtle',
}

/** What a run's rejection means for the user. */
export function outcomeOf(e: unknown): Outcome {
  const code = codeOf(e)
  // No coded answer (the connection failed): the request may have been applied.
  const transport = !(e instanceof ApiError) || e.transport
  return code === 'unknown' || transport
    ? { type: 'unknown', text: `${t('action.unknown')} ${errorDetail(e)}` }
    : code === 'conflict'
      ? // The provider's own reason says what changed (or why the review no
        // longer holds) in full; a server's text gets the generic frame.
        { type: 'conflict', text: e instanceof ApiError && e.why ? errorDetail(e) : t('action.conflict', { detail: errorDetail(e) }) }
      : { type: 'failed', text: t('action.failed', { class: classLabel(code), detail: errorDetail(e) }) }
}

/** "2 of 3 done; 1 outcome unknown; 1 not run". */
export function partsSummary(parts: ActionPart[]): string {
  const n = (o: ActionPart['outcome']) => parts.filter((p) => p.outcome === o).length
  const out = [t('action.partsDone', { done: n('done'), total: parts.length })]
  if (n('refused')) out.push(t('action.partsRefused', { n: n('refused') }))
  if (n('unknown')) out.push(t('action.partsUnknown', { n: n('unknown') }))
  if (n('skipped')) out.push(t('action.partsSkipped', { n: n('skipped') }))
  return out.join('; ')
}

/** A result that did not finish (null: done). */
export function partialOf(res: ActionResult): Extract<Outcome, { type: 'partial' }> | null {
  if (!res.outcome || res.outcome === 'done') return null
  const unknown = res.outcome === 'unknown'
  const text = t(unknown ? 'action.partialUnknown' : res.outcome === 'refused' ? 'action.partialRefused' : 'action.partialSkipped')
  return { type: 'partial', text, parts: res.parts ?? [], unknown }
}

export const codeOf = (e: unknown) => (e instanceof ApiError ? e.code : 'internal')
