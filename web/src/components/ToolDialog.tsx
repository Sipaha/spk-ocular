import { useEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import type { Client } from '../api/client'
import type { ExecInfo, ExecInstance, LogInfo, Ref } from '../api/types'
import { messageText, t } from '../i18n'
import { refTitle } from '../refs'
import { focusMark, restoreFocus } from '../shortcuts'
import { parseArgv } from '../term/argv'
import { Select } from './Select'
import { runningChannel } from './useInstancePicker'

export type ToolMode = 'terminal' | 'logs' | 'files'
export interface ToolSelection { ref: Ref; instance?: ExecInstance; channel?: string; command?: string[] }
interface Props { client: Client; subject: Ref; mode: ToolMode; onOpen: (selection: ToolSelection) => void; onClose: () => void }
const errorText = (error: unknown) => error instanceof Error ? error.message : String(error)

/** Explicit instance/container setup; only terminals accept a command. */
export function ToolDialog({client, subject, mode, onOpen, onClose}: Props) {
  const [exec, setExec] = useState<ExecInfo | null>(null)
  const [logs, setLogs] = useState<LogInfo | null>(null)
  const [selectedLogs, setSelectedLogs] = useState<LogInfo | null>(null)
  const [instance, setInstance] = useState('')
  const [channel, setChannel] = useState('')
  const [command, setCommand] = useState('')
  const [error, setError] = useState('')
  const box = useRef<HTMLFormElement>(null)
  const [mark] = useState(focusMark)
  const title = mode === 'terminal' ? t('term.dialogTitle') : mode === 'logs' ? t('logs.dialogTitle') : t('files.dialogTitle')
  useEffect(() => { box.current?.focus(); return () => restoreFocus(mark) }, [mark])
  useEffect(() => {
    let live = true
    if (mode === 'logs') {
      void client.logInfo(subject).then(info => {
        if (!live) return
        setLogs(info); setInstance(info.aggregate ? '*' : '')
      }, failure => { if (live) setError(errorText(failure)) })
    } else {
      void client.execInfo(subject).then(raw => {
        if (!live) return
        const info = {...raw, instances:(raw.instances ?? []).map(item=>({...item,channels:item.channels ?? []}))}
        setExec(info)
        const selected = info.instances.find(item=>item.id === info.defaultInstance) ?? info.instances[0]
        setInstance(selected?.id ?? ''); setChannel(selected ? runningChannel(selected)?.id ?? '' : '')
      }, failure => { if (live) setError(errorText(failure)) })
    }
    return () => { live = false }
  }, [client, subject, mode])
  const selectedRef = useMemo(() => logs?.instances?.find(item=>(item.ref.uid || item.ref.name) === instance)?.ref ?? subject, [logs, instance, subject])
  useEffect(() => {
    if (!logs) return
    let live = true
    const request = instance === '*' || !logs.aggregate ? Promise.resolve(logs) : client.logInfo(selectedRef)
    void request.then(info => {
      if (!live) return
      setSelectedLogs(info); setChannel(info.defaultChannel)
    }, failure => { if (live) setError(errorText(failure)) })
    return () => { live = false }
  }, [client, logs, instance, selectedRef])
  const selectedExec = exec?.instances.find(item=>item.id === instance)
  const instanceOptions = mode === 'logs'
    ? logs?.aggregate ? [{value:'*',label:logs.allInstancesLabel ? messageText(logs.allInstancesLabel) : t('logs.allPods')}, ...(logs.instances ?? []).map(item=>({value:item.ref.uid || item.ref.name,label:item.title}))] : [{value:'',label:refTitle(subject)}]
    : (exec?.instances ?? []).map(item=>({value:item.id,label:item.ready ? item.title : `${item.title} (${t('term.notReady')})`}))
  const channelOptions = mode === 'logs'
    ? [{value:'*',label:selectedLogs?.allChannelsLabel ? messageText(selectedLogs.allChannelsLabel) : t('logs.allChannels')}, ...(selectedLogs?.channels ?? []).map(item=>({value:item.id,label:item.title}))]
    : (selectedExec?.channels ?? []).map(item=>({value:item.id,label:`${item.title}${item.note ? ` (${item.note})` : ''}${item.running ? '' : ` — ${t('term.notRunning',{state:item.state ?? ''})}`}`,disabled:!item.running}))
  const ready = mode === 'logs' ? !!selectedLogs : !!selectedExec && (!selectedExec.channels.length || selectedExec.channels.some(item=>item.id === channel && item.running))
  const submit = (event: React.FormEvent) => {
    event.preventDefault()
    if (!ready) return
    try {
      const argv = mode === 'terminal' ? parseArgv(command) : []
      onOpen({ref:mode === 'logs' ? selectedRef : subject, instance:selectedExec, channel:channel || undefined, command:argv.length ? argv : undefined})
    } catch (failure) { setError(errorText(failure)) }
  }
  return createPortal(<div className="fixed inset-0 z-50 flex items-start justify-center bg-black/40 pt-24" onMouseDown={event=>{if(event.target === event.currentTarget) onClose()}}>
    <form ref={box} role="dialog" aria-modal="true" aria-label={title} tabIndex={-1} onSubmit={submit}
      className="flex w-[min(560px,90%)] flex-col gap-3 rounded-lg border border-line bg-panel p-4 shadow-2xl outline-none"
      onKeyDown={event=>{
        if(event.key === 'Escape') {event.stopPropagation();onClose()}
        if(event.key === 'Tab') {
          const controls=[...(box.current?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled)') ?? [])]
          const first=controls[0],last=controls.at(-1)
          if(event.shiftKey && (document.activeElement === first || document.activeElement === box.current)) {event.preventDefault();last?.focus()}
          else if(!event.shiftKey && document.activeElement === last) {event.preventDefault();first?.focus()}
        }
      }}>
      <h2 className="font-semibold">{title} · <span className="text-fg-muted">{refTitle(subject)}</span></h2>
      {error && <p role="alert" className="rounded-md bg-danger/10 px-3 py-2 text-xs text-danger">{error}</p>}
      {!exec && !logs && !error && <p className="text-fg-subtle">{t('app.loading')}</p>}
      {exec && !exec.instances.length && <p className="text-fg-subtle">{exec.noInstances ? messageText(exec.noInstances) : t('term.noInstances')}</p>}
      {!!instanceOptions.length && (exec || logs) && <div className="flex flex-col gap-1 text-xs text-fg-muted">
        <span aria-hidden="true">{mode === 'logs' ? logs?.instanceLabel ? messageText(logs.instanceLabel) : t('logs.pod') : exec?.instanceLabel ? messageText(exec.instanceLabel) : t('term.instance')}</span>
        <Select label={mode === 'logs' ? logs?.instanceLabel ? messageText(logs.instanceLabel) : t('logs.pod') : exec?.instanceLabel ? messageText(exec.instanceLabel) : t('term.instance')} value={instance} options={instanceOptions} className="text-sm"
          onChange={value=>{
            setInstance(value); setError('')
            if(mode === 'logs') {setSelectedLogs(null);setChannel('')}
            else {const next=exec?.instances.find(item=>item.id === value);setChannel(next ? runningChannel(next)?.id ?? '' : '')}
          }}/>
      </div>}
      {!!channelOptions.length && (exec || logs) && <div className="flex flex-col gap-1 text-xs text-fg-muted">
        <span aria-hidden="true">{mode === 'logs' ? selectedLogs?.channelLabel ? messageText(selectedLogs.channelLabel) : t('term.channel') : exec?.channelLabel ? messageText(exec.channelLabel) : t('term.channel')}</span>
        <Select label={mode === 'logs' ? selectedLogs?.channelLabel ? messageText(selectedLogs.channelLabel) : t('term.channel') : exec?.channelLabel ? messageText(exec.channelLabel) : t('term.channel')} value={channel} options={channelOptions} disabled={mode === 'logs' && !selectedLogs} onChange={setChannel} className="text-sm"/>
      </div>}
      {mode === 'terminal' && <label className="flex flex-col gap-1 text-xs text-fg-muted">{t('term.commandLabel')}
        <input aria-label={t('term.commandLabel')} autoFocus value={command} onChange={event=>{setCommand(event.target.value);setError('')}} placeholder={t('term.commandPlaceholder')} spellCheck={false} className="rounded-md border border-line bg-app px-2 py-1 text-sm text-fg outline-none focus:border-accent"/>
        <span className="text-fg-subtle">{t('term.commandHelp')}</span>
      </label>}
      <div className="flex justify-end gap-2"><button type="button" className="rounded-md px-3 py-1 text-fg-muted hover:bg-hover" onClick={onClose}>{t('term.cancel')}</button>
        <button type="submit" disabled={!ready} className="rounded-md bg-accent px-3 py-1 text-accent-fg disabled:opacity-50">{t('term.submit')}</button></div>
    </form>
  </div>,document.body)
}
