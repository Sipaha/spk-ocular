import { useEffect, useRef, useState } from 'react'
import type { Client } from '../api/client'
import type { AgentAccessStatus, AgentClientSetup, AgentClientSetupRequest } from '../api/types'
import { t } from '../i18n'
import { useStore } from '../store'
import { copyText } from '../term/clipboard'

/** Read-only discovery; only an explicit setup click may write client settings. */
export function ClientSetup({ client, status }: { client: Client; status: AgentAccessStatus }) {
  const mode = useStore(s => s.info?.mode === 'desktop' ? 'desktop' : 'browser')
  const [rows, setRows] = useState<AgentClientSetup[]>([])
  const [busy, setBusy] = useState(true)
  const [failed, setFailed] = useState(false)
  const [manual, setManual] = useState(false)
  const [copied, setCopied] = useState<string | null>(null)
  const [copyFailed, setCopyFailed] = useState(false)
  const [copying, setCopying] = useState(false)
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)
  const lifetime = useRef({ active: false, client })
  const [owner, setOwner] = useState(client)
  const generation = useRef(0)
  const pending = useRef(false)

  if (owner !== client) {
    setOwner(client)
    setRows([]); setBusy(true); setFailed(false)
    setCopied(null); setCopyFailed(false); setCopying(false)
  }

  useEffect(() => {
    const life = { active: true, client }
    lifetime.current = life
    const id = ++generation.current
    const pendingRef = pending
    pendingRef.current = true
    void client.agentClientSetup({}).then(view => {
      if (life.active && id === generation.current) setRows(view.clients)
    }).catch(() => {
      if (life.active && id === generation.current) setFailed(true)
    }).finally(() => {
      if (life.active && id === generation.current) { pending.current = false; setBusy(false) }
    })
    const timerRef = timer
    return () => { life.active = false; pendingRef.current = false; clearTimeout(timerRef.current) }
  }, [client])

  const request = async (req: AgentClientSetupRequest) => {
    const life = lifetime.current
    if (!life.active || life.client !== client || pending.current) return
    const id = ++generation.current
    pending.current = true
    setBusy(true)
    setFailed(false)
    try {
      const view = await client.agentClientSetup(req)
      if (life.active && id === generation.current) setRows(view.clients)
    } catch {
      // Do not replay a write after an ambiguous failure or reuse a stale revision.
      if (life.active && id === generation.current) { setRows([]); setFailed(true) }
    } finally {
      if (life.active && id === generation.current) { pending.current = false; setBusy(false) }
    }
  }

  return <div className="space-y-3">
    <div className="flex flex-wrap items-center justify-between gap-2">
      <p className="text-fg-muted">{t('agents.setupHint')}</p>
      <button type="button" className="agents-button" disabled={busy} onClick={() => void request({})}>{t('agents.detectClients')}</button>
    </div>
    <div aria-busy={busy} className="space-y-2">
      {busy && <p role="status">{t('agents.setupChecking')}</p>}
      {rows.map(row => <div key={row.client} className="agents-client-row" role="group" aria-label={row.client === 'codex' ? 'Codex' : 'Claude Code'}>
        <div className="min-w-0 flex-1">
          <strong>{row.client === 'codex' ? 'Codex' : 'Claude Code'}</strong>
          <p className="text-fg-muted">{t(`agents.setupState.${row.state}`)}</p>
          {row.path && <p className="break-all font-mono text-fg-subtle">{row.path}</p>}
        </div>
        {row.state === 'ready' && <button type="button" className="agents-button" disabled={busy || !row.revision} onClick={() => void request({ client: row.client, install: true, revision: row.revision })}>
          {t('agents.setupClient', { client: row.client === 'codex' ? 'Codex' : 'Claude Code' })}
        </button>}
      </div>)}
    </div>
    {failed && <p role="alert" className="text-danger">{t('agents.setupFailed')}</p>}
    <details onToggle={e => setManual(e.currentTarget.open)}>
      <summary className="cursor-pointer text-fg-muted">{t('agents.manualSetup')}</summary>
      {manual && <div className="mt-2 space-y-3">
        <p className="text-fg-muted" data-instruction>{t('agents.instruction')}</p>
        {(['codex', 'claude'] as const).map(name => {
          const config = name === 'codex' ? status.codexConfig : status.claudeConfig
          const label = t(name === 'codex' ? 'agents.copyCodex' : 'agents.copyClaude')
          return <div key={name}>
            <div className="mb-1 flex flex-wrap items-center justify-between gap-2">
              <strong>{name === 'codex' ? 'Codex · TOML' : 'Claude Code · JSON'}</strong>
              <button type="button" className="agents-button" aria-label={label} disabled={!config || copying} onClick={() => {
                if (!config || copying) return
                const life = lifetime.current
                clearTimeout(timer.current)
                setCopied(null); setCopyFailed(false); setCopying(true)
                void copyText(config, mode, true).then(() => {
                  if (!life.active) return
                  setCopied(name)
                  timer.current = setTimeout(() => setCopied(null), 1500)
                }).catch(() => { if (life.active) setCopyFailed(true) })
                  .finally(() => { if (life.active) setCopying(false) })
              }}>{copied === name ? t('agents.copied') : label}</button>
            </div>
            <pre className="agents-client-config" data-client-config={name}>{config}</pre>
          </div>
        })}
        {copyFailed && <p role="alert" className="text-danger">{t('agents.copyFailed')}</p>}
      </div>}
    </details>
  </div>
}
