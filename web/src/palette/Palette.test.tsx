import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import type { KindDescriptor, RecentObject } from '../api/types'
import { initialState, useStore } from '../store'
import { fakeClient, k8s, podRow, podsKind } from '../test/fakeClient'
import { usePalette } from './store'

beforeEach(() => {
  useStore.setState({ ...initialState })
  usePalette.setState({ open: false, host: null, rows: [], liveScopes: null })
})

const deployKind: KindDescriptor = { id: 'apps/deployments', title: 'Deployments', group: 'Workloads', scoped: true, aliases: ['deploy'], columns: [{ id: 'name', title: 'Name', type: 'text' }] }

function setup() {
  const f = fakeClient([k8s('prod'), k8s('dev')])
  f.state.view.groups[0].aliases = { scope: ['ns'], target: ['ctx'] }
  f.state.view.selected = { provider: 'kubernetes', id: 'prod' }
  f.client.listKinds = vi.fn(async () => [{ ...podsKind, aliases: ['po'] }, deployKind])
  f.state.kinds['apps/deployments'] = deployKind
  f.state.rows = [podRow('api-1', 'web'), podRow('db-0', 'web')]
  f.state.rowsByKind['apps/deployments'] = [
    { id: 'd1', ref: { provider: 'kubernetes', target: 'prod', scope: 'web', kind: 'apps/deployments', name: 'web' }, cells: [{ text: 'web' }], health: { state: 'ok' } },
    { id: 'd2', ref: { provider: 'kubernetes', target: 'prod', scope: 'web', kind: 'apps/deployments', name: 'batch' }, cells: [{ text: 'batch' }], health: { state: 'ok' } },
  ]
  return f
}

async function openApp(f: ReturnType<typeof fakeClient>) {
  render(<App client={f.client} />)
  const grid = await screen.findByRole('grid', { name: 'resources' })
  await within(grid).findByText('api-1')
  return grid
}

async function openPalette() {
  await userEvent.keyboard('{Control>}k{/Control}')
  return await screen.findByRole('dialog', { name: 'Go to' })
}

