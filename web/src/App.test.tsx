import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it } from 'vitest'
import { App } from './App'
import { initialState, useStore } from './store'
import { fakeClient, k8s } from './test/fakeClient'

beforeEach(() => useStore.setState({ ...initialState }))

describe('App', () => {
  it('lists contexts, marks current, and opens one on click', async () => {
    const f = fakeClient([k8s('prod', { current: true }), k8s('dev')])
    render(<App client={f.client} />)
    expect(await screen.findByRole('option', { name: /prod/ })).toBeInTheDocument()
    expect(screen.getByText('current')).toBeInTheDocument()
    expect(screen.getByText('Pick a context on the left')).toBeInTheDocument()

    await userEvent.click(screen.getByRole('option', { name: /dev/ }))
    expect(await screen.findByRole('heading', { name: 'dev' })).toBeInTheDocument()
    expect(screen.getByText('https://dev.example:6443')).toBeInTheDocument()
    expect(screen.getByRole('option', { name: /dev/ })).toHaveAttribute('aria-selected', 'true')
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
    expect(await screen.findByRole('heading', { name: 'beta' })).toBeInTheDocument()
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

  it('shows unreadable kubeconfig files as a warning', async () => {
    const f = fakeClient([k8s('ok')])
    f.state.view.groups[0].problems = [{ source: '/home/u/.kube/broken', message: 'yaml: line 3: did not find expected key' }]
    render(<App client={f.client} />)
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not read 1 file(s)')
    expect(screen.getByRole('alert')).toHaveTextContent('broken: yaml: line 3')
  })

  it('explains where contexts come from when there are none', async () => {
    const f = fakeClient([])
    render(<App client={f.client} />)
    await waitFor(() => expect(screen.getByText(/Ocular reads KUBECONFIG/)).toBeInTheDocument())
  })
})
