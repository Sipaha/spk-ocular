// The log line buffer of one tab: entries with stable ids, bounded by lines
// and by characters; the oldest go first.

import { PLAIN, stripAnsi, type Style } from './ansi'
import { detectLevel, type LogLevel } from './levels'
import type { LineTuple } from './ndjson'

export const MAX_LOG_LINES = 50_000
export const MAX_LOG_CHARS = 16_000_000
/**
 * While the user reads (not following), trimming the front would slide the
 * text under them: the window may grow this much further before the oldest
 * lines go anyway.
 */
export const FROZEN_SLACK = 1.2

export interface LogEntry {
  /** Monotonic, never reused: the virtualizer's row key. */
  id: number
  /** Source id (stream-local). */
  src: number
  /** Provider timestamp (RFC 3339 with nanoseconds), "" if none. */
  ts: string
  /** Text without escapes: search, filters, levels and copies use it. */
  plain: string
  /** The text as received, only when it had escapes. */
  raw?: string
  /** ANSI style at the line start, only when not the default. */
  style?: Style
  level: LogLevel | null
  flags: number
}

/** Per-source ingestion state: multi-line records inherit their source's level and ANSI style. */
export interface Carry {
  level: LogLevel | null
  style: Style
}

export class Ingest {
  nextId = 1
  private carry = new Map<number, Carry>()

  entries(src: number, lines: LineTuple[]): LogEntry[] {
    const c = this.carry.get(src) ?? { level: null, style: PLAIN }
    const out: LogEntry[] = new Array(lines.length)
    for (let i = 0; i < lines.length; i++) {
      const [ts, text, flags] = lines[i]
      const start = c.style
      const { plain, end } = stripAnsi(text, start)
      const level = detectLevel(plain)
      if (level) c.level = level
      const e: LogEntry = { id: this.nextId++, src, ts, plain, level: level ?? c.level, flags: flags ?? 0 }
      if (plain !== text) e.raw = text
      if (start !== PLAIN) e.style = start
      c.style = end
      out[i] = e
    }
    this.carry.set(src, c)
    return out
  }

  /** Restore the same ids and ANSI/level carry when rendering an owned stream
   * in another OS window. The provider stream itself continues in the owner. */
  snapshotCarry(): [number, Carry][] { return [...this.carry] }

  restore(entries: LogEntry[], nextId?: number, carry?: [number, Carry][]) {
    this.carry.clear()
    this.nextId = nextId ?? (entries.length ? entries[entries.length - 1].id + 1 : 1)
    if (carry) { this.carry = new Map(carry); return }
    for (const entry of entries) {
      const { end } = stripAnsi(entry.raw ?? entry.plain, entry.style ?? PLAIN)
      this.carry.set(entry.src, { level: entry.level, style: end })
    }
  }

  /** A new stream: no carry (ids go on: they stay unique in the tab). */
  forget() {
    this.carry.clear()
  }

  /** A boundary in one source (restart, reconnect) or a source retired. */
  forgetSource(src: number) {
    this.carry.delete(src)
  }
}

export const entryChars = (e: LogEntry) => e.plain.length + (e.raw?.length ?? 0) + e.ts.length

export interface Window {
  entries: LogEntry[]
  chars: number
  /** Lines dropped from the front so far. */
  evicted: number
}

export const EMPTY_WINDOW: Window = { entries: [], chars: 0, evicted: 0 }

/** Appends and trims to the caps (stretched by FROZEN_SLACK while frozen). */
export function append(w: Window, add: LogEntry[], frozen = false, maxLines = MAX_LOG_LINES, maxChars = MAX_LOG_CHARS): Window {
  let chars = w.chars
  for (const e of add) chars += entryChars(e)
  const all = add.length ? w.entries.concat(add) : w.entries
  return trim({ entries: all, chars, evicted: w.evicted }, frozen, maxLines, maxChars)
}

export function trim(w: Window, frozen = false, maxLines = MAX_LOG_LINES, maxChars = MAX_LOG_CHARS): Window {
  const k = frozen ? FROZEN_SLACK : 1
  const lines = Math.floor(maxLines * k)
  const charCap = Math.floor(maxChars * k)
  let drop = Math.max(0, w.entries.length - lines)
  let chars = w.chars
  for (let i = 0; i < drop; i++) chars -= entryChars(w.entries[i])
  while (chars > charCap && drop < w.entries.length - 1) chars -= entryChars(w.entries[drop++])
  if (drop === 0) return w
  return { entries: w.entries.slice(drop), chars, evicted: w.evicted + drop }
}
