import { useState } from 'react'
import type { Client } from '../api/client'
import type { Tunnel } from '../api/types'
import { classLabel, t } from '../i18n'
import { formatBytes } from '../format'
import { copyText } from '../term/clipboard'
import { Reconfigured } from '../dock/Dock'
import { reconfigured, useStore } from '../store'
import { openURL } from './open'
import { tunnels, tunnelURL, useTunnels } from './store'
import { refTitle } from '../refs'

interface Props {
  client: Client
  mode: 'desktop' | 'browser'
}

const stateColor: Record<Tunnel['state'], string> = {
  ready: 'bg-success',
  connecting: 'bg-warning',
  idle: 'bg-fg-subtle',
  error: 'bg-danger',
}

/** The status bar's "⇄ N": the tunnels of the app, in any target. */
export function TunnelsIndicator() {
  const n = useTunnels((s) => s.list.length)
  const failing = useTunnels((s) => s.list.some((x) => x.state === 'error'))
  const open = useTunnels((s) => s.panel)
  if (!n && !open) return null
  return (
    <button
      className={['rounded px-1.5 hover:bg-hover hover:text-fg', failing ? 'text-danger' : 'text-fg-muted'].join(' ')}
      onClick={() => tunnels.showPanel(!open)}
      aria-expanded={open}
      title={t('fwd.indicator')}
    >
      ⇄ {n}
    </button>
  )
}

export function TunnelsPanel({ client, mode }: Props) {
  const { list, panel, error } = useTunnels()
  if (!panel) return null
  return (
    <section
      aria-label={t('fwd.panel')}
      className="absolute bottom-7 right-2 z-30 flex max-h-[60vh] w-[min(760px,95vw)] flex-col rounded-lg border border-line bg-panel shadow-2xl"
    >
      <header className="flex items-center gap-2 border-b border-line px-3 py-1.5">
        <h2 className="text-xs font-semibold">{t('fwd.panel')}</h2>
        <span className="text-xs text-fg-subtle">{list.length}</span>
        <button className="ml-auto rounded px-2 text-fg-muted hover:bg-hover hover:text-fg" onClick={() => tunnels.showPanel(false)} aria-label={t('drawer.close')}>
          ×
        </button>
      </header>
      {error && (
        <p role="alert" className="px-3 py-1 text-xs text-danger">
          {error}
        </p>
      )}
      {list.length === 0 && <p className="px-3 py-4 text-center text-xs text-fg-subtle">{t('fwd.none')}</p>}
      <ul className="min-h-0 overflow-y-auto">
        {list.map((tn) => (
          <TunnelRow key={tn.id} tn={tn} client={client} mode={mode} />
        ))}
      </ul>
    </section>
  )
}

function TunnelRow({ tn, client, mode }: { tn: Tunnel; client: Client; mode: 'desktop' | 'browser' }) {
  const [copied, setCopied] = useState(false)
  const [stopError, setStopError] = useState<string | null>(null)
  const ref = tn.target.ref
  const stale = useStore((s) => reconfigured(s.view, tn.target.provider, tn.target.target, tn.target.configRev))
  const what = `${ref.kind.split('/').pop()}/${refTitle(ref)}:${tn.target.port}`
  const addr = tn.addresses[0]
  return (
    <li className="flex flex-col gap-0.5 border-b border-line px-3 py-2 text-xs last:border-b-0" aria-label={what}>
      <div className="flex items-center gap-2">
        <span className={`h-2 w-2 shrink-0 rounded-full ${stateColor[tn.state]}`} title={t(`fwd.state.${tn.state}`)} />
        <code className="shrink-0 whitespace-nowrap font-mono text-fg" data-address>
          {tn.addresses.join('  ')}
        </code>
        <span className="text-fg-subtle">→</span>
        <span className="min-w-0 truncate text-fg-muted" title={tn.target.endpoint ? `${tn.target.targetTitle} · ${tn.target.endpoint}` : tn.target.targetTitle}>
          {what}
        </span>
        <span className="shrink-0 rounded bg-hover px-1 text-[11px] text-fg-muted">{tn.target.targetTitle}</span>
        {stale && <Reconfigured />}
        <span className="ml-auto flex shrink-0 gap-1">
          <button
            className="rounded-md border border-line px-2 py-0.5 hover:bg-hover"
            onClick={() => {
              void copyText(addr, mode)
              setCopied(true)
              setTimeout(() => setCopied(false), 1500)
            }}
          >
            {copied ? t('fwd.copied') : t('fwd.copy')}
          </button>
          {tn.scheme && (
            <button className="rounded-md border border-line px-2 py-0.5 hover:bg-hover" onClick={() => void openURL(tunnelURL(tn), mode)} title={tunnelURL(tn)}>
              {t('fwd.openURL')}
            </button>
          )}
          <button
            className="rounded-md border border-line px-2 py-0.5 text-danger hover:bg-danger/10"
            onClick={() => client.stopForward(tn.id).catch((e) => setStopError(e instanceof Error ? e.message : String(e)))}
          >
            {t('fwd.stop')}
          </button>
        </span>
      </div>
      <div className="flex flex-wrap gap-x-3 pl-4 text-fg-subtle">
        <span>{t(`fwd.state.${tn.state}`)}</span>
        {tn.upstream && <span>{t('fwd.via', { upstream: tn.upstream })}</span>}
        <span>{t('fwd.conns', { conns: tn.conns, served: tn.served })}</span>
        <span>
          ↓ {formatBytes(tn.bytesIn)} ↑ {formatBytes(tn.bytesOut)}
        </span>
        {tn.rejected > 0 && <span className="text-warning">{t('fwd.rejected', { n: tn.rejected })}</span>}
        {tn.ipv6 !== 'ok' && (
          <span className="text-warning" title={tn.ipv6Detail}>
            {t(tn.ipv6 === 'busy' ? 'fwd.ipv6Busy' : 'fwd.ipv6None')}
          </span>
        )}
      </div>
      {tn.lastError && (
        <div role="alert" className="pl-4 text-danger">
          {classLabel(tn.lastError.class)}: {tn.lastError.message}
        </div>
      )}
      {stopError && <div className="pl-4 text-danger">{stopError}</div>}
    </li>
  )
}
