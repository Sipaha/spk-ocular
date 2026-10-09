import { StrictMode } from 'react'
import { act, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Client } from '../api/client'
import type { TerminalInfo } from '../api/types'
import type { TermTab } from '../dock/store'
import type { TermEnd, TermSink } from './protocol'
import TerminalView from './TerminalView'
import { dock } from '../dock/store'
import { setLanguage } from '../i18n'

// jsdom cannot run xterm (no canvas, no layout): a stand-in that keeps the
// handlers the view installs.
const h = vi.hoisted(() => ({
  terms: [] as FakeTerm[],
  conns: [] as FakeConn[],
  clipboard: [] as ((text: string) => void)[],
}))

interface FakeTerm {
  keys: (ev: Partial<KeyboardEvent>) => boolean
  paste: ReturnType<typeof vi.fn>
}

interface FakeConn {
  sink: TermSink
  live: boolean
  input: ReturnType<typeof vi.fn>
  interrupt: ReturnType<typeof vi.fn>
  close(): void
  end(e: TermEnd): void
}

vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    cols = 80
    rows = 24
    keys: FakeTerm['keys'] = () => true
    paste = vi.fn()
    constructor() {
      h.terms.push(this)
    }
    loadAddon() {}
    open() {}
    onData() {}
    onBinary() {}
    onResize() {}
    attachCustomKeyEventHandler(fn: FakeTerm['keys']) {
      this.keys = fn
    }
    write(_: unknown, done?: () => void) {
      done?.()
    }
    focus() {}
    getSelection() {
      return ''
    }
    dispose() {}
  },
}))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { fit() {} } }))
vi.mock('./clipboard', () => ({
  copyText: vi.fn(async () => {}),
  readText: vi.fn(() => new Promise<string>((resolve) => h.clipboard.push(resolve))),
}))
vi.mock('./protocol', async (orig) => ({
  ...(await orig<typeof import('./protocol')>()),
  TermConnection: class {
    live = true
    input = vi.fn()
    constructor(
      _url: string,
      public sink: TermSink,
    ) {
      h.conns.push(this as unknown as FakeConn)
      sink.phase('running')
    }
    resize() {}
    interrupt = vi.fn()
    close() {
      this.live = false
    }
    end(e: TermEnd) {
      this.live = false
      this.sink.phase('ended')
      this.sink.end(e)
    }
  },
}))

const tab = {
  id: 'tab-1',
  kind: 'term',
  title: 'api-1',
  target: { provider: 'kubernetes', id: 'prod' },
  targetTitle: 'prod',
  open: { ref: { provider: 'kubernetes', target: 'prod', kind: 'pods', scope: 'ns', name: 'api-1' } },
} as unknown as TermTab

const info = (id: string): TerminalInfo =>
  ({ terminalId: id, streamId: `s-${id}`, target: { provider: 'kubernetes', target: 'prod', targetTitle: 'prod', ref: tab.open.ref } }) as unknown as TerminalInfo

function deferred<T>() {
  let resolve!: (v: T) => void
  const promise = new Promise<T>((r) => (resolve = r))
  return { promise, resolve }
}

function fakeClient() {
  return {
    openTerminal: vi.fn(async () => info('t-1')),
    reopenTerminal: vi.fn(async (id: string) => info(id)),
    forgetTerminal: vi.fn(async () => {}),
    streamBase: vi.fn(async () => '/streams'),
  }
}

const pasteKey = { type: 'keydown', code: 'KeyV', ctrlKey: true, shiftKey: true, preventDefault() {} } as Partial<KeyboardEvent>

beforeEach(() => {
  h.terms.length = 0
  h.conns.length = 0
  h.clipboard.length = 0
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      disconnect() {}
    },
  )
})
afterEach(() => vi.unstubAllGlobals())

