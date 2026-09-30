import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { EditorView } from '@codemirror/view'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import type { EditPrepareRequest, KindDescriptor, Query, Ref } from '../api/types'
import { resetGuard } from '../edit/guard'
import { initialState, useStore } from '../store'
import { kindsView, fakeClient, k8s, podRow, podsKind } from '../test/fakeClient'

beforeEach(() => {
  useStore.setState({ ...initialState })
  resetGuard()
})

const pods: KindDescriptor = { ...podsKind, editable: true }
const nodes: KindDescriptor = { id: 'nodes', title: 'Nodes', group: 'Cluster', scoped: false, columns: [{ id: 'name', title: 'Name', type: 'text' }] }
const docText = (name: string, rv = '1') => `# header\nkind: Pod\nmetadata:\n  name: ${name}\n  resourceVersion: "${rv}"\nspec:\n  x: 1\n`

async function openProd() {
  const f = fakeClient([k8s('prod'), k8s('stage')])
  f.state.view.selected = { provider: 'kubernetes', id: 'prod' }
  f.state.rows = [{ ...podRow('api-1', 'web'), rev: '1' }, { ...podRow('api-2', 'web'), rev: '1' }]
  f.state.rowsByKind['pods-api-1'] = [{ ...podRow('api-1', 'web'), rev: '1' }]
  f.client.listKinds = vi.fn(async () => kindsView([pods, nodes]))
  const open = f.client.openView
  // The drawer's view of its one object (by name) is a view of its own.
  f.client.openView = vi.fn(async (p: string, t: string, q: Query) => (q.name ? { viewId: `v-pods-${q.name}`, kind: pods } : open(p, t, q)))
  f.client.getEditSource = vi.fn(async (ref: Ref) => ({ ref: { ...ref, uid: `uid-web-${ref.name}` }, text: docText(ref.name), version: '1', base: `base-${ref.name}` }))
  f.client.prepareEdit = vi.fn(async (req: EditPrepareRequest) => ({
    where: { provider: 'kubernetes', target: 'prod', targetTitle: 'prod', configRev: '1', ref: req.ref },
    before: req.original,
    after: req.edited,
    checked: true,
    changed: req.original !== req.edited,
    rights: { state: 'allowed' as const },
    token: 'grant',
  }))
  render(<App client={f.client} />)
  const grid = await screen.findByRole('grid', { name: 'resources' })
  await within(grid).findByText('api-2')
  return { f, grid }
}

const editorView = async (drawer: HTMLElement) => {
  const host = await within(drawer).findByLabelText('YAML editor')
  await waitFor(() => expect(host.querySelector('.cm-editor')).not.toBeNull())
  return EditorView.findFromDOM(host.querySelector<HTMLElement>('.cm-editor')!)!
}

const type = (v: EditorView, from: string, to: string) => {
  const at = v.state.doc.toString().indexOf(from)
  act(() => v.dispatch({ changes: { from: at, to: at + from.length, insert: to } }))
}

async function openEditor() {
  const ctx = await openProd()
  await userEvent.click(within(ctx.grid).getByText('api-1'))
  const drawer = await screen.findByRole('dialog', { name: 'pods api-1' })
  await within(drawer).findByRole('button', { name: 'Edit' })
  await userEvent.click(within(drawer).getByRole('button', { name: 'Edit' }))
  const v = await editorView(drawer)
  return { ...ctx, drawer, v }
}

