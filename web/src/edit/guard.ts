// Unsaved edits are never lost silently. Open editors hold them (the YAML
// editor and a value dialog may both be open); whatever would drop them
// (closing the details, another object, tab, view or target) goes through
// mayLeave, which asks first (DiscardPrompt).
import { useSyncExternalStore } from 'react'

export interface EditHolder {
  /** There are edits not written. */
  dirty: () => boolean
  /** The edits are dropped: leave edit mode. */
  discard: () => void
  /** Keep editing: focus the editor again. */
  focus?: () => void
}

interface Pending {
  go: () => void
  stay?: () => void
}

// Replaced, never changed in place: its identity is useEditHolder's snapshot.
let holders: readonly EditHolder[] = []
let pending: Pending | null = null
const listeners = new Set<() => void>()
const emit = () => listeners.forEach((l) => l())

const anyDirty = () => holders.some((h) => h.dirty())

/** The editor holds its edits until the returned release is called. */
export function holdEdits(h: EditHolder): () => void {
  holders = [...holders, h]
  emit()
  return () => {
    if (!holders.includes(h)) return
    holders = holders.filter((x) => x !== h)
    emit()
    if (pending && !anyDirty()) {
      // The editors went on their own (the object was deleted, say): nothing to ask.
      const p = pending
      pending = null
      emit()
      p.go()
    }
  }
}

/** The question is asked now (not a hook: for listeners). */
export const leaveAsked = () => pending !== null

/** Unsaved edits are held now. */
export const editsHeld = anyDirty

/**
 * Runs go now when no edits would be lost; else asks, and runs go only if
 * the user discards them (stay, if given, when they keep editing).
 */
export function mayLeave(go: () => void, stay?: () => void): void {
  if (!anyDirty()) {
    go()
    return
  }
  // A second request while asking replaces the first: the latest choice wins.
  pending?.stay?.()
  pending = { go, stay }
  emit()
}

/** The prompt's answer: drop the edits (of every editor) and go on. */
export function discardEdits() {
  const p = pending
  const hs = holders
  pending = null
  holders = []
  emit()
  for (const h of hs) h.discard()
  p?.go()
}

/** The prompt's answer: stay in the editor. */
export function keepEditing() {
  const p = pending
  pending = null
  emit()
  p?.stay?.()
  // The latest editor with edits: the one the question was about.
  const latest = [...holders].reverse().find((h) => h.dirty())
  latest?.focus?.()
}

const subscribe = (l: () => void) => {
  listeners.add(l)
  return () => listeners.delete(l)
}

/** The editors holding edits now (changes when one starts or ends; their
 * edits may be clean): what waits for edits re-checks editsHeld on change. */
export const useEditHolder = () => useSyncExternalStore(subscribe, () => holders)

/** The prompt is asked now. */
export const useLeaveAsked = () => useSyncExternalStore(subscribe, () => pending !== null)

/** Tests: nothing held, nothing asked. */
export function resetGuard() {
  holders = []
  pending = null
  emit()
}

// Browser mode: a reload or closing the tab asks too.
if (typeof window !== 'undefined') {
  window.addEventListener('beforeunload', (e) => {
    if (editsHeld()) e.preventDefault()
  })
}
