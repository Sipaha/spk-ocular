import {composedLogClient,compositionKey} from './composition'
import type {LogComposition} from '../api/types'
import { useStore } from '../store'
import { LogContext } from './LogContext'
import { LocationStatus } from '../components/LocationStatus'
import { ActionIconButton } from '../components/ActionIconButton'
import { ActionIcon, WindowActionIcon } from '../components/icons'
import type { LogViewState, LogWindowBridge } from './windowBridge'
import { useLayoutResizing } from '../components/layoutResize'
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import type { Client } from '../api/client'
import type { LogInfo, LogQuery, Ref } from '../api/types'
import { Select as AppSelect, type SelectOption } from '../components/Select'
import { classLabel, messageText, t, type MessageKey } from '../i18n'
import { isShortcut } from '../keyboard'
import { overlayOpen } from '../shortcuts'
import { LogViewport } from './LogViewport'
import { rowText, shortLabels, sourceColor, type RowOpts } from './row'
import { downloadLogHistory } from './downloadHistory'
import { serverLogFilter } from './match'
import { useLogFilter } from './useLogFilter'
import { useLogStream, type LogSource } from './useLogStream'

const TAILS = [100, 500, 1000, 5000, -1]
const SINCE: [string, number][] = [
  ['5m', 5 * 60],
  ['15m', 15 * 60],
  ['1h', 3600],
  ['6h', 6 * 3600],
  ['24h', 24 * 3600],
]

interface Props {
  client: Client
  composition?: LogComposition
  connectionName?: string
  subject: Ref
  /** The visible tab: keyboard shortcuts are live only there. */
  active: boolean
  bridge?: LogWindowBridge
  initial?: LogViewState
  initialChannel?: string
  initialPrevious?:boolean
  focusEpoch?: number
  onWindowAction?: () => void
}

/**
 * LogViewer: one object's logs. Composes useLogStream (the stream and its
 * buffer), useLogFilter (filter, search) and LogViewport (the
 * virtual list).
 */
