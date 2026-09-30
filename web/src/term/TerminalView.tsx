import { useEffect, useRef, useState } from 'react'
import { Terminal, type ITheme } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import type { Client } from '../api/client'
import { ApiError } from '../api/client'
import type { TerminalInfo } from '../api/types'
import { classLabel, messageText, t } from '../i18n'
import { isShortcut } from '../keyboard'
import { dock, type TermTab } from '../dock/store'
import { formatArgv } from './argv'
import { copyText, readText } from './clipboard'
import { PasteTooLargeError, TermConnection, wsBase, type TermEnd, type TermPhase } from './protocol'
import { errorDetail } from '../errors'

// Darcula, as the log viewer's ANSI palette (src/index.css).
const theme: ITheme = {
  background: '#1e1f22',
  foreground: '#dfe1e5',
  cursor: '#dfe1e5',
  cursorAccent: '#1e1f22',
  selectionBackground: '#2e436e',
  black: '#4b4e55',
  red: '#f0524f',
  green: '#5c962c',
  yellow: '#c0a13a',
  blue: '#3993d4',
  magenta: '#a771bf',
  cyan: '#00a3a3',
  white: '#bcbec4',
  brightBlack: '#6f737a',
  brightRed: '#ff4050',
  brightGreen: '#4fc414',
  brightYellow: '#e5bf00',
  brightBlue: '#1fb0ff',
  brightMagenta: '#ed7eed',
  brightCyan: '#00e5e5',
  brightWhite: '#ffffff',
}

const FONT = '"JetBrains Mono", "SF Mono", "Cascadia Code", "Fira Code", "Roboto Mono", ui-monospace, Menlo, Consolas, monospace'
const DIM = (s: string) => `\x1b[2m${s}\x1b[22m`

export interface Props {
  client: Client
  tab: TermTab
  active: boolean
  mode: 'desktop' | 'browser'
}

const errText = (e: unknown) => (e instanceof ApiError ? `${classLabel(e.code)}: ${errorDetail(e)}` : errorDetail(e))

function describe(info: TerminalInfo): string {
  const tg = info.target
  const lines = [`${t('term.context')}: ${tg.targetTitle}`]
  if (tg.endpoint) lines.push(`${t('term.server')}: ${tg.endpoint}`)
  lines.push(tg.command?.length ? `${t('term.command')}: ${formatArgv(tg.command)}` : t('term.shell'))
  return lines.join('\n')
}

/**
 * One terminal tab. Output is untrusted: no clipboard writes from escape
 * sequences (xterm has no OSC 52 without its addon), no link opening, no
 * title changes. Copy is the selection + Ctrl+Shift+C; paste Ctrl+Shift+V
 * or the native paste (bracketed when the program asks for it).
 */
