import { vi } from 'vitest'
import { ApiError, type Client } from '../api/client'
import type { AgentAccessStatus, AgentAuditEntry, AgentPending, AgentTarget, ApiEvent, ConnectionStatus, KindDescriptor, KindsView, Query, RecentObject, Ref, Row, Target, TargetsView, TerminalRequest, ViewStatus } from '../api/types'

export const k8s = (id: string, extra: Partial<Target> = {}): Target => ({
  provider: 'kubernetes',
  id,
  title: id,
  subtitle: `${id}-cluster`,
  details: [
    { key: 'cluster', value: `${id}-cluster` },
    { key: 'server', value: `https://${id}.example:6443` },
  ],
  ...extra,
})

/** What the Kubernetes provider calls its scopes (core.ScopeNames). */
export const k8sScopeNames = {
  singular: { key: 'kubernetes.scope.singular', text: 'Namespace' },
  plural: { key: 'kubernetes.scope.plural', text: 'namespaces' },
  all: { key: 'kubernetes.scope.all', text: 'All namespaces' },
}

/** A session's catalog of fixed kinds (ListKinds). */
export const kindsView = (kinds: KindDescriptor[], over: Partial<KindsView> = {}): KindsView => ({ kinds, rev: 1, state: 'ready', session: 1, ...over })

/** Resource component fixtures can opt into sessions that are already connected. */
export const connectedClient = (targets: Target[]) => fakeClient(targets, true)

