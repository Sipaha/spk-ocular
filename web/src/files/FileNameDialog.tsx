import {useLayoutEffect,useRef,useState} from 'react'
import {t} from '../i18n'
import {useModalFocus} from '../components/modal'

const button='h-[28px] rounded border border-line px-3 text-sm hover:bg-hover disabled:opacity-50'

export function FileNameDialog({directory,initialName='',mode='create',onSubmit,onClose}:{directory:string;initialName?:string;mode?:'create'|'rename';onSubmit:(name:string)=>Promise<void>;onClose:()=>void}){
 const [name,setName]=useState(initialName),[busy,setBusy]=useState(false),[error,setError]=useState('')
 const box=useRef<HTMLFormElement>(null),input=useRef<HTMLInputElement>(null)
 useLayoutEffect(()=>{if(error)input.current?.focus()},[error])
 useLayoutEffect(()=>{if(initialName)input.current?.select()},[initialName])
 const title=t(mode==='create'?'files.create':'files.rename')
 const close=()=>{if(!busy)onClose()}
 const modalKey=useModalFocus(box,close,input)
 const valid=name.length>0&&name!=='.'&&name!=='..'&&!name.includes('/')&&!name.includes('\0')&&new TextEncoder().encode(name).length<=255
 const create=async()=>{
  if(!valid||busy||(mode==='rename'&&name===initialName))return
  setBusy(true);setError('')
  try{await onSubmit(name)}catch(e){setError(e instanceof Error?e.message:String(e));setBusy(false)}
 }
 return <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-3" onMouseDown={e=>{if(e.target===e.currentTarget)close()}}>
  <form ref={box} role="dialog" aria-modal="true" aria-label={title} tabIndex={-1} className="w-[min(440px,calc(100vw-24px))] border border-line bg-panel p-4" onKeyDown={modalKey} onSubmit={e=>{e.preventDefault();void create()}}>
   <h2 className="mb-3 font-semibold">{title}</h2>
   <p className="mb-3 break-all font-mono text-sm text-fg-muted">{directory}</p>
   <label className="flex flex-col gap-1 text-sm">{t('files.name')}<input ref={input} disabled={busy} value={name} onChange={e=>{setName(e.target.value);setError('')}} className="h-[30px] border border-line bg-app px-2 font-mono"/></label>
   {name&&!valid&&<p className="mt-2 text-sm text-warning">{t('files.nameInvalid')}</p>}
   {error&&<p role="alert" className="mt-2 whitespace-pre-wrap break-words text-sm text-danger">{error}</p>}
   <div className="mt-4 flex justify-end gap-2"><button type="button" disabled={busy} className={button} onClick={close}>{t('action.cancel')}</button><button type="submit" disabled={!valid||busy||(mode==='rename'&&name===initialName)} className={button+' bg-accent text-accent-fg'}>{busy?t('app.loading'):title}</button></div>
  </form>
 </div>
}
