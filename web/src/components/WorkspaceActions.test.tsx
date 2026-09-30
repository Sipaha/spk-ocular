import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import type { ActionDescriptor, ActionParams, KindDescriptor, Ref } from '../api/types'
import { initialState, useStore } from '../store'
import { kindsView, fakeClient, k8s, podRow, podsKind } from '../test/fakeClient'

beforeEach(() => useStore.setState({ ...initialState }))

const del: ActionDescriptor = { id: 'delete', title: 'Delete', destructive: true }
const restart: ActionDescriptor = { id: 'restart', title: 'Restart' }
const scale: ActionDescriptor = { id: 'scale', title: 'Scale', param: { kind: 'count', min: 0, max: 10 } }
const pods: KindDescriptor = { ...podsKind, logs: true, actions: [del] }
const nodes: KindDescriptor = { id: 'nodes', title: 'Nodes', group: 'Cluster', scoped: false, columns: [{ id: 'name', title: 'Name', type: 'text' }], actions: [restart, scale] }

async function openProd() {
  const f = fakeClient([k8s('prod')])
  f.state.view.selected = { provider: 'kubernetes', id: 'prod' }
  f.state.rows = [{ ...podRow('api-1', 'web'), rev: '1' }, { ...podRow('api-2', 'web'), rev: '1' }]
  f.client.listKinds = vi.fn(async () => kindsView([pods, nodes]))
  f.client.prepareAction = vi.fn(async (ref: Ref, action: string, params: ActionParams) => ({
    where: { provider: 'kubernetes', target: 'prod', targetTitle: 'prod', configRev: '1', ref: { ...ref, uid: ref.uid ?? 'pinned' } },
    action: [del, restart, scale].find((a) => a.id === action)!,
    params,
    destructive: action === 'delete',
    rights: { state: 'allowed' as const },
    expect: 'e',
    current: action === 'scale' ? 1 : undefined,
  }))
  render(<App client={f.client} />)
  const grid = await screen.findByRole('grid', { name: 'resources' })
  await within(grid).findByText('api-2')
  return { f, grid }
}

const body = (grid: HTMLElement) => grid.querySelector<HTMLElement>('[data-table-scroll]')!

