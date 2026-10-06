import type { Client } from '../api/client'
import { Configurations, type ConfigurationActions } from '../configurations/Configurations'
import { PanelResize, usePanelWidths } from './PanelResize'
import { useEffect, useRef, useState } from 'react'
import type { Target, TargetGroup } from '../api/types'
import { providerText, t } from '../i18n'
import { type Actions, matchesFilter, targetKey, useStore } from '../store'
import { ProviderIcon, SearchIcon, WarningIcon } from './icons'
import { agents, pendingOf, useAgents } from '../agents/store'
import { Menu, type MenuItem } from '../actions/Menu'

export function Sidebar({ act, client, configRef }: { act: Actions; client: Client; configRef: React.RefObject<ConfigurationActions | null> }) {
  const width = usePanelWidths((s) => s.targets)
  const view = useStore((s) => s.view)
  const filter = useStore((s) => s.filter)
  const listRef = useRef<HTMLDivElement>(null)
  const [menu, setMenu] = useState<{ target: Target; at: { x: number; y: number }; configs: ConfigurationActions | null } | null>(null)

  const menuTarget = menu ? view?.groups.flatMap(g=>g.targets).find(target=>targetKey(target)===targetKey(menu.target)) : null
  const menuItems = menu&&menuTarget ? targetMenuItems(menuTarget,act,menu.configs) : []

  const onListKey = (e: React.KeyboardEvent) => {
    if (e.key === 'ContextMenu' || (e.shiftKey && e.key === 'F10')) {
      const cursor = useStore.getState().cursor
      const target = view?.groups.flatMap(g => g.targets).find(x => targetKey(x) === cursor) ?? view?.groups.flatMap(g=>g.targets).find(x=>view.selected && targetKey(x)===targetKey(view.selected))
      if (target && (target.provider==='kubernetes'||target.open||target.connection?.state==='connecting'||target.connection?.state==='connected')) { e.preventDefault(); const r=listRef.current?.querySelector<HTMLElement>('[data-cursor=true]')?.getBoundingClientRect() ?? listRef.current!.getBoundingClientRect(); setMenu({target,at:{x:r.left,y:r.bottom},configs:configRef.current}) }
    } else if (e.key === 'ArrowDown') {
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
    <aside data-area="targets" style={{ width, maxWidth: '30vw' }} className="relative flex h-full shrink-0 flex-col border-r border-line bg-sidebar">
      <PanelResize label={t('panels.targets')} value={width} min={160} max={() => Math.min(420, window.innerWidth * 0.3)} onDone={(targets) => usePanelWidths.setState({ targets })} />
      <label className="target-filter field-shell">
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
        className="target-list min-h-0 flex-1 overflow-y-auto px-2 pb-3 outline-none"
      >
        {view?.groups.map((g) => <Group key={g.provider} group={g} act={act} client={client} configRef={configRef} onMenu={(target, at) => setMenu({ target, at, configs:configRef.current })} />)}
      </div>
      {menu && menuItems.length>0 && (
        <Menu
          items={menuItems}
          at={menu.at}
          label={t('target.menu')}
          onClose={() => setMenu(null)}
        />
      )}
    </aside>
  )
}

/** Source actions and connection controls reflect the target's actual state. */
function targetMenuItems(target: Target, act: Actions, configs: ConfigurationActions | null): MenuItem[] {
  const items: MenuItem[] = []
  if(target.connection?.state==='connecting')items.push({id:'cancel',label:t('connection.cancel'),onSelect:()=>void act.cancelConnect(target,target.connection?.id)})
  if(target.provider==='kubernetes'&&configs){
    if(!target.locked)items.push({id:'inspect',label:t('configs.inspect'),onSelect:()=>configs.openTarget(target,'inspect')},
      {id:'edit',label:t('configs.edit'),onSelect:()=>configs.openTarget(target,'edit')})
    if(!target.id.startsWith('stored:'))items.push({id:'reveal',label:t('configs.reveal'),onSelect:()=>configs.openTarget(target,'reveal')})
    items.push({id:'rename',label:t('configs.rename'),separator:true,onSelect:()=>configs.openTarget(target,'rename')},
      {id:'remove',label:t('configs.remove'),danger:true,onSelect:()=>configs.openTarget(target,'remove')})
  }
  if(target.connection?.state!=='connecting'&&(target.open||target.connection?.state==='connected'))items.push({id:'close',label:t('target.close'),hint:t('target.closeHint'),separator:items.length>0,onSelect:()=>void act.closeTarget(target)})
  return items
}

function Group({ group, act, client, configRef, onMenu }: { group: TargetGroup; act: Actions; client: Client; configRef: React.RefObject<ConfigurationActions | null>; onMenu: (target: Target, at: { x: number; y: number }) => void }) {
  const filter = useStore((s) => s.filter)
  const visible = group.targets.filter((x) => matchesFilter(x, filter))
  return (
    <section className="mt-2" aria-label={group.title}>
      <h2 className="sidebar-group-heading">
        <ProviderIcon provider={group.provider} className="h-4 w-4 text-fg-muted" />
        <span className="flex-1">{group.title}</span>
        <span className="text-xs font-normal text-fg-subtle">{group.targets.length}</span>
      </h2>
      {group.provider === 'kubernetes' && <Configurations ref={configRef} client={client} onChanged={() => act.reload()} />}
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
        <TargetRow key={x.id} target={x} act={act} onMenu={onMenu} />
      ))}
    </section>
  )
}

