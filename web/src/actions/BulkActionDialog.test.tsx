import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../api/client'
import type { ActionDescriptor, ActionParams, ActionPlan, ActionResult, Ref } from '../api/types'
import { initialState, useStore } from '../store'
import { fakeClient, k8s } from '../test/fakeClient'
import { BulkActionDialog, type BulkItem } from './BulkActionDialog'

beforeEach(() => useStore.setState({ ...initialState }))

const del: ActionDescriptor = { id: 'delete', title: 'Delete', destructive: true }
const restart: ActionDescriptor = { id: 'restart', title: 'Restart' }
const scale: ActionDescriptor = { id: 'scale', title: 'Scale', param: { kind: 'count', min: 0, max: 10 } }

const refOf = (name: string, scope = 'web'): Ref => ({ provider: 'kubernetes', target: 'prod', scope, kind: 'pods', name })
const itemsOf = (...names: string[]): BulkItem[] => names.map((n) => ({ id: `id-${n}`, ref: refOf(n) }))

function planOf(action: ActionDescriptor, ref: Ref, params: ActionParams = {}, extra: Partial<ActionPlan> = {}): ActionPlan {
  return {
    where: { provider: 'kubernetes', target: 'prod', targetTitle: 'prod-ctx', configRev: 'rev-7', ref: { ...ref, uid: `uid-${ref.name}` } },
    action,
    params,
    destructive: action.destructive,
    effects: [{ text: `${action.id} ${ref.name}` }],
    rights: { state: 'allowed' },
    expect: `exp-${ref.name}-${params.count ?? ''}`,
    ...extra,
  }
}

function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

const done = (name: string): ActionResult => ({ message: { text: `pod ${name} deleted` }, outcome: 'done' })

function setup(
  action: ActionDescriptor,
  items: BulkItem[],
  plan: (r: Ref, p: ActionParams) => ActionPlan | Promise<ActionPlan> = (r, p) => planOf(action, r, p),
  run: (p: ActionPlan) => ActionResult | Promise<ActionResult> = (p) => done(p.where.ref.name),
  timeout?: number,
) {
  const f = fakeClient([k8s('prod')])
  f.client.prepareAction = vi.fn(async (r: Ref, _a: string, p: ActionParams) => plan(r, p))
  f.client.runAction = vi.fn(async (p: ActionPlan) => run(p))
  const onClose = vi.fn()
  const onDone = vi.fn()
  render(<BulkActionDialog client={f.client} req={{ items, action, kindTitleOf: () => 'pod' }} onClose={onClose} onDone={onDone} runTimeoutMs={timeout} />)
  return { f, onClose, onDone, dialog: screen.getByRole('dialog') }
}

const rowOf = (dialog: HTMLElement, name: string) => within(within(dialog).getByRole('list', { name: 'Objects' })).getByText(name).closest('li') as HTMLElement

