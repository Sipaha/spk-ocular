import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import type { AgentClientSetupView } from '../api/types'
import { fakeClient } from '../test/fakeClient'
import { ClientSetup } from './ClientSetup'

it('discards an old client response after replacement and installs only the new revision', async () => {
  const first = fakeClient([])
  const next = fakeClient([])
  let finish!: (view: AgentClientSetupView) => void
  first.client.agentClientSetup = vi.fn(() => new Promise<AgentClientSetupView>(resolve => { finish = resolve }))
  next.state.agentClients[0].path = '/next/config.toml'
  next.state.agentClients[0].revision = 'next-revision'
  const view = render(<ClientSetup client={first.client} status={first.state.agentStatus} />)
  view.rerender(<ClientSetup client={next.client} status={next.state.agentStatus} />)
  expect(await screen.findByText('/next/config.toml')).toBeInTheDocument()
  await act(async () => finish({ clients: first.state.agentClients }))
  expect(screen.queryByText('/home/u/.codex/config.toml')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Set up Codex' }))
  await waitFor(() => expect(next.client.agentClientSetup).toHaveBeenLastCalledWith({ client: 'codex', install: true, revision: 'next-revision' }))
  expect(first.client.agentClientSetup).toHaveBeenCalledExactlyOnceWith({})
})

it('keeps setup single-flight and drops a failed installation from a replaced client', async () => {
  const first = fakeClient([])
  const next = fakeClient([])
  let reject!: (error: Error) => void
  const view = render(<ClientSetup client={first.client} status={first.state.agentStatus} />)
  await screen.findByRole('button', { name: 'Set up Codex' })
  vi.mocked(first.client.agentClientSetup).mockImplementationOnce(() => new Promise((_, fail) => { reject = fail }))
  const button = screen.getByRole('button', { name: 'Set up Codex' })
  await userEvent.dblClick(button)
  expect(first.client.agentClientSetup).toHaveBeenCalledTimes(2)
  expect(button).toBeDisabled()
  view.rerender(<ClientSetup client={next.client} status={next.state.agentStatus} />)
  expect(await screen.findByRole('button', { name: 'Set up Codex' })).toBeEnabled()
  await act(async () => reject(new Error('old write failed')))
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: 'Set up Codex' })).toBeEnabled()
})
