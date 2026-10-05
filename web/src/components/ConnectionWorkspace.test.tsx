import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { App } from '../App'
import { ConnectionWorkspace } from './ConnectionWorkspace'
import type { ConnectionStatus, TargetsView } from '../api/types'
import { initialState, useStore } from '../store'
import { fakeClient, k8s } from '../test/fakeClient'

beforeEach(() => useStore.setState({ ...initialState }))

const progress = (over: Partial<ConnectionStatus> = {}): ConnectionStatus => ({
  id: 7, state: 'connecting', phase: 'checking', attempt: 1, maxAttempts: 3,
  startedAt: Date.now() - 3000, attemptStarted: Date.now() - 3000, ...over,
})

it('restores only the selection and stays disconnected through resync', async () => {
  const f = fakeClient([k8s('prod')])
  f.state.view.selected = { provider: 'kubernetes', id: 'prod' }
  render(<App client={f.client} />)
  await screen.findByRole('button', { name: 'Connect' })
  await act(async () => f.emit({ type: 'resync' }))
  expect(f.client.connectTarget).not.toHaveBeenCalled()
  expect(f.client.listKinds).not.toHaveBeenCalled()
  expect(f.client.listScopes).not.toHaveBeenCalled()
  expect(f.client.openView).not.toHaveBeenCalled()
})

it('shows the real retry state and cancels its attempt', async () => {
  const target = k8s('prod', { connection: progress({ phase: 'retry_wait', attempt: 2, retryAt: Date.now() + 1000, errorClass: 'unavailable', error: 'connection refused' }) })
  const f = fakeClient([target])
  f.state.view.selected = target
  render(<App client={f.client} />)
  await screen.findByRole('button', { name: 'Cancel' })
  expect(screen.getByText(/Attempt 2 of 3/)).toBeInTheDocument()
  expect(screen.getByText(/Retrying in/)).toBeInTheDocument()
  expect(screen.getByText('connection refused')).toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  await screen.findByRole('button', { name: 'Connect' })
  expect(f.client.cancelConnectTarget).toHaveBeenCalledWith('kubernetes', 'prod', 7)
  expect(f.client.listKinds).not.toHaveBeenCalled()
  expect(screen.getByText('Connection cancelled')).toBeInTheDocument()
})

it('can cancel before the start response arrives and never mounts a late successful connection', async () => {
  const target = k8s('prod')
  const f = fakeClient([target])
  f.state.view.selected = target
  let started!: (status: ConnectionStatus) => void
  f.client.connectTarget = vi.fn(() => new Promise<ConnectionStatus>((resolve) => { started = resolve }))
  render(<App client={f.client} />)
  await userEvent.click(await screen.findByRole('button', { name: 'Connect' }))
  await userEvent.click(await screen.findByRole('button', { name: 'Cancel' }))
  expect(screen.getByText('Cancelling…')).toBeInTheDocument()
  await act(async () => {
    target.connection = progress({ id: 41, state: 'connected', phase: 'ready' })
    target.open = true
    f.emit({ type: 'targets_changed' })
    started(target.connection)
  })
  await screen.findByRole('button', { name: 'Connect' })
  expect(f.client.cancelConnectTarget).toHaveBeenCalledWith('kubernetes', 'prod', 41)
  expect(f.client.listKinds).not.toHaveBeenCalled()
  expect(f.client.listScopes).not.toHaveBeenCalled()
  expect(f.client.openView).not.toHaveBeenCalled()
})

it('holds cancellation until an overlapping target reload and its follow-up finish', async () => {
  const target = k8s('prod', { connection: progress() })
  const f = fakeClient([target])
  f.state.view.selected = target
  render(<App client={f.client} />)
  await screen.findByRole('button', { name: 'Cancel' })
  const stale = structuredClone(f.state.view)
  stale.groups[0].targets[0].connection = progress({ state: 'connected', phase: 'ready' })
  let answer!: (view: TargetsView) => void
  vi.mocked(f.client.listTargets).mockImplementationOnce(() => new Promise<TargetsView>((resolve) => { answer = resolve }))
  act(() => f.emit({ type: 'targets_changed' }))
  await waitFor(() => expect(answer).toBeDefined())
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }))
  expect(screen.getByText('Cancelling…')).toBeInTheDocument()
  await act(async () => answer(stale))
  await screen.findByRole('button', { name: 'Connect' })
  expect(f.client.openView).not.toHaveBeenCalled()
  expect(f.client.listKinds).not.toHaveBeenCalled()
})


it('starts a new retry countdown from its attempt timestamp after an idle period', () => {
  const old = Date.now() - 10000
  const clock = vi.spyOn(Date, 'now').mockReturnValue(old)
  try {
    const target = k8s('prod')
    const props = { onConnect: vi.fn(), onCancel: vi.fn() }
    const ui = render(<ConnectionWorkspace target={target} {...props} />)
    const started = old + 10000
    clock.mockReturnValue(started)
    ui.rerender(<ConnectionWorkspace target={{ ...target, connection: progress({
      phase: 'retry_wait', startedAt: started, attemptStarted: started, retryAt: started + 500,
    }) }} {...props} />)
    expect(screen.getByText('Retrying in 1 s…')).toBeInTheDocument()
    expect(screen.getByText(/0 s elapsed/)).toBeInTheDocument()
  } finally { clock.mockRestore() }
})
