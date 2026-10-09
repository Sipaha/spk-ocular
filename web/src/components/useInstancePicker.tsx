import { useCallback, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import type { Client } from '../api/client'
import type { ExecInstance, Ref } from '../api/types'
import { messageText, t } from '../i18n'
import { showNotice } from '../store'
import { getExecInfo, getLogInfo } from './toolInfo'
import { SelectList, type SelectOption } from './Select'

interface Choice extends SelectOption { choose: () => void }
interface Picker { context: string; anchor: { current: HTMLElement }; label: string; choices: Choice[]; loading: boolean }

/** Resolve once per click; cancellation and stale responses never open a tool. */
export function useInstancePicker(client: Client, context: string) {
  const [picker, setPicker] = useState<Picker | null>(null)
  const sequence = useRef({value:0})
  const opener = useRef<HTMLElement | null>(null)
  useEffect(() => {
    const token = sequence.current
    token.value++
    return () => { token.value++ }
  }, [context])
  const close = useCallback((back: boolean) => {
    sequence.current.value++
    if (back && opener.current?.isConnected) opener.current.focus()
    setPicker(null)
  }, [])
  useEffect(() => {
    if (!picker?.loading || picker.context !== context) return
    const down = (event: MouseEvent) => { if (!picker.anchor.current.contains(event.target as Node)) close(false) }
    const key = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); close(true) }
    }
    document.addEventListener('mousedown', down, true)
    document.addEventListener('keydown', key, true)
    return () => { document.removeEventListener('mousedown', down, true); document.removeEventListener('keydown', key, true) }
  }, [picker, context, close])
  const pick = useCallback(async (load: () => Promise<{label: string; choices: Choice[]; direct?: () => void}>) => {
    const seq = ++sequence.current.value
    const anchor = { current: document.activeElement instanceof HTMLElement ? document.activeElement : document.body }
    opener.current = anchor.current
    setPicker({ context, anchor, label: t('term.instance'), choices: [], loading: true })
    try {
      const result = await load()
      if (seq !== sequence.current.value || !anchor.current.isConnected) return
      if (result.direct) {
        setPicker(null)
        anchor.current.focus()
        result.direct()
      } else setPicker({context, anchor, label:result.label, choices:result.choices, loading:false})
    } catch (error) {
      if (seq !== sequence.current.value) return
      close(true)
      showNotice(error instanceof Error ? error.message : String(error))
    }
  }, [close, context])
  const pickExec = useCallback((ref: Ref, open: (instance: ExecInstance) => void) => {
    void pick(async () => {
      const info = await getExecInfo(client, ref)
      const instances = info.instances ?? []
      if (!instances.length) throw new Error(info.noInstances ? messageText(info.noInstances) : t('term.noInstances'))
      const only = instances[0]
      if (!info.aggregate && instances.length === 1 && only.channels?.length) {
        const channels = only.channels.filter(channel => channel.running)
        if (!channels.length) throw new Error(t('term.noInstances'))
        const choose = (id: string) => open({...only, defaultChannel:id})
        return {
          label: info.channelLabel ? messageText(info.channelLabel) : t('term.channel'),
          choices: channels.map(channel=>({value:channel.id,label:channel.title,choose:()=>choose(channel.id)})),
          direct: channels.length === 1 ? ()=>choose(channels[0].id) : undefined,
        }
      }
      return {
        label: info.instanceLabel ? messageText(info.instanceLabel) : t('term.instance'),
        choices: instances.map(instance => ({value:instance.id, label:instance.ready ? instance.title : `${instance.title} (${t('term.notReady')})`, choose:()=>open(instance)})),
        direct: instances.length === 1 ? () => open(instances[0]) : undefined,
      }
    })
  }, [client, pick])
  const pickLogs = useCallback((ref: Ref, open: (ref: Ref, channel?: string) => void) => {
    void pick(async () => {
      const info = await getLogInfo(client, ref)
      const instances = info.instances ?? []
      if (!info.selectChannels) return {label:info.instanceLabel ? messageText(info.instanceLabel) : t('term.instance'),choices:[],direct:()=>open(ref,info.defaultChannel)}
      if (!info.aggregate) {
        const channels = info.channels ?? []
        const choose = (id: string) => open(ref, id)
        return {
          label: info.channelLabel ? messageText(info.channelLabel) : t('term.channel'),
          choices: [{value:'*',label:info.allChannelsLabel ? messageText(info.allChannelsLabel) : t('logs.allChannels'),choose:()=>choose('*')}, ...channels.map(channel=>({value:channel.id,label:channel.title,choose:()=>choose(channel.id)}))],
          direct: channels.length <= 1 ? ()=>open(ref, info.defaultChannel) : undefined,
        }
      }
      return {
        label: info.instanceLabel ? messageText(info.instanceLabel) : t('logs.pod'),
        choices: [{value:'*', label:info.allInstancesLabel ? messageText(info.allInstancesLabel) : t('logs.allPods'), choose:()=>open(ref)}, ...instances.map(instance => ({value:instance.ref.uid || instance.ref.name, label:instance.title, choose:()=>open(instance.ref)}))],
        direct: instances.length <= 1 ? () => open(ref) : undefined,
      }
    })
  }, [client, pick])
  const popup = picker?.context === context && !picker.loading && createPortal(<SelectList key={`${context}:${picker.loading}`} anchor={picker.anchor} label={picker.label} value=""
    options={picker.choices}
    search={!picker.loading && picker.choices.length > 12 ? t('select.search') : null} onClose={close}
    onChange={value => picker.choices.find(choice=>choice.value === value)?.choose()} />, document.body)
  return { pickExec, pickLogs, popup, cancel:close }
}

export function runningChannel(instance: ExecInstance) {
  return instance.channels?.find(channel => channel.id === instance.defaultChannel && channel.running)
    ?? instance.channels?.find(channel => channel.running)
}
