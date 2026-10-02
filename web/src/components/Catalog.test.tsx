import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import { ApiError } from '../api/client'
import type { KindDescriptor, KindsView } from '../api/types'
import { initialState, useStore } from '../store'
import { fakeClient, k8s, podsKind } from '../test/fakeClient'

beforeEach(() => useStore.setState({ ...initialState }))

const discovered = (id: string, title: string, subgroup: string, extra: Partial<KindDescriptor> = {}): KindDescriptor => ({
  id, title, group: 'API groups', subgroup, scoped: true, columns: [{ id: 'name', title: 'Name', type: 'text' }], ...extra,
})
const widgets = discovered('ocular.dev/widgets', 'Widgets', 'ocular.dev', { aliases: ['wd', 'widget', 'widgets'] })
const gadgets = discovered('ocular.dev/gadgets', 'Gadgets', 'ocular.dev', { scoped: false })
// A well-known built-in the provider places in a section (not API groups).
const jobs: KindDescriptor = { ...discovered('batch/jobs', 'Jobs', ''), group: 'Workloads', subgroup: undefined }
const foos = discovered('x.io/foos', 'Foos', 'x.io')

const catalog = (kinds: KindDescriptor[], over: Partial<KindsView> = {}): KindsView => ({ kinds, rev: 1, state: 'ready', session: 1, ...over })

async function open(f: ReturnType<typeof fakeClient>) {
  f.state.view.selected = { provider: 'kubernetes', id: 'prod' }
  render(<App client={f.client} />)
  return await screen.findByRole('navigation', { name: 'resources' })
}

describe('navigation of discovered kinds', () => {
  it('API groups fold whole, folded by default; inside, groups by API group; one kind has no level; the header counts kinds', async () => {
    const f = fakeClient([k8s('prod')])
    f.client.listKinds = vi.fn(async () => catalog([podsKind, jobs, widgets, gadgets, foos]))
    const nav = await open(f)
    // Placed kinds sit in their section.
    const workloads = await within(nav).findByRole('region', { name: 'Workloads' })
    expect(within(workloads).getByRole('button', { name: 'Jobs' })).toBeInTheDocument()
    const section = within(nav).getByRole('region', { name: 'API groups' })
    const head = within(section).getByRole('button', { name: /API groups/ })
    expect(head).toHaveAttribute('aria-expanded', 'false')
    expect(head).toHaveTextContent('3')
    expect(within(section).queryByRole('button', { name: /ocular\.dev/ })).not.toBeInTheDocument()
    expect(within(section).queryByRole('button', { name: 'Foos' })).not.toBeInTheDocument()

    await userEvent.click(head)
    expect(head).toHaveAttribute('aria-expanded', 'true')
    expect(f.client.setTargetState).toHaveBeenLastCalledWith('kubernetes', 'prod', 'navOpen', JSON.stringify(['group:API groups']))
    const sub = within(section).getByRole('button', { name: /ocular\.dev/ })
    expect(sub).toHaveAttribute('aria-expanded', 'false')
    expect(within(section).queryByRole('button', { name: 'Widgets' })).not.toBeInTheDocument()
    expect(within(section).getByRole('button', { name: 'Foos' })).toBeInTheDocument() // one kind: no level

    await userEvent.click(sub)
    expect(sub).toHaveAttribute('aria-expanded', 'true')
    await userEvent.click(within(section).getByRole('button', { name: 'Widgets' }))
    expect(f.client.setTargetState).toHaveBeenCalledWith('kubernetes', 'prod', 'navOpen', JSON.stringify(['group:API groups', 'API groups/ocular.dev']))
    expect(f.client.openView).toHaveBeenLastCalledWith('kubernetes', 'prod', expect.objectContaining({ kind: 'ocular.dev/widgets' }))
    // Folded again, the open kind stays in sight.
    await userEvent.click(head)
    expect(within(section).getByRole('button', { name: 'Widgets' })).toHaveAttribute('aria-current', 'page')
    expect(within(section).queryByRole('button', { name: 'Gadgets' })).not.toBeInTheDocument()
  })

  it('remembers what was expanded; a collapsed subgroup still shows the open kind', async () => {
    const f = fakeClient([k8s('prod')])
    f.client.listKinds = vi.fn(async () => catalog([podsKind, widgets, gadgets, discovered('x.io/foos', 'Foos', 'x.io'), discovered('x.io/bars', 'Bars', 'x.io')]))
    f.client.getTargetState = vi.fn(async () => ({ navOpen: JSON.stringify(['group:API groups', 'API groups/x.io']), kind: JSON.stringify('ocular.dev/widgets') }))
    const nav = await open(f)
    const ocular = await within(nav).findByRole('group', { name: 'ocular.dev' })
    expect(within(ocular).getByRole('button', { name: /^ocular\.dev/ })).toHaveAttribute('aria-expanded', 'false')
    expect(within(ocular).getByRole('button', { name: 'Widgets' })).toHaveAttribute('aria-current', 'page')
    expect(within(ocular).queryByRole('button', { name: 'Gadgets' })).not.toBeInTheDocument()
    const x = within(nav).getByRole('group', { name: 'x.io' })
    expect(within(x).getByRole('button', { name: 'Foos' })).toBeInTheDocument()
  })

  it('→ expands and ← collapses a subgroup from the keyboard', async () => {
    const f = fakeClient([k8s('prod')])
    f.client.listKinds = vi.fn(async () => catalog([podsKind, widgets, gadgets]))
    f.client.getTargetState = vi.fn(async () => ({ navOpen: JSON.stringify(['group:API groups']) }))
    const nav = await open(f)
    const sub = await within(nav).findByRole('button', { name: /^ocular\.dev/ })
    sub.focus()
    await userEvent.keyboard('{ArrowRight}')
    expect(sub).toHaveAttribute('aria-expanded', 'true')
    await userEvent.keyboard('{ArrowLeft}')
    expect(sub).toHaveAttribute('aria-expanded', 'false')
  })
})