export default function LogViewer({ client: baseClient, composition, connectionName: initialConnectionName, subject, active, bridge, initial, initialChannel, initialPrevious, focusEpoch, onWindowAction }: Props) {
  const client=useMemo(()=>composition?composedLogClient(baseClient,composition):baseClient,[baseClient,composition])
  const identity=composition?compositionKey(composition):undefined
  const connectionName = useStore(state => state.view?.groups.flatMap(group => group.targets).find(target => target.provider === subject.provider && target.id === subject.target)?.title) || initialConnectionName
  const [info, setInfo] = useState<LogInfo | null>(null)
  const [infoError, setInfoError] = useState<string | null>(null)
  const [channel, setChannel] = useState<string | null>(initial?.channel ?? initialChannel ?? null)
  const [tail, setTail] = useState(initial?.tail ?? 500)
  const [since, setSince] = useState<{ key: string; at: string } | null>(initial?.since ?? null)
  const [serverFilter, setServerFilter] = useState(initial?.filterText ?? '')
  const [previous, setPrevious] = useState(initial?.previous ?? initialPrevious ?? false)
  const [selectionRequest,setSelectionRequest]=useState({focusEpoch,initialChannel,initialPrevious})
  if(selectionRequest.focusEpoch!==focusEpoch||selectionRequest.initialChannel!==initialChannel||selectionRequest.initialPrevious!==initialPrevious){
    setSelectionRequest({focusEpoch,initialChannel,initialPrevious})
    if(initialPrevious!==undefined){setPrevious(initialPrevious);if(initialChannel!==undefined)setChannel(initialChannel)}
  }
  const [showTime, setShowTime] = useState(initial?.showTime ?? false)
  const [showSource, setShowSource] = useState<boolean | null>(initial?.showSource ?? null) // null: automatic
  const [wrap, setWrap] = useState(initial?.wrap ?? false)
  const [follow, setFollow] = useState(initial?.follow ?? true)
  const [selecting, setSelecting] = useState(false)
  const resizing = useLayoutResizing()
  const [note, setNote] = useState<string | null>(null)
  // Track intent before LogInfo resolves and the Stream mounts. A later user
  // action owns focus even if this viewer's loading or activation frame is late.
  const focusCancelled = useRef(false)
  useEffect(() => {
    focusCancelled.current = false
    if (!active) return
    const cancel = () => { focusCancelled.current = true }
    document.addEventListener('pointerdown', cancel, true)
    document.addEventListener('keydown', cancel, true)
    return () => {
      document.removeEventListener('pointerdown', cancel, true)
      document.removeEventListener('keydown', cancel, true)
    }
  }, [active, focusEpoch])
  const mayFocus = useCallback(() => !focusCancelled.current, [])

  useEffect(() => {
    let live = true
    client.logInfo(subject).then(
      (i) => {
        if (!live) return
        setInfo(i)
        setChannel(old => old ?? i.defaultChannel)
      },
      (e) => live && setInfoError(e instanceof Error ? e.message : String(e)),
    )
    return () => {
      live = false
    }
  }, [client, subject])

  useEffect(() => {
    if (!bridge) return
    bridge.configureView(subject, () => ({ channel, tail, since, previous, showTime, showSource, wrap, follow, ...bridge.captureFilter?.() }), state => {
      setChannel(state.channel); setTail(state.tail); setSince(state.since); setPrevious(state.previous)
      setShowTime(state.showTime); setShowSource(state.showSource); setWrap(state.wrap); setFollow(state.follow)
      bridge.applyFilter?.(state)
    }, q => {
      setChannel(q.channel ?? '');setTail(q.tailLines ?? 500);setPrevious(!!q.previous)
      setSince(q.sinceTime ? { key: since?.key ?? '', at: q.sinceTime } : null)
      setServerFilter(q.filter ?? '')
      const current = bridge.captureView?.()
      if (current) bridge.applyFilter?.({ ...current, filterText: q.filter ?? '' })
    }, connectionName, composition)
    if (bridge.role === 'guest') void bridge.post('view', bridge.captureView?.())
  }, [bridge, subject, channel, tail, since, previous, showTime, showSource, wrap, follow, connectionName, composition])

  const query = useMemo<LogQuery | null>(
    () => (channel === null ? null : { channel, previous, compositionKey:identity, follow: !previous, tailLines: tail, sinceTime: since?.at, filter: serverLogFilter(serverFilter) }),
    [channel, previous, tail, since, serverFilter, identity],
  )

  return infoError ? (
    <p role="alert" className="m-3 rounded-md bg-danger/10 px-3 py-2 text-xs text-danger">
      {infoError}
    </p>
  ) : !info || !query ? (
    <p className="p-3 text-fg-subtle">{t('app.loading')}</p>
  ) : (
    <Stream
      client={client}
      composition={composition}
      subject={subject}
      connectionName={connectionName}
      active={active}
      focusEpoch={focusEpoch}
      mayFocus={mayFocus}
      query={query}
      onServerFilter={setServerFilter}
      bridge={bridge}
      initial={initial}
      onWindowAction={onWindowAction}
      windowButton={onWindowAction && <ActionIconButton onClick={onWindowAction} label={t(bridge?.role === 'guest' ? 'logs.returnWindow' : 'logs.detachWindow')} description={`${t(bridge?.role === 'guest' ? 'logs.returnWindow' : 'logs.detachWindow')} (Alt+Shift+Enter)`} ><WindowActionIcon returning={bridge?.role === 'guest'} className="h-4 w-4"/></ActionIconButton>}
      toolbar={
        <>
          <Select
            label={info.channelLabel ? messageText(info.channelLabel) : t('logs.channel')}
            value={channel ?? ''}
            onChange={(v) => {
              setChannel(v)
              if (v === '*') setPrevious(false)
            }}
            options={[
              ...info.channels.map((c) => ({ value: c.id, label: c.note ? `${c.title} (${c.note})` : c.title })),
              ...(info.channels.length > 1 || info.aggregate ? [{ value: '*', label: info.allChannelsLabel ? messageText(info.allChannelsLabel) : t('logs.allChannels') }] : []),
            ]}
          />
          <Select label={t('logs.tail')} value={String(tail)} onChange={(v) => setTail(Number(v))} options={TAILS.map((n) => ({ value: String(n), label: n < 0 ? t('logs.tailAll') : String(n) }))} />
          <Select
            label={t('logs.since')}
            value={since?.key ?? ''}
            onChange={(v) => {
              const s = SINCE.find(([k]) => k === v)
              setSince(s ? { key: v, at: new Date(Date.now() - s[1] * 1000).toISOString() } : null)
            }}
            options={[{ value: '', label: t('logs.sinceAll') }, ...SINCE.map(([k]) => ({ value: k, label: k }))]}
          />
          {info.previous && channel !== '*' && (
            <Toggle on={previous} onClick={() => setPrevious((p) => !p)} label={t('logs.previous')} description={t('logs.previous.tooltip')}>
              <ActionIcon name="previous"/>
            </Toggle>
          )}
        </>
      }
      view={{ showTime, setShowTime, showSource, setShowSource, wrap, setWrap, follow, setFollow, resizing, selecting, setSelecting, note, setNote }}
    />
  )
}

