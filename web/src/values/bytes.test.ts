import { describe, expect, it } from 'vitest'
import { asText, fromBase64, isText, MAX_VALUE_BYTES, MAX_VALUE_INPUT, toBase64, typedBytes, utf8, validKey } from './bytes'

const bytes = (...b: number[]) => Uint8Array.from(b)

describe('value bytes', () => {
  it('text is UTF-8 without control characters but \\n and \\t (as Go says)', () => {
    expect(isText(utf8('a\tb\nc'))).toBe(true)
    expect(isText(utf8('пароль ✓'))).toBe(true)
    expect(isText(new Uint8Array())).toBe(true)
    expect(isText(utf8('a\r\nb'))).toBe(false)
    expect(isText(utf8('a\rb'))).toBe(false)
    expect(isText(bytes(0x61, 0x00))).toBe(false)
    expect(isText(bytes(0xff, 0xfe))).toBe(false)
    expect(isText(utf8('\x7f'))).toBe(false)
    expect(isText(utf8('\u0085'))).toBe(false)
    // A BOM is a character, not a marker to drop: the bytes stay as they are.
    expect(asText(bytes(0xef, 0xbb, 0xbf, 0x61))).toBe('﻿a')
  })

  it('round trips: every value comes back byte for byte', () => {
    for (const b of [utf8('a\r\nb'), utf8('x\r'), utf8('line\n'), bytes(0), bytes(0xff, 0x00, 0x80), new Uint8Array(), utf8('﻿bom')]) {
      const sent = isText(b) ? typedBytes(asText(b)!, 'text') : typedBytes(toBase64(b), 'base64')
      expect('bytes' in sent && [...sent.bytes]).toEqual([...b])
    }
  })

  it('base64 is read strictly, lines skipped (as Go does)', () => {
    expect([...fromBase64('YWJj\nZGVm\r\n')!]).toEqual([...utf8('abcdef')])
    expect([...fromBase64('')!]).toEqual([])
    expect(fromBase64('YWI=')).not.toBeNull()
    expect(fromBase64('YWJ=')).toBeNull() // bits past the value
    expect(fromBase64('YWJj!')).toBeNull()
    expect(fromBase64('YWJ')).toBeNull() // no padding
    expect(fromBase64('YW Jj')).toBeNull()
  })

  it('limits: 1 MiB decoded, 1.5 MiB typed', () => {
    const mib = new Uint8Array(MAX_VALUE_BYTES)
    expect('bytes' in typedBytes(toBase64(mib), 'base64')).toBe(true)
    expect(typedBytes(toBase64(new Uint8Array(MAX_VALUE_BYTES + 1)), 'base64')).toEqual({ error: 'tooLong' })
    expect('bytes' in typedBytes('x'.repeat(MAX_VALUE_BYTES), 'text')).toBe(true)
    expect(typedBytes('я'.repeat(MAX_VALUE_BYTES / 2 + 1), 'text')).toEqual({ error: 'tooLong' })
    expect(typedBytes('\n'.repeat(MAX_VALUE_INPUT + 1), 'base64')).toEqual({ error: 'tooLong' })
  })

  it('keys', () => {
    expect(validKey('tls.crt')).toBe(true)
    expect(validKey('a_b-C.9')).toBe(true)
    expect(validKey('')).toBe(false)
    expect(validKey('a/b')).toBe(false)
    expect(validKey('k'.repeat(254))).toBe(false)
  })
})
