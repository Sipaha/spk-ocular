import { useEffect, useMemo, useRef, useState } from 'react'
import type { Client } from '../api/client'
import type { AgentScope, AgentTarget, KindDescriptor, KindsView } from '../api/types'
import { actionLabel, providerText, t, type MessageKey } from '../i18n'
import { scopeWords, type ScopeWords } from '../scopeNames'
import { showNotice, useStore } from '../store'
import { errorDetail } from '../errors'
import { Select } from '../components/Select'
import { WarningIcon } from '../components/icons'
import {
  addScope,
  confirmable,
  fromGrants,
  isAction,
  problems,
  removeScope,
  sameGrants,
  scopeKey,
  setVerb,
  toGrants,
  verbOptions,
  warnings,
  type ScopeGrants,
  type VerbOption,
  type VerbSel,
} from './model'

/** Reads of a target's kinds while its catalog is still being discovered. */
const DISCOVER_POLLS = 20
const DISCOVER_EVERY_MS = 1500

/** A target's kinds and scopes for the editor (its own session, not the selected one's). */
function useCatalog(client: Client, provider: string, target: string, exists: boolean) {
  const [kinds, setKinds] = useState<KindsView | null>(null)
  const [kindsError, setKindsError] = useState<string | null>(null)
  const [scopes, setScopes] = useState<string[] | null>(null)
  const [scopesError, setScopesError] = useState(false)
  // The editor is keyed by its target: these start empty for each.
  useEffect(() => {
    if (!exists) return
    let live = true
    let timer: ReturnType<typeof setTimeout> | undefined
    let polls = 0
    const readKinds = () =>
      client.listKinds(provider, target).then(
        (v) => {
          if (!live) return
          setKinds(v)
          if (v.state === 'discovering' && ++polls < DISCOVER_POLLS) timer = setTimeout(readKinds, DISCOVER_EVERY_MS)
        },
        (e) => live && setKindsError(errorDetail(e)),
      )
    void readKinds()
    client.listScopes(provider, target).then(
      (v) => {
        if (!live) return
        setScopes(v.scopes.map((s) => s.name).sort())
        setScopesError(!!v.error)
      },
      () => live && setScopesError(true),
    )
    return () => {
      live = false
      clearTimeout(timer)
    }
  }, [client, provider, target, exists])
  return { kinds, kindsError, scopes, scopesError }
}

const verbTitle = (o: Pick<VerbOption, 'verb' | 'action'>) =>
  o.action ? actionLabel(o.action) : t(`agents.verb.${o.verb}` as MessageKey)

const verbHint = (verb: string) => (isAction(verb) ? undefined : t(`agents.verb.${verb}Hint` as MessageKey))

export function scopeLabel(scope: AgentScope, provider: string, words: ScopeWords): string {
  if (scope.mode === 'one') return `${words.singular} ${scope.name ?? ''}`
  if (scope.mode === 'all') return t('agents.allScopes', { all: words.all })
  return providerText('agents.cluster', provider, { scopes: words.plural })
}

interface Props {
  client: Client
  provider: string
  target: string
  title: string
  /** the target's grants as saved (none: not granted) */
  saved: AgentTarget | null
  /** the target is in the configuration now */
  exists: boolean
}

