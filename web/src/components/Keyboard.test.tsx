import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import type { KindDescriptor } from '../api/types'
import { useDock } from '../dock/store'
import { usePalette } from '../palette/store'
import { initialState, useStore } from '../store'
import { fakeClient, k8s, podRow, podsKind } from '../test/fakeClient'

beforeEach(() => {
  useStore.setState({ ...initialState })
  usePalette.setState({ open: false, host: null, rows: [], liveScopes: null })
  useDock.setState({ tabs: [], active: null })
})

const nodesKind: KindDescriptor = { id: 'nodes', title: 'Nodes', group: 'Cluster', scoped: false, columns: [{ id: 'name', title: 'Name', type: 'text' }] }

async function openProd(rows = 3) {
  const f = fakeClient([k8s('prod')])
  f.state.view.selected = { provider: 'kubernetes', id: 'prod' }
  f.client.listKinds = vi.fn(async () => [{ ...podsKind, logs: true }, nodesKind])
  f.state.rows = Array.from({ length: rows }, (_, i) => podRow(`p-${String(i).padStart(2, '0')}`, 'web'))
  render(<App client={f.client} />)
  const grid = await screen.findByRole('grid', { name: 'resources' })
  await within(grid).findByText('p-00')
  return { f, grid, scroll: grid.querySelector<HTMLElement>('[data-table-scroll]')! }
}

const selectedName = (grid: HTMLElement) => within(grid).getAllByRole('row').find((r) => r.getAttribute('aria-selected') === 'true')?.querySelector('[role=gridcell]')?.textContent

describe('keyboard', () => {
  it('F6 goes round contexts, views, the table and details; Shift+F6 goes back', async () => {
    const { scroll } = await openProd()
    await userEvent.keyboard('{F6}')
    expect(screen.getByRole('listbox', { name: 'targets' })).toHaveFocus()
    await userEvent.keyboard('{F6}')
    expect(screen.getByRole('button', { name: 'Pods' })).toHaveFocus() // the current view
    await userEvent.keyboard('{F6}')
    expect(scroll).toHaveFocus()
    await userEvent.keyboard('{ArrowDown}{Enter}')
    const drawer = await screen.findByRole('dialog', { name: 'pods p-00' })
    await userEvent.keyboard('{F6}')
    expect(drawer).toHaveFocus()
    await userEvent.keyboard('{F6}')
    expect(screen.getByRole('listbox', { name: 'targets' })).toHaveFocus()
    await userEvent.keyboard('{Shift>}{F6}{/Shift}')
    expect(drawer).toHaveFocus()
  })

  it('views: arrows move, Enter opens; one tab stop', async () => {
    await openProd()
    const nav = screen.getByRole('navigation', { name: 'resources' })
    expect(within(nav).getAllByRole('button').filter((b) => b.tabIndex === 0)).toEqual([within(nav).getByRole('button', { name: 'Pods' })])
    within(nav).getByRole('button', { name: 'Pods' }).focus()
    await userEvent.keyboard('{ArrowDown}')
    expect(within(nav).getByRole('button', { name: 'Nodes' })).toHaveFocus()
    await userEvent.keyboard('{Home}')
    expect(within(nav).getByRole('button', { name: 'Overview' })).toHaveFocus()
    await userEvent.keyboard('{End}{Enter}')
    expect(await screen.findByRole('heading', { name: 'Nodes' })).toBeInTheDocument()
  })

  it('table: PageDown/PageUp by a page, Home and End', async () => {
    const { grid, scroll } = await openProd(40)
    Object.defineProperty(scroll, 'clientHeight', { value: 10 * 28 }) // jsdom has no layout: ten rows fit
    scroll.focus()
    await userEvent.keyboard('{End}')
    expect(selectedName(grid)).toBe('p-39')
    await userEvent.keyboard('{Home}')
    expect(selectedName(grid)).toBe('p-00')
    await userEvent.keyboard('{PageDown}')
    expect(selectedName(grid)).toBe('p-09') // a page less one row kept for context
    await userEvent.keyboard('{PageDown}')
    expect(selectedName(grid)).toBe('p-18')
    await userEvent.keyboard('{PageUp}{PageUp}')
    expect(selectedName(grid)).toBe('p-00')
  })

  it('? lists the keys; Esc closes it and focus goes back', async () => {
    const { scroll } = await openProd()
    scroll.focus()
    await userEvent.keyboard('{Shift>}[Slash]{/Shift}') // "?" — or "," on a Russian layout: the same key
    const help = await screen.findByRole('dialog', { name: 'Keyboard' })
    expect(within(within(help).getByRole('region', { name: 'Anywhere' })).getByText('Ctrl+K')).toBeInTheDocument()
    expect(within(within(help).getByRole('region', { name: 'Details' })).getByText('Alt+←')).toBeInTheDocument()
    await userEvent.keyboard('{Escape}')
    expect(screen.queryByRole('dialog', { name: 'Keyboard' })).not.toBeInTheDocument()
    expect(scroll).toHaveFocus()
  })

  it('? in a text field is a character', async () => {
    await openProd()
    const filter = screen.getByRole('textbox', { name: 'Filter rows' })
    await userEvent.click(filter)
    await userEvent.keyboard('?')
    expect(filter).not.toHaveValue('')
    expect(screen.queryByRole('dialog', { name: 'Keyboard' })).not.toBeInTheDocument()
  })

  it('details: Alt+← and the path go back along relations; closing gives focus back to the table', async () => {
    const { scroll } = await openProd()
    scroll.focus()
    await userEvent.keyboard('{ArrowDown}{Enter}')
    let drawer = await screen.findByRole('dialog', { name: 'pods p-00' })
    await userEvent.click(await within(drawer).findByRole('button', { name: 'nodes/node-1' }))
    drawer = await screen.findByRole('dialog', { name: 'nodes node-1' })
    const path = within(drawer).getByRole('navigation', { name: 'Path' })
    expect(path).toHaveTextContent('p-00›node-1')
    await userEvent.keyboard('{Alt>}{ArrowLeft}{/Alt}')
    drawer = await screen.findByRole('dialog', { name: 'pods p-00' })
    await userEvent.click(await within(drawer).findByRole('button', { name: 'nodes/node-1' }))
    drawer = await screen.findByRole('dialog', { name: 'nodes node-1' })
    await userEvent.click(within(within(drawer).getByRole('navigation', { name: 'Path' })).getByRole('button', { name: 'p-00' }))
    expect(await screen.findByRole('dialog', { name: 'pods p-00' })).toBeInTheDocument()
    await userEvent.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'pods p-00' })).not.toBeInTheDocument())
    expect(scroll).toHaveFocus()
  })

  it('the bottom panel has a palette button (in a terminal Ctrl+K is the program\'s)', async () => {
    const { scroll } = await openProd()
    scroll.focus()
    await userEvent.keyboard('{ArrowDown}l')
    const panel = await screen.findByRole('region', { name: /panel/i })
    await userEvent.click(within(panel).getByRole('button', { name: 'Go to (Ctrl+K)' }))
    expect(await screen.findByRole('dialog', { name: 'Go to' })).toBeInTheDocument()
  })
})
