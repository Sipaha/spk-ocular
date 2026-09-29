import { describe, expect, it } from 'vitest'
import type { KindDescriptor, RecentObject, Row, Target } from '../api/types'
import { buildItems, type Sources } from './items'

const kind = (id: string, title: string, aliases: string[] = [], extra: Partial<KindDescriptor> = {}): KindDescriptor => ({ id, title, group: 'G', scoped: true, columns: [], aliases, ...extra })
const kinds = [
  kind('pods', 'Pods', ['po', 'pod']),
  kind('apps/deployments', 'Deployments', ['deploy', 'deployment']),
  kind('namespaces', 'Namespaces', ['ns', 'namespace'], { scoped: false }),
  kind('apps/replicasets', 'ReplicaSets', ['rs'], { hidden: true }),
]
const target = (id: string, title = id, subtitle?: string): Target => ({ provider: 'kubernetes', id, title, subtitle })
const row = (name: string, ns = 'demo', kindId = 'pods'): Row => ({
  id: `uid-${name}`,
  ref: { provider: 'kubernetes', target: 'prod', scope: ns, kind: kindId, name, uid: `uid-${name}` },
  cells: [{ text: name }],
  health: { state: 'ok' },
})
const recent = (name: string, uid = `old-${name}`): RecentObject => ({ ref: { provider: 'kubernetes', target: 'prod', scope: 'demo', kind: 'pods', name, uid }, title: name, openedAt: 1 })

const sources = (over: Partial<Sources> = {}): Sources => ({
  kinds,
  targets: [
    { target: target('kubeconfig:prod', 'prod', 'prod-cluster'), groupTitle: 'Kubernetes' },
    { target: target('kubeconfig:dev', 'dev'), groupTitle: 'Kubernetes' },
  ],
  scopes: ['default', 'demo', 'demo-2', 'all'],
  scopeAliases: ['ns', 'namespace'],
  targetAliases: ['ctx', 'context'],
  rows: [row('web-1'), row('db-0')],
  recents: [recent('api')],
  ...over,
})

const labels = (r: ReturnType<typeof buildItems>) => r.items.map((i) => `${i.section}:${i.label}`)
const cursorItem = (r: ReturnType<typeof buildItems>) => r.items.find((i) => i.key === r.cursor)

describe('buildItems — fuzzy', () => {
  it('an empty query lists recent objects, views, contexts, namespaces, then the table; hidden views are not offered', () => {
    const r = buildItems('', sources())
    expect(labels(r)).toEqual([
      'recent:api',
      'kind:Pods', 'kind:Deployments', 'kind:Namespaces',
      'target:prod', 'target:dev',
      'scope:default', 'scope:demo', 'scope:demo-2', 'scope:all',
      'object:web-1', 'object:db-0',
    ])
    expect(r.cursor).toBe(r.items[0].key)
  })

  it('ranks across sources by the match and keeps the first as the cursor', () => {
    const r = buildItems('dep', sources())
    expect(labels(r)[0]).toBe('kind:Deployments')
    expect(cursorItem(r)?.action).toEqual({ type: 'kind', kind: 'apps/deployments' })
  })

  it('a table row and a recent entry of the same object are one item (the live row)', () => {
    const r = buildItems('web', sources({ recents: [recent('web-1', 'uid-web-1')] }))
    expect(labels(r)).toEqual(['object:web-1'])
  })

  it('a recent entry of a replaced object (another UID) stays its own item', () => {
    const r = buildItems('web', sources({ recents: [recent('web-1', 'uid-before')] }))
    expect(labels(r)).toEqual(['object:web-1', 'recent:web-1'])
  })

  it('contexts with the same title are told apart by their subtitle', () => {
    const r = buildItems('prod', sources({ targets: [{ target: target('kubeconfig:prod', 'prod', 'cluster-a'), groupTitle: 'Kubernetes' }, { target: target('file:/x:prod', 'prod', 'x.yaml'), groupTitle: 'Kubernetes' }] }))
    const t = r.items.filter((i) => i.section === 'target')
    expect(t.map((i) => i.hint)).toEqual(['Kubernetes · cluster-a', 'Kubernetes · x.yaml'])
    expect(t.map((i) => i.action)).toEqual([{ type: 'target', ref: { provider: 'kubernetes', id: 'kubeconfig:prod' } }, { type: 'target', ref: { provider: 'kubernetes', id: 'file:/x:prod' } }])
  })

  it('shows at most 200 items', () => {
    const rows = Array.from({ length: 300 }, (_, i) => row(`p-${i}`))
    expect(buildItems('p-', sources({ rows })).items).toHaveLength(200)
  })

  it('without a target only contexts are offered', () => {
    const r = buildItems('', sources({ kinds: [], scopes: [], rows: [], recents: [], scopeAliases: [], targetAliases: [] }))
    expect(labels(r)).toEqual(['target:prod', 'target:dev'])
  })
})

