import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import type { KindDescriptor, Page, RecentObject } from '../api/types'
import { initialState, useStore } from '../store'
import { kindsView, connectedClient as fakeClient, k8s, podRow, podsKind } from '../test/fakeClient'
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
  f.client.listKinds = vi.fn(async () => kindsView([{ ...podsKind, aliases: ['po'] }, deployKind]))
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
    const scroll = grid.querySelector<HTMLElement>('[data-table-scroll]')!
    scroll.focus()
    const before = document.activeElement
    expect(before).toBe(scroll)
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
    expect(screen.getByRole('row', { selected: true })).toHaveAttribute('data-row-id', 'uid-web-db-0')
    await waitFor(() => expect(f.client.touchRecent).toHaveBeenCalledWith(expect.objectContaining({ kind: 'pods', name: 'db-0', uid: 'uid-web-db-0', provider: 'kubernetes', target: 'prod' }), 'db-0'))
  })

  it('waits for the exact object and selects its row id, not a same-name replacement', async () => {
    const f = setup()
    const wanted = podRow('late', 'web')
    wanted.id = 'problems#late-row'
    const replacement = { ...wanted, id: 'replacement', ref: { ...wanted.ref, uid: 'another-uid' } }
    f.state.rows = [podRow('api-1', 'web'), replacement]
    await openApp(f)
    act(() => usePalette.getState().host!.openObject(wanted.ref))
    await screen.findByRole('dialog', { name: 'pods late' })
    expect(screen.queryByRole('row', { selected: true })).toBeNull()
    f.state.rows = [wanted]
    await act(async () => f.emit({ type: 'view_changed', payload: { viewId: 'v-pods', version: 99 } }))
    await waitFor(() => expect(screen.getByRole('row', { selected: true })).toHaveAttribute('data-row-id', wanted.id))
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
    await waitFor(() => expect(f.client.openView).toHaveBeenLastCalledWith('kubernetes', 'prod', { kind: 'pods', scope: { mode: 'one', name: 'web' } }, expect.any(AbortSignal), expect.any(String), false, expect.any(String)))

    const dlg = await openPalette()
    await userEvent.keyboard(':ctx d')
    expect(within(dlg).getByRole('status')).toHaveTextContent('Nothing chosen')
    await userEvent.keyboard('{Enter}') // nothing chosen: nothing happens
    expect(screen.getByRole('dialog', { name: 'Go to' })).toBeInTheDocument()
    await userEvent.keyboard('{ArrowDown}{Enter}')
    await waitFor(() => expect(f.client.selectTarget).toHaveBeenCalledWith('kubernetes', 'dev'))
  })

  it('searches an explicit type and name in its own view, preserving UID and scope', async () => {
    const f = setup()
    f.state.recents = [{ ref: { provider: 'kubernetes', target: 'prod', scope: 'web', kind: 'apps/deployments', name: 'old-web', uid: 'u-old' }, title: 'old-web', openedAt: 1 }]
    f.client.getTargetState = vi.fn(async () => ({ scope: JSON.stringify({ mode: 'some', names: ['web', 'extra'] }) }))
    f.state.rowsByKind['apps/deployments'][0] = { ...f.state.rowsByKind['apps/deployments'][0], ref: f.state.recents[0].ref, cells: [{ text: 'old-web' }] }
    await openApp(f)
    const dlg = await openPalette()
    expect(within(dlg).queryByRole('option',{name:/old-web/})).toBeNull()
    await userEvent.keyboard('dep old-web')
    const first = await within(dlg).findByRole('option', { name: /old-web/ })
    expect(first).toHaveTextContent('Object')
    await userEvent.keyboard('{Enter}')
    expect(await screen.findByRole('heading', { name: 'Deployments' })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Deployments' })).toHaveAttribute('aria-current', 'page')
    expect(screen.getByRole('row', { selected: true })).toHaveAttribute('data-row-id', 'd1')
    expect(f.client.openView).toHaveBeenCalledWith('kubernetes', 'prod', { kind: 'apps/deployments', scope: { mode: 'some', names: ['extra', 'web'] } }, expect.any(AbortSignal), expect.any(String), false, expect.any(String))
    expect(await screen.findByRole('dialog', { name: 'apps/deployments old-web' })).toBeInTheDocument()
    expect(f.client.getResource).toHaveBeenLastCalledWith(expect.objectContaining({ name: 'old-web', uid: 'u-old' }))
  })

  it('recovers a Russian type/name query through one scoped view without rewriting the input', async () => {
    const f = setup()
    const wanted = f.state.rowsByKind['apps/deployments'][0]
    wanted.ref.uid = 'layout-web-uid'
    f.client.getTargetState = vi.fn(async () => ({ scope: JSON.stringify({ mode: 'one', name: 'web' }) }))
    await openApp(f)
    const dlg = await openPalette()
    const input = within(dlg).getByRole('combobox') as HTMLInputElement
    await userEvent.type(input, 'вуз цуи')
    await within(dlg).findByRole('option', { name: /ObjectwebDeployments/ })
    expect(input).toHaveValue('вуз цуи')
    expect(within(dlg).getByText('Keyboard layout: searching for “dep web”')).toBeInTheDocument()
    const reads = () => vi.mocked(f.client.openView).mock.calls.filter(call => call[2].kind === 'apps/deployments')
    expect(reads()).toHaveLength(1)
    expect(reads()[0]).toEqual(['kubernetes', 'prod', { kind: 'apps/deployments', scope: { mode: 'one', name: 'web' } }, expect.any(AbortSignal), expect.any(String), false, expect.any(String)])
    input.setSelectionRange(4, input.value.length)
    await userEvent.keyboard('ифеср')
    await within(dlg).findByRole('option', { name: /ObjectbatchDeployments/ })
    expect(reads()).toHaveLength(1)
    input.setSelectionRange(4, input.value.length)
    await userEvent.keyboard('цуи{Enter}')
    expect(await screen.findByRole('dialog', { name: 'apps/deployments web' })).toBeInTheDocument()
    expect(f.client.getResource).toHaveBeenLastCalledWith(expect.objectContaining({ name: 'web', uid: 'layout-web-uid' }))
  })

  it('waits for the original typed read before recovering its name and closes that view on query change', async () => {
    const f = setup()
    const wanted = f.state.rowsByKind['apps/deployments'][0]
    wanted.ref = { ...wanted.ref, name: 'model', uid: 'layout-model-uid' }
    wanted.cells = [{ text: 'model' }]
    const getRows = f.client.getRows
    let release!: () => void
    f.client.getRows = vi.fn((viewId: string, since: number) => viewId === 'v-apps/deployments' ? new Promise<Page>(resolve => { release = () => { void getRows(viewId, since).then(resolve) } }) : getRows(viewId, since))
    await openApp(f)
    const dlg = await openPalette()
    const input = within(dlg).getByRole('combobox')
    await userEvent.type(input, 'dep ьщвуд')
    await within(dlg).findByText('Loading…')
    expect(within(dlg).queryByText(/Keyboard layout:/)).toBeNull()
    await userEvent.keyboard('{Enter}')
    expect(dlg).toBeInTheDocument()
    await act(async () => release())
    await within(dlg).findByRole('option', { name: /ObjectmodelDeployments/ })
    expect(within(dlg).getByText('Keyboard layout: searching for “dep model”')).toBeInTheDocument()
    expect(vi.mocked(f.client.openView).mock.calls.filter(call => call[2].kind === 'apps/deployments')).toHaveLength(1)
    await userEvent.clear(input)
    await userEvent.type(input, 'nomatch')
    await waitFor(() => expect(f.client.closeView).toHaveBeenCalledWith('v-apps/deployments'))
  })

  it('keeps a real original-layout title ahead of the recovered Latin name', async () => {
    const f = setup()
    const original = podRow('original', 'web')
    original.ref.title = 'ьщвуд'
    f.state.rows.push(original, podRow('model', 'web'))
    await openApp(f)
    const dlg = await openPalette()
    await userEvent.keyboard('ьщвуд')
    expect(within(dlg).getAllByRole('option')).toHaveLength(1)
    expect(within(dlg).getByRole('option')).toHaveTextContent('ьщвуд')
    expect(within(dlg).queryByText(/Keyboard layout:/)).toBeNull()
  })

  it('does not treat a failed original typed read as an empty search', async () => {
    const f = setup()
    f.state.statusByKind['apps/deployments'] = { state: 'error', message: 'permission denied' }
    await openApp(f)
    const dlg = await openPalette()
    await userEvent.keyboard('dep ьщвуд')
    expect(await within(dlg).findByRole('alert')).toHaveTextContent('permission denied')
    expect(within(dlg).queryByText(/Keyboard layout:/)).toBeNull()
    expect(vi.mocked(f.client.openView).mock.calls.filter(call => call[2].kind === 'apps/deployments')).toHaveLength(1)
  })

  it('late history cannot add unavailable rows even when scope changes on an unscoped table', async () => {
    const f = setup()
    f.client.getTargetState = vi.fn(async () => ({ scope: JSON.stringify({ mode: 'one', name: 'web' }) }))
    const nodes: KindDescriptor = { ...podsKind, id: 'nodes', title: 'Nodes', scoped: false }
    f.client.listKinds = vi.fn(async () => kindsView([podsKind, deployKind, nodes]))
    let answer!: (v: RecentObject[]) => void
    f.client.recentObjects = vi.fn(() => new Promise<RecentObject[]>((r) => { answer = r }))
    await openApp(f)
    await userEvent.click(screen.getByRole('button', { name: 'Nodes' }))
    const dlg = await openPalette()
    act(() => usePalette.getState().host!.setScope({ mode: 'some', names: ['web', 'extra'] }))
    await act(async () => answer(['web', 'extra', 'outside'].map((scope) => ({ ref: { provider: 'kubernetes', target: 'prod', scope, kind: 'pods', name: 'match', uid: scope }, title: 'match', openedAt: 1 }))))
    await userEvent.keyboard('match')
    expect(within(dlg).queryAllByRole('option')).toHaveLength(0)
    expect(within(dlg).queryByRole('option', { name: /outside/ })).toBeNull()
    act(() => usePalette.getState().host!.setScope({ mode: 'some', names: [] }))
    expect(within(dlg).queryAllByRole('option')).toHaveLength(0)
    act(() => usePalette.getState().host!.setScope({ mode: 'all' }))
    expect(within(dlg).queryAllByRole('option')).toHaveLength(0)
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
