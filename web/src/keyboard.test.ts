import { isShortcut } from './keyboard'

// A Russian (ЙЦУКЕН) keyboard layout produces a different `key` for a given
// physical key than a US layout does — pressing the physical "F" key sends
// `key: 'а'`, not `key: 'f'`/`'F'` — while `code` always names the physical
// key ('KeyF') regardless of layout. `isShortcut` must match on `code`.
function ruF(overrides: Partial<KeyboardEvent> = {}) {
  return { key: 'а', code: 'KeyF', ctrlKey: false, metaKey: false, shiftKey: false, ...overrides } as unknown as KeyboardEvent
}

test('matches a Ctrl+letter shortcut by code, regardless of the layout-dependent key', () => {
  expect(isShortcut(ruF({ ctrlKey: true }), 'KeyF', { ctrl: true })).toBe(true)
})

test('does not match without the required Ctrl/Cmd modifier', () => {
  expect(isShortcut(ruF(), 'KeyF', { ctrl: true })).toBe(false)
})

test('Cmd (metaKey) satisfies a `ctrl` requirement, same as Ctrl', () => {
  expect(isShortcut(ruF({ metaKey: true }), 'KeyF', { ctrl: true })).toBe(true)
})

test('does not match a different physical key even if `key` happens to match', () => {
  const e = { key: 'f', code: 'KeyG', ctrlKey: true, metaKey: false, shiftKey: false } as unknown as KeyboardEvent
  expect(isShortcut(e, 'KeyF', { ctrl: true })).toBe(false)
})

test('a shortcut with no `ctrl` option set matches regardless of Ctrl/Cmd (matching the code is enough)', () => {
  const e = { key: '0', code: 'Digit0', ctrlKey: true, metaKey: false, shiftKey: false } as unknown as KeyboardEvent
  expect(isShortcut(e, 'Digit0')).toBe(true)
})

test('`ctrl: false` explicitly rejects the shortcut while Ctrl/Cmd is held', () => {
  const e = { key: '0', code: 'Digit0', ctrlKey: true, metaKey: false, shiftKey: false } as unknown as KeyboardEvent
  expect(isShortcut(e, 'Digit0', { ctrl: false })).toBe(false)
})

test('a plain shortcut matches with no modifiers held', () => {
  const e = { key: '0', code: 'Digit0', ctrlKey: false, metaKey: false, shiftKey: false } as unknown as KeyboardEvent
  expect(isShortcut(e, 'Digit0')).toBe(true)
})

test('accepts a list of codes (e.g. the numpad equivalent)', () => {
  const numpad = { key: '0', code: 'Numpad0', ctrlKey: false, metaKey: false, shiftKey: false } as unknown as KeyboardEvent
  expect(isShortcut(numpad, ['Digit0', 'Numpad0'])).toBe(true)
  const digit = { key: '0', code: 'Digit0', ctrlKey: false, metaKey: false, shiftKey: false } as unknown as KeyboardEvent
  expect(isShortcut(digit, ['Digit0', 'Numpad0'])).toBe(true)
})

test('Equal matches with or without Shift (covers both "=" and "+")', () => {
  const plain = { key: '=', code: 'Equal', ctrlKey: false, metaKey: false, shiftKey: false } as unknown as KeyboardEvent
  const shifted = { key: '+', code: 'Equal', ctrlKey: false, metaKey: false, shiftKey: true } as unknown as KeyboardEvent
  expect(isShortcut(plain, ['Equal', 'NumpadAdd'])).toBe(true)
  expect(isShortcut(shifted, ['Equal', 'NumpadAdd'])).toBe(true)
})
