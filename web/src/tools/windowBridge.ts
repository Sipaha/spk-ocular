import {t} from '../i18n'
import {isDesktop} from '../api/client'
import type {WindowMessage} from '../logs/windowBridge'
const event='ocular_tool_window'
const service='github.com/spk/spk-ocular/internal/desktop.LogWindows.'
export type ToolKind='files'|'shell'

/** UI-only, memory-only transport. The main window retains the actual consumer. */
export class ToolWindowBridge {
 readonly sender=crypto.randomUUID()
 private channel:BroadcastChannel|null=null
 private off:(()=>void)|null=null
 private listeners=new Set<(m:WindowMessage)=>void>()
 private popup:ReturnType<typeof window.open>=null
 private opening:Promise<void>|null=null
 private queuedBytes=0
 private sequence=0
 private acknowledgements=new Map<number,{resolve:()=>void;reject:(error:Error)=>void;timer:ReturnType<typeof setTimeout>}>()
 private pending:Promise<unknown>=Promise.resolve()
 private generation=0
 private active=false
 private epoch=new URLSearchParams(window.location.search).get('toolEpoch')??''
 private heartbeat:ReturnType<typeof setInterval>|null=null
 private leaving=()=>{if(this.role==='owner'&&this.opened){void this.post('owner-closed');void this.close()}}
 private latest=new Map<string,unknown>()
 private sending:Promise<void>|null=null
 private terminalFlush?:()=>Promise<void>
 bindTerminalFlush(flush:()=>Promise<void>){this.terminalFlush=flush;return()=>{if(this.terminalFlush===flush)this.terminalFlush=undefined}}
 flushTerminal(){return this.terminalFlush?.()??Promise.resolve()}
 opened=false
 constructor(readonly id:string,readonly role:'owner'|'guest',readonly kind:ToolKind){}
 async start(){
  const generation=++this.generation
  if(isDesktop()){
   const {Events}=await import('@wailsio/runtime');if(generation!==this.generation)return
   this.off=Events.On(event,(e:{data:unknown})=>this.receive(e.data as WindowMessage))
  }else{
   this.channel=new BroadcastChannel(event)
   this.channel.onmessage=e=>this.receive(e.data as WindowMessage)
  }
  this.active=true
  window.addEventListener('pagehide',this.leaving)
  this.heartbeat=setInterval(()=>{
   if(this.role==='owner'&&this.opened&&this.popup?.closed){this.opened=false;this.listeners.forEach(f=>f({id:this.id,sender:'host',type:'closed'}))}
   if(this.role==='guest'&&!isDesktop()&&window.opener?.closed)this.listeners.forEach(f=>f({id:this.id,sender:'host',type:'owner-lost'}))
  },1000)
 }
 private receive(m:WindowMessage&{epoch?:string}){
  if(m.id!==this.id||m.sender===this.sender||m.epoch!==this.epoch)return
  if(m.type==='snapshot-ack'){this.acknowledgements.get(Number(m.payload))?.resolve();return}
  if(m.type==='snapshot-update'){
   const update=m.payload as {seq:number;type:string;payload:unknown}
   this.listeners.forEach(f=>f({...m,type:update.type,payload:update.payload}))
   void this.post('snapshot-ack',update.seq).catch(()=>{})
   return
  }
  this.listeners.forEach(f=>f(m))
 }
 listen(f:(m:WindowMessage)=>void){this.listeners.add(f);return()=>{this.listeners.delete(f)}}
 post(type:string,payload?:unknown):Promise<unknown>{
  const epoch=this.epoch
  const json=JSON.stringify(payload??null)
  const bytes=new TextEncoder().encode(json).length
  if(bytes+this.queuedBytes>16*1024*1024)return Promise.reject(new Error(t('tools.windowLimit')))
  if(!isDesktop()){this.channel?.postMessage({id:this.id,sender:this.sender,type,payload,epoch});return Promise.resolve()}
  this.queuedBytes+=bytes
  const next=this.pending.catch(()=>{}).then(async()=>{const {Call}=await import('@wailsio/runtime');return Call.ByName(service+'SendTool',this.id,this.sender,type,epoch,json)}).finally(()=>{this.queuedBytes-=bytes})
  this.pending=next
  return next
 }
 /** Coalesce snapshots while Wails is sending, rather than queueing full editor copies. */
 update(type:string,payload:unknown){
  if(!this.active)return Promise.resolve()
  this.latest.set(type,payload)
  return this.beginDrain()
 }
 private beginDrain():Promise<void>{
  if(!this.sending)this.sending=this.drain().then(()=>{
   this.sending=null
   if(this.latest.size)return this.beginDrain()
  },error=>{
   this.sending=null
   // A newer snapshot has not been attempted yet. Do not strand it behind
   // a failed startup delivery; its own acknowledgement still bounds it.
   if(this.active&&this.latest.size)void this.beginDrain().catch(()=>{})
   throw error
  })
  return this.sending
 }
 private async drain(){while(this.latest.size){const [type,payload]=this.latest.entries().next().value!;this.latest.delete(type);await this.sendUpdate(type,payload)}}
 private async sendUpdate(type:string,payload:unknown){
  const seq=++this.sequence
  const ack=new Promise<void>((resolve,reject)=>{const timer=setTimeout(()=>reject(new Error(t('tools.windowNoAnswer'))),5000);this.acknowledgements.set(seq,{resolve,reject,timer})})
  void ack.catch(()=>{})
  try{await this.post('snapshot-update',{seq,type,payload});await ack}finally{const pending=this.acknowledgements.get(seq);if(pending)clearTimeout(pending.timer);this.acknowledgements.delete(seq)}
 }
 async flush(){await this.sending;await this.pending}
 open(title:string){if(!this.opening)this.opening=this.openWindow(title).finally(()=>{this.opening=null});return this.opening}
 private async openWindow(title:string){
  if(this.opened){this.popup?.focus();if(!isDesktop())return}else this.epoch=crypto.randomUUID()
  if(isDesktop()){const {Call}=await import('@wailsio/runtime');await Call.ByName(service+'OpenTool',this.id,title,this.kind,this.epoch)}
  else{
   const screen=window.screen,width=Math.min(this.kind==='shell'?960:1200,screen.availWidth||1200),height=Math.min(this.kind==='shell'?600:800,screen.availHeight||800)
   this.popup=window.open(`?toolWindow=${encodeURIComponent(this.id)}&toolKind=${this.kind}&toolEpoch=${this.epoch}`,'ocular-tool-'+this.id,`popup,width=${width},height=${height}`)
   if(!this.popup)throw new Error(t('tools.windowBlocked'))
  }
  this.opened=true
 }
 async close(){
  const epoch=this.epoch
  this.opened=false
  if(isDesktop()){const {Call}=await import('@wailsio/runtime');await Call.ByName(service+'CloseTool',this.id,epoch)}
  else if(this.role==='owner'){this.popup?.close();this.popup=null}else window.close()
 }
 stop(){this.active=false;this.latest.clear();for(const pending of this.acknowledgements.values()){clearTimeout(pending.timer);pending.reject(new Error(t('tools.windowClosed')))}this.acknowledgements.clear();window.removeEventListener('pagehide',this.leaving);this.generation++;if(this.heartbeat)clearInterval(this.heartbeat);this.heartbeat=null;this.off?.();this.channel?.close();this.channel=null;this.off=null;this.listeners.clear()}
}
