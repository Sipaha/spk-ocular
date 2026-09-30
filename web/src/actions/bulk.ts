import type { ActionDescriptor } from '../api/types'

/** An action that may run on several objects at once: no value of its own per object. */
const bulkable = (a: ActionDescriptor) => !a.text && a.param?.kind !== 'choice' && !a.noAgents && !a.single

/**
 * The actions every marked object's kind offers (by id, in the first kind's
 * order) that may run on several at once. A count is shared: its range is
 * what every kind accepts; destructive when any kind says so.
 */
export function bulkActions(kinds: ActionDescriptor[][]): ActionDescriptor[] {
  if (!kinds.length) return []
  const out: ActionDescriptor[] = []
  for (const first of kinds[0]) {
    if (!bulkable(first)) continue
    let merged: ActionDescriptor | null = { ...first }
    for (const other of kinds.slice(1)) {
      const a = other.find((x) => x.id === first.id)
      if (!a || !bulkable(a) || !!a.param !== !!merged.param) {
        merged = null
        break
      }
      merged.destructive = merged.destructive || a.destructive
      if (merged.param && a.param) merged.param = { ...merged.param, min: Math.max(merged.param.min, a.param.min), max: Math.min(merged.param.max, a.param.max) }
    }
    if (merged && (!merged.param || merged.param.min <= merged.param.max)) out.push(merged)
  }
  return out
}

/**
 * Runs fn on the items, at most limit at once, in order; stopped() true
 * starts no more. Resolves, once the started ones end, with the items never
 * started.
 */
export async function pool<T>(items: T[], limit: number, fn: (item: T) => Promise<void>, stopped: () => boolean = () => false): Promise<T[]> {
  let next = 0
  const worker = async () => {
    while (next < items.length && !stopped()) await fn(items[next++])
  }
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, worker))
  return items.slice(next)
}
