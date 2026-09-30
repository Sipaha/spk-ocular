import { create } from 'zustand'
import type { Ref, TargetRef } from '../api/types'
import { refTitle } from '../refs'

// The bottom panel's tabs live above the per-target Workspace: log tabs
// belong to their target (its session) and close when another target is
// selected; terminal tabs belong to the app and stay (their target is
// shown on the tab when it is not the current one).

export interface TermOpen {
  ref: Ref
  instance?: string
  channel?: string
  /** argv; empty = the provider's interactive shell */
  command?: string[]
}

interface TabBase {
  id: string
  target: TargetRef
  targetTitle: string
  title: string
}

export interface LogsTab extends TabBase {
  kind: 'logs'
  ref: Ref
}

export interface TermTab extends TabBase {
  kind: 'term'
  open: TermOpen
  /** a tooltip: context, server, command */
  hint?: string
  /** the target's configRev the terminal was opened with */
  rev?: string
}

export type DockTab = LogsTab | TermTab

export interface DockState {
  tabs: DockTab[]
  active: string | null
  height: number
}

export const DEFAULT_DOCK = 320
export const MIN_DOCK = 140

export const useDock = create<DockState>(() => ({ tabs: [], active: null, height: DEFAULT_DOCK }))

const refKey = (r: Ref) => `${r.kind}/${r.scope ?? ''}/${r.name}/${r.uid ?? ''}`
const targetKey = (t: TargetRef) => `${t.provider}/${t.id}`
let termSeq = 0

function activate(tab: DockTab) {
  useDock.setState((s) => ({ tabs: s.tabs.some((x) => x.id === tab.id) ? s.tabs : [...s.tabs, tab], active: tab.id }))
}

export const dock = {
  /** One log tab per object and target; opening it again activates it. */
  openLogs(target: TargetRef, targetTitle: string, ref: Ref) {
    activate({ kind: 'logs', id: `logs:${targetKey(target)}:${refKey(ref)}`, target, targetTitle, ref, title: `${ref.kind.split('/').pop()}/${refTitle(ref)}` })
  },
  /** Every open is a new terminal (two shells in one pod are normal). */
  openTerminal(target: TargetRef, targetTitle: string, open: TermOpen) {
    termSeq++
    activate({ kind: 'term', id: `term:${targetKey(target)}:${refKey(open.ref)}:${termSeq}`, target, targetTitle, open, title: refTitle(open.ref) })
  },
  update(id: string, patch: Partial<Pick<TermTab, 'title' | 'hint' | 'rev'>>) {
    useDock.setState((s) => ({ tabs: s.tabs.map((t) => (t.id === id ? { ...t, ...patch } : t)) }))
  },
  activate(id: string) {
    useDock.setState({ active: id })
  },
  close(id: string) {
    useDock.setState((s) => {
      const i = s.tabs.findIndex((x) => x.id === id)
      const rest = s.tabs.filter((x) => x.id !== id)
      const active = s.active === id ? (rest[Math.min(i, rest.length - 1)]?.id ?? null) : s.active
      return { tabs: rest, active }
    })
  },
  /** Another target is selected: its log tabs go (their session is closing), terminals stay. */
  keepLogsOf(target: TargetRef | null) {
    useDock.setState((s) => {
      const keep = s.tabs.filter((t) => t.kind !== 'logs' || (target && targetKey(t.target) === targetKey(target)))
      if (keep.length === s.tabs.length) return {}
      const active = keep.some((t) => t.id === s.active) ? s.active : (keep[keep.length - 1]?.id ?? null)
      return { tabs: keep, active }
    })
  },
  setHeight(h: number) {
    useDock.setState({ height: h })
  },
}
