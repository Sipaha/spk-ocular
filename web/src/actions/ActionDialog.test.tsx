import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../api/client'
import type { ActionDescriptor, ActionParams, ActionPlan, ActionResult, Ref } from '../api/types'
import { initialState, useStore } from '../store'
import { setLanguage } from '../i18n'
import { fakeClient, k8s, k8sScopeNames } from '../test/fakeClient'
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
    effects: [{ text: `${action.id} effect` }],
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
  const { unmount } = render(<ActionDialog client={f.client} req={{ ref, action, kindTitle: 'Deployments' }} onClose={onClose} runTimeoutMs={timeout} />)
  return { f, onClose, unmount, dialog: screen.getByRole('dialog') }
}

describe('ActionDialog', () => {
  it('names the scope in the words of the object’s own provider, not the selected one', async () => {
    const zone = { singular: { key: 'other.scope.singular', text: 'Zone' }, plural: { key: 'other.scope.plural', text: 'zones' }, all: { key: 'other.scope.all', text: 'All zones' } }
    useStore.setState({
      view: {
        groups: [
          { provider: 'kubernetes', title: 'Kubernetes', targets: [k8s('prod')], problems: [], scopeNames: k8sScopeNames },
          { provider: 'other', title: 'Other', targets: [{ provider: 'other', id: 'site', title: 'site' }], problems: [], scopeNames: zone },
        ],
        selected: { provider: 'kubernetes', id: 'prod' },
      },
    })
    const other: Ref = { provider: 'other', target: 'site', scope: 'blue', kind: 'crates', name: 'c-1', uid: 'c-1', title: 'alpha' }
    const f = fakeClient([k8s('prod')])
    f.client.prepareAction = vi.fn(async () => planOf(restart, {}, { where: { provider: 'other', target: 'site', targetTitle: 'site', ref: other } }))
    render(<ActionDialog client={f.client} req={{ ref: other, action: restart, kindTitle: 'Crate' }} onClose={() => {}} />)
    const where = await screen.findByLabelText('where')
    await within(screen.getByRole('dialog')).findByRole('button', { name: 'Restart' })
    expect(where).toHaveTextContent('Zoneblue')
    expect(where).toHaveTextContent('Namealpha')
    expect(where).not.toHaveTextContent('Namespace')
  })

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

  it('effects are said in the UI\'s language by key; an unknown key in the provider\'s English', async () => {
    setLanguage('ru')
    try {
      const { dialog } = setup(restart, (p) =>
        planOf(restart, p, {
          effects: [
            { key: 'kubernetes.scale.down', params: { from: '5', to: '2', count: '3' }, text: '5 → 2: 3 pods are removed.' },
            { key: 'other.unknown', text: 'Something only the provider knows.' },
          ],
        }),
      )
      const effects = await within(dialog).findByRole('region', { name: 'Что произойдёт' })
      expect(effects).toHaveTextContent('5 → 2: удаляется pod-ов: 3.')
      expect(effects).toHaveTextContent('Something only the provider knows.')
    } finally {
      setLanguage('en')
    }
  })

  it('unavailable: explained, cannot run', async () => {
    const { dialog } = setup(restart, (p) => planOf(restart, p, { unavailable: { text: 'deployment api is paused: resume its rollout first' } }))
    expect(await within(dialog).findByRole('button', { name: 'Restart' })).toBeDisabled()
    expect(within(dialog).getByRole('alert')).toHaveTextContent('paused')
  })

  it('quick Enter and clicks: one run', async () => {
    const { f, dialog } = setup(restart)
    const run = deferred<ActionResult>()
    f.client.runAction = vi.fn(() => run.promise)
    const confirm = await within(dialog).findByRole('button', { name: 'Restart' })
    await waitFor(() => expect(confirm).toHaveFocus())
    act(() => {
      confirm.click()
      confirm.click()
    })
    await userEvent.keyboard('{Enter}')
    expect(f.client.runAction).toHaveBeenCalledTimes(1)
    await act(async () => run.resolve({ message: { text: 'ok' } }))
  })

  it('a late plan does not replace a later review', async () => {
    const slow = deferred<ActionPlan>()
    const { dialog } = setup(scale, (p) => (p.count === 3 ? slow.promise : planOf(scale, p, { current: 2, effects: [{ text: `to ${p.count ?? '?'}` }] })))
    const input = await within(dialog).findByRole('textbox')
    await waitFor(() => expect(input).toHaveValue('2'))
    await userEvent.clear(input)
    await userEvent.type(input, '3{Enter}')
    await userEvent.clear(input)
    await userEvent.type(input, '4{Enter}')
    await within(dialog).findByText('to 4')
    await act(async () => slow.resolve(planOf(scale, { count: 3 }, { current: 2, effects: [{ text: 'to 3' }] })))
    expect(within(dialog).queryByText('to 3')).not.toBeInTheDocument()
    expect(input).toHaveValue('4')
    expect(within(dialog).getByRole('button', { name: 'Scale' })).toBeEnabled()
  })

  it('Esc while running does not close; the answer does', async () => {
    const { f, onClose, dialog } = setup(restart)
    const run = deferred<ActionResult>()
    f.client.runAction = vi.fn(() => run.promise)
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Restart' }))
    await userEvent.keyboard('{Escape}')
    expect(onClose).not.toHaveBeenCalled()
    expect(within(dialog).getByRole('button', { name: 'Close' })).toBeDisabled()
    await act(async () => run.resolve({ message: { text: 'deployment api: restart requested' } }))
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

  describe("in the UI's language (by key)", () => {
    const done = { key: 'kubernetes.done.restart', params: { kind: 'deployment', name: 'api' }, text: 'deployment api: restart requested' }
    const changed = { key: 'kubernetes.error.changed', params: { kind: 'deployment', name: 'api' }, text: 'deployment api changed since the action was reviewed; review it again' }
    beforeEach(() => setLanguage('ru'))
    afterEach(() => setLanguage('en'))

    it('the notice of a success', async () => {
      const { f, dialog, onClose } = setup(restart)
      f.client.runAction = vi.fn(async () => ({ message: done }))
      await userEvent.click(await within(dialog).findByRole('button', { name: 'Перезапустить' }))
      await waitFor(() => expect(onClose).toHaveBeenCalled())
      expect(useStore.getState().notice).toBe('deployment api: перезапуск запрошен')
    })

    it('a late success and a late refusal', async () => {
      const first = setup(restart, undefined, 50)
      const run = deferred<ActionResult>()
      first.f.client.runAction = vi.fn(() => run.promise)
      await userEvent.click(await within(first.dialog).findByRole('button', { name: 'Перезапустить' }))
      await within(first.dialog).findByRole('alert')
      await act(async () => run.resolve({ message: done }))
      expect(within(first.dialog).getByRole('status')).toHaveTextContent('deployment api: перезапуск запрошен')
      expect(useStore.getState().notice).toBe('deployment api: перезапуск запрошен')
      first.unmount()

      const second = setup(restart, undefined, 50)
      const late = deferred<ActionResult>()
      second.f.client.runAction = vi.fn(() => late.promise)
      await userEvent.click(await within(second.dialog).findByRole('button', { name: 'Перезапустить' }))
      await within(second.dialog).findByRole('alert')
      await act(async () => late.reject(new ApiError('conflict', changed.text, false, changed)))
      expect(within(second.dialog).getByRole('alert')).toHaveTextContent('deployment api изменился после просмотра — посмотрите снова')
    })

    it('a conflict says its reason; a server text stays as it is', async () => {
      const { f, dialog } = setup(restart)
      f.client.runAction = vi.fn(async () => {
        throw new ApiError('conflict', changed.text, false, changed)
      })
      await userEvent.click(await within(dialog).findByRole('button', { name: 'Перезапустить' }))
      const alert = await within(dialog).findByRole('alert')
      expect(alert).toHaveTextContent('deployment api изменился после просмотра — посмотрите снова')
      expect(alert).not.toHaveTextContent('review it again')
    })

    it('a lost connection stays "unknown" whatever reason it carries', async () => {
      const { f, dialog } = setup(del)
      f.client.runAction = vi.fn(async () => {
        throw new ApiError('conflict', 'x', true, changed)
      })
      await userEvent.click(await within(dialog).findByRole('button', { name: 'Удалить' }))
      expect(await within(dialog).findByRole('alert')).toHaveTextContent('неизвест')
    })
  })

  describe('an answer after the timeout', () => {
    it('success: its dialog, still showing "unknown", says it was done', async () => {
      const { f, dialog, onClose } = setup(restart, undefined, 50)
      const run = deferred<ActionResult>()
      f.client.runAction = vi.fn(() => run.promise)
      await userEvent.click(await within(dialog).findByRole('button', { name: 'Restart' }))
      expect(await within(dialog).findByRole('alert')).toHaveTextContent('No answer within 0 s')
      await act(async () => run.resolve({ message: { text: 'deployment api: restart requested' } }))
      expect(within(dialog).getByRole('status')).toHaveTextContent('Done after all: deployment api: restart requested')
      expect(within(dialog).queryByRole('alert')).not.toBeInTheDocument()
      expect(useStore.getState().notice).toBe('deployment api: restart requested')
      expect(onClose).not.toHaveBeenCalled() // the user closes it
      expect(f.client.runAction).toHaveBeenCalledTimes(1) // nothing is sent again
    })

    it('a refusal: its dialog says the late answer', async () => {
      const { f, dialog } = setup(restart, undefined, 50)
      const run = deferred<ActionResult>()
      f.client.runAction = vi.fn(() => run.promise)
      await userEvent.click(await within(dialog).findByRole('button', { name: 'Restart' }))
      await within(dialog).findByRole('alert')
      await act(async () => run.reject(new ApiError('forbidden', 'nope')))
      expect(within(dialog).getByRole('alert')).toHaveTextContent('The answer came late: Failed · access denied: nope')
      expect(within(dialog).queryByRole('button', { name: 'Restart' })).not.toBeInTheDocument()
    })

    it('its dialog closed: a notice says where and what', async () => {
      const f = fakeClient([k8s('prod')])
      f.client.prepareAction = vi.fn(async (_r: Ref, _a: string, p: ActionParams) => planOf(restart, p))
      const run = deferred<ActionResult>()
      f.client.runAction = vi.fn(() => run.promise)
      const { unmount } = render(<ActionDialog client={f.client} req={{ ref, action: restart, kindTitle: 'Deployment' }} onClose={() => {}} runTimeoutMs={50} />)
      await userEvent.click(await screen.findByRole('button', { name: 'Restart' }))
      await screen.findByRole('alert')
      unmount()
      await act(async () => run.resolve({ message: { text: 'deployment api: restart requested' } }))
      expect(useStore.getState().notice).toBe('prod-ctx · Done after all: deployment api: restart requested')
    })

    it('another confirmation open meanwhile is left alone', async () => {
      const f = fakeClient([k8s('prod')])
      f.client.prepareAction = vi.fn(async (_r: Ref, _a: string, p: ActionParams) => planOf(p.count === undefined ? restart : scale, p))
      const run = deferred<ActionResult>()
      f.client.runAction = vi.fn(() => run.promise)
      const first = render(<ActionDialog key={1} client={f.client} req={{ ref, action: restart, kindTitle: 'Deployment' }} onClose={() => {}} runTimeoutMs={50} />)
      await userEvent.click(await screen.findByRole('button', { name: 'Restart' }))
      await screen.findByRole('alert')
      first.rerender(<ActionDialog key={2} client={f.client} req={{ ref, action: del, kindTitle: 'Deployment' }} onClose={() => {}} />)
      const second = screen.getByRole('dialog', { name: 'Delete api' })
      await within(second).findByRole('button', { name: 'Delete' })
      await act(async () => run.reject(new ApiError('forbidden', 'nope')))
      expect(within(second).queryByRole('alert')).not.toBeInTheDocument()
      expect(within(second).getByRole('button', { name: 'Delete' })).toBeEnabled()
      expect(useStore.getState().notice).toBe('prod-ctx · Restart api: The answer came late: Failed · access denied: nope')
    })
  })

  describe('a run of several parts (a service\'s containers)', () => {
    const parts = (last: 'unknown' | 'refused'): ActionResult => ({
      message: { text: '1 of 3 containers restarted' },
      outcome: last,
      parts: [
        { id: 'c1', title: 'web-1', outcome: 'done' },
        { id: 'c2', title: 'web-2', outcome: last, why: { text: 'no answer within 25s' } },
        { id: 'c3', title: 'web-3', outcome: 'skipped' },
      ],
    })

    it('an unknown part: the dialog stays with every part; a notice sums it up', async () => {
      const { f, dialog, onClose } = setup(restart)
      f.client.runAction = vi.fn(async () => parts('unknown'))
      await userEvent.click(await within(dialog).findByRole('button', { name: 'Restart' }))
      expect(await within(dialog).findByRole('alert')).toHaveTextContent('the outcome of a part is not known')
      const list = within(dialog).getByRole('list', { name: 'Result' })
      const items = within(list).getAllByRole('listitem').map((li) => li.textContent)
      expect(items).toEqual(['web-1Done', 'web-2Outcome unknown · no answer within 25s', 'web-3Not run'])
      expect(useStore.getState().notice).toBe('Restart api: 1 of 3 done; 1 outcome unknown; 1 not run')
      expect(within(dialog).queryByRole('button', { name: 'Restart' })).not.toBeInTheDocument()
      expect(within(dialog).getByRole('button', { name: 'Close' })).toBeEnabled()
      expect(onClose).not.toHaveBeenCalled()
    })

    it('a refused part: said as a refusal', async () => {
      const { f, dialog } = setup(restart)
      f.client.runAction = vi.fn(async () => parts('refused'))
      await userEvent.click(await within(dialog).findByRole('button', { name: 'Restart' }))
      expect(await within(dialog).findByRole('alert')).toHaveTextContent('Not everything was done: a part was refused')
      expect(useStore.getState().notice).toBe('Restart api: 1 of 3 done; 1 refused; 1 not run')
    })

    it('all parts done: closes like a single run, the notice counts them', async () => {
      const { f, dialog, onClose } = setup(restart)
      f.client.runAction = vi.fn(async () => ({
        message: { text: '2 of 2 containers restarted' },
        outcome: 'done' as const,
        parts: [{ id: 'c1', title: 'web-1', outcome: 'done' as const }, { id: 'c2', title: 'web-2', outcome: 'done' as const }],
      }))
      await userEvent.click(await within(dialog).findByRole('button', { name: 'Restart' }))
      await waitFor(() => expect(onClose).toHaveBeenCalled())
      expect(useStore.getState().notice).toBe('Restart api: 2 of 2 done')
    })

    it('late: its dialog shows the parts instead of "unknown"', async () => {
      const { f, dialog } = setup(restart, undefined, 50)
      const run = deferred<ActionResult>()
      f.client.runAction = vi.fn(() => run.promise)
      await userEvent.click(await within(dialog).findByRole('button', { name: 'Restart' }))
      expect(await within(dialog).findByRole('alert')).toHaveTextContent('No answer within 0 s')
      await act(async () => run.resolve(parts('unknown')))
      expect(within(dialog).getByRole('alert')).toHaveTextContent('The answer came late: Stopped: the outcome of a part is not known')
      expect(within(dialog).getByRole('list', { name: 'Result' })).toHaveTextContent('web-2Outcome unknown')
    })

    it('late, its dialog closed: a notice with where and the sum', async () => {
      const f = fakeClient([k8s('prod')])
      f.client.prepareAction = vi.fn(async (_r: Ref, _a: string, p: ActionParams) => planOf(restart, p))
      const run = deferred<ActionResult>()
      f.client.runAction = vi.fn(() => run.promise)
      const { unmount } = render(<ActionDialog client={f.client} req={{ ref, action: restart, kindTitle: 'Service' }} onClose={() => {}} runTimeoutMs={50} />)
      await userEvent.click(await screen.findByRole('button', { name: 'Restart' }))
      await screen.findByRole('alert')
      unmount()
      await act(async () => run.resolve(parts('refused')))
      expect(useStore.getState().notice).toBe('prod-ctx · Restart api: The answer came late: 1 of 3 done; 1 refused; 1 not run')
    })
  })

  describe('lists of a plan and many parts', () => {
    const items = (n: number, prefix = 'pod') => Array.from({ length: n }, (_, i) => ({ name: `${prefix}-${i + 1}` }))

    it('a list is shown whole, in portions of 50; a collapsed one opens on request', async () => {
      const { dialog } = setup(
        restart,
        (p) =>
          planOf(restart, p, {
            lists: [
              { title: { key: 'test.evicted', text: 'Evicted' }, destructive: true, items: items(120) },
              { title: { text: 'Left alone' }, collapsed: true, items: [{ name: 'ds-1', note: { text: 'a daemon' } }] },
            ],
          }),
      )
      const evicted = await within(dialog).findByRole('region', { name: 'Evicted (120)' })
      expect(within(evicted).getAllByRole('listitem')).toHaveLength(50)
      await userEvent.click(within(evicted).getByRole('button', { name: 'Show 50 more (70 left)' }))
      expect(within(evicted).getAllByRole('listitem')).toHaveLength(100)
      await userEvent.click(within(evicted).getByRole('button', { name: 'Show 20 more (20 left)' }))
      expect(within(evicted).getAllByRole('listitem')).toHaveLength(120)
      expect(within(evicted).queryByRole('button', { name: /more/ })).toBeNull()
      expect(evicted).toHaveTextContent('pod-120')

      const left = within(dialog).getByRole('region', { name: 'Left alone (1)' })
      expect(within(left).queryByRole('listitem')).toBeNull()
      await userEvent.click(within(left).getByRole('button', { name: 'Left alone (1)' }))
      expect(within(left).getByRole('listitem')).toHaveTextContent('ds-1 · a daemon')
    })

    it('list titles and part reasons are said in the UI\'s language by key', async () => {
      setLanguage('ru')
      try {
        const { f, dialog } = setup(restart, (p) => planOf(restart, p, { lists: [{ title: { key: 'compose.scope.singular', text: 'x' }, items: items(1) }] }))
        expect(await within(dialog).findByRole('region', { name: 'Проект (1)' })).toBeInTheDocument()
        f.client.runAction = vi.fn(async () => ({
          message: { text: 'm' },
          outcome: 'refused' as const,
          parts: [{ id: 'p1', title: 'pod-1', outcome: 'refused' as const, why: { key: 'compose.scope.singular', text: 'x' } }],
        }))
        await userEvent.click(within(dialog).getByRole('button', { name: 'Перезапустить' }))
        expect(await within(dialog).findByRole('list', { name: 'Итог' })).toHaveTextContent('pod-1Отказано · Проект')
      } finally {
        setLanguage('en')
      }
    })

    it('a run left unfinished (skipped parts): the dialog stays with them', async () => {
      const { f, dialog, onClose } = setup(restart)
      f.client.runAction = vi.fn(async () => ({
        message: { text: 'm' },
        outcome: 'skipped' as const,
        parts: [
          { id: 'p1', title: 'pod-1', outcome: 'done' as const },
          { id: 'p2', title: 'bare-1', outcome: 'skipped' as const, why: { text: 'left: no controller' } },
        ],
      }))
      await userEvent.click(await within(dialog).findByRole('button', { name: 'Restart' }))
      expect(await within(dialog).findByRole('alert')).toHaveTextContent('Not everything was done')
      expect(within(dialog).getByRole('list', { name: 'Result' })).toHaveTextContent('bare-1Not run · left: no controller')
      expect(onClose).not.toHaveBeenCalled()
    })

    it('many parts: shown in portions, in their order, every one reachable', async () => {
      const { f, dialog } = setup(restart)
      const parts = Array.from({ length: 130 }, (_, i) => ({ id: `p${i}`, title: `pod-${i + 1}`, outcome: (i === 129 ? 'refused' : 'done') as 'done' | 'refused' }))
      f.client.runAction = vi.fn(async () => ({ message: { text: 'm' }, outcome: 'refused' as const, parts }))
      await userEvent.click(await within(dialog).findByRole('button', { name: 'Restart' }))
      const list = await within(dialog).findByRole('list', { name: 'Result' })
      const shown = within(list).getAllByRole('listitem')
      expect(shown).toHaveLength(50)
      expect(shown[0]).toHaveTextContent('pod-1Done')
      await userEvent.click(within(dialog).getByRole('button', { name: 'Show 50 more (80 left)' }))
      expect(within(list).getAllByRole('listitem')).toHaveLength(100)
      await userEvent.click(within(dialog).getByRole('button', { name: 'Show 30 more (30 left)' }))
      expect(within(list).getAllByRole('listitem')[129]).toHaveTextContent('pod-130Refused')
    })
  })

  it('a conflict offers a new review; its plan can run', async () => {
    let n = 0
    const { f, dialog } = setup(restart, (p) => planOf(restart, p, { expect: `e${++n}` }))
    f.client.runAction = vi.fn(async () => {
      throw new ApiError('conflict', 'deployment api changed since the action was reviewed; review it again')
    })
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Restart' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('changed since this was reviewed')
    f.client.runAction = vi.fn(async () => ({ message: { text: 'ok' } }))
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
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('Could not read the current state · unavailable: dial tcp: connection refused')
    fail = false
    await userEvent.click(within(dialog).getByRole('button', { name: 'Review again' }))
    expect(await within(dialog).findByRole('button', { name: 'Restart' })).toBeEnabled()
  })

  it('the outcome of a run whose dialog went away (another target) is still told', async () => {
    const f = fakeClient([k8s('prod')])
    f.client.prepareAction = vi.fn(async (_r: Ref, _a: string, p: ActionParams) => planOf(restart, p))
    const run = deferred<ActionResult>()
    f.client.runAction = vi.fn(() => run.promise)
    const { unmount } = render(<ActionDialog client={f.client} req={{ ref, action: restart, kindTitle: 'Deployments' }} onClose={() => {}} />)
    await userEvent.click(await screen.findByRole('button', { name: 'Restart' }))
    unmount()
    await act(async () => run.resolve({ message: { text: 'deployment api: restart requested' } }))
    // Another target is shown now: the notice says where it happened.
    expect(useStore.getState().notice).toBe('prod-ctx · deployment api: restart requested')
  })

  it('a failure of a run whose dialog went away is told too', async () => {
    const f = fakeClient([k8s('prod')])
    f.client.prepareAction = vi.fn(async (_r: Ref, _a: string, p: ActionParams) => planOf(restart, p))
    const run = deferred<ActionResult>()
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
