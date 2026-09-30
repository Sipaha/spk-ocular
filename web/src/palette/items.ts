// What the palette offers for an input: fuzzy over the current target's
// views, all targets, scopes, the current table's rows and recent objects,
// or a command (":po", ":deploy web", ":ns demo", ":ns *", ":ctx prod").
// Aliases come from the providers (KindDescriptor.aliases, TargetGroup
// .aliases): nothing here knows Kubernetes.
import type { KindDescriptor, RecentObject, Ref, Row, ScopeSel, Target, TargetRef } from '../api/types'
import { t } from '../i18n'
import { refTitle } from '../refs'
import { scopeWords, type ScopeWords } from '../scopeNames'
import { fuzzyScore } from './score'

export type PaletteAction =
  | { type: 'kind'; kind: string; filter?: string }
  | { type: 'target'; ref: TargetRef }
  | { type: 'scope'; scope: ScopeSel }
  | { type: 'object'; ref: Ref }

export type Section = 'recent' | 'kind' | 'target' | 'scope' | 'object'

export interface PaletteItem {
  /** Stable across live updates: the cursor follows it. */
  key: string
  section: Section
  label: string
  hint?: string
  action: PaletteAction
}

export interface Sources {
  /** The current target's kinds ([] without a target). */
  kinds: KindDescriptor[]
  targets: { target: Target; groupTitle: string }[]
  /** Scope names; null: they cannot be listed (a typed one is taken as is). */
  scopes: string[] | null
  scopeAliases: string[]
  /** What the current target's provider calls its scopes (absent: generic words). */
  scopeWords?: ScopeWords
  targetAliases: string[]
  /** The current table's rows. */
  rows: Row[]
  recents: RecentObject[]
}

export interface Built {
  items: PaletteItem[]
  /** The item Enter runs; null: nothing is chosen (a list to pick from). */
  cursor: string | null
  /** A command nothing answers to. */
  unknown?: boolean
}

export const MAX_ITEMS = 200

const refKey = (r: Ref) => [r.provider, r.target, r.kind, r.scope ?? '', r.name, r.uid ?? ''].join('\u0000')

function kindTitle(kinds: KindDescriptor[], id: string) {
  return kinds.find((k) => k.id === id)?.title ?? id
}

const objectHint = (kinds: KindDescriptor[], r: Ref) => (r.scope ? `${kindTitle(kinds, r.kind)} · ${r.scope}` : kindTitle(kinds, r.kind))

const kindItem = (k: KindDescriptor, filter?: string): PaletteItem => ({
  key: `kind:${k.id}${filter ? `:${filter}` : ''}`,
  section: 'kind',
  label: k.title,
  hint: filter ? t('palette.filtered', { text: filter }) : k.subgroup ? `${k.group} · ${k.subgroup}` : k.group,
  action: filter ? { type: 'kind', kind: k.id, filter } : { type: 'kind', kind: k.id },
})

const targetItem = ({ target, groupTitle }: Sources['targets'][number]): PaletteItem => ({
  key: `target:${target.provider}:${target.id}`,
  section: 'target',
  label: target.title,
  hint: target.subtitle ? `${groupTitle} · ${target.subtitle}` : groupTitle,
  action: { type: 'target', ref: { provider: target.provider, id: target.id } },
})

const wordsOf = (src: Sources) => src.scopeWords ?? scopeWords(undefined)

const scopeItem = (src: Sources, name: string, typed = false): PaletteItem => ({
  key: `scope:${typed ? 'typed:' : ''}${name}`,
  section: 'scope',
  label: name,
  hint: typed ? t('palette.typedScope', { scope: wordsOf(src).singular }) : wordsOf(src).singular,
  action: { type: 'scope', scope: { mode: 'one', name } },
})

const allScopesItem = (src: Sources): PaletteItem => ({ key: 'scope:*', section: 'scope', label: wordsOf(src).all, hint: '*', action: { type: 'scope', scope: { mode: 'all' } } })

const objectItem = (kinds: KindDescriptor[], ref: Ref, label: string, section: 'object' | 'recent'): PaletteItem => ({
  key: `${section}:${refKey(ref)}`,
  section,
  label,
  hint: section === 'recent' ? `${t('palette.recent')} · ${objectHint(kinds, ref)}` : objectHint(kinds, ref),
  action: { type: 'object', ref },
})

/** The live rows, then recent entries of other objects (one item per object). */
function objects(src: Sources): { rows: PaletteItem[]; recents: PaletteItem[] } {
  const seen = new Set<string>()
  const rows: PaletteItem[] = []
  for (const r of src.rows) {
    const k = refKey(r.ref)
    if (seen.has(k)) continue
    seen.add(k)
    rows.push(objectItem(src.kinds, r.ref, refTitle(r.ref), 'object'))
  }
  const recents: PaletteItem[] = []
  for (const r of src.recents) {
    const k = refKey(r.ref)
    if (seen.has(k)) continue
    seen.add(k)
    recents.push(objectItem(src.kinds, r.ref, r.title || refTitle(r.ref), 'recent'))
  }
  return { rows, recents }
}

type Scored = { item: PaletteItem; score: number; order: number }

