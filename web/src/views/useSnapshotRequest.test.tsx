import { act, renderHook, waitFor } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'
import { ViewConnectionContext } from './useView'
import { useSnapshotRequest } from './useSnapshotRequest'
const deferred = <T,>() => {let resolve!:(value:T)=>void;let reject!:(reason:unknown)=>void;const promise=new Promise<T>((yes,no)=>{resolve=yes;reject=no});return{promise,resolve,reject}}
it('retains results during refresh, cancels once and rejects late completion until explicit retry', async () => {
 const second=deferred<string>(),third=deferred<string>();let signal:AbortSignal|undefined
 const load=vi.fn().mockResolvedValueOnce('initial').mockImplementationOnce((s:AbortSignal)=>{signal=s;return second.promise}).mockImplementationOnce(()=>third.promise)
 const {result}=renderHook(()=>useSnapshotRequest<string>(load,'target/scope'))
 await waitFor(()=>expect(result.current.value).toBe('initial'))
 act(()=>{result.current.refresh();result.current.refresh()})
 await waitFor(()=>expect(load).toHaveBeenCalledTimes(2));expect(result.current.value).toBe('initial');expect(result.current.busy).toBe(true)
 act(()=>result.current.cancel());expect(signal?.aborted).toBe(true);expect(result.current.cancelled).toBe(true)
 await act(async()=>second.resolve('late'));expect(result.current.value).toBe('initial');expect(result.current.cancelled).toBe(true)
 act(()=>result.current.refresh());await waitFor(()=>expect(load).toHaveBeenCalledTimes(3))
 await act(async()=>third.resolve('fresh'));expect(result.current.value).toBe('fresh');expect(result.current.cancelled).toBe(false)
})
it('drops values on scope change and fences an older request after a newer result', async () => {
 const old=deferred<string>();const load=vi.fn().mockImplementationOnce(()=>old.promise).mockResolvedValueOnce('new scope')
 const {result,rerender}=renderHook(({scope})=>useSnapshotRequest<string>(load,scope),{initialProps:{scope:'a'}})
 await waitFor(()=>expect(load).toHaveBeenCalledTimes(1));rerender({scope:'b'})
 expect(result.current.value).toBeNull();await waitFor(()=>expect(result.current.value).toBe('new scope'))
 await act(async()=>old.resolve('old scope'));expect(result.current.value).toBe('new scope')
})
it('reports errors without erasing retained results, aborts on unmount and makes no disabled read', async () => {
 const pending=deferred<string>();let signal:AbortSignal|undefined
 const load=vi.fn().mockResolvedValueOnce('initial').mockRejectedValueOnce(new Error('denied')).mockImplementationOnce((s:AbortSignal)=>{signal=s;return pending.promise})
 const {result,unmount}=renderHook(()=>useSnapshotRequest<string>(load,'key'))
 await waitFor(()=>expect(result.current.value).toBe('initial'));act(()=>result.current.refresh());await waitFor(()=>expect(result.current.error).toBeInstanceOf(Error));expect(result.current.value).toBe('initial')
 act(()=>result.current.refresh());await waitFor(()=>expect(load).toHaveBeenCalledTimes(3));unmount();expect(signal?.aborted).toBe(true)
 const disabled=vi.fn();renderHook(()=>useSnapshotRequest(disabled,'key',{enabled:false}));expect(disabled).not.toHaveBeenCalled()
})

it('drops results when the client loader changes and reconnects only connection-scoped reads', async () => {
 let connection='one'
 const wrapper=({children}:{children:ReactNode})=><ViewConnectionContext value={connection}>{children}</ViewConnectionContext>
 const next=deferred<string>(),load=vi.fn().mockResolvedValueOnce('session one').mockImplementationOnce(()=>next.promise)
 const {result,rerender}=renderHook(()=>useSnapshotRequest<string>(load,'same target'),{wrapper})
 await waitFor(()=>expect(result.current.value).toBe('session one'))
 connection='two';rerender();expect(result.current.value).toBeNull();await waitFor(()=>expect(load).toHaveBeenCalledTimes(2))
 await act(async()=>next.resolve('session two'));expect(result.current.value).toBe('session two')
 const globalLoad=vi.fn().mockResolvedValue('settings')
 const global=renderHook(()=>useSnapshotRequest(globalLoad,'settings',{connectionScoped:false}),{wrapper})
 await waitFor(()=>expect(global.result.current.value).toBe('settings'));connection='three';global.rerender();expect(globalLoad).toHaveBeenCalledTimes(1)
 const late=deferred<string>(),other=vi.fn(()=>late.promise)
 const firstClient=vi.fn().mockResolvedValue('client one')
 const swap=renderHook(({loader})=>useSnapshotRequest<string>(loader,'same'),{initialProps:{loader:firstClient as (s:AbortSignal)=>Promise<string>}})
 await waitFor(()=>expect(swap.result.current.value).toBe('client one'));swap.rerender({loader:other});expect(swap.result.current.value).toBeNull()
 await act(async()=>late.resolve('other client'));expect(swap.result.current.value).toBe('other client')
})
