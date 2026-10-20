import {SerializeAddon} from '../../vendor/addon-serialize/addon-serialize.mjs'
import {ToolWindowBridge} from '../tools/windowBridge'
import {ActionIconButton} from '../components/ActionIconButton'
import {WindowActionIcon} from '../components/icons'
import { layoutResizing, useLayoutResizing } from '../components/layoutResize'
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
import { clearDeliveredInput } from './inputResidue'
import { showNotice } from '../store'

// xterm needs resolved colours, so read the same tokens as the rest of the
// interface when mounting, after the stylesheet has loaded.
function terminalTheme(): ITheme {
  const css = getComputedStyle(document.documentElement)
  const color = (name: string) => css.getPropertyValue(`--color-${name}`).trim() || undefined
  return {
    background: color('app'),
    foreground: color('fg'),
    cursor: color('fg'),
    cursorAccent: color('app'),
    selectionBackground: color('active'),
    black: color('ansi-0'),
    red: color('ansi-1'),
    green: color('ansi-2'),
    yellow: color('ansi-3'),
    blue: color('ansi-4'),
    magenta: color('ansi-5'),
    cyan: color('ansi-6'),
    white: color('ansi-7'),
    brightBlack: color('ansi-8'),
    brightRed: color('ansi-9'),
    brightGreen: color('ansi-10'),
    brightYellow: color('ansi-11'),
    brightBlue: color('ansi-12'),
    brightMagenta: color('ansi-13'),
    brightCyan: color('ansi-14'),
    brightWhite: color('ansi-15'),
  }
}

const FONT = '"JetBrains Mono", "SF Mono", "Cascadia Code", "Fira Code", "Roboto Mono", ui-monospace, Menlo, Consolas, monospace'
const DIM = (s: string) => `\x1b[2m${s}\x1b[22m`

