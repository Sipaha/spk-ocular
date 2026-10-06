import type { AgentGrantSettings, AgentScope, AgentTarget } from '../api/types'
import { t } from '../i18n'
import { fromGrants, scopeKey, toGrants, type ScopeGrants, type VerbSel } from './model'

export interface AccessGroup {
  id: string
  name: string
  disabled: boolean
  verbs: Record<string, VerbSel>
}

export interface AccessScope {
  scope: AgentScope
  disabled: boolean
  groups: AccessGroup[]
}

export function newGroup(index: number, read = false): AccessGroup {
  return { id: crypto.randomUUID(), name: t('agents.groupNameDefault', { n: index }), disabled: false, verbs: read ? { read: { kinds: null, noConfirm: false } } : {} }
}

export function scopesFromTarget(saved: AgentTarget | null): AccessScope[] {
  const scopes = new Map<string, AccessScope>()
  const ensure = (scope: AgentScope) => {
    const key = scopeKey(scope)
    if (!scopes.has(key)) scopes.set(key, { scope, disabled: false, groups: [] })
    return scopes.get(key)!
  }
  // Preserve repeated legacy verbs as separate groups, including noConfirm.
  // Folding them into one row could silently narrow or widen permissions.
  for (const grant of saved?.grants ?? []) {
    const scope = ensure(grant.scope)
    let group = scope.groups.find((g) => !g.verbs[grant.verb])
    if (!group) {
      group = { id: `legacy:${scopeKey(scope.scope)}:${scope.groups.length}`, name: t('agents.groupNameDefault', { n: scope.groups.length + 1 }), disabled: false, verbs: {} }
      scope.groups.push(group)
    }
    group.verbs[grant.verb] = { kinds: grant.kinds ? [...grant.kinds] : null, noConfirm: !!grant.noConfirm }
  }
  for (const group of saved?.groups ?? []) {
    const scope = ensure(group.scope)
    const verbs = fromGrants(group.grants ?? [])[0]?.verbs ?? {}
    scope.groups.push({ id: group.id, name: group.name, disabled: !!group.disabled, verbs })
  }
  for (const scope of saved?.disabledScopes ?? []) ensure(scope).disabled = true
  return [...scopes.values()]
}

export function settingsOf(scopes: AccessScope[]): AgentGrantSettings {
  return {
    groups: scopes.flatMap((s) => s.groups.map((g) => ({
      id: g.id, name: g.name.trim(), scope: s.scope, disabled: g.disabled,
      grants: toGrants([{ scope: s.scope, verbs: g.verbs }]),
    }))),
    disabledScopes: scopes.filter((s) => s.disabled).map((s) => s.scope),
  }
}

export function permissionScopes(scopes: AccessScope[], activeOnly = false): ScopeGrants[] {
  const allOff = scopes.some((s) => s.scope.mode === 'all' && s.disabled)
  return scopes.flatMap((s) => activeOnly && (s.disabled || allOff && s.scope.mode === 'one') ? [] :
    s.groups.filter((g) => !activeOnly || !g.disabled).map((g) => ({ scope: s.scope, verbs: g.verbs })))
}

export function sameSettings(a: AccessScope[], b: AccessScope[]): boolean {
  const normalize = (s: AccessScope[]) => {
    const value = settingsOf(s)
    value.groups?.sort((x, y) => x.id.localeCompare(y.id))
    value.disabledScopes?.sort((x, y) => scopeKey(x).localeCompare(scopeKey(y)))
    return JSON.stringify(value)
  }
  return normalize(a) === normalize(b)
}
