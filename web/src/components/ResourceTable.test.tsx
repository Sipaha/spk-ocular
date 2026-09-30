import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { Column, MetricsView, Row } from '../api/types'
import { ResourceTable } from './ResourceTable'

const columns: Column[] = [
  { id: 'name', title: 'Name', type: 'text' },
  { id: 'cpu', title: 'CPU', type: 'cpu', metric: true },
  { id: 'memory', title: 'Memory', type: 'bytes', metric: true },
]

const row = (name: string): Row =>
  ({ id: `id-${name}`, ref: { provider: 'p', target: 't', kind: 'k', name }, cells: [{ text: name }], health: { state: 'ok' } }) as unknown as Row

const ok = (values: MetricsView['values']): MetricsView => ({ status: 'ok', values })

const names = () => within(screen.getByRole('grid')).getAllByRole('row').slice(1).map((r) => within(r).getAllByRole('gridcell')[0].textContent)

function table(rows: Row[], metrics: MetricsView | null, onVisibleRows?: (ids: string[]) => void) {
  return <ResourceTable columns={columns} rows={rows} hideScope={false} filter="" selected={null} onSelect={() => {}} metrics={metrics} onVisibleRows={onVisibleRows} />
}

describe('ResourceTable metrics', () => {
  it('reports the rows in view (not the overscan), in the shown order', () => {
    const rows = Array.from({ length: 100 }, (_, i) => row(`r${String(i).padStart(3, '0')}`))
    const seen: string[][] = []
    render(table(rows, null, (ids) => seen.push(ids)))
    expect(seen).toHaveLength(1)
    const ids = seen[0]
    expect(ids[0]).toBe('id-r000')
    expect(ids.length).toBeGreaterThan(10)
    expect(ids.length).toBeLessThanOrEqual(Math.ceil(800 / 28) + 1) // a screenful, no overscan
    expect(ids).toEqual(rows.slice(0, ids.length).map((r) => r.id))
  })

  it('shows unknown as empty (never 0) and a partial sum as "≥"', () => {
    render(table([row('a'), row('b')], ok({ 'id-a': { memory: 1024 }, 'id-b': { cpu: 0.5, cpuPartial: true, memory: 2048, memoryPartial: true } })))
    const cells = (name: string) => within(screen.getByText(name).closest('[role="row"]') as HTMLElement).getAllByRole('gridcell').map((c) => c.textContent)
    expect(cells('a')).toEqual(['a', '', '1Ki'])
    expect(cells('b')).toEqual(['b', '≥ 500m', '≥ 2Ki'])
  })

  it('sorted by a metric, new samples update the cells but not the order until the sort is chosen again', async () => {
    const rows = [row('a'), row('b'), row('c')]
    const { rerender } = render(table(rows, ok({ 'id-a': { cpu: 0.1 }, 'id-b': { cpu: 0.2 }, 'id-c': { cpu: 0.3 } })))
    const user = userEvent.setup()
    await user.click(screen.getByRole('columnheader', { name: /CPU/ }))
    await user.click(screen.getByRole('columnheader', { name: /CPU/ }))
    expect(names()).toEqual(['c', 'b', 'a'])
    rerender(table(rows, ok({ 'id-a': { cpu: 0.9 }, 'id-b': { cpu: 0.2 }, 'id-c': { cpu: 0.3 } })))
    expect(names()).toEqual(['c', 'b', 'a']) // held: the reader's place does not move
    expect(screen.getByText('900m')).toBeInTheDocument()
    await user.click(screen.getByRole('columnheader', { name: /CPU/ })) // ascending, taken anew
    await user.click(screen.getByRole('columnheader', { name: /CPU/ })) // descending, taken anew
    expect(names()).toEqual(['a', 'c', 'b'])
    // the set of rows changing takes the order anew
    rerender(table([...rows, row('d')], ok({ 'id-a': { cpu: 0.9 }, 'id-b': { cpu: 0.2 }, 'id-c': { cpu: 0.3 }, 'id-d': { cpu: 0.05 } })))
    rerender(table([...rows, row('d')], ok({ 'id-a': { cpu: 0.01 }, 'id-b': { cpu: 0.2 }, 'id-c': { cpu: 0.3 }, 'id-d': { cpu: 0.05 } })))
    expect(names()).toEqual(['a', 'c', 'b', 'd'])
  })

  it('a metric sort chosen before any sample orders by the first samples', async () => {
    const rows = [row('a'), row('b')]
    const { rerender } = render(table(rows, null))
    const user = userEvent.setup()
    await user.click(screen.getByRole('columnheader', { name: /CPU/ }))
    await user.click(screen.getByRole('columnheader', { name: /CPU/ }))
    act(() => rerender(table(rows, ok({ 'id-a': { cpu: 0.1 }, 'id-b': { cpu: 0.2 } }))))
    expect(names()).toEqual(['b', 'a'])
    act(() => rerender(table(rows, ok({ 'id-a': { cpu: 0.5 }, 'id-b': { cpu: 0.2 } }))))
    expect(names()).toEqual(['b', 'a'])
  })

  it('a metric sort waiting for its first sample is not filled by the other metric', async () => {
    const rows = [row('a'), row('b')]
    const { rerender } = render(table(rows, ok({ 'id-a': { memory: 1 }, 'id-b': { memory: 2 } })))
    const user = userEvent.setup()
    await user.click(screen.getByRole('columnheader', { name: /CPU/ }))
    await user.click(screen.getByRole('columnheader', { name: /CPU/ }))
    act(() => rerender(table(rows, ok({ 'id-a': { cpu: 0.1, memory: 1 }, 'id-b': { cpu: 0.2, memory: 2 } }))))
    expect(names()).toEqual(['b', 'a']) // the first CPU sample orders
    act(() => rerender(table(rows, ok({ 'id-a': { cpu: 0.5, memory: 1 }, 'id-b': { cpu: 0.2, memory: 2 } }))))
    expect(names()).toEqual(['b', 'a']) // later ones only update the cells
  })

  it('unknown sorts last in both directions', async () => {
    render(table([row('a'), row('b'), row('c')], ok({ 'id-a': { cpu: 0 }, 'id-c': { cpu: 0.5 } })))
    const user = userEvent.setup()
    await user.click(screen.getByRole('columnheader', { name: /CPU/ }))
    const asc = names()
    await user.click(screen.getByRole('columnheader', { name: /CPU/ }))
    const desc = names()
    expect([asc, desc].sort()).toEqual([['a', 'c', 'b'], ['c', 'a', 'b']])
  })

  it('reports a new visible set only when it changes', () => {
    const rows = [row('a'), row('b')]
    const seen = vi.fn()
    const { rerender } = render(table(rows, null, seen))
    rerender(table(rows, ok({ 'id-a': { cpu: 1 } }), seen))
    expect(seen).toHaveBeenCalledTimes(1)
    rerender(table([row('a'), row('b'), row('c')], null, seen))
    expect(seen).toHaveBeenCalledTimes(2)
    expect(seen).toHaveBeenLastCalledWith(['id-a', 'id-b', 'id-c'])
  })
})
