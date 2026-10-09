import { WindowActionIcon } from '../components/icons'
import type { LogViewState, LogWindowBridge } from './windowBridge'
import { useLayoutResizing } from '../components/layoutResize'
import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import type { Client } from '../api/client'
import type { LogInfo, LogQuery, Ref } from '../api/types'
import { Select as AppSelect, type SelectOption } from '../components/Select'
import { classLabel, messageText, t, type MessageKey } from '../i18n'
import { isShortcut } from '../keyboard'
import { LogViewport } from './LogViewport'
import { rowText, shortLabels, sourceColor, type RowOpts } from './row'
import { logFileName, downloadLogs } from './save'
import { useLogFilter } from './useLogFilter'
import { useLogStream, type LogSource } from './useLogStream'
import { refTitle } from '../refs'

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
  subject: Ref
  /** The visible tab: keyboard shortcuts are live only there. */
  active: boolean
  bridge?: LogWindowBridge
  initial?: LogViewState
  initialChannel?: string
  onWindowAction?: () => void
}

/**
 * LogViewer: one object's logs. Composes useLogStream (the stream and its
 * buffer), useLogFilter (filter, search) and LogViewport (the
 * virtual list).
 */
export default function LogViewer({ client, subject, active, bridge, initial, initialChannel, onWindowAction }: Props) {
  const [info, setInfo] = useState<LogInfo | null>(null)
  const [infoError, setInfoError] = useState<string | null>(null)
  const [channel, setChannel] = useState<string | null>(initial?.channel ?? initialChannel ?? null)
  const [tail, setTail] = useState(initial?.tail ?? 500)
  const [since, setSince] = useState<{ key: string; at: string } | null>(initial?.since ?? null)
  const [previous, setPrevious] = useState(initial?.previous ?? false)
  const [showTime, setShowTime] = useState(initial?.showTime ?? false)
  const [showSource, setShowSource] = useState<boolean | null>(initial?.showSource ?? null) // null: automatic
  const [wrap, setWrap] = useState(initial?.wrap ?? false)
  const [follow, setFollow] = useState(initial?.follow ?? true)
  const [selecting, setSelecting] = useState(false)
  const resizing = useLayoutResizing()
  const [note, setNote] = useState<string | null>(null)

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
    })
    if (bridge.role === 'guest') void bridge.post('view', bridge.captureView?.())
  }, [bridge, subject, channel, tail, since, previous, showTime, showSource, wrap, follow])

  const query = useMemo<LogQuery | null>(
    () => (channel === null ? null : { channel, previous, follow: !previous, tailLines: tail, sinceTime: since?.at }),
    [channel, previous, tail, since],
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
      subject={subject}
      active={active}
      query={query}
      bridge={bridge}
      initial={initial}
      windowButton={onWindowAction && <button type="button" onClick={onWindowAction} aria-label={t(bridge?.role === 'guest' ? 'logs.returnWindow' : 'logs.detachWindow')} title={t(bridge?.role === 'guest' ? 'logs.returnWindow' : 'logs.detachWindow')} className="ml-1 flex h-[26px] w-[26px] shrink-0 items-center justify-center !p-0"><WindowActionIcon returning={bridge?.role === 'guest'} className="h-4 w-4"/></button>}
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
            <Toggle on={previous} onClick={() => setPrevious((p) => !p)} title={t('logs.previous.tooltip')}>
              {t('logs.previous')}
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

function Stream({ client, subject, active, query, toolbar, view, bridge, initial, windowButton }: { client: Client; subject: Ref; active: boolean; query: LogQuery; toolbar: ReactNode; view: ViewState; bridge?: LogWindowBridge; initial?: LogViewState
  initialChannel?: string; windowButton?: ReactNode }) {
  const { showTime, setShowTime, wrap, setWrap, follow, setFollow, resizing, selecting, setSelecting, note, setNote } = view
  const { win, sources, status, reopen, clear } = useLogStream({ client, ref: subject, query, paused: selecting || resizing, frozen: !follow, bridge })
  const f = useLogFilter(win.entries, { initial })
  const showSource = view.showSource ?? sources.size > 1

  const liveLabels = useMemo(() => {
    const list = [...sources.values()]
    const short = shortLabels(list.map((s) => s.label))
    const m = new Map<number, { label: string; color: string }>()
    list.forEach((s, i) => m.set(s.id, { label: short[i], color: sourceColor(s.key) }))
    return m
  }, [sources])
  // While the user drag-selects, the prefix layout must not change under
  // the selection (a new source can change the short labels and widths).
  const [heldLabels, setHeldLabels] = useState(liveLabels)
  if (!selecting && !resizing && heldLabels !== liveLabels) setHeldLabels(liveLabels)
  const labels = selecting || resizing ? heldLabels : liveLabels
  const width = Math.max(0, ...[...labels.values()].map((l) => l.label.length))
  const rowOpts = useMemo<RowOpts>(
    () => ({ showTime, showSource, labelOf: (src) => (labels.get(src)?.label ?? '?').padEnd(width) }),
    [showTime, showSource, labels, width],
  )

  const { search, filterText, useRegex, setSearch, setFilterText, setUseRegex } = f
  useEffect(() => {
    if (!bridge) return
    bridge.configureFilter(() => ({search,filterText,useRegex}), state => { setSearch(state.search ?? ''); setFilterText(state.filterText ?? ''); setUseRegex(!!state.useRegex) })
    if(bridge.role === 'guest' && bridge.captureView) void bridge.post('view',bridge.captureView())
  },[bridge,search,filterText,useRegex,setSearch,setFilterText,setUseRegex])

  const rootRef = useRef<HTMLDivElement>(null)
  const parentRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)
  const selectAllRef = useRef(false)
  const shownText = () => f.filtered.map((e) => rowText(e, rowOpts)).join('\n')

  const copyShown = () => void navigator.clipboard?.writeText(shownText()).catch(() => {})
  const downloadPending = useRef(false)
  const [downloading, setDownloading] = useState(false)
  const save = async () => {
    if(downloadPending.current) return
    downloadPending.current=true;setDownloading(true)
    try {
      const path = await downloadLogs(logFileName(refTitle(subject)), shownText(), bridge?.id)
      setNote(path ? t('logs.saved', { path }) : null)
    } catch (e) {
      setNote(t('logs.saveFailed', { error: e instanceof Error ? e.message : String(e) }))
    } finally { downloadPending.current=false;setDownloading(false) }
  }

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (!active || e.altKey) return
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
      <div className="log-toolbar">
        <div role="group" aria-label={t('shell.logSource')} className="log-control-group">{toolbar}</div>
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
        <Toggle on={f.useRegex} onClick={() => f.setUseRegex(!f.useRegex)} title={t('logs.regex')}>
          .*
        </Toggle>
        <Btn disabled={!f.matches.length} onClick={() => { f.setMatchIndex(f.current - 1); f.bumpNav() }} title={t('logs.prev')}>
          ↑
        </Btn>
        <Btn disabled={!f.matches.length} onClick={() => { f.setMatchIndex(f.current + 1); f.bumpNav() }} title={t('logs.next')}>
          ↓
        </Btn>
        <span className="tabular-nums text-fg-subtle" aria-label="matches">
          {f.matches.length ? f.current + 1 : 0}/{f.matches.length}
        </span>
        {f.regexState === 'slow' && <span className="text-warning">{t('logs.regexSlow')}</span>}
        {f.regexState === 'invalid' && <span className="text-warning">{t('logs.regexInvalid', { error: f.regexError ?? '' })}</span>}
        </div>
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
        <div role="group" aria-label={t('shell.logDisplay')} className="log-control-group">
        <Toggle on={showTime} onClick={() => setShowTime(!showTime)} title={t('logs.time.tooltip')}>
          {t('logs.time')}
        </Toggle>
        <Toggle on={showSource} onClick={() => view.setShowSource(!showSource)} title={t('logs.source.tooltip')}>
          {t('logs.source')}
        </Toggle>
        <Toggle on={wrap} onClick={() => setWrap(!wrap)} title={t('logs.wrap.tooltip')}>
          {t('logs.wrap')}
        </Toggle>
        </div>
        <div role="group" aria-label={t('shell.logActions')} className="log-control-group log-actions">
        <Btn onClick={copyShown} title={t('logs.copy.tooltip')}>
          {t('logs.copy')}
        </Btn>
        <Btn disabled={downloading} onClick={() => void save()} title={t('logs.save.tooltip')}>
          {t('logs.save')}
        </Btn>
        <Btn onClick={clear} title={t('logs.clear.tooltip')}>
          {t('logs.clear')}
        </Btn>
        {windowButton}
        </div>
      </div>
      {(problems.length > 0 || status.notice) && <Problems problems={problems} notice={status.notice} labels={labels} />}
      <LogViewport
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
      <div className="log-status flex shrink-0 items-center gap-3 border-t border-line px-3 py-0.5 text-[12px] text-fg-subtle">
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
          <button className="text-accent hover:underline" onClick={reopen}>
            {t('logs.reopen')}
          </button>
        )}
        {note && <span className="truncate">{note}</span>}
      </div>
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

function Btn({ children, ...p }: { children: ReactNode; onClick: () => void; title?: string; disabled?: boolean }) {
  return (
    <button {...p} className="rounded border border-line px-1.5 py-0.5 text-fg-muted enabled:hover:bg-hover enabled:hover:text-fg disabled:opacity-40">
      {children}
    </button>
  )
}

function Toggle({ on, children, ...p }: { on: boolean; children: ReactNode; onClick: () => void; title?: string }) {
  return (
    <button {...p} aria-pressed={on} className={['rounded border px-1.5 py-0.5', on ? 'border-accent bg-accent/15 text-accent' : 'border-line text-fg-muted hover:bg-hover'].join(' ')}>
      {children}
    </button>
  )
}

function Select(props: { label: string; value: string; onChange: (v: string) => void; options: SelectOption[] }) {
  return <AppSelect {...props} className="rounded py-0.5 pl-1.5" />
}
