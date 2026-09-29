// ANSI escape handling for log lines. Only SGR (colours and emphasis) is
// interpreted; every other CSI, OSC (window titles, hyperlinks — never
// turned into links) and stray ESC is removed. Search, filters, levels and
// copies work on the plain text; spans are built only for rendered rows.

/** A text style. Colours are CSS colour strings; absent = default. */
export interface Style {
  fg?: string
  bg?: string
  bold?: boolean
  dim?: boolean
  italic?: boolean
  underline?: boolean
  inverse?: boolean
}

export const PLAIN: Style = Object.freeze({})

export interface Span {
  text: string
  style: Style
}

const ESC = '\x1b'

/** The 16 base colours come from the theme (index.css --color-ansi-N). */
const base = (n: number) => `var(--color-ansi-${n})`

function color256(n: number): string | undefined {
  if (!Number.isInteger(n) || n < 0 || n > 255) return undefined
  if (n < 16) return base(n)
  if (n < 232) {
    const c = n - 16
    const v = (x: number) => (x === 0 ? 0 : 55 + x * 40)
    return `rgb(${v(Math.floor(c / 36))},${v(Math.floor(c / 6) % 6)},${v(c % 6)})`
  }
  const g = 8 + (n - 232) * 10
  return `rgb(${g},${g},${g})`
}

function isDefault(s: Style): boolean {
  return !s.fg && !s.bg && !s.bold && !s.dim && !s.italic && !s.underline && !s.inverse
}

export function sameStyle(a: Style, b: Style): boolean {
  return a.fg === b.fg && a.bg === b.bg && !!a.bold === !!b.bold && !!a.dim === !!b.dim &&
    !!a.italic === !!b.italic && !!a.underline === !!b.underline && !!a.inverse === !!b.inverse
}

/** Applies one SGR parameter list ("1;31", "38;5;208", "38:2::1:2:3"). */
export function applySgr(style: Style, params: string): Style {
  const s: Style = { ...style }
  const list = params === '' ? ['0'] : params.split(';')
  for (let i = 0; i < list.length; i++) {
    const p = list[i]
    if (p.includes(':')) {
      // sub-parameter form of extended colours: 38:5:n / 38:2:[cs]:r:g:b
      const sub = p.split(':').map((x) => (x === '' ? NaN : Number(x)))
      const target = sub[0] === 38 ? 'fg' : sub[0] === 48 ? 'bg' : undefined
      if (target && sub[1] === 5) s[target] = color256(sub[2])
      if (target && sub[1] === 2) {
        const [r, g, b] = sub.length >= 6 ? sub.slice(3, 6) : sub.slice(2, 5)
        if ([r, g, b].every((x) => Number.isInteger(x) && x >= 0 && x <= 255)) s[target] = `rgb(${r},${g},${b})`
      }
      continue
    }
    const n = p === '' ? 0 : Number(p)
    if (!Number.isInteger(n)) continue
    if (n === 0) {
      for (const k of Object.keys(s)) delete s[k as keyof Style]
    } else if (n === 1) s.bold = true
    else if (n === 2) s.dim = true
    else if (n === 3) s.italic = true
    else if (n === 4) s.underline = true
    else if (n === 7) s.inverse = true
    else if (n === 22) s.bold = s.dim = false
    else if (n === 23) s.italic = false
    else if (n === 24) s.underline = false
    else if (n === 27) s.inverse = false
    else if (n >= 30 && n <= 37) s.fg = base(n - 30)
    else if (n === 39) delete s.fg
    else if (n >= 40 && n <= 47) s.bg = base(n - 40)
    else if (n === 49) delete s.bg
    else if (n >= 90 && n <= 97) s.fg = base(n - 90 + 8)
    else if (n >= 100 && n <= 107) s.bg = base(n - 100 + 8)
    else if (n === 38 || n === 48) {
      const target = n === 38 ? 'fg' : 'bg'
      if (list[i + 1] === '5') {
        s[target] = color256(Number(list[i + 2]))
        i += 2
      } else if (list[i + 1] === '2') {
        const [r, g, b] = list.slice(i + 2, i + 5).map(Number)
        if ([r, g, b].every((x) => Number.isInteger(x) && x >= 0 && x <= 255)) s[target] = `rgb(${r},${g},${b})`
        i += 4
      }
    }
  }
  for (const k of Object.keys(s) as (keyof Style)[]) if (s[k] === undefined || s[k] === false) delete s[k]
  return isDefault(s) ? PLAIN : s
}

