import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Client } from '../api/client'
import { forgetPages, memoOf, remember, rememberSort } from './pageMemo'
import { createMemoWriter, pageMemoKeyName, pageMemoMaxBytes, parseMemo, serializeMemo } from './pageMemoPersist'

beforeEach(() => forgetPages())
afterEach(() => vi.useRealTimers())

const writerClient = () => {
  const setTargetState = vi.fn(async () => {})
  return { client: { setTargetState } as unknown as Client, setTargetState }
}

describe('serializeMemo / parseMemo', () => {
  it('round-trips the persisted part of a memo (sorts and page)', () => {
    remember('kubernetes/prod', { page: { key: 'pods/{"mode":"all"}', filter: 'api', selected: 'uid-web-api-1', open: null, tab: 'yaml' } })
    rememberSort('kubernetes/prod', 'pods', { col: 'name', desc: true })
    remember('kubernetes/prod', { ui: { kind: 'pods', scope: { mode: 'all' } } })
    const json = serializeMemo(memoOf('kubernetes/prod'))!
    const back = parseMemo(json)!
    expect(back.page?.filter).toBe('api')
    expect(back.page?.tab).toBe('yaml')
    expect(back.sorts.pods).toEqual({ col: 'name', desc: true })
    expect(JSON.parse(json)).not.toHaveProperty('ui') // ui persists by its own keys
  })

  it('is null when the memo does not fit the server cap', () => {
    remember('kubernetes/prod', { page: { key: 'k', filter: 'x'.repeat(pageMemoMaxBytes), selected: null, open: null } })
    expect(serializeMemo(memoOf('kubernetes/prod'))).toBeNull()
  })

  it('parses nothing from an absent, malformed or future-version value', () => {
    expect(parseMemo(undefined)).toBeNull()
    expect(parseMemo('not json')).toBeNull()
    expect(parseMemo('{"v":2,"sorts":{}}')).toBeNull()
    expect(parseMemo('{"v":1}')).toBeNull()
    expect(parseMemo('{"v":1,"sorts":{}}')).toEqual({ sorts: {}, page: undefined })
  })

  it('drops a malformed page but keeps the sorts', () => {
    const back = parseMemo('{"v":1,"sorts":{"pods":{"col":"name","desc":false}},"page":{"key":1,"filter":2}}')!
    expect(back.page).toBeUndefined()
    expect(back.sorts.pods).toEqual({ col: 'name', desc: false })
  })
})

