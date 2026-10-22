import {useEffect, useRef, useState} from 'react'
import type {Client} from '../api/client'
import type {Target} from '../api/types'
import {t} from '../i18n'
import type {KrrResponse, KrrReview, KrrRunOptions} from './model'

/** Only a reviewed existing connection can launch a backend-owned Job. */
export function KrrRunner({client,target,onUpdate,onSettled}:{client:Client;target?:Target|null;onUpdate:(response:KrrResponse)=>void;onSettled?:()=>void}) {
 const [options,setOptions]=useState<KrrRunOptions>({namespace:'',serviceAccount:'',source:'',label:'',cluster:'',singleCluster:false,reviewedAccount:false,noSourceAuth:false})
 const [review,setReview]=useState<KrrReview|null>(null),[busy,setBusy]=useState(false),[error,setError]=useState(false)
 const epoch=useRef(0),pending=useRef<AbortController|null>(null)
 const connected=target?.provider==='kubernetes'&&target.connection?.state==='connected'
 const identity=`${target?.provider??''}/${target?.id??''}/${target?.configRev??''}/${target?.connection?.state??''}/${target?.connection?.id??''}`
 function invalidate(){epoch.current++;pending.current?.abort();pending.current=null}
 useEffect(()=>()=>invalidate(),[client,identity])
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
 const fields: {key:'namespace'|'serviceAccount'|'source'|'label'|'cluster';max:number}[]=[{key:'namespace',max:63},{key:'serviceAccount',max:253},{key:'source',max:1024},...(!options.singleCluster?[{key:'label' as const,max:128},{key:'cluster' as const,max:128}]:[])]
 return <section aria-label={t('krr.runner')} className="space-y-2 border-t border-line pt-3">
  <h3 className="font-semibold">{t('krr.runner')}</h3>
  <p className="text-xs text-fg-muted">{t('krr.runnerScope')}</p>
  {!connected?<p>{t('krr.connectionRequired')}</p>:<>
   <p className="font-mono text-xs">{target?.title??target?.id}</p>
   <div className="grid gap-2 sm:grid-cols-2">{fields.map(field=><label key={field.key} className="flex min-w-0 flex-col gap-1">{t(`krr.${field.key}`)}<input className="min-w-0 rounded border border-line bg-panel px-2 py-1" value={options[field.key]} maxLength={field.max} autoComplete="off" spellCheck={false} onChange={e=>update({[field.key]:e.target.value})}/></label>)}</div>
   <label className="flex items-start gap-2"><input type="checkbox" checked={options.singleCluster} onChange={e=>update({singleCluster:e.target.checked,label:'',cluster:''})}/><span>{t('krr.singleCluster')}</span></label>
   <label className="flex items-start gap-2"><input type="checkbox" checked={options.reviewedAccount} onChange={e=>update({reviewedAccount:e.target.checked})}/><span>{t('krr.reviewedAccount')}</span></label>
   <label className="flex items-start gap-2"><input type="checkbox" checked={options.noSourceAuth} onChange={e=>update({noSourceAuth:e.target.checked})}/><span>{t('krr.noSourceAuth')}</span></label>
   <button className="rounded border border-line px-2 py-1 disabled:opacity-50" disabled={busy||!options.namespace||!options.serviceAccount||!options.source||!options.reviewedAccount||!options.noSourceAuth||(!options.singleCluster&&(!options.label||!options.cluster))} onClick={()=>void perform(false)}>{t('krr.review')}</button>
   {review&&<div className="space-y-2 rounded border border-line p-2 text-xs">
    <p>{t('krr.reviewEffects')}</p><p>{t('krr.runnerStrategy')}</p>
    <p className="font-mono">{review.name} · {review.options.namespace}<br/>{review.image}<br/>namespace UID: {review.namespaceUID}<br/>ServiceAccount UID: {review.serviceAccountUID}</p>
    <p>{t('krr.runnerLimits')}</p>
    <button className="rounded border border-line px-2 py-1 disabled:opacity-50" disabled={busy} onClick={()=>void perform(true)}>{t('krr.start')}</button>
   </div>}
  </>}
  {busy&&<p role="status">{t('krr.preparing')}</p>}
  {error&&<p role="alert" className="text-danger">{t('krr.runnerError')}</p>}
 </section>
}
