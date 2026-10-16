/**
 * xterm 6 retains already-delivered text in its helper textarea. A later IME
 * replacement can replay that history (xtermjs/xterm.js#6078). Clear only after
 * xterm's pending input/composition timers, while no new edit has started.
 * Screen readers retain xterm's normal announcement buffer.
 */
export function clearDeliveredInput(textarea: HTMLTextAreaElement, screenReaderMode: () => boolean): () => void {
  let composing = false
  let timer: ReturnType<typeof setTimeout> | undefined
  const cancel = () => {
    if (timer !== undefined) clearTimeout(timer)
    timer = undefined
  }
  const settle = () => {
    cancel()
    if (composing || screenReaderMode()) return
    timer = setTimeout(() => {
      timer = undefined
      if (!composing && !screenReaderMode()) textarea.value = ''
    }, 0)
  }
  const start = () => { cancel(); composing = true }
  const end = () => { composing = false; settle() }
  const input = (event: Event) => { if (!(event as InputEvent).isComposing) settle() }
  // Installed after term.open: xterm's listeners enqueue their finalizers first.
  const listeners: [string, EventListener][] = [
    ['keydown', cancel], ['beforeinput', cancel], ['compositionstart', start],
    ['compositionend', end], ['input', input], ['keyup', settle], ['blur', cancel],
  ]
  for (const [type, listener] of listeners) textarea.addEventListener(type, listener)
  return () => {
    cancel()
    for (const [type, listener] of listeners) textarea.removeEventListener(type, listener)
  }
}