describe('paste', () => {
  it('goes to the connection the gesture was made in', async () => {
    render(<TerminalView client={fakeClient() as unknown as Client} tab={tab} active mode="browser" />)
    await waitFor(() => expect(h.conns).toHaveLength(1))
    const term = h.terms[0]
    act(() => void term.keys(pasteKey))
    await act(async () => h.clipboard[0]('ls -l'))
    expect(term.paste).toHaveBeenCalledWith('ls -l')
  })

  it('is dropped, visibly, when the terminal reconnected while the clipboard was read', async () => {
    const client = fakeClient()
    render(<TerminalView client={client as unknown as Client} tab={tab} active mode="browser" />)
    await waitFor(() => expect(h.conns).toHaveLength(1))
    const term = h.terms[0]
    act(() => void term.keys(pasteKey))
    act(() => h.conns[0].end({ reason: 'closed', message: '1006' }))
    act(() => screen.getByRole('button', { name: 'Reconnect' }).click())
    await waitFor(() => expect(h.conns).toHaveLength(2))
    await act(async () => h.clipboard[0]('rm -rf build'))
    expect(term.paste).not.toHaveBeenCalled()
    expect(h.conns[1].input).not.toHaveBeenCalled()
    expect(screen.getByText(/paste was not sent/)).toBeInTheDocument()
  })

  it('is dropped, visibly, when the connection ended while the clipboard was read', async () => {
    render(<TerminalView client={fakeClient() as unknown as Client} tab={tab} active mode="browser" />)
    await waitFor(() => expect(h.conns).toHaveLength(1))
    const term = h.terms[0]
    act(() => void term.keys(pasteKey))
    act(() => h.conns[0].end({ reason: 'done' }))
    await act(async () => h.clipboard[0]('make deploy'))
    expect(term.paste).not.toHaveBeenCalled()
    expect(screen.getByText(/paste was not sent/)).toBeInTheDocument()
  })

  it('into an ended terminal says there is no connection and does not read the clipboard', async () => {
    render(<TerminalView client={fakeClient() as unknown as Client} tab={tab} active mode="browser" />)
    await waitFor(() => expect(h.conns).toHaveLength(1))
    act(() => h.conns[0].end({ reason: 'done' }))
    act(() => void h.terms[0].keys(pasteKey))
    expect(h.clipboard).toHaveLength(0)
    expect(screen.getByText(/not connected/)).toBeInTheDocument()
  })
})

describe('terminal lifetime', () => {
  it('a StrictMode remount forgets the terminal its first mount opened late, and keeps its own', async () => {
    const client = fakeClient()
    const first = deferred<TerminalInfo>()
    const second = deferred<TerminalInfo>()
    client.openTerminal.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise)
    const view = render(
      <StrictMode>
        <TerminalView client={client as unknown as Client} tab={tab} active mode="browser" />
      </StrictMode>,
    )
    expect(client.openTerminal).toHaveBeenCalledTimes(2)
    await act(async () => second.resolve(info('t-new')))
    await waitFor(() => expect(h.conns).toHaveLength(1))
    await act(async () => first.resolve(info('t-old')))
    await waitFor(() => expect(client.forgetTerminal).toHaveBeenCalledWith('t-old'))
    expect(client.forgetTerminal).not.toHaveBeenCalledWith('t-new')
    expect(h.conns).toHaveLength(1)
    view.unmount()
    expect(client.forgetTerminal).toHaveBeenCalledWith('t-new')
  })

})

// WebKitGTK gives a Cyrillic key no Latin keyCode, and xterm builds Ctrl+letter
// from keyCode: the control character is taken from the physical key.
describe('Ctrl with a non-Latin layout', () => {
  const key = (code: string, k: string, extra: Partial<KeyboardEvent> = {}) =>
    ({ type: 'keydown', code, key: k, ctrlKey: true, preventDefault() {}, ...extra }) as Partial<KeyboardEvent>

  it('sends the key\'s control character, Ctrl+C as an interrupt', async () => {
    render(<TerminalView client={fakeClient() as unknown as Client} tab={tab} active mode="browser" />)
    await waitFor(() => expect(h.conns).toHaveLength(1))
    const term = h.terms[0]
    const conn = h.conns[0]
    expect(term.keys(key('KeyK', 'л'))).toBe(false)
    expect(conn.input).toHaveBeenCalledWith('\x0b')
    expect(term.keys(key('KeyC', 'с'))).toBe(false)
    expect(conn.interrupt).toHaveBeenCalled()
    expect(term.keys(key('BracketLeft', 'х'))).toBe(false)
    expect(conn.input).toHaveBeenCalledWith('\x1b')
  })

  it('leaves Latin keys, Alt and non-letter keys to xterm', async () => {
    render(<TerminalView client={fakeClient() as unknown as Client} tab={tab} active mode="browser" />)
    await waitFor(() => expect(h.conns).toHaveLength(1))
    const term = h.terms[0]
    expect(term.keys(key('KeyK', 'k'))).toBe(true)
    expect(term.keys(key('KeyK', 'л', { isComposing: true }))).toBe(true)
    expect(term.keys(key('KeyK', 'л', { getModifierState: name => name === 'AltGraph' }))).toBe(true)
    expect(term.keys(key('KeyK', 'л', { altKey: true }))).toBe(true)
    expect(term.keys(key('F6', 'F6'))).toBe(true)
    expect(h.conns[0].input).not.toHaveBeenCalled()
  })
})

