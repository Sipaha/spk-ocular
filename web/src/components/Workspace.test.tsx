import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import { initialState, useStore } from '../store'
import { fakeClient, k8s, podRow, scopeRow } from '../test/fakeClient'

beforeEach(() => useStore.setState({ ...initialState }))

async function openProd(f: ReturnType<typeof fakeClient>) {
  f.state.view.selected = { provider: 'kubernetes', id: 'prod' }
  render(<App client={f.client} />)
  return await screen.findByRole('grid', { name: 'resources' })
}

describe('Workspace', () => {
  it('shows live rows of the default namespace with health', async () => {
    const f = fakeClient([k8s('prod', { details: [{ key: 'defaultNamespace', value: 'web' }] })])
    f.state.rows = [podRow('api-1', 'web'), podRow('api-2', 'web', 'CrashLoopBackOff', { state: 'error', reason: 'CrashLoopBackOff' })]
    const grid = await openProd(f)
    expect(await within(grid).findByText('api-1')).toBeInTheDocument()
    expect(within(grid).getByText('CrashLoopBackOff')).toHaveClass('text-danger')
    expect(f.client.openView).toHaveBeenCalledWith('kubernetes', 'prod', { kind: 'pods', scope: { mode: 'one', name: 'web' } })
    expect(within(grid).queryByText('Namespace')).not.toBeInTheDocument() // one namespace: no namespace column
    expect(screen.getByRole('combobox', { name: 'Namespace' })).toHaveValue('web')
  })

  it('switching to all namespaces reopens the view and shows the namespace column', async () => {
    const f = fakeClient([k8s('prod', { details: [{ key: 'defaultNamespace', value: 'web' }] })])
    await openProd(f)
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Namespace' }), '')
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
    await userEvent.keyboard('/')
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

  it('shows metrics in CPU/Memory columns by row id', async () => {
    const f = fakeClient([k8s('prod')])
    const withMetrics = { ...podsKindWithMetrics }
    f.client.listKinds = vi.fn(async () => [withMetrics])
    f.client.openView = vi.fn(async () => ({ viewId: 'v-pods', kind: withMetrics }))
    f.state.rows = [podRow('api-1', 'web')]
    f.client.getMetrics = vi.fn(async () => ({ status: 'ok', values: { 'uid-web-api-1': { cpu: 0.25, memory: 64 * 1024 * 1024 } } }))
    const grid = await openProd(f)
    expect(await within(grid).findByText('250m')).toBeInTheDocument()
    expect(within(grid).getByText('64Mi')).toBeInTheDocument()
  })

  it('restores the last kind and scope of the target', async () => {
    const f = fakeClient([k8s('prod')])
    f.client.getTargetState = vi.fn(async () => ({ kind: '"pods"', scope: '{"mode":"one","name":"data"}' }))
    await openProd(f)
    expect(f.client.openView).toHaveBeenCalledTimes(1)
    expect(f.client.openView).toHaveBeenCalledWith('kubernetes', 'prod', { kind: 'pods', scope: { mode: 'one', name: 'data' } })
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Namespace' }), 'web')
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
    const picker = screen.getByRole('combobox', { name: 'Namespace' })
    expect(await within(picker).findByRole('option', { name: 'web' })).toBeInTheDocument()
    f.state.rowsByKind.namespaces = [scopeRow('default'), scopeRow('web'), scopeRow('new-team')]
    await act(async () => f.emit({ type: 'view_changed', payload: { viewId: 'v-namespaces', version: 99 } }))
    expect(await within(picker).findByRole('option', { name: 'new-team' })).toBeInTheDocument()
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