describe('the catalog in an open target', () => {
  it('says what it cannot vouch for: discovering, unconfirmed groups, failed', async () => {
    const f = fakeClient([k8s('prod')])
    const listKinds = vi.fn(async () => catalog([podsKind], { state: 'discovering' }))
    f.client.listKinds = listKinds
    const nav = await open(f)
    expect(await within(nav).findByRole('note', { name: 'catalog' })).toHaveTextContent('Looking for API resources…')

    listKinds.mockResolvedValue(catalog([podsKind, widgets], { rev: 2, state: 'partial', unconfirmed: ['metrics.k8s.io', '*'] }))
    await act(async () => f.emit({ type: 'kinds_changed', payload: { provider: 'kubernetes', target: 'prod', session: 1, rev: 2 } }))
    await waitFor(() => expect(within(nav).getByRole('note', { name: 'catalog' })).toHaveTextContent('Not confirmed: metrics.k8s.io, all groups'))

    listKinds.mockResolvedValue(catalog([podsKind, widgets], { rev: 3, state: 'failed' }))
    await act(async () => f.emit({ type: 'kinds_changed', payload: { provider: 'kubernetes', target: 'prod', session: 1, rev: 3 } }))
    await waitFor(() => expect(within(nav).getByRole('note', { name: 'catalog' })).toHaveTextContent('API resources could not be listed'))

    listKinds.mockResolvedValue(catalog([podsKind, widgets], { rev: 4 }))
    await act(async () => f.emit({ type: 'kinds_changed', payload: { provider: 'kubernetes', target: 'prod', session: 1, rev: 4 } }))
    await waitFor(() => expect(within(nav).queryByRole('note', { name: 'catalog' })).not.toBeInTheDocument())
  })

  it('lists again on this target’s newer revision and on resync; others’ and old revisions are ignored', async () => {
    const f = fakeClient([k8s('prod'), k8s('dev')])
    const listKinds = vi.fn(async () => catalog([podsKind], { rev: 5 }))
    f.client.listKinds = listKinds
    const nav = await open(f)
    await waitFor(() => expect(listKinds).toHaveBeenCalledTimes(1))
    await act(async () => f.emit({ type: 'kinds_changed', payload: { provider: 'kubernetes', target: 'dev', session: 1, rev: 9 } }))
    await act(async () => f.emit({ type: 'kinds_changed', payload: { provider: 'kubernetes', target: 'prod', session: 1, rev: 5 } }))
    expect(listKinds).toHaveBeenCalledTimes(1)

    listKinds.mockResolvedValue(catalog([podsKind, jobs], { rev: 6 }))
    await act(async () => f.emit({ type: 'kinds_changed', payload: { provider: 'kubernetes', target: 'prod', session: 1, rev: 6 } }))
    expect(await within(nav).findByRole('button', { name: 'Jobs' })).toBeInTheDocument()

    // a new session (the target reconfigured) starts its revisions anew
    listKinds.mockResolvedValue(catalog([podsKind], { rev: 1, session: 2 }))
    await act(async () => f.emit({ type: 'kinds_changed', payload: { provider: 'kubernetes', target: 'prod', session: 2, rev: 1 } }))
    await waitFor(() => expect(within(nav).queryByRole('button', { name: 'Jobs' })).not.toBeInTheDocument())

    // a lost event: resync lists again
    listKinds.mockResolvedValue(catalog([podsKind, jobs], { rev: 2, session: 2 }))
    await act(async () => f.emit({ type: 'resync' }))
    expect(await within(nav).findByRole('button', { name: 'Jobs' })).toBeInTheDocument()
  })

  it('the latest listing wins over an older answer that comes late', async () => {
    const f = fakeClient([k8s('prod')])
    let release!: (v: KindsView) => void
    const listKinds = vi.fn()
      .mockResolvedValueOnce(catalog([podsKind], { rev: 1 }))
      .mockImplementationOnce(() => new Promise<KindsView>((r) => (release = r)))
      .mockResolvedValueOnce(catalog([podsKind, jobs], { rev: 3 }))
    f.client.listKinds = listKinds
    const nav = await open(f)
    await waitFor(() => expect(listKinds).toHaveBeenCalledTimes(1))
    await act(async () => f.emit({ type: 'kinds_changed', payload: { provider: 'kubernetes', target: 'prod', session: 1, rev: 2 } }))
    await act(async () => f.emit({ type: 'kinds_changed', payload: { provider: 'kubernetes', target: 'prod', session: 1, rev: 3 } }))
    expect(await within(nav).findByRole('button', { name: 'Jobs' })).toBeInTheDocument()
    await act(async () => release(catalog([podsKind], { rev: 2 })))
    expect(within(nav).getByRole('button', { name: 'Jobs' })).toBeInTheDocument()
  })

  it('F5 in the navigation reads the kinds again (the target discovers anew)', async () => {
    const f = fakeClient([k8s('prod')])
    const nav = await open(f)
    const pods = await within(nav).findByRole('button', { name: 'Pods' })
    await waitFor(() => expect(f.client.listKinds).toHaveBeenCalledTimes(1))
    pods.focus()
    await userEvent.keyboard('{F5}')
    expect(f.client.refreshKinds).toHaveBeenCalledWith('kubernetes', 'prod')
    await waitFor(() => expect(f.client.listKinds).toHaveBeenCalledTimes(2))
    await userEvent.click(within(nav).getByRole('button', { name: /Refresh the list/ }))
    expect(f.client.refreshKinds).toHaveBeenCalledTimes(2)
  })

  it('a kind no longer served keeps its page, which says so; the navigation drops it', async () => {
    const f = fakeClient([k8s('prod')])
    const listKinds = vi.fn(async () => catalog([podsKind, jobs]))
    f.client.listKinds = listKinds
    f.client.getTargetState = vi.fn(async () => ({ kind: JSON.stringify('batch/jobs') }))
    const nav = await open(f)
    await screen.findByRole('heading', { name: 'Jobs' })
    listKinds.mockResolvedValue(catalog([podsKind], { rev: 2 }))
    f.state.statusByKind['batch/jobs'] = { state: 'error', class: 'removed', message: 'Jobs are no longer served by the API' }
    await act(async () => f.emit({ type: 'kinds_changed', payload: { provider: 'kubernetes', target: 'prod', session: 1, rev: 2 } }))
    await act(async () => f.emit({ type: 'view_changed', payload: { viewId: 'v-batch/jobs', version: 50 } }))
    await waitFor(() => expect(within(nav).queryByRole('button', { name: 'Jobs' })).not.toBeInTheDocument())
    expect(screen.getByRole('heading', { name: 'Jobs' })).toBeInTheDocument()
    expect(await screen.findByRole('alert')).toHaveTextContent('no longer served by the API')
  })

  it('a remembered discovered kind waits for discovery instead of showing an empty state', async () => {
    const f = fakeClient([k8s('prod')])
    const listKinds = vi.fn(async () => catalog([podsKind], { state: 'discovering' }))
    f.client.listKinds = listKinds
    f.client.getTargetState = vi.fn(async () => ({ kind: JSON.stringify('ocular.dev/widgets') }))
    await open(f)
    expect(await screen.findByText('Loading…')).toBeInTheDocument()
    expect(f.client.openView).not.toHaveBeenCalled()
    listKinds.mockResolvedValue(catalog([podsKind, widgets], { rev: 2 }))
    await act(async () => f.emit({ type: 'kinds_changed', payload: { provider: 'kubernetes', target: 'prod', session: 1, rev: 2 } }))
    expect(await screen.findByRole('heading', { name: 'Widgets' })).toBeInTheDocument()
    expect(f.client.openView).toHaveBeenLastCalledWith('kubernetes', 'prod', expect.objectContaining({ kind: 'ocular.dev/widgets' }))
  })
})

