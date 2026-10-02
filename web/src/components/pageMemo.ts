import type { Ref, ScopeSel } from '../api/types'

/**
 * What a target's workspace looked like when it was left (P18): going back
 * shows it as it was. The page and the sorts also survive an app restart
 * (P19): they are persisted in the target's target_state under "pageMemo"
 * and seeded back on the first visit of a run. Marks are never kept: an
 * action must not touch what was not seen being marked.
 */
export interface TargetMemo {
  /** The kind and scope shown. */
  ui?: { kind: string; scope: ScopeSel }
  navOpen?: string[]
  /** Per kind: the column it was sorted by. */
  sorts: Record<string, { col: string; desc: boolean }>
  /** The page shown last: its filter, cursor, open details and their tab. */
  page?: PageMemo
}

export interface PageMemo {
  /** The page's kind and scope (a page of another kind starts clean). */
  key: string
  filter: string
  selected: string | null
  open: Ref | null
  tab?: string
}

const memos = new Map<string, TargetMemo>()

/** Listeners for changes of what P19 persists (the page and the sorts). */
const listeners = new Map<string, Set<() => void>>()

/** Calls cb when the persisted part of target's memo changes; returns an unsubscribe. */
export function onMemoChange(target: string, cb: () => void): () => void {
  let set = listeners.get(target)
  if (!set) {
    set = new Set()
    listeners.set(target, set)
  }
  set.add(cb)
  return () => {
    set.delete(cb)
    if (!set.size) listeners.delete(target)
  }
}

function notify(target: string) {
  listeners.get(target)?.forEach((cb) => cb())
}

/** The memo of a target (by targetKey), made on first use. */
export function memoOf(target: string): TargetMemo {
  let m = memos.get(target)
  if (!m) {
    m = { sorts: {} }
    memos.set(target, m)
  }
  return m
}

/** Remembers what of target's workspace changed (merged into its memo). */
export function remember(target: string, patch: Partial<Omit<TargetMemo, 'sorts'>>) {
  Object.assign(memoOf(target), patch)
  // ui and navOpen persist by their own target_state keys; only the page
  // needs this module's listeners.
  if ('page' in patch) notify(target)
}

/** Remembers the sort of kind in target. */
export function rememberSort(target: string, kind: string, s: { col: string; desc: boolean }) {
  memoOf(target).sorts[kind] = s
  notify(target)
}

/**
 * Seeds what P19 persists without notifying: the values were just read from
 * target_state, so writing them back would add nothing.
 */
export function seedPersisted(target: string, persisted: { sorts?: TargetMemo['sorts']; page?: PageMemo }) {
  const m = memoOf(target)
  if (persisted.sorts) m.sorts = { ...m.sorts, ...persisted.sorts }
  if (persisted.page) m.page = persisted.page
}

/** A page's key in PageMemo: its kind and scope. */
export const pageMemoKey = (kind: string, scope: ScopeSel) => `${kind}/${JSON.stringify(scope)}`

/** Tests: every target starts unvisited. */
export function forgetPages() {
  memos.clear()
  listeners.clear()
}
