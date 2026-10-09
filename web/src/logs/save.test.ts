import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
const mocks=vi.hoisted(()=>({desktop:false,call:vi.fn()}))
vi.mock('../api/client',()=>({isDesktop:()=>mocks.desktop}))
vi.mock('@wailsio/runtime',()=>({Call:{ByName:mocks.call}}))
import { downloadLogs, logFileName, plainLogDownload } from './save'

beforeEach(()=>{mocks.desktop=false;mocks.call.mockReset()})
afterEach(()=>vi.unstubAllGlobals())

describe('log downloads',()=>{
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
  await expect(downloadLogs('logs.log','x')).rejects.toThrow('disk full');expect(writable.abort).toHaveBeenCalledOnce();expect(writable.close).not.toHaveBeenCalled()
 })
 it('uses the requesting native window and preserves cancellation',async()=>{
  mocks.desktop=true;mocks.call.mockResolvedValue('')
  expect(await downloadLogs('logs.log','native text','log-window-id')).toBeNull()
  expect(mocks.call).toHaveBeenCalledWith('github.com/spk/spk-ocular/internal/desktop.LogWindows.ExportLogs','log-window-id','logs.log','Download logs','Download','native text')
 })
 it('suggests a filesystem-safe timestamped name',()=>{
  expect(logFileName('pod/name',new Date(2026,9,9,12,1,2))).toBe('pod_name_20261009_120102.log')
 })
})