/** One target's grants: scopes with verbs, kinds and "without confirmation"; saved whole. */
export function GrantEditor({ client, provider, target, title, saved, exists }: Props) {
  const view = useStore((s) => s.view)
  const words = scopeWords(view?.groups.find((g) => g.provider === provider)?.scopeNames)
  const { kinds, kindsError, scopes, scopesError } = useCatalog(client, provider, target, exists)
  const savedGrants = useMemo(() => saved?.grants ?? [], [saved])
  // The draft is the user's own until saved or discarded; without one the
  // editor shows what is saved (and follows it).
  const [draft, setDraft] = useState<ScopeGrants[] | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [typed, setTyped] = useState('')
  const current = draft ?? fromGrants(savedGrants)
  const dirty = draft !== null && !sameGrants(toGrants(draft), savedGrants)
  const kindList = useMemo(() => kinds?.kinds ?? [], [kinds])
  const byMode = useMemo(() => ({ one: verbOptions(kindList, 'one'), all: verbOptions(kindList, 'all'), cluster: verbOptions(kindList, 'cluster') }), [kindList])
  const optionsOf = (mode: AgentScope['mode']) => byMode[mode]
  const probs = problems(current, optionsOf)
  const warns = warnings(provider, current, kindList)

  const edit = (next: ScopeGrants[]) => {
    setError(null)
    setDraft(next)
  }

  const save = async (grants = toGrants(current)) => {
    setBusy(true)
    setError(null)
    try {
      await client.saveAgentGrants(provider, target, grants)
      setDraft(null)
      showNotice(t('agents.saved', { target: title }))
    } catch (e) {
      setError(t('agents.saveFailed', { error: errorDetail(e) }))
    } finally {
      setBusy(false)
    }
  }

  const reconfirm = async (observed: string) => {
    setBusy(true)
    setError(null)
    try {
      await client.reconfirmAgentTarget(provider, target, observed)
    } catch (e) {
      setError(errorDetail(e))
    } finally {
      setBusy(false)
    }
  }

  const taken = new Set(current.map((s) => scopeKey(s.scope)))
  const addOptions = [
    { value: '', label: t('agents.addScope'), disabled: true },
    ...(kindList.some((k) => k.scoped) ? [{ value: 'all', label: t('agents.allScopes', { all: words.all }), pinned: true, disabled: taken.has('all') }] : []),
    ...(kindList.some((k) => !k.scoped) ? [{ value: 'cluster', label: providerText('agents.cluster', provider, { scopes: words.plural }), pinned: true, disabled: taken.has('cluster') }] : []),
    ...(scopes ?? []).filter((n) => !taken.has(`one:${n}`)).map((n) => ({ value: `one:${n}`, label: n })),
  ]
  const add = (v: string) => {
    if (v === 'all') edit(addScope(current, { mode: 'all' }))
    else if (v === 'cluster') edit(addScope(current, { mode: 'cluster' }))
    else if (v.startsWith('one:')) edit(addScope(current, { mode: 'one', name: v.slice(4) }))
  }
  const addTyped = () => {
    const n = typed.trim()
    if (!n) return
    edit(addScope(current, { mode: 'one', name: n }))
    setTyped('')
  }

  const reason = probs[0]
  const reasonText = reason && t('agents.cantSave', { reason: problemText(reason, current, optionsOf, words) })

  return (
    <section aria-label={title} className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto p-3 text-xs">
      <header className="flex flex-wrap items-baseline gap-2">
        <h3 className="text-sm font-semibold">{title}</h3>
        {!exists && <span className="rounded bg-warning/15 px-1 text-warning">{t('agents.missing')}</span>}
      </header>

      {saved?.observed && (
        <div role="alert" className="flex flex-col gap-2 rounded-md bg-warning/10 px-3 py-2 text-warning">
          <p className="break-all">{t('agents.suspended', { was: saved.identity, now: saved.observed })}</p>
          <button type="button" disabled={busy} className="self-start rounded-md border border-warning px-2 py-0.5 hover:bg-warning/10 disabled:opacity-50" onClick={() => void reconfirm(saved.observed ?? '')}>
            {t('agents.reconfirm')}
          </button>
        </div>
      )}

      {exists && !kinds && !kindsError && <p className="text-fg-subtle">{t('agents.kindsLoading')}</p>}
      {kindsError && <p role="alert" className="text-danger">{t('agents.kindsFailed', { error: kindsError })}</p>}
      {kinds?.state === 'discovering' && <p className="text-fg-subtle">{t('agents.kindsDiscovering')}</p>}

      {current.length === 0 && <p className="text-fg-muted">{t('agents.nothing')}</p>}

      {current.map((s) => (
        <ScopeCard
          key={scopeKey(s.scope)}
          provider={provider}
          words={words}
          grants={s}
          options={optionsOf(s.scope.mode)}
          disabled={busy}
          onVerb={(verb, sel) => edit(setVerb(current, scopeKey(s.scope), verb, sel))}
          onRemove={() => edit(removeScope(current, scopeKey(s.scope)))}
        />
      ))}

      {exists && kinds && (
        <div className="flex flex-wrap items-center gap-2">
          <Select label={t('agents.addScope')} value="" options={addOptions} onChange={add} search searchLabel={words.plural} disabled={busy} />
          {scopesError && (
            <>
              <span className="text-fg-subtle">{t('agents.scopesFailed', { scopes: words.plural })}</span>
              <input
                value={typed}
                onChange={(e) => setTyped(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') {
                    e.preventDefault()
                    addTyped()
                  }
                }}
                aria-label={t('agents.scopeName', { scope: words.singular })}
                placeholder={t('agents.scopeName', { scope: words.singular })}
                className="w-40 rounded-md border border-line bg-app px-2 py-1 font-mono outline-none focus:border-accent"
                spellCheck={false}
              />
              <button type="button" className="rounded-md border border-line px-2 py-1 hover:bg-hover" onClick={addTyped}>
                {t('agents.addScope')}
              </button>
            </>
          )}
        </div>
      )}

      {warns.length > 0 && (
        <ul aria-label="warnings" className="flex flex-col gap-1 rounded-md bg-warning/10 px-3 py-2 text-warning">
          {warns.map((w) => (
            <li key={w} className="flex gap-1.5">
              <WarningIcon className="mt-px h-3.5 w-3.5 shrink-0" />
              <span>{w === 'cluster' ? providerText('agents.warn.cluster', provider, { scopes: words.plural }) : t(`agents.warn.${w}` as MessageKey, { all: words.all })}</span>
            </li>
          ))}
        </ul>
      )}

      {error && <p role="alert" className="text-danger">{error}</p>}

      <footer className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          disabled={!dirty || !!reason || busy}
          className="rounded-md bg-accent px-3 py-1 text-accent-fg disabled:opacity-50"
          onClick={() => void save()}
          title={reasonText}
        >
          {busy ? t('agents.saving') : t('agents.save')}
        </button>
        {dirty && (
          <button type="button" disabled={busy} className="rounded-md px-3 py-1 text-fg-muted hover:bg-hover" onClick={() => setDraft(null)}>
            {t('agents.discard')}
          </button>
        )}
        {dirty && reasonText && <span role="status" className="text-danger">{reasonText}</span>}
        {savedGrants.length > 0 && (
          <button type="button" disabled={busy} className="ml-auto rounded-md border border-line px-2 py-1 text-danger hover:bg-danger/10" onClick={() => void save([])}>
            {t('agents.revokeTarget')}
          </button>
        )}
      </footer>
    </section>
  )
}

