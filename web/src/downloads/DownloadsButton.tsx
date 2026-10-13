import {useEffect,useId,useLayoutEffect,useRef,useState} from 'react'
import {createPortal} from 'react-dom'
import {getLanguage,t} from '../i18n'
import {isDesktop} from '../api/client'
import {copyValue} from '../values/copy'
import {ActionIcon,FileIcon,FolderIcon,CloseIcon,WindowActionIcon} from '../components/icons'
import {safeDownloadFailure} from './failures'
import {downloadSize,downloadTime,downloadType} from './presentation'
import {ActionIconButton} from '../components/ActionIconButton'
import {ModalCloseButton,useModalFocus} from '../components/modal'
import {cancelDownloadDelete,deleteDownloadFile,dismissDownload,prepareDownloadDelete,type DownloadDeletePreview,clearDownloadHistory,isDownloadUnseen,markDownloadsViewed,openDownload,reloadDownloads,useDownloads,watchDownloads,type DownloadRecord} from './store'

export function DownloadsButton() {
 useEffect(watchDownloads,[])
 const [open,setOpen]=useState(false)
 const active=useDownloads(s=>s.records.filter(r=>r.status==='active').length)
 const unseen=useDownloads(s=>s.records.filter(r=>isDownloadUnseen(r,s.viewed)).length)
 const description=useId()
 const status=[active?t('downloads.activeCount',{n:active}):'',unseen?t('downloads.unseenCount',{n:unseen}):''].filter(Boolean).join(' · ')

 return <>
  <button type="button" className="help-trigger downloads-trigger relative" data-active={active>0} data-unseen={unseen>0} aria-label={t('downloads.title')} aria-describedby={status?description:undefined} title={[t('downloads.title'),status].filter(Boolean).join(' · ')} aria-haspopup="dialog" aria-expanded={open} onClick={()=>setOpen(!open)}>
   <ActionIcon name="download" className="h-4 w-4"/>
   {active>0&&<span className="downloads-trigger-track" aria-hidden="true"><span className="downloads-trigger-runner"/></span>}
   {unseen>0&&<span className="downloads-unseen-dot" aria-hidden="true"/>}
   {status&&<span id={description} className="sr-only">{status}</span>}
  </button>
  {open&&<DownloadsPanel onClose={()=>setOpen(false)}/>}
 </>
}

