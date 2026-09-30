import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../api/client'
import type { Ref, Value, ValueList, ValuePlan, ValueResult } from '../api/types'
import { DiscardPrompt } from '../edit/DiscardPrompt'
import { editsHeld, resetGuard } from '../edit/guard'
import { initialState, useStore } from '../store'
import { fakeClient, k8s } from '../test/fakeClient'
import { resetCopies } from './copy'
import { ValuesSection } from './ValuesSection'

const MARKER = 'MARKER-5e1f-value'
const subject: Ref = { provider: 'kubernetes', target: 'prod', scope: 'web', kind: 'secrets', name: 'db', uid: 'u1' }

const listOf = (version: string, extra: Partial<ValueList> = {}): ValueList => ({
  ref: subject,
  version,
  keys: [
    { key: 'password', size: MARKER.length, text: true },
    { key: 'tls.key', size: 3 },
  ],
  base: `base-${version}`,
  ...extra,
})
const valueOf = (key: string, version: string, extra: Partial<Value> = {}): Value => ({ key, size: MARKER.length, text: true, value: key === 'tls.key' ? 'AAEC' : MARKER, uid: 'u1', version, ...extra })

function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

let clip: string[]
let writeText: ReturnType<typeof vi.fn>
beforeEach(() => {
  useStore.setState({ ...initialState })
  resetGuard()
  resetCopies()
  clip = []
  writeText = vi.fn(async (s: string) => void clip.push(s))
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
})
afterEach(() => localStorage.clear())

function setup(version = '7') {
  const f = fakeClient([k8s('prod')])
  let listed = version
  f.client.getValues = vi.fn(async () => listOf(listed))
  f.client.revealValue = vi.fn(async (_ref: Ref, key: string) => valueOf(key, listed))
  const view = (revision?: string) => (
    <>
      <ValuesSection client={f.client} subject={subject} revision={revision} kindTitle="Secret" />
      <DiscardPrompt />
    </>
  )
  const r = render(view(version))
  return {
    f,
    ...r,
    /** The object moves to version v (the details' live view says so). */
    move: (v: string) => {
      listed = v
      r.rerender(view(v))
    },
    row: (key: string) => screen.getByText(key, { selector: 'div' }).closest('li') as HTMLElement,
  }
}