function problemText(p: ReturnType<typeof problems>[number], scopes: ScopeGrants[], optionsOf: (m: AgentScope['mode']) => VerbOption[], words: ScopeWords): string {
  const s = scopes.find((x) => scopeKey(x.scope) === p.scope)
  const o = s && optionsOf(s.scope.mode).find((x) => x.verb === p.verb)
  const verb = o ? verbTitle(o) : p.verb.startsWith('action:') ? p.verb.slice(7) : p.verb
  const where = s?.scope.mode === 'one' ? `${s.scope.name} · ` : ''
  return t(p.key, { verb: `${where}${verb}`, scope: words.singular })
}

interface CardProps {
  provider: string
  words: ScopeWords
  grants: ScopeGrants
  options: VerbOption[]
  disabled: boolean
  onVerb: (verb: string, sel: VerbSel | null) => void
  onRemove: () => void
}

function ScopeCard({ provider, words, grants, options, disabled, onVerb, onRemove }: CardProps) {
  const label = scopeLabel(grants.scope, provider, words)
  // A verb granted that the kinds do not offer (the catalog changed): still shown, to be removed.
  const extra = Object.keys(grants.verbs).filter((v) => !options.some((o) => o.verb === v))
  const rows: VerbOption[] = [...options, ...extra.map((verb) => ({ verb, destructive: false, kinds: [] as KindDescriptor[] }))]
  return (
    <section aria-label={label} className="rounded-md border border-line">
      <header className="flex items-center gap-2 border-b border-line bg-sidebar/60 px-3 py-1.5">
        <h4 className="font-semibold">{label}</h4>
        {grants.scope.mode === 'cluster' && <span className="text-fg-subtle">{t('agents.clusterReadOnly')}</span>}
        <button type="button" disabled={disabled} className="ml-auto rounded px-2 text-fg-muted hover:bg-hover hover:text-fg" onClick={onRemove}>
          {t('agents.removeScope')}
        </button>
      </header>
      <ul className="flex flex-col">
        {rows.map((o) => (
          <VerbRow key={o.verb} option={o} sel={grants.verbs[o.verb] ?? null} disabled={disabled} onChange={(sel) => onVerb(o.verb, sel)} />
        ))}
      </ul>
    </section>
  )
}

function VerbRow({ option, sel, disabled, onChange }: { option: VerbOption; sel: VerbSel | null; disabled: boolean; onChange: (sel: VerbSel | null) => void }) {
  const [picking, setPicking] = useState(false)
  const title = verbTitle(option)
  const hint = verbHint(option.verb)
  const on = !!sel
  const bad = !!sel && ((sel.kinds && sel.kinds.length === 0) || (!sel.kinds && option.destructive))
  return (
    <li className="border-b border-line px-3 py-1.5 last:border-b-0">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <label className="flex min-w-40 items-center gap-1.5">
          <input
            type="checkbox"
            checked={on}
            disabled={disabled}
            aria-label={option.destructive ? `${title} (${t('agents.destructive')})` : title}
            onChange={(e) => {
              if (!e.target.checked) return onChange(null)
              // A destructive action is granted by name: its kinds are chosen first.
              onChange({ kinds: option.destructive ? [] : null, noConfirm: false })
              if (option.destructive) setPicking(true)
            }}
          />
          <span className={option.destructive ? 'text-danger' : ''}>{title}</span>
          {option.destructive && <span className="rounded bg-danger/10 px-1 text-[11px] text-danger">{t('agents.destructive')}</span>}
        </label>
        {on && option.kinds.length > 0 && (
          <button
            type="button"
            aria-expanded={picking}
            aria-label={`${title}: ${t('agents.kindsPick')}`}
            disabled={disabled}
            className={['max-w-[28rem] truncate rounded-md border px-2 py-0.5 text-left hover:bg-hover', bad ? 'border-danger text-danger' : 'border-line text-fg-muted'].join(' ')}
            onClick={() => setPicking((p) => !p)}
          >
            {kindsText(sel, option.kinds)} ▾
          </button>
        )}
        {on && confirmable(option.verb) && sel.kinds !== null && (
          <label className="flex items-center gap-1.5 text-fg-muted" title={t('agents.noConfirmHint')}>
            <input type="checkbox" checked={sel.noConfirm} disabled={disabled} onChange={(e) => onChange({ ...sel, noConfirm: e.target.checked })} />
            {t('agents.noConfirm')}
          </label>
        )}
        {hint && <span className="text-fg-subtle">{hint}</span>}
      </div>
      {on && picking && <KindPicker verb={option.verb} kinds={option.kinds} sel={sel} onChange={onChange} onDone={() => setPicking(false)} />}
    </li>
  )
}

