import { act, fireEvent, render, screen, within } from '@testing-library/react'
import { useState } from 'react'
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

it('column resizing changes a shared width without sorting; double-click resets it', async () => {
  const save = vi.fn()
  const sort = vi.fn()
  render(<ResourceTable columns={columns} rows={[row('a')]} hideScope={false} filter="" selected={null} onSelect={() => {}} onSort={sort} onWidths={save} initialWidths={{ cpu: 170 }} />)
  const grid = screen.getByRole('grid')
  expect(grid.style.getPropertyValue('--column-1')).toBe('170px')
  const handle = screen.getByRole('separator', { name: 'Resize column Name' })
  vi.spyOn(handle.parentElement!, 'getBoundingClientRect').mockReturnValue({ width: 220 } as DOMRect)
  handle.focus()
  await userEvent.keyboard('{ArrowRight}')
  expect(grid.style.getPropertyValue('--column-0')).toBe('230px')
  expect(save).toHaveBeenLastCalledWith({ name: 230, cpu: 170 })
  expect(sort).not.toHaveBeenCalled()
  fireEvent.doubleClick(handle)
  expect(grid.style.getPropertyValue('--column-0')).toBe('')
  expect(save).toHaveBeenLastCalledWith({ cpu: 170 })
  expect(sort).not.toHaveBeenCalled()
})

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

describe('ResourceTable health without a status column', () => {
  const plain: Column[] = [{ id: 'name', title: 'Name', type: 'text' }, { id: 'size', title: 'Size', type: 'number' }]
  const hrow = (name: string, health: Row['health']): Row => ({ ...row(name), health })
  it('a table of server columns (no status column) still shows each row’s health: a dot by the name, the reason on hover', () => {
    render(
      <ResourceTable
        columns={plain}
        rows={[hrow('alpha', { state: 'ok' }), hrow('beta', { state: 'error', reason: 'Broken', message: 'the widget is broken' }), hrow('delta', { state: 'terminating', reason: 'Terminating' })]}
        hideScope={false}
        filter=""
        selected={null}
        onSelect={() => {}}
        metrics={null}
      />,
    )
    const r = (name: string) => screen.getByText(name).closest('[role="row"]') as HTMLElement
    const first = (name: string) => within(r(name)).getAllByRole('gridcell')[0]
    expect(first('beta').querySelector('[data-health]')).toHaveAttribute('data-health', 'error')
    expect(r('beta')).toHaveAttribute('title', 'Broken: the widget is broken')
    expect(first('alpha').querySelector('[data-health]')).toHaveAttribute('data-health', 'ok')
    expect(first('delta').querySelector('[data-health]')).toHaveAttribute('data-health', 'terminating')
    expect(first('delta')).toHaveClass('text-fg-subtle')
    expect(within(r('beta')).getAllByRole('gridcell')[1].querySelector('[data-health]')).toBeNull()
  })
  it('with a status column the dot stays there', () => {
    render(<ResourceTable columns={[...plain, { id: 'status', title: 'Status', type: 'status' }]} rows={[{ ...hrow('a', { state: 'error' }), cells: [{ text: 'a' }, { num: 1 }, { text: 'Failed' }] }]} hideScope={false} filter="" selected={null} onSelect={() => {}} metrics={null} />)
    const cells = within(screen.getByText('a').closest('[role="row"]') as HTMLElement).getAllByRole('gridcell')
    expect(cells[0].querySelector('[data-health]')).toBeNull()
    expect(cells[2].querySelector('[data-health]')).toHaveAttribute('data-health', 'error')
  })
})

