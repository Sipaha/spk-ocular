import {useMemo,useRef,useState} from 'react'
import {createPortal} from 'react-dom'
import type {Column,Query,Row,SortSpec,Target,ViewPreset,ViewPresetRegistry} from '../api/types'
import {useModalFocus} from '../components/modal'
import {useView} from '../views/useView'
import type {ViewHub} from '../views/viewSync'
import {t} from '../i18n'
import {columnLabel} from '../presentation'
import {columnsFor,labelKeys,labelState,validLabelKeys} from './model'

export function PresetDialog({target,hub,query,base,registry,filter,sort,busy,onSave,onClose}: {
  target: Pick<Target,'provider'|'id'>;hub: ViewHub;query: Query;base: Column[];registry: ViewPresetRegistry;filter:string;sort?:SortSpec;busy:boolean
  onSave:(preset:ViewPreset)=>Promise<boolean>;onClose:()=>void
}) {
  const current=registry.presets.find(p=>p.name===registry.active)
  const [name,setName]=useState(current?.name ?? '')
  const [keys,setKeys]=useState((current?.labels ?? []).join('\n'))
  const [hidden,setHidden]=useState(current?.hidden ?? [])
  const [order,setOrder]=useState(current?.order ?? [])
  const [error,setError]=useState(false)
  const box=useRef<HTMLFormElement>(null)
  const modalKey=useModalFocus(box,()=>{if(!busy)onClose()})
  const labels=useMemo(()=>labelKeys(keys),[keys])
  const valid=validLabelKeys(labels)
  const queryKey=JSON.stringify({...query,labelKeys:valid&&labels.length?labels:undefined})
  const previewQuery=useMemo(()=>JSON.parse(queryKey) as Query,[queryKey])
  const preview=useView(hub,target.provider,target.id,previewQuery)
  const preset:ViewPreset={name,labels,hidden,order,filter,sort}
  const cols=columnsFor(base,valid?preset:{...preset,labels:[]})
  const move=(id:string,delta:number)=>{const ids=cols.map(c=>c.id),i=ids.indexOf(id),j=i+delta;if(j<0||j>=ids.length)return;[ids[i],ids[j]]=[ids[j],ids[i]];setOrder(ids)}
  const validName=new TextEncoder().encode(name).length<=64 && name.trim()!=='' && !/\p{Cc}/u.test(name)
  const full=registry.presets.length>=4&&!registry.presets.some(p=>p.name===name)
  const sample=useMemo(()=>preview.rows.slice().sort((a,b)=>a.ref.name.localeCompare(b.ref.name)).slice(0,3),[preview.rows])
  const previewText=(row:Row,key:string)=>{const state=labelState(row,key);return state==='value'?row.labels?.[key]?.value || t('presets.empty'):t(`presets.${state}`)}
  return createPortal(<div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-3" onMouseDown={e=>{if(e.target===e.currentTarget&&!busy)onClose()}}>
    <form ref={box} role="dialog" aria-modal="true" aria-label={t('presets.title')} tabIndex={-1} onKeyDown={modalKey}
      onSubmit={e=>{e.preventDefault();if(valid&&validName&&!full)void onSave({...preset,hidden:hidden.filter(id=>!id.startsWith('metadata.labels:')||cols.some(c=>c.id===id)),order:[...cols.map(c=>c.id),...order.filter(id=>!id.startsWith('metadata.labels:')&&!cols.some(c=>c.id===id))],sort:sort?.column.startsWith('metadata.labels:')&&!cols.some(c=>c.id===sort.column)?{column:'name'}:sort}).then(ok=>{if(ok)onClose();else setError(true)})}}
      className="flex max-h-[90vh] w-[min(680px,100%)] min-w-0 flex-col gap-3 overflow-y-auto rounded-lg border border-line bg-panel p-4 outline-none">
      <h2 className="font-semibold">{t('presets.title')}</h2>
      <p className="text-xs text-fg-muted">{t('presets.help')}</p>
      <label className="flex flex-col gap-1 text-xs">{t('presets.name')}<input autoFocus aria-label={t('presets.name')} value={name} maxLength={64} onChange={e=>setName(e.target.value)} className="rounded border border-line bg-app px-2 py-1"/></label>
      <label className="flex flex-col gap-1 text-xs">{t('presets.keys')}<textarea aria-label={t('presets.keys')} value={keys} onChange={e=>setKeys(e.target.value)} rows={3} maxLength={2031} spellCheck={false} placeholder="app.kubernetes.io/version" className="rounded border border-line bg-app px-2 py-1 font-mono"/></label>
      {!validName&&name!==''&&<p role="alert" className="text-xs text-danger">{t('presets.nameError')}</p>}
      {!valid&&<p role="alert" className="text-xs text-danger">{t('presets.invalid')}</p>}
      {full&&<p role="alert" className="text-xs text-danger">{t('presets.full')}</p>}
      <fieldset className="min-w-0"><legend className="mb-1 text-xs text-fg-muted">{t('presets.columns')}</legend>
        <div className="max-h-44 overflow-y-auto rounded border border-line">{cols.map((c,i)=><div key={c.id} className="flex min-w-0 items-center gap-2 border-b border-line px-2 py-1 text-xs last:border-0">
          <label className="flex min-w-0 flex-1 items-center gap-2"><input type="checkbox" checked={!hidden.includes(c.id)||c.id==='name'} disabled={c.id==='name'} onChange={e=>setHidden(h=>e.target.checked?h.filter(id=>id!==c.id):[...h,c.id])}/><span className="min-w-0 [overflow-wrap:anywhere]">{c.id.startsWith('metadata.labels:')?c.title:columnLabel(c.title)}</span></label>
          <button type="button" aria-label={t('presets.up',{name:c.title})} disabled={i===0} onClick={()=>move(c.id,-1)} className="rounded px-2 py-1 hover:bg-hover disabled:opacity-30">↑</button>
          <button type="button" aria-label={t('presets.down',{name:c.title})} disabled={i===cols.length-1} onClick={()=>move(c.id,1)} className="rounded px-2 py-1 hover:bg-hover disabled:opacity-30">↓</button>
        </div>)}</div>
      </fieldset>
      <section aria-label={t('presets.preview')} className="min-w-0 text-xs"><h3 className="mb-1 text-fg-muted">{t('presets.preview')}</h3>
        <p className="mb-1 text-fg-subtle">{t('presets.capture')}</p>
        {preview.status.state!=='ready'&&<p role="status">{t(preview.status.state==='loading'?'app.loading':'presets.unavailable')}</p>}
        {valid&&labels.map(key=><div key={key} className="border-t border-line py-1 [overflow-wrap:anywhere]"><span className="font-mono">{key}</span><span className="text-fg-muted"> · {sample.length?sample.map(row=>`${row.ref.name}: ${previewText(row,key)}`).join(' · '):t('presets.noRows')}</span></div>)}
      </section>
      {error&&<p role="alert" className="text-xs text-danger">{t('presets.saveError')}</p>}
      <div className="flex flex-wrap justify-end gap-2 text-sm"><button type="button" disabled={busy} onClick={onClose} className="rounded px-3 py-1 hover:bg-hover">{t('term.cancel')}</button><button type="submit" disabled={busy||!valid||!validName||full} className="rounded bg-accent px-3 py-1 text-accent-fg disabled:opacity-50">{t('presets.save')}</button></div>
    </form>
  </div>,document.body)
}
