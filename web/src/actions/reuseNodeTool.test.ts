import {describe,it,expect,vi} from 'vitest'
import type {Client} from '../api/client'
import {reuseNodeTool} from './reuseNodeTool'
const ref={provider:'kubernetes',target:'cluster',kind:'nodes',name:'worker',uid:'uid'}
const plan={reuse:true,expect:'signed',rights:{state:'allowed'},params:{text:'approved'},where:{configRev:'original'}}
describe('warm node admission',()=>{
 it('runs the exact warm plan without another review or custom image',async()=>{
  const result={files:{ref,preparedId:'owned'}},adopt=vi.fn()
  const client={prepareAction:vi.fn(async()=>plan),runAction:vi.fn(async()=>result)} as unknown as Client
  expect(await reuseNodeTool(client,ref,'node-files',()=>true,adopt)).toBe(true)
  expect(client.prepareAction).toHaveBeenCalledWith(ref,'node-files',{})
  expect(client.runAction).toHaveBeenCalledWith(plan);expect(adopt).toHaveBeenCalledWith(result)
 })
 it('never converts a stale warm run into a cold create',async()=>{
  const client={prepareAction:vi.fn(async()=>plan),runAction:vi.fn(async()=>{throw new Error('gone')})} as unknown as Client
  await expect(reuseNodeTool(client,ref,'node-shell',()=>true,vi.fn())).rejects.toThrow('gone')
  expect(client.prepareAction).toHaveBeenCalledOnce();expect(client.runAction).toHaveBeenCalledOnce()
 })
 it('closes a late capability instead of adopting it into a replacement workspace',async()=>{
  let current=true;const result={files:{ref,preparedId:'late'}}
  const client={prepareAction:vi.fn(async()=>plan),runAction:vi.fn(async()=>{current=false;return result}),files:vi.fn(async()=>({}))} as unknown as Client
  const adopt=vi.fn();await reuseNodeTool(client,ref,'node-files',()=>current,adopt)
  expect(adopt).not.toHaveBeenCalled();expect(client.files).toHaveBeenCalledWith(expect.objectContaining({preparedId:'late',command:'close'}))
 })
})
