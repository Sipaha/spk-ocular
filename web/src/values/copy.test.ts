import { beforeEach, describe, expect, it, vi } from 'vitest'
import { copyValue, resetCopies } from './copy'

beforeEach(resetCopies)

/** A read that answers when told to. */
function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

describe('copyValue', () => {
  it('writes what was read and says so only after the write succeeded', async () => {
    const writes: string[] = []
    const w = deferred<void>()
    const write = vi.fn(async (text: string) => {
      writes.push(text)
      await w.promise
    })
    const done = copyValue({ read: async () => 'A', valid: () => true, write })
    await vi.waitFor(() => expect(writes).toEqual(['A']))
    let settled = false
    void done.then(() => (settled = true))
    await Promise.resolve()
    expect(settled).toBe(false)
    w.resolve()
    expect(await done).toBe('copied')
  })

  it('Copy(A) then Copy(B), answers B then A: the clipboard ends with B', async () => {
    const clip: string[] = []
    const write = async (text: string) => void clip.push(text)
    const a = deferred<string>()
    const b = deferred<string>()
    const pa = copyValue({ read: () => a.promise, valid: () => true, write })
    const pb = copyValue({ read: () => b.promise, valid: () => true, write })
    b.resolve('B')
    expect(await pb).toBe('copied')
    a.resolve('A')
    expect(await pa).toBe('superseded')
    expect(clip).toEqual(['B'])
  })

  it('writes finishing out of order: the last copy is what stays', async () => {
    let clip = ''
    const slowA = deferred<void>()
    const write = vi.fn(async (text: string) => {
      if (text === 'A') await slowA.promise
      clip = text
    })
    const pa = copyValue({ read: async () => 'A', valid: () => true, write })
    await vi.waitFor(() => expect(write).toHaveBeenCalledTimes(1))
    const pb = copyValue({ read: async () => 'B', valid: () => true, write })
    // B waits for A's write: one in flight.
    await new Promise((r) => setTimeout(r, 10))
    expect(write).toHaveBeenCalledTimes(1)
    slowA.resolve()
    expect(await pa).toBe('superseded')
    expect(await pb).toBe('copied')
    expect(clip).toBe('B')
  })

  it('a queued write that is no longer the latest is skipped', async () => {
    const clip: string[] = []
    const slow = deferred<void>()
    const write = vi.fn(async (text: string) => {
      if (text === 'A') await slow.promise
      clip.push(text)
    })
    const pa = copyValue({ read: async () => 'A', valid: () => true, write })
    await vi.waitFor(() => expect(write).toHaveBeenCalledTimes(1))
    const pb = copyValue({ read: async () => 'B', valid: () => true, write })
    // B waits in the queue behind A's write when C comes.
    await new Promise((r) => setTimeout(r, 10))
    const pc = copyValue({ read: async () => 'C', valid: () => true, write })
    slow.resolve()
    expect(await Promise.all([pa, pb, pc])).toEqual(['superseded', 'superseded', 'copied'])
    expect(clip).toEqual(['A', 'C'])
  })

  it('an answer for a view that moved on is never written', async () => {
    const write = vi.fn(async () => {})
    let valid = true
    const r = deferred<string>()
    const p = copyValue({ read: () => r.promise, valid: () => valid, write })
    valid = false
    r.resolve('A')
    expect(await p).toBe('stale')
    expect(write).not.toHaveBeenCalled()
  })

  it('a view that moved on while its write waited is not written', async () => {
    const clip: string[] = []
    const slow = deferred<void>()
    const write = vi.fn(async (text: string) => {
      if (text === 'A') await slow.promise
      clip.push(text)
    })
    const pa = copyValue({ read: async () => 'A', valid: () => true, write })
    await vi.waitFor(() => expect(write).toHaveBeenCalledTimes(1))
    let valid = true
    const pb = copyValue({ read: async () => 'B', valid: () => valid, write })
    await new Promise((r) => setTimeout(r, 10))
    valid = false
    slow.resolve()
    expect(await pa).toBe('superseded')
    expect(await pb).toBe('stale')
    expect(clip).toEqual(['A'])
  })

  it('a failed write is an error, and the next copy still writes', async () => {
    const write = vi.fn(async (text: string) => {
      if (text === 'A') throw new Error('denied')
    })
    await expect(copyValue({ read: async () => 'A', valid: () => true, write })).rejects.toThrow('denied')
    expect(await copyValue({ read: async () => 'B', valid: () => true, write })).toBe('copied')
  })

  it('a failed read is an error unless superseded', async () => {
    const write = vi.fn(async () => {})
    await expect(copyValue({ read: async () => Promise.reject(new Error('gone')), valid: () => true, write })).rejects.toThrow('gone')
    const a = deferred<string>()
    const pa = copyValue({ read: () => a.promise, valid: () => true, write })
    const pb = copyValue({ read: async () => 'B', valid: () => true, write })
    a.reject(new Error('late'))
    expect(await pa).toBe('superseded')
    expect(await pb).toBe('copied')
  })
})
