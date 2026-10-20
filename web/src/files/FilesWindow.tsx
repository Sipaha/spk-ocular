import {useCallback,useEffect,useRef,useState} from 'react'
import type {Client} from '../api/client'
import type {FilesTab} from '../dock/store'
import {loadLanguage,setLanguage,t} from '../i18n'
import {useStore} from '../store'
import {DiscardPrompt} from '../edit/DiscardPrompt'
import {ToolWindowBridge} from '../tools/windowBridge'
import FileBrowser,{type FilesSnapshot} from './FileBrowser'

export default function FilesWindow({client,id}:{client:Client;id:string}){
 const [bridge]=useState(()=>new ToolWindowBridge(id,'guest','files'))
 const [boot,setBoot]=useState<{tab:FilesTab;snapshot:FilesSnapshot}|null>(null)
 const tabRef=useRef<FilesTab|null>(null)
 const latest=useRef<FilesSnapshot|undefined>(undefined),closing=useRef(false)
 const [error,setError]=useState('')
 const finish=useCallback(async()=>{
  if(!latest.current||latest.current.busy)return
  closing.current=false
  try{await bridge.flush();await bridge.post('return',latest.current)}catch(e){closing.current=false;setError(String(e))}
 },[bridge])
 const restore=useCallback(()=>{closing.current=true;void finish()},[finish])
 const capture=useCallback((s:FilesSnapshot)=>{
  latest.current=s
  void bridge.update('state',s).catch(e=>setError(String(e)))
  if(closing.current&&!s.busy)void finish()
 },[bridge,finish])
 useEffect(()=>{
  let live=true
  const off=bridge.listen(m=>{
   if(m.type==='boot'){const b=m.payload as {tab:FilesTab;snapshot:FilesSnapshot};latest.current=b.snapshot;tabRef.current=b.tab;setBoot(b)}
   if(m.type==='request-close')restore()
   if(m.type==='flush-request')void bridge.flush().then(()=>bridge.post('state',latest.current)).then(()=>bridge.post('flushed',m.payload)).catch(e=>setError(String(e)))
   if(m.type==='owner-closed'||m.type==='owner-lost'){const tab=tabRef.current;if(tab?.preparedId)void client.files({ref:tab.ref,preparedId:tab.preparedId,instance:'',channel:'',path:'/',command:'close'}).catch(()=>{});void bridge.close()}
  })
  void(async()=>{const info=await client.appInfo();await loadLanguage(info.language);if(!live)return;setLanguage(info.language);useStore.setState({info});await bridge.start();if(live)await bridge.post('hello')})().catch(e=>setError(String(e)))
  const closed=()=>{void bridge.post('closed',latest.current)}
  window.addEventListener('beforeunload',closed)
  return()=>{live=false;off();window.removeEventListener('beforeunload',closed);bridge.stop()}
 },[bridge,client,restore])
 return <main className="flex h-screen min-h-0 flex-col bg-app text-fg">
  {error&&<p role="alert" className="p-2 text-danger">{error}</p>}
  <div className="min-h-0 flex-1">{boot?<FileBrowser client={client} subject={boot.tab.ref} selectedInstance={boot.tab.selectedInstance} preparedId={boot.tab.preparedId} inline transfer={{initial:boot.snapshot,capture,managed:true,onWindowAction:restore,returning:true}} onClose={restore}/>:<p>{t('app.loading')}</p>}</div>
  <DiscardPrompt/>
 </main>
}
