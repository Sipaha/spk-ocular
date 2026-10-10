import {lazy,Suspense,useEffect,useRef,useState} from 'react'
import {createPortal} from 'react-dom'
import type {Client} from '../api/client'
import type {CompareRequest,ComparisonSide,ResourceComparison} from '../api/types'
import {EditDiff} from '../edit/EditDiff'
import {errorDetail} from '../errors'
import {getLanguage,t} from '../i18n'
import {refTitle} from '../refs'
import {focusMark,restoreFocus} from '../shortcuts'
import {useStore} from '../store'
import {clearComparison,closeComparison,selectionAvailable,useComparison} from './store'

const ComparisonDialog=lazy(async()=>({default:Dialog}))
const button='rounded border border-line px-2 py-1 text-xs hover:bg-hover disabled:opacity-50'

export default function Comparison({client}:{client:Client}){
 const baseline=useComparison(s=>s.baseline),other=useComparison(s=>s.other)
 const view=useStore(s=>s.view)
 if(!baseline)return null
 const available=selectionAvailable(baseline,view)
 return <>
  <aside aria-label={t('compare.baseline')} className="flex shrink-0 flex-wrap items-center gap-2 border-b border-line bg-panel px-3 py-1 text-xs" data-comparison-baseline>
   <strong>{t('compare.baseline')}</strong><span className="min-w-0 flex-1 truncate" title={`${baseline.ref.kind} · ${baseline.ref.scope??''}/${baseline.ref.name} · ${baseline.ref.uid}`}>{baseline.targetTitle} · {baseline.ref.scope?baseline.ref.scope+'/':''}{refTitle(baseline.ref)} · {baseline.ref.kind}</span>
   {!available&&<span className="text-warning">{t('compare.connectionChanged')}</span>}
   <span className="text-fg-muted">{t('compare.pickOther')}</span><button className={button} onClick={clearComparison}>{t('compare.clear')}</button>
  </aside>
  {other&&<Suspense fallback={<p role="status">{t('app.loading')}</p>}><ComparisonDialog client={client} request={{left:baseline,right:other}}/></Suspense>}
 </>
}
function Dialog({client,request}:{client:Client;request:CompareRequest}){
 const view=useStore(s=>s.view)
 const available=selectionAvailable(request.left,view)&&selectionAvailable(request.right,view)
 const [result,setResult]=useState<ResourceComparison|null>(null)
 const [error,setError]=useState('')
 const [refresh,setRefresh]=useState(0)
 const [full,setFull]=useState(false)
 const box=useRef<HTMLElement>(null)
 const [mark]=useState(focusMark)
 const left=request.left,right=request.right
 useEffect(()=>{box.current?.focus();return()=>restoreFocus(mark)},[mark])
 useEffect(()=>{
  if(!available)return
  const abort=new AbortController()
  void client.compareResources({left,right},abort.signal).then(r=>{if(!abort.signal.aborted)setResult(r)},e=>{if(!abort.signal.aborted)setError(errorDetail(e))})
  return()=>abort.abort()
 },[client,left,right,available,refresh])
 const validResult=available&&!error?result:null
 const before=validResult?(full?validResult.left.fullYAML:validResult.left.yaml):''
 const after=validResult?(full?validResult.right.fullYAML:validResult.right.yaml):''
 const omitted=validResult?[...new Set([...validResult.left.omitted,...validResult.right.omitted])]:[]
 return createPortal(<div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-3" onMouseDown={e=>{if(e.target===e.currentTarget)closeComparison()}}>
  <section ref={box} role="dialog" aria-modal="true" aria-label={t('compare.title')} tabIndex={-1} className="flex h-[min(900px,calc(100dvh-24px))] w-[min(1400px,calc(100vw-24px))] flex-col border border-line bg-panel outline-none" onKeyDown={e=>{
   if(e.key==='Escape'){e.preventDefault();e.stopPropagation();closeComparison()}
   if(e.key==='Tab'){
    const controls=[...(box.current?.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled),summary,[tabindex="0"]')??[])].filter(el=>el.getClientRects().length&&!el.closest('[inert]'))
    const first=controls[0],last=controls.at(-1)
    if(e.shiftKey&&(document.activeElement===first||document.activeElement===box.current)){e.preventDefault();last?.focus()}
    else if(!e.shiftKey&&(document.activeElement===last||document.activeElement===box.current)){e.preventDefault();first?.focus()}
   }
  }}>
   <header className="flex h-10 shrink-0 items-center gap-2 border-b border-line px-3"><strong className="text-sm">{t('compare.title')}</strong><span className="flex-1"/><button className={button} disabled={!available} onClick={()=>{setResult(null);setError('');setRefresh(n=>n+1)}}>{t('graph.refresh')}</button><button className="rounded px-2 text-lg hover:bg-hover" aria-label={t('drawer.close')} onClick={closeComparison}>×</button></header>
   <div className="grid shrink-0 grid-cols-2 gap-3 border-b border-line px-3 py-2 text-xs">
    <Side label={t('compare.left')} selection={left} side={validResult?.left}/><Side label={t('compare.right')} selection={right} side={validResult?.right}/>
   </div>
   <div className="shrink-0 space-y-1 border-b border-line px-3 py-2 text-xs text-fg-muted">
    <p>{t('compare.snapshotHint')}</p><p>{t('compare.normalizationHint')}</p>
    {left.ref.provider!==right.ref.provider||left.ref.kind!==right.ref.kind?<p className="text-warning">{t('compare.differentKinds')}</p>:null}
    <label className="flex items-center gap-2 text-fg"><input type="checkbox" checked={full} onChange={e=>setFull(e.target.checked)}/>{t('compare.serviceFields')}</label>
    {omitted.length>0&&!full&&<details><summary className="cursor-pointer">{t('compare.omitted',{count:omitted.length})}</summary><p className="break-words font-mono">{omitted.join(', ')}</p></details>}
    {(validResult?.left.valuesExcluded||validResult?.right.valuesExcluded)&&<p className="text-warning">{t('compare.secretExcluded')}</p>}
   </div>
   <div className="min-h-0 flex-1 overflow-auto p-3" tabIndex={0} data-comparison-content>
    {!available?<p role="alert" className="text-warning">{t('compare.connectionChanged')}</p>:error?<p role="alert" className="text-danger">{error}</p>:!validResult?<p role="status" className="text-fg-muted">{t('app.loading')}</p>:before===after?<p className="text-fg-muted">{t('compare.identical')}</p>:<EditDiff key={`${refresh}:${full}`} before={before} after={after} label={t('compare.diff')}/>}
   </div>
  </section>
 </div>,document.body)
}
function Side({label,selection,side}:{label:string;selection:CompareRequest['left'];side?:ComparisonSide}){
 return <div className="min-w-0"><strong>{label}</strong><p className="truncate" title={selection.targetTitle}>{selection.targetTitle} · {selection.ref.provider}</p><p className="break-words" title={selection.ref.uid}>{selection.ref.scope?selection.ref.scope+'/':''}{refTitle(selection.ref)} · {selection.ref.kind}</p>{side&&<p className="text-fg-subtle">{t('compare.captured',{time:new Date(side.capturedAt).toLocaleString(getLanguage())})}</p>}</div>
}
