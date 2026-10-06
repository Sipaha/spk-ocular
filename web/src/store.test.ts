import { beforeEach, describe, expect, it, vi } from 'vitest'
import { actions, initialState, reconfigured, useStore, visibleTargets } from './store'
import type { TargetsView } from './api/types'
import { holdEdits, keepEditing, discardEdits, resetGuard } from './edit/guard'
import { fakeClient, k8s } from './test/fakeClient'
import * as i18n from './i18n'

beforeEach(() => useStore.setState({ ...initialState }))

describe('store', () => {
  it('keeps a catalogue startup failure visible and retries without opening a view early', async () => {
    const f = fakeClient([k8s('a')])
    const loader = vi.spyOn(i18n, 'loadLanguage').mockRejectedValueOnce(new Error('catalogue unavailable'))
    try {
      const act = actions(f.client)
      await act.init()
      expect(useStore.getState().loadError).toBe('catalogue unavailable')
      expect(useStore.getState().view).toBeNull()
      expect(f.client.listTargets).not.toHaveBeenCalled()
      await act.init()
      expect(useStore.getState().view?.groups[0].targets).toHaveLength(1)
      expect(useStore.getState().loadError).toBeNull()
    } finally {
      loader.mockRestore()
    }
  })
  it('loads targets and remembers the selection', async () => {
    const f = fakeClient([k8s('a'), k8s('b')])
    const act = actions(f.client)
    await act.init()
    expect(useStore.getState().view?.groups[0].targets).toHaveLength(2)
    await act.select({ provider: 'kubernetes', id: 'b' })
    expect(useStore.getState().view?.selected).toEqual({ provider: 'kubernetes', id: 'b' })
    expect(useStore.getState().cursor).toBe('kubernetes/b')
  })

  it('serializes selection writes, the last click wins', async () => {
    const f = fakeClient([k8s('a'), k8s('b'), k8s('c')])
    const act = actions(f.client)
    await act.init()
    const order: string[] = []
    let releaseFirst!: () => void
    const firstDone = new Promise<void>((r) => (releaseFirst = r))
    const sel = f.client.selectTarget as ReturnType<typeof vi.fn>
    const real = sel.getMockImplementation() as (p: string, id: string) => Promise<void>
    sel.mockImplementation(async (p: string, id: string) => {
      order.push(`start ${id}`)
      if (id === 'a') await firstDone // the first write is slow
      await real(p, id)
      order.push(`end ${id}`)
    })
    const pa = act.select({ provider: 'kubernetes', id: 'a' })
    void act.select({ provider: 'kubernetes', id: 'b' })
    void act.select({ provider: 'kubernetes', id: 'c' })
    releaseFirst()
    await pa
    expect(order).toEqual(['start a', 'end a', 'start c', 'end c'])
    expect(useStore.getState().view?.selected).toEqual({ provider: 'kubernetes', id: 'c' })
  })

  it('reports a failed selection without losing the list', async () => {
    const f = fakeClient([k8s('a')])
    const act = actions(f.client)
    await act.init()
    await act.select({ provider: 'kubernetes', id: 'gone' })
    expect(useStore.getState().actionError).toContain('not_found')
    expect(useStore.getState().view?.groups[0].targets).toHaveLength(1)
  })

  it('coalesces overlapping reloads into one follow-up', async () => {
    const f = fakeClient([k8s('a')])
    const act = actions(f.client)
    await act.init()
    const calls = (f.client.listTargets as ReturnType<typeof vi.fn>).mock.calls.length
    const p = act.reload()
    void act.reload()
    void act.reload()
    await p
    expect((f.client.listTargets as ReturnType<typeof vi.fn>).mock.calls.length - calls).toBe(2)
  })

  it('filters by title and subtitle, moving the cursor into the result', async () => {
    const f = fakeClient([k8s('prod'), k8s('dev', { subtitle: 'kind-local' })])
    const act = actions(f.client)
    await act.init()
    act.setFilter('KIND')
    const s = useStore.getState()
    expect(visibleTargets(s.view, s.filter).map((x) => x.id)).toEqual(['dev'])
    expect(s.cursor).toBe('kubernetes/dev')
  })

  it('moves the cursor within bounds', async () => {
    const f = fakeClient([k8s('a'), k8s('b'), k8s('c')])
    const act = actions(f.client)
    await act.init()
    act.moveCursor(1)
    expect(useStore.getState().cursor).toBe('kubernetes/a')
    act.moveCursor(5)
    expect(useStore.getState().cursor).toBe('kubernetes/c')
    act.moveCursor(-1)
    expect(useStore.getState().cursor).toBe('kubernetes/b')
  })
})

describe('reconfigured', () => {
  const view: TargetsView = {
    selected: null,
    groups: [{ provider: 'k', title: 'K', targets: [{ provider: 'k', id: 'a', title: 'a', configRev: 'r2' }, { provider: 'k', id: 'b', title: 'b' }], problems: [] }],
  }
  it('is set when the target now has another revision', () => {
    expect(reconfigured(view, 'k', 'a', 'r1')).toBe(true)
    expect(reconfigured(view, 'k', 'a', 'r2')).toBe(false)
  })
  it('is not guessed without both revisions or the target', () => {
    expect(reconfigured(view, 'k', 'a', undefined)).toBe(false)
    expect(reconfigured(view, 'k', 'b', 'r1')).toBe(false)
    expect(reconfigured(view, 'k', 'gone', 'r1')).toBe(false)
    expect(reconfigured(null, 'k', 'a', 'r1')).toBe(false)
  })
})

it('disconnects the selected target only after resolving its unsaved edits', async()=>{
 const selected=k8s('a',{open:true}),other=k8s('b',{open:true})
 const f=fakeClient([selected,other]);f.state.view.selected=selected
 const ops=actions(f.client);await ops.init()
 const release=holdEdits({dirty:()=>true,discard:vi.fn()})
 try{
  await ops.closeTarget(other)
  expect(f.client.closeTarget).toHaveBeenCalledWith('kubernetes','b')
  const cancelled=ops.closeTarget(selected)
  expect(f.client.closeTarget).not.toHaveBeenCalledWith('kubernetes','a')
  keepEditing();await cancelled
  expect(f.client.closeTarget).not.toHaveBeenCalledWith('kubernetes','a')
  const confirmed=ops.closeTarget(selected)
  discardEdits();await confirmed
  expect(f.client.closeTarget).toHaveBeenCalledWith('kubernetes','a')
  expect(useStore.getState().view?.selected?.id).toBe('a')
 }finally{release();resetGuard()}
})
