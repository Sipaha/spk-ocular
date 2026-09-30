// Unsaved edits are never lost silently. The one open editor holds them;
// whatever would drop it (closing the details, another object, tab, view or
// target) goes through mayLeave, which asks first (DiscardPrompt).
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

let holder: EditHolder | null = null
let pending: Pending | null = null
const listeners = new Set<() => void>()
const emit = () => listeners.forEach((l) => l())

/** The editor holds its edits until the returned release is called. */
export function holdEdits(h: EditHolder): () => void {
  holder = h
  emit()
  return () => {
    if (holder !== h) return
    holder = null
    emit()
    if (pending) {
      // The editor went on its own (its object was deleted, say): nothing to ask.
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
export const editsHeld = () => !!holder?.dirty()

/**
 * Runs go now when no edits would be lost; else asks, and runs go only if
 * the user discards them (stay, if given, when they keep editing).
 */
export function mayLeave(go: () => void, stay?: () => void): void {
  if (!holder?.dirty()) {
    go()
    return
  }
  // A second request while asking replaces the first: the latest choice wins.
  pending?.stay?.()
  pending = { go, stay }
  emit()
}

/** The prompt's answer: drop the edits and go on. */
export function discardEdits() {
  const p = pending
  const h = holder
  pending = null
  holder = null
  emit()
  h?.discard()
  p?.go()
}

/** The prompt's answer: stay in the editor. */
export function keepEditing() {
  const p = pending
  pending = null
  emit()
  p?.stay?.()
  holder?.focus?.()
}

const subscribe = (l: () => void) => {
  listeners.add(l)
  return () => listeners.delete(l)
}

/** The editor holding edits now (changes when one starts or ends; its
 * edits may be clean): what waits for edits re-checks editsHeld on change. */
export const useEditHolder = () => useSyncExternalStore(subscribe, () => holder)

/** The prompt is asked now. */
export const useLeaveAsked = () => useSyncExternalStore(subscribe, () => pending !== null)

/** Tests: nothing held, nothing asked. */
export function resetGuard() {
  holder = null
  pending = null
  emit()
}

// Browser mode: a reload or closing the tab asks too.
if (typeof window !== 'undefined') {
  window.addEventListener('beforeunload', (e) => {
    if (editsHeld()) e.preventDefault()
  })
}
