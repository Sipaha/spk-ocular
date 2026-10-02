import type { Client } from '../api/client'
import type { ScopeSel } from '../api/types'

// Shared across remounts of a target's Workspace: returning before an older
// write finishes must not let it overwrite a newer checkbox gesture.
const pending = new WeakMap<Client, Map<string, Promise<void>>>()

export function persistNavigation(client: Client, provider: string, target: string, next: { kind: string; scope: ScopeSel }): Promise<void> {
  return persistTargetEntries(client, provider, target, { kind: JSON.stringify(next.kind), scope: JSON.stringify(next.scope) })
}

export function persistTargetEntries(client: Client, provider: string, target: string, entries: Record<string, string>): Promise<void> {
  let byTarget = pending.get(client)
  if (!byTarget) { byTarget = new Map(); pending.set(client, byTarget) }
  const key = JSON.stringify([provider, target])
  const values = Object.entries(entries)
  const write = (byTarget.get(key) ?? Promise.resolve()).catch(() => {}).then(async () => {
    const results = await Promise.allSettled(values.map(([key, value]) => client.setTargetState(provider, target, key, value)))
    const failed = results.find((r) => r.status === 'rejected')
    if (failed?.status === 'rejected') throw failed.reason
  })
  byTarget.set(key, write)
  const settled = () => { if (byTarget.get(key) === write) byTarget.delete(key) }
  void write.then(settled, settled)
  return write
}
