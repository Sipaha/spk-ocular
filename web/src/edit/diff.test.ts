import { describe, expect, it } from 'vitest'
import { diff, diffLines, hunks, type DiffLine } from './diff'

const ops = (d: DiffLine[]) => d.map((l) => `${l.op}${l.text}`)

describe('diffLines', () => {
  it('equal texts are all context; empty texts are nothing', () => {
    expect(ops(diffLines('a\nb', 'a\nb'))).toEqual([' a', ' b'])
    expect(diffLines('', '')).toEqual([])
  })

  it('an insertion, a deletion and a replacement', () => {
    expect(ops(diffLines('a\nc', 'a\nb\nc'))).toEqual([' a', '+b', ' c'])
    expect(ops(diffLines('a\nb\nc', 'a\nc'))).toEqual([' a', '-b', ' c'])
    expect(ops(diffLines('a\nb\nc', 'a\nx\nc'))).toEqual([' a', '-b', '+x', ' c'])
    expect(ops(diffLines('', 'a'))).toEqual(['+a'])
    expect(ops(diffLines('a', ''))).toEqual(['-a'])
  })

  it('numbers lines on both sides', () => {
    const d = diffLines('a\nb\nc', 'a\nx\nc')
    expect(d.map((l) => [l.op, l.a ?? null, l.b ?? null])).toEqual([
      [' ', 1, 1],
      ['-', 2, null],
      ['+', null, 2],
      [' ', 3, 3],
    ])
  })

  it('is minimal: a moved key is one deletion and one insertion', () => {
    const d = diffLines('k1: 1\nk2: 2\nk3: 3\nk4: 4', 'k2: 2\nk3: 3\nk4: 4\nk1: 1')
    expect(d.filter((l) => l.op !== ' ')).toHaveLength(2)
  })

  it('a trailing newline does not make an empty line differ', () => {
    expect(ops(diffLines('a\nb\n', 'a\nc\n'))).toEqual([' a', '-b', '+c'])
  })

  it('5k lines with a few changes are fast', () => {
    const a = Array.from({ length: 5000 }, (_, i) => `  key${i}: value${i}`)
    const b = [...a]
    b[10] = '  key10: changed'
    b.splice(2500, 0, '  inserted: yes')
    b.splice(4000, 3)
    const t = performance.now()
    const d = diffLines(a.join('\n'), b.join('\n'))
    expect(performance.now() - t).toBeLessThan(50)
    expect(d.filter((l) => l.op === '+')).toHaveLength(2)
    expect(d.filter((l) => l.op === '-')).toHaveLength(4)
  })

  it('two unrelated 5k-line texts stay bounded in time (a replacement, not a search)', () => {
    const a = Array.from({ length: 5000 }, (_, i) => `a${i}`).join('\n')
    const b = Array.from({ length: 5000 }, (_, i) => `b${i}`).join('\n')
    const t = performance.now()
    const d = diffLines(a, b)
    expect(performance.now() - t).toBeLessThan(500)
    expect(d.filter((l) => l.op === '-')).toHaveLength(5000)
    expect(d.filter((l) => l.op === '+')).toHaveLength(5000)
  })
})

describe('diffLines worst cases stay bounded', () => {
  it('300k short lines replaced whole', () => {
    const a = Array.from({ length: 300_000 }, (_, i) => `a${i % 7}`).join('\n')
    const b = Array.from({ length: 300_000 }, (_, i) => `b${i % 5}`).join('\n')
    const t = performance.now()
    const d = diffLines(a, b)
    expect(performance.now() - t).toBeLessThan(400)
    expect(d.filter((l) => l.op !== '+').map((l) => l.text).join('\n')).toBe(a)
    expect(d.filter((l) => l.op !== '-').map((l) => l.text).join('\n')).toBe(b)
  })

  it('interleaved changes beyond the budget: both sides still rebuild, marked approximate', () => {
    const a = Array.from({ length: 20_000 }, (_, i) => (i % 2 ? `same${i}` : `a${i}`))
    const b = Array.from({ length: 20_000 }, (_, i) => (i % 2 ? `same${i}` : `b${i}`))
    const t = performance.now()
    const r = diff(a.join('\n'), b.join('\n'))
    const d = r.lines
    expect(performance.now() - t).toBeLessThan(400)
    expect(r.approximate).toBe(true)
    expect(diff('a\nb', 'a\nc').approximate).toBe(false)
    expect(d.filter((l) => l.op !== '+').map((l) => l.text)).toEqual(a)
    expect(d.filter((l) => l.op !== '-').map((l) => l.text)).toEqual(b)
  })

  it('one 3 MB line', () => {
    const a = 'x'.repeat(3_000_000)
    const b = 'x'.repeat(2_999_999) + 'y'
    const t = performance.now()
    const d = diffLines(`k: 1\n${a}`, `k: 1\n${b}`)
    expect(performance.now() - t).toBeLessThan(200)
    expect(d.map((l) => l.op)).toEqual([' ', '-', '+'])
  })
})

describe('diffLines on random texts', () => {
  const lcs = (a: string[], b: string[]) => {
    const t = Array.from({ length: a.length + 1 }, () => new Array<number>(b.length + 1).fill(0))
    for (let i = 1; i <= a.length; i++) for (let j = 1; j <= b.length; j++) t[i][j] = a[i - 1] === b[j - 1] ? t[i - 1][j - 1] + 1 : Math.max(t[i - 1][j], t[i][j - 1])
    return t[a.length][b.length]
  }
  it('rebuilds both sides, numbers them in order and is minimal', () => {
    let seed = 7
    const rnd = (n: number) => ((seed = (seed * 1103515245 + 12345) % 2 ** 31), seed % n)
    for (let run = 0; run < 300; run++) {
      const a = Array.from({ length: rnd(15) }, () => 'abcd'[rnd(4)])
      const b = Array.from({ length: rnd(15) }, () => 'abcd'[rnd(4)])
      const d = diffLines(a.join('\n'), b.join('\n'))
      expect(d.filter((l) => l.op !== '+').map((l) => l.text)).toEqual(a)
      expect(d.filter((l) => l.op !== '-').map((l) => l.text)).toEqual(b)
      expect(d.filter((l) => l.op !== '+').map((l) => l.a)).toEqual(a.map((_, i) => i + 1))
      expect(d.filter((l) => l.op !== '-').map((l) => l.b)).toEqual(b.map((_, i) => i + 1))
      expect(d.filter((l) => l.op === ' ')).toHaveLength(lcs(a, b))
    }
  })
})

describe('hunks', () => {
  it('keeps 3 lines of context around changes and folds the rest', () => {
    const a = Array.from({ length: 20 }, (_, i) => `l${i}`)
    const b = [...a]
    b[10] = 'changed'
    const h = hunks(diffLines(a.join('\n'), b.join('\n')))
    expect(h.map((x) => ('skipped' in x ? `skip ${x.skipped}` : ops(x.lines).join(',')))).toEqual([
      'skip 7',
      ' l7, l8, l9,-l10,+changed, l11, l12, l13',
      'skip 6',
    ])
  })

  it('close changes share a hunk; no changes, no hunks', () => {
    const a = Array.from({ length: 12 }, (_, i) => `l${i}`)
    const b = [...a]
    b[2] = 'x'
    b[8] = 'y'
    const h = hunks(diffLines(a.join('\n'), b.join('\n')))
    expect(h.filter((x) => !('skipped' in x))).toHaveLength(1)
    expect(hunks(diffLines('a\nb', 'a\nb'))).toEqual([])
  })
})
