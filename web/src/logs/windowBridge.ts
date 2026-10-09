import { isDesktop } from '../api/client'
import type { Ref, LogQuery } from '../api/types'
import type { Frame } from './ndjson'
import type { Window, Carry } from './buffer'
import type { LogSource, StreamStatus } from './useLogStream'

export interface LogViewState {
 channel: string | null; tail: number; since: { key: string; at: string } | null; previous: boolean
 showTime: boolean; showSource: boolean | null; wrap: boolean; follow: boolean
 search?: string; filterText?: string; useRegex?: boolean
}
export interface LogStreamSnapshot { nextId: number; carry: [number, Carry][]; win: Window; sources: LogSource[]; status: StreamStatus; query: LogQuery }
export interface WindowMessage { id: string; sender: string; type: string; payload?: unknown }
export const sameLogQuery = (a: LogQuery, b: LogQuery) =>
 a.channel === b.channel && a.previous === b.previous && a.follow === b.follow &&
 a.tailLines === b.tailLines && a.sinceTime === b.sinceTime
const event = 'ocular_log_window'
const service = 'github.com/spk/spk-ocular/internal/desktop.LogWindows.'

/** One stream owner, with a secondary window rendering its snapshots and frames. */
export class LogWindowBridge {
 readonly sender = crypto.randomUUID()
 private listeners = new Set<(message: WindowMessage) => void>()
 private channel: BroadcastChannel | null = null
 private generation = 0
 opened = false
 private off: (() => void) | null = null
 private pending: Promise<unknown> = Promise.resolve()
 connected = false
 captureFilter?: () => Pick<LogViewState, 'search' | 'filterText' | 'useRegex'>
 applyFilter?: (state: LogViewState) => void
 captureView?: () => LogViewState
 applyView?: (state: LogViewState) => void
 captureStream?: () => LogStreamSnapshot
 applyQuery?: (query: LogQuery) => void
 subject?: Ref
 constructor(readonly id: string, readonly role: 'owner' | 'guest') {}
 async start() {
  const generation=++this.generation
  if (isDesktop()) {
   const { Events } = await import('@wailsio/runtime')
   if(generation!==this.generation)return
   this.off = Events.On(event, (message: { data: unknown }) => this.receive(message.data as WindowMessage))
  } else {
   this.channel = new BroadcastChannel(event)
   this.channel.onmessage = e => this.receive(e.data as WindowMessage)
  }
 }
 private receive(message: WindowMessage) {
  if (message.id !== this.id || message.sender === this.sender) return
  if (this.role === 'owner') {
   if (message.type === 'hello') void this.post('boot', { subject: this.subject, view: this.captureView?.() })
   if (message.type === 'subscribe') {
    const query = message.payload as LogQuery
    const snapshot = this.captureStream?.()
    if (snapshot && sameLogQuery(snapshot.query,query)) {
     void this.post('snapshot', snapshot)
     this.connected = true
    } else { this.connected = true; this.applyQuery?.(query) }
   }
   if (message.type === 'view') this.applyView?.(message.payload as LogViewState)
   if (message.type === 'closed' || message.type === 'return') this.connected = false
  }
  this.listeners.forEach(listener => listener(message))
 }
 configureView(subject: Ref, capture: () => LogViewState, apply: (state: LogViewState) => void, query: (query: LogQuery) => void) { this.subject=subject;this.captureView=capture;this.applyView=apply;this.applyQuery=query }
 configureFilter(capture: () => Pick<LogViewState,'search'|'filterText'|'useRegex'>, apply: (state: LogViewState) => void) { this.captureFilter=capture;this.applyFilter=apply }
 configureStream(capture: () => LogStreamSnapshot) { this.captureStream=capture }
 listen(listener: (message: WindowMessage) => void) { this.listeners.add(listener); return () => { this.listeners.delete(listener) } }
 post(type: string, payload?: unknown): Promise<unknown> {
  const message: WindowMessage = { id: this.id, sender: this.sender, type, payload }
  if (!isDesktop()) { this.channel?.postMessage(message); return Promise.resolve() }
  // Wails calls run concurrently; serialize to preserve NDJSON frame order.
  this.pending = this.pending.catch(() => {}).then(async () => {
   const { Call } = await import('@wailsio/runtime')
   return Call.ByName(service+'Send',this.id,this.sender,type,JSON.stringify(payload ?? null))
  })
  const result=this.pending
  this.pending=result.catch(error=>{this.listeners.forEach(listener=>listener({id:this.id,sender:this.sender,type:'error',payload:error instanceof Error?error.message:String(error)}))})
  return result
 }
 publish(frame: Frame) { if(this.role==='owner' && this.connected) void this.post('frame', frame) }
 async open(title: string) {
  if(isDesktop()) { const { Call }=await import('@wailsio/runtime');await Call.ByName(service+'Open',this.id,title) }
  else { const popup=window.open(`?logsWindow=${encodeURIComponent(this.id)}`,'ocular-logs-'+this.id,'popup,width=1000,height=650');if(!popup) throw new Error('The browser blocked the log window'); }
  this.opened=true
 }
 async close() { this.opened=false; if(isDesktop()){ const {Call}=await import('@wailsio/runtime');await Call.ByName(service+'Close',this.id) } else window.close() }
 stop() { this.generation++; this.off?.(); this.channel?.close();this.off=null;this.channel=null;this.listeners.clear() }
}
