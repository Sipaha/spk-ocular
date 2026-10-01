import { useEffect, useMemo, useState } from 'react'
import type { Client } from './api/client'
import { setLanguage, t } from './i18n'
import { actions, selectedTarget, useStore } from './store'
import { ViewHub } from './views/viewSync'
import { Workspace } from './components/Workspace'
import { Sidebar } from './components/Sidebar'
import { StatusBar } from './components/StatusBar'
import { TargetDetails } from './components/TargetDetails'
import { Dock } from './dock/Dock'
import { dock } from './dock/store'
import { TunnelsPanel } from './tunnels/TunnelsPanel'
import { tunnelLoader } from './tunnels/store'
import { Palette } from './palette/Palette'
import { openPalette } from './palette/store'
import { cycleArea, globalShortcut } from './shortcuts'
import { HelpDialog } from './components/HelpDialog'
import { DiscardPrompt } from './edit/DiscardPrompt'
import { AgentsPanel } from './agents/AgentsPanel'
import { AgentConfirm } from './agents/ConfirmDialog'
import { agentLoaders, agents, useAgents } from './agents/store'

export function App({ client }: { client: Client }) {
  const act = useMemo(() => actions(client), [client])
  const hub = useMemo(() => new ViewHub(client), [client])
  const loadTunnels = useMemo(() => tunnelLoader(client), [client])
  const loadAgents = useMemo(() => agentLoaders(client), [client])
  const waiting = useAgents((s) => s.pending.length)
  const info = useStore((s) => s.info)
  const view = useStore((s) => s.view)
  const target = useStore((s) => selectedTarget(s.view))
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
      <div className="flex min-h-0 flex-1">
        <Sidebar act={act} />
        <div className="flex min-w-0 flex-1 flex-col">
          {target ? (
            <Workspace key={`${target.provider}/${target.id}`} client={client} hub={hub} target={target} />
          ) : (
            <main className="min-h-0 min-w-0 flex-1 overflow-y-auto">
              <TargetDetails />
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
      {help && <HelpDialog onClose={() => setHelp(false)} />}
      <DiscardPrompt />
      <StatusBar />
    </div>
  )
}
