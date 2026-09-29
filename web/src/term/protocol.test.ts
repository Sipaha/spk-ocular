import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { IN_CHUNK, IN_QUEUE_MAX, IN_WINDOW, PasteTooLargeError, TermConnection, wsBase, type TermEnd, type TermPhase, type WSLike } from './protocol'

class FakeWS implements WSLike {
  binaryType = 'blob'
  readyState = 0
  onopen: ((ev: unknown) => void) | null = null
  onmessage: ((ev: { data: unknown }) => void) | null = null
  onclose: ((ev: { code: number; reason: string }) => void) | null = null
  onerror: ((ev: unknown) => void) | null = null
  sent: (string | Uint8Array)[] = []
  closed: number | null = null
  constructor(public url: string) {}
  send(d: string | ArrayBufferLike | Uint8Array) {
    this.sent.push(typeof d === 'string' ? d : new Uint8Array(d as ArrayBuffer))
  }
  close(code?: number) {
    this.closed = code ?? 1000
    this.readyState = 3
  }
  open() {
    this.readyState = 1
    this.onopen?.({})
  }
  out(n: number) {
    this.onmessage?.({ data: new Uint8Array(n).buffer })
  }
  ctl(m: object) {
    this.onmessage?.({ data: JSON.stringify(m) })
  }
  controls(k: string) {
    return this.sent.filter((s): s is string => typeof s === 'string').map((s) => JSON.parse(s)).filter((m) => m.k === k)
  }
  bytes() {
    return this.sent.filter((s): s is Uint8Array => typeof s !== 'string')
  }
}

function setup() {
  const sockets: FakeWS[] = []
  const done: (() => void)[] = []
  const phases: TermPhase[] = []
  const ends: TermEnd[] = []
  const written: number[] = []
  const conn = new TermConnection(
    'ws://x/term/1',
    {
      output: (d, cb) => {
        written.push(d.byteLength)
        done.push(cb)
      },
      phase: (p) => phases.push(p),
      end: (e) => ends.push(e),
    },
    (u) => {
      const ws = new FakeWS(u)
      sockets.push(ws)
      return ws
    },
  )
  return { conn, ws: sockets[0], done, phases, ends, written }
}

beforeEach(() => vi.useFakeTimers())
afterEach(() => vi.useRealTimers())

describe('TermConnection', () => {
  it('acks cumulatively once output is processed, not when it arrives', () => {
    const { ws, done } = setup()
    ws.open()
    ws.out(1000)
    ws.out(2000)
    vi.advanceTimersByTime(100)
    expect(ws.controls('ack')).toEqual([]) // received, not yet processed by xterm
    done[0]()
    done[1]()
    vi.advanceTimersByTime(25)
    expect(ws.controls('ack')).toEqual([{ k: 'ack', n: 3000 }]) // one coalesced, cumulative
    ws.out(70 << 10)
    done[2]()
    expect(ws.controls('ack').at(-1)).toEqual({ k: 'ack', n: 3000 + (70 << 10) }) // a big chunk acks at once
  })

  it('keeps at most the input window in flight and resumes on iack', () => {
    const { conn, ws } = setup()
    ws.open()
    conn.input(new Uint8Array(IN_WINDOW + 3 * IN_CHUNK))
    const sent = ws.bytes()
    expect(sent.every((c) => c.byteLength <= IN_CHUNK)).toBe(true)
    expect(sent.reduce((a, c) => a + c.byteLength, 0)).toBe(IN_WINDOW)
    expect(conn.pending).toBe(3 * IN_CHUNK)
    ws.ctl({ k: 'iack', n: 2 * IN_CHUNK })
    expect(conn.pending).toBe(IN_CHUNK)
    ws.ctl({ k: 'iack', n: 2 * IN_CHUNK }) // repeated: harmless
    ws.ctl({ k: 'iack', n: 10 * IN_WINDOW }) // impossible: ignored
    expect(conn.inFlight).toBe(IN_WINDOW)
  })

  it('refuses a paste beyond the local queue and drops nothing already queued', () => {
    const { conn, ws } = setup() // not open yet: everything queues
    conn.input(new Uint8Array(IN_QUEUE_MAX - 10))
    expect(() => conn.input(new Uint8Array(11))).toThrow(PasteTooLargeError)
    expect(conn.pending).toBe(IN_QUEUE_MAX - 10)
    conn.cancelInput()
    expect(conn.pending).toBe(0)
    ws.open()
    expect(ws.bytes()).toHaveLength(0)
  })

  it('sends the size once open and the latest size after', () => {
    const { conn, ws } = setup()
    conn.resize(80, 24)
    conn.resize(100, 30)
    expect(ws.controls('resize')).toEqual([])
    ws.open()
    expect(ws.controls('resize')).toEqual([{ k: 'resize', cols: 100, rows: 30 }])
  })

  it('reports the exit code with the end, after the output', () => {
    const { ws, ends, phases, written } = setup()
    ws.open()
    ws.ctl({ k: 'state', state: 'running' })
    ws.out(10)
    ws.ctl({ k: 'exit', code: 3 })
    ws.ctl({ k: 'end', reason: 'done' })
    expect(written).toEqual([10])
    expect(ends).toEqual([{ reason: 'done', code: 3 }])
    expect(phases).toEqual(['opening', 'running', 'ended'])
    expect(ws.closed).toBe(1000)
  })

  it('an abnormal close without end is a lost connection, not an exit', () => {
    const { ws, ends } = setup()
    ws.open()
    ws.onclose?.({ code: 1006, reason: '' })
    expect(ends).toEqual([{ reason: 'closed', message: '1006' }])
  })

  it('a closed connection never credits: late xterm callbacks of an old generation are ignored', () => {
    const { conn, ws, done } = setup()
    ws.open()
    ws.out(100)
    conn.close() // reconnect: this generation is over
    done[0]()
    vi.advanceTimersByTime(100)
    expect(ws.controls('ack')).toEqual([])
    ws.out(5) // late messages too
    expect(done).toHaveLength(1)
  })
})

describe('wsBase', () => {
  it('turns the stream base into a WebSocket base', () => {
    expect(wsBase('http://127.0.0.1:4567/tok', { protocol: 'wails:', host: 'localhost' })).toBe('ws://127.0.0.1:4567/tok')
    expect(wsBase('/streams/tok', { protocol: 'http:', host: '127.0.0.1:5190' })).toBe('ws://127.0.0.1:5190/streams/tok')
    expect(wsBase('/streams/tok', { protocol: 'https:', host: 'h' })).toBe('wss://h/streams/tok')
  })
})
