import { create } from 'zustand'
import type { KindDescriptor, Ref, Row, ScopeSel, TargetRef } from '../api/types'

/** What the open target's workspace lends the palette. */
export interface PaletteHost {
  target: TargetRef
  kinds: KindDescriptor[]
  /** The one-shot scope list; null: scopes cannot be listed. */
  scopes: string[] | null
  /** The target's selected scopes, even on an unscoped table; null until loaded. */
  selectedScope: ScopeSel | null
  /** Opens a view; filter replaces the table filter ('' clears it). */
  openKind(kind: string, filter: string): void
  setScope(scope: ScopeSel): void
  /** Opens the object's kind and details, preserving the selected scopes. */
  openObject(ref: Ref): void
}

interface State {
  open: boolean
  host: PaletteHost | null
  /** The current table's rows. */
  rows: Row[]
  /** The scope list kept live by the open table's scope picker (null: none). */
  liveScopes: string[] | null
}

export const usePalette = create<State>(() => ({ open: false, host: null, rows: [], liveScopes: null }))

export const openPalette = () => usePalette.setState({ open: true })
export const closePalette = () => usePalette.setState({ open: false })

/** Registers v under key until the returned function runs (only if still v). */
export function lend<K extends 'host' | 'rows' | 'liveScopes'>(key: K, v: State[K]): () => void {
  usePalette.setState({ [key]: v } as Partial<State>)
  return () => {
    if (usePalette.getState()[key] === v) usePalette.setState({ [key]: key === 'rows' ? [] : null } as Partial<State>)
  }
}
