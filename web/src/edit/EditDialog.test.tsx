import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../api/client'
import type { EditPlan, EditResult, Ref } from '../api/types'
import { initialState, useStore } from '../store'
import { fakeClient, k8s } from '../test/fakeClient'
import { EditDialog, type EditReview } from './EditDialog'

beforeEach(() => useStore.setState({ ...initialState }))

const ref: Ref = { provider: 'kubernetes', target: 'prod', scope: 'web', kind: 'configmaps', name: 'cfg', uid: 'u1' }
const before = 'kind: ConfigMap\nmetadata:\n  name: cfg\n  resourceVersion: "7"\ndata:\n  a: "1"\n'
const after = before.replace('a: "1"', 'a: "2"')
const req: EditReview = { ref, base: 'base-1', original: before, edited: after, kindTitle: 'ConfigMap' }

function planOf(extra: Partial<EditPlan> = {}): EditPlan {
  return {
    where: { provider: 'kubernetes', target: 'prod', targetTitle: 'prod-ctx', endpoint: 'https://prod.example:6443', configRev: 'rev-7', ref },
    before,
    after,
    checked: true,
    changed: true,
    rights: { state: 'allowed' },
    token: 'grant-1',
    ...extra,
  }
}

function setup(plans: (() => EditPlan | Promise<EditPlan>)[] = [() => planOf()]) {
  const f = fakeClient([k8s('prod')])
  let n = 0
  f.client.prepareEdit = vi.fn(async () => plans[Math.min(n++, plans.length - 1)]())
  const onBack = vi.fn()
  const onDone = vi.fn()
  const { unmount } = render(<EditDialog client={f.client} req={req} onBack={onBack} onDone={onDone} />)
  return { f, onBack, onDone, unmount, dialog: screen.getByRole('dialog') }
}