describe('Palette', () => {
  it('Ctrl+K opens it with the input focused; Esc closes and gives focus back', async () => {
    const f = setup()
    const grid = await openApp(f)
    const scroll = grid.closest('[data-table-scroll]') as HTMLElement | null
    ;(scroll ?? document.body).focus()
    const before = document.activeElement
    const dlg = await openPalette()
    expect(within(dlg).getByRole('combobox', { name: 'Go to' })).toHaveFocus()
    await userEvent.keyboard('{Escape}')
    expect(screen.queryByRole('dialog', { name: 'Go to' })).not.toBeInTheDocument()
    expect(document.activeElement).toBe(before)
  })

  it('finds a row of the current table and opens its details; the open is recorded as recent', async () => {
    const f = setup()
    await openApp(f)
    const dlg = await openPalette()
    await userEvent.keyboard('db-0')
    expect(within(dlg).getAllByRole('option')[0]).toHaveTextContent('db-0')
    await userEvent.keyboard('{Enter}')
    expect(await screen.findByRole('dialog', { name: 'pods db-0' })).toBeInTheDocument()
    await waitFor(() => expect(f.client.touchRecent).toHaveBeenCalledWith(expect.objectContaining({ kind: 'pods', name: 'db-0', uid: 'uid-web-db-0', provider: 'kubernetes', target: 'prod' }), 'db-0'))
  })

  it(':deploy web opens Deployments filtered by "web"', async () => {
    const f = setup()
    await openApp(f)
    await openPalette()
    await userEvent.keyboard(':deploy web{Enter}')
    expect(await screen.findByRole('heading', { name: 'Deployments' })).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Filter rows' })).toHaveValue('web')
    const grid = screen.getByRole('grid', { name: 'resources' })
    expect(await within(grid).findByText('web')).toBeInTheDocument()
    expect(within(grid).queryByText('batch')).not.toBeInTheDocument()
  })

  it(':ns switches the namespace; an ambiguous :ctx lists without choosing', async () => {
    const f = setup()
    await openApp(f)
    await openPalette()
    await userEvent.keyboard(':ns web{Enter}')
    await waitFor(() => expect(f.client.openView).toHaveBeenLastCalledWith('kubernetes', 'prod', { kind: 'pods', scope: { mode: 'one', name: 'web' } }))

    const dlg = await openPalette()
    await userEvent.keyboard(':ctx d')
    expect(within(dlg).getByRole('status')).toHaveTextContent('Several match')
    await userEvent.keyboard('{Enter}') // nothing chosen: nothing happens
    expect(screen.getByRole('dialog', { name: 'Go to' })).toBeInTheDocument()
    await userEvent.keyboard('{ArrowDown}{Enter}')
    await waitFor(() => expect(f.client.selectTarget).toHaveBeenCalledWith('kubernetes', 'dev'))
  })

  it('offers recent objects of the current target, and opens one from the overview', async () => {
    const f = setup()
    f.state.recents = [{ ref: { provider: 'kubernetes', target: 'prod', scope: 'web', kind: 'apps/deployments', name: 'old-web', uid: 'u-old' }, title: 'old-web', openedAt: 1 }]
    await openApp(f)
    await userEvent.click(screen.getByRole('button', { name: 'Overview' }))
    const dlg = await openPalette()
    const first = await within(dlg).findByRole('option', { name: /old-web/ })
    expect(first).toHaveTextContent('Recent')
    await userEvent.keyboard('{Enter}')
    expect(await screen.findByRole('heading', { name: 'Deployments' })).toBeInTheDocument()
    expect(await screen.findByRole('dialog', { name: 'apps/deployments old-web' })).toBeInTheDocument()
    expect(f.client.getResource).toHaveBeenLastCalledWith(expect.objectContaining({ name: 'old-web', uid: 'u-old' }))
  })

  it('drops recent objects that arrive after the target changed', async () => {
    const f = setup()
    const answers: ((v: RecentObject[]) => void)[] = []
    f.client.recentObjects = vi.fn(() => new Promise<RecentObject[]>((r) => answers.push(r)))
    await openApp(f)
    const dlg = await openPalette()
    act(() => usePalette.setState((s) => ({ host: s.host && { ...s.host, target: { provider: 'kubernetes', id: 'dev' } } })))
    await act(async () => answers[0]([{ ref: { provider: 'kubernetes', target: 'prod', kind: 'pods', name: 'stale' }, title: 'stale', openedAt: 1 }]))
    expect(within(dlg).queryByText('stale')).not.toBeInTheDocument()
  })

  it('a null scope list does not break the workspace or the palette', async () => {
    const f = setup()
    f.client.listScopes = vi.fn(async () => ({ scopes: null as unknown as [] }))
    await openApp(f)
    const dlg = await openPalette()
    expect(within(dlg).queryByText('Namespace')).not.toBeInTheDocument()
  })

  it('Ctrl+K in a terminal is the program\'s', async () => {
    const f = setup()
    await openApp(f)
    const term = document.createElement('div')
    term.setAttribute('data-terminal', '')
    term.tabIndex = 0
    document.body.appendChild(term)
    term.focus()
    await userEvent.keyboard('{Control>}k{/Control}')
    expect(screen.queryByRole('dialog', { name: 'Go to' })).not.toBeInTheDocument()
    term.remove()
  })

  it('the selection stays on its item when the list updates live', async () => {
    const f = setup()
    await openApp(f)
    const dlg = await openPalette()
    await userEvent.keyboard('-')
    expect(within(dlg).getAllByRole('option').slice(0, 2).map((o) => o.textContent)).toEqual(['Objectdb-0Pods · web', 'Objectapi-1Pods · web'])
    await userEvent.keyboard('{ArrowDown}')
    expect(within(dlg).getByRole('option', { selected: true })).toHaveTextContent('api-1')
    f.state.rows = [podRow('x-0', 'web'), podRow('api-1', 'web'), podRow('db-0', 'web')]
    await act(async () => f.emit({ type: 'view_changed', payload: { viewId: 'v-pods', version: 99 } }))
    await within(dlg).findByText('x-0')
    expect(within(dlg).getByRole('option', { selected: true })).toHaveTextContent('api-1')
  })
})
