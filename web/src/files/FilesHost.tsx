import {useCallback,useEffect,useLayoutEffect,useRef,useState} from 'react'
import type {Client} from '../api/client'
import {dock,holdDockClose,type FilesTab} from '../dock/store'
import {holdEdits} from '../edit/guard'
import {t} from '../i18n'
import {ActionIconButton} from '../components/ActionIconButton'
import {WindowActionIcon} from '../components/icons'
import {ToolWindowBridge} from '../tools/windowBridge'
import FileBrowser,{type FilesSnapshot} from './FileBrowser'

/** Owns the single capability; the dock and window are interchangeable views. */
export default function FilesHost({client,tab}:{client:Client;tab:FilesTab}){
 const [bridge]=useState(()=>new ToolWindowBridge(crypto.randomUUID(),'owner','files'))
 const snapshot=useRef<FilesSnapshot|undefined>(undefined)
 const mounted=useRef(true)
 const [resume,setResume]=useState<FilesSnapshot|undefined>(undefined)
 const [detached,setDetached]=useState(false),[error,setError]=useState('')
 const detachedRef=useRef(detached)
 useLayoutEffect(()=>{detachedRef.current=detached},[detached])
 const capture=useCallback((s:FilesSnapshot)=>{snapshot.current=s},[])
 const returning=useRef(new Map<string,()=>void>())
 useEffect(()=>{
  const unhold=holdEdits({scope:tab.id,dirty:()=>detachedRef.current&&!!snapshot.current&&(snapshot.current.writing||!!snapshot.current.doc&&snapshot.current.text!==snapshot.current.doc.text),discard:()=>{if(snapshot.current)snapshot.current={...snapshot.current,doc:null,text:''}},focus:()=>{dock.activate(tab.id);if(bridge.opened)void bridge.open(tab.title)}})
  const unblock=holdDockClose(tab.id,async()=>{
   if(bridge.opened){
    const id=crypto.randomUUID()
    const flushed=new Promise<void>((resolve,reject)=>{
     const timer=setTimeout(()=>{returning.current.delete(id);reject(new Error(t('tools.windowNoAnswer')))},5000)
     returning.current.set(id,()=>{clearTimeout(timer);resolve()})
    })
    try{await bridge.post('flush-request',id);await flushed}catch(e){setError(String(e));return true}
   }
   return !!snapshot.current?.writing
  })
  const off=bridge.listen(m=>{
   if(m.type==='hello')void bridge.post('boot',{tab,snapshot:snapshot.current})
   if(m.type==='state')capture(m.payload as FilesSnapshot)
   if(m.type==='flushed'){returning.current.get(String(m.payload))?.();returning.current.delete(String(m.payload))}
   if(m.type==='return'||m.type==='closed'){if(m.payload)capture(m.payload as FilesSnapshot);setResume(snapshot.current);setDetached(false);void bridge.close()}
  })
  void bridge.start().catch(e=>setError(String(e)))
  return()=>{
   off();unhold();unblock()
   if(bridge.opened){void bridge.post('owner-closed');void bridge.close()}
   bridge.stop()
  }
 },[bridge,tab,client,capture])
 // A capability belongs to the tab, never an individual view's mount.
 useEffect(()=>{
  mounted.current=true
  const release=()=>{if(tab.preparedId)void client.files({ref:tab.ref,preparedId:tab.preparedId,instance:'',channel:'',path:'/',command:'close'}).catch(()=>{})}
  window.addEventListener('pagehide',release)
  return()=>{mounted.current=false;window.removeEventListener('pagehide',release);setTimeout(()=>{if(!mounted.current)release()},0)}
 },[client,tab])
 const detach=async()=>{
  if(!snapshot.current||snapshot.current.busy||snapshot.current.initializing)return
  try{setError('');if(new TextEncoder().encode(JSON.stringify({tab,snapshot:snapshot.current})).length>16*1024*1024)throw new Error(t('tools.windowLimit'));await bridge.open(`${tab.targetTitle} · ${tab.title}`);setDetached(true)}catch(e){setError(String(e))}
 }
 const restore=()=>{void bridge.post('request-close').catch(e=>setError(String(e)))}
 return <>
  {error&&<p role="alert" className="p-2 text-danger">{error}</p>}
  {detached?<div className="relative flex h-full items-center justify-center text-fg-muted"><span>{t('files.title')} · {t('logs.detachWindow')}</span><div className="absolute right-3 top-2"><ActionIconButton label={t('logs.returnWindow')} description={t('logs.returnWindow')} onClick={restore}><WindowActionIcon returning className="h-4 w-4"/></ActionIconButton></div></div>:<FileBrowser client={client} subject={tab.ref} selectedInstance={tab.selectedInstance} preparedId={tab.preparedId} inline editScope={tab.id} transfer={{initial:resume,capture,managed:true,onWindowAction:()=>void detach()}} onClose={()=>dock.close(tab.id)}/>}
 </>
}
