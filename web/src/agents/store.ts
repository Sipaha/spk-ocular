import { create } from 'zustand'
import { selectedScopes } from '../scopes'
import type { Client } from '../api/client'
import type { AgentAccessStatus, AgentPending, AgentTarget, AgentScope, ScopeSel } from '../api/types'

// Agent access as the UI keeps it: the socket's state, the targets'
// grants, the plans waiting for the user. Reloaded on the agent_* events
// (and on resync); the journal is read by its tab on its own.

export type AgentsTab = 'grants' | 'journal'

export interface AgentsState {
  status: AgentAccessStatus | null
  targets: AgentTarget[]
  pending: AgentPending[]
  /** the panel is open, on which tab */
  panel: AgentsTab | null
  /** the target the grants tab edits ("provider/target") */
  chosen: string | null
  contextScopes: AgentScope[] | null
  /** the confirmation dialog is put aside until another plan comes */
  asideIds: string[]
  error: string | null
  /** grows with every agent_audit_changed (the open journal reloads) */
  auditTick: number
}

export const initialAgents: AgentsState = { status: null, targets: [], pending: [], panel: null, chosen: null, contextScopes: null, asideIds: [], error: null, auditTick: 0 }

export const useAgents = create<AgentsState>(() => ({ ...initialAgents }))

const errText = (e: unknown) => (e instanceof Error ? e.message : String(e))

/** A reload in flight takes one more after it at most: an older answer never overwrites a newer one. */
function sequenced(run: () => Promise<void>) {
  let inFlight = false
  let again = false
  return async function load() {
    if (inFlight) {
      again = true
      return
    }
    inFlight = true
    try {
      do {
        again = false
        await run()
      } while (again)
    } finally {
      inFlight = false
    }
  }
}

export function agentLoaders(client: Client) {
  const grants = sequenced(async () => {
    try {
      const [status, targets] = await Promise.all([client.agentAccessStatus(), client.listAgentGrants()])
      useAgents.setState({ status, targets, error: null })
    } catch (e) {
      useAgents.setState({ error: errText(e) })
    }
  })
  const pending = sequenced(async () => {
    try {
      const list = await client.listAgentPending()
      list.sort((a, b) => a.at.localeCompare(b.at) || a.id.localeCompare(b.id))
      useAgents.setState((s) => ({ pending: list, asideIds: s.asideIds.filter((id) => list.some((p) => p.id === id)) }))
    } catch (e) {
      useAgents.setState({ error: errText(e) })
    }
  })
  return { grants, pending }
}

export const agents = {
  showPanel(tab: AgentsTab | null) {
    useAgents.setState(tab === null ? { panel: tab, contextScopes: null } : { panel: tab })
  },
  choose(key: string | null) {
    useAgents.setState({ chosen: key, contextScopes: null })
  },
  openScopes(provider: string, target: string, scope: ScopeSel) {
    const contextScopes: AgentScope[] = scope.mode === 'all'
      ? [{ mode: 'all' }]
      : selectedScopes(scope).map((name) => ({ mode: 'one', name }))
    if (!contextScopes.length) return
    useAgents.setState({ panel: 'grants', chosen: agentTargetKey(provider, target), contextScopes })
  },
  /** Puts the waiting plans aside (the dialog comes back with a new one). */
  putAside() {
    useAgents.setState((s) => ({ asideIds: s.pending.map((p) => p.id) }))
  },
  bringBack() {
    useAgents.setState({ asideIds: [] })
  },
  auditChanged() {
    useAgents.setState((s) => ({ auditTick: s.auditTick + 1 }))
  },
}

/** The plans the dialog shows (in the order they came), unless put aside. */
export const shownPending = (s: Pick<AgentsState, 'pending' | 'asideIds'>) => (s.pending.some((p) => !s.asideIds.includes(p.id)) ? s.pending : [])

export const pendingOf = (list: AgentPending[], provider: string, target: string) => list.filter((p) => p.provider === provider && p.target === target).length

export const agentTargetKey = (provider: string, target: string) => `${provider}/${target}`
