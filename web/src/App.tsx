import { useEffect, useMemo } from 'react'
import type { Client } from './api/client'
import { setLanguage, t } from './i18n'
import { actions, useStore } from './store'
import { Sidebar } from './components/Sidebar'
import { StatusBar } from './components/StatusBar'
import { TargetDetails } from './components/TargetDetails'

export function App({ client }: { client: Client }) {
  const act = useMemo(() => actions(client), [client])
  const info = useStore((s) => s.info)
  const view = useStore((s) => s.view)
  const loadError = useStore((s) => s.loadError)

  useEffect(() => {
    const off = client.subscribeEvents((e) => {
      if (e.type === 'targets_changed' || e.type === 'resync') void act.reload()
    })
    void act.init()
    return off
  }, [client, act])

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
        <main className="min-w-0 flex-1 overflow-y-auto">
          <TargetDetails />
        </main>
      </div>
      <StatusBar />
    </div>
  )
}
