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
  it('reopening the active logs requests focus without duplicating a pinned tab',()=>{
    dock.openLogs(prod,'prod',pod('a'))
    const id=useDock.getState().active!
    dock.pin(id);dock.openLogs(prod,'prod',pod('a'))
    const tabs=useDock.getState().tabs
    expect(tabs).toHaveLength(1)
    expect(tabs[0]).toMatchObject({id,pinned:true,focusEpoch:2})
  })
})

it('closes tab groups by fresh order, preserves surviving active tabs and rejects stale anchors',()=>{
 for(const name of ['a','b','c'])dock.openLogs(prod,'prod',pod(name))
 dock.openTerminal(dev,'dev',{ref:pod('shell')})
 const ids=useDock.getState().tabs.map(tab=>tab.id)
 dock.activate(ids[0]);dock.closeGroup(ids[1],'right')
 expect(useDock.getState().tabs.map(tab=>tab.id)).toEqual(ids.slice(0,2))
 expect(useDock.getState().active).toBe(ids[0])
 dock.closeGroup(ids[1],'left')
 expect(useDock.getState().tabs.map(tab=>tab.id)).toEqual([ids[1]])
 expect(useDock.getState().active).toBe(ids[1])
 dock.closeGroup('stale','all')
 expect(useDock.getState().tabs).toHaveLength(1)
 dock.closeGroup(ids[1],'all')
 expect(useDock.getState()).toMatchObject({tabs:[],active:null})
})
it('chooses the nearest surviving tab when group closing removes the active tab',()=>{
 for(const name of ['a','b','c','d'])dock.openLogs(prod,'prod',pod(name))
 const ids=useDock.getState().tabs.map(tab=>tab.id)
 dock.activate(ids[3]);dock.closeGroup(ids[1],'right')
 expect(useDock.getState().active).toBe(ids[1])
 dock.activate(ids[0]);dock.closeGroup(ids[1],'left')
 expect(useDock.getState().active).toBe(ids[1])
})
