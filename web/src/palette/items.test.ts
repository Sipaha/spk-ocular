import { describe, expect, it } from 'vitest'
import type { KindDescriptor, RecentObject, Row, ScopeSel, Target } from '../api/types'
import { buildItems, buildKeyboardItems, type Sources } from './items'

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
  selectedScope: { mode: 'all' },
  scopeAliases: ['ns', 'namespace'],
  targetAliases: ['ctx', 'context'],
  rows: [row('web-1'), row('db-0')],
  recents: [recent('api')],
  ...over,
})

const labels = (r: ReturnType<typeof buildItems>) => r.items.map((i) => `${i.section}:${i.label}`)
const cursorItem = (r: ReturnType<typeof buildItems>) => r.items.find((i) => i.key === r.cursor)

describe('buildItems — fuzzy', () => {
  it('an empty query lists eligible views, contexts, namespaces and live objects; history adds no rows', () => {
    const r = buildItems('', sources())
    expect(labels(r)).toEqual([
      'kind:Pods', 'kind:Deployments', 'kind:Namespaces',
      'target:prod', 'target:dev',
      'scope:default', 'scope:demo', 'scope:demo-2', 'scope:all',
      'object:web-1', 'object:db-0',
      'command:Agent access',
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

  it('a replaced historical UID cannot introduce an absent object', () => {
    const r = buildItems('web', sources({ recents: [recent('web-1', 'uid-before')] }))
    expect(labels(r)).toEqual(['object:web-1'])
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

  it('without a target only contexts and the app\'s panels are offered', () => {
    const r = buildItems('', sources({ kinds: [], scopes: [], rows: [], recents: [], scopeAliases: [], targetAliases: [] }))
    expect(labels(r)).toEqual(['target:prod', 'target:dev', 'command:Agent access'])
  })

  it('the agent access panel is found by its title and by "agents"', () => {
    for (const q of ['agent acc', 'agents']) {
      const r = buildItems(q, sources({ kinds: [], scopes: [], rows: [], recents: [] }))
      expect(cursorItem(r)?.action).toEqual({ type: 'command', id: 'agents' })
    }
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

  it('own names (alias, id) win over another kind id segment or title; a tie is a list', () => {
    // The provider gives an alias to one kind (described first): a CRD whose
    // plural is "pods" or short name "po" answers to them only by segment.
    const crds = [
      ...kinds,
      kind('other.example/pods', 'Pods', ['pod.other'], { group: 'API groups', subgroup: 'other.example' }),
      kind('x.example/po', 'Pos', [], { group: 'API groups', subgroup: 'x.example' }),
      kind('a.example/things', 'Things', ['things'], { group: 'API groups', subgroup: 'a.example' }),
      kind('b.example/things', 'Things', [], { group: 'API groups', subgroup: 'b.example' }),
      kind('c.example/gizmos', 'Gizmos', [], { group: 'API groups', subgroup: 'c.example' }),
      kind('d.example/gizmos', 'Gizmos', [], { group: 'API groups', subgroup: 'd.example' }),
    ]
    const src = sources({ kinds: crds })
    const pods = buildItems(':pods', src)
    expect(cursorItem(pods)?.action).toEqual({ type: 'kind', kind: 'pods' })
    expect(labels(pods)).toEqual(['kind:Pods', 'kind:Pods']) // the other one is still offered
    expect(pods.items[1]).toMatchObject({ hint: 'API groups · other.example' })
    expect(cursorItem(buildItems(':po', src))?.action).toEqual({ type: 'kind', kind: 'pods' })
    expect(cursorItem(buildItems(':things', src))?.action).toEqual({ type: 'kind', kind: 'a.example/things' })
    expect(cursorItem(buildItems(':other.example/pods', src))?.action).toEqual({ type: 'kind', kind: 'other.example/pods' })
    const tie = buildItems(':gizmos', src)
    expect(tie.cursor).toBeNull()
    expect(tie.items.map((i) => i.action)).toEqual([{ type: 'kind', kind: 'c.example/gizmos' }, { type: 'kind', kind: 'd.example/gizmos' }])
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

describe('buildItems — the provider names its scopes', () => {
  const zones = { singular: 'Zone', plural: 'zones', all: 'All zones' }
  it('scope items say the provider’s words', () => {
    const r = buildItems(':ns *', sources({ scopeWords: zones }))
    expect(cursorItem(r)).toMatchObject({ label: 'All zones', hint: '*' })
    expect(buildItems(':ns demo', sources({ scopeWords: zones })).items[0]).toMatchObject({ label: 'demo', hint: 'Zone' })
    expect(buildItems(':ns nowhere', sources({ scopeWords: zones })).items.at(-1)).toMatchObject({ label: 'nowhere', hint: 'Zone as typed' })
  })
  it('without names: generic words, never Kubernetes ones', () => {
    expect(cursorItem(buildItems(':ns *', sources()))).toMatchObject({ label: 'All scopes' })
    expect(buildItems(':ns demo', sources()).items[0]).toMatchObject({ hint: 'Scope' })
  })
  it('an object is listed by its title', () => {
    const r = buildItems('', sources({ rows: [{ ...row('3f2a9c'), ref: { ...row('3f2a9c').ref, title: 'web-1' } }], recents: [] }))
    expect(labels(r)).toContain('object:web-1')
  })
})

describe('buildItems — object scope selection', () => {
  const scenarios: { scope: ScopeSel | null; visible: string[] }[] = [
    { scope: { mode: 'one', name: 'demo' }, visible: ['demo'] },
    { scope: { mode: 'some', names: ['demo', 'other'] }, visible: ['demo', 'other'] },
    { scope: { mode: 'some', names: [] }, visible: [] },
    { scope: { mode: 'all' }, visible: ['demo', 'other', 'outside'] },
    { scope: { mode: 'none' }, visible: [] },
    { scope: null, visible: [] },
  ]
  it.each(scenarios)('filters eligible live rows for $scope, preserving global rows without adding history', ({ scope, visible }) => {
    const rows = ['demo', 'other', 'outside', ''].map((ns) => row(`match-live-${ns || 'global'}`, ns, ns ? 'pods' : 'nodes'))
    const recents = ['demo', 'other', 'outside', ''].map((ns) => ({ ...recent(`match-recent-${ns || 'global'}`), ref: row(`match-recent-${ns || 'global'}`, ns, ns ? 'unknown.io/things' : 'nodes').ref }))
    for (const query of ['', 'match']) {
      const items = buildItems(query, sources({ rows, recents, selectedScope: scope, scopes: null })).items.filter((i) => i.action.type === 'object')
      expect(items.map((i) => i.label).sort()).toEqual([...visible, 'global'].map((ns) => `match-live-${ns}`).sort())
    }
  })

  it('keeps scope-switching commands and suggestions usable outside the selection', () => {
    const src = sources({ selectedScope: { mode: 'one', name: 'demo' } })
    expect(cursorItem(buildItems(':ns default', src))?.action).toEqual({ type: 'scope', scope: { mode: 'one', name: 'default' } })
    expect(labels(buildItems('default', src))).toContain('scope:default')
  })
})

describe('typed resource queries and history ranking',()=>{
 it('dep model and pod model constrain type and name, retaining selected namespace boundaries',()=>{
  const src=sources({selectedScope:{mode:'one',name:'demo'},rows:[row('ecos-model','demo','apps/deployments'),row('ecos-model-pod','demo','pods'),row('ecos-model-other','outside','apps/deployments')],recents:[recent('ghost-model')]})
  expect(labels(buildItems('dep model',src))).toEqual(['object:ecos-model'])
  expect(labels(buildItems('pod model',src))).toEqual(['object:ecos-model-pod'])
 })
 it('history ranks matching live results but cannot bypass matching or introduce another type or old UID',()=>{
  const a=row('model-a'),b=row('model-b'),r={...recent('model-b'),ref:b.ref}
  const src=sources({rows:[a,b],recents:[r,recent('ghost-model'),recent('model-a','replaced')]})
  expect(labels(buildItems('model',src))).toEqual(['object:model-b','object:model-a'])
  expect(labels(buildItems('missing',src))).toEqual([])
  expect(labels(buildItems('pod model',src))).toEqual(['object:model-b','object:model-a'])
 })
 it('ambiguous type prefixes offer filtered views with no automatic selection',()=>{
  const src=sources({kinds:[...kinds,kind('other/deployments','Other deployments',['deploy'])]})
  const result=buildItems('dep model',src)
  expect(result.cursor).toBeNull()
  expect(result.items.map(item=>item.action)).toEqual([{type:'kind',kind:'apps/deployments',filter:'model'},{type:'kind',kind:'other/deployments',filter:'model'}])
 })
})

describe('keyboard fallback after no original matches', () => {
 it('recovers type/name and name-only searches inside the selected scope, retaining UID', () => {
  const wanted = row('model', 'demo', 'pods')
  const src = sources({ selectedScope: { mode: 'one', name: 'demo' }, rows: [wanted, row('model', 'outside', 'pods')], recents: [recent('model-ghost')] })
  for (const input of ['зщв ьщвуд', 'pod ьщвуд', 'ьщвуд']) {
   const result = buildKeyboardItems(input, src)
   expect(result.query).toBe(input === 'ьщвуд' ? 'model' : 'pod model')
   expect(result.built.items.map(item => item.action)).toEqual([{ type: 'object', ref: wanted.ref }])
  }
 })
 it('keeps real original matches ahead of any corrected results', () => {
  const src = sources({ rows: [row('ьщвуд'), row('model')] })
  const result = buildKeyboardItems('ьщвуд', src)
  expect(result.query).toBe('ьщвуд')
  expect(labels(result.built)).toEqual(['object:ьщвуд'])
 })
 it('waits for the original read and never rewrites commands', () => {
  const src = sources({ rows: [row('model')] })
  expect(buildKeyboardItems('pod ьщвуд', src, false).query).toBe('pod ьщвуд')
  expect(buildKeyboardItems(':pod ьщвуд', src).query).toBe(':pod ьщвуд')
 })
 it('preserves a resolved localized kind and does not choose an ambiguous corrected type', () => {
  const src = sources({ kinds: [kind('pods', 'Поды', ['pod'])], rows: [row('model')] })
  expect(buildKeyboardItems('под ьщвуд', src).query).toBe('под model')
  expect(labels(buildKeyboardItems('под ьщвуд', src).built)).toEqual(['object:model'])
  const ambiguous = sources({ kinds: [kind('pods', 'Pods', ['pod']), kind('other/pods', 'Other Pods', ['pod'])] })
  const result = buildKeyboardItems('зщв ьщвуд', ambiguous)
  expect(result.built.cursor).toBeNull()
  expect(result.built.items.map(item => item.action)).toEqual([{ type: 'kind', kind: 'pods', filter: 'model' }, { type: 'kind', kind: 'other/pods', filter: 'model' }])
 })
})
