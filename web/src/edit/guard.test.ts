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

  it('two editors hold at once (a YAML edit and a value dialog): releasing one keeps the other', () => {
    let yamlDirty = false
    const yamlDiscard = vi.fn()
    holdEdits({ dirty: () => yamlDirty, discard: yamlDiscard })
    const valueFocus = vi.fn()
    const releaseValue = holdEdits({ dirty: () => false, discard: vi.fn(), focus: valueFocus })
    releaseValue()
    // The YAML editor, held before, still asks once it is changed.
    yamlDirty = true
    expect(editsHeld()).toBe(true)
    const go = vi.fn()
    mayLeave(go)
    expect(go).not.toHaveBeenCalled()
    discardEdits()
    expect(yamlDiscard).toHaveBeenCalledTimes(1)
    expect(go).toHaveBeenCalledTimes(1)
  })

  it('leaving discards every holder; keeping focuses the latest one with edits', () => {
    const a = { dirty: () => true, discard: vi.fn(), focus: vi.fn() }
    const b = { dirty: () => true, discard: vi.fn(), focus: vi.fn() }
    const c = { dirty: () => false, discard: vi.fn(), focus: vi.fn() }
    holdEdits(a)
    holdEdits(b)
    holdEdits(c)
    mayLeave(vi.fn())
    keepEditing()
    expect(b.focus).toHaveBeenCalledTimes(1)
    expect(a.focus).not.toHaveBeenCalled()
    expect(c.focus).not.toHaveBeenCalled()
    const go = vi.fn()
    mayLeave(go)
    discardEdits()
    expect([a.discard, b.discard, c.discard].map((f) => f.mock.calls.length)).toEqual([1, 1, 1])
    expect(go).toHaveBeenCalledTimes(1)
    expect(editsHeld()).toBe(false)
  })

  it('a request waits while another holder still has edits', () => {
    const releaseA = holdEdits({ dirty: () => true, discard: vi.fn() })
    const releaseB = holdEdits({ dirty: () => true, discard: vi.fn() })
    const go = vi.fn()
    mayLeave(go)
    releaseB()
    expect(go).not.toHaveBeenCalled()
    releaseA()
    expect(go).toHaveBeenCalledTimes(1)
  })
})
