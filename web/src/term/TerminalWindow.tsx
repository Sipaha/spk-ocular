import {useCallback,useEffect,useState} from 'react'
import {isDesktop,type Client} from '../api/client'
import type {TermTab} from '../dock/store'
import {loadLanguage,setLanguage,t} from '../i18n'
import {useStore} from '../store'
import {ToolWindowBridge} from '../tools/windowBridge'
import TerminalView from './TerminalView'

export default function TerminalWindow({client,id}:{client:Client;id:string}){
 const [bridge]=useState(()=>new ToolWindowBridge(id,'guest','shell'))
 const [tab,setTab]=useState<TermTab|null>(null),[error,setError]=useState('')
 const restore=useCallback(()=>{void(async()=>{await bridge.flushTerminal?.();await bridge.flush();await bridge.post('return')})().catch(e=>setError(String(e)))},[bridge])
 useEffect(()=>{
  let live=true
  const off=bridge.listen(m=>{
   if(m.type==='tab')setTab(m.payload as TermTab)
   if(m.type==='request-close')restore()
   if(m.type==='owner-closed'||m.type==='owner-lost')void bridge.close()
  })
  void(async()=>{const info=await client.appInfo();await loadLanguage(info.language);if(!live)return;setLanguage(info.language);useStore.setState({info});await bridge.start();if(live)await bridge.post('hello')})().catch(e=>setError(String(e)))
  const closed=()=>{void bridge.post('closed')}
  window.addEventListener('beforeunload',closed)
  return()=>{live=false;off();window.removeEventListener('beforeunload',closed);bridge.stop()}
 },[client,bridge,restore])
 return <main className="flex h-screen min-h-0 flex-col bg-app text-fg">
  {error&&<p role="alert" className="p-2 text-danger">{error}</p>}
  <div className="min-h-0 flex-1">{tab?<TerminalView client={client} tab={tab} active mode={isDesktop()?'desktop':'browser'} bridge={bridge} onWindowAction={restore}/>:<p>{t('app.loading')}</p>}</div>
 </main>
}
