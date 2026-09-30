import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { Ref } from '../api/types'
import { fakeClient, k8s } from '../test/fakeClient'
import LogViewer from './LogViewer'

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

afterEach(() => vi.unstubAllGlobals())

describe('LogViewer', () => {
  it('streams lines from several sources with ANSI, search and levels; gone asks to reopen', async () => {
    const { client: c } = fakeClient([k8s('dev')])
    c.logInfo = vi.fn(async () => ({ channels: [{ id: 'app', title: 'app' }], defaultChannel: 'app', aggregate: true, previous: false }))
    const s1 = stream()
    const s2 = stream()
    const fetchMock = vi.fn().mockResolvedValueOnce(s1.response).mockResolvedValueOnce(s2.response)
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

    await user.click(screen.getByRole('button', { name: 'ERROR' }))
    await waitFor(() => expect(screen.queryByText(/boom/)).not.toBeInTheDocument())
    expect(screen.getByLabelText('line count')).toHaveTextContent('1 of 2 lines')

    act(() => s1.send({ k: 'state', s: 2, state: 'waiting', msg: 'the container exited (code 1); waiting for it to restart' }))
    await screen.findByText(/waiting for it to restart/)

    act(() => {
      s1.send({ k: 'end', reason: 'gone' })
      s1.close()
    })
    await screen.findByText('The session was closed (context changed)', { exact: false })
    await user.click(screen.getByRole('button', { name: 'Reopen' }))
    await waitFor(() => expect(c.openLogStream).toHaveBeenCalledTimes(2))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))
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
