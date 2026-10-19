import {create} from 'zustand'
import {safeDownloadFailure,type DownloadFailureReason} from './failures'
import {isDesktop} from '../api/client'

export type DownloadStatus = 'active'|'complete'|'failed'|'cancelled'|'interrupted'|'browser'
export interface DownloadRecord {id:string;name:string;path?:string;directory?:boolean;failureReason?:DownloadFailureReason;kind:'logs'|'files';status:DownloadStatus;bytes:number;startedAt:number;endedAt?:number;owner?:string}
interface Snapshot {records:DownloadRecord[];storageError:boolean}
const viewedKey='ocular-downloads-viewed-v1'
function readViewed():string[] {
 try {
  const value:unknown=JSON.parse(localStorage.getItem(viewedKey)??'[]')
  return Array.isArray(value)?value.filter((id):id is string=>typeof id==='string'&&id.length>0&&id.length<=100).slice(0,200):[]
 }catch{return []}
}
interface State extends Snapshot {error:boolean;viewed:string[]}
export const useDownloads=create<State>(()=>({records:[],storageError:false,error:false,viewed:readViewed()}))
const service='github.com/spk/spk-ocular/internal/desktop.Downloads.'
const storageKey='ocular-download-history-v1'
const removedKey='ocular-downloads-removed-v1'
const removed=new Set<string>()
function rememberRemoved(ids:unknown[]) {
 for(const id of ids.slice(0,200))if(typeof id==='string'&&id.length>0&&id.length<=100)removed.add(id)
 while(removed.size>200)removed.delete(removed.values().next().value!)
}
function readRemoved() {
 try{const ids:unknown=JSON.parse(localStorage.getItem(removedKey)??'[]');if(Array.isArray(ids))rememberRemoved(ids)}catch{/* Keep the current in-memory tombstones. */}
}
const limit=200
const browserOwner=crypto.randomUUID()
let generation=0,users=0,off:(()=>void)|null=null
let viewedChannel:BroadcastChannel|null=null
let stopViewed:(()=>void)|null=null
let channel:BroadcastChannel|null=null
let timer:ReturnType<typeof setInterval>|null=null
let pulling=false,again=false
let mutationEpoch=0
const lastPublish=new Map<string,number>()
const cancellations=new Map<string,()=>void>()

export function isDownloadUnseen(record:DownloadRecord,viewed:readonly string[]) {
 return (record.status==='complete'||record.status==='browser')&&!viewed.includes(record.id)
}

/** Viewing the list acknowledges only successful results already displayed.
 * Active jobs remain unacknowledged until their result appears in an open panel. */
export function markDownloadsViewed(records:readonly DownloadRecord[]) {
 const state=useDownloads.getState()
 const ids=records.filter(r=>isDownloadUnseen(r,state.viewed)).map(r=>r.id)
 if(!ids.length)return
 const viewed=[...new Set([...ids,...state.viewed,...readViewed()])].slice(0,200)
 useDownloads.setState({viewed})
 try{localStorage.setItem(viewedKey,JSON.stringify(viewed))}catch{/* The in-memory acknowledgement still works when storage is unavailable. */}
 viewedChannel?.postMessage(viewed)
}

function cleanBrowserRecord(r:DownloadRecord):DownloadRecord {
 return {id:r.id,name:r.name,path:undefined,kind:'logs',status:r.status,bytes:r.bytes,startedAt:r.startedAt,endedAt:r.endedAt,owner:r.owner,failureReason:r.status==='failed'?safeDownloadFailure(r.failureReason):undefined}
}
function validRecord(v:unknown):v is DownloadRecord {
 if(!v||typeof v!=='object')return false
 const r=v as DownloadRecord
 return typeof r.id==='string'&&r.id.length>0&&r.id.length<=100&&typeof r.name==='string'&&r.name.length<=32768&&r.kind==='logs'&&['active','complete','failed','cancelled','interrupted','browser'].includes(r.status)&&Number.isSafeInteger(r.bytes)&&r.bytes>=0&&Number.isSafeInteger(r.startedAt)&&r.startedAt>=0&&(r.endedAt===undefined||Number.isSafeInteger(r.endedAt)&&r.endedAt>=0)&&(r.owner===undefined||typeof r.owner==='string'&&r.owner.length<=100)
}

async function call<T>(method:string,...args:unknown[]):Promise<T> {
 const {Call}=await import('@wailsio/runtime')
 return await Call.ByName(service+method,...args) as T
}

