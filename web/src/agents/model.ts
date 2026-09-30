// The grant editor's model (P14): a target's grant rows (internal/agentgrant
// Grant) as scopes with their verbs, and back; what cannot be saved and
// why; the honest warnings of the spec. Pure: no client, no React.
import type { ActionDescriptor, AgentGrant, AgentScope, KindDescriptor } from '../api/types'

export const VERB_READ = 'read'
export const VERB_LOGS = 'logs'
export const VERB_EDIT = 'edit'
export const ACTION_PREFIX = 'action:'

/** A verb's selection in one scope: kinds null = every kind. */
export interface VerbSel {
  kinds: string[] | null
  noConfirm: boolean
}

/** One scope of a target with the verbs granted in it. */
export interface ScopeGrants {
  scope: AgentScope
  verbs: Record<string, VerbSel>
}

export const scopeKey = (s: AgentScope) => (s.mode === 'one' ? `one:${s.name ?? ''}` : s.mode)

const scopeRank = (s: AgentScope) => (s.mode === 'one' ? 0 : s.mode === 'all' ? 1 : 2)

/** Named scopes by name, then "all", then "cluster". */
export function sortScopes(list: ScopeGrants[]): ScopeGrants[] {
  return [...list].sort((a, b) => scopeRank(a.scope) - scopeRank(b.scope) || (a.scope.name ?? '').localeCompare(b.scope.name ?? ''))
}

/** Grant rows as the editor shows them: one entry per scope. */
export function fromGrants(grants: AgentGrant[]): ScopeGrants[] {
  const by = new Map<string, ScopeGrants>()
  for (const g of grants) {
    const k = scopeKey(g.scope)
    let s = by.get(k)
    if (!s) {
      s = { scope: g.scope.mode === 'one' ? { mode: 'one', name: g.scope.name } : { mode: g.scope.mode }, verbs: {} }
      by.set(k, s)
    }
    s.verbs[g.verb] = { kinds: g.kinds ? [...g.kinds] : null, noConfirm: !!g.noConfirm }
  }
  return sortScopes([...by.values()])
}

/** The editor's scopes as grant rows (scopes without verbs grant nothing). */
export function toGrants(scopes: ScopeGrants[]): AgentGrant[] {
  const out: AgentGrant[] = []
  for (const s of sortScopes(scopes)) {
    for (const verb of Object.keys(s.verbs).sort(verbOrder)) {
      const v = s.verbs[verb]
      const g: AgentGrant = { scope: s.scope.mode === 'one' ? { mode: 'one', name: s.scope.name } : { mode: s.scope.mode }, verb, kinds: v.kinds ? [...v.kinds].sort() : null }
      // Only a grant of kinds named skips the confirmation (all kinds never do).
      if (v.noConfirm && v.kinds) g.noConfirm = true
      out.push(g)
    }
  }
  return out
}

const verbRank = (v: string) => (v === VERB_READ ? 0 : v === VERB_LOGS ? 1 : v === VERB_EDIT ? 2 : 3)
export const verbOrder = (a: string, b: string) => verbRank(a) - verbRank(b) || a.localeCompare(b)

/** Equal grants whatever their order. */
export function sameGrants(a: AgentGrant[], b: AgentGrant[]): boolean {
  return JSON.stringify(toGrants(fromGrants(a))) === JSON.stringify(toGrants(fromGrants(b)))
}

export const actionVerb = (id: string) => ACTION_PREFIX + id
export const isAction = (verb: string) => verb.startsWith(ACTION_PREFIX)
export const actionId = (verb: string) => verb.slice(ACTION_PREFIX.length)

/** A verb the editor offers in a scope, with the kinds it applies to. */
export interface VerbOption {
  verb: string
  /** the action (for its label), when the verb is one */
  action?: ActionDescriptor
  /** some kind's action is destructive: granted only by name */
  destructive: boolean
  kinds: KindDescriptor[]
}

/**
 * The verbs of a scope and the kinds each applies to (agentapi's ListKinds
 * shows a verb for a kind only if it applies): the cluster scope reads the
 * kinds outside scopes; the others read, and change, the kinds in them.
 */
