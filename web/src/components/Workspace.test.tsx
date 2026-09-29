import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import { initialState, useStore } from '../store'
import { fakeClient, k8s, podRow } from '../test/fakeClient'

beforeEach(() => useStore.setState({ ...initialState }))

async function openProd(f: ReturnType<typeof fakeClient>) {
  f.state.view.selected = { provider: 'kubernetes', id: 'prod' }
  render(<App client={f.client} />)
  return await screen.findByRole('grid', { name: 'resources' })
}

describe('Workspace', () => {
  it('shows live rows of the default namespace with health', async () => {
    const f = fakeClient([k8s('prod', { details: [{ key: 'namespace', value: 'web' }] })])
    f.state.rows = [podRow('api-1', 'web'), podRow('api-2', 'web', 'CrashLoopBackOff', { state: 'error', reason: 'CrashLoopBackOff' })]
    const grid = await openProd(f)
    expect(await within(grid).findByText('api-1')).toBeInTheDocument()
    expect(within(grid).getByText('CrashLoopBackOff')).toHaveClass('text-danger')
    expect(f.client.openView).toHaveBeenCalledWith('kubernetes', 'prod', { kind: 'pods', scope: { mode: 'one', name: 'web' } })
    expect(within(grid).queryByText('Namespace')).not.toBeInTheDocument() // one namespace: no namespace column
    expect(screen.getByRole('combobox', { name: 'Namespace' })).toHaveValue('web')
  })

  it('switching to all namespaces reopens the view and shows the namespace column', async () => {
    const f = fakeClient([k8s('prod', { details: [{ key: 'namespace', value: 'web' }] })])
    const grid = await openProd(f)
    await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Namespace' }), '')
    expect(f.client.openView).toHaveBeenLastCalledWith('kubernetes', 'prod', { kind: 'pods', scope: { mode: 'all' } })
    expect(f.client.closeView).toHaveBeenCalled()
    expect(within(grid).getByText('Namespace')).toBeInTheDocument()
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
    await act(async () => f.emit({ type: 'view_changed', payload: { viewId: 'v1', version: 2 } }))
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
