import { runScopeHandlers, type EditorView } from '@codemirror/view'

const punctuation: Record<string, readonly [string, string]> = {
  Backquote: ['`', '~'], Minus: ['-', '_'], Equal: ['=', '+'],
  BracketLeft: ['[', '{'], BracketRight: [']', '}'], Backslash: ['\\', '|'],
  Semicolon: [';', ':'], Quote: ["'", '"'], Comma: [',', '<'], Period: ['.', '>'], Slash: ['/', '?'],
}

/** Normalize modified physical keys before CodeMirror's deprecated keyCode
 * fallback. Plain text, composition and AltGr retain their input semantics. */
export function bindPhysicalEditorKeys(view: EditorView): () => void {
  const handle = (event: KeyboardEvent) => {
    if (event.defaultPrevented || event.isComposing || event.getModifierState('AltGraph') || !(event.ctrlKey || event.metaKey || event.altKey)) return
    let key: string | undefined
    if (/^Key[A-Z]$/.test(event.code)) key = event.shiftKey ? event.code.slice(3) : event.code.slice(3).toLowerCase()
    else if (/^Digit[0-9]$/.test(event.code)) key = event.shiftKey ? ')!@#$%^&*('[Number(event.code.slice(5))] : event.code.slice(5)
    else key = punctuation[event.code]?.[event.shiftKey ? 1 : 0]
    if (!key) return
    const normalized = new KeyboardEvent('keydown', { key, code: event.code, ctrlKey: event.ctrlKey, metaKey: event.metaKey, altKey: event.altKey, shiftKey: event.shiftKey, repeat: event.repeat })
    const scope = event.target instanceof Element && event.target.closest('.cm-search') ? 'search-panel' : 'editor'
    if (runScopeHandlers(view, normalized, scope)) { event.preventDefault(); event.stopPropagation() }
  }
  view.dom.addEventListener('keydown', handle, true)
  return () => view.dom.removeEventListener('keydown', handle, true)
}
