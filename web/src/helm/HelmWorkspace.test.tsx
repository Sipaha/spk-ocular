import { act, render, screen, waitFor, cleanup } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'
import { HelmWorkspace } from './HelmWorkspace'
import { fakeClient } from '../test/fakeClient'
import { resetGuard } from '../edit/guard'
import type { HelmRequest, HelmResponse } from './types'

const target = { provider: 'kubernetes', id: 'test', title: 'Test' }
afterEach(() => { cleanup(); resetGuard() })
it('keeps an empty explicit namespace selection empty', async () => {
  const { client } = fakeClient([target])
  client.helm = vi.fn(async (r: HelmRequest): Promise<HelmResponse> => r.command === 'settings' ? { settings: { repositories: [], storage: { driver: 'secret' } } } : { releases: [] })
  render(<HelmWorkspace client={client} target={target} scope={{ mode: 'some', names: [] }} scopePicker={null} initialTab="releases" />)
  await waitFor(() => expect(client.helm).toHaveBeenCalledWith(expect.objectContaining({ command: 'list', scope: { mode: 'some', names: [] } }), expect.any(AbortSignal)))
  expect(await screen.findByText('No matching entries.')).toBeVisible()
})
it('never runs uninstall before an explicit prepared review', async () => {
  const { client } = fakeClient([target])
  const onResource = vi.fn()
  const resourceRef = { provider: 'kubernetes', target: 'test', kind: 'configmaps', scope: 'blue', name: 'config' }
  const release = { name: 'demo', namespace: 'blue', revision: 2, chart: 'demo', chartVersion: '1.0.0', appVersion: '1', status: 'deployed', updated: '2026-10-05T00:00:00Z' }
  client.helm = vi.fn(async (r: HelmRequest): Promise<HelmResponse> => {
    if (r.command === 'settings') return { settings: { repositories: [], storage: { driver: 'secret' } } }
    if (r.command === 'list') return { releases: [release], configRev: 'rev' }
    if (r.command === 'detail') return { configRev: 'rev', detail: { ...release, resources: [{ apiVersion: 'v1', kind: 'ConfigMap', namespace: 'blue', name: 'config', ref: resourceRef }], values: '{}', computedValues: '{}', manifest: 'kind: ConfigMap', hooks: '', notes: '', history: [release] } }
    if (r.command === 'prepare') return { configRev: 'rev', plan: { id: 'one-shot', operation: r.operation!, currentRevision: 2, manifest: 'kind: ConfigMap', previousManifest: 'kind: ConfigMap', notes: '', warnings: [], expires: '2026-10-05T01:00:00Z' } }
    if (r.command === 'run') return { result: { outcome: 'done', message: '' } }
    return {}
  })
  const user = userEvent.setup()
  render(<HelmWorkspace client={client} target={target} scope={{ mode: 'all' }} scopePicker={null} initialTab="releases" onResource={onResource} />)
  await user.click(await screen.findByRole('button', { name: 'demo' }))
  await user.click(await screen.findByRole('button', { name: 'Resources' }))
  await user.click(screen.getByRole('button', { name: 'config' }))
  expect(onResource).toHaveBeenCalledWith(resourceRef)
  await user.click(await screen.findByRole('button', { name: 'Uninstall' }))
  expect(client.helm).not.toHaveBeenCalledWith(expect.objectContaining({ command: 'run' }), expect.anything())
  await user.click(screen.getByRole('button', { name: 'Review operation' }))
  await screen.findByRole('button', { name: 'Execute reviewed operation' })
  expect(client.helm).not.toHaveBeenCalledWith(expect.objectContaining({ command: 'run' }), expect.anything())
  await user.click(screen.getByRole('button', { name: 'Execute reviewed operation' }))
  await waitFor(() => expect(client.helm).toHaveBeenCalledWith(expect.objectContaining({ command: 'run', planId: 'one-shot', configRev: 'rev' }), expect.any(AbortSignal)))
})

it('cannot redisplay a late chart-values response after the detail is closed', async () => {
  const { client } = fakeClient([target])
  let resolveLate!: (response: HelmResponse) => void
  const late = new Promise<HelmResponse>(resolve => { resolveLate = resolve })
  const charts = ['2.0.0', '1.0.0'].map(version => ({ repository: 'fixture', name: 'demo', version, description: '', appVersion: '1', deprecated: false }))
  client.helm = vi.fn(async (r: HelmRequest): Promise<HelmResponse> => {
    if (r.command === 'settings') return { settings: { repositories: [{ name: 'fixture', url: 'https://fixture.invalid' }], storage: { driver: 'secret' } } }
    if (r.command === 'catalog') return { charts }
    if (r.command === 'chart') return r.chart?.version === '1.0.0' ? late : { chart: { ...charts[0], values: 'safe defaults' } }
    return {}
  })
  const user = userEvent.setup()
  render(<HelmWorkspace client={client} target={target} scope={{ mode: 'all' }} scopePicker={null} initialTab="charts" />)
  await user.click(await screen.findByRole('button', { name: 'demo' }))
  await user.click(await screen.findByRole('button', { name: 'Chart version' }))
  await user.click(screen.getByRole('option', { name: '1.0.0' }))
  await user.click(screen.getByRole('button', { name: 'Close' }))
  await act(async () => resolveLate({ chart: { ...charts[1], values: 'private values must stay hidden' } }))
  expect(screen.queryByText('private values must stay hidden')).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Install' })).not.toBeInTheDocument()
})