interface ViewState {
  showTime: boolean
  setShowTime: (v: boolean) => void
  showSource: boolean | null
  setShowSource: (v: boolean) => void
  wrap: boolean
  setWrap: (v: boolean) => void
  follow: boolean
  setFollow: (v: boolean) => void
  resizing: boolean
  selecting: boolean
  setSelecting: (v: boolean) => void
  note: string | null
  setNote: (v: string | null) => void
}

function Stream({ client, composition, connectionName, subject, active, focusEpoch, mayFocus, query, onServerFilter, toolbar, view, bridge, initial, windowButton, onWindowAction }: { client: Client; composition?:LogComposition; connectionName?: string; subject: Ref; active: boolean; focusEpoch?: number; mayFocus: () => boolean; query: LogQuery; onServerFilter: (filter: string) => void; toolbar: ReactNode; view: ViewState; bridge?: LogWindowBridge; initial?: LogViewState
  initialChannel?: string; windowButton?: ReactNode; onWindowAction?: () => void }) {
  const { showTime, setShowTime, wrap, setWrap, follow, setFollow, resizing, selecting, setSelecting, note, setNote } = view
  const { win, sources, status, reopen, clear } = useLogStream({ client, ref: subject, query, paused: selecting || resizing, frozen: !follow, bridge })
  const f = useLogFilter(win.entries, { initial })
  const showSource = !!composition || (view.showSource ?? sources.size > 1)

  const liveLabels = useMemo(() => {
    const list = [...sources.values()]
    const short = composition?list.map(s=>s.label):shortLabels(list.map((s) => s.label))
    const m = new Map<number, { label: string; color: string }>()
    list.forEach((s, i) => m.set(s.id, { label: short[i], color: sourceColor(s.key) }))
    return m
  }, [sources,composition])
  // While the user drag-selects, the prefix layout must not change under
  // the selection (a new source can change the short labels and widths).
  const [heldLabels, setHeldLabels] = useState(liveLabels)
  if (!selecting && !resizing && heldLabels !== liveLabels) setHeldLabels(liveLabels)
  const labels = selecting || resizing ? heldLabels : liveLabels
  const width = Math.max(0, ...[...sources.values()].filter(s=>!composition||!s.key.startsWith('root/')).map(s=>labels.get(s.id)?.label.length??0))
  const rowOpts = useMemo<RowOpts>(
    () => ({ showTime, showSource, labelOf: (src) => (labels.get(src)?.label ?? '?').padEnd(width) }),
    [showTime, showSource, labels, width],
  )

  const { search, filterText, useRegex, setSearch, setFilterText, setUseRegex } = f
  const historyLoading = status.phase === 'opening' || status.phase === 'backlog' || (serverLogFilter(filterText) ?? '') !== (query.filter ?? '')
  useEffect(() => {
    const timer = setTimeout(() => onServerFilter(filterText.slice(0, 500)), 250)
    return () => clearTimeout(timer)
  }, [filterText, onServerFilter])
  useEffect(() => {
    if (!bridge) return
    bridge.configureFilter(() => ({search,filterText,useRegex}), state => { setSearch(state.search ?? ''); setFilterText(state.filterText ?? ''); setUseRegex(!!state.useRegex) })
    if(bridge.role === 'guest' && bridge.captureView) void bridge.post('view',bridge.captureView())
  },[bridge,search,filterText,useRegex,setSearch,setFilterText,setUseRegex])

  const rootRef = useRef<HTMLDivElement>(null)
  const parentRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!active) return
    // Run after a picker/dialog restores its opener and a lazy viewer mounts.
    const frame = requestAnimationFrame(() => {
      if (mayFocus() && !document.querySelector('[aria-modal="true"], [role="menu"]')) parentRef.current?.focus({ preventScroll: true })
    })
    return () => cancelAnimationFrame(frame)
  }, [active, focusEpoch, mayFocus])
  const searchRef = useRef<HTMLInputElement>(null)
  const selectAllRef = useRef(false)
  const shownText = () => f.filtered.map((e) => rowText(e, rowOpts)).join('\n')

  const copyShown = () => void navigator.clipboard?.writeText(shownText()).catch(() => {})
  const downloadPending = useRef(false)
  const downloadController=useRef<AbortController|null>(null)
  const mounted=useRef(true)
  const [downloading, setDownloading] = useState(false)
  const [downloadProgress,setDownloadProgress]=useState({lines:0,bytes:0})
  useEffect(()=>{mounted.current=true;return()=>{mounted.current=false;downloadController.current?.abort()}},[])
  useEffect(()=>()=>downloadController.current?.abort(),[client,subject,query.channel,query.previous])
  const save = async (chooseLocation=false) => {
    if(downloadPending.current) return
    const sourceLabels=new Map([...sources.values()].map(source=>[source.key,rowOpts.labelOf(source.id)]))
    const format={showTime:rowOpts.showTime,showSource:rowOpts.showSource,labelOf:(key:string,fallback:string)=>sourceLabels.get(key)??fallback}
    const controller=new AbortController()
    downloadController.current=controller
    downloadPending.current=true;setDownloading(true);setNote(null);setDownloadProgress({lines:0,bytes:0})
    try {
      const result=await downloadLogHistory(client,composition?{...subject,title:t('logs.composition.title')}:subject,query.channel??'',!!query.previous,format,controller.signal,bridge?.id,value=>{if(mounted.current&&!controller.signal.aborted)setDownloadProgress(value)},()=>controller.abort(),chooseLocation)
      if(mounted.current&&!controller.signal.aborted&&result)setNote(result.path?t('logs.saved',{path:result.path}):t('logs.historySaved',{lines:result.lines}))
    } catch (e) {
      if(mounted.current)setNote(controller.signal.aborted?t('logs.historyCancelled'):t('logs.saveFailed',{error:e instanceof Error?e.message:String(e)}))
    } finally {downloadPending.current=false;if(downloadController.current===controller)downloadController.current=null;if(mounted.current)setDownloading(false)}
  }

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (!active || e.defaultPrevented || e.nativeEvent.isComposing) return
    if (e.key === 'Enter' && e.altKey && e.shiftKey && !e.ctrlKey && !e.metaKey && onWindowAction && !overlayOpen()) {
      e.preventDefault(); e.stopPropagation()
      if (!e.repeat) onWindowAction()
      return
    }
    if (e.altKey) return
    const target = e.target as HTMLElement
    const inInput = target.tagName === 'INPUT' || target.tagName === 'SELECT' || target.tagName === 'TEXTAREA'
    const inSearch = target === searchRef.current
    if (isShortcut(e, 'KeyF', { ctrl: true, shift: false })) {
      e.preventDefault()
      searchRef.current?.focus()
      searchRef.current?.select()
    } else if (e.key === 'F3' || isShortcut(e, 'KeyG', { ctrl: true })) {
      e.preventDefault()
      f.setMatchIndex(f.current + (e.shiftKey ? -1 : 1))
      f.bumpNav()
    } else if (e.key === 'Escape' && inSearch) {
      e.preventDefault()
      f.setSearch('')
      parentRef.current?.focus()
    } else if (inInput) {
      return
    } else if (isShortcut(e, 'KeyA', { ctrl: true, shift: false }) && parentRef.current) {
      e.preventDefault()
      const sel = window.getSelection()
      sel?.removeAllRanges()
      const range = document.createRange()
      range.selectNodeContents(parentRef.current)
      sel?.addRange(range)
      selectAllRef.current = true
    } else if (isShortcut(e, 'KeyC', { ctrl: true, shift: true })) {
      e.preventDefault()
      copyShown()
    } else if (isShortcut(e, 'KeyS', { ctrl: true, shift: false })) {
      e.preventDefault()
      void save()
    } else if (isShortcut(e, 'KeyL', { ctrl: true, shift: false })) {
      e.preventDefault()
      clear()
    }
  }

  // a source that simply ended (a complete answer) is not a problem;
  // gap/truncated warnings stay (history may be incomplete) after the
  // source is streaming again
  const problems = [...sources.values()].filter(
    (s) => s.warn || (s.state && s.state.state !== 'streaming' && !(s.state.state === 'ended' && !s.state.msg && !s.state.class)),
  )
  const terminal = status.phase === 'gone' || status.phase === 'disconnected' || status.phase === 'error'

  return (
    <div ref={rootRef} className="flex h-full min-h-0 flex-col" onKeyDown={onKeyDown}>
      {composition&&<details className="max-h-36 shrink-0 overflow-auto border-b border-line px-3 py-1 text-xs"><summary className="cursor-pointer">{t('logs.composition.count',{count:composition.refs.length})} · {t('logs.composition.memory')}</summary><ul>{composition.refs.map(r=>{const source=[...sources.values()].find(s=>s.key===`root/${r.scope}/${r.uid}`),state=source?.state;return <li key={`${r.scope}/${r.uid}`} className="break-words">{r.scope} · {r.kind}/{r.name} · UID: {r.uid} · <span className={state?.state==='error'?'text-danger':'text-fg-muted'}>{state?.state==='streaming'?t('logs.composition.observed'):t(`logs.state.${state?.state??'waiting'}` as MessageKey)}{state?.class&&` · ${classLabel(state.class)}`}{state?.msg&&` · ${state.msg}`}</span></li>})}</ul></details>}
      <div className="log-toolbar">
        <div role="group" aria-label={t('shell.logSource')} className="log-control-group">{toolbar}</div>
        <div role="group" aria-label={t('shell.logFilter')} className="log-control-group">
        <input
          value={f.filterText}
          onChange={(e) => f.setFilterText(e.target.value)}
          placeholder={t('logs.filter')}
          title={t('logs.filter.tooltip')}
          aria-label={t('logs.filter')}
          spellCheck={false}
          className="w-32 rounded border border-line bg-app px-2 py-0.5 outline-none focus:border-accent"
        />
        </div>
        <div role="group" aria-label={t('shell.logSearch')} className="log-control-group">
        <input
          ref={searchRef}
          value={f.search}
          onChange={(e) => f.setSearch(e.target.value)}
          onKeyDown={(e) => {
            if (e.key !== 'Enter') return
            e.preventDefault()
            f.setMatchIndex(f.current + (e.shiftKey ? -1 : 1))
            f.bumpNav()
          }}
          placeholder={t('logs.search')}
          aria-label={t('logs.search')}
          spellCheck={false}
          className="w-44 rounded border border-line bg-app px-2 py-0.5 outline-none focus:border-accent"
        />
        <Toggle on={f.useRegex} onClick={() => f.setUseRegex(!f.useRegex)} label={t('logs.regex')} description={t('logs.regex')}>
          <ActionIcon name="regex"/>
        </Toggle>
        <Btn disabled={!f.matches.length} onClick={() => { f.setMatchIndex(f.current - 1); f.bumpNav() }} label={t('logs.prev')} description={t('logs.prev')}>
          <ActionIcon name="up"/>
        </Btn>
        <Btn disabled={!f.matches.length} onClick={() => { f.setMatchIndex(f.current + 1); f.bumpNav() }} label={t('logs.next')} description={t('logs.next')}>
          <ActionIcon name="down"/>
        </Btn>
        <span className="tabular-nums text-fg-subtle" aria-label="matches">
          {f.matches.length ? f.current + 1 : 0}/{f.matches.length}
        </span>
        {f.regexState === 'slow' && <span className="text-warning">{t('logs.regexSlow')}</span>}
        {f.regexState === 'invalid' && <span className="text-warning">{t('logs.regexInvalid', { error: f.regexError ?? '' })}</span>}
        </div>
        <div role="group" aria-label={t('shell.logDisplay')} className="log-control-group">
        <Toggle on={showTime} onClick={() => setShowTime(!showTime)} label={t('logs.time')} description={t('logs.time.tooltip')}>
          <ActionIcon name="time"/>
        </Toggle>
        <Toggle on={showSource} disabled={!!composition} onClick={() => view.setShowSource(!showSource)} label={t('logs.source')} description={t('logs.source.tooltip')}>
          <ActionIcon name="source"/>
        </Toggle>
        <Toggle on={wrap} onClick={() => setWrap(!wrap)} label={t('logs.wrap')} description={t('logs.wrap.tooltip')}>
          <ActionIcon name="wrap"/>
        </Toggle>
        </div>
        <div role="group" aria-label={t('shell.logActions')} className="log-control-group log-actions">
        <Btn onClick={copyShown} label={t('logs.copy')} description={t('logs.copy.tooltip')}>
          <ActionIcon name="copy"/>
        </Btn>
        <Btn disabled={downloading} onContextMenu={event=>{event.preventDefault();void save(true)}} onClick={() => void save()} label={t('logs.save')} description={t('logs.save.tooltip')}>
          <ActionIcon name="download"/>
        </Btn>
        <Btn disabled={!!query.filter && historyLoading} onClick={clear} label={t('logs.clear')} description={t('logs.clear.tooltip')}>
          <ActionIcon name="clear"/>
        </Btn>
        {windowButton}
        </div>
      </div>
      <LogContext subject={subject} composition={composition} connectionName={connectionName} loading={historyLoading} />
      {(problems.length > 0 || status.notice) && <Problems problems={problems} notice={status.notice} labels={labels} />}
      <LogViewport
        loading={historyLoading}
        entries={f.filtered}
        rangesFor={f.rangesFor}
        matchIndices={f.matches}
        safeMatchIndex={f.current}
        rowOpts={rowOpts}
        sourceColor={(src) => labels.get(src)?.color ?? 'inherit'}
        searchNavTick={f.navTick}
        wordWrap={wrap}
        follow={follow}
        setFollow={setFollow}
        parentRef={parentRef}
        selectAllRef={selectAllRef}
        onSelectingChange={setSelecting}
      />
      <LocationStatus active={active}>
        <span aria-label="line count">
          {f.filtered.length !== win.entries.length
            ? t('logs.linesOf', { count: f.filtered.length, total: win.entries.length })
            : t('logs.lines', { count: win.entries.length })}
        </span>
        {win.evicted > 0 && <span className="text-warning">{t('logs.evicted', { count: win.evicted })}</span>}
        <span aria-label="stream state" className={terminal ? 'text-danger' : status.phase === 'live' ? 'text-success' : ''}>
          {t(`logs.phase.${status.phase}` as MessageKey)}
          {status.cls && ` · ${classLabel(status.cls)}`}
          {status.message && ` · ${status.message}`}
        </span>
        {terminal && (
          <ActionIconButton label={t('logs.reopen')} description={t('logs.reopenHint')} onClick={reopen}><ActionIcon name="reconnect"/></ActionIconButton>
        )}
        {downloading&&<span role="status" className="min-w-0 truncate" title={t('logs.historyReading',{lines:downloadProgress.lines,bytes:downloadProgress.bytes})}>{t('logs.historyReading',{lines:downloadProgress.lines,bytes:downloadProgress.bytes})}</span>}
        {note&&!downloading&&<span role="status" className="min-w-0 truncate" title={note}>{note}</span>}
      </LocationStatus>
    </div>
  )
}

