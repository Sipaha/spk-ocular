import { useEffect, useMemo } from 'react'
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

export function App({ client }: { client: Client }) {
  const act = useMemo(() => actions(client), [client])
  const hub = useMemo(() => new ViewHub(client), [client])
  const info = useStore((s) => s.info)
  const view = useStore((s) => s.view)
  const target = useStore((s) => selectedTarget(s.view))
  const loadError = useStore((s) => s.loadError)
  const targetProvider = target?.provider
  const targetId = target?.id

  // Another target: its log tabs close (their session goes), terminals stay;
  // the panel height is remembered per target.
  useEffect(() => {
    dock.keepLogsOf(targetProvider && targetId ? { provider: targetProvider, id: targetId } : null)
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
      if (e.type === 'resync') hub.resyncAll()
      if (e.type === 'targets_changed' || e.type === 'resync') void act.reload()
    })
    void act.init()
    return off
  }, [client, act, hub])

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
    <div className="flex h-full flex-col">
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
      <StatusBar />
    </div>
  )
}
