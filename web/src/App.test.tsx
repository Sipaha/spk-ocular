import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { App } from './App'
import { initialState, useStore } from './store'
import { fakeClient, k8s, podRow } from './test/fakeClient'

beforeEach(() => useStore.setState({ ...initialState }))

describe('App', () => {
  it('selects a context without opening it until Connect is pressed', async () => {
    const f = fakeClient([k8s('prod', { current: true }), k8s('dev')])
    render(<App client={f.client} />)
    expect(await screen.findByRole('option', { name: /prod/ })).toBeInTheDocument()
    expect(screen.queryByText('current')).not.toBeInTheDocument()
    expect(screen.getByText('Pick a context on the left')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('option', { name: /dev/ }))
    const connect = await screen.findByRole('button', { name: 'Connect' })
    expect(f.client.connectTarget).not.toHaveBeenCalled()
    expect(f.client.listKinds).not.toHaveBeenCalled()
    expect(f.client.listScopes).not.toHaveBeenCalled()
    expect(f.client.openView).not.toHaveBeenCalled()
    await userEvent.click(connect)
    expect(f.client.connectTarget).toHaveBeenCalledWith('kubernetes', 'dev')
    expect(await screen.findByRole('heading', { name: 'Pods' })).toBeInTheDocument()
    expect(screen.getByRole('option', { name: /dev/ })).toHaveAttribute('aria-selected', 'true')
    expect(screen.queryByRole('button', { name: 'Overview' })).not.toBeInTheDocument()
    await userEvent.click(screen.getByRole('button', { name: 'Connection information' }))
    expect(await screen.findByRole('heading', { name: 'dev' })).toBeInTheDocument()
    expect(screen.getByText('https://dev.example:6443')).toBeInTheDocument()
  })

  it('connection information keeps the table and filter mounted, traps focus, and returns it on close', async () => {
    const f = fakeClient([k8s('prod')])
    f.state.view.selected = { provider: 'kubernetes', id: 'prod' }
    f.state.rows = [podRow('api-1', 'web')]
    render(<App client={f.client} />)
    await userEvent.click(await screen.findByRole('button', { name: 'Connect' }))
    const grid = await screen.findByRole('grid', { name: 'resources' })
    const filter = screen.getByRole('textbox', { name: 'Filter rows' })
    await userEvent.type(filter, 'api')
    const opened = vi.mocked(f.client.openView).mock.calls.length
    const trigger = screen.getByRole('button', { name: 'Connection information' })
    await userEvent.click(trigger)
    const dialog = screen.getByRole('dialog', { name: 'Connection information' })
    const close = within(dialog).getByRole('button', { name: 'Close' })
    expect(close).toHaveFocus()
    await userEvent.tab()
    expect(within(dialog).getByLabelText('Connection information')).toHaveFocus()
    await userEvent.tab()
    expect(close).toHaveFocus()
    await userEvent.tab({ shift: true })
    expect(dialog).toContainElement(document.activeElement as HTMLElement)
    await userEvent.keyboard('{Control>}k{/Control}{F6}')
    expect(screen.queryByRole('dialog', { name: 'Go to' })).not.toBeInTheDocument()
    expect(dialog).toContainElement(document.activeElement as HTMLElement)
    await userEvent.keyboard('{Escape}')
    expect(dialog).not.toBeInTheDocument()
    expect(trigger).toHaveFocus()
    expect(screen.getByRole('grid', { name: 'resources' })).toBe(grid)
    expect(filter).toHaveValue('api')
    expect(f.client.openView).toHaveBeenCalledTimes(opened)
    await userEvent.click(trigger)
    await userEvent.click(screen.getByRole('dialog', { name: 'Connection information' }).parentElement!)
    expect(screen.queryByRole('dialog', { name: 'Connection information' })).not.toBeInTheDocument()
    expect(trigger).toHaveFocus()
  })

  it('connection information follows live details and closes when its target disappears', async () => {
    const target = k8s('prod')
    const f = fakeClient([target])
    f.state.view.selected = { provider: 'kubernetes', id: 'prod' }
    render(<App client={f.client} />)
    await userEvent.click(await screen.findByRole('button', { name: 'Connect' }))
    await screen.findByRole('grid', { name: 'resources' })
    await userEvent.click(screen.getByRole('button', { name: 'Connection information' }))
    target.details = [{ key: 'server', value: 'https://new.example:6443' }]
    act(() => f.emit({ type: 'targets_changed' }))
    expect(await screen.findByText('https://new.example:6443')).toBeInTheDocument()
    f.state.view.groups[0].targets = []
    act(() => f.emit({ type: 'targets_changed' }))
    await screen.findByText('Pick a context on the left')
    expect(screen.queryByRole('dialog', { name: 'Connection information' })).not.toBeInTheDocument()
    f.state.view.groups[0].targets = [target]
    act(() => f.emit({ type: 'targets_changed' }))
    await screen.findByRole('button', { name: 'Connection information' })
    expect(screen.queryByRole('dialog', { name: 'Connection information' })).not.toBeInTheDocument()
  })

  it('"/" focuses the filter; arrows and Enter select from the keyboard', async () => {
    const f = fakeClient([k8s('alpha'), k8s('beta'), k8s('gamma')])
    render(<App client={f.client} />)
    await screen.findByRole('option', { name: /alpha/ })
    await userEvent.keyboard('/')
    expect(screen.getByRole('textbox', { name: 'Filter' })).toHaveFocus()
    await userEvent.keyboard('a')
    // alpha, beta, gamma all contain "a": cursor starts on the first one
    await userEvent.keyboard('{ArrowDown}{Enter}')
    await userEvent.click(await screen.findByRole('button', { name: 'Connect' }))
    expect(await screen.findByRole('heading', { name: 'Pods' })).toBeInTheDocument()
    expect(f.client.selectTarget).toHaveBeenCalledWith('kubernetes', 'beta')
  })

  it('reloads on targets_changed', async () => {
    const f = fakeClient([k8s('a')])
    render(<App client={f.client} />)
    await screen.findByRole('option', { name: /a/ })
    f.state.view.groups[0].targets.push(k8s('added'))
    act(() => f.emit({ type: 'targets_changed' }))
    expect(await screen.findByRole('option', { name: /added/ })).toBeInTheDocument()
  })

  it('reloads on resync (missed events)', async () => {
    const f = fakeClient([k8s('a')])
    render(<App client={f.client} />)
    await screen.findByRole('option', { name: /a/ })
    f.state.view.groups[0].targets.push(k8s('late'))
    act(() => f.emit({ type: 'resync' }))
    expect(await screen.findByRole('option', { name: /late/ })).toBeInTheDocument()
  })

  it('shows unreadable kubeconfig files as a warning', async () => {
    const f = fakeClient([k8s('ok')])
    f.state.view.groups[0].problems = [{ source: '/home/u/.kube/broken', message: 'yaml: line 3: did not find expected key' }]
    render(<App client={f.client} />)
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not read 1 file(s)')
    expect(screen.getByRole('alert')).toHaveTextContent('broken: yaml: line 3')
  })

  it('offers explicit configuration import when there are no targets', async () => {
    const f = fakeClient([])
    render(<App client={f.client} />)
    await waitFor(() => expect(screen.getByText(/No configurations added/)).toBeInTheDocument())
  })
})
