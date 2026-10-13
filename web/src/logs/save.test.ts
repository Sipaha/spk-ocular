import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
const mocks=vi.hoisted(()=>({desktop:false,call:vi.fn()}))
vi.mock('../api/client',()=>({isDesktop:()=>mocks.desktop}))
vi.mock('@wailsio/runtime',()=>({Call:{ByName:mocks.call}}))
import {useDownloads} from '../downloads/store'
import { downloadLogs, logFileName, openLogDownload, plainLogDownload } from './save'

beforeEach(()=>{mocks.desktop=false;mocks.call.mockReset()})
afterEach(()=>vi.unstubAllGlobals())

describe('log downloads',()=>{
 it('records permission/quota failures without retaining sensitive exception text',async()=>{
  const writable={write:vi.fn(async()=>{throw new DOMException('credential=DO-NOT-RETAIN','QuotaExceededError')}),close:vi.fn(async()=>{}),abort:vi.fn(async()=>{})}
  vi.stubGlobal('showSaveFilePicker',vi.fn(async()=>({name:'quota.log',createWritable:async()=>writable})))
  await expect(downloadLogs('quota.log','x')).rejects.toThrow('DO-NOT-RETAIN')
  expect(useDownloads.getState().records[0]).toMatchObject({status:'failed',failureReason:'no_space'})
  expect(localStorage.getItem('ocular-download-history-v1')).not.toContain('DO-NOT-RETAIN')
 })
 it('removes ANSI colors, emphasis and OSC hyperlinks from downloads',()=>{
  expect(plainLogDownload('\x1b[1;31mERROR\x1b[0m привет\r\n\x1b]8;;https://example.test\x07link\x1b]8;;\x07')).toBe('ERROR привет\r\nlink')
  expect(plainLogDownload('\u009b32mgreen\u009b0m')).toBe('green')
  expect(plainLogDownload('literal [31m and JSON {"color":"red"}')).toBe('literal [31m and JSON {"color":"red"}')
 })
 it('chooses a browser destination and writes exact UTF-8 text',async()=>{
  const writable={write:vi.fn(async()=>{}),close:vi.fn(async()=>{}),abort:vi.fn(async()=>{})}
  const picker=vi.fn(async()=>({name:'chosen.log',createWritable:async()=>writable}))
  vi.stubGlobal('showSaveFilePicker',picker)
  expect(await downloadLogs('suggested.log','\x1b[32mпривет\x1b[0m\r\nplain\n')).toBe('chosen.log')
  expect(picker).toHaveBeenCalledWith(expect.objectContaining({suggestedName:'suggested.log'}))
  expect(writable.write).toHaveBeenCalledWith('привет\r\nplain\n');expect(writable.close).toHaveBeenCalledOnce();expect(writable.abort).not.toHaveBeenCalled()
 })
 it('does not write or report success when the picker is cancelled',async()=>{
  vi.stubGlobal('showSaveFilePicker',vi.fn(async()=>{throw new DOMException('cancelled','AbortError')}))
  expect(await downloadLogs('logs.log','x')).toBeNull()
 })
 it('aborts a failed browser write',async()=>{
  const writable={write:vi.fn(async()=>{throw new Error('disk full')}),close:vi.fn(async()=>{}),abort:vi.fn(async()=>{})}
  vi.stubGlobal('showSaveFilePicker',vi.fn(async()=>({name:'logs.log',createWritable:async()=>writable})))
  await expect(downloadLogs('logs.log','x')).rejects.toThrow('disk full');expect(writable.abort).toHaveBeenCalledOnce();expect(writable.close).not.toHaveBeenCalled();expect(useDownloads.getState().records[0]).toMatchObject({status:'failed',failureReason:'write'})
 })
 it('uses the requesting native window and preserves cancellation',async()=>{
  mocks.desktop=true;mocks.call.mockResolvedValue('')
  expect(await downloadLogs('logs.log','native text','log-window-id')).toBeNull()
  expect(mocks.call).toHaveBeenCalledWith('github.com/spk/spk-ocular/internal/desktop.LogWindows.ExportLogs','log-window-id','logs.log','Download logs','Download','native text')
 })
 it('streams browser chunks into a staged writable and commits only at completion',async()=>{
  const writable={write:vi.fn(async()=>{}),close:vi.fn(async()=>{}),abort:vi.fn(async()=>{})}
  vi.stubGlobal('showSaveFilePicker',vi.fn(async()=>({name:'history.log',createWritable:async()=>writable})))
  const writer=await openLogDownload('requested.log')
  await writer!.write('first\n');await writer!.write('second\n')
  expect(writable.close).not.toHaveBeenCalled();expect(writable.write.mock.calls).toEqual([['first\n'],['second\n']])
  expect(await writer!.close()).toBe('history.log');expect(writable.close).toHaveBeenCalledOnce()
 })
 it('streams native chunks through an opaque export id, with explicit abort',async()=>{
  mocks.desktop=true;mocks.call.mockResolvedValue('opaque-job')
  const writer=await openLogDownload('history.log','guest-window')
  await writer!.write('chunk');await writer!.abort()
  expect(mocks.call).toHaveBeenCalledWith('github.com/spk/spk-ocular/internal/desktop.LogWindows.BeginLogExport','guest-window','history.log','Download logs','Download')
  expect(mocks.call).toHaveBeenCalledWith('github.com/spk/spk-ocular/internal/desktop.LogWindows.AppendLogExport','opaque-job','chunk')
  expect(mocks.call).toHaveBeenCalledWith('github.com/spk/spk-ocular/internal/desktop.LogWindows.CancelLogExport','opaque-job')
 })
 it('suggests a filesystem-safe timestamped name',()=>{
  expect(logFileName('pod/name',new Date(2026,9,9,12,1,2))).toBe('pod_name_20261009_120102.log')
 })
})