function ranked(query: string, list: { item: PaletteItem; texts: [string, number][] }[]): PaletteItem[] {
  const out: Scored[] = []
  list.forEach(({ item, texts }, order) => {
    let best: number | null = null
    for (const [text, bias] of texts) {
      const s = fuzzyScore(query, text)
      if (s !== null && (best === null || s + bias > best)) best = s + bias
    }
    if (best !== null) out.push({ item, score: best, order })
  })
  out.sort((a, b) => b.score - a.score || a.order - b.order)
  return out.map((s) => s.item)
}

const visibleKinds = (src: Sources) => src.kinds.filter((k) => !k.hidden)

function fuzzy(query: string, src: Sources): Built {
  const { rows, recents } = objects(src)
  const kinds = visibleKinds(src).map((k) => ({ item: kindItem(k), texts: [[k.title, 0], ...(k.aliases ?? []).map((a): [string, number] => [a, 0])] as [string, number][] }))
  const targets = src.targets.map((x) => ({ item: targetItem(x), texts: [[x.target.title, 0], [x.target.subtitle ?? '', -2000]] as [string, number][] }))
  const scopes = (src.scopes ?? []).map((n) => ({ item: scopeItem(src, n), texts: [[n, 0]] as [string, number][] }))
  const objs = rows.map((item) => ({ item, texts: [[item.label, 0]] as [string, number][] }))
  const recs = recents.map((item) => ({ item, texts: [[item.label, 0]] as [string, number][] }))
  // Nothing typed: what was opened last first; typed: ties go to live things.
  const list = query.trim() ? [...kinds, ...targets, ...scopes, ...objs, ...recs] : [...recs, ...kinds, ...targets, ...scopes, ...objs]
  const items = ranked(query, list).slice(0, MAX_ITEMS)
  return { items, cursor: items[0]?.key ?? null }
}

const lower = (s: string) => s.toLocaleLowerCase()

/** A kind's own names in a command: its aliases and full id (the provider
 * gives each alias to one kind). */
const ownNames = (k: KindDescriptor) => [...(k.aliases ?? []), k.id].map(lower)

/** Every name a kind answers to in a command: its own, the id's last
 * segment and the title (these may be another kind's too: "other.example/pods"). */
const kindNames = (k: KindDescriptor) => [...ownNames(k), k.id.split('/').pop() ?? k.id, k.title].map(lower)

/** One exact candidate is chosen; several (or none) leave the list to pick from. */
const choose = (items: PaletteItem[], exact: PaletteItem[]): Built => ({ items: items.slice(0, MAX_ITEMS), cursor: exact.length === 1 ? exact[0].key : null })

function command(input: string, src: Sources): Built {
  const body = input.slice(1).trim()
  const sp = body.search(/\s/)
  const head = lower(sp < 0 ? body : body.slice(0, sp))
  const rest = sp < 0 ? '' : body.slice(sp).trim()

  // 1. Scope and target commands (they need an argument).
  if (rest && src.scopeAliases.map(lower).includes(head)) {
    if (rest === '*') return choose([allScopesItem(src)], [allScopesItem(src)])
    if (src.scopes === null) {
      const typed = scopeItem(src, rest)
      return choose([typed], [typed])
    }
    const exact = src.scopes.filter((n) => n === rest).map((n) => scopeItem(src, n))
    const others = ranked(rest, src.scopes.filter((n) => n !== rest).map((n) => ({ item: scopeItem(src, n), texts: [[n, 0]] })))
    const typed = exact.length ? [] : [scopeItem(src, rest, true)]
    return choose([...exact, ...others, ...typed], exact)
  }
  if (rest && src.targetAliases.map(lower).includes(head)) {
    const isExact = (x: Sources['targets'][number]) => lower(x.target.title) === lower(rest) || x.target.id === rest
    const exact = src.targets.filter(isExact).map(targetItem)
    const others = ranked(rest, src.targets.filter((x) => !isExact(x)).map((x) => ({ item: targetItem(x), texts: [[x.target.title, 0], [x.target.subtitle ?? '', -2000]] })))
    return choose([...exact, ...others], exact)
  }

  // 2. Kinds: ":po" opens the view, ":deploy web" opens it filtered. A kind's
  // own name wins over another's id segment or title; several such — a list.
  const own = src.kinds.filter((k) => ownNames(k).includes(head))
  const exactKinds = src.kinds.filter((k) => kindNames(k).includes(head))
  if (head && exactKinds.length) {
    const items = [...own, ...exactKinds.filter((k) => !own.includes(k))].map((k) => kindItem(k, rest || undefined))
    return choose(items, own.length ? items.slice(0, own.length) : items)
  }
  if (!rest) {
    // Incomplete: what it could become (never chosen by itself).
    const kinds = visibleKinds(src).filter((k) => kindNames(k).some((n) => n.startsWith(head))).map((k) => kindItem(k))
    const extra: PaletteItem[] = []
    if (head && src.targetAliases.map(lower).includes(head)) extra.push(...src.targets.map(targetItem))
    if (head && src.scopeAliases.map(lower).includes(head)) extra.push(allScopesItem(src), ...(src.scopes ?? []).map((n) => scopeItem(src, n)))
    const items = [...kinds, ...extra]
    if (items.length) return choose(items, [])
  }
  return { items: [], cursor: null, unknown: true }
}

export function buildItems(input: string, src: Sources): Built {
  return input.trimStart().startsWith(':') ? command(input.trimStart(), src) : fuzzy(input, src)
}
