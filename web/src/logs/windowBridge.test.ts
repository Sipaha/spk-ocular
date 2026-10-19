import { afterEach, describe, expect, it, vi } from 'vitest'
import { EMPTY_WINDOW } from './buffer'
import { LogWindowBridge, sameLogQuery, type WindowMessage } from './windowBridge'

describe('log window protocol',()=>{
 it('compares query fields across Wails JSON object key ordering',()=>{
  const owner={channel:'main',previous:false,follow:true,tailLines:500,sinceTime:undefined}
  const guest=JSON.parse('{"channel":"main","follow":true,"previous":false,"tailLines":500}')
  expect(sameLogQuery(owner,guest)).toBe(true)
  expect(sameLogQuery(owner,{...guest,tailLines:100})).toBe(false)
  expect(sameLogQuery(owner,{...guest,filter:'ERROR'})).toBe(false)
  expect(sameLogQuery(owner,{...guest,filter:''})).toBe(true)
  expect(sameLogQuery(owner,{...guest,sinceTime:'2026-10-09T00:00:00Z'})).toBe(false)
 })
})


afterEach(()=>{vi.restoreAllMocks();vi.unstubAllGlobals()})
it('closing an owned detached browser window closes its popup rather than the main page',async()=>{
 const popupClose=vi.fn(),mainClose=vi.spyOn(window,'close').mockImplementation(()=>{})
 vi.spyOn(window,'open').mockReturnValue({close:popupClose} as unknown as Window)
 const owner=new LogWindowBridge('owned-popup','owner')
 await owner.open('logs');await owner.close()
 expect(popupClose).toHaveBeenCalledTimes(1)
 expect(mainClose).not.toHaveBeenCalled()
 expect(owner.opened).toBe(false)
})

it('tags forwarded frames with query identity without copying a snapshot per frame',async()=>{
 const messages:WindowMessage[]=[]
 vi.stubGlobal('BroadcastChannel',class{
  onmessage:((event:unknown)=>void)|null=null
  postMessage(message:WindowMessage){messages.push(message)}
  close(){}
 })
 const owner=new LogWindowBridge('query-tag','owner')
 const query={channel:'main',follow:true,tailLines:500,filter:'ERROR'}
 const capture=vi.fn(()=>({nextId:1,carry:[],win:EMPTY_WINDOW,sources:[],status:{phase:'live' as const},query}))
 await owner.start();owner.configureStream(capture,query);owner.connected=true
 owner.publish({k:'ready'})
 expect(messages[0].payload).toEqual({query,frame:{k:'ready'}})
 expect(capture).not.toHaveBeenCalled()
 owner.stop()
})