describe('buildItems — commands', () => {
  it(':ns <name> switches the namespace when the name is exact', () => {
    const r = buildItems(':ns demo', sources())
    expect(cursorItem(r)?.action).toEqual({ type: 'scope', scope: { mode: 'one', name: 'demo' } })
    expect(labels(r)).toContain('scope:demo-2') // listed, never chosen silently
  })

  it(':ns * is every namespace; :ns all is the namespace named "all"', () => {
    expect(cursorItem(buildItems(':ns *', sources()))?.action).toEqual({ type: 'scope', scope: { mode: 'all' } })
    expect(cursorItem(buildItems(':ns all', sources()))?.action).toEqual({ type: 'scope', scope: { mode: 'one', name: 'all' } })
  })

  it(':ns without a name opens the Namespaces view (a kind alias)', () => {
    expect(cursorItem(buildItems(':ns', sources()))?.action).toEqual({ type: 'kind', kind: 'namespaces' })
  })

  it('a namespace not in the list is offered as typed, but not chosen by Enter', () => {
    const r = buildItems(':ns dem', sources())
    expect(r.cursor).toBeNull()
    expect(r.items.map((i) => i.action)).toContainEqual({ type: 'scope', scope: { mode: 'one', name: 'dem' } })
    expect(labels(r)).toEqual(expect.arrayContaining(['scope:demo', 'scope:demo-2']))
  })

  it('when namespaces cannot be listed the typed one is the choice', () => {
    const r = buildItems(':ns secret-ns', sources({ scopes: null }))
    expect(cursorItem(r)?.action).toEqual({ type: 'scope', scope: { mode: 'one', name: 'secret-ns' } })
  })

  it(':ctx <title> switches the context when exactly one has that title', () => {
    const r = buildItems(':ctx dev', sources())
    expect(cursorItem(r)?.action).toEqual({ type: 'target', ref: { provider: 'kubernetes', id: 'kubeconfig:dev' } })
  })

  it(':ctx with an ambiguous title lists the candidates without choosing', () => {
    const r = buildItems(':ctx prod', sources({ targets: [{ target: target('kubeconfig:prod', 'prod', 'a'), groupTitle: 'Kubernetes' }, { target: target('file:/x:prod', 'prod', 'x.yaml'), groupTitle: 'Kubernetes' }] }))
    expect(r.cursor).toBeNull()
    expect(labels(r)).toEqual(['target:prod', 'target:prod'])
  })

  it(':ctx with a partial title lists matches without choosing', () => {
    const r = buildItems(':ctx pr', sources())
    expect(r.cursor).toBeNull()
    expect(labels(r)).toEqual(['target:prod'])
  })

  it(':<kind alias> opens the view; with text, the view filtered by it', () => {
    expect(cursorItem(buildItems(':po', sources()))?.action).toEqual({ type: 'kind', kind: 'pods' })
    expect(cursorItem(buildItems(':deploy web', sources()))?.action).toEqual({ type: 'kind', kind: 'apps/deployments', filter: 'web' })
    expect(cursorItem(buildItems(':Deployments', sources()))?.action).toEqual({ type: 'kind', kind: 'apps/deployments' })
    expect(cursorItem(buildItems(':rs', sources()))?.action).toEqual({ type: 'kind', kind: 'apps/replicasets' })
  })

  it('an incomplete command lists the views it could be, choosing none', () => {
    const r = buildItems(':de', sources())
    expect(r.cursor).toBeNull()
    expect(labels(r)).toEqual(['kind:Deployments'])
    expect(labels(buildItems(':', sources()))).toEqual(['kind:Pods', 'kind:Deployments', 'kind:Namespaces'])
  })

  it('an unknown command says so', () => {
    const r = buildItems(':zzz', sources())
    expect(r.items).toEqual([])
    expect(r.unknown).toBe(true)
  })
})
