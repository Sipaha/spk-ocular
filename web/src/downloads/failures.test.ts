import {describe,expect,it} from 'vitest'
import {DownloadFailureError,downloadFailureReason,safeDownloadFailure} from './failures'
describe('safe download failure reasons',()=>{
 it('uses typed causes and phases without parsing or retaining arbitrary messages',()=>{
  expect(downloadFailureReason(new DOMException('secret context','NotAllowedError'),'write')).toBe('permission')
  expect(downloadFailureReason(new DOMException('secret context','QuotaExceededError'),'write')).toBe('no_space')
  expect(downloadFailureReason({code:'forbidden',detail:'credential=DO-NOT-RETAIN'},'source_unavailable')).toBe('permission')
  expect(downloadFailureReason({code:'gone'},'write')).toBe('source_unavailable')
  expect(downloadFailureReason({transport:true,detail:'private URL'},'unknown')).toBe('connection')
  expect(downloadFailureReason(new DownloadFailureError('incomplete','private source details'),'write')).toBe('incomplete')
  expect(downloadFailureReason(new Error('disk full credential=DO-NOT-RETAIN'))).toBe('unknown')
  expect(safeDownloadFailure('credential=DO-NOT-RETAIN')).toBe('unknown')
 })
})
