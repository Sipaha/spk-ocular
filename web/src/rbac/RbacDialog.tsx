import {useEffect,useRef,useState} from 'react'
import {createPortal} from 'react-dom'
import type {Client} from '../api/client'
import type {Ref,ScopeSel} from '../api/types'
import {t} from '../i18n'
import {focusMark,restoreFocus} from '../shortcuts'
import RbacWorkbench from './RbacWorkbench'
export default function RbacDialog({client,target,subject,failedRef,onResource,onClose}:{client:Client;target:{provider:string;id:string};subject?:Ref;failedRef?:Ref;onResource:(ref:Ref)=>void;onClose:()=>void}){
 const box=useRef<HTMLElement>(null)
 const [mark]=useState(focusMark)
 const ref=subject??failedRef
 const scope:ScopeSel=ref?.scope?{mode:'one',name:ref.scope}:{mode:'some',names:[]}
 useEffect(()=>{box.current?.focus();return()=>restoreFocus(mark)},[mark])
 return createPortal(<div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-3" onMouseDown={e=>{if(e.target===e.currentTarget)onClose()}}><section ref={box} role="dialog" aria-modal="true" aria-label={t('rbac.title')} tabIndex={-1} className="flex h-[min(850px,calc(100dvh-24px))] w-[min(1100px,calc(100vw-24px))] flex-col border border-line bg-panel outline-none" onKeyDown={e=>{
  if(e.key==='Escape'){e.preventDefault();e.stopPropagation();onClose()}
  if(e.key==='Tab'){
   const controls=[...(box.current?.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled),summary,[tabindex="0"]')??[])].filter(el=>el.getClientRects().length&&!el.closest('[inert]'))
   const first=controls[0],last=controls.at(-1)
   if(e.shiftKey&&(document.activeElement===first||document.activeElement===box.current)){e.preventDefault();last?.focus()}
   else if(!e.shiftKey&&(document.activeElement===last||document.activeElement===box.current)){e.preventDefault();first?.focus()}
  }
 }}><header className="flex h-10 shrink-0 items-center gap-2 border-b border-line px-3"><strong className="text-sm">{t('rbac.title')}</strong><span className="min-w-0 flex-1 truncate text-xs text-fg-muted">{ref?.scope}{ref?.scope?'/':''}{ref?.title??ref?.name}</span><button className="rounded px-2 text-lg hover:bg-hover" aria-label={t('drawer.close')} onClick={onClose}>×</button></header><RbacWorkbench client={client} target={target} scope={scope} initialSubject={subject} failedRef={failedRef} onResource={onResource}/></section></div>,document.body)
}
