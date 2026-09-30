import { useEffect, useMemo, useState } from 'react'
import type { Client } from '../api/client'
import type { ExecInfo, Ref } from '../api/types'
import { messageText, t } from '../i18n'
import { ArgvError, parseArgv } from './argv'
import type { TermOpen } from '../dock/store'
import { refTitle } from '../refs'

interface Props {
  client: Client
  subject: Ref
  onOpen: (open: TermOpen) => void
  onClose: () => void
}

const errText = (e: unknown) => (e instanceof Error ? e.message : String(e))

/** Choose the pod (for a workload), the container and a command. */
export function TerminalDialog({ client, subject, onOpen, onClose }: Props) {
  const [info, setInfo] = useState<ExecInfo | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [instance, setInstance] = useState('')
  const [channel, setChannel] = useState('')
  const [command, setCommand] = useState('')
  const [parseError, setParseError] = useState<string | null>(null)

  useEffect(() => {
    let live = true
    client.execInfo(subject).then(
      (i) => {
        if (!live) return
        setInfo(i)
        const inst = i.instances.find((x) => x.id === i.defaultInstance) ?? i.instances[0]
        setInstance(inst?.id ?? '')
        setChannel(inst?.defaultChannel ?? '')
      },
      (e) => live && setError(errText(e)),
    )
    return () => {
      live = false
    }
  }, [client, subject])

  const inst = useMemo(() => info?.instances.find((x) => x.id === instance), [info, instance])

  const submit = (e: React.FormEvent) => {
    e.preventDefault()
    let argv: string[]
    try {
      argv = parseArgv(command)
    } catch (err) {
      setParseError(err instanceof ArgvError ? err.message : errText(err))
      return
    }
    onOpen({ ref: subject, instance: instance || undefined, channel: channel || undefined, command: argv.length ? argv : undefined })
  }

  return (
    <div className="absolute inset-0 z-20 flex items-start justify-center bg-black/40 pt-24" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <form
        role="dialog"
        aria-label={t('term.dialogTitle')}
        onSubmit={submit}
        onKeyDown={(e) => e.key === 'Escape' && (e.stopPropagation(), onClose())}
        className="flex w-[min(560px,90%)] flex-col gap-3 rounded-lg border border-line bg-panel p-4 shadow-2xl"
      >
        <h2 className="font-semibold">
          {t('term.dialogTitle')} · <span className="text-fg-muted">{refTitle(subject)}</span>
        </h2>
        {error && (
          <p role="alert" className="rounded-md bg-danger/10 px-3 py-2 text-xs text-danger">
            {error}
          </p>
        )}
        {!info && !error && <p className="text-fg-subtle">{t('app.loading')}</p>}
        {info && info.instances.length === 0 && <p className="text-fg-subtle">{info.noInstances ? messageText(info.noInstances) : t('term.noInstances')}</p>}
        {info && info.instances.length > 0 && (
          <>
            {info.instances.length > 1 && (
              <label className="flex flex-col gap-1 text-xs text-fg-muted">
                {info.instanceLabel ? messageText(info.instanceLabel) : t('term.instance')}
                <select
                  value={instance}
                  onChange={(e) => {
                    setInstance(e.target.value)
                    setChannel(info.instances.find((x) => x.id === e.target.value)?.defaultChannel ?? '')
                  }}
                  className="rounded-md border border-line bg-app px-2 py-1 text-sm text-fg outline-none focus:border-accent"
                >
                  {info.instances.map((x) => (
                    <option key={x.id} value={x.id}>
                      {x.title}
                      {x.ready ? '' : ` (${t('term.notReady')})`}
                    </option>
                  ))}
                </select>
              </label>
            )}
            <label className="flex flex-col gap-1 text-xs text-fg-muted">
              {info.channelLabel ? messageText(info.channelLabel) : t('term.channel')}
              <select value={channel} onChange={(e) => setChannel(e.target.value)} className="rounded-md border border-line bg-app px-2 py-1 text-sm text-fg outline-none focus:border-accent">
                {inst?.channels.map((c) => (
                  <option key={c.id} value={c.id} disabled={!c.running}>
                    {c.title}
                    {c.note ? ` (${c.note})` : ''}
                    {c.running ? '' : ` — ${t('term.notRunning', { state: c.state ?? '' })}`}
                  </option>
                ))}
              </select>
            </label>
          </>
        )}
        <label className="flex flex-col gap-1 text-xs text-fg-muted">
          {t('term.commandLabel')}
          <input
            autoFocus
            value={command}
            onChange={(e) => {
              setCommand(e.target.value)
              setParseError(null)
            }}
            placeholder={t('term.commandPlaceholder')}
            spellCheck={false}
            className="rounded-md border border-line bg-app px-2 py-1 font-mono text-sm text-fg outline-none focus:border-accent"
          />
          <span className="text-fg-subtle">{t('term.commandHelp')}</span>
        </label>
        {parseError && (
          <p role="alert" className="text-xs text-danger">
            {parseError}
          </p>
        )}
        <div className="flex justify-end gap-2">
          <button type="button" className="rounded-md px-3 py-1 text-fg-muted hover:bg-hover" onClick={onClose}>
            {t('term.cancel')}
          </button>
          <button type="submit" disabled={!info || info.instances.length === 0} className="rounded-md bg-accent px-3 py-1 text-accent-fg disabled:opacity-50">
            {t('term.submit')}
          </button>
        </div>
      </form>
    </div>
  )
}
