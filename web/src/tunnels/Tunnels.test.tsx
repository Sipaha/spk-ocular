import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Browser } from '@wailsio/runtime'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ApiError } from '../api/client'
import type { Tunnel } from '../api/types'
import { App } from '../App'
import { initialState, useStore } from '../store'
import { kindsView, fakeClient, k8s, podRow, podsKind } from '../test/fakeClient'
import { useTunnels } from './store'

beforeEach(() => {
  useStore.setState({ ...initialState })
  useTunnels.setState({ list: [], panel: false, error: null })
})

const tunnel = (id: string, extra: Partial<Tunnel> = {}): Tunnel => ({
  id,
  target: { provider: 'kubernetes', target: 'prod', targetTitle: 'prod', endpoint: 'prod.example:6443', ref: { provider: 'kubernetes', target: 'prod', kind: 'services', scope: 'web', name: 'web' }, port: 80 },
  localPort: 8080,
  addresses: ['127.0.0.1:8080', '[::1]:8080'],
  ipv6: 'ok',
  state: 'ready',
  upstream: 'web-1:80',
  conns: 1,
  served: 3,
  rejected: 0,
  failed: 0,
  bytesIn: 2048,
  bytesOut: 512,
  started: '2026-09-30T00:00:00Z',
  ...extra,
})

async function setup(mode: 'desktop' | 'browser' = 'browser') {
  const f = fakeClient([k8s('prod')])
  f.client.appInfo = vi.fn(async () => ({ name: 'SPK Ocular', version: 'test', mode, language: 'en' as const }))
  f.client.listKinds = vi.fn(async () => kindsView([{ ...podsKind, forward: true }]))
  f.state.rows = [podRow('api-1', 'web')]
  f.state.view.selected = { provider: 'kubernetes', id: 'prod' }
  render(<App client={f.client} />)
  const grid = await screen.findByRole('grid', { name: 'resources' })
  return { f, grid, user: userEvent.setup() }
}

describe('tunnels', () => {
  it('the list follows forwards_changed; errors, ::1 status and Open only for web ports', async () => {
    const { f, user } = await setup('desktop')
    expect(screen.queryByRole('button', { name: /⇄/ })).not.toBeInTheDocument()
    f.client.listForwards = vi.fn(async () => [
      tunnel('f1', { scheme: 'http' }),
      tunnel('f2', { scheme: undefined, localPort: 5432, addresses: ['127.0.0.1:5432'], ipv6: 'busy', state: 'error', lastError: { class: 'gone', message: 'pod db was replaced', at: '' }, target: { ...tunnel('x').target, ref: { ...tunnel('x').target.ref, kind: 'pods', name: 'db' }, port: 5432 } }),
    ])
    await act(async () => f.emit({ type: 'forwards_changed' }))
    const ind = await screen.findByRole('button', { name: /⇄ 2/ })
    expect(ind).toHaveClass('text-danger') // one is failing
    await user.click(ind)
    const panel = screen.getByRole('region', { name: 'Port forwards' })
    const web = within(panel).getByRole('listitem', { name: 'services/web:80' })
    expect(web).toHaveTextContent('127.0.0.1:8080')
    expect(web).toHaveTextContent('[::1]:8080')
    expect(web).toHaveTextContent('via web-1:80')
    await user.click(within(web).getByRole('button', { name: 'Open' }))
    expect(Browser.OpenURL).toHaveBeenCalledWith('http://127.0.0.1:8080/')

    const db = within(panel).getByRole('listitem', { name: 'pods/db:5432' })
    expect(within(db).queryByRole('button', { name: 'Open' })).not.toBeInTheDocument()
    expect(db).toHaveTextContent('[::1] is taken by another program')
    expect(within(db).getByRole('alert')).toHaveTextContent('pod db was replaced')
    await user.click(within(db).getByRole('button', { name: 'Stop' }))
    expect(f.client.stopForward).toHaveBeenCalledWith('f2')
  })

  it('forwards a port from the details; a taken local port is shown in the dialog', async () => {
    const { f, grid, user } = await setup()
    f.client.forwardInfo = vi.fn(async () => ({ ports: [{ port: 8080, name: 'http', protocol: 'TCP', scheme: 'http', supported: true }, { port: 53, protocol: 'UDP', supported: false, reason: 'UDP cannot be forwarded' }], anyPort: true }))
    f.client.startForward = vi.fn(async () => {
      throw new ApiError('conflict', 'local port 8080 is in use')
    })
    await user.click(await within(grid).findByText('api-1'))
    await user.keyboard('{Enter}')
    const ports = await screen.findByRole('region', { name: 'Ports' })
    await waitFor(() => expect(within(ports).getByText('8080')).toBeInTheDocument())
    expect(within(ports).getByText('cannot be forwarded')).toHaveAttribute('title', 'UDP cannot be forwarded')
    await user.click(within(ports).getByRole('button', { name: 'Forward' }))
    const dialog = screen.getByRole('dialog', { name: 'Forward a port' })
    expect(within(dialog).getByRole('button', { name: 'Open in a browser as' })).toHaveTextContent('http')
    await user.type(within(dialog).getByRole('textbox', { name: 'Local port' }), '8080')
    await user.click(within(dialog).getByRole('button', { name: 'Forward' }))
    expect(await within(dialog).findByRole('alert')).toHaveTextContent('local port 8080 is in use')
    expect(f.client.startForward).toHaveBeenCalledWith({ ref: expect.objectContaining({ name: 'api-1' }), port: 8080, localPort: 8080, scheme: 'http' })

    f.client.startForward = vi.fn(async () => tunnel('f1'))
    await user.clear(within(dialog).getByRole('textbox', { name: 'Local port' }))
    await user.click(within(dialog).getByRole('button', { name: 'Forward' }))
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Forward a port' })).not.toBeInTheDocument())
    expect(f.client.startForward).toHaveBeenCalledWith(expect.objectContaining({ port: 8080, localPort: undefined }))
    expect(screen.getByRole('region', { name: 'Port forwards' })).toBeInTheDocument() // the panel shows the new one
  })
})
