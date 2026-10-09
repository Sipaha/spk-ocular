import { afterEach, describe, expect, it } from 'vitest'
import { KEYS, cycleArea, globalShortcut } from './shortcuts'

type Init = Partial<KeyboardEventInit> & { target?: Element; composing?: boolean; altGraph?: boolean }

function ev({ target, composing, altGraph, ...init }: Init): KeyboardEvent {
  const e = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, ...init })
  Object.defineProperty(e, 'target', { value: target ?? document.body })
  if (composing) Object.defineProperty(e, 'isComposing', { value: true })
  if (altGraph) Object.defineProperty(e, 'getModifierState', { value: (k: string) => k === 'AltGraph' })
  return e
}

afterEach(() => {
  document.body.innerHTML = ''
})

describe('globalShortcut', () => {
  it('Ctrl+K is the palette, also in a text field', () => {
    const input = document.body.appendChild(document.createElement('input'))
    expect(globalShortcut(ev({ code: 'KeyK', key: 'k', ctrlKey: true }))).toBe('palette')
    expect(globalShortcut(ev({ code: 'KeyK', key: 'л', ctrlKey: true, target: input }))).toBe('palette') // Russian layout: the physical key
    expect(globalShortcut(ev({ code: 'KeyK', key: 'K', ctrlKey: true, shiftKey: true }))).toBeNull()
  })

  it('? is help by the physical key (Shift+/ types "," on a Russian layout), never in a field', () => {
    expect(globalShortcut(ev({ code: 'Slash', key: '?', shiftKey: true }))).toBe('help')
    expect(globalShortcut(ev({ code: 'Slash', key: ',', shiftKey: true }))).toBe('help')
    expect(globalShortcut(ev({ code: 'Comma', key: '?', shiftKey: true }))).toBeNull() // a different physical key
    const input = document.body.appendChild(document.createElement('input'))
    expect(globalShortcut(ev({ code: 'Slash', key: '?', shiftKey: true, target: input }))).toBeNull()
  })

  it('/ focuses a filter: by the physical key without Shift', () => {
    expect(globalShortcut(ev({ code: 'Slash', key: '/' }))).toBe('filter')
    expect(globalShortcut(ev({ code: 'Slash', key: '.' }))).toBe('filter')
    expect(globalShortcut(ev({ code: 'Digit7', key: '/', shiftKey: true }))).toBeNull() // a different physical key
    const area = document.body.appendChild(document.createElement('textarea'))
    expect(globalShortcut(ev({ code: 'Slash', key: '/', target: area }))).toBeNull()
    expect(globalShortcut(ev({ code: 'Slash', key: '/', ctrlKey: true }))).toBeNull()
    expect(globalShortcut(ev({ code: 'Slash', key: ',', shiftKey: true }))).not.toBe('filter') // Russian Shift+Slash is "?"
    expect(globalShortcut(ev({ code: 'KeyA', key: 'a' }))).toBeNull()
  })

  it('F6 and Shift+F6 move between areas, from a field too', () => {
    const input = document.body.appendChild(document.createElement('input'))
    expect(globalShortcut(ev({ code: 'F6', key: 'F6' }))).toBe('nextArea')
    expect(globalShortcut(ev({ code: 'F6', key: 'F6', shiftKey: true, target: input }))).toBe('prevArea')
  })

  it('keys of a terminal, an IME composition, AltGr and held keys are not the app\'s', () => {
    const term = document.body.appendChild(document.createElement('div'))
    term.setAttribute('data-terminal', '')
    const inside = term.appendChild(document.createElement('textarea'))
    expect(globalShortcut(ev({ code: 'KeyK', ctrlKey: true, target: inside }))).toBeNull()
    expect(globalShortcut(ev({ code: 'F6', key: 'F6', target: inside }))).toBeNull()
    expect(globalShortcut(ev({ code: 'KeyK', ctrlKey: true, composing: true }))).toBeNull()
    expect(globalShortcut(ev({ code: 'Slash', key: '/', altGraph: true }))).toBeNull()
    expect(globalShortcut(ev({ code: 'KeyK', ctrlKey: true, repeat: true }))).toBeNull()
  })

  it('an open modal dialog keeps its keys', () => {
    const dlg = document.body.appendChild(document.createElement('div'))
    dlg.setAttribute('aria-modal', 'true')
    expect(globalShortcut(ev({ code: 'KeyK', ctrlKey: true }))).toBeNull()
    expect(globalShortcut(ev({ code: 'F6', key: 'F6' }))).toBeNull()
  })

  it('text in CodeMirror is typing', () => {
    const cm = document.body.appendChild(document.createElement('div'))
    cm.className = 'cm-content'
    cm.contentEditable = 'true'
    expect(globalShortcut(ev({ code: 'Slash', key: '?', shiftKey: true, target: cm }))).toBeNull()
  })
})

describe('KEYS', () => {
  it('each key is described once per scope', () => {
    const seen = new Set<string>()
    for (const k of KEYS) {
      const id = `${k.scope}:${k.id}`
      expect(seen.has(id), id).toBe(false)
      seen.add(id)
    }
  })
})

describe('cycleArea', () => {
  function area(name: string, focusable = true, hidden = false) {
    const el = document.body.appendChild(document.createElement('div'))
    el.dataset.area = name
    if (hidden) el.hidden = true
    const btn = el.appendChild(document.createElement('button'))
    if (focusable) btn.dataset.areaFocus = ''
    return { el, btn }
  }

  it('goes round the visible areas in order, both ways, skipping hidden ones', () => {
    const targets = area('targets')
    const nav = area('nav')
    area('details', true, true)
    const dock = area('dock')
    const table = area('table')
    targets.btn.focus()
    cycleArea(1)
    expect(document.activeElement).toBe(nav.btn)
    cycleArea(1)
    expect(document.activeElement).toBe(table.btn) // order is fixed, not the DOM's
    cycleArea(1)
    expect(document.activeElement).toBe(dock.btn)
    cycleArea(1)
    expect(document.activeElement).toBe(targets.btn)
    cycleArea(-1)
    expect(document.activeElement).toBe(dock.btn)
  })

  it('from outside every area starts at the first (or the last going back)', () => {
    const targets = area('targets')
    const dock = area('dock')
    document.body.focus()
    cycleArea(1)
    expect(document.activeElement).toBe(targets.btn)
    ;(document.activeElement as HTMLElement).blur()
    cycleArea(-1)
    expect(document.activeElement).toBe(dock.btn)
  })
})
