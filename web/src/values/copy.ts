import { Clipboard } from '@wailsio/runtime'
import { isDesktop } from '../api/client'

// Copying a value. The clipboard is one for the whole application, so
// copies have one generation: any new copy (of any key or object) makes
// every earlier one stale, and a stale copy's value is never written.
// Writes go one at a time: an older system write never ends after a newer
// one. Not term/clipboard.ts: its "success" swallows failures, and a copy
// says "copied" only after the write really succeeded.

let gen = 0
let queue: Promise<unknown> = Promise.resolve()

/** Writes text to the system clipboard; rejects when it was not written. */
export async function writeClipboard(text: string): Promise<void> {
  if (isDesktop()) {
    try {
      await Clipboard.SetText(text)
      return
    } catch {
      // The page's own clipboard, if the runtime's failed.
    }
  }
  if (!navigator.clipboard) throw new Error('no clipboard')
  await navigator.clipboard.writeText(text)
}

export type CopyOutcome = 'copied' | 'superseded' | 'stale'

interface Copy {
  /** Reads the value now (RevealValue). */
  read: () => Promise<string>
  /** The copy is still for what is shown (details open, same object and revision). */
  valid: () => boolean
  write?: (text: string) => Promise<void>
}

/**
 * Reads a value and writes it to the clipboard if this is still the latest
 * copy and still valid, after the writes before it ended. 'copied' only
 * when written and not overtaken; a failed read or write of the latest copy
 * rejects.
 */
export async function copyValue({ read, valid, write = writeClipboard }: Copy): Promise<CopyOutcome> {
  const g = ++gen
  let text: string
  try {
    text = await read()
  } catch (e) {
    if (g !== gen) return 'superseded'
    throw e
  }
  if (g !== gen) return 'superseded'
  const run = queue.then(async (): Promise<CopyOutcome> => {
    // Checked again: a newer copy or a move may have come while waiting.
    if (g !== gen) return 'superseded'
    if (!valid()) return 'stale'
    await write(text)
    return g === gen ? 'copied' : 'superseded'
  })
  queue = run.catch(() => {})
  return run
}

/** Tests: no copies. */
export function resetCopies() {
  gen = 0
  queue = Promise.resolve()
}
