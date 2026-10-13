export const downloadFailureReasons=['unknown','permission','no_space','destination_exists','destination_unavailable','source_unavailable','connection','timeout','incomplete','limit','unsafe_archive','invalid_archive','write'] as const
export type DownloadFailureReason=typeof downloadFailureReasons[number]
export function safeDownloadFailure(value:unknown):DownloadFailureReason {
 return typeof value==='string'&&(downloadFailureReasons as readonly string[]).includes(value)?value as DownloadFailureReason:'unknown'
}
/** The exception stays with its caller. Only an allowlisted reason enters history. */
export class DownloadFailureError extends Error {
 constructor(public readonly reason:DownloadFailureReason,message:string){super(message)}
}
export function downloadClassReason(code:unknown,fallback:DownloadFailureReason):DownloadFailureReason {
 switch(code){
  case 'forbidden':case 'unauthorized':return 'permission'
  case 'not_found':case 'gone':case 'conflict':return 'source_unavailable'
  case 'timeout':return 'timeout'
  case 'limit':return 'limit'
  case 'unavailable':case 'disconnected':return 'connection'
  default:return fallback
 }
}
export function downloadFailureReason(error:unknown,fallback:DownloadFailureReason='unknown'):DownloadFailureReason {
 if(error instanceof DownloadFailureError)return safeDownloadFailure(error.reason)
 if(error instanceof DOMException){
  if(error.name==='NotAllowedError'||error.name==='SecurityError'||error.name==='NoModificationAllowedError')return 'permission'
  if(error.name==='QuotaExceededError')return 'no_space'
  if(error.name==='NotFoundError')return 'destination_unavailable'
  if(error.name==='NetworkError')return 'connection'
  if(error.name==='TimeoutError')return 'timeout'
 }
 if(error&&typeof error==='object'){
  const coded=error as {code?:unknown;transport?:unknown;why?:{key?:unknown}}
  if(coded.why?.key==='logs.historyLimit'||coded.why?.key==='logs.historyBrowserLimit')return 'limit'
  if(coded.why?.key==='logs.historyIncomplete')return 'incomplete'
  return downloadClassReason(coded.code,coded.transport===true?'connection':fallback)
 }
 return fallback
}