describe('EditDialog', () => {
  it('shows where, the server-checked result and its diff; Apply writes the reviewed edit once', async () => {
    const { f, onDone, dialog } = setup()
    const apply = await within(dialog).findByRole('button', { name: 'Apply' })
    expect(within(dialog).getByLabelText('where')).toHaveTextContent('prod-ctx')
    expect(within(dialog).getByLabelText('where')).toHaveTextContent('https://prod.example:6443')
    expect(within(dialog).getByLabelText('where')).toHaveTextContent('web')
    expect(within(dialog).getByLabelText('where')).toHaveTextContent('ConfigMap')
    expect(dialog).toHaveTextContent('checked by the server without writing')
    const diff = within(dialog).getByRole('region', { name: 'Changes' })
    expect(diff.querySelector('[data-op="-"]')).toHaveTextContent('a: "1"')
    expect(diff.querySelector('[data-op="+"]')).toHaveTextContent('a: "2"')
    await waitFor(() => expect(apply).toHaveFocus())
    expect(apply).toHaveClass('bg-accent')

    let finish!: (r: EditResult) => void
    f.client.runEdit = vi.fn(() => new Promise<EditResult>((res) => (finish = res)))
    await userEvent.click(apply)
    await userEvent.click(apply) // a second press while writing: nothing
    expect(f.client.runEdit).toHaveBeenCalledTimes(1)
    expect(f.client.runEdit).toHaveBeenCalledWith({ ref, base: 'base-1', original: before, edited: after, token: 'grant-1' })
    // Esc does not leave while writing.
    await userEvent.keyboard('{Escape}')
    expect(dialog).toBeInTheDocument()
    // The same object as expected (only its version moved): done.
    finish({ message: 'configmap cfg: changes written', version: '8', actual: after.replace('"7"', '"8"') })
    await waitFor(() => expect(onDone).toHaveBeenCalledWith(expect.objectContaining({ message: 'configmap cfg: changes written' })))
  })

  it('a local result is said so; a destructive plan is red with the focus on Back', async () => {
    const { dialog } = setup([() => planOf({ checked: false, destructive: true, warnings: [{ key: 'kubernetes.edit.local', text: 'Not checked by the server' }] })])
    const apply = await within(dialog).findByRole('button', { name: 'Apply' })
    expect(dialog).toHaveTextContent('Computed locally, not checked by the server.')
    expect(apply).toHaveClass('bg-danger')
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Back to the text' })).toHaveFocus())
  })

  it('a refusal, a denial or no change offers no Apply', async () => {
    for (const plan of [
      planOf({ unavailable: { text: 'data.a: Invalid value' }, token: undefined }),
      planOf({ rights: { state: 'denied', reason: 'RBAC' }, token: undefined }),
      planOf({ changed: false, token: undefined, after: before }),
    ]) {
      const { dialog, onBack, unmount } = setup([() => plan])
      await within(dialog).findByRole('button', { name: 'Back to the text' })
      await waitFor(() => expect(dialog).toHaveAttribute('aria-busy', 'false'))
      const apply = within(dialog).queryByRole('button', { name: 'Apply' })
      if (apply) expect(apply).toBeDisabled()
      if (plan.unavailable) expect(within(dialog).getByRole('alert')).toHaveTextContent('The server refuses this edit: data.a: Invalid value')
      if (!plan.changed) expect(dialog).toHaveTextContent('No changes')
      await userEvent.keyboard('{Escape}')
      expect(onBack).toHaveBeenCalled()
      unmount()
    }
  })

  it('a conflict at the write is reviewed again, never written again by itself', async () => {
    const { f, dialog } = setup([() => planOf(), () => planOf({ rebased: true, token: 'grant-2', warnings: [{ text: 'rebased' }] })])
    f.client.runEdit = vi.fn(async () => {
      throw new ApiError('conflict', 'configmap cfg changed since the edit was reviewed')
    })
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Apply' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('The object changed since the review')
    expect(within(dialog).queryByRole('button', { name: 'Apply' })).not.toBeInTheDocument()
    expect(f.client.runEdit).toHaveBeenCalledTimes(1)
    await userEvent.click(within(dialog).getByRole('button', { name: 'Review again' }))
    await waitFor(() => expect(f.client.prepareEdit).toHaveBeenCalledTimes(2))
    f.client.runEdit = vi.fn(async () => ({ message: 'written' }))
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Apply' }))
    expect(f.client.runEdit).toHaveBeenCalledWith(expect.objectContaining({ token: 'grant-2' }))
  })

  it('a review again that turns destructive starts at Back, wherever the focus was', async () => {
    const { f, dialog } = setup([() => planOf(), () => planOf({ destructive: true, collisions: ['data.a'], token: 'grant-2' })])
    f.client.runEdit = vi.fn(async () => {
      throw new ApiError('conflict', 'changed')
    })
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Apply' }))
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Review again' }))
    const apply = await within(dialog).findByRole('button', { name: 'Apply' })
    expect(apply).toHaveClass('bg-danger')
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Back to the text' })).toHaveFocus())
  })

  it('a lost answer is unknown, not a failure', async () => {
    const { f, dialog } = setup()
    f.client.runEdit = vi.fn(async () => {
      throw new ApiError('internal', 'Load failed', true)
    })
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Apply' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('outcome is not known')
    expect(within(dialog).queryByRole('button', { name: 'Apply' })).not.toBeInTheDocument()
  })

  it('a result other than the expected one is said, with the difference', async () => {
    const { f, dialog, onDone } = setup()
    const actual = after.replace('"7"', '"8"').replace('data:', 'data:\n  added: by-webhook')
    f.client.runEdit = vi.fn(async () => ({ message: 'configmap cfg: changes written', version: '8', actual }))
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Apply' }))
    expect(await within(dialog).findByRole('status')).toHaveTextContent('differs from the expected result')
    const diff = within(dialog).getByRole('region', { name: 'Expected → written' })
    expect(diff.querySelector('[data-op="+"]')).toHaveTextContent('added: by-webhook')
    expect(diff).not.toHaveTextContent('resourceVersion')
    expect(onDone).not.toHaveBeenCalled()
    await userEvent.click(within(dialog).getByRole('button', { name: 'Close' }))
    expect(onDone).toHaveBeenCalledWith(expect.objectContaining({ actual }))
  })

  it('a held Enter never applies', async () => {
    const { f, dialog } = setup()
    const apply = await within(dialog).findByRole('button', { name: 'Apply' })
    await waitFor(() => expect(apply).toHaveFocus())
    for (let i = 0; i < 3; i++) apply.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', repeat: true, bubbles: true, cancelable: true }))
    expect(f.client.runEdit).not.toHaveBeenCalled()
  })
})