describe('actions in the workspace', () => {
  it('the row menu acts on the row it was opened on, not the selected one', async () => {
    const { f, grid } = await openProd()
    await userEvent.click(within(grid).getByText('api-1')) // selected (and opened)
    await userEvent.click(screen.getByRole('button', { name: 'Close' }))
    fireEvent.contextMenu(within(grid).getByText('api-2'))
    const menu = screen.getByRole('menu', { name: 'Row actions' })
    expect(within(menu).getAllByRole('menuitem').map((m) => m.textContent)).toEqual(['Details', 'Logs', 'Delete'])
    expect(within(menu).getByRole('menuitem', { name: 'Delete' })).toHaveClass('text-danger')
    await userEvent.click(within(menu).getByRole('menuitem', { name: 'Delete' }))
    expect(f.client.prepareAction).toHaveBeenCalledWith(expect.objectContaining({ name: 'api-2', uid: 'uid-web-api-2' }), 'delete', {})
    const dialog = await screen.findByRole('dialog', { name: 'Delete api-2' })
    expect(dialog).toHaveTextContent('Pods')
  })

  it('Delete on the table: the selected row; not in the filter', async () => {
    const { f, grid } = await openProd()
    const filter = screen.getByRole('textbox', { name: 'Filter rows' })
    await userEvent.click(filter)
    await userEvent.keyboard('{Delete}')
    expect(f.client.prepareAction).not.toHaveBeenCalled()

    body(grid).focus()
    await userEvent.keyboard('{ArrowDown}{ArrowDown}')
    await userEvent.keyboard('{Delete}')
    const dialog = await screen.findByRole('dialog', { name: 'Delete api-2' })
    // Delete in the dialog does not start another one.
    await userEvent.keyboard('{Delete}')
    expect(f.client.prepareAction).toHaveBeenCalledTimes(1)
    await userEvent.keyboard('{Escape}')
    expect(dialog).not.toBeInTheDocument()
    await waitFor(() => expect(body(grid)).toHaveFocus())
  })

  it('Shift+F10 opens the row menu; a held Enter from it does not confirm', async () => {
    const { f, grid } = await openProd()
    body(grid).focus()
    await userEvent.keyboard('{ArrowDown}')
    await userEvent.keyboard('{Shift>}{F10}{/Shift}')
    const menu = screen.getByRole('menu', { name: 'Row actions' })
    expect(within(menu).getByRole('menuitem', { name: 'Details' })).toHaveFocus()
    await userEvent.keyboard('{ArrowDown}{ArrowDown}')
    expect(within(menu).getByRole('menuitem', { name: 'Delete' })).toHaveFocus()
    // Enter held: the first press chooses, the repeats reach the dialog.
    await userEvent.keyboard('{Enter}')
    const dialog = await screen.findByRole('dialog', { name: 'Delete api-1' })
    await waitFor(() => expect(within(dialog).getByRole('button', { name: 'Cancel' })).toHaveFocus())
    for (let i = 0; i < 3; i++) fireEvent.keyDown(document.activeElement!, { key: 'Enter', repeat: true })
    expect(f.client.runAction).not.toHaveBeenCalled()
    expect(dialog).toBeInTheDocument()
  })

  it('the drawer menu acts on the object shown now (after following a relation)', async () => {
    const { f, grid } = await openProd()
    await userEvent.click(within(grid).getByText('api-1'))
    const drawer = await screen.findByRole('dialog', { name: 'pods api-1' })
    await userEvent.click(within(drawer).getByRole('button', { name: /Actions/ }))
    expect(within(screen.getByRole('menu', { name: 'Actions' })).getAllByRole('menuitem').map((m) => m.textContent)).toEqual(['Delete'])
    await userEvent.keyboard('{Escape}')
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
    expect(screen.getByRole('dialog', { name: 'pods api-1' })).toBeInTheDocument() // Esc closed only the menu

    await userEvent.click(await within(drawer).findByRole('button', { name: 'nodes/node-1' }))
    await screen.findByRole('dialog', { name: 'nodes node-1' })
    await userEvent.click(within(drawer).getByRole('button', { name: /Actions/ }))
    const menu = screen.getByRole('menu', { name: 'Actions' })
    expect(within(menu).getAllByRole('menuitem').map((m) => m.textContent)).toEqual(['Restart', 'Scale…'])
    await userEvent.click(within(menu).getByRole('menuitem', { name: 'Scale…' }))
    expect(f.client.prepareAction).toHaveBeenCalledWith(expect.objectContaining({ kind: 'nodes', name: 'node-1' }), 'scale', {})
    const dialog = await screen.findByRole('dialog', { name: 'Scale node-1' })
    expect(dialog).toHaveTextContent('Nodes')
    await waitFor(() => expect(within(dialog).getByRole('textbox')).toHaveValue('1'))
  })

  it('a done action says so in the status bar', async () => {
    const { f, grid } = await openProd()
    f.client.runAction = vi.fn(async () => ({ message: 'pod api-2: deletion requested' }))
    fireEvent.contextMenu(within(grid).getByText('api-2'))
    await userEvent.click(within(screen.getByRole('menu')).getByRole('menuitem', { name: 'Delete' }))
    const dialog = await screen.findByRole('dialog', { name: 'Delete api-2' })
    await userEvent.click(await within(dialog).findByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(dialog).not.toBeInTheDocument())
    expect(screen.getByRole('status')).toHaveTextContent('pod api-2: deletion requested')
  })

  it('a deleted object in the drawer is said so, not an error', async () => {
    const { f, grid } = await openProd()
    // The drawer's view of its one object (by name) is a view of its own.
    const open = f.client.openView
    f.client.openView = vi.fn(async (p: string, t: string, q: Parameters<typeof open>[2]) =>
      q.name ? { viewId: `v-pods-${q.name}`, kind: pods } : open(p, t, q),
    )
    f.state.rowsByKind['pods-api-1'] = [{ ...podRow('api-1', 'web'), rev: '1' }]
    await userEvent.click(within(grid).getByText('api-1'))
    await screen.findByRole('dialog', { name: 'pods api-1' })
    // The details have followed the object's revision (debounced) once.
    const first = vi.mocked(f.client.getResource)
    await vi.waitFor(() => expect(first.mock.calls.length).toBe(2), { timeout: 2000 })
    const { ApiError } = await import('../api/client')
    f.client.getResource = vi.fn(async () => {
      throw new ApiError('not_found', 'pods "api-1" not found')
    })
    f.state.rowsByKind['pods-api-1'] = []
    await act(async () => f.emit({ type: 'view_changed', payload: { viewId: 'v-pods-api-1', version: 99 } }))
    expect(await screen.findByText('This object no longer exists.', undefined, { timeout: 3000 })).toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /Actions/ })).not.toBeInTheDocument() // nothing to act on
  })

})