function kindsText(sel: VerbSel, kinds: KindDescriptor[]): string {
  if (!sel.kinds) return t('agents.kindsAll')
  if (sel.kinds.length === 0) return t('agents.kindsNone')
  return t('agents.kindsList', { list: sel.kinds.map((id) => kinds.find((k) => k.id === id)?.title ?? id).join(', ') })
}

function KindPicker({ verb, kinds, sel, onChange, onDone }: { verb: string; kinds: KindDescriptor[]; sel: VerbSel; onChange: (sel: VerbSel) => void; onDone: () => void }) {
  const [q, setQ] = useState('')
  const filter = useRef<HTMLInputElement>(null)
  useEffect(() => filter.current?.focus(), [])
  const shown = useMemo(() => {
    const f = q.trim().toLowerCase()
    const list = [...kinds].sort((a, b) => a.title.localeCompare(b.title))
    return f ? list.filter((k) => k.title.toLowerCase().includes(f) || k.id.toLowerCase().includes(f)) : list
  }, [kinds, q])
  const chosen = new Set(sel.kinds ?? [])
  // Kinds granted that the catalog no longer lists stay visible (to be removed).
  const gone = (sel.kinds ?? []).filter((id) => !kinds.some((k) => k.id === id))
  const toggle = (id: string, on: boolean) => {
    const next = new Set(chosen)
    if (on) next.add(id)
    else next.delete(id)
    onChange({ ...sel, kinds: [...next].sort() })
  }
  return (
    <div className="mt-1.5 flex flex-col gap-1 rounded-md border border-line bg-app p-2" role="group" aria-label={t('agents.kindsPick')}>
      <div className="flex items-center gap-2">
        <input
          ref={filter}
          value={q}
          onChange={(e) => setQ(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Escape') {
              e.preventDefault()
              e.stopPropagation()
              onDone()
            }
          }}
          aria-label={t('agents.kindsFilter')}
          placeholder={t('agents.kindsFilter')}
          className="w-48 rounded-md border border-line bg-panel px-2 py-0.5 outline-none focus:border-accent"
          spellCheck={false}
        />
        <label className="flex items-center gap-1.5">
          <input type="checkbox" checked={!sel.kinds} onChange={(e) => onChange({ ...sel, kinds: e.target.checked ? null : [] })} />
          {t('agents.kindsAllOption')}
        </label>
        <button type="button" className="ml-auto rounded-md border border-line px-2 py-0.5 hover:bg-hover" onClick={onDone}>
          {t('agents.kindsDone')}
        </button>
      </div>
      <p className="text-fg-subtle">{t('agents.kindsAllNote')}</p>
      <ul className="grid max-h-48 grid-cols-[repeat(auto-fill,minmax(14rem,1fr))] gap-x-3 overflow-y-auto">
        {gone.map((id) => (
          <li key={id}>
            <label className="flex items-center gap-1.5">
              <input type="checkbox" checked onChange={() => toggle(id, false)} />
              <span className="font-mono text-fg-subtle">{id}</span>
            </label>
          </li>
        ))}
        {shown.map((k) => (
          <li key={k.id}>
            <label className="flex min-w-0 items-center gap-1.5" title={k.id}>
              <input type="checkbox" checked={sel.kinds ? chosen.has(k.id) : !(verb === 'edit' && k.sensitive)} disabled={!sel.kinds} onChange={(e) => toggle(k.id, e.target.checked)} />
              <span className="truncate">{k.title}</span>
              {verb === 'edit' && k.sensitive && <span className="shrink-0 rounded bg-warning/15 px-1 text-[11px] text-warning">{t('agents.sensitive')}</span>}
            </label>
          </li>
        ))}
      </ul>
    </div>
  )
}
