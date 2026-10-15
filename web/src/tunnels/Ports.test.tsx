import { render, waitFor } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { fakeClient } from '../test/fakeClient'
import { PortsSection } from './Ports'

it('does not reread an unchanged object on parent renders; a revision or UID change does refresh ports', async () => {
  const { client } = fakeClient([])
  client.forwardInfo = vi.fn(async () => ({ ports: [], anyPort: true }))
  const subject = { provider: 'kubernetes', target: 'fixture', kind: 'pods', scope: 'team-a', name: 'pod', uid: 'one' }
  const view = render(<PortsSection client={client} subject={subject} revision="1" />)
  await waitFor(() => expect(client.forwardInfo).toHaveBeenCalledTimes(1))
  view.rerender(<PortsSection client={client} subject={{ ...subject }} revision="1" />)
  await Promise.resolve()
  expect(client.forwardInfo).toHaveBeenCalledTimes(1)
  view.rerender(<PortsSection client={client} subject={{ ...subject }} revision="2" />)
  await waitFor(() => expect(client.forwardInfo).toHaveBeenCalledTimes(2))
  view.rerender(<PortsSection client={client} subject={{ ...subject, uid: 'replacement' }} revision="2" />)
  await waitFor(() => expect(client.forwardInfo).toHaveBeenCalledTimes(3))
})
