import { describe, expect, it } from 'vitest'
import { append, EMPTY_WINDOW, entryChars, Ingest, trim } from './buffer'
import { detectLevel } from './levels'

describe('levels', () => {
  it.each([
    ['2026-09-29 12:00:00.123 ERROR boom', 'ERROR'],
    ['[warn] disk', 'WARN'],
    ['level=info msg=hi', 'INFO'],
    ['{"level":"debug","msg":"x"}', 'DEBUG'],
    ['E0929 12:00:00.000 1 main.go:1] bad', 'ERROR'],
    ['WARNING: low', 'WARN'],
    ['just text', null],
  ])('%s', (line, level) => expect(detectLevel(line)).toBe(level))
})

describe('Ingest', () => {
  it('carries level and ANSI style per source, not across sources', () => {
    const ing = new Ingest()
    const a1 = ing.entries(1, [['t1', '\x1b[31mERROR boom'], ['t2', '  at frame']])
    const b1 = ing.entries(2, [['t3', 'plain from b']])
    const a2 = ing.entries(1, [['t4', 'still red\x1b[0m'], ['t5', 'after reset']])
    expect(a1.map((e) => e.level)).toEqual(['ERROR', 'ERROR'])
    expect(a1[1].style).toEqual({ fg: 'var(--color-ansi-1)' })
    expect(a1[1].raw).toBeUndefined()
    expect(b1[0].level).toBeNull()
    expect(b1[0].style).toBeUndefined()
    expect(a2[0].style).toEqual({ fg: 'var(--color-ansi-1)' })
    expect(a2[1].style).toBeUndefined()
    expect(a2[0].plain).toBe('still red')
    expect([...a1, ...b1, ...a2].map((e) => e.id)).toEqual([1, 2, 3, 4, 5])
  })
})

describe('window', () => {
  const ing = new Ingest()
  const lines = (n: number, len = 10) => ing.entries(1, Array.from({ length: n }, (_, i) => ['', String(i).padEnd(len, 'x')] as [string, string]))

  it('drops the oldest past the line cap and keeps ids', () => {
    let w = append(EMPTY_WINDOW, lines(8), false, 5, 1e9)
    expect(w.entries).toHaveLength(5)
    expect(w.evicted).toBe(3)
    const firstId = w.entries[0].id
    w = append(w, lines(1), false, 5, 1e9)
    expect(w.entries[0].id).toBe(firstId + 1)
    expect(w.chars).toBe(w.entries.reduce((n, e) => n + entryChars(e), 0))
  })

  it('bounds characters too', () => {
    const w = append(EMPTY_WINDOW, lines(10, 100), false, 1e9, 350)
    expect(w.entries).toHaveLength(3)
    expect(w.chars).toBeLessThanOrEqual(350)
  })

  it('stretches while frozen and trims back when following', () => {
    const frozen = append(EMPTY_WINDOW, lines(12), true, 10, 1e9)
    expect(frozen.entries).toHaveLength(12)
    expect(trim(frozen, false, 10, 1e9).entries).toHaveLength(10)
  })
})
