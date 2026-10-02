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
    remember('kubernetes/prod', { ui: { kind: 'pods', scope: { mode: 'all' } }, navOpen: ['group:x'] })
    const json = serializeMemo(memoOf('kubernetes/prod'))!
    const back = parseMemo(json)!
    expect(back.page?.filter).toBe('api')
    expect(back.page?.tab).toBe('yaml')
    expect(back.sorts.pods).toEqual({ col: 'name', desc: true })
    expect(json).not.toContain('navOpen') // ui/navOpen persist by their own keys
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
