import { useEffect, useState, type ComponentProps, type KeyboardEvent, type RefObject } from 'react'
import { focusMark, restoreFocus } from '../shortcuts'
import { CloseIcon } from './icons'

const controls = 'button:not(:disabled),a[href],input:not(:disabled),textarea:not(:disabled),select:not(:disabled),summary,[tabindex]:not([tabindex="-1"])'

function reachable(element: HTMLElement): boolean {
 if (element.tabIndex < 0 || element.closest('[inert]')) return false
 for (let parent: HTMLElement | null = element; parent; parent = parent.parentElement) {
  const style = getComputedStyle(parent)
  if (parent.hidden || style.display === 'none' || style.visibility === 'hidden') return false
  if (parent.tagName === 'DETAILS' && !parent.hasAttribute('open') && !parent.querySelector(':scope > summary')?.contains(element)) return false
 }
 return true
}

/** Shared read-only/setup dialog lifecycle; nested widgets can consume their
 * own keys first. Feature owners retain close/edit/review decisions. */
export function useModalFocus(box: RefObject<HTMLElement | null>, onClose: () => void, initial?: RefObject<HTMLElement | null>) {
 const [mark] = useState(focusMark)
 useEffect(() => {
  const focus = initial?.current ?? box.current
  focus?.focus()
  return () => restoreFocus(mark)
 }, [box, initial, mark])
 return (event: KeyboardEvent<HTMLElement>) => {
  if (event.defaultPrevented || event.nativeEvent.isComposing) return
  if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); onClose(); return }
  if (event.key !== 'Tab') return
  const items = [...(box.current?.querySelectorAll<HTMLElement>(controls) ?? [])].filter(reachable)
  const first = items[0], last = items.at(-1), active = document.activeElement
  if (!first) { event.preventDefault(); box.current?.focus() }
  else if (active === box.current || !box.current?.contains(active) || event.shiftKey && active === first || !event.shiftKey && active === last) {
   event.preventDefault(); (event.shiftKey ? last : first)?.focus()
  }
 }
}

export function ModalCloseButton({ label, className = '', ...props }: ComponentProps<'button'> & { label: string }) {
 return <button {...props} type="button" aria-label={label} title={label} className={'action-icon-button shrink-0 ' + className}><CloseIcon className="h-[20px] w-[20px]" /></button>
}
