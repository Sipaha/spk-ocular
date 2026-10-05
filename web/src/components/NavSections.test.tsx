import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { App } from '../App'
import { actions, initialState, useStore } from '../store'
import { connectedClient as fakeClient, k8s, kindsView, podsKind } from '../test/fakeClient'

beforeEach(() => useStore.setState({ ...initialState }))

it.each([false, true])('opens only Workloads by default and respects saved choices (saved=%s)', async (saved) => {
  const f = fakeClient([k8s('a')])
  f.state.view.selected = { provider: 'kubernetes', id: 'a' }
  f.client.listKinds = vi.fn(async () => kindsView([podsKind,
    { ...podsKind, id: 'services', title: 'Services', group: 'Network' },
    { ...podsKind, id: 'problems', title: 'Problems', group: 'Health' },
  ]))
  if (saved) f.client.getNavSections = vi.fn(async () => ({ favorites: true, 'group:Workloads': false, 'group:Network': true }))
  render(<App client={f.client} />)
  await screen.findByRole('grid', { name: 'resources' })
  const nav = within(screen.getByRole('navigation', { name: 'resources' }))
  expect(nav.getByRole('button', { name: /^Workloads \(/ })).toHaveAttribute('aria-expanded', String(!saved))
  expect(nav.getByRole('button', { name: /^Favorites \(/ })).toHaveAttribute('aria-expanded', String(saved))
  expect(nav.getByRole('button', { name: /^Network \(/ })).toHaveAttribute('aria-expanded', String(saved))
  expect(nav.getByRole('button', { name: /^Health \(/ })).toHaveAttribute('aria-expanded', 'false')
  expect(nav.getByRole('button', { name: 'Pods' })).toHaveAttribute('aria-current', 'page')
  expect(f.client.setNavSection).not.toHaveBeenCalled()
})

it('all sections fold globally across targets; search expands temporarily and Enter opens the matching kind', async () => {
  const f = fakeClient([k8s('a'), k8s('b')])
  f.state.view.selected = { provider: 'kubernetes', id: 'a' }
  f.client.listKinds = vi.fn(async () => kindsView([podsKind, { ...podsKind, id: 'jobs', title: 'Jobs' }, { ...podsKind, id: 'services', title: 'Services', group: 'Network' }]))
  f.client.getFavoriteKinds = vi.fn(async () => [{ provider: 'kubernetes', kind: 'services' }])
  render(<App client={f.client} />)
  await screen.findByRole('grid', { name: 'resources' })
  const section = (name: string) => within(screen.getByRole('region', { name }))
  const head = (name: string) => section(name).getByRole('button', { name: new RegExp(`^${name} `) })
  await userEvent.click(head('Workloads'))
  expect(head('Workloads')).toHaveAttribute('aria-expanded', 'false')
  expect(section('Workloads').queryByRole('button', { name: 'Jobs' })).toBeNull()
  expect(section('Workloads').getByRole('button', { name: 'Pods' })).toHaveAttribute('aria-current', 'page')
  head('Favorites').focus()
  await userEvent.keyboard('{ArrowRight}')
  await userEvent.keyboard('{ArrowLeft}')
  expect(section('Favorites').queryByRole('button', { name: 'Services' })).toBeNull()
  await userEvent.click(screen.getByRole('option', { name: 'bb-cluster' }))
  await waitFor(() => expect(f.client.listKinds).toHaveBeenCalledWith('kubernetes', 'b'))
  expect(head('Workloads')).toHaveAttribute('aria-expanded', 'false')
  expect(head('Favorites')).toHaveAttribute('aria-expanded', 'false')
  expect(f.client.setNavSection).toHaveBeenCalledWith('group:Workloads', false)
  expect(f.client.setNavSection).toHaveBeenCalledWith('favorites', false)
  const writes = vi.mocked(f.client.setNavSection).mock.calls.length
  const filter = screen.getByRole('textbox', { name: 'Filter resources' })
  await userEvent.type(filter, 'Jobs')
  expect(head('Workloads')).toHaveAttribute('aria-expanded', 'true')
  expect(section('Workloads').getByRole('button', { name: 'Jobs' })).toBeInTheDocument()
  await userEvent.keyboard('{Enter}')
  expect(await screen.findByRole('heading', { name: 'Jobs' })).toBeInTheDocument()
  await userEvent.clear(filter)
  expect(head('Workloads')).toHaveAttribute('aria-expanded', 'false')
  expect(f.client.setNavSection).toHaveBeenCalledTimes(writes)
  head('Favorites').focus()
  await userEvent.keyboard('{ArrowRight}')
  expect(section('Favorites').getByRole('button', { name: 'Services' })).toBeInTheDocument()
})

it('serializes rapid changes and rolls back only failed writes, keeping queued changes', async () => {
  const f = fakeClient([])
  f.client.getNavSections = vi.fn(async () => ({ 'group:absent': true }))
  let reject!: (e: Error) => void
  const pending = new Promise<void>((_, fail) => { reject = fail })
  const write = vi.fn().mockImplementationOnce(() => pending).mockResolvedValue(undefined)
  f.client.setNavSection = write
  const a = actions(f.client)
  await a.init()
  const first = a.setNavSection('group:Workloads', false)
  const second = a.setNavSection('favorites', false)
  const third = a.setNavSection('group:Workloads', true)
  await act(async () => { await Promise.resolve() })
  expect(write).toHaveBeenCalledTimes(1)
  expect(useStore.getState().navSections).toEqual({ 'group:absent': true, favorites: false, 'group:Workloads': true })
  await act(async () => {
    reject(new Error('disk unavailable'))
    await Promise.all([first, second, third])
  })
  expect(write.mock.calls).toEqual([['group:Workloads', false], ['favorites', false], ['group:Workloads', true]])
  expect(useStore.getState().navSections).toEqual({ 'group:absent': true, favorites: false, 'group:Workloads': true })
  expect(useStore.getState().notice).toBe('Could not save resource section settings')
  write.mockRejectedValueOnce(new Error('disk unavailable'))
  await a.setNavSection('favorites', true)
  expect(useStore.getState().navSections.favorites).toBe(false)
})

it('does not write defaults over an unreadable preference; retry loads the saved expansion', async () => {
  const f = fakeClient([])
  f.client.getNavSections = vi.fn().mockRejectedValueOnce(new Error('disk unavailable')).mockResolvedValue({ favorites: false })
  const a = actions(f.client)
  await a.init()
  await a.setNavSection('favorites', true)
  expect(f.client.setNavSection).not.toHaveBeenCalled()
  expect(useStore.getState().notice).toBe('Could not load resource section settings')
  await a.init()
  expect(useStore.getState().navSections).toEqual({ favorites: false })
  expect(useStore.getState().navSectionsReady).toBe(true)
})
