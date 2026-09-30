import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { DIFF_PAGE, EditDiff, LONG_LINE } from './EditDiff'

const lines = (n: number, f: (i: number) => string) => Array.from({ length: n }, (_, i) => f(i)).join('\n') + '\n'

describe('EditDiff', () => {
  it('says how many lines changed and folds unchanged runs', () => {
    const before = lines(100, (i) => `line ${i}`)
    const after = before.replace('line 50\n', 'line fifty\n')
    render(<EditDiff before={before} after={after} />)
    const d = screen.getByRole('region', { name: 'Changes' })
    expect(d).toHaveTextContent('lines removed: 1, added: 1')
    expect(d.querySelector('[data-op="-"]')).toHaveTextContent('line 50')
    expect(d.querySelector('[data-op="+"]')).toHaveTextContent('line fifty')
    expect(d).toHaveTextContent('unchanged lines: 47')
    expect(d).toHaveTextContent('unchanged lines: 46')
  })

  it('a change past the first page is reachable', async () => {
    // Every line changes: the diff has 2 × 3000 rows; the removal of the
    // changed line 2001 is on the second page.
    const before = lines(3000, (i) => `old ${i + 1}`)
    const after = lines(3000, (i) => `new ${i + 1}`)
    render(<EditDiff before={before} after={after} />)
    const d = screen.getByRole('region', { name: 'Changes' })
    const has = (text: string) => [...d.querySelectorAll('[data-op]')].some((r) => r.textContent?.includes(text))
    // Removals first (old 1…3000), then additions: old 2001 is on page two.
    expect(d.querySelectorAll('[data-op]')).toHaveLength(DIFF_PAGE)
    expect(has('old 2000')).toBe(true)
    expect(has('old 2001')).toBe(false)
    await userEvent.click(within(d).getByRole('button', { name: 'Show the next 2000 lines (of 4000 more)' }))
    expect(has('old 2001')).toBe(true)
    await userEvent.click(within(d).getByRole('button', { name: 'Show the next 2000 lines (of 2000 more)' }))
    expect(has('new 3000')).toBe(true)
    expect(within(d).queryByRole('button', { name: /Show the next/ })).not.toBeInTheDocument()
    // 6000 rendered rows in jsdom: seconds under a loaded make check.
  }, 30_000)

  it('the removal of line 2001 among changed lines is reachable', async () => {
    const before = lines(2500, (i) => `a ${i + 1}`)
    const after = lines(2500, (i) => (i === 2000 ? `b ${i + 1}` : `a ${i + 1}`))
    render(<EditDiff before={before} after={after} />)
    const d = screen.getByRole('region', { name: 'Changes' })
    expect(d.querySelector('[data-op="-"]')).toHaveTextContent('a 2001')
    expect(d.querySelector('[data-op="+"]')).toHaveTextContent('b 2001')
  })

  it('a long line is folded; its differing tail shows when unfolded', async () => {
    const head = 'x'.repeat(LONG_LINE + 500)
    render(<EditDiff before={`k: ${head}TAIL-A\n`} after={`k: ${head}TAIL-B\n`} />)
    const d = screen.getByRole('region', { name: 'Changes' })
    expect(d).not.toHaveTextContent('TAIL-A')
    expect(d).not.toHaveTextContent('TAIL-B')
    for (const b of within(d).getAllByRole('button', { name: /show the whole line/ })) await userEvent.click(b)
    expect(d.querySelector('[data-op="-"]')).toHaveTextContent('TAIL-A')
    expect(d.querySelector('[data-op="+"]')).toHaveTextContent('TAIL-B')
  })
})
