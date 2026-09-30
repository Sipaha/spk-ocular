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

/** Work bound of one diff: the search keeps at most this many diagonal
 * cells (4 bytes each, ~4 MB) and does at most WORK steps; beyond either,
 * the middle is shown as replaced whole (Approximate). */
const MAX_CELLS = 1_000_000
const WORK = 5_000_000

const split = (s: string) => (s === '' ? [] : s.replace(/\n$/, '').split('\n'))

export interface Diff {
  lines: DiffLine[]
  /** The budget ran out: part of it is shown as replaced whole, not minimal. */
  approximate: boolean
}

/** The shortest line diff (Myers) within a bounded budget. */
export const diffLines = (before: string, after: string): DiffLine[] => diff(before, after).lines

/** A line diff, common ends trimmed first; lines compare as interned
 * numbers; beyond the budget the middle is replaced whole (approximate). */
export function diff(before: string, after: string): Diff {
  const a = split(before)
  const b = split(after)
  let pre = 0
  while (pre < a.length && pre < b.length && a[pre] === b[pre]) pre++
  let suf = 0
  while (suf < a.length - pre && suf < b.length - pre && a[a.length - 1 - suf] === b[b.length - 1 - suf]) suf++
  const out: DiffLine[] = []
  for (let i = 0; i < pre; i++) out.push({ op: ' ', text: a[i], a: i + 1, b: i + 1 })
  const exact = middle(a, b, pre, a.length - suf, pre, b.length - suf, out)
  for (let k = suf; k > 0; k--) {
    const i = a.length - k
    const j = b.length - k
    out.push({ op: ' ', text: a[i], a: i + 1, b: j + 1 })
  }
  return { lines: out, approximate: !exact }
}

/** Appends the middle's diff; false when the budget ran out. */
function middle(a: string[], b: string[], a0: number, a1: number, b0: number, b1: number, out: DiffLine[]): boolean {
  const n = a1 - a0
  const m = b1 - b0
  const replace = (exact: boolean) => {
    for (let i = a0; i < a1; i++) out.push({ op: '-', text: a[i], a: i + 1 })
    for (let j = b0; j < b1; j++) out.push({ op: '+', text: b[j], b: j + 1 })
    return exact
  }
  if (n === 0 || m === 0) return replace(true)
  // Lines as numbers: a snake compares ints, not (long) strings.
  const ids = new Map<string, number>()
  const id = (s: string) => {
    let v = ids.get(s)
    if (v === undefined) ids.set(s, (v = ids.size))
    return v
  }
  const x0 = Int32Array.from({ length: n }, (_, i) => id(a[a0 + i]))
  const y0 = Int32Array.from({ length: m }, (_, j) => id(b[b0 + j]))
  const max = n + m
  // trace[d]: the furthest x on diagonals -d..d (index k + d) after step d.
  const trace: Int32Array[] = []
  const at = (d: number, k: number) => (k < -d || k > d ? -1 : trace[d][k + d])
  let cells = 0
  let work = 0
  let found = -1
  for (let d = 0; d <= max && found < 0; d++) {
    cells += 2 * d + 1
    if (cells > MAX_CELLS || work > WORK) return replace(false)
    const row = new Int32Array(2 * d + 1)
    for (let k = -d; k <= d; k += 2) {
      let x = d === 0 ? 0 : k === -d || (k !== d && at(d - 1, k - 1) < at(d - 1, k + 1)) ? at(d - 1, k + 1) : at(d - 1, k - 1) + 1
      let y = x - k
      const sx = x
      while (x < n && y < m && x0[x] === y0[y]) {
        x++
        y++
      }
      work += 1 + x - sx
      row[k + d] = x
      if (x >= n && y >= m) {
        found = d
        break
      }
    }
    work += d
    trace.push(row)
  }
  if (found < 0) return replace(false)
  // Walk back from (n, m).
  const rev: DiffLine[] = []
  let x = n
  let y = m
  for (let d = found; d > 0; d--) {
    const k = x - y
    const down = k === -d || (k !== d && at(d - 1, k - 1) < at(d - 1, k + 1))
    const pk = down ? k + 1 : k - 1
    const px = at(d - 1, pk)
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
  return true
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