describe('ValuesSection', () => {
  it('an object once gone stays over: what was shown does not come back', async () => {
    const { f, row, rerender, container } = setup()
    await userEvent.click(within(await waitFor(() => row('password'))).getByRole('button', { name: 'Show' }))
    await within(row('password')).findByText(MARKER)
    const at = (gone: boolean) => (
      <>
        <ValuesSection client={f.client} subject={subject} revision="7" gone={gone} kindTitle="Secret" />
        <DiscardPrompt />
      </>
    )
    rerender(at(true))
    expect(container).not.toHaveTextContent(MARKER)
    // Read again (the same UID): still over.
    rerender(at(false))
    expect(container).not.toHaveTextContent(MARKER)
    expect(within(row('password')).getByRole('button', { name: 'Show' })).toBeDisabled()
  })

  it('lists keys with sizes and no value; Show reads one key now, Hide drops it', async () => {
    const { f, row, container } = setup()
    await screen.findByText('password')
    expect(row('password')).toHaveTextContent(`${MARKER.length} B`)
    expect(row('tls.key')).toHaveTextContent('binary')
    expect(container).not.toHaveTextContent(MARKER)
    expect(f.client.revealValue).not.toHaveBeenCalled()

    await userEvent.click(within(row('password')).getByRole('button', { name: 'Show' }))
    expect(await within(row('password')).findByText(MARKER)).toBeInTheDocument()
    expect(f.client.revealValue).toHaveBeenCalledWith(subject, 'password')
    // Never in attributes (titles, labels) nor in storage.
    for (const el of container.querySelectorAll('*')) for (const a of el.attributes) expect(a.value).not.toContain(MARKER)
    expect(JSON.stringify({ ...localStorage })).not.toContain(MARKER)

    await userEvent.click(within(row('password')).getByRole('button', { name: 'Hide' }))
    expect(container).not.toHaveTextContent(MARKER)
  })

  it('a new revision hides what is shown and says the value changed', async () => {
    const { f, row, move, container } = setup()
    await screen.findByText('password')
    await userEvent.click(within(row('password')).getByRole('button', { name: 'Show' }))
    await within(row('password')).findByText(MARKER)
    // Hidden at once, even before the keys are read again.
    let relist!: (l: ValueList) => void
    f.client.getValues = vi.fn(() => new Promise<ValueList>((res) => (relist = res)))
    move('8')
    expect(container).not.toHaveTextContent(MARKER)
    expect(within(row('password')).getByText('The value changed: show it again')).toBeInTheDocument()
    await waitFor(() => expect(f.client.getValues).toHaveBeenCalledTimes(1))
    await act(async () => relist(listOf('8')))
    expect(container).not.toHaveTextContent(MARKER)
    expect(within(row('password')).getByText('The value changed: show it again')).toBeInTheDocument()
  })

  it('late answers are dropped: after Hide, after Hide all, from another version, after unmount', async () => {
    const { f, row, container, unmount } = setup()
    await screen.findByText('password')
    const answers: ReturnType<typeof deferred<Value>>[] = []
    f.client.revealValue = vi.fn(() => {
      const d = deferred<Value>()
      answers.push(d)
      return d.promise
    })
    const show = () => userEvent.click(within(row('password')).getByRole('button', { name: 'Show' }))

    // Hide while reading: the answer is for a key hidden since.
    await show()
    await userEvent.click(within(row('password')).getByRole('button', { name: 'Hide' }))
    await act(async () => answers[0].resolve(valueOf('password', '7')))
    expect(container).not.toHaveTextContent(MARKER)

    // Hide all while reading.
    await show()
    await userEvent.click(within(row('tls.key')).getByRole('button', { name: 'Show' }))
    answers[2].resolve(valueOf('tls.key', '7'))
    await within(row('tls.key')).findByText('AAEC')
    await userEvent.click(screen.getByRole('button', { name: 'Hide all' }))
    await act(async () => answers[1].resolve(valueOf('password', '7')))
    expect(container).not.toHaveTextContent(MARKER)

    // Read from another version than the one listed: never shown.
    await show()
    await act(async () => answers[3].resolve(valueOf('password', '9')))
    expect(container).not.toHaveTextContent(MARKER)
    expect(within(row('password')).getByText('The value changed: show it again')).toBeInTheDocument()

    // Of another object (a replacement under the name): never shown.
    await show()
    await act(async () => answers[4].resolve(valueOf('password', '7', { uid: 'u2' })))
    expect(container).not.toHaveTextContent(MARKER)

    await show()
    unmount()
    await act(async () => answers[5].resolve(valueOf('password', '7')))
    expect(document.body).not.toHaveTextContent(MARKER)
  })

  it('a long value shows its first 64 KiB, the rest on request', async () => {
    const { f, row } = setup()
    const long = 'x'.repeat((64 << 10) + 10)
    f.client.revealValue = vi.fn(async () => valueOf('password', '7', { value: long }))
    await screen.findByText('password')
    await userEvent.click(within(row('password')).getByRole('button', { name: 'Show' }))
    const pre = await waitFor(() => row('password').querySelector('[data-value]') as HTMLElement)
    expect(pre.textContent).toHaveLength(64 << 10)
    await userEvent.click(within(row('password')).getByRole('button', { name: /Show the whole value/ }))
    expect(pre.textContent).toHaveLength(long.length)
  })
})

