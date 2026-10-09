import { WindowActionIcon } from '../components/icons'
import { lazy, Suspense, useEffect, useState } from 'react'
import type { Client } from '../api/client'
import type { LogsTab } from '../dock/store'
import { t } from '../i18n'
import { LogWindowBridge } from './windowBridge'
const LogViewer = lazy(() => import('./LogViewer'))

export function LogHost({ client, tab, active }: { client: Client; tab: LogsTab; active: boolean }) {
 const [bridge] = useState(()=>new LogWindowBridge(crypto.randomUUID(),'owner'))
 const [detached,setDetached] = useState(false)
 const [error,setError] = useState('')
 useEffect(()=>{
  let live=true
  void bridge.start().catch(e=>{if(live)setError(String(e))})
  const off=bridge.listen(message=>{if(message.type==='return'||message.type==='closed') setDetached(false);if(message.type==='error')setError(String(message.payload))})
  return()=>{live=false;off();if(bridge.opened){void bridge.post('owner-closed');void bridge.close()}bridge.stop()}
 },[bridge])
 const detach=async()=>{try{setError('');await bridge.open(`${tab.targetTitle} · ${tab.title}`);setDetached(true)}catch(e){setError(e instanceof Error?e.message:String(e))}}
 return <>
  {error && <p role="alert" className="p-2 text-danger">{error}</p>}
  <div className="h-full" hidden={detached}><Suspense fallback={<p>{t('app.loading')}</p>}><LogViewer client={client} subject={tab.ref} initialChannel={tab.channel} active={active&&!detached} bridge={bridge} onWindowAction={()=>void detach()}/></Suspense></div>
  {detached && <div className="relative flex h-full items-center justify-center gap-3 text-fg-muted"><span>{t('logs.inWindow')}</span><button className="absolute right-3 top-2 flex h-[26px] w-[26px] items-center justify-center rounded border border-line hover:bg-hover" aria-label={t('logs.returnWindow')} title={t('logs.returnWindow')} onClick={()=>{setDetached(false);void bridge.close()}}><WindowActionIcon returning className="h-4 w-4"/></button></div>}
 </>
}
