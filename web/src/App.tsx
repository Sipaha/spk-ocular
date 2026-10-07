import { useEffect, useMemo, useState, useRef } from 'react'
import type { Client } from './api/client'
import { setLanguage, t } from './i18n'
import { actions, selectedTarget, targetKey, useStore } from './store'
import { ViewHub } from './views/viewSync'
import { Workspace } from './components/Workspace'
import { ConnectionWorkspace } from './components/ConnectionWorkspace'
import type { ConfigurationActions } from './configurations/Configurations'
import { Sidebar } from './components/Sidebar'
import { AppHeader } from './components/AppHeader'
import { StatusBar } from './components/StatusBar'
import { EmptyWorkspace } from './components/TargetDetails'
import { Dock } from './dock/Dock'
import { dock } from './dock/store'
import { TunnelsPanel } from './tunnels/TunnelsPanel'
import { tunnelLoader } from './tunnels/store'
import { Palette } from './palette/Palette'
import { openPalette } from './palette/store'
import { cycleArea, globalShortcut } from './shortcuts'
import { AboutDialog } from './components/AboutDialog'
import { HelpDialog } from './components/HelpDialog'
import { editsHeld, mayLeave, useEditHolder } from './edit/guard'
import { DiscardPrompt } from './edit/DiscardPrompt'
import { AgentsPanel } from './agents/AgentsPanel'
import { AgentConfirm } from './agents/ConfirmDialog'
import { agentLoaders, agents, useAgents } from './agents/store'