describe('BulkActionDialog', () => {
  it('reviews each object on its own, at most 6 at once, and runs only those that can run', async () => {
    const names = Array.from({ length: 9 }, (_, i) => `p${i}`)
    const waits = new Map(names.map((n) => [n, deferred<ActionPlan>()]))
    const { f, dialog, onDone } = setup(del, itemsOf(...names), (r) => waits.get(r.name)!.promise)
    await waitFor(() => expect(f.client.prepareAction).toHaveBeenCalledTimes(6))
    expect(dialog).toHaveTextContent('Checked 0 of 9')
    const plans: Record<string, Partial<ActionPlan>> = {
      p1: { unavailable: { text: 'pod p1 is being deleted' } },
      p2: { rights: { state: 'denied', reason: 'delete pods in web' } },
      p4: { warnings: [{ key: 'k.w', text: 'managed by StatefulSet db' }] },
      p5: { rights: { state: 'unknown', reason: 'the check took too long' } },
    }
    for (const n of names) {
      await act(async () => {
        if (n === 'p3') waits.get(n)!.reject(new ApiError('forbidden', 'no get pods'))
        else waits.get(n)!.resolve(planOf(del, refOf(n), {}, plans[n]))
      })
    }
    expect(f.client.prepareAction).toHaveBeenCalledTimes(9)
    expect(rowOf(dialog, 'p1')).toHaveTextContent('unavailable: pod p1 is being deleted')
    expect(rowOf(dialog, 'p2')).toHaveTextContent('no permission: delete pods in web')
    expect(rowOf(dialog, 'p3')).toHaveTextContent('Could not read the current state · access denied: no get pods')
    expect(rowOf(dialog, 'p4')).toHaveTextContent('ready · warnings: 1')
    expect(rowOf(dialog, 'p5')).toHaveTextContent('ready') // unknown rights: it may still run
    expect(dialog).toHaveTextContent('Will run: 6; skipped: 3')
    const confirm = within(dialog).getByRole('button', { name: 'Delete 6' })
    expect(confirm).toHaveClass('bg-danger')
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Cancel' })).toHaveFocus()) // destructive: Cancel first
    await userEvent.click(confirm)
    await within(dialog).findByText('6 of 6 done')
    const sent = vi.mocked(f.client.runAction).mock.calls.map((c) => c[0].expect).sort()
    expect(sent).toEqual(['exp-p0-', 'exp-p4-', 'exp-p5-', 'exp-p6-', 'exp-p7-', 'exp-p8-'])
    expect(rowOf(dialog, 'p0')).toHaveTextContent('pod p0 deleted')
    expect(rowOf(dialog, 'p1')).toHaveTextContent('unavailable') // never run
    expect(onDone).toHaveBeenCalledWith(['id-p0', 'id-p4', 'id-p5', 'id-p6', 'id-p7', 'id-p8'])
    expect(useStore.getState().notice).toContain('Delete: 6 of 6 done')
    expect(within(dialog).queryByRole('button', { name: /^Delete/ })).toBeNull() // one run per review
  })

  it('an object’s details open on request: its effects, warnings, rights', async () => {
    const { dialog } = setup(restart, itemsOf('a', 'b'), (r) => planOf(restart, r, {}, { warnings: [{ text: `slow ${r.name}` }] }))
    await within(dialog).findByRole('button', { name: 'Restart 2' })
    const a = within(rowOf(dialog, 'a')).getByRole('button', { name: /a/ })
    expect(a).toHaveAttribute('aria-expanded', 'false')
    expect(dialog).not.toHaveTextContent('restart a')
    await userEvent.click(a)
    expect(a).toHaveAttribute('aria-expanded', 'true')
    expect(rowOf(dialog, 'a')).toHaveTextContent('restart a')
    expect(rowOf(dialog, 'a')).toHaveTextContent('Permission: checked: allowed')
  })

  it('a warning several objects share is said once with how many and whose', async () => {
    const same = (r: Ref) => planOf(del, r, {}, { warnings: [{ key: 'kubernetes.forceDelete.nodeNotReady', params: { node: r.name }, text: `node of ${r.name} is not ready` }] })
    const { dialog } = setup(del, itemsOf('a', 'b', 'c', 'd'), same)
    const summary = await within(dialog).findByRole('region', { name: 'Warnings' })
    expect(within(summary).getAllByRole('listitem')).toHaveLength(1)
    expect(summary).toHaveTextContent('node of a is not ready — 4 objects: a, b, c…')
  })

  it('one object’s conflict, unknown outcome or partial run leaves the others alone; at most 4 run at once', async () => {
    const waits = new Map(['a', 'b', 'c', 'd', 'e'].map((n) => [n, deferred<ActionResult>()]))
    const { f, dialog, onDone } = setup(restart, itemsOf('a', 'b', 'c', 'd', 'e'), undefined, (p) => waits.get(p.where.ref.name)!.promise)
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Restart 5' }))
    await waitFor(() => expect(f.client.runAction).toHaveBeenCalledTimes(4))
    await act(async () => waits.get('a')!.reject(new ApiError('conflict', 'deployment a changed since the review', false, { text: 'deployment a changed since the review' })))
    await waitFor(() => expect(f.client.runAction).toHaveBeenCalledTimes(5))
    await act(async () => waits.get('b')!.reject(new ApiError('unknown', 'connection reset')))
    await act(async () =>
      waits.get('c')!.resolve({ message: { text: 'restarted' }, outcome: 'refused', parts: [{ id: 'c1', title: 'c1', outcome: 'refused', why: { text: 'no' } }] }),
    )
    await act(async () => waits.get('d')!.resolve(done('d')))
    await act(async () => waits.get('e')!.resolve(done('e')))
    await within(dialog).findByText(/2 of 5 done/)
    expect(dialog).toHaveTextContent('2 of 5 done; conflicts: 1; outcome unknown: 1; partly: 1')
    expect(rowOf(dialog, 'a')).toHaveTextContent('changed since the review')
    expect(rowOf(dialog, 'b')).toHaveTextContent('not known')
    expect(rowOf(dialog, 'c')).toHaveTextContent('0 of 1 done; 1 refused')
    expect(onDone).toHaveBeenCalledWith(['id-d', 'id-e'])
  })

  it('no answer in time: that object’s outcome is unknown', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })
    try {
      const { dialog } = setup(restart, itemsOf('a', 'b'), undefined, (p) => (p.where.ref.name === 'a' ? new Promise<ActionResult>(() => {}) : done('b')), 1000)
      const user = userEvent.setup({ advanceTimers: vi.advanceTimersByTime })
      await user.click(await within(dialog).findByRole('button', { name: 'Restart 2' }))
      await act(async () => vi.advanceTimersByTime(1500))
      await within(dialog).findByText(/1 of 2 done/)
      expect(rowOf(dialog, 'a')).toHaveTextContent('No answer within 1 s')
    } finally {
      vi.useRealTimers()
    }
  })

  it('Stop starts no more: the ones not started are not run; the dialog stays while any runs', async () => {
    const waits = new Map(['a', 'b', 'c', 'd', 'e', 'f'].map((n) => [n, deferred<ActionResult>()]))
    const { f, dialog, onClose } = setup(restart, itemsOf('a', 'b', 'c', 'd', 'e', 'f'), undefined, (p) => waits.get(p.where.ref.name)!.promise)
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Restart 6' }))
    await waitFor(() => expect(f.client.runAction).toHaveBeenCalledTimes(4))
    await userEvent.keyboard('{Escape}')
    expect(onClose).not.toHaveBeenCalled()
    expect(within(dialog).getByRole('button', { name: 'Cancel' })).toBeDisabled()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Stop' }))
    for (const n of ['a', 'b', 'c', 'd']) await act(async () => waits.get(n)!.resolve(done(n)))
    await within(dialog).findByText(/4 of 6 done/)
    expect(f.client.runAction).toHaveBeenCalledTimes(4)
    expect(rowOf(dialog, 'e')).toHaveTextContent('not run')
    expect(dialog).toHaveTextContent('not run: 2')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Close' }))
    expect(onClose).toHaveBeenCalled()
  })

  it('a shared count is reviewed first; a changed count reviews every object again before anything runs', async () => {
    const late = new Map<string, ReturnType<typeof deferred<ActionPlan>>>()
    const { f, dialog } = setup(scale, itemsOf('a', 'b'), (r, p) => {
      if (p.count === 5 && r.name === 'b') {
        const d = deferred<ActionPlan>()
        late.set('b', d)
        return d.promise
      }
      return planOf(scale, r, p, { current: 2 })
    })
    expect(f.client.prepareAction).not.toHaveBeenCalled()
    const count = within(dialog).getByRole('textbox', { name: 'Replicas' })
    await waitFor(() => expect(count).toHaveFocus())
    await userEvent.type(count, '3{Enter}')
    await within(dialog).findByRole('button', { name: 'Scale 2' })
    expect(f.client.prepareAction).toHaveBeenCalledWith(refOf('a'), 'scale', { count: 3 })
    expect(rowOf(dialog, 'a')).toHaveTextContent('now 2')
    await userEvent.clear(count)
    await userEvent.type(count, '5')
    expect(within(dialog).queryByRole('button', { name: 'Scale 2' })).toBeNull() // not reviewed for 5
    await userEvent.keyboard('{Enter}')
    await waitFor(() => expect(f.client.prepareAction).toHaveBeenCalledTimes(4))
    expect(within(dialog).queryByRole('button', { name: /^Scale \d/ })).toBeNull() // b's plan for 5 is still out
    await act(async () => late.get('b')!.resolve(planOf(scale, refOf('b'), { count: 5 })))
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Scale 2' }))
    expect(vi.mocked(f.client.runAction).mock.calls.map((c) => c[0].params)).toEqual([{ count: 5 }, { count: 5 }])
  })

  it('a count out of range is not reviewed', async () => {
    const { f, dialog } = setup(scale, itemsOf('a'))
    await userEvent.type(within(dialog).getByRole('textbox', { name: 'Replicas' }), '11{Enter}')
    expect(dialog).toHaveTextContent('0–10')
    expect(f.client.prepareAction).not.toHaveBeenCalled()
  })

  it('Review again reads every plan anew', async () => {
    let n = 0
    const { f, dialog } = setup(restart, itemsOf('a', 'b'), (r) => planOf(restart, r, {}, n++ < 2 ? { unavailable: { text: 'busy' } } : {}))
    await within(dialog).findByText('Will run: 0; skipped: 2')
    expect(within(dialog).getByRole('button', { name: 'Restart 0' })).toBeDisabled()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Review again' }))
    await within(dialog).findByRole('button', { name: 'Restart 2' })
    expect(f.client.prepareAction).toHaveBeenCalledTimes(4)
  })
})
