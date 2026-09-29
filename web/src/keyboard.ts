// isShortcut matches a keyboard shortcut by the *physical* key
// (`KeyboardEvent.code`, e.g. 'KeyF', 'Digit0', 'Equal') rather than the
// character it produces (`KeyboardEvent.key`). `key` is layout-dependent —
// on a Russian (ЙЦУКЕН) layout the physical "F" key sends `key: 'а'`, so a
// shortcut written as `e.key === 'f'` silently never fires — while `code`
// always names the same physical key regardless of the active layout.
// Layout-neutral keys (Escape, Enter, Arrow*, Delete, Backspace, Tab) are
// unaffected by layout and should keep using `e.key` directly; this helper
// is only for letter/digit/punctuation shortcuts.
export interface ShortcutOpts {
  // true: Ctrl or Cmd (metaKey) must be held. false: neither must be held.
  // undefined (default): don't care.
  ctrl?: boolean
  // true: Shift must be held. false: Shift must not be held. undefined
  // (default): don't care — e.g. Equal matches both "=" and "+".
  shift?: boolean
}

export function isShortcut(
  e: Pick<KeyboardEvent, 'code' | 'ctrlKey' | 'metaKey' | 'shiftKey'>,
  codes: string | string[],
  opts: ShortcutOpts = {},
): boolean {
  const list = Array.isArray(codes) ? codes : [codes]
  if (!list.includes(e.code)) return false
  const ctrlHeld = e.ctrlKey || e.metaKey
  if (opts.ctrl === true && !ctrlHeld) return false
  if (opts.ctrl === false && ctrlHeld) return false
  if (opts.shift === true && !e.shiftKey) return false
  if (opts.shift === false && e.shiftKey) return false
  return true
}
