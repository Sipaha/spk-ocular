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
  await user.click(await screen.findByRole('gridcell', { name: 'demo' }))
  await user.click(await screen.findByRole('tab', { name: 'Resources' }))
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
  await user.click(await screen.findByRole('gridcell', { name: 'demo' }))
  await user.click(await screen.findByRole('button', { name: 'Chart version' }))
  await user.click(screen.getByRole('option', { name: '1.0.0' }))
  await user.click(screen.getByRole('button', { name: 'Close' }))
  await act(async () => resolveLate({ chart: { ...charts[1], values: 'private values must stay hidden' } }))
  expect(screen.queryByText('private values must stay hidden')).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: 'Install' })).not.toBeInTheDocument()
})

it('opens settings as a normal workspace without an internal section dropdown or visible section heading',async()=>{
 const {client}=fakeClient([target])
 client.helm=vi.fn(async()=>({settings:{repositories:[],storage:{driver:'secret'}}}))
 render(<HelmWorkspace client={client} target={target} scope={{mode:'all'}} scopePicker={null} initialTab="settings"/>)
 expect(await screen.findByRole('button',{name:'Add repository'})).toBeEnabled()
 expect(screen.queryByRole('button',{name:'Helm section'})).toBeNull()
 expect(screen.getByRole('heading',{name:'Repositories & settings'})).toHaveClass('sr-only')
 expect(client.helm).not.toHaveBeenCalledWith(expect.objectContaining({command:'list'}),expect.anything())
})
it('uses shared delayed refresh/cancel controls, retains the grid, and ignores a cancelled late result',async()=>{
 const {client}=fakeClient([target]);let count=0,resolve!:(value:HelmResponse)=>void
 const release={name:'old',namespace:'blue',revision:1,chart:'demo',chartVersion:'1',appVersion:'1',status:'deployed',updated:'2026-10-05T00:00:00Z'}
 client.helm=vi.fn(async(request:HelmRequest)=>{
  if(request.command==='settings')return{settings:{repositories:[],storage:{driver:'secret'}}}
  if(++count===2)return new Promise<HelmResponse>(done=>{resolve=done})
  return{releases:[{...release,name:count===1?'old':'fresh'}]}
 })
 const user=userEvent.setup()
 render(<HelmWorkspace client={client} target={target} scope={{mode:'all'}} scopePicker={null} initialTab="releases"/>)
 await screen.findByRole('gridcell',{name:'old'})
 const grid=screen.getByRole('grid'),refresh=screen.getByRole('button',{name:'Refresh'})
 await user.click(refresh)
 expect(screen.queryByRole('button',{name:'Cancel'})).toBeNull();expect(screen.queryByRole('button',{name:'Stop waiting'})).toBeNull()
 expect(screen.getByRole('grid')).toBe(grid)
 await waitFor(()=>expect(screen.getByRole('button',{name:'Cancel'})).toBeVisible(),{timeout:3500})
 expect(screen.getByRole('status')).toHaveTextContent(/Received —/)
 await user.click(screen.getByRole('button',{name:'Cancel'}))
 expect(screen.getByText('Request cancelled')).toBeVisible();expect(grid).not.toBeVisible()
 await act(async()=>resolve({releases:[{...release,name:'late'}]}))
 expect(screen.queryByText('late')).toBeNull();expect(screen.getByText('Request cancelled')).toBeVisible()
 await user.click(screen.getByRole('button',{name:'Retry'}));await screen.findByRole('gridcell',{name:'fresh'})
 expect(screen.getByRole('grid')).toBe(grid);expect(count).toBe(3)
})
it('drops previous scope data immediately and fences a late scope response',async()=>{
 const {client}=fakeClient([target]);let resolve!:(value:HelmResponse)=>void
 client.helm=vi.fn(async(request:HelmRequest)=>{
  if(request.command==='settings')return{settings:{repositories:[],storage:{driver:'secret'}}}
  if(request.scope?.name==='blue')return new Promise<HelmResponse>(done=>{resolve=done})
  return{releases:[{name:'green-release',namespace:'green',revision:1,chart:'demo',chartVersion:'1',appVersion:'1',status:'deployed',updated:'2026-10-05T00:00:00Z'}]}
 })
 const {rerender}=render(<HelmWorkspace client={client} target={target} scope={{mode:'one',name:'blue'}} scopePicker={null} initialTab="releases"/>)
 await waitFor(()=>expect(resolve).toBeTypeOf('function'))
 rerender(<HelmWorkspace client={client} target={target} scope={{mode:'one',name:'green'}} scopePicker={null} initialTab="releases"/>)
 await screen.findByRole('gridcell',{name:'green-release'})
 await act(async()=>resolve({releases:[]}))
 expect(screen.getByRole('gridcell',{name:'green-release'})).toBeVisible()
})
