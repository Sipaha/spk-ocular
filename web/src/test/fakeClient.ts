import { vi } from 'vitest'
import type { Client } from '../api/client'
import type { ApiEvent, KindDescriptor, Row, Target, TargetsView } from '../api/types'

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

/** An in-memory Client: tests mutate `view` and call `emit`. */
export function fakeClient(targets: Target[]) {
  let listener: ((e: ApiEvent) => void) | null = null
  const state = {
    rows: [] as Row[],
    view: { groups: [{ provider: 'kubernetes', title: 'Kubernetes', targets, problems: [] }], selected: null } as TargetsView,
  }
  const client: Client = {
    appInfo: vi.fn(async () => ({ name: 'SPK Ocular', version: 'test', mode: 'browser' as const, language: 'en' as const })),
    listTargets: vi.fn(async () => structuredClone(state.view)),
    selectTarget: vi.fn(async (provider: string, id: string) => {
      const all = state.view.groups.flatMap((g) => g.targets)
      if (!all.some((x) => x.provider === provider && x.id === id)) throw new Error('not_found: no target')
      state.view.selected = { provider, id }
    }),
    listKinds: vi.fn(async () => [podsKind]),
    listScopes: vi.fn(async () => ({ scopes: [{ name: 'default' }, { name: 'web' }] })),
    openView: vi.fn(async () => ({ viewId: 'v1', kind: podsKind })),
    getRows: vi.fn(async () => ({ viewId: 'v1', version: 1, reset: true, upserts: state.rows, deleted: [], status: { state: 'ready' as const } })),
    closeView: vi.fn(async () => {}),
    touchViews: vi.fn(async () => []),
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
