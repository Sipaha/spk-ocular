import { beforeEach, describe, expect, it, vi } from 'vitest'
import { discardEdits, editsHeld, holdEdits, keepEditing, mayLeave, resetGuard } from './guard'

beforeEach(resetGuard)

describe('guard', () => {
  it('goes at once when nothing is held or nothing changed', () => {
    const go = vi.fn()
    mayLeave(go)
    expect(go).toHaveBeenCalledTimes(1)
    const release = holdEdits({ dirty: () => false, discard: vi.fn() })
    mayLeave(go)
    expect(go).toHaveBeenCalledTimes(2)
    release()
  })

  it('asks when edits would be lost: discard drops them and goes; keep stays', () => {
    const discard = vi.fn()
    const focus = vi.fn()
    holdEdits({ dirty: () => true, discard, focus })
    expect(editsHeld()).toBe(true)
    const go = vi.fn()
    const stay = vi.fn()
    mayLeave(go, stay)
    expect(go).not.toHaveBeenCalled()
    keepEditing()
    expect(stay).toHaveBeenCalledTimes(1)
    expect(focus).toHaveBeenCalledTimes(1)
    expect(go).not.toHaveBeenCalled()

    mayLeave(go, stay)
    discardEdits()
    expect(discard).toHaveBeenCalledTimes(1)
    expect(go).toHaveBeenCalledTimes(1)
    expect(editsHeld()).toBe(false)
    // Nothing held any more: the next one goes at once.
    mayLeave(go)
    expect(go).toHaveBeenCalledTimes(2)
  })

  it('a second request while asking replaces the first (the first stays)', () => {
    holdEdits({ dirty: () => true, discard: vi.fn() })
    const first = { go: vi.fn(), stay: vi.fn() }
    const second = { go: vi.fn(), stay: vi.fn() }
    mayLeave(first.go, first.stay)
    mayLeave(second.go, second.stay)
    expect(first.stay).toHaveBeenCalledTimes(1)
    discardEdits()
    expect(first.go).not.toHaveBeenCalled()
    expect(second.go).toHaveBeenCalledTimes(1)
  })

  it('an editor gone on its own while asking lets the request go', () => {
    const release = holdEdits({ dirty: () => true, discard: vi.fn() })
    const go = vi.fn()
    mayLeave(go)
    release()
    expect(go).toHaveBeenCalledTimes(1)
  })
})
