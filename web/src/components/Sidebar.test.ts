import { describe, expect, it } from 'vitest'
import { isFilterKey } from './Sidebar'

const key = (k: string, code: string, mods: Partial<KeyboardEvent> = {}) =>
  ({ key: k, code, ctrlKey: false, metaKey: false, altKey: false, shiftKey: false, ...mods }) as KeyboardEvent

describe('isFilterKey', () => {
  it('matches "/" on US and on the same physical key in the Russian layout', () => {
    expect(isFilterKey(key('/', 'Slash'))).toBe(true)
    expect(isFilterKey(key('.', 'Slash'))).toBe(true) // ЙЦУКЕН
  })
  it('matches "/" typed elsewhere (German Shift+7)', () => {
    expect(isFilterKey(key('/', 'Digit7', { shiftKey: true }))).toBe(true)
  })
  it('ignores modified and other keys', () => {
    expect(isFilterKey(key('/', 'Slash', { ctrlKey: true }))).toBe(false)
    expect(isFilterKey(key(',', 'Slash', { shiftKey: true }))).toBe(false) // Russian Shift+Slash
    expect(isFilterKey(key('a', 'KeyA'))).toBe(false)
  })
})