export async function reloadDownloads() {
 if(!isDesktop())return
 if(pulling){again=true;return}
 pulling=true
 const owner=generation
 const epoch=mutationEpoch
 try {
  const snapshot=await call<Snapshot>('List')
  if(owner===generation&&epoch===mutationEpoch)useDownloads.setState({...snapshot,error:false})
 }catch{if(owner===generation&&epoch===mutationEpoch)useDownloads.setState({error:true})}
 finally{pulling=false;if(again){again=false;if(owner===generation&&users>0)void reloadDownloads()}}
}

function browserHistory():DownloadRecord[] {
 try {
  const value:unknown=JSON.parse(localStorage.getItem(storageKey)??'[]')
  if(!Array.isArray(value)||value.length>limit)throw new Error('invalid download history')
  readRemoved()
  return value.filter(validRecord).filter(r=>r.status!=='active'&&!removed.has(r.id)).map(cleanBrowserRecord)
 }catch{useDownloads.setState({storageError:true});return []}
}

function trim(records:DownloadRecord[]) {
 return records.sort((a,b)=>b.startedAt-a.startedAt).filter((r,i)=>r.status==='active'||i<limit)
}

function persistBrowser() {
 const saved=new Map(browserHistory().map(r=>[r.id,r]))
 for(const r of useDownloads.getState().records)if(r.status!=='active'&&!removed.has(r.id))saved.set(r.id,r)
 const records=trim([...saved.values()]).slice(0,limit)
 try{localStorage.setItem(storageKey,JSON.stringify(records));useDownloads.setState({storageError:false})}catch{useDownloads.setState({storageError:true})}
}

function mergeBrowser(records:DownloadRecord[]) {
 readRemoved()
 const saved=new Map(useDownloads.getState().records.filter(r=>!removed.has(r.id)).map(r=>[r.id,r]))
 const added=new Map<string,DownloadRecord>()
 for(const r of records.filter(validRecord).filter(r=>!removed.has(r.id)).slice(0,limit))if(!saved.get(r.id)?.endedAt||r.endedAt){if(!saved.has(r.id))added.set(r.id,cleanBrowserRecord(r));else saved.set(r.id,cleanBrowserRecord(r))}
 useDownloads.setState({records:trim([...added.values(),...saved.values()])})
}

/** App-level metadata is shared with detached log windows; no file contents or
 * browser-invented absolute paths travel through storage or messages. */
export function watchDownloads() {
 users++
 if(users===1){
  const owner=++generation
  useDownloads.setState({viewed:readViewed()})
  const receiveViewed=(ids:unknown)=>{
   if(!Array.isArray(ids))return
   const safe=ids.filter((id):id is string=>typeof id==='string'&&id.length>0&&id.length<=100).slice(0,200)
   useDownloads.setState({viewed:[...new Set([...safe,...useDownloads.getState().viewed])].slice(0,200)})
  }
  const storedViewed=(event:StorageEvent)=>{if(event.key===viewedKey)receiveViewed(readViewed())}
  window.addEventListener('storage',storedViewed)
  if(typeof BroadcastChannel!=='undefined'){
   viewedChannel=new BroadcastChannel(viewedKey)
   viewedChannel.onmessage=event=>receiveViewed(event.data)
  }
  stopViewed=()=>{window.removeEventListener('storage',storedViewed);viewedChannel?.close();viewedChannel=null}
  if(isDesktop()) {
   void import('@wailsio/runtime').then(({Events})=>{if(owner===generation&&users>0){off=Events.On('ocular_downloads_changed',()=>{void reloadDownloads()});void reloadDownloads()}}).catch(()=>useDownloads.setState({error:true}))
   timer=setInterval(()=>{void reloadDownloads()},2000)
  }else {
   mergeBrowser(browserHistory())
   if(typeof BroadcastChannel!=='undefined') {
    channel=new BroadcastChannel(storageKey)
    channel.onmessage=event=>{
     if(event.data?.type==='request')channel?.postMessage({type:'snapshot',records:useDownloads.getState().records})
     else if(event.data?.type==='snapshot'&&Array.isArray(event.data.records))mergeBrowser(event.data.records)
     else if(event.data?.type==='remove'&&typeof event.data.id==='string'){rememberRemoved([event.data.id]);useDownloads.setState({records:useDownloads.getState().records.filter(r=>r.id!==event.data.id)})}
     else if(event.data?.type==='cancel'&&typeof event.data.id==='string')cancellations.get(event.data.id)?.()
     else if(event.data?.type==='clear')useDownloads.setState({records:useDownloads.getState().records.filter(r=>r.status==='active')})
    }
    channel.postMessage({type:'request'})
   }
   const stored=(event:StorageEvent)=>{if(event.key===storageKey){const active=useDownloads.getState().records.filter(r=>r.status==='active');useDownloads.setState({records:trim([...active,...browserHistory()])})}}
   const leaving=()=>{for(const r of useDownloads.getState().records)if(r.status==='active'&&r.owner===browserOwner)updateBrowserDownload(r.id,r.bytes,'interrupted')}
   window.addEventListener('storage',stored);window.addEventListener('beforeunload',leaving)
   off=()=>{window.removeEventListener('storage',stored);window.removeEventListener('beforeunload',leaving)}
  }
 }
 return()=>{users--;if(users===0){generation++;stopViewed?.();stopViewed=null;off?.();off=null;channel?.close();channel=null;if(timer)clearInterval(timer);timer=null}}
}

