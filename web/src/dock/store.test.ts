import { beforeEach, describe, expect, it } from 'vitest'
import { dock, useDock } from './store'

const prod = { provider: 'kubernetes', id: 'prod' }
const dev = { provider: 'kubernetes', id: 'dev' }
const pod = (name: string) => ({ provider: 'kubernetes', target: 'prod', kind: 'pods', scope: 'web', name, uid: name })

beforeEach(() => useDock.setState({ tabs: [], active: null }))

describe('dock tabs', () => {
  it('another target closes its log tabs and keeps terminals', () => {
    dock.openLogs(prod, 'prod', pod('a'))
    dock.openTerminal(prod, 'prod', { ref: pod('a') })
    dock.openTerminal(prod, 'prod', { ref: pod('a') })
    expect(useDock.getState().tabs.map((t) => t.kind)).toEqual(['logs', 'term', 'term'])
    dock.keepLogsOf(dev)
    const { tabs, active } = useDock.getState()
    expect(tabs.map((t) => t.kind)).toEqual(['term', 'term'])
    expect(tabs[0].id).not.toBe(tabs[1].id) // two shells in one pod
    expect(tabs.every((t) => t.target.id === 'prod')).toBe(true)
    expect(active).toBe(tabs[1].id)
  })

  it('one log tab per object; closing activates a neighbour', () => {
    dock.openLogs(prod, 'prod', pod('a'))
    dock.openLogs(prod, 'prod', pod('b'))
    dock.openLogs(prod, 'prod', pod('a'))
    const { tabs, active } = useDock.getState()
    expect(tabs).toHaveLength(2)
    expect(active).toBe(tabs[0].id)
    dock.close(tabs[0].id)
    expect(useDock.getState().active).toBe(tabs[1].id)
  })
})
