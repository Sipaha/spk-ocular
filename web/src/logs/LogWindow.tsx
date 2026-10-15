import { lazy, Suspense, useEffect, useState } from 'react'
import type { Client } from '../api/client'
import type { Ref } from '../api/types'
import { loadLanguage, setLanguage, t } from '../i18n'
import { useStore } from '../store'
import { LogWindowBridge, type LogViewState } from './windowBridge'
const LogViewer = lazy(() => import('./LogViewer'))

export function LogWindow({client,id}:{client:Client;id:string}) {
 const [bridge]=useState(()=>new LogWindowBridge(id,'guest'))
 const [boot,setBoot]=useState<{subject:Ref;view:LogViewState}|null>(null)
 const [error,setError]=useState('')
 useEffect(()=>{
  let live=true
  const off=bridge.listen(message=>{
   if(message.type==='boot') setBoot(message.payload as {subject:Ref;view:LogViewState})
   if(message.type==='owner-closed') void bridge.close();if(message.type==='error')setError(String(message.payload))
  })
  void (async()=>{const info=await client.appInfo();await loadLanguage(info.language);setLanguage(info.language);if(!live)return;useStore.setState({info});await bridge.start();if(!live)return;await bridge.post('hello')})().catch(e=>{if(live)setError(String(e))})
  const closed=()=>{void bridge.post('closed')}
  window.addEventListener('beforeunload',closed)
  return()=>{live=false;off();window.removeEventListener('beforeunload',closed);bridge.stop()}
 },[client,bridge])
 const restore=async()=>{try{if(bridge.captureView)await bridge.post('view',bridge.captureView());await bridge.post('return');await bridge.close()}catch(e){setError(String(e))}}
 return <main className="flex h-screen min-h-0 flex-col bg-app text-fg">
  {error && <p role="alert" className="p-2 text-danger">{error}</p>}
  {boot?<Suspense fallback={<p>{t('app.loading')}</p>}><LogViewer client={client} subject={boot.subject} active bridge={bridge} initial={boot.view} onWindowAction={()=>void restore()}/></Suspense>:<p className="p-3">{t('app.loading')}</p>}
 </main>
}