export function beginBrowserDownload(name:string,onCancel?:()=>void):string {
 const id=crypto.randomUUID()
 if(onCancel)cancellations.set(id,onCancel)
 const record:DownloadRecord={id,name,kind:'logs',status:'active',bytes:0,startedAt:Date.now(),owner:browserOwner}
 mergeBrowser([record])
 channel?.postMessage({type:'snapshot',records:[record]})
 return id
}
export function updateBrowserDownload(id:string,bytes:number,status:DownloadStatus='active',reason?:DownloadFailureReason) {
 if(status==='active'&&Date.now()-(lastPublish.get(id)??0)<250)return
 if(status==='active')lastPublish.set(id,Date.now());else lastPublish.delete(id)
 const records=useDownloads.getState().records.map(r=>r.id===id&&r.status==='active'?{...r,bytes,status,failureReason:status==='failed'?safeDownloadFailure(reason):undefined,endedAt:status==='active'?undefined:Date.now(),owner:status==='active'?r.owner:undefined}:r)
 useDownloads.setState({records})
 if(status!=='active'){cancellations.delete(id);persistBrowser()}
 const record=records.find(r=>r.id===id)
 if(record)channel?.postMessage({type:'snapshot',records:[record]})
}

/** Cancel only an active app-owned transfer, including a detached owner. */
export async function cancelDownload(id:string) {
 const record=useDownloads.getState().records.find(r=>r.id===id)
 if(!record||record.status!=='active')return
 if(isDesktop()){await call('Cancel',id);await reloadDownloads();return}
 const cancel=cancellations.get(id)
 if(cancel)cancel()
 else channel?.postMessage({type:'cancel',id})
}

export async function clearDownloadHistory() {
 if(isDesktop()){
  await call('ClearCompleted');mutationEpoch++
  useDownloads.setState({records:useDownloads.getState().records.filter(r=>r.status==='active')})
  await reloadDownloads();return
 }
 try{localStorage.removeItem(storageKey);useDownloads.setState({storageError:false})}catch{useDownloads.setState({storageError:true})}
 useDownloads.setState({records:useDownloads.getState().records.filter(r=>r.status==='active')})
 channel?.postMessage({type:'clear'})
}
export async function dismissDownload(id:string) {
 const record=useDownloads.getState().records.find(r=>r.id===id)
 if(!record||record.status==='active')throw new Error('download is unavailable')
 if(isDesktop())await call('Dismiss',id)
 else {
  readRemoved();rememberRemoved([id])
  const ids=[...removed].slice(-200);removed.clear();for(const value of ids)removed.add(value)
  try{localStorage.setItem(removedKey,JSON.stringify(ids))}catch{useDownloads.setState({storageError:true})}
 }
 mutationEpoch++
 useDownloads.setState({records:useDownloads.getState().records.filter(r=>r.id!==id)})
 if(isDesktop())await reloadDownloads()
 else {persistBrowser();channel?.postMessage({type:'remove',id})}
}
export interface DownloadDeletePreview {token:string;path:string;directory:boolean}
export async function prepareDownloadDelete(id:string) {return await call<DownloadDeletePreview>('PrepareDelete',id)}
export async function cancelDownloadDelete(id:string,token:string) {await call('CancelDelete',id,token)}
export async function deleteDownloadFile(id:string,token:string) {
 await call('DeleteFile',id,token);mutationEpoch++
 useDownloads.setState({records:useDownloads.getState().records.filter(r=>r.id!==id)})
 await reloadDownloads()
}
export async function openDownload(id:string,reveal=false) {await call(reveal?'Reveal':'Open',id)}
