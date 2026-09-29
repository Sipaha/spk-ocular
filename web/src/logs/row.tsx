import type { ReactNode } from 'react'
import { parseAnsi, PLAIN, styleCss } from './ansi'
import type { LogEntry } from './buffer'
import type { Range } from './match'
import { LINE_CUT } from './ndjson'

/** Shown (and copied) after a line the backend cut at its size limit. */
export const CUT_MARK = ' … [line cut: longer than 256 KiB]'

export interface RowOpts {
  showTime: boolean
  showSource: boolean
  labelOf: (src: number) => string
}

const pad = (n: number, w = 2) => String(n).padStart(w, '0')

/** Local wall-clock time of a provider timestamp: HH:MM:SS.mmm ("" if none). */
export function fmtTime(ts: string): string {
  if (!ts) return ''
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ''
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${pad(d.getMilliseconds(), 3)}`
}

const TIME_WIDTH = 12

/** Exactly the text a row shows: copies and selection offsets use it. */
export function rowText(e: LogEntry, o: RowOpts): string {
  let s = ''
  if (o.showTime) s += fmtTime(e.ts).padEnd(TIME_WIDTH) + ' '
  if (o.showSource) s += o.labelOf(e.src) + ' '
  return s + e.plain + (e.flags & LINE_CUT ? CUT_MARK : '')
}

const SOURCE_COLORS = ['#6fb3f2', '#e0a458', '#8fce7a', '#d68fd6', '#5fc7c0', '#e07f7f', '#b3a6f5', '#c9c36a']

/** A stable colour per source key. */
export function sourceColor(key: string): string {
  let h = 0
  for (let i = 0; i < key.length; i++) h = (h * 31 + key.charCodeAt(i)) | 0
  return SOURCE_COLORS[Math.abs(h) % SOURCE_COLORS.length]
}

/**
 * Short source labels: the pod-name part all sources share (up to a "-")
 * is dropped — "chatter-6c87cdb5b6-gfrk7/app" → "gfrk7/app" — so the
 * prefix column stays narrow.
 */
export function shortLabels(labels: string[]): string[] {
  if (labels.length < 2) return labels
  const pods = labels.map((l) => l.split('/')[0])
  let common = pods[0]
  for (const p of pods) {
    let i = 0
    while (i < common.length && i < p.length && common[i] === p[i]) i++
    common = common.slice(0, i)
  }
  const cut = common.lastIndexOf('-') + 1
  const sameCtr = new Set(labels.map((l) => l.split('/')[1])).size === 1
  return labels.map((l) => {
    const [pod, ctr] = l.split('/')
    const short = pod.slice(cut) || pod
    return sameCtr || ctr === undefined ? short : `${short}/${ctr}`
  })
}

/**
 * The message part of a row: ANSI spans (from the style the source left),
 * with search matches (plain-text ranges) marked on top.
 */
export function renderMessage(e: LogEntry, ranges: Range[], current: boolean): ReactNode {
  const msg = renderSpans(e, ranges, current)
  return e.flags & LINE_CUT ? [msg, <span key="cut" className="text-warning">{CUT_MARK}</span>] : msg
}

function renderSpans(e: LogEntry, ranges: Range[], current: boolean): ReactNode {
  if (e.plain === '') return ' ' // a blank line still needs a caret position (WebKit hit-testing)
  const spans = e.raw || e.style ? parseAnsi(e.raw ?? e.plain, e.style ?? PLAIN).spans : [{ text: e.plain, style: PLAIN }]
  if (!ranges.length && spans.length === 1 && spans[0].style === PLAIN) return e.plain
  const out: ReactNode[] = []
  let pos = 0
  let r = 0
  let key = 0
  for (const sp of spans) {
    const css = styleCss(sp.style)
    let i = 0
    while (i < sp.text.length) {
      const abs = pos + i
      while (r < ranges.length && ranges[r][1] <= abs) r++
      const inMatch = r < ranges.length && ranges[r][0] <= abs
      const until = inMatch ? ranges[r][1] : r < ranges.length ? ranges[r][0] : Infinity
      const piece = sp.text.slice(i, Math.min(sp.text.length, until - pos))
      if (inMatch) {
        out.push(
          <mark key={key++} className={current ? 'log-match-current' : 'log-match'}>
            {piece}
          </mark>,
        )
      } else out.push(css ? <span key={key++} style={css}>{piece}</span> : piece)
      i += piece.length
    }
    pos += sp.text.length
  }
  return out
}
