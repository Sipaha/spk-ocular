import { useEffect, useId, useRef, useState, type KeyboardEvent } from 'react'
import { useStore as useModel } from 'zustand'
import { createPortal } from 'react-dom'
import type { Client } from '../api/client'
import type { Target } from '../api/types'
import { t } from '../i18n'
import { focusMark, restoreFocus } from '../shortcuts'
import { AppearanceIcon } from './ConnectionIcon'
import { appearanceModel, iconIds, paletteIds, useAppearance, type Appearance } from './appearance'

export function AppearanceDialog({client,target,onClose}:{client:Client;target:Target;onClose:()=>void}){
 const saved=useAppearance(client,target)
 const [choice,setDraft]=useState<Appearance|null>(null),draft=choice??saved
 const model=appearanceModel(client),failed=useModel(model.store,s=>!!s.errors[JSON.stringify([target.provider,target.id])])
 useEffect(()=>{void model.ensure(target)},[model,target])
 const [busy,setBusy]=useState(false),[error,setError]=useState(false)
 const [mark]=useState(focusMark),ref=useRef<HTMLElement>(null),id=useId()
 useEffect(()=>{ref.current?.querySelector<HTMLButtonElement>('[data-cancel]')?.focus();return()=>restoreFocus(mark)},[mark])
 const move=(e:KeyboardEvent<HTMLFieldSetElement>,kind:'icon'|'palette')=>{
  if(!draft)return
  if(!['ArrowLeft','ArrowRight','ArrowUp','ArrowDown','Home','End'].includes(e.key))return
  const buttons=Array.from(e.currentTarget.querySelectorAll<HTMLButtonElement>('button')),current=buttons.indexOf(e.target as HTMLButtonElement)
  if(current<0)return
  e.preventDefault();e.stopPropagation()
  const step=e.key==='ArrowUp'?-(kind==='icon'?8:1):e.key==='ArrowDown'?(kind==='icon'?8:1):e.key==='ArrowLeft'?-1:1
  const next=e.key==='Home'?0:e.key==='End'?buttons.length-1:(current+step+buttons.length)%buttons.length
  setDraft(kind==='icon'?{...draft,icon:iconIds[next]}:{...draft,palette:paletteIds[next]});buttons[next].focus()
 }
 const save=async()=>{if(!draft)return;setBusy(true);setError(false);try{await appearanceModel(client).save(target,draft);onClose()}catch{setError(true)}finally{setBusy(false)}}
 return createPortal(<div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-3" onMouseDown={e=>{if(e.target===e.currentTarget&&!busy)onClose()}}>
  <section ref={ref} tabIndex={-1} role="dialog" aria-modal="true" aria-labelledby={id} className="appearance-dialog max-h-[90vh] w-[min(480px,100%)] overflow-y-auto rounded-lg border border-line bg-panel p-4 shadow-2xl" onKeyDown={e=>{
   if(e.key==='Escape'){e.preventDefault();e.stopPropagation();if(!busy)onClose()}
   if(e.key==='Tab'){
    const stops=Array.from(ref.current?.querySelectorAll<HTMLElement>('button:not(:disabled)')??[]).filter(el=>el.tabIndex>=0),first=stops[0],last=stops.at(-1)
    if(!stops.length){e.preventDefault();ref.current?.focus();return}
    if(e.shiftKey&&document.activeElement===first){e.preventDefault();last?.focus()}
    if(!e.shiftKey&&document.activeElement===last){e.preventDefault();first?.focus()}
   }
  }}>
   <h2 id={id} className="font-semibold">{t('appearance.title')}</h2>
   {draft?<><div className="my-4 flex items-center gap-3"><AppearanceIcon value={draft} className="connection-avatar-large"/><span className="min-w-0 break-all">{target.title}</span></div>
   <fieldset disabled={busy} onKeyDown={e=>move(e,'icon')}><legend className="mb-2 text-xs text-fg-muted">{t('appearance.icon')}</legend>
    <div className="appearance-icons">{iconIds.map((icon,index)=><button key={icon} type="button" aria-label={t('appearance.iconNumber',{n:index+1})} title={t('appearance.iconNumber',{n:index+1})} tabIndex={draft.icon===icon?0:-1} aria-pressed={draft.icon===icon} onClick={()=>setDraft({...draft,icon})}><AppearanceIcon value={{...draft,icon}}/></button>)}</div>
   </fieldset>
   <fieldset disabled={busy} className="mt-4" onKeyDown={e=>move(e,'palette')}><legend className="mb-2 text-xs text-fg-muted">{t('appearance.palette')}</legend>
    <div className="appearance-palette">{paletteIds.map((palette,index)=><button key={palette} type="button" aria-label={t('appearance.paletteNumber',{n:index+1})} title={t('appearance.paletteNumber',{n:index+1})} tabIndex={draft.palette===palette?0:-1} aria-pressed={draft.palette===palette} onClick={()=>setDraft({...draft,palette})}><AppearanceIcon value={{...draft,palette}}/></button>)}</div>
   </fieldset>
   </>:!failed&&<div role="status" className="flex min-h-64 items-center justify-center text-sm text-fg-muted">{t('app.loading')}</div>}
   {(error||failed)&&<p role="alert" className="mt-3 text-sm text-danger">{t('appearance.failed')} <button type="button" className="underline" disabled={busy} onClick={()=>{if(error)void save();else void model.ensure(target,true)}}>{t('appearance.retry')}</button></p>}
   <footer className="mt-4 flex justify-end gap-2"><button data-cancel type="button" className="rounded border border-line px-3 py-1.5 hover:bg-hover" disabled={busy} onClick={onClose}>{t('action.cancel')}</button><button type="button" className="rounded bg-accent px-3 py-1.5 text-accent-fg disabled:opacity-40" disabled={busy||!draft} onClick={()=>void save()}>{t('appearance.save')}</button></footer>
  </section>
 </div>,document.body)
}
