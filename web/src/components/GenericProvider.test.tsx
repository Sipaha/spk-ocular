import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import { ApiError } from '../api/client'
import type { KindDescriptor, Row, Target } from '../api/types'
import { initialState, useStore } from '../store'
import { kindsView, connectedClient as fakeClient } from '../test/fakeClient'

// A provider that is not Kubernetes: no pods, no events, its own default
// view and scope, its own scope words (or none). The generic UI must take
// all of it from the provider's metadata.

beforeEach(() => useStore.setState({ ...initialState }))

const col = [{ id: 'name', title: 'Name', type: 'text' as const }]
const boxes: KindDescriptor = { id: 'boxes', title: 'Boxes', group: 'Things', scoped: true, columns: col }
const crates: KindDescriptor = { id: 'crates', title: 'Crates', group: 'Things', scoped: true, default: true, columns: col }

const target = (extra: Partial<Target> = {}): Target => ({ provider: 'other', id: 'site', title: 'site', ...extra })

const row = (kind: string, name: string, extra: Partial<Row['ref']> = {}): Row => ({
  id: `id-${name}`,
  ref: { provider: 'other', target: 'site', scope: 'blue', kind, name, uid: `id-${name}`, ...extra },
  cells: [{ text: extra.title ?? name }],
  health: { state: 'ok' },
})

function otherProvider(opts: { scopeNames?: boolean; resync?: boolean; scopesDenied?: boolean; defaultScope?: string } = {}) {
  const f = fakeClient([])
  f.state.view = {
    groups: [
      {
        provider: 'other',
        title: 'Other',
        targets: [target({ defaultScope: opts.defaultScope })],
        problems: [],
        scopeNames: opts.scopeNames
          ? { singular: { key: 'other.scope.singular', text: 'Zone' }, plural: { key: 'other.scope.plural', text: 'zones' }, all: { key: 'other.scope.all', text: 'All zones' } }
          : undefined,
      },
    ],
    selected: { provider: 'other', id: 'site' },
  }
  f.client.listKinds = vi.fn(async () => kindsView([boxes, crates]))
  f.state.kinds = { boxes, crates }
  f.client.listScopes = vi.fn(async () =>
    opts.scopesDenied ? { scopes: [], error: { code: 'forbidden', detail: 'no list' } } : { scopes: [{ name: 'blue' }, { name: 'green' }] },
  )
  const open = f.client.openView
  f.client.openView = vi.fn(async (...a: Parameters<typeof open>) => ({ ...(await open(...a)), resync: !!opts.resync }))
  return f
}

