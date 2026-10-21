import {useCallback,useContext,useState} from 'react'
import type {Client} from '../api/client'
import type {Ref} from '../api/types'
import type {HistorySource,HistoryMetric,HistoryRequest} from './types'
import {ViewConnectionContext} from '../views/useView'
import {useSnapshotRequest} from '../views/useSnapshotRequest'
import {t} from '../i18n'
import {errorDetail} from '../errors'
import {DateText} from '../components/DateText'
import {Select} from '../components/Select'
const button='rounded border border-line px-2 py-1 text-xs hover:bg-hover disabled:opacity-40'
const input='min-w-0 w-full rounded border border-line bg-bg px-2 py-1 text-xs'
const identity=(r:Ref)=>JSON.stringify([r.provider,r.target,r.kind,r.scope??'',r.name,r.uid])
export function ResourceMetricsHistory(props:{client:Client;subject:Ref;snapshotKey:unknown;revision?:string}){
 const connection=useContext(ViewConnectionContext)
 const [parent,setParent]=useState({snapshot:props.snapshotKey,client:props.client,epoch:0})
 const epoch=parent.snapshot===props.snapshotKey&&parent.client===props.client?parent.epoch:parent.epoch+1
 if(parent.snapshot!==props.snapshotKey||parent.client!==props.client)setParent({snapshot:props.snapshotKey,client:props.client,epoch})
 return <Panel key={JSON.stringify([identity(props.subject),connection,epoch,props.revision])} {...props}/>
}
function Panel({client,subject}:{client:Client;subject:Ref}){
 const [source,setSource]=useState<HistorySource>({url:'',token:'',dedicated:false,clusterLabel:'cluster',clusterValue:''})
 const [window,setWindow]=useState('1h'),[admitted,setAdmitted]=useState<HistoryRequest|null>(null)
 const refJSON=JSON.stringify(subject)
 const change=(patch:Partial<HistorySource>)=>{setAdmitted(null);setSource(s=>({...s,...patch}))}
 const load=useCallback(async(signal:AbortSignal)=>{
  if(!admitted)throw new Error(t('history.unavailable'))
  const snapshot=await client.resourceMetricsHistory(admitted,signal),ref:Ref=JSON.parse(refJSON)
  if(identity(snapshot.ref)!==identity(ref)||!snapshot.resourceVersion)throw new Error(t('workspace.replaced'))
  return snapshot
 },[client,admitted,refJSON])
 const request=useSnapshotRequest(load,identity(subject),{enabled:admitted!==null})
 const snapshot=admitted&&!request.busy&&!request.cancelled&&!request.error?request.value:null
 return <section data-metrics-history aria-label={t('history.title')} className="min-w-0 space-y-2 wrap-anywhere">
  <h3 className="text-sm font-semibold">{t('history.title')}</h3>
  <p className="text-xs text-fg-muted">{t('history.notice')}</p>
  <div className="grid min-w-0 gap-2 text-xs">
   <label>{t('history.source')}<input className={input} value={source.url} autoComplete="off" spellCheck={false} maxLength={2048} placeholder="https://prometheus.example" onChange={e=>change({url:e.target.value,token:''})}/></label>
   <label>{t('history.token')}<input className={input} type="password" maxLength={4096} value={source.token} autoComplete="off" onChange={e=>change({token:e.target.value})}/></label>
   <label className="flex items-start gap-2"><input type="checkbox" checked={source.dedicated} onChange={e=>change({dedicated:e.target.checked,clusterLabel:e.target.checked?'':'cluster',clusterValue:''})}/>{t('history.dedicated')}</label>
   {!source.dedicated&&<div className="grid min-w-0 grid-cols-2 gap-2"><label>{t('history.clusterLabel')}<input className={input} value={source.clusterLabel} maxLength={64} autoComplete="off" onChange={e=>change({clusterLabel:e.target.value})}/></label><label>{t('history.clusterValue')}<input className={input} value={source.clusterValue} maxLength={256} autoComplete="off" onChange={e=>change({clusterValue:e.target.value})}/></label></div>}
   <Select label={t('history.window')} value={window} options={['1h','6h','24h'].map(value=>({value,label:value}))} onChange={value=>{setAdmitted(null);setWindow(value)}}/>
  </div>
  <button type="button" className={button} disabled={request.busy||!source.url||(!source.dedicated&&(!source.clusterLabel||!source.clusterValue))} onClick={()=>admitted?request.refresh():setAdmitted({ref:subject,source:{...source},window})}>{request.busy?t('app.loading'):t('history.load')}</button>
  {request.busy&&<button type="button" className={button+' ml-2'} onClick={request.cancel}>{t('view.loadCancel')}</button>}
  {request.error!=null&&<p role="alert" className="text-xs text-danger">{errorDetail(request.error)}</p>}
  {request.cancelled&&<p className="text-xs text-fg-muted">{t('view.loadCancelled')}</p>}
  {snapshot&&<div className="min-w-0 space-y-3 text-xs">
   <p className="break-all">{snapshot.source} · <code>{snapshot.ref.uid}</code> · resourceVersion <code>{snapshot.resourceVersion}</code></p>
   <p>{t('history.asserted')}</p>
   {snapshot.start<=snapshot.end&&<p><DateText value={new Date(snapshot.start*1000).toISOString()}/> — <DateText value={new Date(snapshot.end*1000).toISOString()}/> · {t('history.step')}: {snapshot.step}s</p>}
   {snapshot.start!==snapshot.requestedStart&&<p className="text-fg-muted">{t('history.clipped')}</p>}
   <p className="text-fg-muted">{t('history.coverageNotice')}</p>
   {snapshot.metrics.length===0&&<p>{t('history.unavailable')}</p>}
   {snapshot.metrics.map(metric=><Chart key={metric.name} metric={metric} expected={Math.max(0,Math.floor((snapshot.end-snapshot.start)/snapshot.step)+1)}/>)}
   <h4 className="font-semibold">{t('history.current')}</h4>
   <p className="text-fg-muted">{t('history.currentNotice')}</p>
   {snapshot.declarations.map(d=><div key={d.container} className="min-w-0 border-t border-line pt-2"><p className="font-mono">{d.container}</p><p>CPU · {t('history.requests')}: {format(d.cpuRequest)} · {t('history.limits')}: {format(d.cpuLimit)}</p><p>RAM · {t('history.requests')}: {format(d.memoryRequest)} B · {t('history.limits')}: {format(d.memoryLimit)} B</p></div>)}
  </div>}
 </section>
}
const format=(n:number|null)=>n===null?'—':n.toLocaleString(undefined,{maximumFractionDigits:9})
function Chart({metric,expected}:{metric:HistoryMetric;expected:number}){
 const title=t(metric.name==='cpu'?'history.cpu':metric.name==='memory'?'history.memory':'history.throttling')
 const maximum=Math.max(metric.name==='throttling'?1:0,...metric.series.flatMap(s=>s.values.filter((n):n is number=>n!==null)),1)
 const paths=metric.series.map(series=>{
  let path='',active=false
  series.values.forEach((n,i)=>{if(n===null){active=false;return};const x=20+(i/Math.max(1,expected-1))*600,y=125-n/maximum*110;path+=`${active?'L':'M'}${x.toFixed(2)},${y.toFixed(2)} ${active?'':'l0.01,0 '}`;active=true})
  return path
 })
 const colors=['var(--color-accent)','var(--color-warning)','var(--color-success)']
 return <article data-history-metric={metric.name} className="min-w-0 space-y-1 border-t border-line pt-2">
  <h4 className="font-semibold">{title}</h4>
  <p className={metric.state==='available'?'text-fg-muted':'text-warning'}>{metric.state==='forbidden'?t('history.forbidden'):metric.state==='limited'?t('history.limited'):metric.state==='unavailable'?t('history.unavailable'):metric.state==='partial'?t('history.partial'):t('history.evaluated')}</p>
  {metric.series.length>0&&<><p className="break-all font-mono text-fg-muted">{format(maximum)}</p><svg role="img" aria-label={title} viewBox="0 0 640 150" className="block h-32 w-full min-w-0" preserveAspectRatio="none">
   <path d="M20,15 V125 H620" fill="none" stroke="currentColor" vectorEffect="non-scaling-stroke" opacity=".35"/>
   {paths.map((path,i)=><path key={i} d={path} fill="none" stroke={colors[i%colors.length]} vectorEffect="non-scaling-stroke" strokeLinecap="round" strokeWidth="2"/>)}
  </svg><p className="font-mono text-fg-muted">0</p></>}
  {(metric.missingContainers?.length??0)>0&&<p className="text-warning">{t('history.missingContainers')}: {metric.missingContainers?.join(', ')}</p>}
  {metric.series.map((s,i)=><p key={s.incarnation+s.container} className="break-all"><span aria-hidden="true" style={{color:colors[i%colors.length]}}>● </span><code>{s.container}</code> · {t('history.incarnation')} {i+1} · {t('history.points')}: {s.values.filter(n=>n!==null).length}/{expected}</p>)}
 </article>
}
