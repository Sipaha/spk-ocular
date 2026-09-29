// Regex search in a worker with a time budget. The worker holds its own
// copy of the lines (sent incrementally) and answers with the ranges of
// matching lines; a pattern that takes too long gets the worker terminated
// (the only way to stop a runaway regex) and is reported as too slow.

import { regexRanges, type Range } from './match'

export type ToWorker =
  | { t: 'pattern'; seq: number; source: string }
  | { t: 'add'; seq: number; items: [id: number, text: string][] }
  | { t: 'drop'; before: number }

export type FromWorker = { t: 'hits'; seq: number; hits: [id: number, ranges: Range[]][]; reset: boolean }

export interface WorkerLike {
  postMessage(m: ToWorker): void
  terminate(): void
  onmessage: ((e: { data: FromWorker }) => void) | null
}

/** A request not answered within this is a runaway regex. */
export const REGEX_BUDGET_MS = 1500

export type RegexState = 'idle' | 'busy' | 'ready' | 'slow'

export class RegexSearch {
  private worker: WorkerLike | null = null
  private seq = 0
  private waiting = new Set<number>()
  private timer: ReturnType<typeof setTimeout> | null = null
  private sentUpTo = 0 // highest id sent
  hits = new Map<number, Range[]>()
  state: RegexState = 'idle'

  constructor(
    private make: () => WorkerLike,
    private onChange: () => void,
    private budget = REGEX_BUDGET_MS,
  ) {}

  /** A new pattern over the current lines ("" = off). */
  setPattern(source: string, lines: { id: number; plain: string }[]) {
    this.stop()
    this.hits = new Map()
    this.sentUpTo = 0
    if (!source) {
      this.state = 'idle'
      this.onChange()
      return
    }
    this.worker = this.make()
    this.worker.onmessage = (e) => this.receive(e.data)
    this.send({ t: 'pattern', seq: ++this.seq, source })
    this.state = 'busy'
    this.add(lines)
    this.onChange()
  }

  /** New lines (ids above the last sent ones). */
  add(lines: { id: number; plain: string }[]) {
    if (!this.worker) return
    const items: [number, string][] = []
    for (const l of lines) if (l.id > this.sentUpTo) items.push([l.id, l.plain])
    if (!items.length) return
    this.sentUpTo = items[items.length - 1][0]
    this.send({ t: 'add', seq: ++this.seq, items })
  }

  /** Lines below id are gone from the buffer. */
  drop(before: number) {
    if (!this.worker) return
    this.worker.postMessage({ t: 'drop', before })
    for (const id of this.hits.keys()) if (id < before) this.hits.delete(id)
  }

  stop() {
    this.worker?.terminate()
    this.worker = null
    this.waiting.clear()
    if (this.timer) clearTimeout(this.timer)
    this.timer = null
  }

  private send(m: ToWorker & { seq: number }) {
    this.waiting.add(m.seq)
    this.worker!.postMessage(m)
    this.arm()
  }

  private arm() {
    if (this.timer || this.waiting.size === 0) return
    this.timer = setTimeout(() => {
      this.timer = null
      if (this.waiting.size === 0) return
      this.stop()
      this.hits = new Map()
      this.state = 'slow'
      this.onChange()
    }, this.budget)
  }

  private receive(m: FromWorker) {
    this.waiting.delete(m.seq)
    if (m.reset) this.hits = new Map()
    for (const [id, r] of m.hits) this.hits.set(id, r)
    if (this.timer) clearTimeout(this.timer)
    this.timer = null
    this.arm() // still waiting for later requests: a fresh budget for them
    this.state = this.waiting.size ? 'busy' : 'ready'
    this.onChange()
  }
}

/** The worker's logic, shared with tests (search.worker.ts wires it up). */
export function workerCore(post: (m: FromWorker) => void) {
  let re: RegExp | null = null
  let lines: [number, string][] = []
  const scan = (items: [number, string][]) => {
    const hits: [number, Range[]][] = []
    if (!re) return hits
    for (const [id, text] of items) {
      const r = regexRanges(re, text)
      if (r.length) hits.push([id, r])
    }
    return hits
  }
  return (m: ToWorker) => {
    if (m.t === 'pattern') {
      re = new RegExp(m.source, 'gi')
      post({ t: 'hits', seq: m.seq, hits: scan(lines), reset: true })
    } else if (m.t === 'add') {
      lines = lines.concat(m.items)
      post({ t: 'hits', seq: m.seq, hits: scan(m.items), reset: false })
    } else if (m.t === 'drop') {
      let i = 0
      while (i < lines.length && lines[i][0] < m.before) i++
      if (i) lines = lines.slice(i)
    }
  }
}
