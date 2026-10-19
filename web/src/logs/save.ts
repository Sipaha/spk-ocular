import { isDesktop } from '../api/client'
import { stripAnsi } from './ansi'
import { t } from '../i18n'
import {DownloadFailureError,downloadFailureReason,type DownloadFailureReason} from '../downloads/failures'
import {beginBrowserDownload,updateBrowserDownload,watchDownloads} from '../downloads/store'

/** A suggested UTF-8 log filename: "<name>_<yyyymmdd_hhmmss>.log". */
export function logFileName(name: string, d = new Date()): string {
  const p = (n: number) => String(n).padStart(2, '0')
  const ts = `${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}_${p(d.getHours())}${p(d.getMinutes())}${p(d.getSeconds())}`
  return `${name.replace(/[^\w.-]+/g, '_').slice(0, 140) || 'logs'}_${ts}.log`
}

interface WritableLogFile { write: (text: string) => Promise<void>; close: () => Promise<void>; abort: () => Promise<void> }
interface SavePicker { showSaveFilePicker?: (options: { suggestedName: string; types: { description: string; accept: Record<string, string[]> }[] }) => Promise<{ name: string; createWritable: () => Promise<WritableLogFile> }> }

/** Remove terminal formatting without touching ordinary brackets or Unicode. */
export function plainLogDownload(text: string): string {
  if (!text.includes('\x1b') && !text.includes('\u009b') && !text.includes('\u009c') && !text.includes('\u009d')) return text
  const normalized = text.replaceAll('\u009b', '\x1b[').replaceAll('\u009d', '\x1b]').replaceAll('\u009c', '\x1b\\')
  return stripAnsi(normalized).plain
}

/** Native windows choose the destination through Wails. Browsers use their
 * save picker where available, with ordinary download-manager fallback. */
export async function downloadLogs(name: string, text: string, windowID = '', chooseLocation=false): Promise<string | null> {
  text = plainLogDownload(text)
  if (isDesktop()) {
    const { Call } = await import('@wailsio/runtime')
    const path = await Call.ByName('github.com/spk/spk-ocular/internal/desktop.LogWindows.ExportLogs', windowID, name, t('logs.downloadDialog'), t('logs.save'), text) as string
    return path || null
  }
  const writer=await openLogDownload(name,windowID,undefined,chooseLocation)
  if(!writer)return null
  try{await writer.write(text);return await writer.close()}
  catch(error){await writer.abort('failed',downloadFailureReason(error,'write')).catch(()=>{});throw error}
}

export interface LogDownloadWriter {
 write(text:string):Promise<void>
 close():Promise<string|null>
 abort(status?:'failed'|'cancelled',reason?:DownloadFailureReason):Promise<void>
}
/** Choose the destination before any asynchronous history read. Native and
 * File System Access writers stage chunks on disk, committing only on success. */
export async function openLogDownload(name:string,windowID='',onCancel?:()=>void,chooseLocation=false):Promise<LogDownloadWriter|null>{
 const unsubscribe=isDesktop()?()=>{}:watchDownloads()
 let stopped=false
 const stop=()=>{if(!stopped){stopped=true;unsubscribe()}}
 try {
  const writer=await createLogDownload(name,windowID,onCancel,chooseLocation)
  if(!writer){stop();return null}
  return {write:text=>writer.write(text),close:async()=>{try{return await writer.close()}finally{stop()}},abort:async(status,reason)=>{try{await writer.abort(status,reason)}finally{stop()}}}
 }catch(error){stop();throw error}
}
async function createLogDownload(name:string,windowID:string,onCancel:(()=>void)|undefined,chooseLocation:boolean):Promise<LogDownloadWriter|null>{
 if(isDesktop()){
  const {Call,Events}=await import('@wailsio/runtime')
  let exportID=''
  const cancelled=new Set<string>()
  const off=Events.On('ocular_log_export_cancelled',event=>{if(typeof event.data!=='string')return;if(exportID===event.data)onCancel?.();else if(cancelled.size<8)cancelled.add(event.data)})
  const prefix='github.com/spk/spk-ocular/internal/desktop.LogWindows.'
  let id:string
  try{id=await Call.ByName(prefix+'BeginLogExport',windowID,name,t('logs.downloadDialog'),t('logs.save'),chooseLocation) as string}catch(error){off();throw error}
  if(!id){off();return null}
  exportID=id
  if(cancelled.has(id))onCancel?.()
  return {write:async text=>{await Call.ByName(prefix+'AppendLogExport',id,text)},close:async()=>{try{return await Call.ByName(prefix+'CompleteLogExport',id) as string}finally{off()}},abort:async(status,reason)=>{try{if(status==='failed')await Call.ByName(prefix+'FailLogExportReason',id,reason??'unknown');else await Call.ByName(prefix+'CancelLogExport',id)}finally{off()}}}
 }
 const picker=window as Window&SavePicker
 if(chooseLocation&&picker.showSaveFilePicker){
  let id=''
  try{
   const handle=await picker.showSaveFilePicker({suggestedName:name,types:[{description:t('logs.downloadDialog'),accept:{'text/plain':['.log','.txt']}}]})
   id=beginBrowserDownload(handle.name,onCancel)
   const writable=await handle.createWritable()
   let bytes=0,closed=false
   return {
    write:async text=>{if(closed)throw new Error('download is closed');try{await writable.write(text)}catch(error){updateBrowserDownload(id,bytes,'failed',downloadFailureReason(error,'write'));throw error}bytes+=new TextEncoder().encode(text).length;updateBrowserDownload(id,bytes)},
    close:async()=>{if(closed)throw new Error('download is closed');try{await writable.close()}catch(error){updateBrowserDownload(id,bytes,'failed',downloadFailureReason(error,'write'));throw error}closed=true;updateBrowserDownload(id,bytes,'complete');return handle.name},
    abort:async(status='cancelled',reason)=>{if(closed)return;closed=true;updateBrowserDownload(id,bytes,status,reason);await writable.abort()},
   }
  }catch(error){if(id)updateBrowserDownload(id,0,error instanceof DOMException&&error.name==='AbortError'?'cancelled':'failed',downloadFailureReason(error,'write'));if(error instanceof DOMException&&error.name==='AbortError')return null;throw error}
 }
 // Browsers without a streaming save picker have a bounded Blob fallback.
 let chunks:string[]=[],bytes=0,closed=false
 const id=beginBrowserDownload(name,onCancel)
 return {
  write:async text=>{if(closed)throw new Error('download is closed');bytes+=new TextEncoder().encode(text).length;if(bytes>64<<20)throw new DownloadFailureError('limit',t('logs.historyBrowserLimit'));chunks.push(text);updateBrowserDownload(id,bytes)},
  close:async()=>{if(closed)throw new Error('download is closed');const url=URL.createObjectURL(new Blob(chunks,{type:'text/plain;charset=utf-8'}));const anchor=document.createElement('a');anchor.href=url;anchor.download=name;anchor.click();closed=true;chunks=[];updateBrowserDownload(id,bytes,'browser');setTimeout(()=>URL.revokeObjectURL(url),5000);return null},
  abort:async(status='cancelled',reason)=>{if(closed)return;closed=true;chunks=[];updateBrowserDownload(id,bytes,status,reason)},
 }
}
