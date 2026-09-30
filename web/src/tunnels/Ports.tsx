import { useEffect, useState } from 'react'
import type { Client } from '../api/client'
import { ApiError } from '../api/client'
import type { ForwardInfo, ForwardPort, Ref } from '../api/types'
import { Select } from '../components/Select'
import { classLabel, t } from '../i18n'
import { tunnels } from './store'

const errText = (e: unknown) => (e instanceof ApiError ? `${classLabel(e.code)}: ${e.detail}` : e instanceof Error ? e.message : String(e))

/** The details' "Ports" section: what can be forwarded, with "Forward". */
export function PortsSection({ client, subject }: { client: Client; subject: Ref }) {
  const [info, setInfo] = useState<ForwardInfo | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [dialog, setDialog] = useState<{ port: ForwardPort | null } | null>(null)
  useEffect(() => {
    let live = true
    client.forwardInfo(subject).then(
      (i) => live && setInfo(i),
      (e) => live && setError(errText(e)),
    )
    return () => {
      live = false
    }
  }, [client, subject])

  return (
    <section aria-label={t('fwd.ports')}>
      <h3 className="mb-1.5 text-[12px] font-semibold uppercase tracking-wider text-fg-subtle">{t('fwd.ports')}</h3>
      {error && <p className="text-xs text-danger">{error}</p>}
      {info?.unsupported && <p className="text-xs text-fg-muted">{info.unsupported}</p>}
      {info && !info.unsupported && (
        <ul className="flex flex-col gap-1">
          {info.ports.map((p) => (
            <li key={`${p.protocol}/${p.port}`} className="flex items-center gap-2 text-xs">
              <code className="w-14 font-mono text-fg">{p.port}</code>
              <span className="text-fg-muted">{[p.name, p.protocol !== 'TCP' ? p.protocol : '', p.note].filter(Boolean).join(' · ')}</span>
              {p.supported ? (
                <button className="ml-auto rounded-md border border-line px-2 py-0.5 text-fg-muted hover:bg-hover hover:text-fg" onClick={() => setDialog({ port: p })}>
                  {t('fwd.forward')}
                </button>
              ) : (
                <span className="ml-auto text-fg-subtle" title={p.reason}>
                  {t('fwd.unsupported')}
                </span>
              )}
            </li>
          ))}
          {info.ports.length === 0 && <li className="text-xs text-fg-subtle">{t('fwd.noPorts')}</li>}
          {info.anyPort && (
            <li>
              <button className="rounded-md border border-line px-2 py-0.5 text-xs text-fg-muted hover:bg-hover hover:text-fg" onClick={() => setDialog({ port: null })}>
                {t('fwd.otherPort')}
              </button>
            </li>
          )}
        </ul>
      )}
      {dialog && <ForwardDialog client={client} subject={subject} port={dialog.port} onClose={() => setDialog(null)} />}
    </section>
  )
}

/** Local port (default: the remote one if free, else any) and whether it is HTTP. */
export function ForwardDialog({ client, subject, port, onClose }: { client: Client; subject: Ref; port: ForwardPort | null; onClose: () => void }) {
  const [remote, setRemote] = useState(port ? String(port.port) : '')
  const [local, setLocal] = useState('')
  const [scheme, setScheme] = useState(port?.scheme ?? '')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const num = (s: string) => (/^\d+$/.test(s.trim()) ? Number(s.trim()) : NaN)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    const r = num(remote)
    const l = local.trim() ? num(local) : 0
    if (!(r >= 1 && r <= 65535) || !(l >= 0 && l <= 65535)) {
      setError(t('fwd.badPort'))
      return
    }
    setBusy(true)
    setError(null)
    try {
      await client.startForward({ ref: subject, port: r, localPort: l || undefined, scheme: scheme || undefined })
      tunnels.showPanel(true)
      onClose()
    } catch (err) {
      setError(errText(err))
      setBusy(false)
    }
  }

  return (
    <div className="fixed inset-0 z-40 flex items-start justify-center bg-black/40 pt-24" onMouseDown={(e) => e.target === e.currentTarget && !busy && onClose()}>
      <form
        role="dialog"
        aria-label={t('fwd.dialogTitle')}
        onSubmit={submit}
        onKeyDown={(e) => e.key === 'Escape' && (e.stopPropagation(), onClose())}
        className="flex w-[min(460px,90%)] flex-col gap-3 rounded-lg border border-line bg-panel p-4 text-sm shadow-2xl"
      >
        <h2 className="font-semibold">
          {t('fwd.dialogTitle')} · <span className="text-fg-muted">{subject.name}</span>
        </h2>
        <label className="flex flex-col gap-1 text-xs text-fg-muted">
          {t('fwd.remotePort')}
          <input
            value={remote}
            onChange={(e) => setRemote(e.target.value)}
            readOnly={!!port}
            autoFocus={!port}
            inputMode="numeric"
            className="rounded-md border border-line bg-app px-2 py-1 font-mono text-sm text-fg outline-none focus:border-accent read-only:opacity-70"
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-fg-muted">
          {t('fwd.localPort')}
          <input
            value={local}
            onChange={(e) => setLocal(e.target.value)}
            autoFocus={!!port}
            inputMode="numeric"
            placeholder={t('fwd.localAuto')}
            className="rounded-md border border-line bg-app px-2 py-1 font-mono text-sm text-fg outline-none focus:border-accent"
          />
        </label>
        <div className="flex flex-col gap-1 text-xs text-fg-muted">
          <span aria-hidden="true">{t('fwd.scheme')}</span>
          <Select
            label={t('fwd.scheme')}
            value={scheme}
            onChange={setScheme}
            options={[
              { value: '', label: t('fwd.schemeNone') },
              { value: 'http', label: 'http' },
              { value: 'https', label: 'https' },
            ]}
            className="text-sm"
          />
        </div>
        {error && (
          <p role="alert" className="rounded-md bg-danger/10 px-3 py-2 text-xs text-danger">
            {error}
          </p>
        )}
        <div className="flex justify-end gap-2">
          <button type="button" className="rounded-md px-3 py-1 text-fg-muted hover:bg-hover" onClick={onClose} disabled={busy}>
            {t('term.cancel')}
          </button>
          <button type="submit" disabled={busy} className="rounded-md bg-accent px-3 py-1 text-accent-fg disabled:opacity-50">
            {busy ? t('fwd.starting') : t('fwd.start')}
          </button>
        </div>
      </form>
    </div>
  )
}
