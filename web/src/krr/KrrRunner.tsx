import {useCallback, useEffect, useId, useRef, useState} from 'react'
import type {Client} from '../api/client'
import type {Target} from '../api/types'
import {t} from '../i18n'
import type {KrrResponse, KrrReview, KrrRunOptions} from './model'

/** Only a reviewed existing connection can launch a backend-owned Job. */
export function KrrRunner({client,target,namespace,onUpdate,onSettled}:{client:Client;target?:Target|null;namespace?:string;onUpdate:(response:KrrResponse)=>void;onSettled?:()=>void}) {
 const [options,setOptions]=useState<KrrRunOptions>({automatic:true,namespace:namespace??'',serviceAccount:'',source:'',label:'',cluster:'',singleCluster:false,reviewedAccount:false,noSourceAuth:false})
 const [review,setReview]=useState<KrrReview|null>(null),[busy,setBusy]=useState(false),[error,setError]=useState(false)
 const sourceList=useId(),[sources,setSources]=useState<string[]>([]),[discoveryIncomplete,setDiscoveryIncomplete]=useState(false),[discoveryEmpty,setDiscoveryEmpty]=useState(false)
 const epoch=useRef(0),pending=useRef<AbortController|null>(null)
 const connected=target?.provider==='kubernetes'&&target.connection?.state==='connected'
 const identity=`${target?.provider??''}/${target?.id??''}/${target?.configRev??''}/${target?.connection?.state??''}/${target?.connection?.id??''}`
 const invalidate=useCallback(()=>{epoch.current++;pending.current?.abort();pending.current=null},[])
 useEffect(()=>()=>invalidate(),[client,identity,invalidate])
 function update(patch:Partial<KrrRunOptions>){invalidate();setReview(null);setBusy(false);setError(false);setOptions(o=>({...o,...patch}))}
 async function perform(start:boolean){
  if(!connected||!target)return
  invalidate();const generation=epoch.current,controller=new AbortController();pending.current=controller
  setError(false);setBusy(true)
  try{
   const response=await client.krr(start?{command:'start',provider:target.provider,target:target.id,review:review??undefined}:{command:'review',provider:target.provider,target:target.id,options},controller.signal)
   if(epoch.current!==generation)return
   setReview(start?null:response.review??null);onUpdate(response)
  }catch{if(epoch.current===generation){setError(true);setReview(null)}}
  finally{if(epoch.current===generation){setBusy(false);pending.current=null;onSettled?.()}}
 }
 const provider=target?.provider,targetID=target?.id
 const discover=useCallback(async()=>{
  if(!connected||!provider||!targetID||!identity)return
  invalidate();const generation=epoch.current,controller=new AbortController();pending.current=controller;setBusy(true);setReview(null);setError(false);setDiscoveryEmpty(false)
  try{
   const response=await client.krr({command:'setup',provider,target:targetID},controller.signal)
   if(epoch.current!==generation)return
   const found=response.setup?.sources??[];setSources(found);setDiscoveryIncomplete(!response.setup?.complete);setDiscoveryEmpty(response.setup?.complete===true&&found.length===0)
   if(response.setup?.complete&&found.length===1)setOptions(o=>({...o,source:found[0]}))
  }catch{if(epoch.current===generation)setDiscoveryIncomplete(true)}
  finally{if(epoch.current===generation){setBusy(false);pending.current=null}}
 },[client,connected,provider,targetID,identity,invalidate])
 useEffect(()=>{const generation=epoch.current;const timer=setTimeout(()=>{if(epoch.current===generation)void discover()},0);return()=>clearTimeout(timer)},[discover])
 return <section aria-label={t('krr.runner')} className="space-y-2 border-t border-line pt-3">
  <h3 className="font-semibold">{t('krr.runner')}</h3>
  <p className="text-xs text-fg-muted">{t('krr.runnerScope')}</p>
  {!connected?<p>{t('krr.connectionRequired')}</p>:<>
   <p className="font-mono text-xs">{target?.title??target?.id}</p>
   <div className="grid gap-2 sm:grid-cols-2">
    <label className="flex min-w-0 flex-col gap-1">{t('krr.namespace')}<input className="min-w-0 rounded border border-line bg-panel px-2 py-1" value={options.namespace} maxLength={63} autoComplete="off" spellCheck={false} onChange={e=>update({namespace:e.target.value})}/></label>
    <label className="flex min-w-0 flex-col gap-1">{t('krr.source')}<input className="min-w-0 rounded border border-line bg-panel px-2 py-1" value={options.source} list={sourceList} maxLength={1024} autoComplete="off" spellCheck={false} onChange={e=>update({source:e.target.value})}/><datalist id={sourceList}>{sources.map(source=><option key={source} value={source}/>)}</datalist></label>
   </div>
   <button className="rounded border border-line px-2 py-1 disabled:opacity-50" disabled={busy} onClick={()=>void discover()}>{t('krr.discover')}</button>
   {discoveryIncomplete&&<p role="status" className="text-xs text-warning">{t('krr.discoveryIncomplete')}</p>}
   {discoveryEmpty&&<p role="status" className="text-xs text-fg-muted">{t('krr.discoveryEmpty')}</p>}
   {sources.length>1&&<p className="text-xs">{t('krr.chooseSource')}</p>}
   <details className="text-xs"><summary className="cursor-pointer">{t('krr.advanced')}</summary><p>{t('krr.sourceScope')}</p><div className="grid gap-2 sm:grid-cols-2">{(['label','cluster'] as const).map(field=><label key={field} className="flex min-w-0 flex-col gap-1">{t(`krr.${field}`)}<input className="min-w-0 rounded border border-line bg-panel px-2 py-1" value={options[field]} maxLength={128} autoComplete="off" spellCheck={false} onChange={e=>update({[field]:e.target.value})}/></label>)}</div></details>
   <button className="rounded border border-line px-2 py-1 disabled:opacity-50" disabled={busy||!options.namespace||!options.source||Boolean(options.label)!==Boolean(options.cluster)} onClick={()=>void perform(false)}>{t('krr.review')}</button>
   {review&&<div className="space-y-2 rounded border border-line p-2 text-xs">
    <p>{t('krr.reviewEffects')}</p><p>{t('krr.runnerStrategy')}</p>
    <p className="font-mono">{review.name} · {review.options.namespace}<br/>{review.image}<br/>namespace UID: {review.namespaceUID}<br/>{t('krr.snapshotAccess')}</p>
    <p>{t('krr.runnerLimits')}</p>
    <button className="rounded border border-line px-2 py-1 disabled:opacity-50" disabled={busy} onClick={()=>void perform(true)}>{t('krr.start')}</button>
   </div>}
  </>}
  {busy&&<p role="status">{t('krr.preparing')}</p>}
  {error&&<p role="alert" className="text-danger">{t('krr.runnerError')}</p>}
 </section>
}
