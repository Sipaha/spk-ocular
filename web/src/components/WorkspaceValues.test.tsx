import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { EditorView } from '@codemirror/view'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import type { KindDescriptor, Query, Ref, Value } from '../api/types'
import { editsHeld, resetGuard } from '../edit/guard'
import { initialState, useStore } from '../store'
import { kindsView, connectedClient as fakeClient, k8s, podRow, podsKind } from '../test/fakeClient'
import { ApiError } from '../api/client'
import { resetCopies } from '../values/copy'

let writeText: ReturnType<typeof vi.fn>
beforeEach(() => {
  useStore.setState({ ...initialState })
  resetGuard()
  resetCopies()
  writeText = vi.fn(async () => {})
  Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
})

const MARKER = 'MARKER-0d4b-value'
// The UI is generic: any kind with values gets the section (pods stand in).
const pods: KindDescriptor = { ...podsKind, editable: true, values: true }

async function openDrawer() {
  const f = fakeClient([k8s('prod')])
  f.state.view.selected = { provider: 'kubernetes', id: 'prod' }
  f.state.rows = [{ ...podRow('api-1', 'web'), rev: '1' }]
  f.state.rowsByKind['pods-api-1'] = [{ ...podRow('api-1', 'web'), rev: '1' }]
  f.client.listKinds = vi.fn(async () => kindsView([pods]))
  const open = f.client.openView
  f.client.openView = vi.fn(async (p: string, t: string, q: Query) => (q.name ? { viewId: `v-pods-${q.name}`, kind: pods } : open(p, t, q)))
  f.client.getValues = vi.fn(async (ref: Ref) => ({ ref, version: '1', keys: [{ key: 'password', size: MARKER.length, text: true }], base: 'b1' }))
  f.client.revealValue = vi.fn(async (ref: Ref, key: string) => ({ key, size: MARKER.length, text: true, value: MARKER, uid: ref.uid ?? '', version: '1' }))
  f.client.getEditSource = vi.fn(async (ref: Ref) => ({ ref, text: 'kind: Pod\nmetadata:\n  name: api-1\n', version: '1', base: 'eb' }))
  render(<App client={f.client} />)
  const grid = await screen.findByRole('grid', { name: 'resources' })
  await userEvent.click(await within(grid).findByText('api-1'))
  const drawer = await screen.findByRole('dialog', { name: 'pods api-1' })
  const values = await within(drawer).findByRole('region', { name: 'Values' })
  await within(values).findByText('password')
  return { f, drawer, values }
}

