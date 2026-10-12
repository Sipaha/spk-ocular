import {useRef} from 'react'
import {createPortal} from 'react-dom'
import type {Client} from '../api/client'
import type {Ref,ScopeSel} from '../api/types'
import {t} from '../i18n'
import {ModalCloseButton,useModalFocus} from '../components/modal'
import RbacWorkbench from './RbacWorkbench'
export default function RbacDialog({client,target,subject,failedRef,onResource,onClose}:{client:Client;target:{provider:string;id:string};subject?:Ref;failedRef?:Ref;onResource:(ref:Ref)=>void;onClose:()=>void}){
 const box=useRef<HTMLElement>(null)
 const modalKey=useModalFocus(box,onClose)
 const ref=subject??failedRef
 const scope:ScopeSel=ref?.scope?{mode:'one',name:ref.scope}:{mode:'some',names:[]}
 return createPortal(<div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-3" onMouseDown={e=>{if(e.target===e.currentTarget)onClose()}}><section ref={box} role="dialog" aria-modal="true" aria-label={t('rbac.title')} tabIndex={-1} className="flex h-[min(850px,calc(100dvh-24px))] w-[min(1100px,calc(100vw-24px))] flex-col border border-line bg-panel outline-none" onKeyDown={modalKey}><header className="flex h-10 shrink-0 items-center gap-2 border-b border-line px-3"><strong className="text-sm">{t('rbac.title')}</strong><span className="min-w-0 flex-1 truncate text-xs text-fg-muted">{ref?.scope}{ref?.scope?'/':''}{ref?.title??ref?.name}</span><ModalCloseButton label={t('drawer.close')} onClick={onClose}/></header><RbacWorkbench client={client} target={target} scope={scope} initialSubject={subject} failedRef={failedRef} onResource={onResource}/></section></div>,document.body)
}
