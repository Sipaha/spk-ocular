// The page half of the terminal WebSocket (internal/streams/term.go).
//
// Binary messages are terminal bytes; text messages are JSON control:
//   page→server  {"k":"resize","cols","rows"}  {"k":"ack","n"}  {"k":"intr"}
//   server→page  {"k":"state","state"}  {"k":"iack","n"}  {"k":"notice","message"}
//                {"k":"exit","code"}  {"k":"end",...}
// "notice" is about the terminal itself (its size could not be set, …), shown
// apart from the output.
//
// Output credit: "ack" carries the cumulative number of output bytes the
// terminal has *processed* (xterm's write callback), so a page that cannot
// keep up slows the command down instead of buffering without bound.
// Input credit: at most IN_WINDOW bytes are sent and not yet confirmed by
// "iack"; a paste waits in a bounded local queue meanwhile.
// Ctrl+C is "intr", outside the window: the server drops input it still
// queues (iack counts it) and writes ^C next, so it gets through even when
// the command does not read and the window is exhausted.

import { asMessage } from '../api/client'
import type { Message } from '../api/types'

export const IN_WINDOW = 256 << 10
export const IN_CHUNK = 16 << 10
export const IN_QUEUE_MAX = 4 << 20
/** Ack at the latest once this much processed output is unacknowledged. */
const ACK_EVERY = 64 << 10
const ACK_DELAY_MS = 20

export type TermPhase = 'opening' | 'connecting' | 'running' | 'ended'

export interface TermEnd {
  reason: 'done' | 'error' | 'gone' | 'closed'
  class?: string
  message?: string
  /** the provider's own reason by key (said in the UI's language) */
  why?: Message
  /** exit code, when the command's end was observed */
  code?: number
}

export interface TermSink {
  /** Output bytes; call done() once they are processed (credits the server). */
  output(data: Uint8Array, done: () => void): void
  phase(p: TermPhase): void
  /** A notice about the terminal from the provider. */
  notice?(m: Message): void
  /** The session is over (exit code first when known). */
  end(e: TermEnd): void
}

/** Minimal WebSocket surface (tests pass a fake). */
export interface WSLike {
  binaryType: string
  readyState: number
  onopen: ((ev: unknown) => void) | null
  onmessage: ((ev: { data: unknown }) => void) | null
  onclose: ((ev: { code: number; reason: string }) => void) | null
  onerror: ((ev: unknown) => void) | null
  send(data: string | ArrayBufferLike | Uint8Array): void
  close(code?: number, reason?: string): void
}

export type WSFactory = (url: string) => WSLike

const OPEN = 1

