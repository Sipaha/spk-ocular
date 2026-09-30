// The app's keys in one place: what the help (?) lists, and the matcher of
// the app-wide ones. Routing, top to bottom: an open modal dialog or menu
// keeps its keys; a terminal gets every key (Ctrl+K, Esc and F6 included) and
// CodeMirror its own; then the app's. Letters go by KeyboardEvent.code, so
// they work on a Russian layout (keyboard.ts).
import type { MessageKey } from './i18n'
import { inTerminal, isShortcut } from './keyboard'

export type Scope = 'global' | 'lists' | 'table' | 'details' | 'logs' | 'terminal'

export type GlobalId = 'palette' | 'help' | 'filter' | 'nextArea' | 'prevArea' | 'resync'

export interface KeyDef {
  id: string
  scope: Scope
  /** As shown in the help. */
  keys: string
  help: MessageKey
  /** App-wide keys only: whether e is this key. */
  match?: (e: KeyboardEvent) => boolean
  /** Works while typing in a text field. */
  inFields?: boolean
}

const plain = (e: KeyboardEvent) => !e.ctrlKey && !e.metaKey && !e.altKey

export const KEYS: KeyDef[] = [
  { id: 'palette', scope: 'global', keys: 'Ctrl+K', help: 'keys.palette', inFields: true, match: (e) => !e.altKey && isShortcut(e, 'KeyK', { ctrl: true, shift: false }) },
  // "?" by the physical key (a Russian layout types ","), or by the character.
  { id: 'help', scope: 'global', keys: '?', help: 'keys.help', match: (e) => plain(e) && (e.key === '?' || isShortcut(e, 'Slash', { shift: true })) },
  // "/" by the physical key (a Russian layout types "."), or by the character
  // (layouts with "/" elsewhere, e.g. Shift+7).
  { id: 'filter', scope: 'global', keys: '/', help: 'keys.filter', match: (e) => plain(e) && (e.key === '/' || isShortcut(e, 'Slash', { shift: false })) },
  // F-keys type nothing: they work from a field too.
  { id: 'nextArea', scope: 'global', keys: 'F6', help: 'keys.nextArea', inFields: true, match: (e) => plain(e) && e.key === 'F6' && !e.shiftKey },
  { id: 'prevArea', scope: 'global', keys: 'Shift+F6', help: 'keys.prevArea', inFields: true, match: (e) => plain(e) && e.key === 'F6' && e.shiftKey },
  // Only where the open view offers it (a [data-resync] button): elsewhere F5 stays the page's.
  { id: 'resync', scope: 'global', keys: 'F5', help: 'keys.resync', inFields: true, match: (e) => plain(e) && e.key === 'F5' && !e.shiftKey && !!document.querySelector('[data-resync]') },
  { id: 'move', scope: 'lists', keys: '↑ ↓', help: 'keys.move' },
  { id: 'page', scope: 'lists', keys: 'PageUp PageDown', help: 'keys.page' },
  { id: 'ends', scope: 'lists', keys: 'Home End', help: 'keys.ends' },
  { id: 'open', scope: 'lists', keys: 'Enter', help: 'keys.open' },
  { id: 'logs', scope: 'table', keys: 'L', help: 'keys.logs' },
  { id: 'terminal', scope: 'table', keys: 'S', help: 'keys.terminal' },
  { id: 'terminalDialog', scope: 'table', keys: 'Shift+S', help: 'keys.terminalDialog' },
  { id: 'delete', scope: 'table', keys: 'Delete', help: 'keys.delete' },
  { id: 'menu', scope: 'table', keys: 'Shift+F10', help: 'keys.menu' },
  { id: 'back', scope: 'details', keys: 'Alt+←', help: 'keys.back' },
  { id: 'close', scope: 'details', keys: 'Esc', help: 'keys.close' },
  { id: 'search', scope: 'logs', keys: 'Ctrl+F', help: 'keys.logSearch' },
  { id: 'next', scope: 'logs', keys: 'F3 Shift+F3', help: 'keys.logNext' },
  { id: 'save', scope: 'logs', keys: 'Ctrl+S', help: 'keys.logSave' },
  { id: 'clear', scope: 'logs', keys: 'Ctrl+L', help: 'keys.logClear' },
  { id: 'copy', scope: 'terminal', keys: 'Ctrl+Shift+C', help: 'keys.termCopy' },
  { id: 'paste', scope: 'terminal', keys: 'Ctrl+Shift+V', help: 'keys.termPaste' },
  { id: 'rest', scope: 'terminal', keys: 'Ctrl+K Esc F6 …', help: 'keys.termRest' },
]