describe('ResourceTable marks', () => {
  const text: Column[] = [{ id: 'name', title: 'Name', type: 'text' }]
  const rows = ['a', 'b', 'c', 'd', 'e'].map(row)

  /** A table with its cursor and marks held as Workspace holds them. */
  function Marked({ filter = '', onOpen, initial = [] }: { filter?: string; onOpen?: (r: Row) => void; initial?: string[] }) {
    const [selected, setSelected] = useState<string | null>(null)
    const [marked, setMarked] = useState<ReadonlySet<string>>(new Set(initial))
    return (
      <>
        <input aria-label="filter" defaultValue="" />
        <ResourceTable columns={text} rows={rows} hideScope={false} filter={filter} selected={selected} onSelect={(r) => setSelected(r.id)} onOpen={onOpen} marked={marked} onMarked={setMarked} />
      </>
    )
  }
  const grid = () => screen.getByRole('grid')
  const rowOf = (name: string) => within(grid()).getByText(name).closest('[role="row"]') as HTMLElement
  const markedNames = () =>
    within(grid())
      .getAllByRole('row')
      .filter((r) => r.hasAttribute('data-marked'))
      .map((r) => r.textContent)
  const names2 = () => within(grid()).getAllByRole('row').slice(1).map((r) => r.textContent)
  const box = (name: string) => within(rowOf(name)).getByRole('checkbox')
  const all = () => within(grid()).getByRole('checkbox', { name: 'Mark all' })
  const focusTable = () => act(() => (grid().querySelector('[data-table-scroll]') as HTMLElement).focus())

  it('a row’s checkbox marks it without opening it; the header marks all, some or none', async () => {
    const onOpen = vi.fn()
    render(<Marked onOpen={onOpen} />)
    const user = userEvent.setup()
    await user.click(box('b'))
    expect(markedNames()).toEqual(['b'])
    expect(box('b')).toHaveAttribute('aria-checked', 'true')
    expect(all()).toHaveAttribute('aria-checked', 'mixed')
    expect(onOpen).not.toHaveBeenCalled()
    await user.click(all())
    expect(markedNames()).toEqual(['a', 'b', 'c', 'd', 'e'])
    expect(all()).toHaveAttribute('aria-checked', 'true')
    await user.click(all())
    expect(markedNames()).toEqual([])
    expect(all()).toHaveAttribute('aria-checked', 'false')
  })

  it('Ctrl+click toggles a mark without opening; Shift+click marks the range from the last toggled row in the shown order', async () => {
    const onOpen = vi.fn()
    render(<Marked onOpen={onOpen} />)
    const user = userEvent.setup()
    await user.keyboard('{Control>}')
    await user.click(rowOf('b'))
    await user.keyboard('{/Control}')
    expect(markedNames()).toEqual(['b'])
    expect(onOpen).not.toHaveBeenCalled()
    await user.keyboard('{Shift>}')
    await user.click(rowOf('d'))
    await user.keyboard('{/Shift}')
    expect(markedNames()).toEqual(['b', 'c', 'd'])
    expect(onOpen).not.toHaveBeenCalled()
    // descending: the range follows what is shown
    await user.click(screen.getByRole('columnheader', { name: /Name/ }))
    expect(names2()).toEqual(['e', 'd', 'c', 'b', 'a'])
    await user.keyboard('{Control>}')
    await user.click(rowOf('d')) // unmarks d, the anchor
    await user.keyboard('{/Control}{Shift>}')
    await user.click(rowOf('a'))
    await user.keyboard('{/Shift}')
    expect(markedNames()).toEqual(['d', 'c', 'b', 'a'])
  })

  it('a plain click and the arrows move the cursor, never the marks', async () => {
    render(<Marked initial={['id-c']} />)
    const user = userEvent.setup()
    await user.click(rowOf('a'))
    await user.keyboard('{ArrowDown}{ArrowDown}{End}')
    expect(markedNames()).toEqual(['c'])
  })

  it('Space marks the cursor’s row, Shift+arrows mark as they go, Ctrl+A marks what the filter shows', async () => {
    const { rerender } = render(<Marked />)
    const user = userEvent.setup()
    await user.click(rowOf('a'))
    await user.keyboard(' ')
    expect(markedNames()).toEqual(['a'])
    await user.keyboard(' ')
    expect(markedNames()).toEqual([])
    await user.keyboard('{ArrowDown}{Shift>}{ArrowDown}{ArrowDown}{/Shift}')
    expect(markedNames()).toEqual(['b', 'c', 'd'])
    expect(rowOf('d')).toHaveAttribute('aria-selected', 'true')
    await user.keyboard('{Control>}a{/Control}')
    expect(markedNames()).toEqual(['a', 'b', 'c', 'd', 'e'])
    await user.keyboard('{Escape}')
    rerender(<Marked filter="c" />)
    focusTable()
    await user.keyboard('{Control>}a{/Control}')
    expect(markedNames()).toEqual(['c'])
  })

  it('Ctrl+A in another field selects its text, not rows', async () => {
    render(<Marked />)
    const user = userEvent.setup()
    await user.click(screen.getByRole('textbox', { name: 'filter' }))
    await user.keyboard('{Control>}a{/Control} ')
    expect(markedNames()).toEqual([])
  })

  it('Esc clears the marks and goes no further; without marks it goes on (details close)', async () => {
    const outer = vi.fn()
    render(
      <div onKeyDown={(e) => e.key === 'Escape' && !e.defaultPrevented && outer()}>
        <Marked initial={['id-a', 'id-b']} />
      </div>,
    )
    const user = userEvent.setup()
    focusTable()
    await user.keyboard('{Escape}')
    expect(markedNames()).toEqual([])
    expect(outer).not.toHaveBeenCalled()
    await user.keyboard('{Escape}')
    expect(outer).toHaveBeenCalledTimes(1)
  })
})
