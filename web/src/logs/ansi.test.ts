import { describe, expect, it } from 'vitest'
import { applySgr, parseAnsi, PLAIN, stripAnsi, styleCss } from './ansi'

describe('parseAnsi', () => {
  it.each([
    ['plain', 'hello', 'hello'],
    ['colour', '\x1b[31mERROR\x1b[0m done', 'ERROR done'],
    ['cursor moves and erase are dropped', 'a\x1b[2K\x1b[1Gb\x1b[?25l', 'ab'],
    ['OSC 8 hyperlink: text kept, target dropped', '\x1b]8;;https://evil.example\x07click\x1b]8;;\x07!', 'click!'],
    ['OSC with ST terminator', '\x1b]0;title\x1b\\x', 'x'],
    ['unterminated CSI at the end', 'x\x1b[31', 'x'],
    ['lone ESC', 'a\x1bb\x1b', 'a'],
    ['charset escape', '\x1b(Bok', 'ok'],
  ])('%s', (_name, raw, plain) => {
    expect(parseAnsi(raw).plain).toBe(plain)
    expect(stripAnsi(raw).plain).toBe(plain)
  })

  it('builds spans and carries the style to the next line', () => {
    const r = parseAnsi('a\x1b[1;32mb\x1b[39mc')
    expect(r.spans).toEqual([
      { text: 'a', style: PLAIN },
      { text: 'b', style: { bold: true, fg: 'var(--color-ansi-2)' } },
      { text: 'c', style: { bold: true } },
    ])
    expect(r.end).toEqual({ bold: true })
    // a multi-line coloured block: the next line starts bold
    expect(parseAnsi('next', r.end).spans).toEqual([{ text: 'next', style: { bold: true } }])
  })

  it('reads 256-colour and truecolor forms', () => {
    expect(applySgr(PLAIN, '38;5;208').fg).toBe('rgb(255,135,0)')
    expect(applySgr(PLAIN, '38;5;244').fg).toBe('rgb(128,128,128)')
    expect(applySgr(PLAIN, '48;2;1;2;3').bg).toBe('rgb(1,2,3)')
    expect(applySgr(PLAIN, '38:2::10:20:30').fg).toBe('rgb(10,20,30)')
    expect(applySgr(PLAIN, '38:5:9').fg).toBe('var(--color-ansi-9)')
    expect(applySgr(PLAIN, '38;2;999;0;0')).toBe(PLAIN)
    expect(applySgr({ fg: 'x', bold: true }, '')).toBe(PLAIN)
    expect(applySgr({ fg: 'x', bold: true }, '22;39')).toBe(PLAIN)
  })

  it('never lets escapes reach the page as markup', () => {
    const r = parseAnsi('<script>\x1b[31m&</script>')
    expect(r.plain).toBe('<script>&</script>')
  })

  it('renders inverse by swapping colours', () => {
    expect(styleCss({ inverse: true, fg: 'red' })).toEqual({ color: 'var(--color-app)', backgroundColor: 'red' })
    expect(styleCss(PLAIN)).toBeUndefined()
  })
})
