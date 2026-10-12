// Regex search in a worker with a time budget. The UI owns retained history;
// each fresh worker matches incremental batches without retaining their text.
// It answers with match ranges; a pattern that takes too long terminates the
// worker (the only way to stop a runaway regex) and is reported as too slow.

import { regexRanges, type Range } from './match'

export type ToWorker =
  | { t: 'pattern'; seq: number; source: string }
  | { t: 'add'; seq: number; items: [id: number, text: string][] }

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
  private floor = 0 // lines below this id are gone (answers may still name them)
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
    this.floor = 0
    if (!source) {
      this.state = 'idle'
      this.onChange()
      return
    }
    const worker = this.make()
    this.worker = worker
    worker.onmessage = (e) => {
      if (this.worker === worker) this.receive(e.data)
    }
    this.send({ t: 'pattern', seq: ++this.seq, source })
    this.state = 'busy'
    this.add(lines)
    this.onChange()
  }

  /** New lines (ids above the last sent ones). */
  add(lines: { id: number; plain: string }[]) {
    if (!this.worker) return
    const items: [number, string][] = []
    // Entries have monotonic IDs: skip the retained prefix without scanning it.
    let lo = 0
    let hi = lines.length
    while (lo < hi) {
      const mid = (lo + hi) >>> 1
      if (lines[mid].id <= this.sentUpTo) lo = mid + 1
      else hi = mid
    }
    for (let i = lo; i < lines.length; i++) items.push([lines[i].id, lines[i].plain])
    if (!items.length) return
    this.sentUpTo = items[items.length - 1][0]
    this.send({ t: 'add', seq: ++this.seq, items })
  }

  /** Lines below id are gone from the buffer. */
  drop(before: number) {
    if (!this.worker) return
    this.floor = Math.max(this.floor, before)
    for (const id of this.hits.keys()) if (id < before) this.hits.delete(id)
  }

  /** Every line sent so far is gone (the buffer was cleared / restarted). */
  dropAll() {
    this.drop(this.sentUpTo + 1)
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
    for (const [id, r] of m.hits) if (id >= this.floor) this.hits.set(id, r)
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
      post({ t: 'hits', seq: m.seq, hits: [], reset: true })
    } else if (m.t === 'add') {
      post({ t: 'hits', seq: m.seq, hits: scan(m.items), reset: false })
    }
  }
}
