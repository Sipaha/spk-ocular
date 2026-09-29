// Decoder for the log stream: NDJSON frames (one JSON object per line) over
// a chunked fetch body. The server half is internal/streams/logsink.go.

export type LineTuple = [ts: string, text: string, flags?: number]

export type Frame =
  | { k: 'source'; id: number; key: string; label: string; channel?: string }
  | { k: 'lines'; s: number; l: LineTuple[] }
  | { k: 'state'; s: number; state: string; class?: string; msg?: string }
  | { k: 'ready' }
  | { k: 'ping' }
  | { k: 'end'; reason: 'done' | 'gone' | 'error'; class?: string; message?: string }

/** Line flags (provider.LineCut / LineNoTime). */
export const LINE_CUT = 1
export const LINE_NO_TIME = 2

/**
 * A frame is at most one batch of lines (≤ 500 lines / 64 KB of text on the
 * server, each line ≤ 256 KiB); anything far larger means a broken stream.
 */
export const MAX_FRAME_CHARS = 8 << 20

export class FrameTooLarge extends Error {
  constructor() {
    super('log stream frame too large')
  }
}

/**
 * NdjsonDecoder turns body chunks into frames. UTF-8 sequences and frames
 * split across chunks are joined; the partial frame is bounded.
 */
export class NdjsonDecoder {
  private dec = new TextDecoder()
  private pending = ''

  push(chunk: Uint8Array): Frame[] {
    return this.split(this.pending + this.dec.decode(chunk, { stream: true }))
  }

  /** The body ended: a trailing frame without a newline is still a frame. */
  end(): Frame[] {
    const rest = this.pending + this.dec.decode()
    this.pending = ''
    return rest.trim() ? this.split(rest + '\n') : []
  }

  private split(text: string): Frame[] {
    const out: Frame[] = []
    let start = 0
    for (;;) {
      const nl = text.indexOf('\n', start)
      if (nl < 0) break
      const line = text.slice(start, nl)
      start = nl + 1
      if (line) out.push(JSON.parse(line) as Frame)
    }
    this.pending = text.slice(start)
    if (this.pending.length > MAX_FRAME_CHARS) throw new FrameTooLarge()
    return out
  }
}