describe('values in the details', () => {
  it('a kind with values has the section; the value is read for the object shown (its UID)', async () => {
    const { f, values } = await openDrawer()
    await userEvent.click(within(values).getByRole('button', { name: 'Show' }))
    await within(values).findByText(MARKER)
    expect(f.client.revealValue).toHaveBeenCalledWith(expect.objectContaining({ provider: 'kubernetes', target: 'prod', kind: 'pods', name: 'api-1', uid: 'uid-web-api-1' }), 'password')
  })

  it('another tab hides a shown value; coming back it is not shown', async () => {
    const { drawer, values } = await openDrawer()
    await userEvent.click(within(values).getByRole('button', { name: 'Show' }))
    await within(values).findByText(MARKER)
    await userEvent.click(within(drawer).getByRole('tab', { name: 'YAML' }))
    expect(drawer).not.toHaveTextContent(MARKER)
    await userEvent.click(within(drawer).getByRole('tab', { name: 'Details' }))
    const again = await within(drawer).findByRole('region', { name: 'Values' })
    await within(again).findByText('password')
    expect(drawer).not.toHaveTextContent(MARKER)
  })

  it('a value dialog opened while the YAML editor is open leaves the editor guarded', async () => {
    const { drawer } = await openDrawer()
    await userEvent.click(within(drawer).getByRole('button', { name: 'Edit' }))
    const host = await within(drawer).findByLabelText('YAML editor')
    await waitFor(() => expect(host.querySelector('.cm-editor')).not.toBeNull())
    await userEvent.click(within(drawer).getByRole('tab', { name: 'Details' }))
    const values = await within(drawer).findByRole('region', { name: 'Values' })
    await userEvent.click(await within(values).findByRole('button', { name: 'Change' }))
    await userEvent.click(within(screen.getByRole('dialog', { name: /Change value/ })).getByRole('button', { name: 'Cancel' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: /Change value/ })).toBeNull())

    await userEvent.click(within(drawer).getByRole('tab', { name: 'YAML' }))
    const cm = within(drawer).getByLabelText('YAML editor').querySelector<HTMLElement>('.cm-editor')!
    const v = EditorView.findFromDOM(cm)!
    act(() => v.dispatch({ changes: { from: 0, insert: '# changed\n' } }))
    expect(editsHeld()).toBe(true)
    await userEvent.click(within(drawer).getByRole('button', { name: 'Close' }))
    expect(await screen.findByRole('alertdialog')).toHaveTextContent('Discard edits?')
  })

  it('an object that fails to read again keeps the open value dialog and its draft', async () => {
    const { f, values } = await openDrawer()
    await userEvent.click(within(values).getByRole('button', { name: 'Change' }))
    const dialog = screen.getByRole('dialog', { name: /Change value/ })
    await userEvent.type(within(dialog).getByLabelText('Value'), 'draft')
    // The details have followed the object's revision (debounced) once.
    await vi.waitFor(() => expect(vi.mocked(f.client.getResource).mock.calls.length).toBe(2), { timeout: 2000 })
    const { ApiError } = await import('../api/client')
    f.client.getResource = vi.fn(async () => {
      throw new ApiError('not_found', 'gone')
    })
    f.state.rowsByKind['pods-api-1'] = []
    await act(async () => f.emit({ type: 'view_changed', payload: { viewId: 'v-pods-api-1', version: 99 } }))
    expect(await screen.findByText('This object no longer exists.', undefined, { timeout: 3000 })).toBeInTheDocument()
    expect(within(screen.getByRole('dialog', { name: /Change value/ })).getByLabelText('Value')).toHaveValue('draft')
    expect(editsHeld()).toBe(true)
  })

  // The object gone for good (deleted, or its name now another object's):
  // what was shown is hidden and a pending copy is given up, while an open
  // draft stays (the user leaves it).
  const deleted = async (f: Awaited<ReturnType<typeof openDrawer>>['f']) => {
    f.client.getResource = vi.fn(async () => {
      throw new ApiError('not_found', 'gone')
    })
    f.state.rowsByKind['pods-api-1'] = []
    await act(async () => f.emit({ type: 'view_changed', payload: { viewId: 'v-pods-api-1', version: 99 } }))
    expect(await screen.findByText('This object no longer exists.', undefined, { timeout: 3000 })).toBeInTheDocument()
  }
  const replaced = async (f: Awaited<ReturnType<typeof openDrawer>>['f']) => {
    f.client.getResource = vi.fn(async (ref: Ref) => ({ ref: { ...ref, uid: 'uid-new' }, health: { state: 'ok' as const }, yaml: '', facts: [], relations: [] }))
    f.state.rowsByKind['pods-api-1'] = [{ ...podRow('api-1', 'web'), ref: { ...podRow('api-1', 'web').ref, uid: 'uid-new' }, rev: '5' }]
    await act(async () => f.emit({ type: 'view_changed', payload: { viewId: 'v-pods-api-1', version: 99 } }))
    await vi.waitFor(() => expect(f.client.getResource).toHaveBeenCalled(), { timeout: 3000 })
    await act(async () => {})
  }

  for (const [name, end] of [['deleted', deleted], ['replaced by another object of the name', replaced]] as const) {
    it(`an object ${name}: a shown value is hidden, an open draft stays`, async () => {
      const { f, values } = await openDrawer()
      await userEvent.click(within(values).getByRole('button', { name: 'Show' }))
      await within(values).findByText(MARKER)
      await userEvent.click(within(values).getByRole('button', { name: 'Change' }))
      const dialog = screen.getByRole('dialog', { name: /Change value/ })
      await userEvent.type(within(dialog).getByLabelText('Value'), 'new')
      await vi.waitFor(() => expect(vi.mocked(f.client.getResource).mock.calls.length).toBe(2), { timeout: 2000 })
      await end(f)
      await waitFor(() => expect(values.querySelector('[data-value]')).toBeNull())
      expect(within(values).queryByText(MARKER)).toBeNull()
      expect(within(screen.getByRole('dialog', { name: /Change value/ })).getByLabelText('Value')).toHaveValue(MARKER + 'new')
      expect(editsHeld()).toBe(true)
      for (const b of ['Show', 'Copy', 'Change', 'Delete', 'Add key']) expect(within(values).getByRole('button', { name: b })).toBeDisabled()
    })

    it(`an object ${name}: a pending copy is given up`, async () => {
      const { f, values } = await openDrawer()
      await vi.waitFor(() => expect(vi.mocked(f.client.getResource).mock.calls.length).toBe(2), { timeout: 2000 })
      let answer!: (v: Value) => void
      f.client.revealValue = vi.fn(() => new Promise<Value>((res) => (answer = res)))
      await userEvent.click(within(values).getByRole('button', { name: 'Copy' }))
      await vi.waitFor(() => expect(f.client.revealValue).toHaveBeenCalled())
      await end(f)
      await act(async () => answer({ key: 'password', value: MARKER, text: true, size: MARKER.length, uid: 'uid-web-api-1', version: '1' }))
      await act(async () => {})
      expect(writeText).not.toHaveBeenCalled()
    })
  }
})