function Problems({ problems, notice, labels }: { problems: LogSource[]; notice?: { state: string; msg?: string }; labels: Map<number, { label: string; color: string }> }) {
  const shown = problems.slice(0, 3)
  const tone = (s: string) => (s === 'error' ? 'text-danger' : s === 'ended' ? 'text-fg-muted' : 'text-warning')
  return (
    <div role="status" className="shrink-0 space-y-0.5 border-b border-line bg-panel/60 px-3 py-1 text-[12px]">
      {notice && <p className={tone(notice.state)}>{notice.msg}</p>}
      {shown.map((s) => {
        const st = s.state && s.state.state !== 'streaming' ? s.state : s.warn!
        return (
          <p key={s.id} className={tone(st.state)}>
            {labels.size > 1 && <b style={{ color: labels.get(s.id)?.color }}>{labels.get(s.id)?.label}: </b>}
            {t(`logs.state.${st.state}` as MessageKey)}
            {st.class && ` · ${classLabel(st.class)}`}
            {st.msg && ` — ${st.msg}`}
            {st !== s.warn && s.warn && <span className="text-warning"> · {t(`logs.state.${s.warn.state}` as MessageKey)}</span>}
          </p>
        )
      })}
      {problems.length > shown.length && <p className="text-fg-subtle">{t('logs.moreProblems', { count: problems.length - shown.length })}</p>}
    </div>
  )
}

function Btn(props: React.ComponentProps<typeof ActionIconButton>) {
  return <ActionIconButton {...props}/>
}

function Toggle({on,...props}: React.ComponentProps<typeof ActionIconButton> & {on:boolean}) {
  return <ActionIconButton {...props} aria-pressed={on}/>
}

function Select(props: { label: string; value: string; onChange: (v: string) => void; options: SelectOption[] }) {
  return <AppSelect {...props} className="rounded py-0.5 pl-1.5" />
}