/**
 * Parses a line: its text without escapes, its styled spans (starting from
 * the style the previous line of the same source left), and the style it
 * leaves for the next line.
 */
export function parseAnsi(raw: string, start: Style = PLAIN): { plain: string; spans: Span[]; end: Style } {
  if (!raw.includes(ESC)) return { plain: raw, spans: raw ? [{ text: raw, style: start }] : [], end: start }
  const spans: Span[] = []
  let style = start
  let text = ''
  let plain = ''
  const flush = () => {
    if (!text) return
    const last = spans[spans.length - 1]
    if (last && sameStyle(last.style, style)) last.text += text
    else spans.push({ text, style })
    plain += text
    text = ''
  }
  let i = 0
  while (i < raw.length) {
    const c = raw[i]
    if (c !== ESC) {
      const next = raw.indexOf(ESC, i)
      const end = next < 0 ? raw.length : next
      text += raw.slice(i, end)
      i = end
      continue
    }
    const kind = raw[i + 1]
    if (kind === '[') {
      // CSI: parameters/intermediates 0x20–0x3F, final 0x40–0x7E
      let j = i + 2
      while (j < raw.length && raw.charCodeAt(j) >= 0x20 && raw.charCodeAt(j) <= 0x3f) j++
      if (j >= raw.length) break // unterminated: drop it
      const final = raw[j]
      if (final === 'm') {
        flush()
        style = applySgr(style, raw.slice(i + 2, j))
      }
      i = j + 1
    } else if (kind === ']') {
      // OSC: up to BEL or ESC \ (hyperlink targets, titles: dropped)
      let j = i + 2
      for (; j < raw.length; j++) {
        if (raw[j] === '\x07') {
          j++
          break
        }
        if (raw[j] === ESC && raw[j + 1] === '\\') {
          j += 2
          break
        }
      }
      i = j
    } else {
      // other escapes: intermediates 0x20–0x2F, then one final byte
      // ("ESC ( B", "ESC 7"); a lone ESC at the end is dropped too
      let j = i + 1
      while (j < raw.length && raw.charCodeAt(j) >= 0x20 && raw.charCodeAt(j) <= 0x2f) j++
      i = Math.min(raw.length, j + 1)
    }
  }
  flush()
  return { plain, spans, end: style }
}

/** Text without escapes and the style left for the next line, cheaply. */
export function stripAnsi(raw: string, start: Style = PLAIN): { plain: string; end: Style } {
  if (!raw.includes(ESC)) return { plain: raw, end: start }
  const { plain, end } = parseAnsi(raw, start)
  return { plain, end }
}

/** Inline CSS for a style (inverse swaps colours). */
export function styleCss(s: Style): Record<string, string> | undefined {
  if (s === PLAIN || isDefault(s)) return undefined
  const css: Record<string, string> = {}
  let fg = s.fg
  let bg = s.bg
  if (s.inverse) {
    fg = s.bg ?? 'var(--color-app)'
    bg = s.fg ?? 'var(--color-fg)'
  }
  if (fg) css.color = fg
  if (bg) css.backgroundColor = bg
  if (s.bold) css.fontWeight = '600'
  if (s.dim) css.opacity = '0.6'
  if (s.italic) css.fontStyle = 'italic'
  if (s.underline) css.textDecoration = 'underline'
  return css
}