/** A text field, a select or an editor: letters there are text. */
export function isTyping(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false
  return target instanceof HTMLInputElement || target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement || target.isContentEditable || target.contentEditable === 'true'
}

/** A modal dialog or a menu is open: it owns the keyboard (menus consume only
 * their own keys — the rest must not act behind them). */
export function overlayOpen(): boolean {
  return !!document.querySelector('[aria-modal="true"], [role="menu"]')
}

/** The key was already handled by whom it was meant for (CodeMirror closing
 * its search, a field clearing itself) or is part of a composition. */
export function consumed(e: KeyboardEvent): boolean {
  return e.defaultPrevented || e.isComposing
}

/** The app-wide key e is, or null (it belongs to someone else or to nobody). */
export function globalShortcut(e: KeyboardEvent): GlobalId | null {
  if (consumed(e) || e.repeat || e.getModifierState?.('AltGraph')) return null
  if (inTerminal(e.target) || overlayOpen()) return null
  const typing = isTyping(e.target)
  for (const k of KEYS) {
    if (k.scope === 'global' && k.match?.(e) && (!typing || k.inFields)) return k.id as GlobalId
  }
  return null
}

// Areas F6 goes round, in this order; each marks itself with data-area and
// its focus target with data-area-focus (else the area itself).
const AREAS = ['targets', 'nav', 'table', 'details', 'dock'] as const
export type Area = (typeof AREAS)[number]

const visible = (el: HTMLElement) => el.isConnected && !el.closest('[hidden]')

/** The element focusing an area focuses. */
export function areaFocus(area: Area): HTMLElement | null {
  const el = document.querySelector<HTMLElement>(`[data-area="${area}"]`)
  if (!el || !visible(el)) return null
  const inner = [...el.querySelectorAll<HTMLElement>('[data-area-focus]')].find(visible)
  return inner ?? el
}

/** F6 (dir 1) / Shift+F6 (dir -1): focus the next visible area. */
export function cycleArea(dir: 1 | -1) {
  const list = AREAS.map((a) => ({ a, el: areaFocus(a) })).filter((x): x is { a: Area; el: HTMLElement } => !!x.el)
  if (!list.length) return
  const cur = (document.activeElement as HTMLElement | null)?.closest<HTMLElement>('[data-area]')?.dataset.area
  const i = list.findIndex((x) => x.a === cur)
  const next = i < 0 ? (dir > 0 ? 0 : list.length - 1) : (i + dir + list.length) % list.length
  list[next].el.focus()
}

/** Where focus was: the element and its area (the fallback if the element goes). */
export function focusMark(): { el: HTMLElement | null; area: Area | null } {
  const el = document.activeElement instanceof HTMLElement && document.activeElement !== document.body ? document.activeElement : null
  const area = (el?.closest<HTMLElement>('[data-area]')?.dataset.area ?? null) as Area | null
  return { el, area }
}

/**
 * Focus back where it was; if that element is gone (a deleted row, closed
 * details) — its area, else the table.
 */
export function restoreFocus(mark: { el: HTMLElement | null; area: Area | null }) {
  if (mark.el?.isConnected && visible(mark.el)) {
    mark.el.focus()
    return
  }
  ;(areaFocus(mark.area ?? 'table') ?? areaFocus('table'))?.focus()
}