/** An in-memory Client: tests mutate `view` and call `emit`. */
export function fakeClient(targets: Target[], connected = false) {
  let listener: ((e: ApiEvent) => void) | null = null
  let connectionID = 0
  const ready = (): ConnectionStatus => ({ id: ++connectionID, state: 'connected', phase: 'ready', attempt: 1, maxAttempts: 3, startedAt: Date.now(), attemptStarted: Date.now(), finishedAt: Date.now() })
  const state = {
    rows: [] as Row[],
    rowsByKind: {} as Record<string, Row[]>,
    /** Descriptors openView answers with (else pods / a generic one). */
    kinds: {} as Record<string, KindDescriptor>,
    statusByKind: {} as Record<string, ViewStatus>,
    version: 0,
    recents: [] as RecentObject[],
    view: { groups: [{ provider: 'kubernetes', title: 'Kubernetes', targets, problems: [], scopeNames: k8sScopeNames }], selected: null } as TargetsView,
    agentStatus: { state: 'serving', socket: '/home/u/.spk/ocular/agent.sock', instruction: 'SPK Ocular (Kubernetes/Docker): `curl -s --unix-socket ~/.spk/ocular/agent.sock http://ocular/v1`', pending: 0 } as AgentAccessStatus,
    agentTargets: [] as AgentTarget[],
    agentPending: [] as AgentPending[],
    audit: [] as AgentAuditEntry[],
  }
  const client: Client = {
 rbacSnapshot: vi.fn(async()=>({principal:"test-user",groups:[],accounts:[],grants:[],problems:[],truncated:false,discovery:"ready",capturedAt:Date.now()})),
 checkAccess: vi.fn(async(req:import("../api/types").AccessRequest)=>({attributes:req.attributes,state:"denied" as const,reason:"",evaluationError:"",checkedAt:Date.now()})),
 clusterTimeline: vi.fn(async () => ({events:[],resources:[],problems:[],discovery:"ready",truncated:false,capturedAt:Date.now()})),
    clusterGraph: vi.fn(async () => ({nodes:[],edges:[],problems:[],truncated:false,discovery:"ready",capturedAt:0})),
    files: vi.fn(async () => ({ path: "/", configRev: "test", text: "", entries: [] })),
    configurations: vi.fn(async () => ({ initialized: true, encrypted: false, locked: false, candidates: [], entries: [] })),
    helm: vi.fn(async () => ({})),
    setLanguage: vi.fn(async () => {}),
    appInfo: vi.fn(async () => ({ name: 'SPK Ocular', version: 'test', mode: 'browser' as const, language: 'en' as const })),
    listTargets: vi.fn(async () => {
      if (connected) for (const group of state.view.groups) for (const target of group.targets) {
        if (!Object.hasOwn(target, 'connection')) { target.connection = ready(); target.open = true }
      }
      return structuredClone(state.view)
    }),
    selectTarget: vi.fn(async (provider: string, id: string) => {
      const all = state.view.groups.flatMap((g) => g.targets)
      if (!all.some((x) => x.provider === provider && x.id === id)) throw new Error('not_found: no target')
      state.view.selected = { provider, id }
    }),
    closeTarget: vi.fn(async (provider: string, id: string) => {
      for (const g of state.view.groups) for (const t of g.targets) if (t.provider === provider && t.id === id) {
        t.open = false
        t.connection = { ...(t.connection ?? ready()), state: 'disconnected', phase: 'closed' }
      }
    }),
    connectTarget: vi.fn(async (provider: string, id: string) => {
      const target = state.view.groups.flatMap((g) => g.targets).find((t) => t.provider === provider && t.id === id)!
      target.connection = ready()
      target.open = true
      listener?.({ type: 'targets_changed' })
      return target.connection
    }),
    cancelConnectTarget: vi.fn(async (provider: string, id: string, attempt: number) => {
      const target = state.view.groups.flatMap((g) => g.targets).find((t) => t.provider === provider && t.id === id)!
      if (target.connection?.id === attempt) {
        target.connection = { ...target.connection, state: 'cancelled', phase: 'cancelled', finishedAt: Date.now() }
        target.open = false
        listener?.({ type: 'targets_changed' })
      }
    }),
    listKinds: vi.fn(async () => kindsView([podsKind])),
    refreshKinds: vi.fn(async () => {}),
    listScopes: vi.fn(async () => ({ scopes: [{ name: 'default' }, { name: 'web' }] })),
    // One view per kind: v-<kind>; rows from state.rowsByKind, pods default to state.rows.
    openView: vi.fn(async (_p: string, _t: string, q: Query) => ({
      viewId: `v-${q.kind}`,
      kind: state.kinds[q.kind] ?? (q.kind === 'pods' ? podsKind : { id: q.kind, title: q.kind, group: 'Other', scoped: false, columns: [{ id: 'name', title: 'Name', type: 'text' as const }] }),
    })),
    getRows: vi.fn(async (viewId: string) => {
      const kind = viewId.slice(2)
      const rows = state.rowsByKind[kind] ?? (kind === 'pods' ? state.rows : [])
      return { viewId, version: ++state.version, reset: true, upserts: rows, deleted: [], status: state.statusByKind[kind] ?? { state: 'ready' as const } }
    }),
    closeView: vi.fn(async () => {}),
    resyncView: vi.fn(async () => {}),
    touchViews: vi.fn(async () => []),
    getResource: vi.fn(async (ref: Ref) => ({
      ref, health: { state: 'ok' as const }, yaml: `kind: Pod\nmetadata:\n  name: ${ref.name}\n`,
      facts: [{ key: 'kind', value: 'Pod' }, { key: 'Node', value: 'node-1' }],
      relations: [{ type: 'runs-on', ref: { provider: 'kubernetes', target: ref.target, kind: 'nodes', name: 'node-1' } }],
    })),
    getMetrics: vi.fn(async () => ({ status: 'unsupported', values: {} })),
    getTargetState: vi.fn(async () => ({})),
    setTargetState: vi.fn(async () => {}),
    getNavSections: vi.fn(async () => ({})),
    setNavSection: vi.fn(async () => {}),
    getFavoriteKinds: vi.fn(async () => []),
    setKindFavorite: vi.fn(async () => {}),
    moveFavoriteKind: vi.fn(async () => {}),
    recentObjects: vi.fn(async (provider: string, target: string) => state.recents.filter((r) => r.ref.provider === provider && r.ref.target === target)),
    touchRecent: vi.fn(async () => {}),
    logInfo: vi.fn(async () => ({ channels: [{ id: 'app', title: 'app' }], defaultChannel: 'app', aggregate: false, previous: true })),
    openLogStream: vi.fn(async () => ({ streamId: 's1' })),
    execInfo: vi.fn(async (ref: Ref) => ({
      instances: [{ id: ref.uid ?? ref.name, title: ref.name, ready: true, channels: [{ id: 'app', title: 'app', running: true }], defaultChannel: 'app' }],
      defaultInstance: ref.uid ?? ref.name,
    })),
    reopenTerminal: vi.fn(async (terminalId: string) => ({
      terminalId, streamId: 't2',
      target: { provider: 'kubernetes', target: 'ctx', targetTitle: 'ctx', ref: { provider: 'kubernetes', target: 'ctx', kind: 'pods', name: 'p' }, instance: 'p', channel: 'app' },
    })),
    forgetTerminal: vi.fn(async () => {}),
    prepareAction: vi.fn(async () => {
      throw new Error('prepareAction not stubbed')
    }),
    runAction: vi.fn(async () => ({ message: { text: 'requested' } })),
    getEditSource: vi.fn(async () => {
      throw new Error('getEditSource not stubbed')
    }),
    prepareEdit: vi.fn(async () => {
      throw new Error('prepareEdit not stubbed')
    }),
    runEdit: vi.fn(async () => ({ message: 'written' })),
    getValues: vi.fn(async () => {
      throw new Error('getValues not stubbed')
    }),
    revealValue: vi.fn(async () => {
      throw new Error('revealValue not stubbed')
    }),
    prepareValueEdit: vi.fn(async () => {
      throw new Error('prepareValueEdit not stubbed')
    }),
    runValueEdit: vi.fn(async () => ({ message: 'written' })),
    forwardInfo: vi.fn(async () => ({ ports: [] })),
    startForward: vi.fn(async () => { throw new Error('startForward: not set up') }),
    stopForward: vi.fn(async () => {}),
    listForwards: vi.fn(async () => []),
    openTerminal: vi.fn(async (req: TerminalRequest) => ({
      terminalId: 'term-1',
      streamId: 't1',
      target: { provider: req.ref.provider, target: req.ref.target, targetTitle: req.ref.target, ref: req.ref, instance: req.ref.name, channel: 'app' },
    })),
    streamBase: vi.fn(async () => '/streams/tok'),
    agentAccessStatus: vi.fn(async () => structuredClone(state.agentStatus)),
    listAgentGrants: vi.fn(async () => structuredClone(state.agentTargets)),
    saveAgentGrants: vi.fn(async (provider: string, target: string, grants: AgentTarget['grants'], settings: Pick<AgentTarget, 'groups' | 'disabledScopes'> = {}) => {
      state.agentTargets = state.agentTargets.filter((x) => !(x.provider === provider && x.target === target))
      if (grants.length || settings.groups?.length || settings.disabledScopes?.length) state.agentTargets.push({ provider, target, title: target, identity: `https://${target}.example:6443`, grants: structuredClone(grants), ...structuredClone(settings) })
      listener?.({ type: 'agent_grants_changed' })
    }),
    revokeAllAgentGrants: vi.fn(async () => {
      state.agentTargets = []
      listener?.({ type: 'agent_grants_changed' })
    }),
    reconfirmAgentTarget: vi.fn(async (provider: string, target: string, observed: string) => {
      for (const x of state.agentTargets) {
        if (x.provider === provider && x.target === target && x.observed === observed) {
          x.identity = x.observed
          delete x.observed
        }
      }
      listener?.({ type: 'agent_grants_changed' })
    }),
    listAgentPending: vi.fn(async () => structuredClone(state.agentPending)),
    decideAgentPending: vi.fn(async (id: string) => {
      if (!state.agentPending.some((p) => p.id === id)) throw new ApiError('gone', 'no such plan')
      state.agentPending = state.agentPending.filter((p) => p.id !== id)
      listener?.({ type: 'agent_pending_changed' })
    }),
    listAgentAudit: vi.fn(async (f) => {
      const limit = f.limit ?? 500
      return state.audit.filter((e) => (!f.agent || e.agent === f.agent) && (!f.target || e.target === f.target) && (!f.before || e.id < f.before)).slice(0, limit)
    }),
    subscribeEvents: vi.fn((cb: (e: ApiEvent) => void) => {
      listener = cb
      return () => {
        listener = null
      }
    }),
  }
  return {
    client,
    state,
    emit: (e: ApiEvent) => listener?.(e),
  }
}