export function App({ client }: { client: Client }) {
  const act = useMemo(() => actions(client), [client])
  const configRef = useRef<ConfigurationActions>(null)
  const connect = () => {
    if (!target) return
    if (target.locked) configRef.current?.unlockTarget(target, async () => {
      const current = useStore.getState().view?.groups.flatMap(g=>g.targets) ?? []
      const exact = current.find(t=>targetKey(t)===targetKey(target)&&!t.locked)
      const candidates = current.filter(t=>t.provider===target.provider&&t.id.startsWith(target.id)&&!t.locked)
      const resolved = exact ?? (candidates.length===1 ? candidates[0] : undefined)
      if (resolved) { await act.select(resolved); await act.connect(resolved) }
      else useStore.setState({actionError:t('configs.chooseAfterUnlock')})
    })
    else void act.connect(target)
  }
  const hub = useMemo(() => new ViewHub(client), [client])
  const loadTunnels = useMemo(() => tunnelLoader(client), [client])
  const loadAgents = useMemo(() => agentLoaders(client), [client])
  const waiting = useAgents((s) => s.pending.length)
  const info = useStore((s) => s.info)
  const view = useStore((s) => s.view)
  const target = useStore((s) => selectedTarget(s.view))
  const connectionAction = useStore((s) => target ? s.connectionActions[targetKey(target)] : undefined)
  // An automatic disconnect must not unmount an editor holding unsaved work.
  useEditHolder()
  const connected = target?.connection?.state === 'connected' && connectionAction !== 'cancelling'
  const [workspaceKey, setWorkspaceKey] = useState<string | null>(null)
  const key = target ? targetKey(target) : null
  const reconnecting = !connected && !!key && workspaceKey === key && target?.connection?.state === 'connecting'
  const held = !connected && !reconnecting && !!key && workspaceKey === key && editsHeld()
  const showWorkspace = connected || reconnecting || held
  const nextWorkspaceKey = showWorkspace ? key : null
  if (workspaceKey !== nextWorkspaceKey) setWorkspaceKey(nextWorkspaceKey)
  const loadError = useStore((s) => s.loadError)
  const targetProvider = target?.provider
  const targetId = target?.id

  // A selected target: the bottom panel's height is remembered per target.
  // Its log and terminal tabs stay across switches (recent targets keep
  // their sessions in the background; a closed session ends its log tabs).
  useEffect(() => {
    if (!targetProvider || !targetId) return
    let live = true
    client.getTargetState(targetProvider, targetId).then(
      (st) => {
        const h = Number(st.logsHeight)
        if (live && h > 0) dock.setHeight(h)
      },
      () => {},
    )
    return () => {
      live = false
    }
  }, [client, targetProvider, targetId])

  useEffect(() => {
    const off = client.subscribeEvents((e) => {
      if (e.type === 'view_changed') hub.onViewChanged(e.payload)
      if (e.type === 'kinds_changed') hub.onKindsChanged(e.payload)
      if (e.type === 'resync') hub.resyncAll()
      if (e.type === 'targets_changed' || e.type === 'resync') void act.reload()
      if (e.type === 'forwards_changed' || e.type === 'resync') void loadTunnels()
      if (e.type === 'agent_grants_changed' || e.type === 'resync') void loadAgents.grants()
      if (e.type === 'agent_pending_changed' || e.type === 'resync') void loadAgents.pending()
      if (e.type === 'agent_audit_changed' || e.type === 'resync') agents.auditChanged()
    })
    void act.init()
    void loadTunnels()
    void loadAgents.grants()
    void loadAgents.pending()
    return off
  }, [client, act, hub, loadTunnels, loadAgents])

  // Plans waiting for the user show in the title (the taskbar, another tab).
  useEffect(() => {
    const base = 'SPK Ocular'
    document.title = waiting ? `(${waiting}) ${base}` : base
  }, [waiting])

  // The app-wide keys (shortcuts.ts): not a terminal's, not under a modal dialog.
  const [help, setHelp] = useState(false)
  const [about, setAbout] = useState(false)
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const id = globalShortcut(e)
      if (!id) return
      e.preventDefault()
      if (id === 'palette') openPalette()
      else if (id === 'help') setHelp(true)
      else if (id === 'nextArea' || id === 'prevArea') cycleArea(id === 'nextArea' ? 1 : -1)
      else if (id === 'resync') document.querySelector<HTMLButtonElement>('[data-resync]')?.click()
      else if (id === 'refreshKinds') document.querySelector<HTMLButtonElement>('[data-refresh-kinds]')?.click()
      else {
        // The open table's filter wins over the contexts filter.
        const el = document.querySelector<HTMLInputElement>('[data-primary-filter]') ?? document.querySelector<HTMLInputElement>('[data-target-filter]')
        el?.focus()
        el?.select()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])

  // Language before the first paint of real content: the app renders the
  // loading state until AppInfo arrives.
  if (info) setLanguage(info.language)

  if (!view) {
    return (
      <div className="flex h-full items-center justify-center text-fg-muted">
        {loadError ? (
          <div className="flex flex-col items-center gap-3">
            <p role="alert">{t('app.loadFailed', { error: loadError })}</p>
            <button className="rounded-md bg-accent px-3 py-1 text-accent-fg" onClick={() => void act.init()}>
              {t('app.retry')}
            </button>
          </div>
        ) : (
          t('app.loading')
        )}
      </div>
    )
  }

  return (
    <div className="relative flex h-full flex-col">
      <AppHeader client={client} target={target} onHelp={() => setHelp(true)} onAbout={() => setAbout(true)} />
      <div className="flex min-h-0 flex-1">
        <Sidebar act={act} client={client} configRef={configRef} />
        <div className="flex min-w-0 flex-1 flex-col">
          {reconnecting && <div role="status" className="border-b border-line px-3 py-2 text-sm text-fg-muted">{t('connection.connecting')}</div>}
          {held && target && <div role="status" className="border-b border-line px-3 py-2 text-sm text-fg-muted">
            {t('connection.disconnected')}
            <button className="ml-3 text-accent" onClick={() => mayLeave(() => { connect() })}>{t('connection.connect')}</button>
          </div>}
          {target ? (
            showWorkspace ?
              <Workspace key={`${target.provider}/${target.id}`} client={client} hub={hub} target={target} onFavorite={act.setKindFavorite} onMoveFavorite={act.moveFavoriteKind} onNavSection={act.setNavSection} /> :
              <ConnectionWorkspace key={targetKey(target)} target={target} pending={connectionAction} onConnect={() => connect()} onCancel={() => void act.cancelConnect(target, target.connection?.id)} />
          ) : (
            <main className="min-h-0 min-w-0 flex-1 overflow-y-auto">
              <EmptyWorkspace />
            </main>
          )}
          <Dock
            client={client}
            current={target ? { provider: target.provider, id: target.id } : null}
            mode={info?.mode === 'desktop' ? 'desktop' : 'browser'}
            onHeightDone={(h) => {
              if (target) void client.setTargetState(target.provider, target.id, 'logsHeight', String(Math.round(h))).catch(() => {})
            }}
          />
        </div>
      </div>
      <TunnelsPanel client={client} mode={info?.mode === 'desktop' ? 'desktop' : 'browser'} />
      <AgentsPanel client={client} />
      <AgentConfirm client={client} reload={loadAgents.pending} />
      <Palette client={client} act={act} />
      {about && <AboutDialog info={info} onClose={() => setAbout(false)} />}
      {help && <HelpDialog onClose={() => setHelp(false)} />}
      <DiscardPrompt />
      <StatusBar />
    </div>
  )
}
