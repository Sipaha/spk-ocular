import { useEffect, useRef } from 'react'
import type { Target, TargetGroup } from '../api/types'
import { providerText, t } from '../i18n'
import { type Actions, matchesFilter, targetKey, useStore } from '../store'
import { EyeIcon, ProviderIcon, SearchIcon, WarningIcon } from './icons'
import { openPalette } from '../palette/store'

export function Sidebar({ act }: { act: Actions }) {
  const view = useStore((s) => s.view)
  const filter = useStore((s) => s.filter)
  const listRef = useRef<HTMLDivElement>(null)

  const onListKey = (e: React.KeyboardEvent) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      act.moveCursor(1)
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      act.moveCursor(-1)
    } else if (e.key === 'Enter') {
      const cursor = useStore.getState().cursor
      const target = view?.groups.flatMap((g) => g.targets).find((x) => targetKey(x) === cursor)
      if (target) {
        e.preventDefault()
        act.select(target)
      }
    }
  }

  const onFilterKey = (e: React.KeyboardEvent<HTMLInputElement>) => {
    if (e.key === 'Escape') {
      e.preventDefault()
      if (filter) act.setFilter('')
      else e.currentTarget.blur()
      return
    }
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp' || e.key === 'Enter') {
      onListKey(e)
      if (e.key !== 'Enter') listRef.current?.focus()
    }
  }

  return (
    <aside data-area="targets" className="flex h-full w-60 shrink-0 flex-col border-r border-line bg-sidebar">
      <div className="flex items-center gap-2 px-4 pt-4 pb-3">
        <EyeIcon className="h-5 w-5 text-accent" />
        <span className="text-[16px] font-semibold tracking-tight">SPK Ocular</span>
        <button
          className="ml-auto rounded border border-line px-1.5 py-0.5 text-[12px] text-fg-subtle hover:bg-hover hover:text-fg"
          onClick={openPalette}
          title={t('palette.label')}
          aria-label={`${t('palette.label')} (Ctrl+K)`}
        >
          Ctrl+K
        </button>
      </div>
      <label className="mx-3 mb-2 flex items-center gap-2 rounded-md border border-line bg-app px-2 py-1.5 focus-within:border-accent">
        <SearchIcon className="h-3.5 w-3.5 text-fg-subtle" />
        <input
          data-target-filter
          value={filter}
          onChange={(e) => act.setFilter(e.target.value)}
          onKeyDown={onFilterKey}
          placeholder={t('sidebar.filterHint')}
          aria-label={t('sidebar.filter')}
          className="w-full bg-transparent text-fg outline-none placeholder:text-fg-subtle"
          spellCheck={false}
        />
      </label>
      <div
        ref={listRef}
        role="listbox"
        tabIndex={0}
        data-area-focus
        onKeyDown={onListKey}
        aria-label="targets"
        className="min-h-0 flex-1 overflow-y-auto px-2 pb-3 outline-none"
      >
        {view?.groups.map((g) => <Group key={g.provider} group={g} act={act} />)}
      </div>
    </aside>
  )
}

function Group({ group, act }: { group: TargetGroup; act: Actions }) {
  const filter = useStore((s) => s.filter)
  const visible = group.targets.filter((x) => matchesFilter(x, filter))
  return (
    <section className="mt-2" aria-label={group.title}>
      <h2 className="flex items-center gap-1.5 px-2 pb-1 text-[12px] font-semibold uppercase tracking-wider text-fg-subtle">
        <ProviderIcon provider={group.provider} className="h-3.5 w-3.5" />
        <span className="flex-1">{group.title}</span>
        <span className="font-normal">{group.targets.length}</span>
      </h2>
      {group.error && <Notice text={t('sidebar.providerError', { error: group.error })} />}
      {group.problems.length > 0 && (
        <Notice text={t('sidebar.problems', { count: group.problems.length })}>
          {group.problems.map((p) => (
            <span key={p.source} title={`${p.source}: ${p.message}`} className="block truncate font-mono text-[12px] opacity-80">
              {p.source.split('/').pop()}: {p.message}
            </span>
          ))}
        </Notice>
      )}
      {group.targets.length === 0 && !group.error && (
        <p className="px-2 py-1 text-xs leading-relaxed text-fg-subtle">{providerText('sidebar.noTargets', group.provider)}</p>
      )}
      {group.targets.length > 0 && visible.length === 0 && <p className="px-2 py-1 text-xs text-fg-subtle">{t('sidebar.empty')}</p>}
      {visible.map((x) => (
        <TargetRow key={x.id} target={x} act={act} />
      ))}
    </section>
  )
}

function TargetRow({ target, act }: { target: Target; act: Actions }) {
  const key = targetKey(target)
  const selected = useStore((s) => (s.view?.selected ? targetKey(s.view.selected) === key : false))
  const cursor = useStore((s) => s.cursor === key)
  const ref = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (cursor) ref.current?.scrollIntoView({ block: 'nearest' })
  }, [cursor])
  return (
    <div
      ref={ref}
      role="option"
      aria-selected={selected}
      data-cursor={cursor || undefined}
      onClick={() => act.select(target)}
      className={[
        'group flex cursor-default items-center gap-2 rounded-md px-2 py-1.5',
        selected ? 'bg-active' : 'hover:bg-hover',
        cursor && !selected ? 'ring-1 ring-line ring-inset' : '',
      ].join(' ')}
    >
      <span className={['h-1.5 w-1.5 shrink-0 rounded-full', selected ? 'bg-accent' : 'bg-fg-subtle/60'].join(' ')} />
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5">
          <span className="truncate">{target.title}</span>
          {target.current && (
            <span title={providerText('target.currentHint', target.provider)} className="shrink-0 rounded bg-panel px-1 text-[11px] text-fg-muted">
              {t('target.current')}
            </span>
          )}
        </span>
        {target.subtitle && target.subtitle !== target.title && (
          <span className="block truncate text-xs text-fg-subtle">{target.subtitle}</span>
        )}
      </span>
    </div>
  )
}

function Notice({ text, children }: { text: string; children?: React.ReactNode }) {
  return (
    <div role="alert" className="mx-1 mb-1 flex items-start gap-1.5 rounded-md bg-warning/10 px-2 py-1.5 text-xs text-warning">
      <WarningIcon className="mt-px h-3.5 w-3.5 shrink-0" />
      <div className="min-w-0 flex-1">
        <span>{text}</span>
        {children}
      </div>
    </div>
  )
}