describe('editing an object in the details', () => {
  it('offers Edit only for editable kinds', async () => {
    const { grid } = await openProd()
    await userEvent.click(within(grid).getByText('api-1'))
    const drawer = await screen.findByRole('dialog', { name: 'pods api-1' })
    expect(await within(drawer).findByRole('button', { name: 'Edit' })).toBeEnabled()
    await userEvent.click(await within(drawer).findByRole('button', { name: 'nodes/node-1' }))
    await screen.findByRole('dialog', { name: 'nodes node-1' })
    expect(within(drawer).queryByRole('button', { name: 'Edit' })).not.toBeInTheDocument()
  })

  it('E (in a Russian layout too) edits, Ctrl+Enter reviews, Apply writes and the editor closes', async () => {
    const { f, grid } = await openProd()
    await userEvent.click(within(grid).getByText('api-1'))
    const drawer = await screen.findByRole('dialog', { name: 'pods api-1' })
    await within(drawer).findByRole('button', { name: 'Edit' })
    // A Russian layout types "у" on the E key.
    fireEvent.keyDown(drawer, { key: 'у', code: 'KeyE' })
    const v = await editorView(drawer)
    expect(f.client.getEditSource).toHaveBeenCalledWith(expect.objectContaining({ kind: 'pods', name: 'api-1', uid: 'uid-web-api-1' }))
    expect(within(drawer).getByRole('tab', { name: 'YAML' })).toHaveAttribute('aria-selected', 'true')
    expect(v.state.doc.toString()).toBe(docText('api-1'))
    expect(within(drawer).getByRole('button', { name: 'Edit' })).toBeDisabled() // editing already
    type(v, 'x: 1', 'x: 2')
    fireEvent.keyDown(v.contentDOM, { key: 'Enter', code: 'Enter', ctrlKey: true })
    const review = await screen.findByRole('dialog', { name: 'Edit api-1' })
    expect(f.client.prepareEdit).toHaveBeenCalledWith({ ref: expect.objectContaining({ uid: 'uid-web-api-1' }), base: 'base-api-1', original: docText('api-1'), edited: docText('api-1').replace('x: 1', 'x: 2') })
    f.client.runEdit = vi.fn(async () => ({ message: 'pod api-1: changes written' }))
    await userEvent.click(await within(review).findByRole('button', { name: 'Apply' }))
    await waitFor(() => expect(review).not.toBeInTheDocument())
    expect(screen.getByRole('status')).toHaveTextContent('pod api-1: changes written')
    expect(within(drawer).queryByLabelText('YAML editor')).not.toBeInTheDocument()
    // Nothing held: closing the details asks nothing.
    await userEvent.click(within(drawer).getByRole('button', { name: 'Close' }))
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(drawer).not.toBeInTheDocument()
  })

  it('Back to the text keeps the edits', async () => {
    const { drawer, v } = await openEditor()
    type(v, 'x: 1', 'x: 3')
    await userEvent.click(within(drawer).getByRole('button', { name: /Review changes/ }))
    const review = await screen.findByRole('dialog', { name: 'Edit api-1' })
    await userEvent.click(await within(review).findByRole('button', { name: 'Back to the text' }))
    expect(review).not.toBeInTheDocument()
    const again = await editorView(drawer)
    expect(again.state.doc.toString()).toContain('x: 3')
  })

  it('unsaved edits are never dropped silently: Esc, close, another row, tab or target ask first', async () => {
    const { f, grid, drawer, v } = await openEditor()
    // Unchanged: Esc just leaves the editor.
    await userEvent.keyboard('{Escape}')
    expect(within(drawer).queryByLabelText('YAML editor')).not.toBeInTheDocument()
    expect(drawer).toBeInTheDocument()
    await userEvent.click(within(drawer).getByRole('button', { name: 'Edit' }))
    const w = await editorView(drawer)
    expect(w).not.toBe(v)
    type(w, 'x: 1', 'x: 9')

    const asks = async (act: () => Promise<unknown> | void) => {
      await act()
      const prompt = await screen.findByRole('alertdialog', { name: 'Discard edits?' })
      await waitFor(() => expect(within(prompt).getByRole('button', { name: 'Keep editing' })).toHaveFocus())
      await userEvent.keyboard('{Escape}') // keep editing
      expect(prompt).not.toBeInTheDocument()
      expect(within(drawer).getByLabelText('YAML editor')).toBeInTheDocument()
      expect((await editorView(drawer)).state.doc.toString()).toContain('x: 9')
    }
    await asks(() => userEvent.keyboard('{Escape}'))
    await asks(() => userEvent.click(within(drawer).getByRole('button', { name: 'Close' })))
    await asks(() => userEvent.click(within(drawer).getByRole('tab', { name: 'Details' })))
    await asks(() => userEvent.click(within(grid).getByText('api-2')))
    expect(screen.getByRole('dialog', { name: 'pods api-1' })).toBeInTheDocument()
    await asks(() => userEvent.click(screen.getByRole('button', { name: /Nodes/ })))
    await asks(() => userEvent.click(screen.getByText('stage')))
    expect(f.client.selectTarget).not.toHaveBeenCalled()

    // Discard: the request goes on.
    await userEvent.click(screen.getByText('stage'))
    await userEvent.click(await screen.findByRole('button', { name: 'Discard' }))
    await waitFor(() => expect(f.client.selectTarget).toHaveBeenCalledWith('kubernetes', 'stage'))
  })

  it('says when the object changed on the server while its text is edited', async () => {
    const { f, drawer } = await openEditor()
    expect(within(drawer).queryByText(/changed on the server/)).not.toBeInTheDocument()
    f.state.rowsByKind['pods-api-1'] = [{ ...podRow('api-1', 'web'), rev: '2' }]
    await act(async () => f.emit({ type: 'view_changed', payload: { viewId: 'v-pods-api-1', version: 99 } }))
    expect(await within(drawer).findByText(/changed on the server/, undefined, { timeout: 3000 })).toBeInTheDocument()
    // The live update does not replace the text being edited.
    expect((await editorView(drawer)).state.doc.toString()).toBe(docText('api-1'))
  })
})