describe('createMemoWriter', () => {
  it('writes the memo debounced: one write per burst of changes', () => {
    vi.useFakeTimers()
    const { client, setTargetState } = writerClient()
    const w = createMemoWriter(client, 'kubernetes', 'prod')
    remember('kubernetes/prod', { page: { key: 'k', filter: 'a', selected: null, open: null } })
    w.note(memoOf('kubernetes/prod'))
    w.note(memoOf('kubernetes/prod')) // same burst
    expect(setTargetState).not.toHaveBeenCalled()
    vi.advanceTimersByTime(1000)
    expect(setTargetState).toHaveBeenCalledTimes(1)
    expect(setTargetState).toHaveBeenCalledWith('kubernetes', 'prod', pageMemoKeyName, expect.stringContaining('"filter":"a"'))
  })

  it('does not write content it has already written', () => {
    vi.useFakeTimers()
    const { client, setTargetState } = writerClient()
    const w = createMemoWriter(client, 'kubernetes', 'prod')
    remember('kubernetes/prod', { page: { key: 'k', filter: 'a', selected: null, open: null } })
    w.note(memoOf('kubernetes/prod'))
    vi.advanceTimersByTime(1000)
    w.note(memoOf('kubernetes/prod'))
    vi.advanceTimersByTime(5000)
    expect(setTargetState).toHaveBeenCalledTimes(1)
  })

  it('skips a memo over the server cap', () => {
    vi.useFakeTimers()
    const { client, setTargetState } = writerClient()
    const w = createMemoWriter(client, 'kubernetes', 'prod')
    remember('kubernetes/prod', { page: { key: 'k', filter: 'x'.repeat(pageMemoMaxBytes), selected: null, open: null } })
    w.note(memoOf('kubernetes/prod'))
    vi.advanceTimersByTime(5000)
    expect(setTargetState).not.toHaveBeenCalled()
  })

  it('cancels an intermediate snapshot when the user restores the saved filter', async () => {
    vi.useFakeTimers()
    const { client, setTargetState } = writerClient()
    const w = createMemoWriter(client, 'kubernetes', 'prod')
    const note = (filter: string) => w.note({ sorts: {}, page: { key: 'k', filter, selected: null, open: null } })
    note('saved')
    await vi.advanceTimersByTimeAsync(1000)
    note('intermediate')
    note('saved')
    await vi.advanceTimersByTimeAsync(1000)
    expect(setTargetState).toHaveBeenCalledTimes(1)
  })

  it('drops a pending snapshot when the current one exceeds the cap', () => {
    vi.useFakeTimers()
    const { client, setTargetState } = writerClient()
    const w = createMemoWriter(client, 'kubernetes', 'prod')
    w.note({ sorts: {}, page: { key: 'k', filter: 'old', selected: null, open: null } })
    w.note({ sorts: {}, page: { key: 'k', filter: 'я'.repeat(pageMemoMaxBytes), selected: null, open: null } })
    w.flush()
    expect(setTargetState).not.toHaveBeenCalled()
  })

  it('waits a full debounce interval after the latest change', async () => {
    vi.useFakeTimers()
    const { client, setTargetState } = writerClient()
    const w = createMemoWriter(client, 'kubernetes', 'prod')
    w.note({ sorts: {} })
    await vi.advanceTimersByTimeAsync(900)
    w.note({ sorts: { pods: { col: 'name', desc: true } } })
    await vi.advanceTimersByTimeAsync(100)
    expect(setTargetState).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(900)
    expect(setTargetState).toHaveBeenCalledTimes(1)
  })

  it('serializes slow writes and keeps only the latest pending snapshot', async () => {
    vi.useFakeTimers()
    const { client, setTargetState } = writerClient()
    let finish!: () => void
    setTargetState.mockImplementationOnce(() => new Promise<void>((resolve) => { finish = resolve }))
    const w = createMemoWriter(client, 'kubernetes', 'prod')
    const note = (filter: string) => w.note({ sorts: {}, page: { key: 'k', filter, selected: null, open: null } })
    note('first')
    await vi.advanceTimersByTimeAsync(1000)
    note('second')
    await vi.advanceTimersByTimeAsync(1000)
    note('latest')
    w.flush()
    expect(setTargetState).toHaveBeenCalledTimes(1)
    finish()
    await vi.advanceTimersByTimeAsync(0)
    expect(setTargetState).toHaveBeenCalledTimes(2)
    expect(setTargetState).toHaveBeenLastCalledWith('kubernetes', 'prod', pageMemoKeyName, expect.stringContaining('"filter":"latest"'))
  })

  it('keeps writes ordered across leaving and reopening a target', async () => {
    vi.useFakeTimers()
    const { client, setTargetState } = writerClient()
    let finish!: () => void
    setTargetState.mockImplementationOnce(() => new Promise<void>((resolve) => { finish = resolve }))
    const before = createMemoWriter(client, 'kubernetes', 'prod')
    before.note({ sorts: {} })
    before.flush() // unmount
    const after = createMemoWriter(client, 'kubernetes', 'prod')
    after.note({ sorts: { pods: { col: 'name', desc: true } } })
    after.flush()
    expect(setTargetState).toHaveBeenCalledTimes(1)
    finish()
    await vi.advanceTimersByTimeAsync(0)
    expect(setTargetState).toHaveBeenCalledTimes(2)
    expect(setTargetState).toHaveBeenLastCalledWith('kubernetes', 'prod', pageMemoKeyName, expect.stringContaining('"desc":true'))
  })

  it('allows another change notification to retry a failed write', async () => {
    vi.useFakeTimers()
    const { client, setTargetState } = writerClient()
    setTargetState.mockRejectedValueOnce(new Error('offline'))
    const w = createMemoWriter(client, 'kubernetes', 'prod')
    w.note({ sorts: {} })
    await vi.advanceTimersByTimeAsync(1000)
    w.note({ sorts: {} })
    await vi.advanceTimersByTimeAsync(1000)
    expect(setTargetState).toHaveBeenCalledTimes(2)
  })

  it('flush writes the pending memo now and leaves nothing behind', () => {
    vi.useFakeTimers()
    const { client, setTargetState } = writerClient()
    const w = createMemoWriter(client, 'kubernetes', 'prod')
    rememberSort('kubernetes/prod', 'pods', { col: 'name', desc: true })
    w.note(memoOf('kubernetes/prod'))
    w.flush()
    expect(setTargetState).toHaveBeenCalledTimes(1)
    expect(setTargetState).toHaveBeenCalledWith('kubernetes', 'prod', pageMemoKeyName, expect.stringContaining('"sorts"'))
    vi.advanceTimersByTime(5000)
    expect(setTargetState).toHaveBeenCalledTimes(1)
  })
})