describe('the navigation follows the open view', () => {
  it('a view opened elsewhere (the palette) is scrolled into the navigation', async () => {
    const f = fakeClient([k8s('prod')])
    f.client.listKinds = vi.fn(async () => catalog([podsKind, widgets, gadgets]))
    const nav = await open(f)
    const spy = vi.spyOn(Element.prototype, 'scrollIntoView')
    await userEvent.keyboard('{Control>}k{/Control}')
    await userEvent.keyboard(':wd{Enter}')
    const item = await within(nav).findByRole('button', { name: 'Widgets' })
    await waitFor(() => expect(spy.mock.contexts).toContain(item))
    spy.mockRestore()
  })
})

describe('a kind that comes back', () => {
  it('a new session still discovering does not strand the open discovered kind', async () => {
    const f = fakeClient([k8s('prod')])
    const listKinds = vi.fn(async () => catalog([podsKind, jobs]))
    f.client.listKinds = listKinds
    f.client.getTargetState = vi.fn(async () => ({ kind: JSON.stringify('batch/jobs') }))
    const nav = await open(f)
    await screen.findByRole('heading', { name: 'Jobs' })
    const opens = () => (f.client.openView as ReturnType<typeof vi.fn>).mock.calls.filter((c) => c[2].kind === 'batch/jobs').length
    const before = opens()
    // the target was reconfigured: session 2 knows only the described kinds yet
    listKinds.mockResolvedValue(catalog([podsKind], { session: 2, state: 'discovering' }))
    await act(async () => f.emit({ type: 'kinds_changed', payload: { provider: 'kubernetes', target: 'prod', session: 2, rev: 1 } }))
    expect(await screen.findByText('Loading…')).toBeInTheDocument()
    listKinds.mockResolvedValue(catalog([podsKind, jobs], { session: 2, rev: 2 }))
    await act(async () => f.emit({ type: 'kinds_changed', payload: { provider: 'kubernetes', target: 'prod', session: 2, rev: 2 } }))
    expect(await within(nav).findByRole('button', { name: 'Jobs' })).toBeInTheDocument()
    await waitFor(() => expect(opens()).toBeGreaterThan(before))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('a removed kind served again opens a fresh view', async () => {
    const f = fakeClient([k8s('prod')])
    const listKinds = vi.fn(async () => catalog([podsKind, jobs]))
    f.client.listKinds = listKinds
    f.client.getTargetState = vi.fn(async () => ({ kind: JSON.stringify('batch/jobs') }))
    const nav = await open(f)
    await screen.findByRole('heading', { name: 'Jobs' })
    listKinds.mockResolvedValue(catalog([podsKind], { rev: 2 }))
    f.state.statusByKind['batch/jobs'] = { state: 'error', class: 'removed', message: 'Jobs are no longer served by the API' }
    await act(async () => f.emit({ type: 'kinds_changed', payload: { provider: 'kubernetes', target: 'prod', session: 1, rev: 2 } }))
    await act(async () => f.emit({ type: 'view_changed', payload: { viewId: 'v-batch/jobs', version: 50 } }))
    expect(await screen.findByRole('alert')).toHaveTextContent('no longer served by the API')
    delete f.state.statusByKind['batch/jobs']
    listKinds.mockResolvedValue(catalog([podsKind, jobs], { rev: 3 }))
    await act(async () => f.emit({ type: 'kinds_changed', payload: { provider: 'kubernetes', target: 'prod', session: 1, rev: 3 } }))
    expect(await within(nav).findByRole('button', { name: 'Jobs' })).toBeInTheDocument()
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
  })
})

describe('a page whose view gave up', () => {
  const jobsOpens = (f: ReturnType<typeof fakeClient>) => (f.client.openView as ReturnType<typeof vi.fn>).mock.calls.filter((c) => c[2].kind === 'batch/jobs').length
  const unsupported = () => new ApiError('unsupported', 'unknown kind batch/jobs')

  it('a new session listing the kind again opens a fresh view, even with the same kinds', async () => {
    const f = fakeClient([k8s('prod')])
    const listKinds = vi.fn(async () => catalog([podsKind, jobs]))
    f.client.listKinds = listKinds
    f.client.getTargetState = vi.fn(async () => ({ kind: JSON.stringify('batch/jobs') }))
    await open(f)
    await screen.findByRole('heading', { name: 'Jobs' })
    await waitFor(() => expect(jobsOpens(f)).toBe(1))
    // The target was reconfigured: the old view is gone and reopens in session 2
    // before it discovered Jobs; the UI never sees session 2 without them.
    const openView = f.client.openView as ReturnType<typeof vi.fn>
    openView.mockRejectedValueOnce(unsupported()).mockRejectedValueOnce(unsupported())
    await act(async () => f.emit({ type: 'view_changed', payload: { viewId: 'v-batch/jobs', gone: true } }))
    expect(await screen.findByRole('alert')).toHaveTextContent('unknown kind')
    listKinds.mockResolvedValue(catalog([podsKind, jobs], { session: 2, rev: 2 }))
    await act(async () => f.emit({ type: 'kinds_changed', payload: { provider: 'kubernetes', target: 'prod', session: 2, rev: 2 } }))
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
    expect(screen.getByRole('heading', { name: 'Jobs' })).toBeInTheDocument()
  })

  it('a view that failed after the listing that serves its kind opens once more (bounded, no polling)', async () => {
    const f = fakeClient([k8s('prod')])
    const listKinds = vi.fn(async () => catalog([podsKind, jobs]))
    f.client.listKinds = listKinds
    f.client.getTargetState = vi.fn(async () => ({ kind: JSON.stringify('batch/jobs') }))
    await open(f)
    await screen.findByRole('heading', { name: 'Jobs' })
    listKinds.mockResolvedValue(catalog([podsKind, jobs], { session: 2, rev: 1 }))
    await act(async () => f.emit({ type: 'kinds_changed', payload: { provider: 'kubernetes', target: 'prod', session: 2, rev: 1 } }))
    await waitFor(() => expect(listKinds).toHaveBeenCalledTimes(2))
    const before = jobsOpens(f)
    const openView = f.client.openView as ReturnType<typeof vi.fn>
    openView.mockRejectedValueOnce(unsupported())
    await act(async () => f.emit({ type: 'view_changed', payload: { viewId: 'v-batch/jobs', gone: true } }))
    await waitFor(() => expect(jobsOpens(f)).toBe(before + 2))
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
    // Failing again with the same listing: the page says so and stops.
    openView.mockRejectedValue(unsupported())
    await act(async () => f.emit({ type: 'view_changed', payload: { viewId: 'v-batch/jobs', gone: true } }))
    expect(await screen.findByRole('alert')).toHaveTextContent('unknown kind')
    const settled = jobsOpens(f)
    await act(async () => new Promise((r) => setTimeout(r, 50)))
    expect(jobsOpens(f)).toBeLessThanOrEqual(settled + 1)
    const last = jobsOpens(f)
    await act(async () => new Promise((r) => setTimeout(r, 50)))
    expect(jobsOpens(f)).toBe(last)
  })

  it('clicking a served kind whose view ended "removed" opens a new view (the catalog never showed it gone)', async () => {
    const f = fakeClient([k8s('prod')])
    f.client.listKinds = vi.fn(async () => catalog([podsKind, jobs]))
    f.client.getTargetState = vi.fn(async () => ({ kind: JSON.stringify('batch/jobs') }))
    const nav = await open(f)
    await screen.findByRole('heading', { name: 'Jobs' })
    // Deleted and created again, coalesced: no listing without Jobs.
    f.state.statusByKind['batch/jobs'] = { state: 'error', class: 'removed', message: 'Jobs are no longer served by the API' }
    await act(async () => f.emit({ type: 'view_changed', payload: { viewId: 'v-batch/jobs', version: 50 } }))
    expect(await screen.findByRole('alert')).toHaveTextContent('no longer served by the API')
    await act(async () => new Promise((r) => setTimeout(r, 50)))
    expect(screen.getByRole('alert')).toHaveTextContent('no longer served by the API')
    const before = jobsOpens(f)
    delete f.state.statusByKind['batch/jobs']
    await userEvent.click(within(nav).getByRole('button', { name: 'Jobs' }))
    await waitFor(() => expect(jobsOpens(f)).toBe(before + 1))
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
  })

  it('the palette opening the kind of a page whose view gave up opens a new view too', async () => {
    const f = fakeClient([k8s('prod')])
    f.client.listKinds = vi.fn(async () => catalog([podsKind, jobs]))
    f.client.getTargetState = vi.fn(async () => ({ kind: JSON.stringify('batch/jobs') }))
    await open(f)
    await screen.findByRole('heading', { name: 'Jobs' })
    f.state.statusByKind['batch/jobs'] = { state: 'error', class: 'removed', message: 'Jobs are no longer served by the API' }
    await act(async () => f.emit({ type: 'view_changed', payload: { viewId: 'v-batch/jobs', version: 50 } }))
    expect(await screen.findByRole('alert')).toHaveTextContent('no longer served by the API')
    await act(async () => new Promise((r) => setTimeout(r, 50)))
    const before = jobsOpens(f)
    delete f.state.statusByKind['batch/jobs']
    await userEvent.keyboard('{Control>}k{/Control}')
    await userEvent.keyboard(':jobs{Enter}')
    await waitFor(() => expect(jobsOpens(f)).toBe(before + 1))
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
  })
})
