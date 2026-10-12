import { act, renderHook, waitFor } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import type { AgentAuditEntry, AgentAuditFilter } from '../api/types'
import { useAuditJournal } from './useAuditJournal'

const entry = (id: number, agent = 'old'): AgentAuditEntry => ({ id, at: new Date(1).toISOString(), agent, method: 'list', phase: 'read', count: 1 })
const first = Array.from({ length: 200 }, (_, i) => entry(300 - i))
function deferred<T>() { let resolve!: (value: T) => void, reject!: (error: Error) => void; const promise = new Promise<T>((yes,no) => {resolve=yes;reject=no});return {promise,resolve,reject} }

describe('audit journal filter ownership', () => {
 it('clears old rows immediately when the filter changes, including failed new reads', async () => {
  const next = deferred<AgentAuditEntry[]>()
  const client = { listAgentAudit: vi.fn((filter: AgentAuditFilter) => filter.agent === 'new' ? next.promise : Promise.resolve([entry(1)])) }
  const {result,rerender} = renderHook(({agent}) => useAuditJournal(client,{agent},0),{initialProps:{agent:'old'}})
  await waitFor(() => expect(result.current.entries).toHaveLength(1))
  rerender({agent:'new'})
  expect(result.current.entries).toEqual([]);expect(result.current.loading).toBe(true)
  await waitFor(() => expect(client.listAgentAudit).toHaveBeenCalledTimes(2))
  await act(async () => next.reject(new Error('new filter unavailable')))
  expect(result.current.entries).toEqual([]);expect(result.current.error).toBe('new filter unavailable')
 })

 it.each(['success','failure'] as const)('ignores old pagination %s after a filter change', async outcome => {
  const old = deferred<AgentAuditEntry[]>()
  const client = {listAgentAudit:vi.fn((filter:AgentAuditFilter)=>filter.before?old.promise:Promise.resolve(filter.agent==='new'?[entry(500,'new')]:first))}
  const {result,rerender}=renderHook(({agent})=>useAuditJournal(client,{agent},0),{initialProps:{agent:'old'}})
  await waitFor(()=>expect(result.current.more).toBe(true))
  act(()=>{result.current.loadMore();result.current.loadMore()})
  expect(client.listAgentAudit.mock.calls.filter(([filter])=>filter.before)).toHaveLength(1)
  rerender({agent:'new'})
  await waitFor(()=>expect(result.current.entries[0]?.agent).toBe('new'))
  await act(async()=>{if(outcome==='success')old.resolve([entry(1)]);else old.reject(new Error('obsolete failure'))})
  expect(result.current.entries).toEqual([entry(500,'new')]);expect(result.current.error).toBeNull();expect(result.current.paging).toBe(false)
 })

 it('does not accept an old request after a filter changes away and back', async () => {
  const old=deferred<AgentAuditEntry[]>();let firstRead=true
  const client={listAgentAudit:vi.fn((filter:AgentAuditFilter)=>{if(filter.agent==='old'&&firstRead){firstRead=false;return old.promise}return Promise.resolve([entry(2,filter.agent)])})}
  const {result,rerender}=renderHook(({agent})=>useAuditJournal(client,{agent},0),{initialProps:{agent:'old'}})
  await waitFor(()=>expect(client.listAgentAudit).toHaveBeenCalledTimes(1))
  rerender({agent:'new'});await waitFor(()=>expect(result.current.entries[0]?.agent).toBe('new'))
  rerender({agent:'old'});await waitFor(()=>expect(result.current.entries[0]?.id).toBe(2))
  await act(async()=>old.resolve([entry(99)]));expect(result.current.entries[0]?.id).toBe(2)
 })
})
