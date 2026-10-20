import {useEffect,useRef,useState} from 'react'
import type {Client} from '../api/client'
import type {TermTab} from '../dock/store'
import {t} from '../i18n'
import {ToolWindowBridge} from '../tools/windowBridge'
import {ActionIconButton} from '../components/ActionIconButton'
import {WindowActionIcon} from '../components/icons'
import TerminalView from './TerminalView'

export default function TerminalHost({client,tab,active,mode}:{client:Client;tab:TermTab;active:boolean;mode:'desktop'|'browser'}){
 const latestTab=useRef(tab)
 useEffect(()=>{latestTab.current=tab},[tab])
 const [bridge]=useState(()=>new ToolWindowBridge(crypto.randomUUID(),'owner','shell'))
 const [detached,setDetached]=useState(false),[error,setError]=useState('')
 useEffect(()=>{
  const off=bridge.listen(m=>{
   if(m.type==='hello')void bridge.post('tab',latestTab.current)
   if(m.type==='return'||m.type==='closed'||m.type==='terminal-new'){setDetached(false);void bridge.close()}
  })
  void bridge.start().catch(e=>setError(String(e)))
  return()=>{off();if(bridge.opened){void bridge.post('owner-closed');void bridge.close()}bridge.stop()}
 },[bridge])
 const detach=async()=>{try{setError('');await bridge.open(`${tab.targetTitle} · ${tab.title}`);setDetached(true)}catch(e){setError(String(e))}}
 return <>
  {error&&<p role="alert" className="p-2 text-danger">{error}</p>}
  <div className="h-full" hidden={detached}><TerminalView client={client} tab={tab} active={active&&!detached} mode={mode} bridge={bridge} detached={detached} onWindowAction={()=>void detach()}/></div>
  {detached&&<div className="relative flex h-full items-center justify-center text-fg-muted"><span>{t('term.shell')} · {t('logs.detachWindow')}</span><div className="absolute right-3 top-2"><ActionIconButton label={t('logs.returnWindow')} description={t('logs.returnWindow')} onClick={()=>void bridge.post('request-close')}><WindowActionIcon returning className="h-4 w-4"/></ActionIconButton></div></div>}
 </>
}