describe('plain Cyrillic typing', () => {
  it('sends one character per physical key and suppresses its keypress replay', async () => {
    render(<TerminalView client={fakeClient() as unknown as Client} tab={tab} active mode="browser" />)
    await waitFor(() => expect(h.conns).toHaveLength(1))
    const term=h.terms[0], conn=h.conns[0]
    for (const letter of 'привет') {
      expect(term.keys({type:'keydown',key:letter,code:'KeyK',preventDefault(){}})).toBe(false)
      expect(term.keys({type:'keypress',key:letter,preventDefault(){}})).toBe(false)
    }
    expect(conn.input.mock.calls.map(call=>call[0]).join('')).toBe('привет')
    expect(conn.input).toHaveBeenCalledTimes(6)
    expect(term.keys({type:'keydown',key:'ж',isComposing:true})).toBe(true)
    expect(term.keys({type:'keydown',key:'ж',altKey:true})).toBe(true)
    expect(conn.input).toHaveBeenCalledTimes(6)
  })
})

describe('notice', () => {
  it('belongs to its run: a reconnect clears it, a late one of the old run does not come back', async () => {
    render(<TerminalView client={fakeClient() as unknown as Client} tab={tab} active mode="browser" />)
    await waitFor(() => expect(h.conns).toHaveLength(1))
    const old = h.conns[0]
    act(() => old.sink.notice?.({ key: 'compose.exec.sizeNotSet', text: 'The terminal size could not be set' }))
    expect(screen.getByText('The terminal size could not be set')).toBeInTheDocument()
    act(() => old.end({ reason: 'done', code: 0 }))
    act(() => screen.getByRole('button', { name: 'Reconnect' }).click())
    await waitFor(() => expect(h.conns).toHaveLength(2))
    expect(screen.queryByText('The terminal size could not be set')).not.toBeInTheDocument()
    act(() => old.sink.notice?.({ text: 'late from the old run' }))
    expect(screen.queryByText('late from the old run')).not.toBeInTheDocument()
  })
})

describe('tab title', () => {
  const titled = async (target: Record<string, unknown>) => {
    const update = vi.spyOn(dock, 'update')
    const client = { ...fakeClient(), openTerminal: vi.fn(async () => ({ ...info('t-1'), target: { ...info('t-1').target, ...target } })) }
    render(<TerminalView client={client as unknown as Client} tab={tab} active mode="browser" />)
    await waitFor(() => expect(update).toHaveBeenCalled())
    const title = update.mock.calls[0][1].title
    update.mockRestore()
    return title
  }

  it('is "channel · instance" where there are channels', async () => {
    expect(await titled({ instance: 'api-1-7f9', channel: 'app' })).toBe('app · api-1-7f9')
  })

  it('is the instance alone without a channel level (a container)', async () => {
    expect(await titled({ instance: 'shop-web-2' })).toBe('shop-web-2')
  })
})

describe('a debugger’s tab (attach)', () => {
  const attachTab = { ...tab, open: { ...tab.open, channel: 'debugger-x1y2z', attach: true } } as unknown as TermTab

  it('opens as an attach and reconnects the same way', async () => {
    const client = fakeClient()
    render(<TerminalView client={client as unknown as Client} tab={attachTab} active mode="browser" />)
    await waitFor(() => expect(h.conns).toHaveLength(1))
    expect(client.openTerminal).toHaveBeenCalledWith(expect.objectContaining({ channel: 'debugger-x1y2z', attach: true }))
    act(() => h.conns[0].end({ reason: 'closed', message: 'connection lost' }))
    act(() => screen.getByRole('button', { name: 'Reconnect' }).click())
    await waitFor(() => expect(client.reopenTerminal).toHaveBeenCalled())
  })

  it('the reason of its end is said in the UI\'s language', async () => {
    setLanguage('ru')
    try {
      render(<TerminalView client={fakeClient() as unknown as Client} tab={attachTab} active mode="browser" />)
      await waitFor(() => expect(h.conns).toHaveLength(1))
      act(() =>
        h.conns[0].end({ reason: 'error', class: 'gone', message: 'debugger d has ended; open a new one (Debug…)', why: { key: 'kubernetes.debug.ended', text: 'debugger d has ended; open a new one (Debug…)', params: { container: 'd' } } }),
      )
      expect(screen.getByRole('alert')).toHaveTextContent('отладчик d завершился; откройте новый («Отладить…»)')
    } finally {
      setLanguage('en')
    }
  })

  it('an ended debugger offers no new terminal (it cannot restart): its text says to debug again', async () => {
    const open = vi.spyOn(dock, 'openTerminal')
    render(<TerminalView client={fakeClient() as unknown as Client} tab={attachTab} active mode="browser" />)
    await waitFor(() => expect(h.conns).toHaveLength(1))
    act(() => h.conns[0].end({ reason: 'error', class: 'gone', message: 'debugger debugger-x1y2z has ended; open a new one (Debug…)' }))
    expect(screen.getByRole('alert')).toHaveTextContent('has ended')
    expect(screen.queryByRole('button', { name: 'Open a new terminal' })).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Reconnect' })).not.toBeInTheDocument()
    expect(open).not.toHaveBeenCalled()
    open.mockRestore()
  })
})
