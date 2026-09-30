import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import type { KindDescriptor } from '../api/types'
import { useDock } from '../dock/store'
import { usePalette } from '../palette/store'
import { initialState, useStore } from '../store'
import { kindsView, fakeClient, k8s, podRow, podsKind } from '../test/fakeClient'

beforeEach(() => {
  useStore.setState({ ...initialState })
  usePalette.setState({ open: false, host: null, rows: [], liveScopes: null })
  useDock.setState({ tabs: [], active: null })
})

const nodesKind: KindDescriptor = { id: 'nodes', title: 'Nodes', group: 'Cluster', scoped: false, columns: [{ id: 'name', title: 'Name', type: 'text' }] }

async function openProd(rows = 3) {
  const f = fakeClient([k8s('prod')])
  f.state.view.selected = { provider: 'kubernetes', id: 'prod' }
  f.client.listKinds = vi.fn(async () => kindsView([{ ...podsKind, logs: true }, nodesKind]))
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
    // jsdom renders what fits its 800px box plus the overscan (38 rows of 30px).
    const { grid, scroll } = await openProd(38)
    Object.defineProperty(scroll, 'clientHeight', { value: 10 * 30 }) // jsdom has no layout: ten rows fit
    scroll.focus()
    await userEvent.keyboard('{End}')
    expect(selectedName(grid)).toBe('p-37')
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

  // Review 2026-09-30 (Codex, P5): Esc that CodeMirror consumed (closing its
  // search) also closed the whole drawer.
  it('Esc closing the YAML search leaves details open; the next Esc closes them', async () => {
    const { scroll } = await openProd(1)
    scroll.focus()
    await userEvent.keyboard('{ArrowDown}{Enter}')
    const drawer = await screen.findByRole('dialog', { name: 'pods p-00' })
    await userEvent.click(within(drawer).getByRole('tab', { name: 'YAML' }))
    await waitFor(() => expect(drawer.querySelector('.cm-content')).not.toBeNull())
    const content = drawer.querySelector<HTMLElement>('.cm-content')!
    content.focus()
    fireEvent.keyDown(content, { key: 'f', code: 'KeyF', ctrlKey: true, keyCode: 70 })
    await waitFor(() => expect(drawer.querySelector('.cm-search')).not.toBeNull())
    const field = drawer.querySelector<HTMLElement>('.cm-search input')!
    fireEvent.keyDown(field, { key: 'Escape', code: 'Escape', keyCode: 27 }) // from the search field
    await waitFor(() => expect(drawer.querySelector('.cm-search')).toBeNull())
    expect(screen.getByRole('dialog', { name: 'pods p-00' })).toBeInTheDocument()

    content.focus()
    fireEvent.keyDown(content, { key: 'f', code: 'KeyF', ctrlKey: true, keyCode: 70 })
    await waitFor(() => expect(drawer.querySelector('.cm-search')).not.toBeNull())
    content.focus()
    fireEvent.keyDown(content, { key: 'Escape', code: 'Escape', keyCode: 27 }) // from the editor, search open
    await waitFor(() => expect(drawer.querySelector('.cm-search')).toBeNull())
    expect(screen.getByRole('dialog', { name: 'pods p-00' })).toBeInTheDocument()

    fireEvent.keyDown(content, { key: 'Escape', code: 'Escape', keyCode: 27 }) // nothing of CodeMirror's open
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'pods p-00' })).not.toBeInTheDocument())
  })

  it('Esc clearing the table filter does not also close details', async () => {
    const { scroll } = await openProd(1)
    scroll.focus()
    await userEvent.keyboard('{ArrowDown}{Enter}')
    await screen.findByRole('dialog', { name: 'pods p-00' })
    const filter = screen.getByRole('textbox', { name: 'Filter rows' })
    await userEvent.type(filter, 'p')
    await userEvent.keyboard('{Escape}')
    expect(filter).toHaveValue('')
    expect(screen.getByRole('dialog', { name: 'pods p-00' })).toBeInTheDocument()
  })

  // Review 2026-09-30 (Codex, P5): an open menu owns the keyboard — F6,
  // Ctrl+K, ? and / used to act behind it.
  it('an open row menu keeps F6, Ctrl+K, ? and / to itself', async () => {
    const { grid } = await openProd(1)
    fireEvent.contextMenu(await within(grid).findByText('p-00'))
    const menu = screen.getByRole('menu', { name: 'Row actions' })
    expect(menu.contains(document.activeElement)).toBe(true)
    await userEvent.keyboard('{F6}')
    expect(menu.contains(document.activeElement)).toBe(true)
    await userEvent.keyboard('{Control>}k{/Control}')
    expect(screen.queryByRole('dialog', { name: 'Go to' })).not.toBeInTheDocument()
    await userEvent.keyboard('{Shift>}{Slash}{/Shift}/')
    expect(screen.queryByRole('dialog', { name: 'Keyboard' })).not.toBeInTheDocument()
    expect(menu.contains(document.activeElement)).toBe(true)
    await userEvent.keyboard('{Escape}')
    expect(screen.queryByRole('menu')).not.toBeInTheDocument()
    await userEvent.keyboard('{F6}')
    expect(screen.getByRole('listbox', { name: 'targets' })).toHaveFocus()
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
