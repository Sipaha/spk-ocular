import { fireEvent, render, screen, within } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { App } from '../App'
import type { ActionDescriptor, KindDescriptor, Row } from '../api/types'
import { initialState, useStore } from '../store'
import { kindsView, fakeClient, k8s, podsKind } from '../test/fakeClient'

beforeEach(() => useStore.setState({ ...initialState }))

const del: ActionDescriptor = { id: 'delete', title: 'Delete', destructive: true }
const pods: KindDescriptor = { ...podsKind, logs: true, actions: [del] }
const events: KindDescriptor = { id: 'events', title: 'Events', group: 'Cluster', scoped: true, columns: [{ id: 'reason', title: 'Reason', type: 'text' }] }
const problems: KindDescriptor = {
  id: 'problems',
  title: 'Problems',
  group: 'Health',
  scoped: true,
  sort: { column: 'severity', desc: true, then: 'since' },
  notCovered: ['Jobs', 'custom resources'],
  columns: [
    { id: 'severity', title: 'Severity', type: 'status' },
    { id: 'kind', title: 'Kind', type: 'text' },
    { id: 'namespace', title: 'Namespace', type: 'text', scopeColumn: true },
    { id: 'name', title: 'Name', type: 'text' },
    { id: 'reason', title: 'Reason', type: 'text' },
    { id: 'message', title: 'Message', type: 'text' },
    { id: 'since', title: 'Since', type: 'age' },
  ],
}

const now = Date.now()
function problem(id: string, kind: string, name: string, severity: string, rank: number, sinceMin: number, extra: Partial<Row> = {}): Row {
  const state = severity === 'recent' ? 'warning' : (severity as Row['health']['state'])
  return {
    id,
    ref: { provider: 'kubernetes', target: 'prod', scope: 'web', kind, name, uid: `u-${name}` },
    cells: [
      { text: severity, num: rank, muted: severity === 'recent' || undefined },
      { text: kind === 'events' ? 'Event' : 'Pod' },
      { text: 'web' },
      { text: name },
      { text: 'Reason' },
      { text: 'message' },
      { time: now - sinceMin * 60_000 },
    ],
    health: { state, reason: 'Reason' },
    ...extra,
  }
}

async function openProblems(rows: Row[], status: object = { state: 'ready' }) {
  const f = fakeClient([k8s('prod')])
  f.state.view.selected = { provider: 'kubernetes', id: 'prod' }
  f.state.kinds = { problems }
  f.state.rowsByKind = { problems: rows }
  f.state.statusByKind = { problems: status as never }
  f.client.listKinds = vi.fn(async () => kindsView([problems, pods, events]))
  f.client.getTargetState = vi.fn(async () => ({ kind: '"problems"' }))
  render(<App client={f.client} />)
  const grid = await screen.findByRole('grid', { name: 'resources' })
  return { f, grid }
}

const names = (grid: HTMLElement) => within(grid).queryAllByRole('row').slice(1).map((r) => within(r).getAllByRole('gridcell')[3]?.textContent)

describe('Problems', () => {
  it('is in the navigation; the worst first, then the most recent', async () => {
    const { grid } = await openProblems([
      problem('events#e1', 'events', 'pod/a', 'recent', 1, 1),
      problem('pods#old', 'pods', 'old-warn', 'warning', 3, 30),
      problem('pods#crash', 'pods', 'crash', 'error', 4, 5),
      problem('pods#new', 'pods', 'new-warn', 'warning', 3, 2),
    ])
    expect(within(screen.getByRole('navigation', { name: 'resources' })).getByRole('button', { name: /Problems/ })).toHaveAttribute('aria-current', 'page')
    await within(grid).findByText('crash')
    expect(names(grid)).toEqual(['crash', 'new-warn', 'old-warn', 'pod/a'])
  })

  it('evidence is shown quieter than a current problem', async () => {
    const { grid } = await openProblems([problem('events#e1', 'events', 'pod/a', 'recent', 1, 1), problem('pods#crash', 'pods', 'crash', 'error', 4, 5)])
    const recent = await within(grid).findByText('recent')
    expect(recent.closest('[role="gridcell"]')).toHaveClass('text-fg-subtle')
    expect(within(grid).getByText('error').closest('[role="gridcell"]')).toHaveClass('text-danger')
  })

  it("a row offers what its object's kind offers, not the table's", async () => {
    const { grid } = await openProblems([problem('pods#crash', 'pods', 'crash', 'error', 4, 5), problem('events#e1', 'events', 'pod/a', 'recent', 1, 1)])
    fireEvent.contextMenu(await within(grid).findByText('crash'))
    let menu = screen.getByRole('menu', { name: 'Row actions' })
    expect(within(menu).getAllByRole('menuitem').map((m) => m.textContent)).toEqual(['Details', 'Logs', 'Delete'])
    fireEvent.keyDown(menu, { key: 'Escape' })
    fireEvent.contextMenu(within(grid).getByText('pod/a'))
    menu = screen.getByRole('menu', { name: 'Row actions' })
    expect(within(menu).getAllByRole('menuitem').map((m) => m.textContent)).toEqual(['Details'])
  })

  it('says what could not be observed; no problems is not "all is well" then', async () => {
    await openProblems([], {
      state: 'ready',
      coverage: [
        { source: 'Pods', state: 'ready' },
        { source: 'Nodes (cluster-wide)', state: 'denied', class: 'forbidden', message: 'nodes is forbidden' },
        { source: 'Warning events', state: 'loading' },
      ],
    })
    const alert = await screen.findByRole('note', { name: 'Not observed' })
    expect(alert).toHaveTextContent('Not observed: Nodes (cluster-wide) (access denied), Warning events (loading)')
    expect(alert).toHaveClass('text-warning')
    expect(screen.getByText('No problems in what could be observed.')).toBeInTheDocument()
    expect(screen.queryByText('No objects')).not.toBeInTheDocument()
  })

  // Review 2026-09-30 (Codex, P5): with every source ready the coverage
  // vanished — "No problems found" then hid what is never looked at.
  it('always says what is checked and what is not, also with full coverage', async () => {
    await openProblems([], { state: 'ready', coverage: [{ source: 'Pods', state: 'ready' }, { source: 'Nodes (cluster-wide)', state: 'ready' }] })
    expect(await screen.findByText('No problems found.')).toBeInTheDocument()
    expect(screen.queryByRole('note', { name: 'Not observed' })).not.toBeInTheDocument()
    const scope = screen.getByRole('note', { name: 'Coverage' })
    expect(scope).toHaveTextContent('Checked: Pods, Nodes (cluster-wide) · not checked: Jobs, custom resources')
    expect(scope).not.toHaveClass('text-warning')
  })
})
