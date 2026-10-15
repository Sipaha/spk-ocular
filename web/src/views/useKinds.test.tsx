import { act, renderHook, waitFor } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { fakeClient } from '../test/fakeClient'
import { useKinds } from './useKinds'
import type { ViewHub } from './viewSync'

it('a refresh completed after switching targets cannot start an old-target catalog read', async () => {
  const { client } = fakeClient([])
  let finish = () => {}
  client.refreshKinds = vi.fn(() => new Promise<void>(resolve => { finish = resolve }))
  const hub = { subscribeKinds: () => () => {} } as unknown as ViewHub
  const view = renderHook(({ target }) => useKinds(client, hub, 'kubernetes', target), { initialProps: { target: 'old' } })
  await waitFor(() => expect(client.listKinds).toHaveBeenCalledTimes(1))
  act(() => view.result.current.refresh())
  view.rerender({ target: 'new' })
  await waitFor(() => expect(client.listKinds).toHaveBeenCalledTimes(2))
  await act(async () => finish())
  expect(client.listKinds).toHaveBeenCalledTimes(2)
  expect(client.listKinds).toHaveBeenLastCalledWith('kubernetes', 'new')
  expect(view.result.current.refreshing).toBe(false)
})

it('an old-session refresh error cannot overwrite a newly discovered session', async () => {
  const { client } = fakeClient([])
  let session = 1
  client.listKinds = vi.fn(async () => ({ kinds: [], session, rev: 1, state: 'ready' as const }))
  let fail: (error: Error) => void = () => {}
  client.refreshKinds = vi.fn(() => new Promise<void>((_, reject) => { fail = reject }))
  let announce: () => void = () => {}
  const hub = { subscribeKinds: (callback: () => void) => { announce = callback; return () => {} } } as unknown as ViewHub
  const view = renderHook(() => useKinds(client, hub, 'kubernetes', 'same-target'))
  await waitFor(() => expect(view.result.current.view?.session).toBe(1))
  act(() => view.result.current.refresh())
  session = 2
  act(() => announce())
  await waitFor(() => expect(view.result.current.view?.session).toBe(2))
  await act(async () => fail(new Error('obsolete session failed')))
  expect(view.result.current.error).toBeNull()
  expect(view.result.current.view?.session).toBe(2)
  expect(client.listKinds).toHaveBeenCalledTimes(2)
  expect(view.result.current.refreshing).toBe(false)
})
