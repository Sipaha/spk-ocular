/** One line of a line diff: context (' '), removed ('-') or added ('+'),
 * with its 1-based number on the side(s) it is on. */
export interface DiffLine {
  op: ' ' | '-' | '+'
  text: string
  a?: number
  b?: number
}

/** A run of unchanged lines not shown (hunks). */
export interface Skipped {
  skipped: number
}

export interface Hunk {
  lines: DiffLine[]
}

/** Beyond this many differences the middle is shown as replaced whole:
 * the search stays bounded (O((N+M)·D)) for unrelated texts. */
const MAX_EDITS = 2000

const split = (s: string) => (s === '' ? [] : s.replace(/\n$/, '').split('\n'))

/** The shortest line diff (Myers), common ends trimmed first. */
export function diffLines(before: string, after: string): DiffLine[] {
  const a = split(before)
  const b = split(after)
  let pre = 0
  while (pre < a.length && pre < b.length && a[pre] === b[pre]) pre++
  let suf = 0
  while (suf < a.length - pre && suf < b.length - pre && a[a.length - 1 - suf] === b[b.length - 1 - suf]) suf++
  const out: DiffLine[] = []
  for (let i = 0; i < pre; i++) out.push({ op: ' ', text: a[i], a: i + 1, b: i + 1 })
  middle(a, b, pre, a.length - suf, pre, b.length - suf, out)
  for (let k = suf; k > 0; k--) {
    const i = a.length - k
    const j = b.length - k
    out.push({ op: ' ', text: a[i], a: i + 1, b: j + 1 })
  }
  return out
}

function middle(a: string[], b: string[], a0: number, a1: number, b0: number, b1: number, out: DiffLine[]) {
  const n = a1 - a0
  const m = b1 - b0
  const replace = () => {
    for (let i = a0; i < a1; i++) out.push({ op: '-', text: a[i], a: i + 1 })
    for (let j = b0; j < b1; j++) out.push({ op: '+', text: b[j], b: j + 1 })
  }
  if (n === 0 || m === 0) return replace()
  const max = Math.min(n + m, MAX_EDITS)
  const off = max + 1
  // v[k + off]: the furthest x on diagonal k; one copy kept per step d.
  let v = new Int32Array(2 * max + 3)
  const trace: Int32Array[] = []
  let found = -1
  for (let d = 0; d <= max && found < 0; d++) {
    trace.push(v.slice())
    const next = v.slice()
    for (let k = -d; k <= d; k += 2) {
      let x = k === -d || (k !== d && v[k - 1 + off] < v[k + 1 + off]) ? v[k + 1 + off] : v[k - 1 + off] + 1
      let y = x - k
      while (x < n && y < m && a[a0 + x] === b[b0 + y]) {
        x++
        y++
      }
      next[k + off] = x
      if (x >= n && y >= m) {
        found = d
        break
      }
    }
    v = next
  }
  if (found < 0) return replace()
  // Walk back from (n, m) through the kept rows.
  const rev: DiffLine[] = []
  let x = n
  let y = m
  for (let d = found; d > 0; d--) {
    const pv = trace[d]
    const k = x - y
    const down = k === -d || (k !== d && pv[k - 1 + off] < pv[k + 1 + off])
    const pk = down ? k + 1 : k - 1
    const px = pv[pk + off]
    const py = px - pk
    // The snake after the edit: down ends at (px, py + 1), right at (px + 1, py).
    while (x > px && y > py && (down ? y > py + 1 : x > px + 1)) {
      x--
      y--
      rev.push({ op: ' ', text: a[a0 + x], a: a0 + x + 1, b: b0 + y + 1 })
    }
    if (down) {
      y--
      rev.push({ op: '+', text: b[b0 + y], b: b0 + y + 1 })
    } else {
      x--
      rev.push({ op: '-', text: a[a0 + x], a: a0 + x + 1 })
    }
  }
  while (x > 0 && y > 0) {
    x--
    y--
    rev.push({ op: ' ', text: a[a0 + x], a: a0 + x + 1, b: b0 + y + 1 })
  }
  for (let i = rev.length - 1; i >= 0; i--) out.push(rev[i])
}

/** Changes with `context` unchanged lines around them; longer unchanged
 * runs are folded (Skipped). No changes: nothing. */
export function hunks(lines: DiffLine[], context = 3): (Hunk | Skipped)[] {
  const changed = lines.map((l) => l.op !== ' ')
  if (!changed.includes(true)) return []
  const keep = new Array<boolean>(lines.length).fill(false)
  changed.forEach((c, i) => {
    if (!c) return
    for (let j = Math.max(0, i - context); j <= Math.min(lines.length - 1, i + context); j++) keep[j] = true
  })
  const out: (Hunk | Skipped)[] = []
  let i = 0
  while (i < lines.length) {
    const start = i
    const kept = keep[i]
    while (i < lines.length && keep[i] === kept) i++
    out.push(kept ? { lines: lines.slice(start, i) } : { skipped: i - start })
  }
  return out
}