function TargetRow({ target, act, onMenu }: { target: Target; act: Actions; onMenu: (target: Target, at: { x: number; y: number }) => void }) {
  const key = targetKey(target)
  const selected = useStore((s) => (s.view?.selected ? targetKey(s.view.selected) === key : false))
  const cursor = useStore((s) => s.cursor === key)
  const connectionAction = useStore(s=>s.connectionActions[key])
  const connecting = target.connection?.state==='connecting'||!!connectionAction
  const waiting = useAgents((s) => pendingOf(s.pending, target.provider, target.id))
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
      onContextMenu={
        selected && target.provider !== 'kubernetes' && !target.open && target.connection?.state!=='connecting' && target.connection?.state!=='connected'
          ? undefined
          : (e) => {
              e.preventDefault()
              onMenu(target, { x: e.clientX, y: e.clientY })
            }
      }
      // The "open" hint goes here, not on the dot: a leaf's title or
      // aria-label would join the option's accessible name (name-from-
      // content) and break getByRole({ name }); the row's own title is a
      // fallback only, its content names it.
      title={!selected && target.open ? t('target.openHint') : undefined}
      className={[
        'target-row group flex cursor-default items-center gap-2 rounded-md px-2 py-1.5',
        selected ? 'bg-active' : 'hover:bg-hover',
        cursor && !selected ? 'ring-1 ring-line ring-inset' : '',
      ].join(' ')}
    >
      <span aria-hidden data-connection-state={connecting?'connecting':target.open?'connected':'disconnected'} className={['h-1.5 w-1.5 shrink-0 rounded-full', connecting ? 'bg-warning' : target.open ? 'bg-success' : 'bg-fg-subtle/60'].join(' ')} />
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5">
          <span className="truncate">{target.title}</span>
          {target.locked && <svg aria-hidden data-encrypted-lock className="h-3.5 w-3.5 shrink-0 text-fg-muted" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4"><rect x="3.5" y="7" width="9" height="7" rx="1.5"/><path d="M5.5 7V4.5a2.5 2.5 0 0 1 5 0V7"/><path d="M8 10v2"/></svg>}
          {waiting > 0 && (
            <button
              type="button"
              title={t('agents.waitingHint')}
              aria-label={`${t('agents.waitingHint')}: ${waiting}`}
              className="ml-auto shrink-0 rounded bg-warning/20 px-1 text-[11px] font-semibold text-warning"
              onClick={(e) => {
                e.stopPropagation()
                agents.bringBack()
              }}
            >
              {waiting}
            </button>
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
