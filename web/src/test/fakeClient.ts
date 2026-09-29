import { vi } from 'vitest'
import type { Client } from '../api/client'
import type { ApiEvent, Target, TargetsView } from '../api/types'

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
