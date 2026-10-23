import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Ref } from '../api/types'
import { fakeClient, k8s } from '../test/fakeClient'
import LogViewer from './LogViewer'
import { useStore } from '../store'
import { LogWindowBridge, type WindowMessage } from './windowBridge'
import { append, EMPTY_WINDOW, Ingest } from './buffer'

/** A controllable NDJSON response body. */
function stream() {
  let ctl!: ReadableStreamDefaultController<Uint8Array>
  const body = new ReadableStream<Uint8Array>({ start: (c) => void (ctl = c) })
  const enc = new TextEncoder()
  return {
    response: new Response(body, { status: 200, headers: { 'Content-Type': 'application/x-ndjson' } }),
    send: (...frames: unknown[]) => ctl.enqueue(enc.encode(frames.map((f) => JSON.stringify(f) + '\n').join(''))),
    raw: (text: string) => ctl.enqueue(enc.encode(text)),
    close: () => ctl.close(),
  }
}

const pod: Ref = { provider: 'kubernetes', target: 'dev', scope: 'ns', kind: 'apps/deployments', name: 'web', uid: 'u1' }

afterEach(() => { vi.unstubAllGlobals(); useStore.setState({ view: null }) })

it.each(['pointer', 'keyboard'])('keeps later %s focus during LogInfo loading and honors a new explicit open',async(input)=>{
 const {client}=fakeClient([k8s('dev')]),info=await client.logInfo(pod)
 let ready!:(value:typeof info)=>void
 client.logInfo=vi.fn(()=>new Promise<typeof info>(resolve=>ready=resolve))
 vi.stubGlobal('fetch',vi.fn(async()=>stream().response))
 const content=(epoch:number)=><><button>Another resource</button><LogViewer client={client} subject={pod} active focusEpoch={epoch}/></>
 const view=render(content(1)),other=screen.getByRole('button',{name:'Another resource'})
 if(input==='pointer')fireEvent.pointerDown(other)
 else fireEvent.keyDown(other,{key:'ArrowDown'})
 other.focus()
 await act(async()=>ready(info))
 const viewport=view.container.querySelector('[data-log-viewport]')!
 expect(viewport).toBeInTheDocument()
 await act(()=>new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve()))))
 expect(other).toHaveFocus()
 view.rerender(content(2))
 await waitFor(()=>expect(viewport).toHaveFocus())
})

