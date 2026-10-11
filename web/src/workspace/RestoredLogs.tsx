import {useEffect,useRef,useState} from 'react'
import type {Client} from '../api/client'
import type {LogsTab} from '../dock/store'
import {dock} from '../dock/store'
import {errorDetail} from '../errors'
import {t} from '../i18n'
import {useStore} from '../store'
import {currentTarget} from './PropertiesHost'

export default function RestoredLogs({client,tab}:{client:Client;tab:LogsTab}){
 const view=useStore(s=>s.view),target=currentTarget(tab.ref,view)
 const source=target?.connection?.state==='connected'?`${target.connection.id}:${target.configRev??''}`:null
 const [busy,setBusy]=useState(false),[error,setError]=useState('')
 const sequence=useRef(0)
 useEffect(()=>()=>{sequence.current++},[])
 const resume=async()=>{
  if(!source||busy)return
  const own=++sequence.current;setBusy(true);setError('')
  try{
   const r=await client.getResource(tab.ref)
   const next=currentTarget(tab.ref,useStore.getState().view)
   if(own!==sequence.current)return
   if(next?.connection?.state!=='connected'||`${next.connection.id}:${next.configRev??''}`!==source)throw new Error(t('workspace.connectFirst'))
   if(!r?.ref||r.ref.uid!==tab.ref.uid||r.ref.kind!==tab.ref.kind||r.ref.name!==tab.ref.name||(r.ref.scope??'')!==(tab.ref.scope??''))throw new Error(t('workspace.replaced'))
   dock.resumeLogs(tab.id)
  }catch(e){if(own===sequence.current)setError(errorDetail(e))}finally{if(own===sequence.current)setBusy(false)}
 }
 return <div className="flex h-full flex-col items-start justify-center gap-2 overflow-auto p-4 text-sm"><p>{t('workspace.restoredHint')}</p><p className="text-fg-muted">{tab.ref.kind} · {tab.ref.scope?tab.ref.scope+'/':''}{tab.ref.name}</p>{error&&<p role="alert" className="text-danger">{error}</p>}<button className="rounded border border-line px-2 py-1 text-xs hover:bg-hover disabled:opacity-50" disabled={!source||busy} onClick={()=>void resume()}>{busy?t('app.loading'):t('workspace.resumeLogs')}</button>{!source&&<p className="text-fg-muted">{t('workspace.connectFirst')}</p>}</div>
}
