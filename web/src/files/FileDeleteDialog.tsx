import {useRef,useState,useLayoutEffect} from 'react'
import {t} from '../i18n'
import {useModalFocus} from '../components/modal'

export function FileDeleteDialog({path,onDelete,onClose}:{path:string;onDelete:()=>Promise<void>;onClose:()=>void}){
 const box=useRef<HTMLDivElement>(null),cancel=useRef<HTMLButtonElement>(null)
 const [busy,setBusy]=useState(false),[error,setError]=useState('')
 const close=()=>{if(!busy)onClose()}
 const key=useModalFocus(box,close,cancel)
 useLayoutEffect(()=>{if(error)cancel.current?.focus()},[error])
 const remove=async()=>{if(busy||error)return;setBusy(true);try{await onDelete()}catch(e){setError(e instanceof Error?e.message:String(e));setBusy(false)}}
 return <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-3" onMouseDown={e=>{if(e.target===e.currentTarget)close()}}>
  <div ref={box} role="alertdialog" aria-modal="true" aria-label={t('files.delete')} tabIndex={-1} className="w-[min(480px,calc(100vw-24px))] border border-line bg-panel p-4" onKeyDown={key}>
   <h2 className="font-semibold">{t('files.delete')}</h2>
   <p className="mt-3 text-sm text-fg-muted">{t('files.deleteWarning')}</p>
   <p className="mt-3 break-all font-mono text-sm">{path}</p>
   {error&&<p role="alert" className="mt-3 break-words whitespace-pre-wrap text-sm text-danger">{error}</p>}
   <div className="mt-4 flex justify-end gap-2">
    <button ref={cancel} type="button" disabled={busy} className="rounded border border-line px-3 py-1 hover:bg-hover" onClick={close}>{t('action.cancel')}</button>
    <button type="button" disabled={busy||!!error} className="rounded border border-danger/50 bg-danger/10 px-3 py-1 text-danger hover:bg-danger/20 disabled:opacity-50" onClick={()=>void remove()}>{busy?t('app.loading'):t('files.delete')}</button>
   </div>
  </div>
 </div>
}