describe('LogViewer', () => {
  it('shows atomic partial history with a reserved loader and replaces it before live appends', async () => {
    const { client: c } = fakeClient([k8s('dev')])
    const body = stream()
    vi.stubGlobal('fetch', vi.fn(async () => body.response))
    const { container } = render(<LogViewer client={c} subject={pod} active />)
    await waitFor(() => expect(c.openLogStream).toHaveBeenCalledTimes(1))
    expect(container.querySelector('[data-log-loading]')).toBeInTheDocument()
    expect(screen.queryByText('No log lines')).not.toBeInTheDocument()
    expect(container.querySelector('[data-log-empty-state]')).toHaveTextContent('Loading…')
    act(() => body.send(
      { k: 'source', id: 1, key: 'pod/app', label: 'pod/app' },
      { k: 'history-start' },
      { k: 'lines', s: 1, l: [['2026-10-19T00:00:00Z', 'ERROR partial']] },
      { k: 'history-end' },
    ))
    await screen.findByText('ERROR partial')
    expect(container.querySelector('[data-log-loading]')).toBeInTheDocument()
    act(() => body.send(
      { k: 'history-start' },
      { k: 'lines', s: 1, l: [['2026-10-19T00:00:00Z', 'ERROR partial'], ['2026-10-19T00:00:01Z', 'ERROR captured live']] },
      { k: 'history-end' },
      { k: 'ready' },
    ))
    await screen.findByText('ERROR captured live')
    expect(screen.getAllByText('ERROR partial')).toHaveLength(1)
    expect(screen.getByLabelText('line count')).toHaveTextContent('2 lines')
    expect(container.querySelector('[data-log-loading]')).not.toBeInTheDocument()
    act(() => body.send({ k: 'lines', s: 1, l: [['2026-10-19T00:00:02Z', 'ERROR newest']] }))
    await screen.findByText('ERROR newest')
    expect(screen.getByLabelText('line count')).toHaveTextContent('3 lines')
    expect(c.openLogStream).toHaveBeenCalledTimes(1)
  })

  it('shows an empty state only after history is ready', async () => {
    const { client: c } = fakeClient([k8s('dev')])
    const body = stream()
    vi.stubGlobal('fetch', vi.fn(async () => body.response))
    const { container } = render(<LogViewer client={c} subject={pod} active />)
    await waitFor(() => expect(c.openLogStream).toHaveBeenCalledTimes(1))
    expect(screen.queryByText('No log lines')).not.toBeInTheDocument()
    act(() => body.send({ k: 'ready' }))
    await screen.findByText('No log lines')
    expect(container.querySelector('[data-log-loading]')).not.toBeInTheDocument()
  })

  it('a detached viewer rejects frames, snapshots, status and resets from a previous filter', async () => {
    const { client: c } = fakeClient([k8s('dev')])
    const bridge = new LogWindowBridge('fenced-view', 'guest')
    const listeners = new Set<(message: WindowMessage) => void>()
    vi.spyOn(bridge, 'listen').mockImplementation(listener => { listeners.add(listener); return () => { listeners.delete(listener) } })
    const emit = (type: string, payload: unknown) => act(() => {
      for (const listener of listeners) listener({ id: bridge.id, sender: 'owner', type, payload })
    })
    render(<LogViewer client={c} subject={pod} active bridge={bridge} initial={{ channel: 'app', tail: 500, since: null, previous: false, showTime: false, showSource: null, wrap: false, follow: true, filterText: 'WARN' }} />)
    await waitFor(() => expect(listeners.size).toBeGreaterThan(0))
    const query = { channel: 'app', previous: false, follow: true, tailLines: 500, filter: 'WARN' }
    const stale = { ...query, filter: 'INFO' }
    emit('frame', { query, frame: { k: 'source', id: 1, key: 'current/app', label: 'current/app' } })
    emit('frame', { query, frame: { k: 'lines', s: 1, l: [['2026-10-19T00:00:00Z', 'WARN current']] } })
    emit('frame', { query, frame: { k: 'ready' } })
    await screen.findByText('WARN current')
    for (const frame of [{ k: 'history-start' }, { k: 'lines', s: 1, l: [['', 'INFO stale']] }, { k: 'history-end' }]) emit('frame', { query: stale, frame })
    const staleWindow = append(EMPTY_WINDOW, new Ingest().entries(1, [['', 'INFO stale snapshot']]))
    emit('snapshot', { query: stale, win: staleWindow, nextId: 2, carry: [], sources: [], status: { phase: 'live' } })
    emit('status', { query: stale, status: { phase: 'error', message: 'stale status' } })
    emit('reset', stale)
    expect(screen.getByText('WARN current')).toBeInTheDocument()
    expect(screen.queryByText('INFO stale')).not.toBeInTheDocument()
    expect(screen.getByLabelText('stream state')).toHaveTextContent('Live')
    expect(screen.getByLabelText('stream state')).not.toHaveTextContent('stale status')
    expect(c.openLogStream).not.toHaveBeenCalled()
  })

  it('uses the connection catalog display name and follows renames without exposing the target ID', async () => {
    const target = 'file:/home/user/.kube/cloud.yaml:admin@cluster'
    const { client: c, state } = fakeClient([k8s(target, { title: 'Cloud production' })])
    useStore.setState({ view: state.view })
    const body = stream()
    vi.stubGlobal('fetch', vi.fn(async () => body.response))
    const { container } = render(<LogViewer client={c} subject={{ ...pod, target }} active />)
    await screen.findByText('Cloud production')
    const strip = container.querySelector('[data-log-context]')!
    expect(strip).toHaveTextContent('Connection: Cloud production')
    expect(strip.innerHTML).not.toContain(target)
    act(() => useStore.setState({ view: { ...state.view, groups: state.view.groups.map(group => ({ ...group, targets: group.targets.map(t => ({ ...t, title: 'Renamed cloud' })) })) } }))
    expect(strip).toHaveTextContent('Connection: Renamed cloud')
  })

  it('streams lines from several sources with ANSI, search and text filters; gone asks to reopen', async () => {
    const { client: c } = fakeClient([k8s('dev')])
    c.logInfo = vi.fn(async () => ({ channels: [{ id: 'app', title: 'app' }], defaultChannel: 'app', aggregate: true, previous: false }))
    const s1 = stream()
    const s2 = stream()
    const s3 = stream()
    const fetchMock = vi.fn().mockResolvedValueOnce(s1.response).mockResolvedValueOnce(s2.response).mockResolvedValueOnce(s3.response)
    vi.stubGlobal('fetch', fetchMock)

    render(<LogViewer client={c} subject={pod} active />)
    await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/streams/tok/logs/s1', expect.anything()))
    expect(c.openLogStream).toHaveBeenCalledWith(pod, { channel: 'app', previous: false, follow: true, tailLines: 500, sinceTime: undefined })

    act(() =>
      s1.send(
        { k: 'source', id: 1, key: 'pa/app', label: 'web-5d8f-abcde/app', channel: 'app' },
        { k: 'source', id: 2, key: 'pb/app', label: 'web-5d8f-fghij/app', channel: 'app' },
        { k: 'lines', s: 1, l: [['2026-09-29T10:00:01.5Z', 'INFO \u001b[32mstarted\u001b[0m ok']] },
        { k: 'lines', s: 2, l: [['2026-09-29T10:00:02Z', 'ERROR boom <b>']] },
        { k: 'ready' },
      ),
    )
    await screen.findByText('started')
    expect(screen.getByText('started')).toHaveStyle({ color: 'var(--color-ansi-2)' })
    expect(screen.getByText(/boom <b>/)).toBeInTheDocument() // text, never markup
    expect(screen.getByText('abcde')).toBeInTheDocument() // short source labels, shown for 2 sources
    expect(screen.getByText('fghij')).toBeInTheDocument()
    expect(screen.getByLabelText('stream state')).toHaveTextContent('Live')

    const user = userEvent.setup()
    await user.type(screen.getByLabelText('Search (Ctrl+F)'), 'boom')
    await waitFor(() => expect(document.querySelector('mark.log-match-current')).toHaveTextContent('boom'))
    expect(screen.getByLabelText('matches')).toHaveTextContent('1/1')

    expect(screen.queryByRole('button', { name: /^(DEBUG|INFO|ERROR|WARN|TRACE|UNKNOWN)$/ })).not.toBeInTheDocument()
    await user.type(screen.getByLabelText('Filter (*)'), 'INFO')
    await waitFor(() => expect(c.openLogStream).toHaveBeenCalledTimes(2))
    expect(c.openLogStream).toHaveBeenLastCalledWith(pod, expect.objectContaining({ filter: 'INFO' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    act(() => s2.send(
      { k: 'source', id: 1, key: 'pa/app', label: 'web-5d8f-abcde/app', channel: 'app' },
      { k: 'source', id: 2, key: 'pb/app', label: 'web-5d8f-fghij/app', channel: 'app' },
      { k: 'lines', s: 1, l: [['2026-09-29T10:00:01.5Z', 'INFO started ok']] },
      { k: 'ready' },
    ))
    await waitFor(() => expect(screen.queryByText(/boom/)).not.toBeInTheDocument())
    expect(screen.getByLabelText('line count')).toHaveTextContent('1 lines')

    act(() => s2.send({ k: 'state', s: 2, state: 'waiting', msg: 'the container exited (code 1); waiting for it to restart' }))
    await screen.findByText(/waiting for it to restart/)

    act(() => {
      s2.send({ k: 'end', reason: 'gone' })
      s2.close()
    })
    await screen.findByText('The session was closed (context changed)', { exact: false })
    await user.click(screen.getByRole('button', { name: 'Reopen' }))
    await waitFor(() => expect(c.openLogStream).toHaveBeenCalledTimes(3))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3))
  })

  it('a stream ending with the provider\'s reason says it by key', async () => {
    const { client: c } = fakeClient([k8s('dev')])
    const s1 = stream()
    vi.stubGlobal('fetch', vi.fn(async () => s1.response))
    render(<LogViewer client={c} subject={pod} active />)
    act(() => {
      s1.send({ k: 'end', reason: 'error', class: 'gone', message: 'raw server words', why: { key: 'test.unknown', text: 'the provider says so' } })
      s1.close()
    })
    await screen.findByText(/the provider says so/)
    expect(screen.queryByText(/raw server words/)).not.toBeInTheDocument()
  })

  it('a stream its session ended says why (P18: closed by the user, a login needed)', async () => {
    const { client: c } = fakeClient([k8s('dev')])
    const s1 = stream()
    vi.stubGlobal('fetch', vi.fn(async () => s1.response))
    render(<LogViewer client={c} subject={pod} active />)
    act(() => {
      s1.send({ k: 'end', reason: 'gone', why: { key: 'api.closedByUser', text: 'the connection to the target was closed' } })
      s1.close()
    })
    await waitFor(() => expect(screen.getByLabelText('stream state')).toHaveTextContent('the connection to the target was closed'))
    expect(screen.getByLabelText('stream state')).toHaveTextContent('The session was closed')
    expect(screen.getByRole('button', { name: 'Reopen' })).toBeInTheDocument()
  })

  it('shows why a stream could not be opened', async () => {
    const { client: c } = fakeClient([k8s('dev')])
    const { ApiError } = await import('../api/client')
    c.openLogStream = vi.fn(async () => {
      throw new ApiError('limit', 'too many open streams (max 8)')
    })
    render(<LogViewer client={c} subject={pod} active />)
    await screen.findByText(/too many open streams/)
    expect(screen.getByRole('button', { name: 'Reopen' })).toBeInTheDocument()
  })

  it('ignores a replaced stream, keeps gap warnings, and aborts on a broken frame', async () => {
    const { client: c } = fakeClient([k8s('dev')])
    const s1 = stream()
    const s2 = stream()
    const signals: AbortSignal[] = []
    const fetchMock = vi.fn(async (_u: string, init: RequestInit) => {
      signals.push(init.signal!)
      return signals.length === 1 ? s1.response : s2.response
    })
    vi.stubGlobal('fetch', fetchMock)
    const user = userEvent.setup()
    render(<LogViewer client={c} subject={pod} active />)
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1))
    act(() =>
      s1.send(
        { k: 'source', id: 1, key: 'a', label: 'a/app' },
        { k: 'lines', s: 1, l: [['2026-09-29T10:00:01Z', 'first']] },
        { k: 'ready' },
        // a reconnect gap and "streaming" again in the same chunk
        { k: 'state', s: 1, state: 'gap', msg: 'lines around the reconnect may be missing' },
        { k: 'state', s: 1, state: 'streaming' },
      ),
    )
    await screen.findByText('first')
    expect(await screen.findByRole('status')).toHaveTextContent('possible gap — lines around the reconnect may be missing')

    // a new stream (another tail): the old one's late lines must not appear
    await user.click(screen.getByRole('button', { name: 'Lines' }))
    await user.click(within(screen.getByRole('listbox', { name: 'Lines' })).getByRole('option', { name: '100' }))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
    expect(signals[0].aborted).toBe(true)
    act(() => s1.send({ k: 'lines', s: 1, l: [['2026-09-29T10:00:02Z', 'late from the old stream']] }))
    act(() => s2.send({ k: 'source', id: 1, key: 'a', label: 'a/app' }, { k: 'lines', s: 1, l: [['2026-09-29T10:00:03Z', 'new stream']] }, { k: 'ready' }))
    await screen.findByText('new stream')
    expect(screen.queryByText('late from the old stream')).not.toBeInTheDocument()
    expect(screen.queryByText('first')).not.toBeInTheDocument()

    // a broken frame: an error, and the request is released at once
    act(() => s2.raw('{not json}\n'))
    await waitFor(() => expect(screen.getByLabelText('stream state')).toHaveTextContent('Failed'))
    expect(signals[1].aborted).toBe(true)
  })

  it('forgets finished sources whose lines were all evicted', async () => {
    const { client: c } = fakeClient([k8s('dev')])
    const s1 = stream()
    vi.stubGlobal('fetch', vi.fn(async () => s1.response))
    render(<LogViewer client={c} subject={pod} active />)
    await waitFor(() => expect(c.openLogStream).toHaveBeenCalled())
    act(() =>
      s1.send(
        { k: 'source', id: 1, key: 'old', label: 'old-pod/app' },
        { k: 'source', id: 2, key: 'new', label: 'new-pod/app' },
        { k: 'lines', s: 1, l: [['', 'from the old pod']] },
        { k: 'lines', s: 2, l: [['', 'from the new pod']] },
        { k: 'ready' },
        { k: 'state', s: 1, state: 'ended', msg: 'the pod was deleted' },
      ),
    )
    await screen.findByText('from the old pod')
    expect(screen.getByRole('button', { name: 'Source' })).toHaveAttribute('aria-pressed', 'true')
    const many = Array.from({ length: 50_000 }, (_, i) => ['', `line ${i}`])
    act(() => s1.send({ k: 'lines', s: 2, l: many.slice(0, 25_000) }, { k: 'lines', s: 2, l: many.slice(25_000) }))
    // the old pod's line is evicted; its source goes with it: one source
    // left, so the prefix column switches off by itself
    await waitFor(() => expect(screen.getByRole('button', { name: 'Source' })).toHaveAttribute('aria-pressed', 'false'), { timeout: 5000 })
    expect(screen.queryByText(/the pod was deleted/)).not.toBeInTheDocument()
  })
})


it('reopening an explicit diagnosis link restores its current/previous selection after a toolbar toggle',async()=>{
 const {client}=fakeClient([k8s('dev')])
 client.logInfo=vi.fn(async()=>({channels:[{id:'app',title:'app'}],defaultChannel:'app',aggregate:false,previous:true}))
 vi.stubGlobal('fetch',vi.fn(async()=>stream().response))
 const view=render(<LogViewer client={client} subject={pod} active initialChannel="app" initialPrevious focusEpoch={1}/>)
 await waitFor(()=>expect(client.openLogStream).toHaveBeenCalledWith(pod,expect.objectContaining({channel:'app',previous:true})))
 fireEvent.click(screen.getByRole('button',{name:'Previous'}))
 await waitFor(()=>expect(client.openLogStream).toHaveBeenLastCalledWith(pod,expect.objectContaining({previous:false})))
 view.rerender(<LogViewer client={client} subject={pod} active initialChannel="app" initialPrevious focusEpoch={2}/>)
 await waitFor(()=>expect(client.openLogStream).toHaveBeenLastCalledWith(pod,expect.objectContaining({channel:'app',previous:true})))
})
