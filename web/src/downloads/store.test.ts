import {beforeEach,describe,expect,it,vi} from 'vitest'
vi.mock('../api/client',()=>({isDesktop:()=>false}))
import {beginBrowserDownload,dismissDownload,isDownloadUnseen,markDownloadsViewed,clearDownloadHistory,updateBrowserDownload,useDownloads,watchDownloads} from './store'

beforeEach(()=>{localStorage.clear();useDownloads.setState({records:[],storageError:false,error:false,viewed:[]})})
describe('browser download metadata',()=>{
 it('retains only a safe first failure reason and removes unrecognized properties from restored history',()=>{
  const id=beginBrowserDownload('failed.log');updateBrowserDownload(id,12,'failed','permission');updateBrowserDownload(id,99,'failed','write')
  expect(useDownloads.getState().records.find(r=>r.id===id)).toMatchObject({failureReason:'permission',bytes:12})
  localStorage.setItem('ocular-download-history-v1',JSON.stringify([{id:'untrusted-reason',name:'failed.log',kind:'logs',status:'failed',bytes:0,startedAt:1,failureReason:'credential=DO-NOT-RETAIN',rawError:'credential=DO-NOT-RETAIN'}]))
  useDownloads.setState({records:[]});const stop=watchDownloads()
  expect(useDownloads.getState().records[0]).toMatchObject({failureReason:'unknown'})
  expect(JSON.stringify(useDownloads.getState().records)).not.toContain('DO-NOT-RETAIN')
  const done=beginBrowserDownload('next.log');updateBrowserDownload(done,12,'complete')
  expect(localStorage.getItem('ocular-download-history-v1')).not.toContain('DO-NOT-RETAIN');stop()
 })
 it('persists seen IDs, ignores unsuccessful/active jobs and bounds acknowledgement metadata',()=>{
  const active=beginBrowserDownload('active.log');markDownloadsViewed(useDownloads.getState().records)
  expect(useDownloads.getState().viewed).not.toContain(active)
  updateBrowserDownload(active,12,'complete');expect(isDownloadUnseen(useDownloads.getState().records[0],[])).toBe(true)
  markDownloadsViewed(useDownloads.getState().records);expect(useDownloads.getState().viewed).toContain(active)
  useDownloads.setState({viewed:[]});const stop=watchDownloads();expect(useDownloads.getState().viewed).toContain(active);stop()
  markDownloadsViewed(Array.from({length:250},(_,i)=>({id:`seen-${i}`,name:'log',kind:'logs' as const,status:'complete' as const,bytes:1,startedAt:1})))
  expect(JSON.parse(localStorage.getItem('ocular-downloads-viewed-v1')!)).toHaveLength(200)
 })
 it('dismisses one terminal record without resurrecting it on a later persist or remount',async()=>{
  const active=beginBrowserDownload('pending.log');await expect(dismissDownload(active)).rejects.toThrow()
  const id=beginBrowserDownload('dismissed.log');updateBrowserDownload(id,12,'complete');await dismissDownload(id)
  updateBrowserDownload(active,30,'complete')
  const stop=watchDownloads();expect(useDownloads.getState().records.some(r=>r.id===id)).toBe(false);stop()
  expect(JSON.parse(localStorage.getItem('ocular-download-history-v1')!).some((r:{id:string})=>r.id===id)).toBe(false)
 })
 it('retains completed and failed metadata across remounts without inventing paths',async()=>{
  const id=beginBrowserDownload('chosen.log');updateBrowserDownload(id,45,'complete')
  const failed=beginBrowserDownload('failed.log');updateBrowserDownload(failed,12,'failed')
  useDownloads.setState({records:[]});const stop=watchDownloads()
  expect(useDownloads.getState().records).toEqual(expect.arrayContaining([expect.objectContaining({id,status:'complete',bytes:45}),expect.objectContaining({id:failed,status:'failed',bytes:12})]))
  expect(useDownloads.getState().records.every(r=>r.path===undefined)).toBe(true)
  stop();await clearDownloadHistory();expect(useDownloads.getState().records).toEqual([])
 })
 it('does not turn cancellation or browser handoff into a completed local file',()=>{
  const id=beginBrowserDownload('cancelled.log');updateBrowserDownload(id,23,'cancelled');updateBrowserDownload(id,99,'complete')
  expect(useDownloads.getState().records[0]).toMatchObject({status:'cancelled',bytes:23})
  const browser=beginBrowserDownload('handed-off.log');updateBrowserDownload(browser,42,'browser')
  expect(useDownloads.getState().records.find(r=>r.id===browser)).toMatchObject({status:'browser',path:undefined})
 })
 it('keeps active transfers when history is cleared and bounds persisted terminal metadata',async()=>{
  const id=beginBrowserDownload('active.log')
  for(let i=0;i<220;i++){const job=beginBrowserDownload(`saved-${i}.log`);updateBrowserDownload(job,i,'complete')}
  const saved=JSON.parse(localStorage.getItem('ocular-download-history-v1')!)
  expect(saved).toHaveLength(200);expect(saved.every((r:{status:string})=>r.status==='complete')).toBe(true)
  await clearDownloadHistory();expect(useDownloads.getState().records).toEqual([expect.objectContaining({id,status:'active'})])
 })
 it('marks an owning browser window’s unfinished transfer interrupted before unloading',()=>{
  const stop=watchDownloads(),id=beginBrowserDownload('pending.log')
  window.dispatchEvent(new Event('beforeunload'))
  expect(useDownloads.getState().records.find(r=>r.id===id)).toMatchObject({status:'interrupted',path:undefined})
  expect(JSON.parse(localStorage.getItem('ocular-download-history-v1')!)).toEqual([expect.objectContaining({id,status:'interrupted'})])
  stop()
 })
})