describe('a provider without Kubernetes knowledge', () => {
  it('opens its default view in its default scope, with its own scope words', async () => {
    const f = otherProvider({ scopeNames: true, defaultScope: 'blue' })
    f.state.rowsByKind = { crates: [row('crates', 'c1')] }
    render(<App client={f.client} />)
    const grid = await screen.findByRole('grid', { name: 'resources' })
    expect(await within(grid).findByText('c1')).toBeInTheDocument()
    expect(f.client.openView).toHaveBeenCalledWith('other', 'site', { kind: 'crates', scope: { mode: 'one', name: 'blue' } })
    expect(f.client.openView).not.toHaveBeenCalledWith('other', 'site', expect.objectContaining({ kind: 'pods' }))
    await userEvent.click(screen.getByRole('button', { name: 'Zone' }))
    expect(within(screen.getByRole('listbox', { name: 'Zone' })).getByRole('option', { name: 'All zones' })).toBeInTheDocument()
  })

  it('without a default kind or scope: the first view, all scopes, generic words', async () => {
    const f = otherProvider()
    f.client.listKinds = vi.fn(async () => kindsView([boxes, { ...crates, default: false }]))
    render(<App client={f.client} />)
    await screen.findByRole('grid', { name: 'resources' })
    // The grid is there a moment before the page's effect opens its view.
    await waitFor(() => expect(f.client.openView).toHaveBeenCalledWith('other', 'site', { kind: 'boxes', scope: { mode: 'all' } }))
    expect(screen.getByRole('button', { name: 'Scope' })).toHaveTextContent('All scopes')
  })

  it('scopes that cannot be listed are typed, named by the provider', async () => {
    const f = otherProvider({ scopeNames: true, scopesDenied: true })
    render(<App client={f.client} />)
    await screen.findByRole('grid', { name: 'resources' })
    const input = screen.getByRole('textbox', { name: 'Zone' })
    expect(input).toHaveAttribute('placeholder', 'Type a zone')
    expect(input).toHaveAttribute('title', 'Cannot list zones: no list. Separate multiple names with commas')
    await userEvent.type(input, 'green{Enter}')
    expect(f.client.openView).toHaveBeenLastCalledWith('other', 'site', { kind: 'crates', scope: { mode: 'one', name: 'green' } })
  })

  it('details have no events section when the kind names no events kind', async () => {
    const f = otherProvider()
    f.state.rowsByKind = { crates: [row('crates', 'c1')] }
    render(<App client={f.client} />)
    const grid = await screen.findByRole('grid', { name: 'resources' })
    await userEvent.dblClick(await within(grid).findByText('c1'))
    await screen.findByRole('dialog', { name: /crates/ })
    await waitFor(() => expect(f.client.getResource).toHaveBeenCalled())
    expect(screen.queryByRole('region', { name: 'Events' })).not.toBeInTheDocument()
    expect(f.client.openView).not.toHaveBeenCalledWith('other', 'site', expect.objectContaining({ kind: 'events' }))
  })

  it('an object is shown by its title, opened by its key', async () => {
    const f = otherProvider()
    f.state.rowsByKind = { crates: [row('crates', '3f2a9c', { title: 'web-1' })] }
    render(<App client={f.client} />)
    const grid = await screen.findByRole('grid', { name: 'resources' })
    await userEvent.dblClick(await within(grid).findByText('web-1'))
    const drawer = await screen.findByRole('dialog', { name: 'crates web-1' })
    expect(within(drawer).getByRole('heading', { name: 'web-1' })).toBeInTheDocument()
    expect(f.client.getResource).toHaveBeenCalledWith(expect.objectContaining({ name: '3f2a9c', title: 'web-1' }))
    await waitFor(() => expect(f.client.touchRecent).toHaveBeenCalledWith(expect.objectContaining({ name: '3f2a9c' }), 'web-1'))
  })

  it('Read again (F5) goes to the open view, only where the session offers it', async () => {
    const f = otherProvider({ resync: true })
    render(<App client={f.client} />)
    await screen.findByRole('grid', { name: 'resources' })
    const button = await screen.findByRole('button', { name: /Read again/ })
    await userEvent.click(button)
    expect(f.client.resyncView).toHaveBeenCalledWith('v-crates')
    await waitFor(() => expect(button).toBeEnabled())
    const ev = new KeyboardEvent('keydown', { key: 'F5', bubbles: true, cancelable: true })
    await act(async () => void window.dispatchEvent(ev))
    expect(ev.defaultPrevented).toBe(true)
    expect(f.client.resyncView).toHaveBeenCalledTimes(2)
  })

  it('F5 is the page’s own where no view can read again', async () => {
    const f = otherProvider()
    render(<App client={f.client} />)
    await screen.findByRole('grid', { name: 'resources' })
    expect(screen.queryByRole('button', { name: /Read again/ })).not.toBeInTheDocument()
    const ev = new KeyboardEvent('keydown', { key: 'F5', bubbles: true, cancelable: true })
    await act(async () => void window.dispatchEvent(ev))
    expect(ev.defaultPrevented).toBe(false)
    expect(f.client.resyncView).not.toHaveBeenCalled()
  })

  it('F5 in a text field of such a view still reads again; a failure is told', async () => {
    const f = otherProvider({ resync: true })
    f.client.resyncView = vi.fn(async () => {
      throw new ApiError('unavailable', 'daemon down')
    })
    render(<App client={f.client} />)
    await screen.findByRole('grid', { name: 'resources' })
    await screen.findByRole('button', { name: /Read again/ })
    const filter = screen.getByRole('textbox', { name: 'Filter rows' })
    filter.focus()
    fireEvent.keyDown(filter, { key: 'F5' })
    await waitFor(() => expect(f.client.resyncView).toHaveBeenCalledWith('v-crates'))
    await waitFor(() => expect(useStore.getState().notice).toBe('Could not read again: unavailable'))
  })
})