export const podsKind: KindDescriptor = {
  id: 'pods',
  title: 'Pods',
  group: 'Workloads',
  scoped: true,
  default: true,
  eventsKind: 'events',
  columns: [
    { id: 'name', title: 'Name', type: 'text' },
    { id: 'namespace', title: 'Namespace', type: 'text', scopeColumn: true },
    { id: 'ready', title: 'Ready', type: 'ratio' },
    { id: 'status', title: 'Status', type: 'status' },
    { id: 'restarts', title: 'Restarts', type: 'number' },
    { id: 'age', title: 'Age', type: 'age' },
  ],
}

export const podRow = (name: string, ns: string, status = 'Running', health: Row['health'] = { state: 'ok' }): Row => ({
  id: `uid-${ns}-${name}`,
  ref: { provider: 'kubernetes', target: 'prod', scope: ns, kind: 'pods', name, uid: `uid-${ns}-${name}` },
  cells: [{ text: name }, { text: ns }, { text: '1/1', num: 1 }, { text: status }, { text: '0', num: 0 }, { time: Date.now() - 3_600_000 }],
  health,
})

export const scopeRow = (name: string): Row => ({
  id: `ns-${name}`,
  ref: { provider: 'kubernetes', target: 'prod', kind: 'namespaces', name },
  cells: [{ text: name }],
  health: { state: 'ok' },
})
