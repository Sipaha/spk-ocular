import { beforeEach, describe, expect, it, vi } from 'vitest'
import { forgetPages, memoOf, onMemoChange, remember, rememberSort, seedPersisted } from './pageMemo'

beforeEach(() => forgetPages())

describe('pageMemo change subscription (P19)', () => {
  it('notifies when the page changes, not for ui', () => {
    const cb = vi.fn()
    onMemoChange('kubernetes/prod', cb)
    remember('kubernetes/prod', { ui: { kind: 'pods', scope: { mode: 'all' } } })
    expect(cb).not.toHaveBeenCalled()
    remember('kubernetes/prod', { page: { key: 'pods/{"mode":"all"}', filter: 'api', selected: null, open: null } })
    expect(cb).toHaveBeenCalledTimes(1)
  })

  it('notifies on a sort change and stops after unsubscribe', () => {
    const cb = vi.fn()
    const off = onMemoChange('kubernetes/prod', cb)
    rememberSort('kubernetes/prod', 'pods', { col: 'name', desc: false })
    expect(cb).toHaveBeenCalledTimes(1)
    off()
    rememberSort('kubernetes/prod', 'pods', { col: 'name', desc: true })
    expect(cb).toHaveBeenCalledTimes(1)
  })

  it('other targets are not notified; forgetPages clears the listeners', () => {
    const a = vi.fn()
    const b = vi.fn()
    onMemoChange('kubernetes/prod', a)
    onMemoChange('kubernetes/stage', b)
    remember('kubernetes/stage', { page: { key: 'x', filter: '', selected: null, open: null } })
    expect(a).not.toHaveBeenCalled()
    expect(b).toHaveBeenCalledTimes(1)
    forgetPages()
    remember('kubernetes/stage', { page: { key: 'y', filter: '', selected: null, open: null } })
    expect(b).toHaveBeenCalledTimes(1)
  })

  it('seedPersisted merges without notifying', () => {
    const cb = vi.fn()
    onMemoChange('kubernetes/prod', cb)
    seedPersisted('kubernetes/prod', {
      sorts: { pods: { col: 'name', desc: true } },
      page: { key: 'k', filter: 'f', selected: null, open: null },
    })
    expect(cb).not.toHaveBeenCalled()
    expect(memoOf('kubernetes/prod').sorts.pods).toEqual({ col: 'name', desc: true })
    expect(memoOf('kubernetes/prod').page?.filter).toBe('f')
  })
})
