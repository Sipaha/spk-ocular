import { describe, expect, it } from 'vitest'
import { FrameTooLarge, MAX_FRAME_CHARS, NdjsonDecoder } from './ndjson'

const enc = new TextEncoder()

describe('NdjsonDecoder', () => {
  it('joins frames and UTF-8 split across chunks', () => {
    const bytes = enc.encode('{"k":"lines","s":1,"l":[["t","привет ✓"]]}\n{"k":"ready"}\n')
    const d = new NdjsonDecoder()
    const frames = []
    // one byte at a time: every split point, including inside a character
    for (const b of bytes) frames.push(...d.push(new Uint8Array([b])))
    expect(frames).toEqual([{ k: 'lines', s: 1, l: [['t', 'привет ✓']] }, { k: 'ready' }])
    expect(d.end()).toEqual([])
  })

  it('keeps a trailing frame without a newline for the end', () => {
    const d = new NdjsonDecoder()
    expect(d.push(enc.encode('{"k":"ping"}\n{"k":"end","reason":"done"}'))).toEqual([{ k: 'ping' }])
    expect(d.end()).toEqual([{ k: 'end', reason: 'done' }])
  })

  it('bounds a partial frame', () => {
    const d = new NdjsonDecoder()
    expect(() => d.push(enc.encode('{"k":"lines","l":"' + 'x'.repeat(MAX_FRAME_CHARS)))).toThrow(FrameTooLarge)
  })
})
