import type { Ref, ScopeSel } from '../api/types'

/**
 * What a target's workspace looked like when it was left (P18): going back
 * shows it as it was. In memory for the app's run, not in SQLite; the kind
 * and scope are also in target_state (across runs). Marks are never kept:
 * an action must not touch what was not seen being marked.
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
}

/** Remembers the sort of kind in target. */
export function rememberSort(target: string, kind: string, s: { col: string; desc: boolean }) {
  memoOf(target).sorts[kind] = s
}

/** A page's key in PageMemo: its kind and scope. */
export const pageMemoKey = (kind: string, scope: ScopeSel) => `${kind}/${JSON.stringify(scope)}`

/** Tests: every target starts unvisited. */
export function forgetPages() {
  memos.clear()
}
