// What the palette offers for an input: fuzzy over the current target's
// views, all targets, scopes and eligible live rows. History ranks matching rows,
// or a command (":po", ":deploy web", ":ns demo", ":ns *", ":ctx prod").
// Aliases come from the providers (KindDescriptor.aliases, TargetGroup
// .aliases): nothing here knows Kubernetes.
import type { KindDescriptor, RecentObject, Ref, Row, ScopeSel, Target, TargetRef } from '../api/types'
import { t } from '../i18n'
import { refTitle } from '../refs'
import { scopeWords, type ScopeWords } from '../scopeNames'
import { fuzzyScore } from './score'
import { selectedScopes } from '../scopes'
import { englishKeyboardQuery } from './keyboardLayout'

export type PaletteAction =
  | { type: 'kind'; kind: string; filter?: string; scope?:ScopeSel }
  | { type: 'target'; ref: TargetRef }
  | { type: 'scope'; scope: ScopeSel }
  | { type: 'object'; ref: Ref; scope?:ScopeSel }
  | { type: 'command'; id: 'agents' }

export type Section = 'kind' | 'target' | 'scope' | 'object' | 'command'

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
  /** Object results stay inside this selection; null until target state is loaded. */
  selectedScope: ScopeSel | null
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

const kindItem = (k: KindDescriptor, filter?: string,scope?:ScopeSel): PaletteItem => ({
  key: `kind:${k.id}${filter ? `:${filter}` : ''}`,
  section: 'kind',
  label: k.title,
  hint: filter ? t('palette.filtered', { text: filter }) : k.subgroup ? `${k.group} · ${k.subgroup}` : k.group,
  action: { type:'kind',kind:k.id,...(filter?{filter}:{}),...(scope?{scope}:{}) },
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

const objectItem = (kinds: KindDescriptor[], ref: Ref): PaletteItem => ({
  key: `object:${refKey(ref)}`,
  section: 'object', label: refTitle(ref), hint: objectHint(kinds, ref),
  action: {type:'object',ref},
})

/** History can rank eligible live rows; it cannot introduce absent objects. */
function objects(src: Sources): PaletteItem[] {
 const names=new Set(src.selectedScope?selectedScopes(src.selectedScope):[])
 const allowed=(ref:Ref)=>!ref.scope||src.selectedScope?.mode==='all'||names.has(ref.scope)
 const seen=new Set<string>()
 return src.rows.filter(row=>{
  const key=refKey(row.ref)
  if(!allowed(row.ref)||seen.has(key))return false
  seen.add(key);return true
 }).map(row=>objectItem(src.kinds,row.ref))
}
function recentPriority(src:Sources): (item:PaletteItem)=>number {
 const priorities=new Map<string,number>()
 src.recents.forEach((r,index)=>{const key=`object:${refKey(r.ref)}`;if(!priorities.has(key))priorities.set(key,1000-Math.min(index,999))})
 return item=>priorities.get(item.key)??0
}

/** The app's own panels, whatever the target. */
const commands = (): { item: PaletteItem; texts: [string, number][] }[] => [
  { item: { key: 'command:agents', section: 'command', label: t('agents.panel'), action: { type: 'command', id: 'agents' } }, texts: [[t('agents.panel'), 0], ['agents', -1000]] },
]

type Scored = { item: PaletteItem; score: number; order: number }

function ranked(query: string, list: { item: PaletteItem; texts: [string, number][] }[], priority: (item:PaletteItem)=>number = ()=>0): PaletteItem[] {
  const out: Scored[] = []
  list.forEach(({ item, texts }, order) => {
    let best: number | null = null
    for (const [text, bias] of texts) {
      const s = fuzzyScore(query, text)
      if (s !== null && (best === null || s + bias > best)) best = s + bias
    }
    if (best !== null) out.push({ item, score: best + priority(item), order })
  })
  out.sort((a, b) => b.score - a.score || a.order - b.order)
  return out.map((s) => s.item)
}

const visibleKinds = (src: Sources) => src.kinds.filter((k) => !k.hidden)

function fuzzy(query: string, src: Sources): Built {
  const rows = objects(src)
  const kinds = visibleKinds(src).map((k) => ({ item: kindItem(k), texts: [[k.title, 0], ...(k.aliases ?? []).map((a): [string, number] => [a, 0])] as [string, number][] }))
  const targets = src.targets.map((x) => ({ item: targetItem(x), texts: [[x.target.title, 0], [x.target.subtitle ?? '', -2000]] as [string, number][] }))
  const scopes = (src.scopes ?? []).map((n) => ({ item: scopeItem(src, n), texts: [[n, 0]] as [string, number][] }))
  const objs = rows.map((item) => ({ item, texts: [[item.label, 0]] as [string, number][] }))
  const items = ranked(query,[...kinds,...targets,...scopes,...objs,...commands()],recentPriority(src)).slice(0,MAX_ITEMS)
  return { items, cursor: items[0]?.key ?? null }
}

const lower = (s: string) => s.toLocaleLowerCase()

/** A kind's own names in a command: its aliases and full id (the provider
 * gives each alias to one kind). */
const ownNames = (k: KindDescriptor) => [...(k.aliases ?? []), k.id].map(lower)

/** Every name a kind answers to in a command: its own, the id's last
 * segment and the title (these may be another kind's too: "other.example/pods"). */
const kindNames = (k: KindDescriptor) => [...ownNames(k), k.id.split('/').pop() ?? k.id, k.title, k.singular ?? ''].map(lower)

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

export interface TypedKindQuery { kinds: KindDescriptor[]; filter: string; scope?:ScopeSel }

/** Exact provider aliases win; an unambiguous prefix also supports "dep model".
 * Ambiguity offers views to choose from and must not start multiple reads. */
function kindQuery(input:string,kinds:KindDescriptor[]):TypedKindQuery|null {
 if(input.trimStart().startsWith(':'))return null
 const text=input.trim(),space=text.search(/\s/)
 if(space<0||!text.slice(space).trim())return null
 if(kinds.some(k=>lower(k.title)===lower(text)))return null
 const head=lower(text.slice(0,space)),filter=text.slice(space).trim()
 const resources=kinds.filter(k=>!k.workspace)
 const own=resources.filter(k=>ownNames(k).includes(head))
 const exact=resources.filter(k=>kindNames(k).includes(head))
 const ownPrefix=resources.filter(k=>!k.hidden).filter(k=>ownNames(k).some(n=>n.startsWith(head)))
 const prefix=resources.filter(k=>!k.hidden&&kindNames(k).some(n=>n&&n.startsWith(head)))
 const matches=own.length?own:exact.length?exact:ownPrefix.length?ownPrefix:prefix
 return matches.length?{kinds:matches,filter}:null
}
/** Explicit scope + kind + name, after the existing kind-first grammar. */
export function typedKindQuery(input:string,kinds:KindDescriptor[]):TypedKindQuery|null {
 const regular=kindQuery(input,kinds)
 if(regular||input.trimStart().startsWith(':')||kinds.some(k=>lower(k.title)===lower(input.trim())))return regular
 const match=input.trim().match(/^(\S+)\s+(\S+)\s+(.+)$/)
 if(!match)return null
 const scoped=kindQuery(match[2]+' '+match[3],kinds.filter(k=>k.scoped))
 return scoped?{...scoped,scope:{mode:'one',name:match[1]}}:null
}
function typedItems(query:TypedKindQuery,src:Sources):Built {
 if(query.kinds.length!==1)return choose(query.kinds.map(k=>kindItem(k,query.filter,query.scope)),[])
 const kind=query.kinds[0]
 const found=objects(query.scope?{...src,selectedScope:query.scope}:src).filter(item=>item.action.type==='object'&&item.action.ref.kind===kind.id).map(item=>item.action.type==='object'&&query.scope?{...item,action:{...item.action,scope:query.scope}}:item)
 const words=query.filter.split(/\s+/).filter(Boolean)
 const matches=found.map((item,order)=>({item,order,scores:words.map(word=>fuzzyScore(word,item.label))})).filter(entry=>entry.scores.every(score=>score!==null))
 const priority=recentPriority(src)
 matches.sort((a,b)=>(b.scores.reduce<number>((sum,score)=>sum+(score??0),0)+priority(b.item))-(a.scores.reduce<number>((sum,score)=>sum+(score??0),0)+priority(a.item))||a.order-b.order)
 const items=matches.map(entry=>entry.item).slice(0,MAX_ITEMS)
 if(!items.length)items.push(kindItem(kind,query.filter,query.scope))
 return {items,cursor:items[0]?.key??null}
}
export function buildItems(input: string, src: Sources): Built {
 if(input.trimStart().startsWith(':'))return command(input.trimStart(),src)
 const typed=typedKindQuery(input,src.kinds)
 return typed?typedItems(typed,src):fuzzy(input,src)
}

/** Keep real matches in the original layout. A filtered-view shortcut is
 * not an object match; a resolved kind keeps its prefix while its name is
 * recovered. Callers wait for an explicit other-kind read before fallback. */
export function buildKeyboardItems(input: string, src: Sources, allowFallback = true): { query: string; built: Built } {
 const built = buildItems(input, src)
 if (!allowFallback || input.trimStart().startsWith(':')) return { query: input, built }
 const typed = typedKindQuery(input, src.kinds)
 const matched = typed?.kinds.length === 1 ? built.items.some(item => item.action.type === 'object') : built.items.length > 0
 if (matched) return { query: input, built }
 const mapped = englishKeyboardQuery(typed ? typed.filter : input)
 if (mapped === null) return { query: input, built }
 const query = typed ? input.trim().slice(0,input.trim().length-typed.filter.length) + mapped : mapped
 return { query, built: buildItems(query, src) }
}
