import type { Client } from '../api/client'
import type { ExecInfo, LogInfo, Ref } from '../api/types'

type Info = ExecInfo | LogInfo
interface Entry { promise: Promise<Info>; expires: number }
const caches = new WeakMap<Client, Map<string, Entry>>()
const TTL = 5000
const keyOf = (type: string, ref: Ref) => JSON.stringify([type, ref.provider, ref.target, ref.scope, ref.kind, ref.name, ref.uid])

// Short-lived metadata only. Execution and reads still validate the pinned UID.
function load<T extends Info>(client: Client, type: string, ref: Ref, request: () => Promise<T>): Promise<T> {
  let cache = caches.get(client)
  if (!cache) { cache = new Map(); caches.set(client, cache) }
  const key = keyOf(type, ref)
  const existing = cache.get(key)
  if (existing && existing.expires > Date.now()) return existing.promise as Promise<T>
  for (const [oldKey, entry] of cache) if (entry.expires < Date.now()) cache.delete(oldKey)
  const entry: Entry = { promise: request(), expires: Number.POSITIVE_INFINITY }
  cache.set(key, entry)
  void entry.promise.then(() => { entry.expires = Date.now() + TTL }, () => { if (cache.get(key) === entry) cache.delete(key) })
  return entry.promise as Promise<T>
}
export const getExecInfo = (client: Client, ref: Ref) => load(client, 'exec', ref, () => client.execInfo(ref))
export const getLogInfo = (client: Client, ref: Ref) => load(client, 'logs', ref, () => client.logInfo(ref))
export function prefetchToolInfo(client: Client, ref: Ref, exec: boolean, logs: boolean) {
  if (exec) void getExecInfo(client, ref).catch(() => {})
  if (logs) void getLogInfo(client, ref).catch(() => {})
}
