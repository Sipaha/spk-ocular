import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { App } from '../App'
import { actions, initialState, useStore } from '../store'
import { connectedClient as fakeClient, k8s, kindsView, podsKind } from '../test/fakeClient'

beforeEach(() => useStore.setState({ ...initialState }))

it('favorites are shared across connections, preserve the current page, and can be removed from the section or keyboard menu', async () => {
  const f = fakeClient([k8s('a'), k8s('b')])
  f.state.view.selected = { provider: 'kubernetes', id: 'a' }
  render(<App client={f.client} />)
  await screen.findByRole('grid', { name: 'resources' })
  const nav = screen.getByRole('navigation', { name: 'resources' })
  const favorites = within(nav).getByRole('region', { name: 'Favorites' })
  const add = within(nav).getByRole('button', { name: 'Add Pods to favorites' })
  await userEvent.click(add)
  expect(within(favorites).getByRole('button', { name: 'Pods' })).toHaveAttribute('aria-current', 'page')
  expect(within(nav).getAllByRole('button', { name: 'Pods' })).toHaveLength(1)
  expect(within(nav).queryByRole('region', { name: 'Workloads' })).toBeNull()
  expect(f.client.setKindFavorite).toHaveBeenCalledWith('kubernetes', 'pods', true)
  await userEvent.click(screen.getByRole('option', { name: 'bb-cluster' }))
  await waitFor(() => expect(f.client.listKinds).toHaveBeenCalledWith('kubernetes', 'b'))
  const otherFavorites = screen.getByRole('region', { name: 'Favorites' })
  expect(within(screen.getByRole('navigation', { name: 'resources' })).getAllByRole('button', { name: 'Pods' })).toHaveLength(1)
  await userEvent.click(within(otherFavorites).getByRole('button', { name: 'Remove Pods from favorites' }))
  expect(within(otherFavorites).queryByRole('button', { name: 'Pods' })).toBeNull()
  expect(within(screen.getByRole('region', { name: 'Workloads' })).getByRole('button', { name: 'Pods' })).toHaveAttribute('aria-current', 'page')
  const pods = screen.getByRole('navigation', { name: 'resources' }).querySelector<HTMLElement>('[data-nav-item][aria-current=page]')!
  pods.focus()
  await userEvent.keyboard('{Shift>}{F10}{/Shift}')
  await userEvent.click(screen.getByRole('menuitem', { name: 'Add Pods to favorites' }))
  expect(within(otherFavorites).getByRole('button', { name: 'Pods' })).toBeInTheDocument()
  expect(screen.queryByRole('region', { name: 'Workloads' })).toBeNull()
})

it('keeps unavailable favorites remembered and never confuses equal kind ids of different providers', async () => {
  const f = fakeClient([k8s('a')])
  f.state.view.selected = { provider: 'kubernetes', id: 'a' }
  f.client.getFavoriteKinds = vi.fn(async () => [{ provider: 'compose', kind: 'pods' }, { provider: 'kubernetes', kind: 'custom/things' }])
  f.client.listKinds = vi.fn(async () => kindsView([podsKind]))
  render(<App client={f.client} />)
  await screen.findByRole('grid', { name: 'resources' })
  expect(screen.getByRole('region', { name: 'Favorites' }).querySelector('.nav-item')).toBeNull()
  expect(useStore.getState().favoriteKinds).toHaveLength(2)
  expect(f.client.setKindFavorite).not.toHaveBeenCalled()
})

it('omits favorite kinds from collapsed API groups and restores their active row and count on removal', async () => {
  const f = fakeClient([k8s('a')])
  f.state.view.selected = { provider: 'kubernetes', id: 'a' }
  const widgets = { ...podsKind, id: 'example.io/widgets', title: 'Widgets', group: 'API groups', subgroup: 'example.io' }
  const gadgets = { ...widgets, id: 'example.io/gadgets', title: 'Gadgets' }
  f.client.listKinds = vi.fn(async () => kindsView([widgets, gadgets]))
  f.client.getFavoriteKinds = vi.fn(async () => [widgets, gadgets].map((k) => ({ provider: 'kubernetes', kind: k.id })))
  render(<App client={f.client} />)
  await screen.findByRole('grid', { name: 'resources' })
  const nav = screen.getByRole('navigation', { name: 'resources' })
  expect(within(nav).queryByRole('region', { name: 'API groups' })).toBeNull()
  const favorites = within(nav).getByRole('region', { name: 'Favorites' })
  await userEvent.click(within(favorites).getByRole('button', { name: 'Remove Widgets from favorites' }))
  const group = within(nav).getByRole('region', { name: 'API groups' })
  expect(within(group).getByRole('button', { name: /API groups/ })).toHaveTextContent('1')
  expect(within(group).getByRole('button', { name: 'Widgets' })).toHaveAttribute('aria-current', 'page')
  expect(within(favorites).queryByRole('button', { name: 'Widgets' })).toBeNull()
  expect(within(group).queryByRole('button', { name: 'Gadgets' })).toBeNull()
  await userEvent.click(within(group).getByRole('button', { name: 'Add Widgets to favorites' }))
  expect(within(nav).queryByRole('region', { name: 'API groups' })).toBeNull()
  expect(within(favorites).getByRole('button', { name: 'Widgets' })).toHaveAttribute('aria-current', 'page')
})

