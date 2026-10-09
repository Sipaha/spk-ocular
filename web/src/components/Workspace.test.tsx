import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import { initialState, targetKey, useStore } from '../store'
import { remember } from './pageMemo'
import { kindsView, connectedClient as fakeClient, k8s, podRow, podsKind, scopeRow } from '../test/fakeClient'

beforeEach(() => useStore.setState({ ...initialState }))

async function pickScope(name: string) {
  await userEvent.click(screen.getByRole('button', { name: 'Namespace' }))
  await userEvent.keyboard(name + '{Enter}')
}

async function openProd(f: ReturnType<typeof fakeClient>) {
  f.state.view.selected = { provider: 'kubernetes', id: 'prod' }
  render(<App client={f.client} />)
  return await screen.findByRole('grid', { name: 'resources' })
}

describe('Workspace', () => {
  it.each(['disk', 'memory'])('a removed Overview selection from %s opens the default kind with its scope', async (source) => {
    const f = fakeClient([k8s('prod')])
    const ui = { kind: '__overview', scope: { mode: 'one' as const, name: 'web' } }
    if (source === 'memory') remember(targetKey({ provider: 'kubernetes', id: 'prod' }), { ui })
    else vi.mocked(f.client.getTargetState).mockResolvedValue({ kind: JSON.stringify(ui.kind), scope: JSON.stringify(ui.scope) })
    await openProd(f)
    expect(await screen.findByRole('heading', { name: 'Pods' })).toBeInTheDocument()
    expect(f.client.openView).toHaveBeenCalledWith('kubernetes', 'prod', { kind: 'pods', scope: ui.scope })
    expect(screen.queryByRole('button', { name: 'Overview' })).not.toBeInTheDocument()
    expect(screen.queryByRole('dialog', { name: 'Connection information' })).not.toBeInTheDocument()
  })

  it('shows loading through target state, opening the view and its initial snapshot', async () => {
    const f = fakeClient([k8s('prod')])
    f.state.view.selected = { provider: 'kubernetes', id: 'prod' }
    let stateReady!: (value: Record<string, string>) => void
    const state = new Promise<Record<string, string>>((resolve) => { stateReady = resolve })
    f.client.getTargetState = vi.fn(() => state)
    let viewReady!: (value: { viewId: string; kind: typeof podsKind }) => void
    f.client.openView = vi.fn(() => new Promise<{ viewId: string; kind: typeof podsKind }>((resolve) => { viewReady = resolve }))
    f.client.getRows = vi.fn(async () => ({ viewId: 'delayed', version: 1, reset: true, upserts: [], deleted: [], status: { state: 'loading' as const } }))
    render(<App client={f.client} />)
    await screen.findByRole('navigation', { name: 'resources' })
    expect(screen.getByRole('status', { name: 'Loading…' })).toBeVisible()
    await act(async () => stateReady({}))
    expect(screen.getByRole('status', { name: 'Loading…' })).toBeVisible()
    await act(async () => viewReady({ viewId: 'delayed', kind: podsKind }))
    expect(screen.getByRole('status', { name: 'Loading…' })).toBeVisible()
    expect(screen.queryByText('No objects')).toBeNull()
    f.client.getRows = vi.fn(async () => ({ viewId: 'delayed', version: 2, reset: true, upserts: [], deleted: [], status: { state: 'ready' as const } }))
    await act(async () => f.emit({ type: 'view_changed', payload: { viewId: 'delayed', version: 2 } }))
    const empty = await screen.findByText('No objects')
    expect(empty.closest('[data-table-scroll]')).not.toBeNull()
    expect(screen.queryByRole('status', { name: 'Loading…' })).toBeNull()
  })

  it('shows live rows of the default namespace with health', async () => {
    const f = fakeClient([k8s('prod', { defaultScope: 'web' })])
    f.state.rows = [podRow('api-1', 'web'), podRow('api-2', 'web', 'CrashLoopBackOff', { state: 'error', reason: 'CrashLoopBackOff' })]
    const grid = await openProd(f)
    expect(await within(grid).findByText('api-1')).toBeInTheDocument()
    expect(within(grid).getByText('CrashLoopBackOff')).toHaveClass('text-danger')
    expect(f.client.openView).toHaveBeenCalledWith('kubernetes', 'prod', { kind: 'pods', scope: { mode: 'one', name: 'web' } })
    expect(within(grid).queryByText('Namespace')).not.toBeInTheDocument() // one namespace: no namespace column
    expect(screen.getByRole('button', { name: 'Namespace' })).toHaveTextContent('web')
  })

  it('keeps table sorting when a later refresh is loading without rows', async () => {
    const f = fakeClient([k8s('prod')])
    f.state.rows = [podRow('api-1', 'web'), podRow('db-0', 'web')]
    await openProd(f)
    const name = screen.getByRole('columnheader', { name: /^Name\b/ })
    fireEvent.click(name)
    expect(name).toHaveAttribute('aria-sort', 'descending')

    f.state.rows = []
    f.state.statusByKind.pods = { state: 'loading' }
    await act(async () => f.emit({ type: 'view_changed', payload: { viewId: 'v-pods', version: f.state.version + 1 } }))
    expect(screen.getByRole('grid', { name: 'resources' })).toBeVisible()
    expect(screen.getByRole('columnheader', { name: /^Name\b/ })).toHaveAttribute('aria-sort', 'descending')

    f.state.rows = [podRow('api-1', 'web'), podRow('db-0', 'web')]
    f.state.statusByKind.pods = { state: 'ready' }
    await act(async () => f.emit({ type: 'view_changed', payload: { viewId: 'v-pods', version: f.state.version + 1 } }))
    expect(screen.getByRole('columnheader', { name: /^Name\b/ })).toHaveAttribute('aria-sort', 'descending')
    expect(screen.queryByRole('status', { name: 'Loading…' })).not.toBeInTheDocument()
  })

  it('switching to all namespaces reopens the view and shows the namespace column', async () => {
    const f = fakeClient([k8s('prod', { defaultScope: 'web' })])
    await openProd(f)
    await userEvent.click(screen.getByRole('button', { name: 'Namespace' }))
    await userEvent.click(screen.getByRole('option', { name: 'All namespaces' }))
    expect(f.client.openView).toHaveBeenLastCalledWith('kubernetes', 'prod', { kind: 'pods', scope: { mode: 'all' } })
    expect(f.client.closeView).toHaveBeenCalled()
    // a new scope is a new page (and a new grid)
    expect(within(await screen.findByRole('grid', { name: 'resources' })).getByText('Namespace')).toBeInTheDocument()
  })

  it('filters rows with "/" and pulls again on view_changed', async () => {
    const f = fakeClient([k8s('prod')])
    f.state.rows = [podRow('api-1', 'web'), podRow('db-0', 'data')]
    const grid = await openProd(f)
    await within(grid).findByText('db-0')
    await userEvent.keyboard('[Slash]')
    expect(screen.getByRole('textbox', { name: 'Filter rows' })).toHaveFocus()
    await userEvent.keyboard('db')
    expect(within(grid).queryByText('api-1')).not.toBeInTheDocument()
    expect(within(grid).getByText('db-0')).toBeInTheDocument()

    f.state.rows = [podRow('db-0', 'data'), podRow('db-1', 'data')]
    await act(async () => f.emit({ type: 'view_changed', payload: { viewId: 'v-pods', version: 99 } }))
    expect(await within(grid).findByText('db-1')).toBeInTheDocument()
  })

  it('a forbidden view is an explained error, not an empty table', async () => {
    const f = fakeClient([k8s('prod')])
    f.client.getRows = vi.fn(async () => ({
      viewId: 'v1', version: 2, reset: true, upserts: [], deleted: [],
      status: { state: 'error' as const, class: 'forbidden', message: 'pods is forbidden: User "dev" cannot list pods' },
    }))
    await openProd(f)
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('Cannot show · access denied')
    expect(alert).toHaveTextContent('cannot list pods')
    expect(screen.queryByText('No objects')).not.toBeInTheDocument()
  })

  it('the namespace picker searches: the first found is marked, arrows move, Enter chooses', async () => {
    const f = fakeClient([k8s('prod', { defaultScope: 'web' })])
    f.client.listScopes = vi.fn(async () => ({ scopes: ['default', 'kube-system', 'team-a', 'team-b', 'web'].map((name) => ({ name })) }))
    await openProd(f)
    const button = screen.getByRole('button', { name: 'Namespace' })
    await waitFor(() => expect(f.client.listScopes).toHaveBeenCalled())
    await userEvent.click(button)
    const search = screen.getByRole('combobox', { name: 'Find a namespace' })
    expect(search).toHaveFocus()
    // Unsearched: the current one is marked, "all" is on top.
    const list = screen.getByRole('listbox', { name: 'Namespace' })
    expect(within(list).getAllByRole('option')[0]).toHaveTextContent('All namespaces')
    expect(within(list).getByRole('option', { selected: true })).toHaveTextContent('web')
    expect(within(list).getByRole('option', { name: 'web' })).toBeInTheDocument() // the ✓ is not its name
    await userEvent.keyboard('team')
    expect(within(list).getAllByRole('option').map((o) => o.textContent)).toEqual(['team-a', 'team-b'])
    expect(document.getElementById(search.getAttribute('aria-activedescendant')!)).toHaveTextContent('team-a')
    await userEvent.keyboard('{ArrowDown}')
    expect(document.getElementById(search.getAttribute('aria-activedescendant')!)).toHaveTextContent('team-b')
    await userEvent.keyboard('{ArrowDown}') // wraps
    expect(document.getElementById(search.getAttribute('aria-activedescendant')!)).toHaveTextContent('team-a')
    await userEvent.keyboard('{ArrowUp}{Enter}')
    expect(f.client.openView).toHaveBeenLastCalledWith('kubernetes', 'prod', { kind: 'pods', scope: { mode: 'one', name: 'team-b' } })
    expect(screen.queryByRole('listbox', { name: 'Namespace' })).not.toBeInTheDocument()
    // The page is new: the keyboard goes on in its table.
    await waitFor(() => expect(document.querySelector('[data-table-scroll]')).toHaveFocus())
    expect(screen.getByRole('button', { name: 'Namespace' })).toHaveTextContent('team-b')

    // Esc closes without a change, and nothing else (no drawer, no shortcut) sees it.
    await userEvent.click(screen.getByRole('button', { name: 'Namespace' }))
    await userEvent.keyboard('kube{Escape}')
    expect(screen.queryByRole('listbox', { name: 'Namespace' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Namespace' })).toHaveFocus()
    expect(f.client.openView).toHaveBeenLastCalledWith('kubernetes', 'prod', { kind: 'pods', scope: { mode: 'one', name: 'team-b' } })
    // Nothing found: Enter does nothing.
    await userEvent.click(screen.getByRole('button', { name: 'Namespace' }))
    await userEvent.keyboard('zzz')
    expect(within(screen.getByRole('listbox', { name: 'Namespace' })).getByRole('status')).toHaveTextContent('Nothing found')
    await userEvent.keyboard('{Enter}')
    expect(screen.getByRole('listbox', { name: 'Namespace' })).toBeInTheDocument()
    // A click elsewhere closes it.
    await userEvent.click(screen.getByRole('grid', { name: 'resources' }))
    expect(screen.queryByRole('listbox', { name: 'Namespace' })).not.toBeInTheDocument()
  })

  it('lets the user type a namespace when listing them is forbidden', async () => {
    const f = fakeClient([k8s('prod')])
    f.client.listScopes = vi.fn(async () => ({ scopes: [], error: { code: 'forbidden', detail: 'namespaces is forbidden' } }))
    await openProd(f)
    const input = await screen.findByRole('textbox', { name: 'Namespace' })
    await userEvent.type(input, 'team-a{Enter}')
    expect(f.client.openView).toHaveBeenLastCalledWith('kubernetes', 'prod', { kind: 'pods', scope: { mode: 'one', name: 'team-a' } })
  })
})

describe('Workspace details and state', () => {
  it('opens a row in the drawer, follows a relation and goes back', async () => {
    const f = fakeClient([k8s('prod')])
    f.state.rows = [podRow('api-1', 'web')]
    const grid = await openProd(f)
    await userEvent.click(await within(grid).findByText('api-1'))
    const drawer = await screen.findByRole('dialog', { name: 'pods api-1' })
    expect(await within(drawer).findByText('node-1', { selector: 'dd' })).toBeInTheDocument()
    await userEvent.click(within(drawer).getByRole('button', { name: 'nodes/node-1' }))
    expect(await screen.findByRole('dialog', { name: 'nodes node-1' })).toBeInTheDocument()
    expect(f.client.getResource).toHaveBeenLastCalledWith(expect.objectContaining({ kind: 'nodes', name: 'node-1', target: 'prod' }))
    await userEvent.click(screen.getByRole('button', { name: 'Back' }))
    expect(await screen.findByRole('dialog', { name: 'pods api-1' })).toBeInTheDocument()
    await userEvent.keyboard('{Escape}')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('a relation that cannot be pinned down is named, not a link', async () => {
    const f = fakeClient([k8s('prod')])
    f.state.rows = [podRow('api-1', 'web')]
    f.client.getResource = vi.fn(async (ref) => ({
      ref, health: { state: 'ok' as const }, yaml: '', facts: [],
      relations: [
        { type: 'about', ref: { provider: 'kubernetes', target: 'prod', kind: 'pods', name: 'api-0', uid: 'u0' } },
        { type: 'about', ref: { provider: 'kubernetes', target: 'prod', kind: 'job', name: 'backup' }, inert: true },
      ],
    }))
    const grid = await openProd(f)
    await userEvent.click(await within(grid).findByText('api-1'))
    const drawer = await screen.findByRole('dialog', { name: 'pods api-1' })
    expect(await within(drawer).findByRole('button', { name: 'pods/api-0' })).toBeInTheDocument()
    expect(within(drawer).getByText('backup').closest('[title]')).toHaveAttribute('title', 'job/backup · Cannot be opened here')
    expect(within(drawer).queryByRole('button', { name: 'job/backup' })).not.toBeInTheDocument()
    expect(within(drawer).getByText('About')).toBeInTheDocument()
  })

  it('shows metrics in CPU/Memory columns by row id', async () => {
    const f = fakeClient([k8s('prod')])
    const withMetrics = { ...podsKindWithMetrics }
    f.client.listKinds = vi.fn(async () => kindsView([withMetrics]))
    f.client.openView = vi.fn(async () => ({ viewId: 'v-pods', kind: withMetrics }))
    f.state.rows = [podRow('api-1', 'web')]
    f.client.getMetrics = vi.fn(async () => ({ status: 'ok', values: { 'uid-web-api-1': { cpu: 0.25, memory: 64 * 1024 * 1024 } } }))
    const grid = await openProd(f)
    expect(await within(grid).findByText('250m')).toBeInTheDocument()
    expect(within(grid).getByText('64Mi')).toBeInTheDocument()
    // asked for the rows in view, abortable
    expect(f.client.getMetrics).toHaveBeenCalledWith('v-pods', ['uid-web-api-1'], expect.any(AbortSignal))
  })

  it('says when metrics cover only the first rows', async () => {
    const f = fakeClient([k8s('prod')])
    const withMetrics = { ...podsKindWithMetrics }
    f.client.listKinds = vi.fn(async () => kindsView([withMetrics]))
    f.client.openView = vi.fn(async () => ({ viewId: 'v-pods', kind: withMetrics }))
    f.state.rows = [podRow('api-1', 'web')]
    f.client.getMetrics = vi.fn(async () => ({ status: 'ok', limit: 100, values: { 'uid-web-api-1': { cpu: 0.25 } } }))
    await openProd(f)
    expect(await screen.findByRole('note', { name: 'CPU/Memory' })).toHaveTextContent('first 100')
  })

  it('explains empty metric columns', async () => {
    const f = fakeClient([k8s('prod')])
    const withMetrics = { ...podsKindWithMetrics }
    f.client.listKinds = vi.fn(async () => kindsView([withMetrics]))
    f.client.openView = vi.fn(async () => ({ viewId: 'v-pods', kind: withMetrics }))
    f.state.rows = [podRow('api-1', 'web')]
    f.client.getMetrics = vi.fn(async () => ({ status: 'forbidden', message: 'pods.metrics.k8s.io is forbidden', values: {} }))
    await openProd(f)
    const note = await screen.findByRole('note', { name: 'CPU/Memory' })
    expect(note).toHaveTextContent('access denied')
    expect(note).toHaveAttribute('title', 'pods.metrics.k8s.io is forbidden')
  })

  it('restores the last kind and scope of the target', async () => {
    const f = fakeClient([k8s('prod')])
    f.client.getTargetState = vi.fn(async () => ({ kind: '"pods"', scope: '{"mode":"one","name":"data"}' }))
    await openProd(f)
    expect(f.client.openView).toHaveBeenCalledTimes(1)
    expect(f.client.openView).toHaveBeenCalledWith('kubernetes', 'prod', { kind: 'pods', scope: { mode: 'one', name: 'data' } })
    await pickScope('web')
    expect(f.client.setTargetState).toHaveBeenCalledWith('kubernetes', 'prod', 'scope', '{"mode":"one","name":"web"}')
  })
})

const podsKindWithMetrics = {
  id: 'pods',
  title: 'Pods',
  group: 'Workloads',
  scoped: true,
  columns: [
    { id: 'name', title: 'Name', type: 'text' as const },
    { id: 'namespace', title: 'Namespace', type: 'text' as const, scopeColumn: true },
    { id: 'ready', title: 'Ready', type: 'ratio' as const },
    { id: 'status', title: 'Status', type: 'status' as const },
    { id: 'restarts', title: 'Restarts', type: 'number' as const },
    { id: 'age', title: 'Age', type: 'age' as const },
    { id: 'cpu', title: 'CPU', type: 'cpu' as const, metric: true },
    { id: 'memory', title: 'Memory', type: 'bytes' as const, metric: true },
  ],
}

describe('live scopes', () => {
  it('keeps the namespace list live through the provider scope kind', async () => {
    const f = fakeClient([k8s('prod')])
    f.client.listScopes = vi.fn(async () => ({ scopes: [{ name: 'default' }], kind: 'namespaces' }))
    f.state.rowsByKind.namespaces = [scopeRow('default'), scopeRow('web')]
    await openProd(f)
    await userEvent.click(screen.getByRole('button', { name: 'Namespace' }))
    const list = screen.getByRole('listbox', { name: 'Namespace' })
    expect(await within(list).findByRole('option', { name: 'web' })).toBeInTheDocument()
    f.state.rowsByKind.namespaces = [scopeRow('default'), scopeRow('web'), scopeRow('new-team')]
    await act(async () => f.emit({ type: 'view_changed', payload: { viewId: 'v-namespaces', version: 99 } }))
    expect(await within(list).findByRole('option', { name: 'new-team' })).toBeInTheDocument()
    expect(f.client.openView).toHaveBeenCalledWith('kubernetes', 'prod', { kind: 'namespaces', scope: { mode: 'none' } })
  })
})

describe('drawer follows its object', () => {
  it('refetches details when the object revision changes, even if the table row does not', async () => {
    const f = fakeClient([k8s('prod')])
    const cm = { ...podRow('api-1', 'web'), rev: '1' }
    f.state.rows = [cm]
    f.state.rowsByKind.pods = [cm]
    const grid = await openProd(f)
    await userEvent.click(await within(grid).findByText('api-1'))
    await screen.findByRole('dialog', { name: 'pods api-1' })
    const calls = (f.client.getResource as ReturnType<typeof vi.fn>).mock.calls.length
    // Only the revision changes (e.g. a ConfigMap value the table does not show).
    f.state.rowsByKind.pods = [{ ...cm, rev: '2' }]
    f.state.rows = f.state.rowsByKind.pods
    await act(async () => f.emit({ type: 'view_changed', payload: { viewId: 'v-pods', version: 999 } }))
    await vi.waitFor(() => expect((f.client.getResource as ReturnType<typeof vi.fn>).mock.calls.length).toBeGreaterThan(calls), { timeout: 2000 })
    expect(f.client.openView).toHaveBeenCalledWith('kubernetes', 'prod', expect.objectContaining({ kind: 'pods', name: 'api-1' }))
  })
})

describe('Workspace logs', () => {
  it('opens logs from the drawer and with L on a row, one tab per object', async () => {
    const f = fakeClient([k8s('prod')])
    f.client.listKinds = vi.fn(async () => kindsView([{ ...podsKind, logs: true }]))
    f.state.rows = [podRow('api-1', 'web'), podRow('api-2', 'web')]
    // streams are not the point here: a body that never sends anything
    vi.stubGlobal('fetch', vi.fn(async () => new Response(new ReadableStream())))
    const grid = await openProd(f)
    const user = userEvent.setup()
    await user.click(await within(grid).findByText('api-1'))
    await user.keyboard('{Enter}')
    await user.click(await screen.findByRole('button', { name: 'Logs' }))
    expect(await screen.findByRole('tab', { name: /pods\/api-1/ })).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: 'Close' })) // the drawer
    await user.click(within(grid).getByText('api-2'))
    await user.keyboard('l')
    expect(await screen.findByRole('tab', { name: /pods\/api-2/ })).toHaveAttribute('aria-selected', 'true')
    await user.click(within(grid).getByText('api-1'))
    await user.keyboard('l') // again: the existing tab, not a second one
    expect(screen.getAllByRole('tab', { name: /pods\/api-1/ })).toHaveLength(1)
    await waitFor(() => expect(f.client.openLogStream).toHaveBeenCalledTimes(2))

    await user.click(within(screen.getByRole('tab', { name: /pods\/api-1/ })).getByRole('button', { name: 'Close tab' }))
    expect(screen.queryByRole('tab', { name: /pods\/api-1/ })).not.toBeInTheDocument()
    vi.unstubAllGlobals()
  })
})

describe('switching targets (P18)', () => {
  const nameHead = (grid: HTMLElement) => within(grid).getByRole('columnheader', { name: /^Name[^a-z]*$/ })
  const rowOf = (grid: HTMLElement, name: string) => within(grid).getByText(name).closest('[role="row"]') as HTMLElement

  it('back to a target: its page as left — filter, sort, cursor, details and their tab; marks are not kept', async () => {
    const f = fakeClient([k8s('prod'), k8s('stage')])
    f.state.rows = [podRow('api-1', 'web'), podRow('api-2', 'web'), podRow('db-0', 'data')]
    let grid = await openProd(f)
    await within(grid).findByText('db-0')
    await userEvent.click(nameHead(grid))
    const sorted = nameHead(grid).getAttribute('aria-sort')
    await userEvent.keyboard('[Slash]')
    await userEvent.keyboard('api')
    await userEvent.click(within(rowOf(grid, 'api-1')).getByRole('checkbox'))
    await userEvent.click(within(grid).getByText('api-2'))
    const drawer = await screen.findByRole('dialog', { name: 'pods api-2' })
    await userEvent.click(within(drawer).getByRole('tab', { name: 'YAML' }))

    await userEvent.click(screen.getByRole('option', { name: /stage/ }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'pods api-2' })).toBeNull())
    expect(screen.getByRole('textbox', { name: 'Filter rows' })).toHaveValue('')
    await userEvent.click(screen.getByRole('option', { name: /prod/ }))

    grid = await screen.findByRole('grid', { name: 'resources' })
    await within(grid).findByText('api-2')
    expect(screen.getByRole('textbox', { name: 'Filter rows' })).toHaveValue('api')
    expect(within(grid).queryByText('db-0')).toBeNull()
    expect(nameHead(grid)).toHaveAttribute('aria-sort', sorted!)
    expect(rowOf(grid, 'api-2')).toHaveAttribute('aria-selected', 'true')
    expect(rowOf(grid, 'api-1')).not.toHaveAttribute('data-marked')
    const again = await screen.findByRole('dialog', { name: 'pods api-2' })
    expect(within(again).getByRole('tab', { name: 'YAML' })).toHaveAttribute('aria-selected', 'true')
  })

  it('a kind keeps its sort while another kind is shown; a new page of the target starts clean', async () => {
    const f = fakeClient([k8s('prod')])
    f.state.rows = [podRow('api-1', 'web'), podRow('db-0', 'data')]
    f.client.listKinds = vi.fn(async () => kindsView([podsKind, { id: 'services', title: 'Services', group: 'Network', scoped: true, columns: [{ id: 'name', title: 'Name', type: 'text' as const }] }]))
    let grid = await openProd(f)
    await within(grid).findByText('db-0')
    await userEvent.click(nameHead(grid))
    const sorted = nameHead(grid).getAttribute('aria-sort')
    await userEvent.keyboard('[Slash]')
    await userEvent.keyboard('db')
    await userEvent.click(screen.getByRole('button', { name: /^Network \(/ }))
    await userEvent.click(screen.getByRole('button', { name: 'Services' }))
    await userEvent.click(screen.getByRole('button', { name: 'Pods' }))
    grid = await screen.findByRole('grid', { name: 'resources' })
    await within(grid).findByText('api-1')
    expect(nameHead(grid)).toHaveAttribute('aria-sort', sorted!)
    expect(screen.getByRole('textbox', { name: 'Filter rows' })).toHaveValue('')
  })

  it('a remembered page of a kind no longer served is not forced: the target shows what it has', async () => {
    const f = fakeClient([k8s('prod'), k8s('stage')])
    f.client.listKinds = vi.fn(async () => kindsView([podsKind, { id: 'widgets', title: 'Widgets', group: 'Other', scoped: false, columns: [{ id: 'name', title: 'Name', type: 'text' as const }] }]))
    await openProd(f)
    await userEvent.click(screen.getByRole('button', { name: /^Other \(/ }))
    await userEvent.click(await screen.findByRole('button', { name: 'Widgets' }))
    await screen.findByRole('heading', { name: 'Widgets' })
    await userEvent.click(screen.getByRole('option', { name: /stage/ }))
    f.client.listKinds = vi.fn(async () => kindsView([podsKind])) // the CRD went meanwhile
    await userEvent.click(screen.getByRole('option', { name: /prod/ }))
    expect(await screen.findByRole('heading', { name: 'Pods' })).toBeInTheDocument()
  })
})

describe('page snapshot across restarts (P19)', () => {
  const nameHead = (grid: HTMLElement) => within(grid).getByRole('columnheader', { name: /^Name[^a-z]*$/ })
  const rowOf = (grid: HTMLElement, name: string) => within(grid).getByText(name).closest('[role="row"]') as HTMLElement

  it('restores the page — filter, sort, cursor, details and their tab — from the persisted memo', async () => {
    const f = fakeClient([k8s('prod')])
    f.state.rows = [podRow('api-1', 'web'), podRow('api-2', 'web'), podRow('db-0', 'data')]
    f.client.getTargetState = vi.fn(async () => ({
      kind: '"pods"',
      scope: '{"mode":"all"}',
      pageMemo: JSON.stringify({
        v: 1,
        sorts: { pods: { col: 'name', desc: true } },
        page: { key: 'pods/{"mode":"all"}', filter: 'api', selected: 'uid-web-api-2', open: { provider: 'kubernetes', target: 'prod', kind: 'pods', name: 'api-2', uid: 'uid-web-api-2' }, tab: 'yaml' },
      }),
    }))
    const grid = await openProd(f)
    await within(grid).findByText('api-1')
    expect(screen.getByRole('textbox', { name: 'Filter rows' })).toHaveValue('api')
    expect(within(grid).queryByText('db-0')).toBeNull()
    expect(nameHead(grid)).toHaveAttribute('aria-sort', 'descending')
    expect(rowOf(grid, 'api-2')).toHaveAttribute('aria-selected', 'true')
    const drawer = await screen.findByRole('dialog', { name: 'pods api-2' })
    expect(within(drawer).getByRole('tab', { name: 'YAML' })).toHaveAttribute('aria-selected', 'true')
  })

  it('persists the page memo debounced; marks are never in it', async () => {
    const f = fakeClient([k8s('prod')])
    f.state.rows = [podRow('api-1', 'web'), podRow('db-0', 'data')]
    const grid = await openProd(f)
    await within(grid).findByText('db-0')
    await userEvent.click(within(rowOf(grid, 'api-1')).getByRole('checkbox'))
    await userEvent.keyboard('[Slash]')
    await userEvent.keyboard('api')
    expect(within(rowOf(grid, 'api-1')).getByRole('checkbox')).toBeChecked()
    const written = vi.mocked(f.client.setTargetState)
    await waitFor(
      () => {
        const call = written.mock.calls.find((c) => c[2] === 'pageMemo')
        expect(call).toBeDefined()
        expect(call![3]).toContain('"filter":"api"')
      },
      { timeout: 4000 },
    )
    const json = written.mock.calls.find((c) => c[2] === 'pageMemo')![3]
    expect(json).not.toContain('marked')
    const memo = JSON.parse(json)
    expect(memo.v).toBe(1)
    expect(Object.keys(memo).sort()).toEqual(['page', 'sorts', 'v'])
  })

  it('a page past the server cap stays in memory but is not persisted', async () => {
    const f = fakeClient([k8s('prod')])
    f.state.rows = [podRow('api-1', 'web')]
    await openProd(f)
    const grid = await screen.findByRole('grid', { name: 'resources' })
    await within(grid).findByText('api-1')
    await userEvent.keyboard('[Slash]')
    fireEvent.change(screen.getByRole('textbox', { name: 'Filter rows' }), { target: { value: 'x'.repeat(5000) } })
    expect(screen.getByRole('textbox', { name: 'Filter rows' })).toHaveValue('x'.repeat(5000))
    await new Promise((r) => setTimeout(r, 1600))
    // The oversized current state also cancels the pending initial snapshot.
    const written = vi.mocked(f.client.setTargetState)
    const writes = written.mock.calls.filter((c) => c[2] === 'pageMemo')
    expect(writes).toHaveLength(0)
  })
})
