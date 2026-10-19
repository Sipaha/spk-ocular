import {DownloadFailureError,downloadFailureReason,downloadClassReason,type DownloadFailureReason} from '../downloads/failures'
import {asMessage,type Client} from '../api/client'
import type {Ref} from '../api/types'
import {classLabel,messageText,t} from '../i18n'
import {NdjsonDecoder,LINE_CUT,LINE_NO_TIME,type Frame} from './ndjson'
import {logFileName,openLogDownload,plainLogDownload} from './save'
import {refTitle} from '../refs'
import {rowText,type RowOpts} from './row'

export interface LogDownloadFormat extends Pick<RowOpts,'showTime'|'showSource'>{
 labelOf:(key:string,fallback:string)=>string
}

const MAX_BYTES=1<<30,MAX_LINES=10_000_000
const utf8=new TextEncoder()
interface Progress{lines:number;bytes:number}
/** Export finite retained source history, never the virtual viewport or UI
 * buffer. Every destination is aborted unless a valid complete end arrives. */
export async function downloadLogHistory(client:Client,subject:Ref,channel:string,previous:boolean,format:LogDownloadFormat,signal:AbortSignal,windowID='',progress?:(value:Progress)=>void,onCancel?:()=>void,chooseLocation=false):Promise<(Progress&{path:string|null})|null>{
 const writer=await openLogDownload(logFileName(refTitle(subject)),windowID,onCancel,chooseLocation)
 if(!writer)return null
 let reader:ReadableStreamDefaultReader<Uint8Array>|undefined
 const abort=()=>{void reader?.cancel().catch(()=>{});void writer.abort().catch(()=>{})}
 signal.addEventListener('abort',abort,{once:true})
 const check=()=>{if(signal.aborted)throw new DOMException('download cancelled','AbortError')}
 let lines=0,bytes=0,ended=false,lastProgress=0
 let failurePhase:DownloadFailureReason='source_unavailable'
 const sources=new Map<number,{key:string;label:string}>()
 const decoder=new NdjsonDecoder()
 const incomplete=()=>new DownloadFailureError('incomplete',t('logs.historyIncomplete'))
 try{
  check()
  const opened=await client.openLogHistory(subject,channel,previous,signal)
  check()
  const base=await client.streamBase()
  check()
  failurePhase='connection'
  const response=await fetch(base+'/logs/'+encodeURIComponent(opened.streamId),{signal})
  if(!response.ok||!response.body)throw new DownloadFailureError(response.status===401||response.status===403?'permission':response.status===404?'source_unavailable':'connection',t('logs.historyIncomplete')+` (HTTP ${response.status})`)
  reader=response.body.getReader()
  const frame=async(value:Frame)=>{
   check()
   if(ended){if(value.k==='ping')return;throw incomplete()}
   switch(value.k){
    case 'source':{
     if(!Number.isSafeInteger(value.id)||value.id<1||typeof value.key!=='string'||typeof value.label!=='string'||value.key.length>4096||value.label.length>4096)throw incomplete()
     const old=sources.get(value.id)
     if(old&&old.key!==value.key)throw incomplete()
     if(!old&&sources.size>=1000)throw new DownloadFailureError('limit',t('logs.historyLimit'))
     sources.set(value.id,{key:value.key,label:plainLogDownload(value.label).replace(/[\r\n]/g,' ')})
     break
    }
    case 'lines':{
     const source=sources.get(value.s)
     if(!source||!Array.isArray(value.l))throw incomplete()
     const label=plainLogDownload(format.labelOf(source.key,source.label)).replace(/[\r\n]/g,' ')
     const rowOpts={showTime:format.showTime,showSource:format.showSource,labelOf:()=>label}
     let text=''
     for(const line of value.l){
      if(!Array.isArray(line)||line.length<2||line.length>3||typeof line[0]!=='string'||typeof line[1]!=='string'||line[2]!==undefined&&(!Number.isSafeInteger(line[2])||line[2]<0||line[2]&~(LINE_CUT|LINE_NO_TIME)||line[2]&LINE_CUT))throw incomplete()
      if(line[0]&&(!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/.test(line[0])||Number.isNaN(Date.parse(line[0]))))throw incomplete()
      const entry=rowText({ts:line[0],src:value.s,plain:plainLogDownload(line[1]),flags:0},rowOpts)+'\n'
      bytes+=utf8.encode(entry).length;lines++
      if(bytes>MAX_BYTES||lines>MAX_LINES)throw new DownloadFailureError('limit',t('logs.historyLimit'))
      text+=entry
      if(text.length>=64<<10){check();failurePhase='write';await writer.write(text);failurePhase='incomplete';text=''}
     }
     if(text){check();failurePhase='write';await writer.write(text);failurePhase='incomplete'}
     const now=Date.now();if(now-lastProgress>200){lastProgress=now;progress?.({lines,bytes})}
     break
    }
    case 'state':{
     if(!['streaming','ended','gap','truncated','limited','error','waiting'].includes(value.state))throw incomplete()
     if(['gap','truncated','limited','error','waiting'].includes(value.state)||value.state==='ended'&&(value.class||value.msg))throw new DownloadFailureError(downloadClassReason(value.class,'incomplete'),t('logs.historyIncomplete')+(value.msg?': '+value.msg:''))
     break
    }
    case 'end':{
     if(value.reason!=='done'){
      if(value.class==='limit')throw new DownloadFailureError('limit',t('logs.historyLimit'))
      const message=asMessage(value.why)
      if(message?.key==='logs.historyIncomplete'||message?.key==='logs.historyLimit')throw new DownloadFailureError(message.key==='logs.historyLimit'?'limit':'incomplete',messageText(message))
      throw new DownloadFailureError(downloadClassReason(value.class,value.reason==='gone'?'source_unavailable':'incomplete'),t('logs.historyIncomplete')+': '+(message?messageText(message):value.message??classLabel(value.class??'internal')))
     }
     if(!sources.size)throw incomplete()
     ended=true;break
    }
    case 'ping':case 'ready':break
    default:throw incomplete()
   }
  }
  for(;;){check();failurePhase='connection';const next=await reader.read();failurePhase='incomplete';check();if(next.done)break;for(const value of decoder.push(next.value))await frame(value)}
  for(const value of decoder.end())await frame(value)
  if(!ended)throw incomplete()
  check();progress?.({lines,bytes})
  failurePhase='write'
  const path=await writer.close()
  return {path,lines,bytes}
 }catch(error){await writer.abort(error instanceof DOMException&&error.name==='AbortError'?'cancelled':'failed',downloadFailureReason(error,failurePhase)).catch(()=>{});throw error}
 finally{signal.removeEventListener('abort',abort);await reader?.cancel().catch(()=>{});reader?.releaseLock()}
}