export function verbOptions(kinds: KindDescriptor[], mode: AgentScope['mode']): VerbOption[] {
  if (mode === 'cluster') return [{ verb: VERB_READ, destructive: false, kinds: kinds.filter((k) => !k.scoped) }]
  const scoped = kinds.filter((k) => k.scoped)
  const out: VerbOption[] = [
    { verb: VERB_READ, destructive: false, kinds: scoped },
    { verb: VERB_LOGS, destructive: false, kinds: scoped.filter((k) => k.logs) },
    { verb: VERB_EDIT, destructive: false, kinds: scoped.filter((k) => k.editable) },
  ]
  const actions = new Map<string, VerbOption>()
  for (const k of scoped) {
    for (const a of k.actions ?? []) {
      if (a.noAgents) continue // exec-level: never an agent's
      let o = actions.get(a.id)
      if (!o) {
        o = { verb: actionVerb(a.id), action: a, destructive: false, kinds: [] }
        actions.set(a.id, o)
      }
      o.kinds.push(k)
      if (a.destructive) o.destructive = true
    }
  }
  return [...out.filter((o) => o.kinds.length), ...[...actions.values()].sort((a, b) => a.verb.localeCompare(b.verb))]
}

/** Whether a verb can skip the confirmation (it may make destructive plans). */
export const confirmable = (verb: string) => verb === VERB_EDIT || isAction(verb)

/** Why the editor's grants cannot be saved (message key and values). */
export interface Problem {
  scope: string
  verb: string
  key: 'agents.problem.destructiveAll' | 'agents.problem.noKinds' | 'agents.problem.noName'
}

/**
 * What stops a save: a destructive action granted for "all kinds" (such
 * plans are allowed only for kinds named — the grant would never run
 * them), a kind list left empty, a namespace without a name.
 */
export function problems(scopes: ScopeGrants[], options: (mode: AgentScope['mode']) => VerbOption[]): Problem[] {
  const out: Problem[] = []
  for (const s of scopes) {
    const key = scopeKey(s.scope)
    if (s.scope.mode === 'one' && !s.scope.name?.trim()) out.push({ scope: key, verb: '', key: 'agents.problem.noName' })
    const opts = options(s.scope.mode)
    for (const [verb, v] of Object.entries(s.verbs)) {
      if (v.kinds && v.kinds.length === 0) out.push({ scope: key, verb, key: 'agents.problem.noKinds' })
      else if (!v.kinds && opts.find((o) => o.verb === verb)?.destructive) out.push({ scope: key, verb, key: 'agents.problem.destructiveAll' })
    }
  }
  return out
}

export type Warning = 'dockerRoot' | 'allScopes' | 'cluster' | 'editLogs' | 'operators' | 'noConfirm' | 'sensitiveByName' | 'undo' | 'forceDelete'

/** The spec's honest warnings for what the editor grants. */
export function warnings(provider: string, scopes: ScopeGrants[], kinds: KindDescriptor[]): Warning[] {
  const has = (p: (s: ScopeGrants) => boolean) => scopes.some(p)
  const any = scopes.some((s) => Object.keys(s.verbs).length > 0)
  const out: Warning[] = []
  if (provider === 'compose' && any) out.push('dockerRoot')
  if (has((s) => s.scope.mode === 'all' && Object.keys(s.verbs).length > 0)) out.push('allScopes')
  if (has((s) => s.scope.mode === 'cluster' && Object.keys(s.verbs).length > 0)) out.push('cluster')
  if (has((s) => !!s.verbs[VERB_EDIT] && !!s.verbs[VERB_LOGS])) out.push('editLogs')
  if (has((s) => !!s.verbs[VERB_EDIT])) out.push('operators')
  const sensitive = new Set(kinds.filter((k) => k.sensitive).map((k) => k.id))
  if (has((s) => !!s.verbs[VERB_EDIT]?.kinds?.some((k) => sensitive.has(k)))) out.push('sensitiveByName')
  if (has((s) => !!s.verbs['action:undo'])) out.push('undo')
  if (has((s) => !!s.verbs['action:forceDelete'])) out.push('forceDelete')
  if (has((s) => Object.values(s.verbs).some((v) => v.noConfirm))) out.push('noConfirm')
  return out
}

/** Sets or clears one verb of one scope (a new list, the rest shared). */
export function setVerb(scopes: ScopeGrants[], key: string, verb: string, sel: VerbSel | null): ScopeGrants[] {
  return scopes.map((s) => {
    if (scopeKey(s.scope) !== key) return s
    const verbs = { ...s.verbs }
    if (sel) verbs[verb] = sel
    else delete verbs[verb]
    return { ...s, verbs }
  })
}

/** Adds a scope (with read) unless it is there. */
export function addScope(scopes: ScopeGrants[], scope: AgentScope): ScopeGrants[] {
  if (scopes.some((s) => scopeKey(s.scope) === scopeKey(scope))) return scopes
  return sortScopes([...scopes, { scope, verbs: { [VERB_READ]: { kinds: null, noConfirm: false } } }])
}

export const removeScope = (scopes: ScopeGrants[], key: string) => scopes.filter((s) => scopeKey(s.scope) !== key)