export interface Props {
  client: Client
  tab: TermTab
  active: boolean
  mode: 'desktop' | 'browser'
  bridge?:ToolWindowBridge
  detached?:boolean
  onWindowAction?:()=>void
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
export default function TerminalView({ client, tab, active, mode, bridge, detached=false, onWindowAction }: Props) {
  const resizing = useLayoutResizing()
  const hostRef = useRef<HTMLDivElement>(null)
  const termRef = useRef<Terminal | null>(null)
  const fitRef = useRef<FitAddon | null>(null)
  const connRef = useRef<Pick<TermConnection,'input'|'interrupt'|'resize'|'close'|'live'> | null>(null)
  const idRef = useRef<string | null>(null)
  // Every (re)connect is a generation: late answers and callbacks of an
  // older one never touch the newer.
  const genRef = useRef(0)
  const [phase, setPhase] = useState<TermPhase>('opening')
  const [ended, setEnded] = useState<TermEnd | null>(null)
  const [openError, setOpenError] = useState<{ text: string; code?: string } | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [confirmAgain, setConfirmAgain] = useState(false)
  const detachedRef=useRef(detached)
  useEffect(()=>{detachedRef.current=detached},[detached])
  const statusRef=useRef({phase,ended,notice,openError})
  useEffect(()=>{statusRef.current={phase,ended,notice,openError}},[phase,ended,notice,openError])
  const custom = !!tab.open.command?.length

  const connectRef = useRef<(again: boolean) => Promise<void>>(async () => {})

  useEffect(() => {
    const host = hostRef.current!
    const gens = genRef
    // This mount's life (StrictMode mounts twice: a ref would be revived
    // by the second mount while the first one's open is still pending).
    let alive = true
    const forget = (id: string) => {
      void client.forgetTerminal(id).catch(e => {
        if (tab.open.preparedId) showNotice(`${tab.targetTitle} · ${errText(e)}`, 30_000)
      })
    }
    let peer=false,sequence=0
    const waiting=new Map<number,()=>void>()
    const releasePeer=()=>{peer=false;if(bridge?.role==='owner'&&termRef.current)termRef.current.options.disableStdin=false;for(const done of waiting.values())done();waiting.clear()}
    const publish=(data:Uint8Array,done:()=>void)=>{
      if(!peer||!bridge){done();return}
      const seq=++sequence;waiting.set(seq,done)
      void bridge.post('terminal-output',{seq,data:Array.from(data)}).catch(e=>{releasePeer();setNotice(String(e))})
    }
    const write=(term:Terminal,data:string|Uint8Array,done:()=>void=()=>{})=>term.write(data,()=>publish(typeof data==='string'?new TextEncoder().encode(data):data,done))
    const connect = async (again: boolean) => {
      if(bridge?.role==='guest'){await bridge.post('terminal-reconnect',{again});return}
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
        const prepared = idRef.current ?? tab.open.preparedId
        info = prepared
          ? await client.reopenTerminal(prepared, cols, rows)
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
        if (!alive || info.terminalId !== idRef.current) forget(info.terminalId)
        return
      }
      idRef.current = info.terminalId
      const tg = info.target
      dock.update(tab.id, { title: tab.open.preparedId ? `${t('act.node-shell')} · ${tg.ref.name}` : tg.channel && tg.instance ? `${tg.channel} · ${tg.instance}` : tg.instance || tab.title, hint: describe(info), rev: tg.configRev })
      let base: string
      try {
        base = wsBase(await client.streamBase())
      } catch (e) {
        if (gen === genRef.current) setOpenError({ text: errText(e) })
        return
      }
      if (gen !== genRef.current || !alive) return
      if (again) write(term,`\r\n${DIM(`──── ${t('term.reconnected')} ────`)}\r\n`)
      const conn = new TermConnection(`${base}/term/${encodeURIComponent(info.streamId)}`, {
        output: (data, done) => (gen === genRef.current ? write(term,data,done) : done()),
        phase: (p) => gen === genRef.current && setPhase(p),
        notice: (m) => gen === genRef.current && setNotice(messageText(m)),
        end: (e) => {
          if (gen !== genRef.current) return
          setEnded(e)
          write(term,`\r\n${DIM(`[${endText(e)}]`)}\r\n`)
        },
      })
      connRef.current = conn
      conn.resize(term.cols, term.rows)
    }
    connectRef.current = connect

    const term = new Terminal({
      scrollback: 5000,
      theme: terminalTheme(),
      fontFamily: FONT,
      fontSize: 14,
      cursorBlink: true,
      allowProposedApi: false,
      // Links in the output are never opened (untrusted data).
      linkHandler: { activate: () => {} },
    })
    const fit = new FitAddon()
    const serialize=new SerializeAddon()
    term.loadAddon(serialize)
    term.loadAddon(fit)
    term.open(host)
    const clearInput = term.textarea ? clearDeliveredInput(term.textarea, () => !!term.options.screenReaderMode) : () => {}
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
      if (ev.isComposing || ev.getModifierState?.('AltGraph')) return true
      // WebKitGTK's Cyrillic key events can replay the growing helper textarea.
      // Handle plain physical typing once; composition and AltGr stay with xterm.
      if (!ev.ctrlKey && !ev.altKey && !ev.metaKey && /^[\p{Script=Cyrillic}]$/u.test(ev.key ?? '') && (ev.type === 'keydown' || ev.type === 'keypress')) {
        ev.preventDefault()
        if (ev.type === 'keydown') {
          if (term.textarea) term.textarea.value = ''
          send(ev.key)
        }
        return false
      }
      if (ev.type !== 'keydown') return true
      if (!ev.altKey && isShortcut(ev, 'KeyC', { ctrl: true, shift: true })) {
        ev.preventDefault()
        const sel = term.getSelection()
        if (sel) void copyText(sel, mode)
        return false
      }
      if (!ev.altKey && isShortcut(ev, 'KeyV', { ctrl: true, shift: true })) {
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
          if (genRef.current !== gen || connRef.current !== conn || !conn.live || (bridge?.role==='owner'&&peer)) {
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

    const flushTerminal=()=>new Promise<void>(resolve=>term.write('',resolve))
    const clearTerminalFlush=bridge?.role==='guest'?bridge.bindTerminalFlush(flushTerminal):undefined
    const offBridge=bridge?.listen(message=>{
      if(bridge.role==='owner'){
        if(message.type==='terminal-ready'){
          releasePeer()
          term.write('',()=>{
            const boot={text:serialize.serialize({scrollback:5000}),cols:term.cols,rows:term.rows,status:statusRef.current}
            peer=true
            // Only the visible parser answers DA/DSR/OSC queries. The owner
            // still parses output, but must not generate a duplicate PTY reply.
            term.options.disableStdin=true
            void bridge.post('terminal-snapshot',boot).catch(e=>{releasePeer();setNotice(String(e))})
          })
        }
        if(message.type==='terminal-ack'){const seq=Number(message.payload);waiting.get(seq)?.();waiting.delete(seq)}
        if(message.type==='terminal-input'&&peer)send(String(message.payload))
        if(message.type==='terminal-binary'&&peer)connRef.current?.input(Uint8Array.from(message.payload as number[]))
        if(message.type==='terminal-resize'){const size=message.payload as {cols:number;rows:number};if(Number.isInteger(size.cols)&&Number.isInteger(size.rows)&&size.cols>0&&size.rows>0&&size.cols<=1000&&size.rows<=1000)term.resize(size.cols,size.rows)}
        if(message.type==='terminal-reconnect')void connect(true)
        if(message.type==='terminal-new')dock.openTerminal(tab.target,tab.targetTitle,{...tab.open,instance:undefined})
        if(['return','closed'].includes(message.type))releasePeer()
      }else{
        if(message.type==='terminal-snapshot'){
          const boot=message.payload as {text:string;cols:number;rows:number;status:typeof statusRef.current}
          term.resize(boot.cols,boot.rows);term.reset();term.write(boot.text,()=>{fit.fit();term.focus()})
          setPhase(boot.status.phase);setEnded(boot.status.ended);setNotice(boot.status.notice);setOpenError(boot.status.openError)
        }
        if(message.type==='terminal-output'){
          const frame=message.payload as {seq:number;data:number[]}
          term.write(Uint8Array.from(frame.data),()=>void bridge.post('terminal-ack',frame.seq))
        }
        if(message.type==='terminal-status'){
          const status=message.payload as typeof statusRef.current
          setPhase(status.phase);setEnded(status.ended);setNotice(status.notice);setOpenError(status.openError)
        }
      }
    })
    if(bridge?.role==='guest'){
      const remote=(type:string,payload:unknown)=>{void bridge.post(type,payload).catch(e=>setNotice(String(e)))}
      connRef.current={live:true,input:data=>{remote(typeof data==='string'?'terminal-input':'terminal-binary',typeof data==='string'?data:Array.from(data))},interrupt:()=>{remote('terminal-input','\x03')},resize:(cols,rows)=>{remote('terminal-resize',{cols,rows})},close:()=>{}}
    }
    // Fit only to a real size: a hidden tab keeps its last valid geometry.
    let timer: ReturnType<typeof setTimeout> | null = null
    const ro = new ResizeObserver(() => {
      if (timer) clearTimeout(timer)
      timer = setTimeout(() => {
        timer = null
        if (!detachedRef.current && !layoutResizing() && host.clientWidth > 0 && host.clientHeight > 0) fit.fit()
      }, 50)
    })
    ro.observe(host)

    // StrictMode's discarded mount must not consume a reviewed one-shot
    // terminal. Ordinary terminals keep their independent-open behavior.
    const initial = bridge?.role==='guest'?setTimeout(()=>void bridge.post('terminal-ready'),0):tab.open.preparedId ? setTimeout(() => void connect(false), 0) : null
    if (bridge?.role!=='guest'&&!tab.open.preparedId) void connect(false)
    return () => {
      if (initial !== null) clearTimeout(initial)
      alive = false
      offBridge?.();releasePeer()
      clearTerminalFlush?.()
      gens.current++
      ro.disconnect()
      if (timer) clearTimeout(timer)
      connRef.current?.close()
      connRef.current = null
      if (idRef.current) forget(idRef.current)
      idRef.current = null
      clearInput()
      term.dispose()
      termRef.current = null
    }
    // Mounted once per tab: the tab's open request never changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // Activation: one fit (the size may have changed while hidden) and focus.
  useEffect(() => {
    if (!active || resizing || detached) return
    const id = requestAnimationFrame(() => {
      const host = hostRef.current
      if (host && host.clientWidth > 0 && host.clientHeight > 0) fitRef.current?.fit()
      termRef.current?.focus()
    })
    return () => cancelAnimationFrame(id)
  }, [active, resizing, detached])

  useEffect(()=>{if(bridge?.role==='owner'&&bridge.opened)void bridge.update('terminal-status',{phase,ended,notice,openError}).catch(()=>{})},[bridge,phase,ended,notice,openError])

  const again = () => {
    if (custom && !confirmAgain) {
      setConfirmAgain(true) // a custom command is never repeated without asking
      return
    }
    void connectRef.current(true)
    termRef.current?.focus()
  }
  const openNew = () => {if(bridge?.role==='guest')void bridge.post('terminal-new').catch(e=>setNotice(String(e)));else dock.openTerminal(tab.target,tab.targetTitle,{...tab.open,instance:undefined})}

  const gone = openError?.code === 'gone' || ended?.class === 'gone'
  return (
    <div className="flex h-full min-h-0 flex-col" data-terminal>
      {onWindowAction&&<header className="flex h-[30px] shrink-0 justify-end border-b border-line px-2"><ActionIconButton label={t(bridge?.role==='guest'?'logs.returnWindow':'logs.detachWindow')} description={t(bridge?.role==='guest'?'logs.returnWindow':'logs.detachWindow')} onClick={onWindowAction}><WindowActionIcon returning={bridge?.role==='guest'} className="h-4 w-4"/></ActionIconButton></header>}
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
              {tab.open.preparedId && <span className="text-fg-muted">{t('term.reviewNodeShell')}</span>}
              {!gone && !tab.open.preparedId && (
                <button className="rounded-md bg-accent px-2 py-0.5 text-accent-fg" onClick={again}>
                  {t('term.reconnect')}
                </button>
              )}
              {/* An ended debugger cannot restart: its text says to debug again. */}
              {gone && !tab.open.attach && !tab.open.preparedId && (
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
  return `${e.class ? classLabel(e.class) + ': ' : ''}${e.why ? messageText(e.why) : (e.message ?? t('term.failed'))}`
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