describe('copying a value', () => {
  it('reads the value now, writes it without showing it, and says so only after the write', async () => {
    const { row, container } = setup()
    await screen.findByText('password')
    const w = deferred<void>()
    writeText.mockImplementation(async (s: string) => {
      await w.promise
      clip.push(s)
    })
    await userEvent.click(within(row('password')).getByRole('button', { name: 'Copy' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1))
    expect(useStore.getState().notice).toBeNull()
    await act(async () => w.resolve())
    await waitFor(() => expect(useStore.getState().notice).toBe('The value of key password is copied'))
    expect(clip).toEqual([MARKER])
    expect(container).not.toHaveTextContent(MARKER)
    expect(useStore.getState().notice).not.toContain(MARKER)
  })

  it('a binary value is copied as base64, and the notice says so', async () => {
    const { row } = setup()
    await screen.findByText('tls.key')
    await userEvent.click(within(row('tls.key')).getByRole('button', { name: 'Copy' }))
    await waitFor(() => expect(useStore.getState().notice).toBe('The value of key tls.key is copied as base64 (it is binary)'))
    expect(clip).toEqual(['AAEC'])
  })

  it('Copy(A) then Copy(B), answers B then A: the clipboard holds B', async () => {
    const { f, row } = setup()
    await screen.findByText('password')
    const answers = new Map<string, ReturnType<typeof deferred<Value>>>()
    f.client.revealValue = vi.fn((_r: Ref, key: string) => {
      const d = deferred<Value>()
      answers.set(key, d)
      return d.promise
    })
    await userEvent.click(within(row('password')).getByRole('button', { name: 'Copy' }))
    await userEvent.click(within(row('tls.key')).getByRole('button', { name: 'Copy' }))
    await act(async () => answers.get('tls.key')!.resolve(valueOf('tls.key', '7')))
    await act(async () => answers.get('password')!.resolve(valueOf('password', '7')))
    await waitFor(() => expect(useStore.getState().notice).toContain('tls.key'))
    expect(clip).toEqual(['AAEC'])
  })

  it('a refused write is a visible error, never "copied"', async () => {
    const { row } = setup()
    await screen.findByText('password')
    writeText.mockRejectedValue(new Error('Write permission denied'))
    await userEvent.click(within(row('password')).getByRole('button', { name: 'Copy' }))
    expect(await within(row('password')).findByRole('alert')).toHaveTextContent('Not copied · Write permission denied')
    expect(useStore.getState().notice).toBeNull()
  })

  it('a copy waiting behind another write is dropped if the object moves meanwhile', async () => {
    const { row, move } = setup()
    await screen.findByText('password')
    const first = deferred<void>()
    writeText.mockImplementationOnce(async (s: string) => {
      await first.promise
      clip.push(s)
    })
    await userEvent.click(within(row('password')).getByRole('button', { name: 'Copy' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledTimes(1))
    await userEvent.click(within(row('tls.key')).getByRole('button', { name: 'Copy' }))
    await new Promise((r) => setTimeout(r, 20)) // read, and queued behind the first write
    move('8')
    await act(async () => first.resolve())
    await new Promise((r) => setTimeout(r, 20))
    expect(clip).toEqual([MARKER])
    expect(writeText).toHaveBeenCalledTimes(1)
    expect(useStore.getState().notice).toBeNull()
  })

  it('an answer after the object moved on is not written', async () => {
    const { f, row, move } = setup()
    await screen.findByText('password')
    const d = deferred<Value>()
    f.client.revealValue = vi.fn(() => d.promise)
    await userEvent.click(within(row('password')).getByRole('button', { name: 'Copy' }))
    move('8')
    await waitFor(() => expect(f.client.getValues).toHaveBeenCalledTimes(2))
    await act(async () => d.resolve(valueOf('password', '7')))
    expect(writeText).not.toHaveBeenCalled()
    expect(useStore.getState().notice).toBeNull()
  })
})

const planOf = (extra: Partial<ValuePlan> = {}): ValuePlan => ({
  where: { provider: 'kubernetes', target: 'prod', targetTitle: 'prod-ctx', endpoint: 'https://prod.example:6443', configRev: 'r', ref: subject },
  key: 'password',
  op: 'set',
  before: MARKER.length,
  after: 3,
  checked: true,
  changed: true,
  rights: { state: 'allowed' },
  token: 'grant-1',
  ...extra,
})

describe('changing a value', () => {
  it('an existing key starts not loaded: Review waits for Load current or typing', async () => {
    const { f, row } = setup()
    await screen.findByText('password')
    await userEvent.click(within(row('password')).getByRole('button', { name: 'Change' }))
    const dialog = screen.getByRole('dialog')
    expect(dialog).toHaveTextContent('The current value is not loaded')
    const review = within(dialog).getByRole('button', { name: /Review/ })
    expect(review).toBeDisabled()
    expect(within(dialog).getByLabelText('Value')).toHaveValue('')

    await userEvent.click(within(dialog).getByRole('button', { name: 'Load current' }))
    await waitFor(() => expect(within(dialog).getByLabelText('Value')).toHaveValue(MARKER))
    expect(review).toBeEnabled()
    // Loaded, not changed: nothing to lose.
    expect(editsHeld()).toBe(false)

    f.client.prepareValueEdit = vi.fn(async () => planOf())
    await userEvent.clear(within(dialog).getByLabelText('Value'))
    await userEvent.type(within(dialog).getByLabelText('Value'), 'new')
    expect(editsHeld()).toBe(true)
    await userEvent.click(review)
    const rv = await screen.findByRole('dialog')
    await within(rv).findByText(`${MARKER.length} → 3 B`)
    expect(f.client.prepareValueEdit).toHaveBeenCalledWith({ ref: subject, base: 'base-7', key: 'password', op: 'set', value: 'new', encoding: 'text' })
    // No value in the review.
    expect(rv).not.toHaveTextContent(MARKER)
    expect(rv).not.toHaveTextContent('new →')

    f.client.runValueEdit = vi.fn(async (): Promise<ValueResult> => ({ message: 'secret db: key password written', version: '8' }))
    const apply = within(rv).getByRole('button', { name: 'Apply' })
    await waitFor(() => expect(apply).toHaveFocus())
    await userEvent.click(apply)
    expect(f.client.runValueEdit).toHaveBeenCalledTimes(1)
    expect(f.client.runValueEdit).toHaveBeenCalledWith({ ref: subject, base: 'base-7', key: 'password', op: 'set', value: 'new', encoding: 'text', token: 'grant-1' })
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
    expect(useStore.getState().notice).toBe('Key password is written')
    expect(editsHeld()).toBe(false)
  })

  it('a shown value starts the draft; an empty value is sent only when typed', async () => {
    const { f, row } = setup()
    await screen.findByText('password')
    await userEvent.click(within(row('password')).getByRole('button', { name: 'Show' }))
    await within(row('password')).findByText(MARKER)
    await userEvent.click(within(row('password')).getByRole('button', { name: 'Change' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByLabelText('Value')).toHaveValue(MARKER)
    await userEvent.clear(within(dialog).getByLabelText('Value'))
    expect(dialog).toHaveTextContent('0 B')
    f.client.prepareValueEdit = vi.fn(async () => planOf({ after: 0 }))
    await userEvent.click(within(dialog).getByRole('button', { name: /Review/ }))
    await waitFor(() => expect(f.client.prepareValueEdit).toHaveBeenCalledWith(expect.objectContaining({ value: '', encoding: 'text' })))
  })

  it('a binary key is edited as base64 only; base64 is checked before any request', async () => {
    const { f, row } = setup()
    await screen.findByText('tls.key')
    await userEvent.click(within(row('tls.key')).getByRole('button', { name: 'Change' }))
    const dialog = screen.getByRole('dialog')
    expect(within(dialog).getByRole('radio', { name: 'Text' })).toBeDisabled()
    expect(within(dialog).getByRole('radio', { name: 'Base64' })).toHaveAttribute('aria-checked', 'true')
    expect(dialog).toHaveTextContent('it is edited as base64')
    await userEvent.type(within(dialog).getByLabelText('Value'), 'YWJ=')
    expect(dialog).toHaveTextContent('This is not valid base64')
    expect(within(dialog).getByRole('button', { name: /Review/ })).toBeDisabled()
    await userEvent.clear(within(dialog).getByLabelText('Value'))
    await userEvent.type(within(dialog).getByLabelText('Value'), 'AA0K')
    f.client.prepareValueEdit = vi.fn(async () => planOf({ key: 'tls.key' }))
    await userEvent.click(within(dialog).getByRole('button', { name: /Review/ }))
    await waitFor(() => expect(f.client.prepareValueEdit).toHaveBeenCalledWith(expect.objectContaining({ key: 'tls.key', value: 'AA0K', encoding: 'base64' })))
  })

  it('text and base64 switch without changing the bytes; bytes that are not text stay base64', async () => {
    const { row } = setup()
    await screen.findByText('password')
    await userEvent.click(within(row('password')).getByRole('button', { name: 'Change' }))
    const dialog = screen.getByRole('dialog')
    const field = within(dialog).getByLabelText('Value')
    await userEvent.type(field, 'ab{Enter}c')
    await userEvent.click(within(dialog).getByRole('radio', { name: 'Base64' }))
    expect(field).toHaveValue(btoa('ab\nc'))
    await userEvent.click(within(dialog).getByRole('radio', { name: 'Text' }))
    expect(field).toHaveValue('ab\nc')
    await userEvent.click(within(dialog).getByRole('radio', { name: 'Base64' }))
    await userEvent.clear(field)
    await userEvent.type(field, btoa('a\r\nb'))
    await userEvent.click(within(dialog).getByRole('radio', { name: 'Text' }))
    expect(dialog).toHaveTextContent('These bytes are not plain text: they stay base64.')
    expect(within(dialog).getByRole('radio', { name: 'Base64' })).toHaveAttribute('aria-checked', 'true')
  })

  it('a new key is checked: its form, and that it is not there already', async () => {
    const { row } = setup()
    await screen.findByText('password')
    void row
    await userEvent.click(screen.getByRole('button', { name: 'Add key' }))
    const dialog = screen.getByRole('dialog')
    const key = within(dialog).getByRole('textbox', { name: 'Key' })
    expect(key).toHaveFocus()
    await userEvent.type(key, 'a/b')
    expect(dialog).toHaveTextContent('A key is Latin letters')
    await userEvent.clear(key)
    await userEvent.type(key, 'password')
    expect(dialog).toHaveTextContent('Key password already exists')
    await userEvent.clear(key)
    await userEvent.type(key, 'api.token')
    expect(within(dialog).getByRole('button', { name: /Review/ })).toBeDisabled() // no value typed yet
    await userEvent.type(within(dialog).getByLabelText('Value'), 'v')
    expect(within(dialog).getByRole('button', { name: /Review/ })).toBeEnabled()
  })

  it('leaving with a changed draft asks; discarding closes the dialog', async () => {
    const { row } = setup()
    await screen.findByText('password')
    await userEvent.click(within(row('password')).getByRole('button', { name: 'Change' }))
    const dialog = screen.getByRole('dialog', { name: /Change value/ })
    await userEvent.type(within(dialog).getByLabelText('Value'), 'x')
    await userEvent.keyboard('{Escape}')
    const prompt = await screen.findByRole('alertdialog')
    await userEvent.click(within(prompt).getByRole('button', { name: 'Discard' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: /Change value/ })).toBeNull())
    expect(editsHeld()).toBe(false)
  })

  it('the draft stays when the object changes; the dialog says so', async () => {
    const { row, move } = setup()
    await screen.findByText('password')
    await userEvent.click(within(row('password')).getByRole('button', { name: 'Change' }))
    await userEvent.type(within(screen.getByRole('dialog')).getByLabelText('Value'), 'draft')
    move('8')
    const dialog = await screen.findByRole('dialog')
    expect(await within(dialog).findByText(/The object changed on the server/)).toBeInTheDocument()
    expect(within(dialog).getByLabelText('Value')).toHaveValue('draft')
  })
})

describe('reviewing a change', () => {
  async function openReview(plan: ValuePlan | Promise<ValuePlan>, op: 'Change' | 'Delete' = 'Change') {
    const s = setup()
    await screen.findByText('password')
    s.f.client.prepareValueEdit = vi.fn(async () => plan)
    await userEvent.click(within(s.row('password')).getByRole('button', { name: op }))
    if (op === 'Change') {
      const d = screen.getByRole('dialog')
      await userEvent.type(within(d).getByLabelText('Value'), 'new')
      await userEvent.click(within(d).getByRole('button', { name: /Review/ }))
    }
    const rv = screen.getByRole('dialog')
    await waitFor(() => expect(rv).toHaveAttribute('aria-busy', 'false'))
    return { ...s, rv }
  }

  it('deleting a key goes straight to a dangerous review: red Apply, focus on Cancel', async () => {
    const { f, rv } = await openReview(planOf({ op: 'delete', after: -1, destructive: true, warnings: [{ key: 'kubernetes.values.delete', text: 'A deleted key cannot be restored here' }] }), 'Delete')
    expect(f.client.prepareValueEdit).toHaveBeenCalledWith({ ref: subject, base: 'base-7', key: 'password', op: 'delete' })
    expect(rv).toHaveTextContent(`the key is deleted (${MARKER.length} B)`)
    expect(rv).toHaveTextContent('A deleted key cannot be restored here')
    const apply = within(rv).getByRole('button', { name: 'Apply' })
    expect(apply).toHaveClass('bg-danger')
    await waitFor(() => expect(within(rv).getByRole('button', { name: 'Cancel' })).toHaveFocus())
    expect(editsHeld()).toBe(false)
  })

  it('who reads it: listed, or "not known" with why — never "nobody" when unknown', async () => {
    const { rv } = await openReview(planOf({ consumers: { known: false, why: 'forbidden to list pods', items: ['pods/web-1 (env)'] } }))
    expect(within(rv).getByRole('region', { name: 'Read by' })).toHaveTextContent('pods/web-1 (env)')
    expect(within(rv).getByRole('region', { name: 'Read by' })).toHaveTextContent('Who reads it is not known: forbidden to list pods')
    expect(rv).not.toHaveTextContent('Nothing that could be seen reads it')
  })

  it('no rights: no Apply; a conflict is reviewed again', async () => {
    const { f, rv } = await openReview(planOf({ rights: { state: 'denied', reason: 'no patch' }, token: undefined }))
    expect(within(rv).getByRole('button', { name: 'Apply' })).toBeDisabled()
    expect(rv).toHaveTextContent('no patch')

    f.client.prepareValueEdit = vi.fn(async () => planOf())
    await userEvent.click(within(rv).getByRole('button', { name: 'Back to the value' }))
    const d = screen.getByRole('dialog')
    expect(within(d).getByLabelText('Value')).toHaveValue('new')
    await userEvent.click(within(d).getByRole('button', { name: /Review/ }))
    const rv2 = screen.getByRole('dialog')
    f.client.runValueEdit = vi.fn(async () => Promise.reject(new ApiError('conflict', 'secret db changed')))
    await userEvent.click(await within(rv2).findByRole('button', { name: 'Apply' }))
    expect(await within(rv2).findByRole('alert')).toHaveTextContent('The object changed since the review: secret db changed')
    expect(within(rv2).queryByRole('button', { name: 'Apply' })).toBeNull()
    await userEvent.click(within(rv2).getByRole('button', { name: 'Review again' }))
    expect(await within(rv2).findByRole('button', { name: 'Apply' })).toBeEnabled()
    expect(f.client.runValueEdit).toHaveBeenCalledTimes(1)
  })

  it('a write kept otherwise than reviewed is said, not "written"', async () => {
    const { f, rv } = await openReview(planOf())
    f.client.runValueEdit = vi.fn(async () => ({ message: 'm', differs: ['password', 'other'] }))
    await userEvent.click(within(rv).getByRole('button', { name: 'Apply' }))
    expect(await within(rv).findByRole('status')).toHaveTextContent('the server kept these keys otherwise than reviewed: password, other')
    expect(useStore.getState().notice).toBeNull()
    await userEvent.click(within(rv).getByRole('button', { name: 'Close' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
  })

  it('an unknown outcome is said as such and never sent again', async () => {
    const { f, rv } = await openReview(planOf())
    f.client.runValueEdit = vi.fn(async () => Promise.reject(new ApiError('internal', 'Failed to fetch', true)))
    await userEvent.click(within(rv).getByRole('button', { name: 'Apply' }))
    expect(await within(rv).findByRole('alert')).toHaveTextContent('its outcome is not known')
    expect(within(rv).queryByRole('button', { name: 'Apply' })).toBeNull()
    expect(f.client.runValueEdit).toHaveBeenCalledTimes(1)
  })
})

describe('review findings (P10)', () => {
  it('a copy answered after the object moved on is not written, even before the keys are read again', async () => {
    const { f, row, move } = setup()
    await screen.findByText('password')
    const d = deferred<Value>()
    f.client.revealValue = vi.fn(() => d.promise)
    f.client.getValues = vi.fn(() => new Promise<ValueList>(() => {})) // the new keys never come
    await userEvent.click(within(row('password')).getByRole('button', { name: 'Copy' }))
    move('8')
    await act(async () => d.resolve(valueOf('password', '7')))
    await new Promise((r) => setTimeout(r, 20))
    expect(writeText).not.toHaveBeenCalled()
    expect(useStore.getState().notice).toBeNull()
  })

  it('Hide gives up a pending copy of that key', async () => {
    const { f, row } = setup()
    await screen.findByText('password')
    await userEvent.click(within(row('password')).getByRole('button', { name: 'Show' }))
    await within(row('password')).findByText(MARKER)
    const d = deferred<Value>()
    f.client.revealValue = vi.fn(() => d.promise)
    await userEvent.click(within(row('password')).getByRole('button', { name: 'Copy' }))
    await userEvent.click(within(row('password')).getByRole('button', { name: 'Hide' }))
    await act(async () => d.resolve(valueOf('password', '7')))
    await new Promise((r) => setTimeout(r, 20))
    expect(writeText).not.toHaveBeenCalled()
  })

  it('Load current never overwrites what was typed after asking', async () => {
    const { f, row } = setup()
    await screen.findByText('password')
    const d = deferred<Value>()
    f.client.revealValue = vi.fn(() => d.promise)
    await userEvent.click(within(row('password')).getByRole('button', { name: 'Change' }))
    const dialog = screen.getByRole('dialog')
    await userEvent.click(within(dialog).getByRole('button', { name: 'Load current' }))
    await waitFor(() => expect(f.client.revealValue).toHaveBeenCalled())
    await userEvent.type(within(dialog).getByLabelText('Value'), 'my-new-draft')
    await act(async () => d.resolve(valueOf('password', '7')))
    expect(within(dialog).getByLabelText('Value')).toHaveValue('my-new-draft')
    expect(editsHeld()).toBe(true)
    expect(dialog).toHaveTextContent('The current value was not put in')
  })

  it('a failed re-read of the keys keeps the open dialog and its draft', async () => {
    const { f, row, move } = setup()
    await screen.findByText('password')
    await userEvent.click(within(row('password')).getByRole('button', { name: 'Change' }))
    await userEvent.type(within(screen.getByRole('dialog')).getByLabelText('Value'), 'draft')
    f.client.getValues = vi.fn(async () => Promise.reject(new ApiError('unavailable', 'down')))
    move('8')
    await waitFor(() => expect(f.client.getValues).toHaveBeenCalled())
    await new Promise((r) => setTimeout(r, 20))
    expect(within(screen.getByRole('dialog')).getByLabelText('Value')).toHaveValue('draft')
    expect(editsHeld()).toBe(true)
    expect(screen.getByRole('alert')).toHaveTextContent('Could not read the keys')
  })

  it('lists the keys under StrictMode (effects run twice)', async () => {
    const f = fakeClient([k8s('prod')])
    f.client.getValues = vi.fn(async () => listOf('7'))
    render(<ValuesSection client={f.client} subject={subject} revision="7" kindTitle="Secret" />, { reactStrictMode: true })
    expect(await screen.findByText('password')).toBeInTheDocument()
  })

  it('the review says what the server leaves: a set the server drops reads as a deletion', async () => {
    const { f, row } = setup()
    await screen.findByText('password')
    f.client.prepareValueEdit = vi.fn(async () => planOf({ after: -1, serverChanges: ['password'], destructive: true }))
    await userEvent.click(within(row('password')).getByRole('button', { name: 'Change' }))
    const d = screen.getByRole('dialog')
    await userEvent.type(within(d).getByLabelText('Value'), 'new')
    await userEvent.click(within(d).getByRole('button', { name: /Review/ }))
    const rv = screen.getByRole('dialog')
    expect(await within(rv).findByText(`the key is deleted (${MARKER.length} B)`)).toBeInTheDocument()
    expect(rv).not.toHaveTextContent('→ -1')
  })
})
