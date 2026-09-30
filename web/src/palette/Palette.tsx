import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import type { Client } from '../api/client'
import type { RecentObject } from '../api/types'
import { t, type MessageKey } from '../i18n'
import { type Actions, useStore } from '../store'
import { SearchIcon } from '../components/icons'
import { buildItems, type PaletteItem, type Sources } from './items'
import { scopeWords } from '../scopeNames'
import { closePalette, usePalette } from './store'
import { focusMark, restoreFocus } from '../shortcuts'

/** Ctrl+K: go to a view, context, namespace, row of the table or recent object. */
export function Palette({ client, act }: { client: Client; act: Actions }) {
  const open = usePalette((s) => s.open)
  return open ? <PaletteDialog client={client} act={act} /> : null
}

const PAGE = 10

function PaletteDialog({ client, act }: { client: Client; act: Actions }) {
  const host = usePalette((s) => s.host)
  const rows = usePalette((s) => s.rows)
  const liveScopes = usePalette((s) => s.liveScopes)
  const view = useStore((s) => s.view)
  const [query, setQuery] = useState('')
  // The item the user moved to (kept across live updates by its key).
  const [moved, setMoved] = useState<string | null>(null)
  const [recents, setRecents] = useState<RecentObject[]>([])
  const [mark] = useState(focusMark)
  const input = useRef<HTMLInputElement>(null)
  const list = useRef<HTMLDivElement>(null)

  const hostProvider = host?.target.provider
  const hostId = host?.target.id
  useEffect(() => {
    if (!hostProvider || !hostId) return
    let live = true
    client.recentObjects(hostProvider, hostId).then(
      (rs) => {
        // An answer for a target that is no longer the current one is dropped.
        const cur = usePalette.getState().host?.target
        if (live && cur?.provider === hostProvider && cur.id === hostId) setRecents(rs)
      },
      () => {},
    )
    return () => {
      live = false
      setRecents([])
    }
  }, [client, hostProvider, hostId])

  useEffect(() => {
    input.current?.focus()
    return () => restoreFocus(mark)
  }, [mark])

  const sources: Sources = useMemo(() => {
    const group = view?.groups.find((g) => g.provider === hostProvider)
    return {
      kinds: host?.kinds ?? [],
      targets: (view?.groups ?? []).flatMap((g) => g.targets.map((target) => ({ target, groupTitle: g.title }))),
      scopes: host ? (liveScopes ?? host.scopes) : [],
      scopeAliases: host ? (group?.aliases?.scope ?? []) : [],
      scopeWords: scopeWords(group?.scopeNames),
      targetAliases: [...new Set((view?.groups ?? []).flatMap((g) => g.aliases?.target ?? []))],
      rows: host ? rows : [],
      recents: host ? recents : [],
    }
  }, [view, host, hostProvider, liveScopes, rows, recents])
  const built = useMemo(() => buildItems(query, sources), [query, sources])
  // Examples from the provider's own aliases (":po", ":ns"): the UI knows no kind.
  const placeholder = useMemo(() => {
    if (!host) return t('palette.placeholderTargets')
    const base = t('palette.placeholder', { scopes: sources.scopeWords?.plural ?? '' })
    const examples = [...sources.kinds.filter((k) => !k.hidden && k.aliases?.length).slice(0, 2).map((k) => `:${k.aliases![0]}`), ...sources.scopeAliases.slice(0, 1).map((a) => `:${a} …`)]
    return examples.length ? t('palette.examples', { placeholder: base, list: examples.join(', ') }) : base
  }, [sources, host])
  const cursor = moved && built.items.some((i) => i.key === moved) ? moved : built.cursor
  const index = built.items.findIndex((i) => i.key === cursor)

  useLayoutEffect(() => {
    list.current?.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' })
  }, [cursor])

  const run = (item: PaletteItem) => {
    closePalette()
    const a = item.action
    if (a.type === 'target') void act.select(a.ref)
    else if (a.type === 'kind') host?.openKind(a.kind, a.filter ?? '')
    else if (a.type === 'scope') host?.setScope(a.scope)
    else host?.openObject(a.ref)
  }

  const move = (to: number) => {
    const n = built.items.length
    if (!n) return
    setMoved(built.items[Math.max(0, Math.min(n - 1, to))].key)
  }

  const onKey = (e: React.KeyboardEvent) => {
    if (e.nativeEvent.isComposing) return
    const n = built.items.length
    if (e.key === 'Escape') {
      e.preventDefault()
      closePalette()
    } else if (e.key === 'ArrowDown') {
      e.preventDefault()
      move(index < 0 ? 0 : index + 1)
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      move(index < 0 ? n - 1 : index - 1)
    } else if (e.key === 'PageDown') {
      e.preventDefault()
      move(index < 0 ? PAGE - 1 : index + PAGE)
    } else if (e.key === 'PageUp') {
      e.preventDefault()
      move(index < 0 ? 0 : index - PAGE)
    } else if (e.key === 'Enter') {
      e.preventDefault()
      if (index >= 0) run(built.items[index])
    } else if (e.key === 'Tab') {
      e.preventDefault() // focus stays in the palette
    }
  }

  const note = built.unknown ? t('palette.unknown') : !built.items.length ? t('palette.empty') : index < 0 ? t('palette.pick') : null

  return (
    <div className="fixed inset-0 z-40 flex items-start justify-center bg-black/40 pt-[12vh]" onMouseDown={(e) => e.target === e.currentTarget && closePalette()}>
      <div role="dialog" aria-modal="true" aria-label={t('palette.label')} className="flex max-h-[70vh] w-[min(640px,92%)] flex-col overflow-hidden rounded-lg border border-line bg-panel shadow-2xl" onKeyDown={onKey}>
        <label className="flex items-center gap-2 border-b border-line px-3 py-2">
          <SearchIcon className="h-4 w-4 shrink-0 text-fg-subtle" />
          <input
            ref={input}
            role="combobox"
            aria-expanded="true"
            aria-controls="palette-list"
            aria-activedescendant={index >= 0 ? `palette-opt-${index}` : undefined}
            aria-label={t('palette.label')}
            value={query}
            onChange={(e) => {
              setQuery(e.target.value)
              setMoved(null)
            }}
            placeholder={placeholder}
            spellCheck={false}
            autoComplete="off"
            className="w-full bg-transparent text-[16px] outline-none placeholder:text-fg-subtle"
          />
        </label>
        <div ref={list} id="palette-list" role="listbox" aria-label={t('palette.label')} className="min-h-0 flex-1 overflow-y-auto py-1">
          {built.items.map((item, i) => (
            <div
              key={item.key}
              id={`palette-opt-${i}`}
              role="option"
              aria-selected={i === index}
              onMouseDown={(e) => e.preventDefault()}
              onClick={() => run(item)}
              onMouseMove={() => item.key !== cursor && setMoved(item.key)}
              className={['flex cursor-pointer items-baseline gap-3 px-3 py-1.5', i === index ? 'bg-active text-fg' : 'text-fg-muted'].join(' ')}
            >
              <span className="w-20 shrink-0 truncate text-[12px] uppercase tracking-wide text-fg-subtle">{item.section === 'scope' ? sources.scopeWords?.singular : t(`palette.section.${item.section}` as MessageKey)}</span>
              <span className="min-w-0 flex-1 truncate text-fg">{item.label}</span>
              {item.hint && <span className="max-w-[45%] shrink-0 truncate text-xs text-fg-subtle">{item.hint}</span>}
            </div>
          ))}
        </div>
        <footer className="flex items-center gap-3 border-t border-line px-3 py-1.5 text-xs text-fg-subtle">
          {note && <span role="status">{note}</span>}
          {host && <span className="ml-auto">{t('palette.coverage')}</span>}
        </footer>
      </div>
    </div>
  )
}
