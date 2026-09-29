import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../api/client'
import type { ActionDescriptor, ActionParams, ActionPlan, Ref } from '../api/types'
import { initialState, useStore } from '../store'
import { fakeClient, k8s } from '../test/fakeClient'
import { ActionDialog } from './ActionDialog'

beforeEach(() => useStore.setState({ ...initialState }))

const ref: Ref = { provider: 'kubernetes', target: 'prod', scope: 'web', kind: 'apps/deployments', name: 'api', uid: 'u1' }
const restart: ActionDescriptor = { id: 'restart', title: 'Restart' }
const scale: ActionDescriptor = { id: 'scale', title: 'Scale', param: { kind: 'count', min: 0, max: 10 } }
const del: ActionDescriptor = { id: 'delete', title: 'Delete', destructive: true }

function planOf(action: ActionDescriptor, params: ActionParams = {}, extra: Partial<ActionPlan> = {}): ActionPlan {
  return {
    where: { provider: 'kubernetes', target: 'prod', targetTitle: 'prod-ctx', endpoint: 'https://prod.example:6443', configRev: 'rev-7', ref },
    action,
    params,
    destructive: action.destructive,
    effects: [`${action.id} effect`],
    rights: { state: 'allowed' },
    expect: `exp-${action.id}-${params.count ?? ''}`,
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

function setup(action: ActionDescriptor, plan: (p: ActionParams) => ActionPlan | Promise<ActionPlan> = (p) => planOf(action, p), timeout?: number) {
  const f = fakeClient([k8s('prod')])
  f.client.prepareAction = vi.fn(async (_r: Ref, _a: string, p: ActionParams) => plan(p))
  const onClose = vi.fn()
  render(<ActionDialog client={f.client} req={{ ref, action, kindTitle: 'Deployments' }} onClose={onClose} runTimeoutMs={timeout} />)
  return { f, onClose, dialog: screen.getByRole('dialog') }
}

describe('ActionDialog', () => {
  it('shows where, what and the rights; the run carries the plan back', async () => {
    const { f, onClose, dialog } = setup(restart)
    const confirm = await within(dialog).findByRole('button', { name: 'Restart' })
    expect(dialog).toHaveTextContent('prod-ctx')
    expect(dialog).toHaveTextContent('https://prod.example:6443')
    expect(dialog).toHaveTextContent('web')
    expect(dialog).toHaveTextContent('Deployments')
    expect(dialog).toHaveTextContent('restart effect')
    expect(dialog).toHaveTextContent('Permission: checked: allowed')
    await waitFor(() => expect(confirm).toHaveFocus())
    await userEvent.click(confirm)
    expect(f.client.runAction).toHaveBeenCalledTimes(1)
    const sent = vi.mocked(f.client.runAction).mock.calls[0][0]
    expect(sent.expect).toBe('exp-restart-')
    expect(sent.where.configRev).toBe('rev-7')
    expect(sent.where.ref.uid).toBe('u1')
    await waitFor(() => expect(onClose).toHaveBeenCalled())
    expect(useStore.getState().notice).toBe('requested')
  })

  it('a destructive plan: red, Cancel has the focus, Enter cancels', async () => {
    const { f, onClose, dialog } = setup(del)
    const confirm = await within(dialog).findByRole('button', { name: 'Delete' })
    expect(confirm).toHaveClass('bg-danger')
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Cancel' })).toHaveFocus())
    await userEvent.keyboard('{Enter}')
    expect(onClose).toHaveBeenCalled()
    expect(f.client.runAction).not.toHaveBeenCalled()
  })

  it('a held Enter never confirms', async () => {
    const { f, dialog } = setup(restart)
    const confirm = await within(dialog).findByRole('button', { name: 'Restart' })
    await waitFor(() => expect(confirm).toHaveFocus())
    // Only repeats: the first press happened where the dialog was opened.
    act(() => {
      confirm.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', repeat: true, bubbles: true, cancelable: true }))
    })
    expect(f.client.runAction).not.toHaveBeenCalled()
  })

  it('scale: the count is chosen, Enter reviews it, only the reviewed count runs', async () => {
    const { f, dialog } = setup(scale, (p) => planOf(scale, p, { current: 2, destructive: p.count === 0 }))
    const input = await within(dialog).findByRole('textbox')
    await waitFor(() => expect(input).toHaveValue('2'))
    expect(input).toHaveFocus()
    expect(dialog).toHaveTextContent('now 2')
    expect(within(dialog).queryByRole('button', { name: 'Scale' })).not.toBeInTheDocument()
    await userEvent.clear(input)
    await userEvent.type(input, '5{Enter}')
    expect(f.client.prepareAction).toHaveBeenLastCalledWith(ref, 'scale', { count: 5 })
    expect(f.client.runAction).not.toHaveBeenCalled()
    const confirm = await within(dialog).findByRole('button', { name: 'Scale' })
    expect(dialog).toHaveTextContent('scale effect')
    // Another count: review first.
    await userEvent.clear(input)
    await userEvent.type(input, '6')
    expect(within(dialog).queryByRole('button', { name: 'Scale' })).not.toBeInTheDocument()
    expect(dialog).not.toHaveTextContent('scale effect')
    await userEvent.clear(input)
    await userEvent.type(input, '5')
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Scale' }))
    expect(vi.mocked(f.client.runAction).mock.calls[0][0].params).toEqual({ count: 5 })
    expect(confirm).not.toBeInTheDocument() // one run: it is gone once sent
  })

  it('scale to 0: the reviewed destructive plan moves the focus from the count to Cancel', async () => {
    const { f, dialog } = setup(scale, (p) => planOf(scale, p, { current: 2, destructive: p.count === 0 }))
    const input = await within(dialog).findByRole('textbox')
    await waitFor(() => expect(input).toHaveValue('2'))
    await userEvent.clear(input)
    await userEvent.type(input, '0{Enter}')
    const confirm = await within(dialog).findByRole('button', { name: 'Scale' })
    expect(confirm).toHaveClass('bg-danger')
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Cancel' })).toHaveFocus())
    await userEvent.keyboard('{Enter}')
    expect(f.client.runAction).not.toHaveBeenCalled()
  })

  it('scale: an out-of-range count is explained, not sent', async () => {
    const { f, dialog } = setup(scale, (p) => planOf(scale, p, { current: 2 }))
    const input = await within(dialog).findByRole('textbox')
    await waitFor(() => expect(input).toHaveValue('2'))
    await userEvent.clear(input)
    await userEvent.type(input, '11{Enter}')
    expect(dialog).toHaveTextContent('0–10')
    expect(f.client.prepareAction).toHaveBeenCalledTimes(1)
  })

  it('denied rights disable the confirmation; unknown rights are not "checked"', async () => {
    const { dialog } = setup(restart, (p) => planOf(restart, p, { rights: { state: 'denied', reason: 'you may not patch deployments in web' } }))
    expect(await within(dialog).findByRole('button', { name: 'Restart' })).toBeDisabled()
    expect(dialog).toHaveTextContent('not allowed: you may not patch deployments in web')
  })

  it('unknown rights: the action is possible, the text does not say checked', async () => {
    const { dialog } = setup(restart, (p) => planOf(restart, p, { rights: { state: 'unknown', reason: 'timeout' } }))
    expect(await within(dialog).findByRole('button', { name: 'Restart' })).toBeEnabled()
    expect(dialog).toHaveTextContent('could not be checked')
    expect(dialog).not.toHaveTextContent('checked: allowed')
  })

  it('unavailable: explained, cannot run', async () => {
    const { dialog } = setup(restart, (p) => planOf(restart, p, { unavailable: 'deployment api is paused: resume its rollout first' }))
    expect(await within(dialog).findByRole('button', { name: 'Restart' })).toBeDisabled()
    expect(within(dialog).getByRole('alert')).toHaveTextContent('paused')
  })

  it('quick Enter and clicks: one run', async () => {
    const { f, dialog } = setup(restart)
    const run = deferred<{ message: string }>()
    f.client.runAction = vi.fn(() => run.promise)
    const confirm = await within(dialog).findByRole('button', { name: 'Restart' })
    await waitFor(() => expect(confirm).toHaveFocus())
    act(() => {
      confirm.click()
      confirm.click()
    })
    await userEvent.keyboard('{Enter}')
    expect(f.client.runAction).toHaveBeenCalledTimes(1)
    await act(async () => run.resolve({ message: 'ok' }))
  })

  it('a late plan does not replace a later review', async () => {
    const slow = deferred<ActionPlan>()
    const { dialog } = setup(scale, (p) => (p.count === 3 ? slow.promise : planOf(scale, p, { current: 2, effects: [`to ${p.count ?? '?'}`] })))
    const input = await within(dialog).findByRole('textbox')
    await waitFor(() => expect(input).toHaveValue('2'))
    await userEvent.clear(input)
    await userEvent.type(input, '3{Enter}')
    await userEvent.clear(input)
    await userEvent.type(input, '4{Enter}')
    await within(dialog).findByText('to 4')
    await act(async () => slow.resolve(planOf(scale, { count: 3 }, { current: 2, effects: ['to 3'] })))
    expect(within(dialog).queryByText('to 3')).not.toBeInTheDocument()
    expect(input).toHaveValue('4')
    expect(within(dialog).getByRole('button', { name: 'Scale' })).toBeEnabled()
  })

  it('Esc while running does not close; the answer does', async () => {
    const { f, onClose, dialog } = setup(restart)
    const run = deferred<{ message: string }>()
    f.client.runAction = vi.fn(() => run.promise)
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Restart' }))
    await userEvent.keyboard('{Escape}')
    expect(onClose).not.toHaveBeenCalled()
    expect(within(dialog).getByRole('button', { name: 'Close' })).toBeDisabled()
    await act(async () => run.resolve({ message: 'deployment api: restart requested' }))
    expect(onClose).toHaveBeenCalled()
    expect(useStore.getState().notice).toBe('deployment api: restart requested')
  })

  it('an error stays in the dialog; no second run', async () => {
    const { f, onClose, dialog } = setup(restart)
    f.client.runAction = vi.fn(async () => {
      throw new ApiError('forbidden', 'deployments.apps "api" is forbidden')
    })
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Restart' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('Failed · access denied: deployments.apps "api" is forbidden')
    expect(within(dialog).queryByRole('button', { name: 'Restart' })).not.toBeInTheDocument()
    expect(onClose).not.toHaveBeenCalled()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Close' }))
    expect(onClose).toHaveBeenCalled()
  })

  it('unknown outcome: check before repeating, only Close', async () => {
    const { f, dialog } = setup(del)
    f.client.runAction = vi.fn(async () => {
      throw new ApiError('unknown', 'context deadline exceeded')
    })
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Delete' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('outcome is not known. Check the object before repeating.')
    expect(within(dialog).queryByRole('button', { name: 'Delete' })).not.toBeInTheDocument()
    expect(within(dialog).queryByRole('button', { name: 'Review again' })).not.toBeInTheDocument()
  })

  it('no answer in time: the outcome is unknown', async () => {
    const { f, dialog } = setup(restart, undefined, 50)
    f.client.runAction = vi.fn(() => new Promise<never>(() => {}))
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Restart' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('No answer within 0 s')
    expect(within(dialog).getByRole('button', { name: 'Close' })).toBeEnabled()
  })

  it('a conflict offers a new review; its plan can run', async () => {
    let n = 0
    const { f, dialog } = setup(restart, (p) => planOf(restart, p, { expect: `e${++n}` }))
    f.client.runAction = vi.fn(async () => {
      throw new ApiError('conflict', 'deployment api changed since the action was reviewed; review it again')
    })
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Restart' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('changed since this was reviewed')
    f.client.runAction = vi.fn(async () => ({ message: 'ok' }))
    await userEvent.click(within(dialog).getByRole('button', { name: 'Review again' }))
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Restart' }))
    expect(vi.mocked(f.client.runAction).mock.calls[0][0].expect).toBe('e2')
  })

  it('a failed review is explained and can be retried', async () => {
    let fail = true
    const { dialog } = setup(restart, (p) => {
      if (fail) throw new ApiError('unavailable', 'dial tcp: connection refused')
      return planOf(restart, p)
    })
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('Could not read the current state · cluster unavailable: dial tcp: connection refused')
    fail = false
    await userEvent.click(within(dialog).getByRole('button', { name: 'Review again' }))
    expect(await within(dialog).findByRole('button', { name: 'Restart' })).toBeEnabled()
  })

  it('the outcome of a run whose dialog went away (another target) is still told', async () => {
    const f = fakeClient([k8s('prod')])
    f.client.prepareAction = vi.fn(async (_r: Ref, _a: string, p: ActionParams) => planOf(restart, p))
    const run = deferred<{ message: string }>()
    f.client.runAction = vi.fn(() => run.promise)
    const { unmount } = render(<ActionDialog client={f.client} req={{ ref, action: restart, kindTitle: 'Deployments' }} onClose={() => {}} />)
    await userEvent.click(await screen.findByRole('button', { name: 'Restart' }))
    unmount()
    await act(async () => run.resolve({ message: 'deployment api: restart requested' }))
    // Another target is shown now: the notice says where it happened.
    expect(useStore.getState().notice).toBe('prod-ctx · deployment api: restart requested')
  })

  it('a failure of a run whose dialog went away is told too', async () => {
    const f = fakeClient([k8s('prod')])
    f.client.prepareAction = vi.fn(async (_r: Ref, _a: string, p: ActionParams) => planOf(restart, p))
    const run = deferred<{ message: string }>()
    f.client.runAction = vi.fn(() => run.promise)
    const { unmount } = render(<ActionDialog client={f.client} req={{ ref, action: restart, kindTitle: 'Deployments' }} onClose={() => {}} />)
    await userEvent.click(await screen.findByRole('button', { name: 'Restart' }))
    unmount()
    await act(async () => run.reject(new ApiError('forbidden', 'nope')))
    expect(useStore.getState().notice).toBe('prod-ctx · Restart api: Failed · access denied: nope')
  })

  it('a lost connection during a run is an unknown outcome, not a failure', async () => {
    const { f, dialog } = setup(del)
    f.client.runAction = vi.fn(async () => {
      throw new TypeError('Failed to fetch')
    })
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Delete' }))
    const alert = await within(dialog).findByRole('alert')
    expect(alert).toHaveTextContent('outcome is not known. Check the object before repeating.')
    expect(alert).toHaveTextContent('Failed to fetch')
    expect(alert).not.toHaveTextContent('Failed ·')
    expect(within(dialog).queryByRole('button', { name: 'Review again' })).not.toBeInTheDocument()
  })

  it('a transport answer (no coded error) during a run is an unknown outcome', async () => {
    const { f, dialog } = setup(restart)
    f.client.runAction = vi.fn(async () => {
      throw new ApiError('internal', 'HTTP 502', true)
    })
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Restart' }))
    const alert = await within(dialog).findByRole('alert')
    expect(alert).toHaveTextContent('outcome is not known')
    expect(alert).toHaveTextContent('HTTP 502')
  })

  it('Tab stays in the dialog', async () => {
    const { dialog } = setup(restart)
    await within(dialog).findByRole('button', { name: 'Restart' })
    for (let i = 0; i < 4; i++) {
      await userEvent.tab()
      expect(dialog.contains(document.activeElement)).toBe(true)
    }
  })
})
