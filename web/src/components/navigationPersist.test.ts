import { expect, it, vi } from 'vitest'
import { fakeClient } from '../test/fakeClient'
import { persistNavigation } from './navigationPersist'

it('serializes rapid selections across workspace remounts, waits for both writes after a failure, and leaves other targets independent', async () => {
  const { client } = fakeClient([])
  let releaseKind!: () => void
  let rejectScope!: (error: Error) => void
  const kind = new Promise<void>((resolve) => { releaseKind = resolve })
  const scope = new Promise<void>((_, reject) => { rejectScope = reject })
  const stored = new Map<string, string>()
  client.setTargetState = vi.fn(async (_p, target, key, value) => {
    if (target === 'a' && key === 'kind' && value === '"pods"') await kind
    if (target === 'a' && key === 'scope' && value.includes('"one"')) await scope
    stored.set(`${target}/${key}`, value)
  })
  const first = persistNavigation(client, 'kubernetes', 'a', { kind: 'pods', scope: { mode: 'one', name: 'blue' } }).catch(() => {})
  // A new Workspace has the same client and target, no shared React state.
  const latest = persistNavigation(client, 'kubernetes', 'a', { kind: 'services', scope: { mode: 'some', names: ['blue', 'green'] } })
  const other = persistNavigation(client, 'kubernetes', 'b', { kind: 'pods', scope: { mode: 'all' } })
  await other
  expect(stored.get('b/scope')).toBe('{"mode":"all"}')
  rejectScope(new Error('write failed'))
  await Promise.resolve()
  expect(client.setTargetState).toHaveBeenCalledTimes(4)
  releaseKind()
  await Promise.all([first, latest])
  expect(stored.get('a/kind')).toBe('"services"')
  expect(JSON.parse(stored.get('a/scope')!)).toEqual({ mode: 'some', names: ['blue', 'green'] })
})
