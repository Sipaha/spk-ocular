import { beforeEach, describe, expect, it } from 'vitest'
import { dock, useDock } from './store'

const prod = { provider: 'kubernetes', id: 'prod' }
const dev = { provider: 'kubernetes', id: 'dev' }
const pod = (name: string) => ({ provider: 'kubernetes', target: 'prod', kind: 'pods', scope: 'web', name, uid: name })

beforeEach(() => useDock.setState({ tabs: [], active: null }))

describe('dock tabs', () => {
  it('tabs of several targets live side by side', () => {
    dock.openLogs(prod, 'prod', pod('a'))
    dock.openLogs(dev, 'dev', pod('a'))
    dock.openTerminal(prod, 'prod', { ref: pod('a') })
    dock.openTerminal(prod, 'prod', { ref: pod('a') })
    const { tabs, active } = useDock.getState()
    expect(tabs.map((t) => `${t.kind}:${t.target.id}`)).toEqual(['logs:prod', 'logs:dev', 'term:prod', 'term:prod'])
    expect(tabs[2].id).not.toBe(tabs[3].id) // two shells in one pod
    expect(active).toBe(tabs[3].id)
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
