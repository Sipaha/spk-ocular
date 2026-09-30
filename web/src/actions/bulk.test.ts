import { describe, expect, it } from 'vitest'
import type { ActionDescriptor } from '../api/types'
import { bulkActions, pool } from './bulk'

const del: ActionDescriptor = { id: 'delete', title: 'Delete', destructive: true }
const restart: ActionDescriptor = { id: 'restart', title: 'Restart' }
const scale = (min: number, max: number): ActionDescriptor => ({ id: 'scale', title: 'Scale', param: { kind: 'count', min, max } })

describe('bulkActions', () => {
  it('the actions every marked kind offers, in the first kind’s order', () => {
    expect(bulkActions([[restart, del], [del, restart]]).map((a) => a.id)).toEqual(['restart', 'delete'])
    expect(bulkActions([[restart, del], [del]]).map((a) => a.id)).toEqual(['delete'])
    expect(bulkActions([[restart], [del]])).toEqual([])
    expect(bulkActions([])).toEqual([])
  })

  it('never one that takes a text or a choice, is not for agents or goes one object at a time', () => {
    const debug: ActionDescriptor = { id: 'debug', title: 'Debug', param: { kind: 'choice', min: 0, max: 0 }, text: { title: { text: 'Image' }, max: 9 }, noAgents: true }
    const undo: ActionDescriptor = { id: 'undo', title: 'Roll back', param: { kind: 'choice', min: 0, max: 0 } }
    const exec: ActionDescriptor = { id: 'x', title: 'X', noAgents: true }
    const drain: ActionDescriptor = { id: 'drain', title: 'Drain', destructive: true, single: true }
    expect(bulkActions([[debug, undo, exec, drain, del]]).map((a) => a.id)).toEqual(['delete'])
  })

  it('a count shared by all: the range every kind accepts; a kind without it drops the action', () => {
    expect(bulkActions([[scale(0, 10)], [scale(1, 5)]])[0].param).toEqual({ kind: 'count', min: 1, max: 5 })
    expect(bulkActions([[scale(0, 3)], [scale(4, 9)]])).toEqual([]) // no count fits both
    expect(bulkActions([[scale(0, 3)], [{ id: 'scale', title: 'Scale' }]])).toEqual([])
  })

  it('destructive when any kind says so', () => {
    expect(bulkActions([[{ id: 'stop', title: 'Stop' }], [{ id: 'stop', title: 'Stop', destructive: true }]])[0].destructive).toBe(true)
  })
})

describe('pool', () => {
  it('runs at most limit at once, each item once, and stops starting new ones when asked', async () => {
    let running = 0
    let most = 0
    const started: number[] = []
    let stop = false
    const release: (() => void)[] = []
    const done = pool(
      [1, 2, 3, 4, 5, 6, 7],
      3,
      async (n) => {
        started.push(n)
        running++
        most = Math.max(most, running)
        await new Promise<void>((r) => release.push(r))
        running--
      },
      () => stop,
    )
    await Promise.resolve()
    expect(started).toEqual([1, 2, 3])
    release.shift()!()
    await new Promise((r) => setTimeout(r, 0))
    expect(started).toEqual([1, 2, 3, 4])
    stop = true
    while (release.length) {
      release.shift()!()
      await new Promise((r) => setTimeout(r, 0))
    }
    const left = await done
    expect(most).toBe(3)
    expect(started).toEqual([1, 2, 3, 4])
    expect(left).toEqual([5, 6, 7])
  })
})
