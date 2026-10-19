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

it('expands leading, trailing and middle context from the selected boundary without duplicating lines', async () => {
 const before=lines(180,i=>`context ${i+1}`),after=before.replace('context 51\n','changed fifty-one\n').replace('context 131\n','changed one-thirty-one\n')
 const {container}=render(<EditDiff before={before} after={after}/>)
 const gaps=()=>Array.from(container.querySelectorAll<HTMLElement>('[data-diff-context]'))
 expect(gaps()).toHaveLength(3)
 expect(within(gaps()[0]).queryByRole('button',{name:/below/})).toBeNull()
 expect(within(gaps()[2]).queryByRole('button',{name:/above/})).toBeNull()
 await userEvent.click(within(gaps()[0]).getByRole('button',{name:'Show 20 lines above'}))
 expect(screen.getByText('context 28',{exact:true})).toBeVisible()
 expect(screen.queryByText('context 1',{exact:true})).toBeNull()
 await userEvent.click(within(gaps()[1]).getByRole('button',{name:'Show 20 lines below'}))
 expect(screen.getByText('context 55',{exact:true})).toBeVisible()
 await userEvent.click(within(gaps()[1]).getByRole('button',{name:'Show 20 lines above'}))
 expect(screen.getByText('context 127',{exact:true})).toBeVisible()
 await userEvent.click(within(gaps()[2]).getByRole('button',{name:'Show 20 lines below'}))
 expect(screen.getByText('context 154',{exact:true})).toBeVisible()
 while(gaps().length)await userEvent.click(within(gaps()[0]).getAllByRole('button')[0])
 const unchanged=Array.from(container.querySelectorAll('[data-op=" "]')).map(row=>row.lastElementChild!.textContent)
 expect(unchanged).toHaveLength(178);expect(new Set(unchanged).size).toBe(178)
 expect(container.querySelectorAll('[data-op="-"]')).toHaveLength(2);expect(container.querySelectorAll('[data-op="+"]')).toHaveLength(2)
 const first=container.querySelector('[data-op=" "]')!
 expect(first.children[0]).toHaveTextContent('1');expect(first.children[1]).toHaveTextContent('1');expect(first.lastElementChild).toHaveTextContent('context 1')
})
it('keeps expanded long-line identity stable when earlier context opens and resets on new input',async()=>{
 const long='long '+ 'x'.repeat(LONG_LINE+10)+'VISIBLE-TAIL'
 const before=lines(100,i=>i===50?long:`line ${i}`),after=before.replace(long,long+' changed')
 const {container,rerender}=render(<EditDiff before={before} after={after}/>)
 const removal=container.querySelector<HTMLElement>('[data-op="-"]')!
 await userEvent.click(within(removal).getByRole('button',{name:/show the whole line/}))
 await userEvent.click(within(container.querySelector<HTMLElement>('[data-diff-context]')!).getByRole('button',{name:'Show 20 lines above'}))
 expect(container.querySelector('[data-op="-"]')).toBe(removal);expect(removal).toHaveTextContent('VISIBLE-TAIL')
 rerender(<EditDiff before={before} after={before.replace('line 20\n','replacement\n')}/>)
 expect(screen.queryByText('line 0',{exact:true})).toBeNull()
 expect(container.querySelectorAll('[data-diff-context]')).toHaveLength(2)
})