function DownloadsPanel({onClose}:{onClose:()=>void}) {
 const box=useRef<HTMLElement>(null),close=useRef<HTMLButtonElement>(null),heading=useId()
 const {records,storageError,error}=useDownloads()
 const [actionError,setActionError]=useState(''),[copied,setCopied]=useState(''),[busy,setBusy]=useState('')
 const [deleting,setDeleting]=useState<(DownloadDeletePreview&{id:string})|null>(null)
 useEffect(()=>{if(!deleting)markDownloadsViewed(records)},[records,deleting])
 const [openedAt]=useState(()=>Date.now())
 useEffect(()=>()=>{if(deleting)void cancelDownloadDelete(deleting.id,deleting.token).catch(()=>{})},[deleting])
 const pending=useRef(false)
 const alive=useRef(true)
 useEffect(()=>{alive.current=true;return()=>{alive.current=false}},[])
 const act=async(id:string,fn:()=>Promise<unknown>)=>{
  if(pending.current)return
  pending.current=true
  setBusy(id);setActionError('');setCopied('')
  try{await fn()}catch{if(alive.current)setActionError(t('downloads.actionFailed'))}
  finally{pending.current=false;if(alive.current)setBusy('')}
 }
 const copy=async(record:DownloadRecord)=>{
  if(!record.path)return
  const outcome=await copyValue({read:async()=>record.path!,valid:()=>alive.current&&useDownloads.getState().records.some(r=>r.id===record.id&&r.status==='complete'&&r.path===record.path)})
  if(alive.current&&outcome==='copied')setCopied(record.id)
 }
 const prepare=async(record:DownloadRecord)=>{
  const preview=await prepareDownloadDelete(record.id)
  if(alive.current)setDeleting({...preview,id:record.id})
  else await cancelDownloadDelete(record.id,preview.token)
 }
 const cancelDelete=()=>{
  if(deleting)void cancelDownloadDelete(deleting.id,deleting.token).catch(()=>{})
  setDeleting(null);close.current?.focus()
 }
 const modalKey=useModalFocus(box,()=>{if(deleting)cancelDelete();else onClose()},close)
 const closePanel=()=>{cancelDelete();onClose()}
 const date=(value:number)=>new Date(value).toLocaleString(getLanguage())
 return createPortal(<div className="fixed inset-0 z-50" onMouseDown={event=>{if(event.target===event.currentTarget){event.preventDefault();closePanel()}}}>
  <section ref={box} role="dialog" aria-modal="true" aria-labelledby={heading} onKeyDown={modalKey} className="absolute right-3 top-12 flex max-h-[calc(100vh-64px)] w-[min(480px,calc(100vw-24px))] min-w-0 flex-col rounded-md border border-fg-subtle/70 bg-panel text-sm shadow-floating" data-downloads-panel>
   <header className="flex shrink-0 items-center gap-3 border-b border-line px-3 py-2">
    <h2 id={heading} className="min-w-0 flex-1 font-semibold">{t('downloads.title')}</h2>
    <button type="button" className="text-xs text-fg-muted enabled:hover:text-fg disabled:cursor-default disabled:opacity-40" disabled={!!deleting||!!busy||!records.some(r=>r.status!=='active')} onClick={()=>void act('clear',clearDownloadHistory)}>{t('downloads.clear')}</button>
    <ModalCloseButton ref={close} label={t('action.close')} onClick={closePanel}/>
   </header>
   {deleting?<DeleteConfirmation preview={deleting} busy={!!busy} onCancel={cancelDelete} onDelete={()=>void act(deleting.id,async()=>{try{await deleteDownloadFile(deleting.id,deleting.token)}finally{if(alive.current){setDeleting(null);close.current?.focus()}}})}/>:
   <div className="min-h-0 overflow-y-auto">
    {records.length===0&&<p className="p-5 text-fg-muted">{t('downloads.empty')}</p>}
    <ul>{records.map(record=>{
     const timestamp=record.endedAt??record.startedAt
     const age=downloadTime(timestamp,openedAt)
     return <li key={record.id} className="border-b border-line px-3 py-2 last:border-0" data-download-id={record.id}>
      <div className="flex items-center gap-2.5">
       <DownloadItemIcon record={record}/>
       <div className="min-w-0 flex-1">
        <p className="truncate font-medium" title={record.name}>{record.name}</p>
        <div className="flex flex-wrap items-center gap-x-1 text-xs text-fg-muted">
         <span title={date(timestamp)}>{downloadSize(record.bytes)}</span>
         {age&&<><span aria-hidden="true">·</span><time dateTime={new Date(timestamp).toISOString()} title={date(timestamp)}>{age}</time></>}
         {record.status!=='complete'&&<><span aria-hidden="true">·</span><span className={record.status==='failed'?'text-danger':''}>{t(`downloads.status.${record.status}`)}</span></>}
         {record.status==='complete'&&<span className="sr-only">{t('downloads.status.complete')}</span>}
        </div>
       </div>
       <div className="flex shrink-0 gap-0.5">
        {record.status==='complete'&&<>
         <ActionIconButton label={t('downloads.open')} description={t('downloads.open')} disabled={!record.path||!!busy} onClick={()=>void act(record.id,()=>openDownload(record.id))}><WindowActionIcon className="h-4 w-4"/></ActionIconButton>
         <ActionIconButton label={t('downloads.folder')} description={t('downloads.folder')} disabled={!record.path||!!busy} onClick={()=>void act(record.id,()=>openDownload(record.id,true))}><ActionIcon name="files" className="h-4 w-4"/></ActionIconButton>
         <ActionIconButton label={t('downloads.copy')} description={t('downloads.copy')} disabled={!record.path||!!busy} onClick={()=>void act(record.id,()=>copy(record))}><ActionIcon name="copy" className="h-4 w-4"/></ActionIconButton>
        </>}
        {record.status!=='active'&&<>
         <ActionIconButton label={t('downloads.delete')} description={t('downloads.delete')} disabled={record.status!=='complete'||!record.path||!!busy} onClick={()=>void act(record.id,()=>prepare(record))}><ActionIcon name="clear" className="h-4 w-4"/></ActionIconButton>
         <ActionIconButton label={t('downloads.dismiss')} description={t('downloads.dismiss')} disabled={!!busy} onClick={()=>void act(record.id,()=>dismissDownload(record.id))}><CloseIcon className="h-4 w-4"/></ActionIconButton>
        </>}
       </div>
      </div>
      {record.status==='failed'&&<p className="ml-[38px] mt-0.5 text-xs text-danger" data-download-failure>{t(`downloads.failure.${safeDownloadFailure(record.failureReason)}`)}</p>}
      {record.status==='active'&&<div role="progressbar" aria-label={t('downloads.status.active')} className="mt-1.5 h-0.5 overflow-hidden rounded bg-hover"><div className="downloads-progress h-full w-1/3 rounded bg-accent"/></div>}
      {copied===record.id&&<p role="status" className="mt-1 text-xs text-fg-muted">{t('downloads.copied')}</p>}
     </li>
    })}</ul>
   </div>}
   {(!isDesktop()||storageError||error||actionError)&&<footer className="shrink-0 border-t border-line px-3 py-2 text-xs text-fg-muted">
    {!isDesktop()&&<p>{t('downloads.browserNote')}</p>}
    {storageError&&<p role="alert">{t('downloads.storageError')}</p>}
    {error&&<p role="alert">{t('downloads.loadFailed')} <button className="text-accent" onClick={()=>void reloadDownloads()}>{t('app.retry')}</button></p>}
    {actionError&&<p role="alert">{actionError}</p>}
   </footer>}
  </section>
 </div>,document.body)
}

