import { describe, expect, it } from 'vitest'
import type { AgentGrant, KindDescriptor } from '../api/types'
import { addScope, fromGrants, problems, sameGrants, setVerb, toGrants, verbOptions, warnings } from './model'

const kind = (id: string, extra: Partial<KindDescriptor> = {}): KindDescriptor => ({ id, title: id, group: 'g', columns: [], scoped: true, ...extra })

const kinds: KindDescriptor[] = [
  kind('pods', { logs: true, editable: true, actions: [{ id: 'delete', title: 'Delete', destructive: true }] }),
  kind('apps/deployments', { logs: true, editable: true, actions: [{ id: 'restart', title: 'Restart' }, { id: 'scale', title: 'Scale' }, { id: 'delete', title: 'Delete', destructive: true }] }),
  kind('secrets', { editable: true, sensitive: true }),
  kind('nodes', { scoped: false, actions: [{ id: 'drain', title: 'Drain', destructive: true }] }),
  kind('namespaces', { scoped: false }),
]

const optionsOf = (mode: 'one' | 'all' | 'cluster') => verbOptions(kinds, mode)

describe('grant rows ↔ scopes', () => {
  it('round-trips a target\'s rows whatever their order', () => {
    const rows: AgentGrant[] = [
      { scope: { mode: 'cluster' }, verb: 'read', kinds: null },
      { scope: { mode: 'one', name: 'web' }, verb: 'action:delete', kinds: ['pods'], noConfirm: true },
      { scope: { mode: 'one', name: 'web' }, verb: 'read', kinds: null },
      { scope: { mode: 'all' }, verb: 'logs', kinds: ['pods', 'apps/deployments'] },
    ]
    const scopes = fromGrants(rows)
    expect(scopes.map((s) => s.scope)).toEqual([{ mode: 'one', name: 'web' }, { mode: 'all' }, { mode: 'cluster' }])
    expect(toGrants(scopes)).toEqual([
      { scope: { mode: 'one', name: 'web' }, verb: 'read', kinds: null },
      { scope: { mode: 'one', name: 'web' }, verb: 'action:delete', kinds: ['pods'], noConfirm: true },
      { scope: { mode: 'all' }, verb: 'logs', kinds: ['apps/deployments', 'pods'] },
      { scope: { mode: 'cluster' }, verb: 'read', kinds: null },
    ])
    expect(sameGrants(rows, [...rows].reverse())).toBe(true)
    expect(sameGrants(rows, rows.slice(1))).toBe(false)
  })

  it('a scope without verbs grants nothing; a new scope starts with read', () => {
    let s = addScope([], { mode: 'one', name: 'web' })
    expect(toGrants(s)).toEqual([{ scope: { mode: 'one', name: 'web' }, verb: 'read', kinds: null }])
    s = setVerb(s, 'one:web', 'read', null)
    expect(toGrants(s)).toEqual([])
    expect(addScope(addScope([], { mode: 'all' }), { mode: 'all' })).toHaveLength(1)
  })
})

describe('verbOptions', () => {
  it('the cluster scope only reads the kinds outside scopes', () => {
    expect(optionsOf('cluster')).toEqual([{ verb: 'read', destructive: false, kinds: [kinds[3], kinds[4]] }])
  })
  it('a namespace offers what applies to its kinds; an action is destructive if any kind\'s is', () => {
    const o = optionsOf('one')
    expect(o.map((x) => x.verb)).toEqual(['read', 'logs', 'edit', 'action:delete', 'action:restart', 'action:scale'])
    expect(o.find((x) => x.verb === 'logs')?.kinds.map((k) => k.id)).toEqual(['pods', 'apps/deployments'])
    expect(o.find((x) => x.verb === 'action:delete')).toMatchObject({ destructive: true, kinds: [kinds[0], kinds[1]] })
    expect(o.find((x) => x.verb === 'action:scale')?.destructive).toBe(false)
  })
})

describe('problems', () => {
  it('a destructive action for all kinds cannot be saved; named kinds can', () => {
    const all = fromGrants([{ scope: { mode: 'one', name: 'web' }, verb: 'action:delete', kinds: null }])
    expect(problems(all, optionsOf)).toEqual([{ scope: 'one:web', verb: 'action:delete', key: 'agents.problem.destructiveAll' }])
    const named = setVerb(all, 'one:web', 'action:delete', { kinds: ['pods'], noConfirm: false })
    expect(problems(named, optionsOf)).toEqual([])
    // Scale is not destructive by itself (scale to 0 is, by plan): all kinds is allowed.
    expect(problems(fromGrants([{ scope: { mode: 'all' }, verb: 'action:scale', kinds: null }]), optionsOf)).toEqual([])
  })
  it('an empty kind list is not "all kinds"', () => {
    const s = fromGrants([{ scope: { mode: 'one', name: 'web' }, verb: 'logs', kinds: [] }])
    expect(problems(s, optionsOf)).toEqual([{ scope: 'one:web', verb: 'logs', key: 'agents.problem.noKinds' }])
  })
})

describe('warnings', () => {
  const w = (provider: string, rows: AgentGrant[]) => warnings(provider, fromGrants(rows), kinds)
  it('says what the grants mean', () => {
    expect(w('kubernetes', [])).toEqual([])
    expect(w('compose', [{ scope: { mode: 'one', name: 'shop' }, verb: 'read', kinds: null }])).toEqual(['dockerRoot'])
    expect(w('kubernetes', [{ scope: { mode: 'all' }, verb: 'read', kinds: null }])).toEqual(['allScopes'])
    expect(w('kubernetes', [{ scope: { mode: 'cluster' }, verb: 'read', kinds: null }])).toEqual(['cluster'])
    expect(w('kubernetes', [{ scope: { mode: 'one', name: 'web' }, verb: 'edit', kinds: null }])).toEqual(['operators'])
    expect(
      w('kubernetes', [
        { scope: { mode: 'one', name: 'web' }, verb: 'edit', kinds: ['secrets'] },
        { scope: { mode: 'one', name: 'web' }, verb: 'logs', kinds: null },
        { scope: { mode: 'one', name: 'web' }, verb: 'action:delete', kinds: ['pods'], noConfirm: true },
      ]),
    ).toEqual(['editLogs', 'operators', 'sensitiveByName', 'noConfirm'])
  })
})