export default function TerminalView({ client, tab, active, mode }: Props) {
  const hostRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const connRef = useRef<TermConnection | null>(null)
  const idRef = useRef<string | null>(null)
  // Every (re)connect is a generation: late answers and callbacks of an
  // older one never touch the newer.
  const genRef = useRef(0)
  const [phase, setPhase] = useState<TermPhase>('opening')
  const [ended, setEnded] = useState<TermEnd | null>(null)
  const [openError, setOpenError] = useState<{ text: string; code?: string } | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [confirmAgain, setConfirmAgain] = useState(false)
  const custom = !!tab.open.command?.length

  const connectRef = useRef<(again: boolean) => Promise<void>>(async () => {})

  useEffect(() => {
    const host = hostRef.current!
    const gens = genRef
    // This mount's life (StrictMode mounts twice: a ref would be revived
    // by the second mount while the first one's open is still pending).
    let alive = true
    const connect = async (again: boolean) => {
      const term = termRef.current
      if (!term) return
      const gen = ++genRef.current
      connRef.current?.close()
      connRef.current = null
      setEnded(null)
      setOpenError(null)
      setConfirmAgain(false)
      setNotice(null) // a notice was about the previous run
      setPhase('opening')
      const { cols, rows } = term
      let info: TerminalInfo
      try {
        info = idRef.current
          ? await client.reopenTerminal(idRef.current, cols, rows)
          : await client.openTerminal({ ...tab.open, cols, rows })
      } catch (e) {
        if (gen === genRef.current && alive) {
          setOpenError({ text: errText(e), code: e instanceof ApiError ? e.code : undefined })
          setPhase('ended')
        }
        return
      }
      if (!alive || gen !== genRef.current) {
        // The tab closed or a newer attempt began: a terminal only this
        // attempt knows of is forgotten (a reopen's is the tab's own).
        if (!alive || info.terminalId !== idRef.current) void client.forgetTerminal(info.terminalId).catch(() => {})
        return
      }
      idRef.current = info.terminalId
      const tg = info.target
      dock.update(tab.id, { title: tg.channel && tg.instance ? `${tg.channel} · ${tg.instance}` : tg.instance || tab.title, hint: describe(info), rev: tg.configRev })
      let base: string
      try {
        base = wsBase(await client.streamBase())
      } catch (e) {
        if (gen === genRef.current) setOpenError({ text: errText(e) })
        return
      }
      if (gen !== genRef.current || !alive) return
      if (again) term.write(`\r\n${DIM(`──── ${t('term.reconnected')} ────`)}\r\n`)
      const conn = new TermConnection(`${base}/term/${encodeURIComponent(info.streamId)}`, {
        output: (data, done) => (gen === genRef.current ? term.write(data, done) : done()),
        phase: (p) => gen === genRef.current && setPhase(p),
        notice: (m) => gen === genRef.current && setNotice(messageText(m)),
        end: (e) => {
          if (gen !== genRef.current) return
          setEnded(e)
          term.write(`\r\n${DIM(`[${endText(e)}]`)}\r\n`)
        },
      })
      connRef.current = conn
      conn.resize(term.cols, term.rows)
    }
    connectRef.current = connect

    const term = new Terminal({
      scrollback: 5000,
      theme,
      fontFamily: FONT,
      fontSize: 14,
      cursorBlink: true,
      allowProposedApi: false,
      // Links in the output are never opened (untrusted data).
      linkHandler: { activate: () => {} },
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(host)
    termRef.current = term
    fitRef.current = fit
    if (host.clientWidth > 0 && host.clientHeight > 0) fit.fit()

    const send = (d: string) => {
      const c = connRef.current
      if (!c) return
      if (d === '\x03') {
        c.interrupt() // drops a paste still waiting, and gets through a full input window
        return
      }
      try {
        c.input(d)
      } catch (e) {
        if (e instanceof PasteTooLargeError) setNotice(t('term.pasteTooLarge'))
      }
    }
    term.onData(send)
    term.onBinary((d) => connRef.current?.input(Uint8Array.from(d, (ch) => ch.charCodeAt(0) & 0xff)))
    term.onResize(({ cols, rows }) => connRef.current?.resize(cols, rows))
    term.attachCustomKeyEventHandler((ev) => {
      if (ev.type !== 'keydown') return true
      if (isShortcut(ev, 'KeyC', { ctrl: true, shift: true })) {
        ev.preventDefault()
        const sel = term.getSelection()
        if (sel) void copyText(sel, mode)
        return false
      }
      if (isShortcut(ev, 'KeyV', { ctrl: true, shift: true })) {
        ev.preventDefault()
        // The clipboard answers later: the text goes to the connection the
        // gesture was made in, or nowhere (and says so).
        const conn = connRef.current
        const gen = genRef.current
        if (!conn?.live) {
          setNotice(t('term.pasteNoConnection'))
          return false
        }
        void readText(mode).then((text) => {
          if (!text || !alive) return
          if (genRef.current !== gen || connRef.current !== conn || !conn.live) {
            setNotice(t('term.pasteDropped'))
            return
          }
          term.paste(text)
        })
        return false
      }
      const ctl = layoutControl(ev)
      if (ctl) {
        ev.preventDefault()
        send(ctl)
        return false
      }
      return true
    })

    // Fit only to a real size: a hidden tab keeps its last valid geometry.
    let timer: ReturnType<typeof setTimeout> | null = null
    const ro = new ResizeObserver(() => {
      if (timer) clearTimeout(timer)
      timer = setTimeout(() => {
        timer = null
        if (host.clientWidth > 0 && host.clientHeight > 0) fit.fit()
      }, 50)
    })
    ro.observe(host)

    void connect(false)
    return () => {
      alive = false
      gens.current++
      ro.disconnect()
      if (timer) clearTimeout(timer)
      connRef.current?.close()
      connRef.current = null
      if (idRef.current) void client.forgetTerminal(idRef.current).catch(() => {})
      idRef.current = null
      term.dispose()
      termRef.current = null
    }
    // Mounted once per tab: the tab's open request never changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Activation: one fit (the size may have changed while hidden) and focus.
  useEffect(() => {
    if (!active) return
    const id = requestAnimationFrame(() => {
      const host = hostRef.current
      if (host && host.clientWidth > 0 && host.clientHeight > 0) fitRef.current?.fit()
      termRef.current?.focus()
    })
    return () => cancelAnimationFrame(id)
  }, [active])

  const again = () => {
    if (custom && !confirmAgain) {
      setConfirmAgain(true) // a custom command is never repeated without asking
      return
    }
    void connectRef.current(true)
    termRef.current?.focus()
  }
  const openNew = () => dock.openTerminal(tab.target, tab.targetTitle, { ...tab.open, instance: undefined })

  const gone = openError?.code === 'gone' || ended?.class === 'gone'
  return (
    <div className="flex h-full min-h-0 flex-col" data-terminal>
      {(phase === 'opening' || phase === 'connecting') && !openError && (
        <div className="shrink-0 border-b border-line px-3 py-0.5 text-[12px] text-fg-subtle" role="status">
          {t('term.connecting')}
        </div>
      )}
      {(ended || openError) && (
        <div role="alert" className="flex shrink-0 flex-wrap items-center gap-2 border-b border-line bg-panel px-3 py-1 text-xs">
          <span className={openError || ended?.reason === 'error' || ended?.reason === 'closed' ? 'text-danger' : 'text-fg-muted'}>
            {openError ? openError.text : endText(ended!)}
          </span>
          {confirmAgain ? (
            <>
              <span className="text-fg-muted">{t('term.runAgain')}</span>
              <code className="rounded bg-app px-1 font-mono">{formatArgv(tab.open.command ?? [])}</code>
              <button className="rounded-md bg-accent px-2 py-0.5 text-accent-fg" onClick={again}>
                {t('term.run')}
              </button>
              <button className="rounded-md px-2 py-0.5 text-fg-muted hover:bg-hover" onClick={() => setConfirmAgain(false)}>
                {t('term.cancel')}
              </button>
            </>
          ) : (
            <>
              {!gone && (
                <button className="rounded-md bg-accent px-2 py-0.5 text-accent-fg" onClick={again}>
                  {t('term.reconnect')}
                </button>
              )}
              {/* An ended debugger cannot restart: its text says to debug again. */}
              {gone && !tab.open.attach && (
                <button className="rounded-md bg-accent px-2 py-0.5 text-accent-fg" onClick={openNew}>
                  {t('term.openNew')}
                </button>
              )}
            </>
          )}
        </div>
      )}
      {notice && (
        <div role="alert" className="flex shrink-0 items-center gap-2 border-b border-line bg-warning/10 px-3 py-1 text-xs text-warning">
          {notice}
          <button className="ml-auto rounded px-1 hover:bg-hover" onClick={() => setNotice(null)} aria-label={t('drawer.close')}>
            ×
          </button>
        </div>
      )}
      <div ref={hostRef} className="min-h-0 flex-1 overflow-hidden bg-app py-1 pl-2" data-terminal-host />
    </div>
  )
}

function endText(e: TermEnd): string {
  if (e.code !== undefined) return t('term.exited', { code: String(e.code) })
  switch (e.reason) {
    case 'done':
      return t('term.ended')
    case 'gone':
      return t('term.gone')
    case 'closed':
      return t('term.lost', { detail: e.message ?? '' })
  }
  return `${e.class ? classLabel(e.class) + ': ' : ''}${e.message ?? t('term.failed')}`
}

// Control characters by physical key for Ctrl with a non-Latin layout:
// WebKitGTK gives a Cyrillic key no Latin keyCode, and xterm builds
// Ctrl+letter from keyCode — Ctrl+C would not interrupt. Latin layouts stay
// with xterm.
const controlKeys: Record<string, string> = { BracketLeft: '\x1b', Backslash: '\x1c', BracketRight: '\x1d' }

function layoutControl(ev: Pick<KeyboardEvent, 'code' | 'key' | 'ctrlKey' | 'altKey' | 'metaKey'>): string | null {
  if (!ev.ctrlKey || ev.altKey || ev.metaKey || ev.key.length !== 1 || ev.key.charCodeAt(0) < 0x80) return null
  const m = /^Key([A-Z])$/.exec(ev.code)
  if (m) return String.fromCharCode(m[1].charCodeAt(0) - 64)
  return controlKeys[ev.code] ?? null
}
