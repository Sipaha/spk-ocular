import {useEffect,useMemo,useRef,useState} from 'react'
import {useVirtualizer} from '@tanstack/react-virtual'
import type {Client} from '../api/client'
import type {AccessAttributes,AccessReview,RBACSnapshot,RBACGrant,RBACRule,Ref,ScopeSel} from '../api/types'
import {getLanguage,t} from '../i18n'
import {Select} from '../components/Select'
import {errorDetail} from '../errors'
import {matchingGrants} from './model'

const button='rounded-md border border-line px-2 py-1 text-xs hover:bg-hover disabled:opacity-40'
const input='min-w-0 rounded-md border border-line bg-panel px-2 py-1 outline-none focus:border-accent disabled:opacity-40'
const defaults=(scope:ScopeSel):AccessAttributes=>({verb:'get',group:'',resource:'pods',subresource:'',namespace:scope.mode==='one'?scope.name??'':scope.names?.[0]??'',name:''})
export interface RbacProps {client:Client;target:{provider:string;id:string};scope:ScopeSel;initialSubject?:Ref;failedRef?:Ref;onResource:(ref:Ref)=>void}

export default function RbacWorkbench({client,target,scope,initialSubject,failedRef,onResource}:RbacProps){
 const [subject,setSubject]=useState<Ref|undefined>(initialSubject)
 const [snapshot,setSnapshot]=useState<RBACSnapshot|null>(null)
 const [busy,setBusy]=useState(true)
 const [error,setError]=useState<unknown>(null)
 const [revision,setRevision]=useState(0)
 const [filter,setFilter]=useState('')
 const [attrs,setAttrs]=useState<AccessAttributes>(()=>defaults(scope))
 const [review,setReview]=useState<AccessReview|null>(null)
 const [checkError,setCheckError]=useState<unknown>(null)
 const [checking,setChecking]=useState(false)
 const [local,setLocal]=useState<AccessAttributes|null>(null)
 const [manualNS,setManualNS]=useState(scope.mode==='one'?scope.name??'':'')
 const [manualName,setManualName]=useState('')
 const check=useRef<{generation:number;abort:AbortController|null}>({generation:0,abort:null})
 const scroll=useRef<HTMLDivElement>(null)
 const scopeKey=JSON.stringify(scope),subjectKey=JSON.stringify(subject??null)
 useEffect(()=>{
  let live=true
  const abort=new AbortController()
  client.rbacSnapshot({provider:target.provider,target:target.id,scope:JSON.parse(scopeKey),subject:JSON.parse(subjectKey)??undefined},abort.signal).then(value=>{if(live){setSnapshot(value);setError(null);setBusy(false);if(value.subject&&!JSON.parse(subjectKey)?.uid)setSubject(value.subject)}},e=>{if(live){setError(e);setBusy(false)}})
  return()=>{live=false;abort.abort()}
 },[client,target.provider,target.id,scopeKey,subjectKey,revision])
 useEffect(()=>{const pending=check.current;return()=>{pending.generation++;pending.abort?.abort()}},[client,target.provider,target.id,scopeKey,subjectKey])
 const invalidate=()=>{check.current.generation++;check.current.abort?.abort();setChecking(false);setReview(null);setCheckError(null);setLocal(null)}
 const change=(field:keyof AccessAttributes,value:string)=>{invalidate();setAttrs(a=>({...a,[field]:value,...((field==='verb'&&(value==='deletecollection'||(value==='create'&&!a.subresource)))||(field==='subresource'&&!value&&a.verb==='create')?{name:''}:{})}))}
 const choose=(ref:Ref|undefined)=>{invalidate();setSubject(ref);setSnapshot(null);setBusy(true)}
 const runCheck=(ref?:Ref)=>{
  check.current.abort?.abort()
  const abort=new AbortController(),generation=++check.current.generation
  check.current.abort=abort;setChecking(true);setReview(null);setLocal(null);setCheckError(null)
  const attributes=ref?{...attrs,verb:'get',subresource:''}:attrs
  client.checkAccess({provider:target.provider,target:target.id,attributes,ref},abort.signal).then(value=>{
   if(generation===check.current.generation){setAttrs(value.attributes);setReview(value);setChecking(false)}
  },e=>{if(generation===check.current.generation){setCheckError(e);setChecking(false)}})
 }
 const rows=useMemo(()=>{
  const query=filter.trim().toLocaleLowerCase()
  return(snapshot?.grants??[]).flatMap<{grant:RBACGrant;index:number;rule:RBACRule|null;ruleIndex:number}>((grant,index)=>{
   if(query&&![grant.binding.name,grant.bindingKind,grant.role.name,grant.roleKind,grant.subjectKind,grant.subjectName,grant.namespace??'',...grant.rules.flatMap(r=>[...r.apiGroups,...r.resources,...r.verbs,...r.resourceNames,...r.nonResourceURLs])].some(s=>s.toLocaleLowerCase().includes(query)))return[]
   return grant.rules.length?grant.rules.map((rule,ruleIndex)=>({grant,index,rule,ruleIndex})):[{grant,index,rule:null,ruleIndex:0}]
  })
 },[snapshot,filter])
 const virtual=useVirtualizer({count:rows.length,getScrollElement:()=>scroll.current,estimateSize:()=>108,overscan:4,getItemKey:i=>(rows[i].grant.binding.uid||rows[i].index)+':'+rows[i].ruleIndex})
 const matches=local&&snapshot?matchingGrants(snapshot.grants,local):null
 const selectedKey=subject?JSON.stringify(subject):'self'
 const options=[{value:'self',label:t('rbac.currentUser')},...(snapshot?.accounts??[]).map(ref=>({value:JSON.stringify(ref),label:ref.scope+'/'+(ref.title??ref.name)}))]
 if(subject&&!options.some(o=>o.value===selectedKey))options.push({value:selectedKey,label:subject.scope+'/'+(subject.title??subject.name)})
 const date=(at:number)=>new Date(at).toLocaleString(getLanguage())
 const refButton=(ref:Ref,label:string)=><button className="max-w-full truncate text-left text-accent underline underline-offset-2 disabled:text-fg-muted disabled:no-underline" disabled={!ref.kind||!ref.uid} title={ref.scope?ref.scope+'/'+ref.name:ref.name} onClick={()=>onResource(ref)}>{label}</button>
 return <div className="flex min-h-0 min-w-0 flex-1 flex-col" data-rbac-workbench>
  <div className="shrink-0 space-y-2 border-b border-line px-3 py-2 text-xs">
   {!initialSubject&&<div className="flex flex-wrap items-center gap-2"><Select label={t('rbac.subject')} value={selectedKey} options={options} onChange={value=>choose(value==='self'?undefined:JSON.parse(value))}/><span className="text-fg-muted">{t('rbac.subjectHint')}</span></div>}
   <div className="flex flex-wrap items-center gap-2"><strong className="break-all">{snapshot?.principal||t('rbac.identityUnknown')}</strong><button className={button} disabled={busy} onClick={()=>{setBusy(true);setRevision(r=>r+1)}}>{busy?t('app.loading'):t('graph.refresh')}</button><span className="ml-auto text-fg-muted">{snapshot?t('timeline.snapshot',{time:date(snapshot.capturedAt)}):''}</span></div>
   {!!snapshot?.groups.length&&<p className="break-all text-fg-muted">{t('rbac.groups')}: {snapshot.groups.join(', ')}</p>}
   <p className="text-fg-muted">{t('rbac.declarations')}</p>
   {error!==null&&<p role="alert" className="text-danger">{errorDetail(error)}{snapshot?' · '+t('timeline.stale'):''}</p>}
   {!initialSubject&&<details><summary className="cursor-pointer text-fg-muted">{t('rbac.manual')}</summary><form className="mt-2 flex flex-wrap gap-2" onSubmit={e=>{e.preventDefault();if(manualNS&&manualName)choose({provider:target.provider,target:target.id,kind:'',scope:manualNS,name:manualName})}}><input className={input} aria-label={t('rbac.accountNamespace')} placeholder={t('rbac.accountNamespace')} value={manualNS} onChange={e=>setManualNS(e.target.value)}/><input className={input} aria-label={t('rbac.accountName')} placeholder={t('rbac.accountName')} value={manualName} onChange={e=>setManualName(e.target.value)}/><button className={button} disabled={!manualNS||!manualName}>{t('rbac.inspectAccount')}</button></form></details>}
  </div>
  <div className="max-h-[45%] shrink-0 overflow-auto border-b border-line px-3 py-2 text-xs">
   <h3 className="mb-2 font-semibold">{t('rbac.checkTitle')}</h3>
   <div className="grid grid-cols-2 gap-2 xl:grid-cols-3">
    <div><span>{t('rbac.verb')}</span><Select label={t('rbac.verb')} value={attrs.verb} options={['get','list','watch','create','update','patch','delete','deletecollection','bind','escalate','impersonate'].map(value=>({value,label:value}))} onChange={value=>change('verb',value)} className="mt-1 w-full"/></div>
    {(['group','resource','subresource','namespace','name'] as const).map(field=><label key={field}>{t(`rbac.${field}`)}<input className={input+' mt-1 w-full'} aria-label={t(`rbac.${field}`)} value={attrs[field]} disabled={field==='name'&&(attrs.verb==='deletecollection'||(attrs.verb==='create'&&!attrs.subresource))} onChange={e=>change(field,e.target.value)} placeholder={field==='group'?t('rbac.coreGroup'):field==='namespace'?t('rbac.allNamespaces'):undefined}/></label>)}
   </div>
   <p className="my-2 text-fg-muted">{t('rbac.nameHint')}</p>
   <div className="flex flex-wrap gap-2"><button className={button} disabled={busy||!snapshot?.principal||!attrs.resource} onClick={()=>setLocal({...attrs})}>{t('rbac.findGrant')}</button><button className={button} disabled={checking||!attrs.resource} onClick={()=>runCheck()}>{checking?t('app.loading'):t('rbac.checkMine')}</button>{failedRef&&<button className={button} disabled={checking} onClick={()=>runCheck(failedRef)}>{t('rbac.checkResource')} · {failedRef.kind}/{failedRef.name}</button>}</div>
   <p className="mt-2 text-fg-muted">{t('rbac.connectionOnly')}</p>
   {checkError!==null&&<p role="alert" className="mt-2 text-danger">{errorDetail(checkError)}</p>}
   {review&&<div role="status" className="mt-2 rounded-md border border-line p-2"><strong className={review.state==='allowed'?'text-success':review.state==='denied'?'text-danger':'text-warning'}>{t(`rbac.${review.state}`)}</strong><span className="ml-2 text-fg-muted">{date(review.checkedAt)}</span><p className="mt-1 break-all font-mono">{review.attributes.verb} {review.attributes.group||'core'}/{review.attributes.resource}{review.attributes.subresource?'/'+review.attributes.subresource:''} · {review.attributes.namespace||t('rbac.allNamespaces')}{review.attributes.name?' · '+review.attributes.name:''}</p>{review.reason&&<p className="mt-1 break-words">{review.reason}</p>}{review.evaluationError&&<p className="mt-1 text-warning">{review.evaluationError}</p>}<p className="mt-1 text-fg-muted">{t('rbac.verdictHint')}</p></div>}
   {matches&&<div role="status" className="mt-2 border-l-2 border-accent pl-2"><strong>{matches.length?t('rbac.matches',{count:matches.length}):t('rbac.noMatch')}</strong>{matches.map((g,i)=><p key={i}>{g.bindingKind}/{g.binding.name} → {g.roleKind}/{g.role.name}</p>)}<p className="text-fg-muted">{t('rbac.localHint')}</p></div>}
  </div>
  <div className="flex shrink-0 flex-wrap items-center gap-2 border-b border-line px-3 py-2 text-xs"><strong>{t('rbac.provenance')}</strong><span className="text-fg-muted">{t('rbac.counts',{count:snapshot?.grants.length??0})}</span><input className={input+' ml-auto'} aria-label={t('rbac.search')} placeholder={t('rbac.search')} value={filter} onChange={e=>setFilter(e.target.value)}/></div>
  <div ref={scroll} data-area="table" data-area-focus tabIndex={0} className="min-h-0 flex-1 overflow-auto outline-none focus-visible:ring-1 focus-visible:ring-accent" aria-label={t('rbac.provenance')}>
   {busy&&!snapshot&&<p role="status" className="p-3 text-xs text-fg-muted">{t('app.loading')}</p>}
   {!busy&&!rows.length&&<p role="status" className="p-3 text-xs text-fg-muted">{snapshot?.principal?t('rbac.empty'):t('rbac.identityUnknown')}</p>}
   <div className="relative w-full" style={{height:virtual.getTotalSize()}}>{virtual.getVirtualItems().map(item=>{
    const {grant,rule}=rows[item.index]
    return <article key={item.key} data-index={item.index} ref={virtual.measureElement} className="absolute left-0 top-0 w-full border-b border-line px-3 py-2 text-xs" style={{transform:`translateY(${item.start}px)`}} data-rbac-grant>
     <div className="flex flex-wrap items-center gap-2 font-semibold">{refButton(grant.binding,grant.bindingKind+'/'+grant.binding.name)}<span>→</span>{refButton(grant.role,grant.roleKind+'/'+grant.role.name)}<span className="ml-auto font-normal text-fg-muted">{grant.clusterWide?t('rbac.clusterWide'):grant.namespace}</span></div>
     <p className="mt-1 break-all text-fg-muted">{t('rbac.via')}: {grant.subjectKind} / {grant.subjectName}</p>
     {grant.error&&<p role="status" className="mt-1 text-warning">{t('rbac.roleError',{class:grant.error})}</p>}
     {grant.aggregated&&<p className="mt-1 text-fg-muted">{t('rbac.aggregated')}</p>}
     {rule&&<p className="mt-2 break-all rounded-sm bg-app px-2 py-1"><span className="font-mono">{rule.verbs.join(', ')} · {rule.resources.length?rule.apiGroups.map(g=>g||'core').join(', ')+' / '+rule.resources.join(', '):rule.nonResourceURLs.join(', ')}</span>{!!rule.resourceNames.length&&<span className="ml-2 text-fg-muted">{t('rbac.namedOnly')}: {rule.resourceNames.join(', ')}</span>}{!!rule.nonResourceURLs.length&&!grant.clusterWide&&<span className="ml-2 text-warning">{t('rbac.nonResourceIgnored')}</span>}</p>}
    </article>
   })}</div>
  </div>
  <footer className="shrink-0 border-t border-line px-3 py-2 text-xs text-fg-muted"><p>{t('rbac.coverage')}</p>{snapshot?.discovery!=='ready'&&snapshot&&<p className="text-warning">{t('graph.discovery')}</p>}{snapshot?.truncated&&<p role="status" className="text-warning">{t('rbac.truncated')}</p>}{!!snapshot?.problems.length&&<details className="text-warning"><summary>{t('graph.partial',{count:snapshot.problems.length})}</summary><ul className="max-h-24 overflow-auto">{snapshot.problems.map((p,i)=><li key={i}>{p.kind}{p.scope?' / '+p.scope:''} · {p.class}</li>)}</ul></details>}</footer>
 </div>
}
