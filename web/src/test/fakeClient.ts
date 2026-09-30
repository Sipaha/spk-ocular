import { vi } from 'vitest'
import type { Client } from '../api/client'
import type { ApiEvent, KindDescriptor, KindsView, Query, RecentObject, Ref, Row, Target, TargetsView, TerminalRequest, ViewStatus } from '../api/types'

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

/** An in-memory Client: tests mutate `view` and call `emit`. */
export function fakeClient(targets: Target[]) {
  let listener: ((e: ApiEvent) => void) | null = null
  const state = {
    rows: [] as Row[],
    rowsByKind: {} as Record<string, Row[]>,
    /** Descriptors openView answers with (else pods / a generic one). */
    kinds: {} as Record<string, KindDescriptor>,
    statusByKind: {} as Record<string, ViewStatus>,
    version: 0,
    recents: [] as RecentObject[],
    view: { groups: [{ provider: 'kubernetes', title: 'Kubernetes', targets, problems: [], scopeNames: k8sScopeNames }], selected: null } as TargetsView,
  }
  const client: Client = {
    appInfo: vi.fn(async () => ({ name: 'SPK Ocular', version: 'test', mode: 'browser' as const, language: 'en' as const })),
    listTargets: vi.fn(async () => structuredClone(state.view)),
    selectTarget: vi.fn(async (provider: string, id: string) => {
      const all = state.view.groups.flatMap((g) => g.targets)
      if (!all.some((x) => x.provider === provider && x.id === id)) throw new Error('not_found: no target')
      state.view.selected = { provider, id }
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
    runAction: vi.fn(async () => ({ message: 'requested' })),
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
