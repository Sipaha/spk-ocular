import { afterEach, describe, expect, it, vi } from 'vitest'
import { LogWindowBridge, sameLogQuery } from './windowBridge'

describe('log window protocol',()=>{
 it('compares query fields across Wails JSON object key ordering',()=>{
  const owner={channel:'main',previous:false,follow:true,tailLines:500,sinceTime:undefined}
  const guest=JSON.parse('{"channel":"main","follow":true,"previous":false,"tailLines":500}')
  expect(sameLogQuery(owner,guest)).toBe(true)
  expect(sameLogQuery(owner,{...guest,tailLines:100})).toBe(false)
  expect(sameLogQuery(owner,{...guest,sinceTime:'2026-10-09T00:00:00Z'})).toBe(false)
 })
})


afterEach(()=>vi.restoreAllMocks())
it('closing an owned detached browser window closes its popup rather than the main page',async()=>{
 const popupClose=vi.fn(),mainClose=vi.spyOn(window,'close').mockImplementation(()=>{})
 vi.spyOn(window,'open').mockReturnValue({close:popupClose} as unknown as Window)
 const owner=new LogWindowBridge('owned-popup','owner')
 await owner.open('logs');await owner.close()
 expect(popupClose).toHaveBeenCalledTimes(1)
 expect(mainClose).not.toHaveBeenCalled()
 expect(owner.opened).toBe(false)
})
