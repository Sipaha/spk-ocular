import type { ScopeSel } from './api/types'

export const selectedScopes = (scope: ScopeSel): string[] => scope.mode === 'one' ? [scope.name!] : scope.mode === 'some' ? scope.names ?? [] : []

/** Explicit empty selection stays empty; only All widens the query. */
export function scopeSet(names: string[]): ScopeSel {
  const sorted = [...new Set(names)].sort()
  return sorted.length === 1 ? { mode: 'one', name: sorted[0] } : { mode: 'some', names: sorted }
}

export function parseScope(value: unknown): ScopeSel | null {
  if (!value || typeof value !== 'object') return null
  const s = value as ScopeSel
  if (s.mode === 'all' || s.mode === 'none') return { mode: s.mode }
  if (s.mode === 'one' && typeof s.name === 'string' && s.name) return { mode: 'one', name: s.name }
  if (s.mode === 'some' && Array.isArray(s.names) && s.names.every((n) => typeof n === 'string' && n)) return scopeSet(s.names)
  return null
}