it('failed rapid add/remove restores the last confirmed membership and leaves other favorites intact', async () => {
  const f = fakeClient([])
  f.client.setKindFavorite = vi.fn(async () => { throw new Error('disk unavailable') })
  const a = actions(f.client)
  await a.init()
  useStore.setState({ favoriteKinds: [{ provider: 'kubernetes', kind: 'services' }] })
  await act(async () => {
    const first = a.setKindFavorite('kubernetes', 'pods', true)
    const last = a.setKindFavorite('kubernetes', 'pods', false)
    await Promise.all([first, last])
  })
  expect(useStore.getState().favoriteKinds).toEqual([{ provider: 'kubernetes', kind: 'services' }])
  expect(useStore.getState().notice).toBe('Could not save favorites')
})

it('filters kinds by name and alias, searches favorites too, and does not change the open table while typing', async () => {
  const f = fakeClient([k8s('a')])
  f.state.view.selected = { provider: 'kubernetes', id: 'a' }
  const deploy = { ...podsKind, id: 'deployments', title: 'Deployments', aliases: ['deploy'], group: 'Workloads' }
  f.client.listKinds = vi.fn(async () => kindsView([podsKind, deploy]))
  f.client.getFavoriteKinds = vi.fn(async () => [{ provider: 'kubernetes', kind: 'pods' }])
  render(<App client={f.client} />)
  await screen.findByRole('grid', { name: 'resources' })
  const filter = screen.getByRole('textbox', { name: 'Filter resources' })
  const nav = screen.getByRole('navigation', { name: 'resources' })
  await userEvent.click(within(nav).getByRole('button', { name: /^Favorites \(/ }))
  await userEvent.type(filter, 'deploy')
  expect(within(nav).queryByRole('button', { name: 'Pods' })).toBeNull()
  expect(within(nav).getByRole('button', { name: 'Deployments' })).toBeInTheDocument()
  expect(screen.getByRole('heading', { name: 'Pods' })).toBeInTheDocument()
  await userEvent.keyboard('{Enter}')
  expect(await screen.findByRole('heading', { name: 'Deployments' })).toBeInTheDocument()
  await userEvent.click(filter)
  await userEvent.keyboard('{Escape}')
  expect(filter).toHaveValue('')
  expect(within(screen.getByRole('region', { name: 'Favorites' })).getByRole('button', { name: 'Pods' })).toBeInTheDocument()
  await userEvent.type(filter, 'pods')
  expect(within(nav).getAllByRole('button', { name: 'Pods' })).toHaveLength(1)
  expect(within(nav).queryByRole('region', { name: 'Workloads' })).toBeNull()
  await userEvent.keyboard('{Enter}')
  expect(await screen.findByRole('heading', { name: 'Pods' })).toBeInTheDocument()
})

it('uses the saved favorite order and lets the keyboard move an item without opening it', async () => {
  const f = fakeClient([k8s('a')])
  f.state.view.selected = { provider: 'kubernetes', id: 'a' }
  f.client.listKinds = vi.fn(async () => kindsView([podsKind, { ...podsKind, id: 'services', title: 'Services' }]))
  f.client.getFavoriteKinds = vi.fn(async () => [{ provider: 'kubernetes', kind: 'services' }, { provider: 'kubernetes', kind: 'pods' }])
  render(<App client={f.client} />)
  await screen.findByRole('grid', { name: 'resources' })
  const fav = screen.getByRole('region', { name: 'Favorites' })
  const order = () => [...fav.querySelectorAll('.nav-item')].map((e) => e.textContent)
  await userEvent.click(within(fav).getByRole('button', { name: /^Favorites \(/ }))
  expect(order()).toEqual(['Services', 'Pods'])
  within(fav).getByRole('button', { name: 'Pods' }).focus()
  await userEvent.keyboard('{Shift>}{F10}{/Shift}')
  await userEvent.click(screen.getByRole('menuitem', { name: 'Move up' }))
  expect(order()).toEqual(['Pods', 'Services'])
  expect(f.client.moveFavoriteKind).toHaveBeenCalledWith('kubernetes', 'pods', 'services')
  expect(screen.getByRole('heading', { name: 'Pods' })).toBeInTheDocument()
})