export function DeleteConfirmation({preview,busy,onCancel,onDelete}:{preview:DownloadDeletePreview;busy:boolean;onCancel:()=>void;onDelete:()=>void}) {
 const box=useRef<HTMLDivElement>(null),cancel=useRef<HTMLButtonElement>(null)
 // Preparation can mount this review before its busy flag clears. Focus the
 // enabled Cancel before paint, without stealing an existing in-review choice.
 useLayoutEffect(()=>{if(!busy&&!box.current?.contains(document.activeElement))cancel.current?.focus()},[busy])
 return <div ref={box} className="min-h-0 overflow-y-auto p-4" data-download-delete-confirmation>
  <h3 className="font-semibold">{t('downloads.deleteTitle')}</h3>
  <p className="mt-2 text-fg-muted">{t(preview.directory?'downloads.deleteDirectoryWarning':'downloads.deleteFileWarning')}</p>
  <p className="mt-3 break-all font-mono text-xs">{preview.path}</p>
  <div className="mt-4 flex justify-end gap-2">
   <button ref={cancel} type="button" className="rounded border border-line px-3 py-1 hover:bg-hover" disabled={busy} onClick={onCancel}>{t('action.cancel')}</button>
   <button type="button" className="rounded border border-danger/50 bg-danger/10 px-3 py-1 text-danger hover:bg-danger/20 disabled:opacity-40" disabled={busy} onClick={onDelete}>{t('downloads.delete')}</button>
  </div>
 </div>
}

function DownloadItemIcon({record}:{record:DownloadRecord}) {
 const type=downloadType(record)
 const paths={archive:'M4 7h16v14H4zM3 3h18v4H3zM10 10h4',image:'M4 3h16v18H4zM4 17l5-5 4 4 3-3 4 4M15 7h.01',code:'M8 8l-4 4 4 4M16 8l4 4-4 4M13 6l-2 12',text:'M5 3h10l4 4v14H5zM9 10h6M9 14h6M9 18h4'}
 return <span className="flex h-8 w-7 shrink-0 items-center justify-center text-fg-muted" title={t(`downloads.type.${type}`)} data-download-type={type}>
  {type==='folder'?<FolderIcon className="h-6 w-6 text-accent"/>:type==='file'?<FileIcon className="h-6 w-6"/>:<svg viewBox="0 0 24 24" className="h-6 w-6" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round"><path d={paths[type]}/></svg>}
 </span>
}
