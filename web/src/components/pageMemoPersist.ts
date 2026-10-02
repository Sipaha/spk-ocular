import { useEffect } from 'react'
import type { Client } from '../api/client'
import { memoOf, onMemoChange, type PageMemo, type TargetMemo } from './pageMemo'

/** The target_state key the page snapshot is persisted under (P19). */
export const pageMemoKeyName = 'pageMemo'
/** The server caps one target_state value (maxStateValue); past it the memo stays memory-only. */
export const pageMemoMaxBytes = 4096

const debounceMs = 1000
const persistedVersion = 1

interface PersistedMemo {
  v: number
  sorts: TargetMemo['sorts']
  page?: PageMemo
}

/** JSON of what survives a restart, or null when it does not fit the server's cap. */
export function serializeMemo(m: TargetMemo): string | null {
  const s = JSON.stringify({ v: persistedVersion, sorts: m.sorts, page: m.page } satisfies PersistedMemo)
  return new TextEncoder().encode(s).length <= pageMemoMaxBytes ? s : null
}

/** The stored snapshot, or null when absent, malformed or of another version. */
export function parseMemo(json: string | undefined): { sorts: TargetMemo['sorts']; page?: PageMemo } | null {
  if (!json) return null
  let v: unknown
  try {
    v = JSON.parse(json)
  } catch {
    return null
  }
  if (!v || typeof v !== 'object') return null
  const p = v as Partial<PersistedMemo>
  if (p.v !== persistedVersion || !p.sorts || typeof p.sorts !== 'object' || Array.isArray(p.sorts)) return null
  const sorts = Object.fromEntries(
    Object.entries(p.sorts).filter(([, s]) => s && typeof s === 'object' && typeof (s as { col?: unknown }).col === 'string' && typeof (s as { desc?: unknown }).desc === 'boolean'),
  ) as TargetMemo['sorts']
  const page = validPage(p.page)
  return { sorts, page }
}

/** A page memo worth restoring, or undefined. */
function validPage(p: PageMemo | undefined): PageMemo | undefined {
  if (!p || typeof p !== 'object') return undefined
  if (typeof p.key !== 'string' || typeof p.filter !== 'string') return undefined
  if (p.selected != null && typeof p.selected !== 'string') return undefined
  if (p.tab != null && typeof p.tab !== 'string') return undefined
  if (p.open != null && (typeof p.open !== 'object' || typeof p.open.kind !== 'string' || typeof p.open.name !== 'string')) return undefined
  return p
}

/**
 * Debounced writer of the memo a target's Workspace shows. One write per
 * burst of changes; nothing on unchanged or over-cap content; flush writes
 * the pending snapshot now (leaving the target must not lose the last
 * second).
 */
function memoWriter(client: Client, provider: string, target: string) {
  let last: string | null = null
  let inFlight: string | null = null
  let timer: ReturnType<typeof setTimeout> | null = null
  let pending: string | null = null
  const clearTimer = () => {
    if (timer !== null) clearTimeout(timer)
    timer = null
  }
  const flush = () => {
    clearTimer()
    if (inFlight !== null) return
    const json = pending
    pending = null
    if (json === null || json === last) return
    inFlight = json
    void client.setTargetState(provider, target, pageMemoKeyName, json).then(
      () => { last = json },
      () => { last = null }, // a failed write is not proof of persistence
    ).finally(() => {
      inFlight = null
      // If a newer snapshot is still being edited, respect its debounce.
      // Otherwise a flush requested during the write can now finish.
      if (timer === null) flush()
    })
  }
  return {
    note(m: TargetMemo) {
      const json = serializeMemo(m)
      clearTimer()
      // Every notification supersedes the pending value, including a
      // return to the saved value or a snapshot too large to persist.
      pending = json === (inFlight ?? last) ? null : json
      if (pending !== null) timer = setTimeout(flush, debounceMs)
    },
    flush,
  }
}

// Keep a target's writer across Workspace unmounts: a slow request from
// before A → B → A must finish before the next write to A. The client is
// weakly held; targets already have application-lifetime page memos.
const writers = new WeakMap<Client, Map<string, ReturnType<typeof memoWriter>>>()

export function createMemoWriter(client: Client, provider: string, target: string) {
  let byTarget = writers.get(client)
  if (!byTarget) {
    byTarget = new Map()
    writers.set(client, byTarget)
  }
  const key = JSON.stringify([provider, target])
  let writer = byTarget.get(key)
  if (!writer) {
    writer = memoWriter(client, provider, target)
    byTarget.set(key, writer)
  }
  return writer
}

/** Wires a target's Workspace to persist its page snapshot (P19). */
export function useMemoPersist(client: Client, provider: string, target: string, tkey: string) {
  useEffect(() => {
    const w = createMemoWriter(client, provider, target)
    const off = onMemoChange(tkey, () => w.note(memoOf(tkey)))
    return () => {
      off()
      w.flush()
    }
  }, [client, provider, target, tkey])
}
