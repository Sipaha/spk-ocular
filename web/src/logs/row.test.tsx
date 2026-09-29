import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { Ingest } from './buffer'
import { fmtTime, renderMessage, rowText, shortLabels, sourceColor } from './row'

describe('row', () => {
  it('shortens pod names shared by all sources', () => {
    expect(shortLabels(['chatter-6c87cdb5b6-gfrk7/app', 'chatter-6c87cdb5b6-x2p9q/app'])).toEqual(['gfrk7', 'x2p9q'])
    expect(shortLabels(['web-a/app', 'web-a/side'])).toEqual(['a/app', 'a/side'])
    expect(shortLabels(['one/app'])).toEqual(['one/app'])
    expect(sourceColor('a')).toBe(sourceColor('a'))
  })

  it('shows and copies the same text', () => {
    const [e] = new Ingest().entries(1, [['2026-09-29T10:00:01.123456789Z', '\x1b[32mok\x1b[0m done']])
    const t = rowText(e, { showTime: true, showSource: true, labelOf: () => 'pod/app' })
    expect(t).toBe(`${fmtTime(e.ts).padEnd(12)} pod/app ok done`)
    expect(fmtTime(e.ts)).toMatch(/^\d\d:\d\d:01\.123$/)
  })

  it('marks matches across ANSI spans', () => {
    const [e] = new Ingest().entries(1, [['', 'a\x1b[31mbcd\x1b[0mef']])
    const { container } = render(<div>{renderMessage(e, [[2, 5]], true)}</div>)
    expect(container.textContent).toBe('abcdef')
    const marks = [...container.querySelectorAll('mark')].map((m) => m.textContent)
    expect(marks).toEqual(['cd', 'e'])
    expect(container.querySelector('mark')?.className).toBe('log-match-current')
    const red = [...container.querySelectorAll('span')].find((s) => s.textContent === 'b')
    expect(red?.style.color).toBe('var(--color-ansi-1)')
  })
})
