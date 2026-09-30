// A value's bytes as the UI types and shows them: text (UTF-8, as typed)
// or base64. What is text follows Go (internal/providers/kubernetes
// isText): UTF-8 without control characters but \n and \t — a \r is not
// text, a browser's text field turns CRLF into LF.

/** One value's limit (decoded bytes) and what may be typed for it. */
export const MAX_VALUE_BYTES = 1 << 20
export const MAX_VALUE_INPUT = 3 << 19

export const utf8 = (s: string) => new TextEncoder().encode(s)

export function isText(b: Uint8Array): boolean {
  let s: string
  try {
    s = new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(b)
  } catch {
    return false
  }
  for (const ch of s) {
    const c = ch.codePointAt(0)!
    if ((c < 0x20 && c !== 0x0a && c !== 0x09) || c === 0x7f || (c >= 0x80 && c < 0xa0)) return false
  }
  return true
}

export function toBase64(b: Uint8Array): string {
  let s = ''
  for (let i = 0; i < b.length; i += 0x8000) s += String.fromCharCode(...b.subarray(i, i + 0x8000))
  return btoa(s)
}

/**
 * Standard base64 read as Go's strict decoder does: \r and \n are skipped,
 * padding is required, no bits past the value. null: not valid base64.
 */
export function fromBase64(s: string): Uint8Array | null {
  const t = s.replace(/[\r\n]/g, '')
  if (t.length % 4 !== 0 || !/^[A-Za-z0-9+/]*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(t)) return null
  let raw: string
  try {
    raw = atob(t)
  } catch {
    return null
  }
  const b = Uint8Array.from(raw, (c) => c.charCodeAt(0))
  // Bits past the value make another text of the same bytes.
  return toBase64(b) === t ? b : null
}

/** The value typed in a mode: its bytes, or why it cannot be sent. */
export function typedBytes(value: string, mode: 'text' | 'base64'): { bytes: Uint8Array } | { error: 'tooLong' | 'badBase64' } {
  if (value.length > MAX_VALUE_INPUT) return { error: 'tooLong' }
  const bytes = mode === 'text' ? utf8(value) : fromBase64(value)
  if (!bytes) return { error: 'badBase64' }
  if (bytes.length > MAX_VALUE_BYTES) return { error: 'tooLong' }
  return { bytes }
}

/** Text for bytes that are text, else null. */
export function asText(b: Uint8Array): string | null {
  return isText(b) ? new TextDecoder('utf-8', { ignoreBOM: true }).decode(b) : null
}

/** A key the Kubernetes API takes: letters, digits, '-', '_', '.'; at most 253. */
export const validKey = (k: string) => k.length > 0 && k.length <= 253 && /^[-._a-zA-Z0-9]+$/.test(k)