/** The WebSocket base for a stream base (desktop: http://127.0.0.1…; browser: a path). */
export function wsBase(streamBase: string, loc: Pick<Location, 'protocol' | 'host'> = window.location): string {
  if (/^https?:\/\//.test(streamBase)) return streamBase.replace(/^http/, 'ws')
  return `${loc.protocol === 'https:' ? 'wss' : 'ws'}://${loc.host}${streamBase}`
}

export class PasteTooLargeError extends Error {
  constructor() {
    super('paste too large')
  }
}

const enc = new TextEncoder()

/**
 * One connection of a terminal. A reconnect is a new TermConnection: the
 * old one's late callbacks (xterm write callbacks, timers, socket events)
 * are ignored because it is closed — they never credit or end the new one.
 */
export class TermConnection {
  private ws: WSLike
  private closed = false
  private ended = false
  // output credit
  private processed = 0
  private acked = 0
  private ackTimer: ReturnType<typeof setTimeout> | null = null
  // input credit
  private sent = 0
  private confirmed = 0
  private queue: Uint8Array[] = []
  private queued = 0
  private exitCode: number | undefined
  private lastSize: { cols: number; rows: number } | null = null

  constructor(
    url: string,
    private sink: TermSink,
    factory: WSFactory = (u) => new WebSocket(u) as unknown as WSLike,
  ) {
    this.ws = factory(url)
    this.ws.binaryType = 'arraybuffer'
    sink.phase('opening')
    this.ws.onopen = () => {
      if (this.closed) return
      if (this.lastSize) this.sendResize(this.lastSize.cols, this.lastSize.rows)
      this.pump()
    }
    this.ws.onmessage = (ev) => this.onMessage(ev.data)
    this.ws.onclose = (ev) => {
      if (this.closed) return
      this.finish({ reason: ev.code === 1000 ? 'done' : 'closed', message: ev.code === 1000 ? undefined : `${ev.code} ${ev.reason}`.trim() })
    }
    this.ws.onerror = () => {}
  }

  /** Not closed or ended: input is still accepted. */
  get live(): boolean {
    return !this.closed
  }

  /** Bytes queued locally, not yet sent (paste in progress). */
  get pending(): number {
    return this.queued
  }

  /** Bytes sent but not yet written to the command's stdin. */
  get inFlight(): number {
    return this.sent - this.confirmed
  }

  private onMessage(data: unknown) {
    if (this.closed) return
    if (typeof data === 'string') {
      this.onControl(data)
      return
    }
    const bytes = data instanceof ArrayBuffer ? new Uint8Array(data) : (data as Uint8Array)
    const n = bytes.byteLength
    this.sink.output(bytes, () => this.credit(n))
  }

  private onControl(text: string) {
    let m: { k?: string; state?: string; n?: number; code?: number; reason?: string; class?: string; message?: unknown; why?: unknown }
    try {
      m = JSON.parse(text)
    } catch {
      return
    }
    switch (m.k) {
      case 'state':
        if (m.state === 'connecting' || m.state === 'running') this.sink.phase(m.state)
        break
      case 'iack':
        if (!Number.isInteger(m.n) || m.n! < this.confirmed || m.n! > this.sent) {
          this.finish({ reason: 'error', class: 'internal', message: 'terminal protocol violation (input counter)' })
          return
        }
        this.confirmed = m.n!
        this.pump()
        break
      case 'notice': {
        const msg = m.message as Message | undefined
        if (msg && typeof msg.text === 'string') this.sink.notice?.(msg)
        break
      }
      case 'exit':
        if (typeof m.code === 'number') this.exitCode = m.code
        break
      case 'end':
        this.finish({
          reason: m.reason === 'done' || m.reason === 'gone' || m.reason === 'error' ? m.reason : 'error',
          class: m.class,
          message: typeof m.message === 'string' ? m.message : undefined,
          ...(asMessage(m.why) ? { why: asMessage(m.why) } : {}),
        })
        break
    }
  }

  private credit(n: number) {
    if (this.closed) return // a callback of a connection that is gone
    this.processed += n
    if (this.processed - this.acked >= ACK_EVERY) {
      this.flushAck()
    } else if (!this.ackTimer) {
      this.ackTimer = setTimeout(() => {
        this.ackTimer = null
        this.flushAck()
      }, ACK_DELAY_MS)
    }
  }

  private flushAck() {
    if (this.ackTimer) {
      clearTimeout(this.ackTimer)
      this.ackTimer = null
    }
    if (this.closed || this.processed === this.acked || this.ws.readyState !== OPEN) return
    this.acked = this.processed
    this.ws.send(JSON.stringify({ k: 'ack', n: this.processed }))
  }

  /** Input (typed or pasted). Throws PasteTooLargeError beyond the local queue; nothing is dropped silently. */
  input(data: string | Uint8Array) {
    if (this.closed) return
    const bytes = typeof data === 'string' ? enc.encode(data) : data
    if (this.queued + bytes.byteLength > IN_QUEUE_MAX) throw new PasteTooLargeError()
    for (let off = 0; off < bytes.byteLength; off += IN_CHUNK) {
      const c = bytes.subarray(off, Math.min(bytes.byteLength, off + IN_CHUNK))
      this.queue.push(c)
      this.queued += c.byteLength
    }
    this.pump()
  }

  private pump() {
    if (this.closed || this.ws.readyState !== OPEN) return
    while (this.queue.length) {
      const c = this.queue[0]
      if (this.sent - this.confirmed + c.byteLength > IN_WINDOW) return
      this.queue.shift()
      this.queued -= c.byteLength
      this.sent += c.byteLength
      this.ws.send(c)
    }
  }

  /** Drops input not yet sent (closing). */
  cancelInput() {
    this.queue = []
    this.queued = 0
  }

  /**
   * Ctrl+C: drops the local queue (a paste in progress) and sends "intr":
   * the server drops what it still queues and writes ^C first. Bytes already
   * written to the command's stdin cannot be recalled.
   */
  interrupt() {
    if (this.closed) return
    this.cancelInput()
    if (this.ws.readyState === OPEN) this.ws.send(JSON.stringify({ k: 'intr' }))
  }

  resize(cols: number, rows: number) {
    if (cols < 1 || rows < 1) return
    this.lastSize = { cols, rows }
    if (!this.closed && this.ws.readyState === OPEN) this.sendResize(cols, rows)
  }

  private sendResize(cols: number, rows: number) {
    this.ws.send(JSON.stringify({ k: 'resize', cols: Math.min(1000, cols), rows: Math.min(1000, rows) }))
  }

  private finish(e: TermEnd) {
    if (this.ended) return
    this.ended = true
    if (this.exitCode !== undefined) e = { ...e, code: this.exitCode }
    this.sink.phase('ended')
    this.sink.end(e)
    this.close()
  }

  /** Ends this connection; its later events are ignored. */
  close() {
    if (this.closed) return
    this.closed = true
    this.cancelInput()
    if (this.ackTimer) clearTimeout(this.ackTimer)
    this.ackTimer = null
    try {
      this.ws.close(1000, 'closed')
    } catch {
      // already closed
    }
  }
}
