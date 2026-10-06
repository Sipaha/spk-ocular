import { createPortal } from 'react-dom'
import { lazy, Suspense, useEffect, useImperativeHandle, useId, useRef, useState, type Ref } from 'react'
import type { Target } from '../api/types'
import type { Client } from '../api/client'
import type { ConfigRequest, ConfigState, ConfigEntry } from './types'
import { t } from '../i18n'
import { editsHeld } from '../edit/guard'
import { focusMark, restoreFocus } from '../shortcuts'

import { Menu } from '../actions/Menu'
const YamlView = lazy(() => import('../components/YamlView'))

export type ConfigurationAction = 'inspect' | 'edit' | 'rename' | 'remove' | 'reveal'
export interface ConfigurationActions { unlockTarget: (target: Target, resume: () => Promise<void>) => void; openTarget: (target: Target, action: ConfigurationAction) => void }

type Mode = 'reset' | 'edit' | 'inspect' | 'scan' | 'setup' | 'unlock' | 'editor' | 'rename' | 'remove'
export function Configurations({ client, onChanged, ref }: { client: Client; onChanged: () => unknown; ref?: Ref<ConfigurationActions> }) {
 const [state,setState]=useState<ConfigState|null>(null)
 const [mode,setMode]=useState<Mode|null>(null)
 const [entry,setEntry]=useState<ConfigEntry|null>(null)
 const [addMenu,setAddMenu]=useState<{x:number;y:number}|null>(null)
 const [content,setContent]=useState<string|null>(null)
 const [contentRevision,setContentRevision]=useState('')
 const [reviewEdit,setReviewEdit]=useState(false)
 const alive=useRef(true)
 useEffect(()=>{alive.current=true;return()=>{alive.current=false}},[])
 const cancelRef=useRef<HTMLButtonElement>(null)
 const [busy,setBusy]=useState(false)
 const [error,setError]=useState('')
 const [paths,setPaths]=useState<string[]>([])
 const [password,setPassword]=useState('')
 const [repeat,setRepeat]=useState('')
 const [name,setName]=useState('')
 const [renameTarget,setRenameTarget]=useState('')
 const [renameRevision,setRenameRevision]=useState('')
 const [originalName,setOriginalName]=useState('')
 const [yaml,setYaml]=useState('')
 const [discard,setDiscard]=useState(false)
 const resumeConnect=useRef<(() => Promise<void>)|null>(null)
 const [afterUnlock,setAfterUnlock]=useState<Mode|null>(null)
 const box=useRef<HTMLDivElement>(null)
 const mark=useRef<ReturnType<typeof focusMark>|null>(null)
 useEffect(()=>{let live=true;void client.configurations({command:'status'}).then(s=>{if(live){setState(s);if(!s.initialized)setMode('scan')}},e=>{if(live)setError(String(e))});return()=>{live=false}},[client])
 useEffect(()=>{
  if(!mode)return
  if(!mark.current)mark.current=focusMark()
  if(busy){box.current?.focus();return}
  if(mode==='remove'||mode==='reset'){cancelRef.current?.focus();return}
  if(mode==='inspect'||mode==='edit'){
   (box.current?.querySelector<HTMLElement>('.cm-content')??box.current)?.focus()
   return
  }
  const first=box.current?.querySelector<HTMLInputElement|HTMLTextAreaElement>('input:not(:disabled),textarea:not(:disabled)')
  if(first){first.focus();if(mode==='rename'&&first instanceof HTMLInputElement)first.select()}
  else box.current?.focus()
 },[mode,busy,content])
 const close=()=>{resumeConnect.current=null;setRenameTarget('');setRenameRevision('');setOriginalName('');setReviewEdit(false);setContentRevision('');setContent(null);setAddMenu(null);setAfterUnlock(null);setMode(null);setEntry(null);setPassword('');setRepeat('');setName('');setYaml('');setError('');setDiscard(false);if(mark.current){restoreFocus(mark.current);mark.current=null}}
 const request=async(req:ConfigRequest,next:Mode|null)=>{
  setBusy(true);setError('')
  try{const s=await client.configurations(req);setState(s);setPassword('');setRepeat('');await onChanged();if(next===null)close();else setMode(next);return true}
  catch(e){setError(e instanceof Error?e.message:t('configs.error'));return false}
  finally{setBusy(false)}
 }
 const cancel=()=>{if(busy)return;if(addMenu){setAddMenu(null);return}if(reviewEdit){setReviewEdit(false);return}if(mode==='inspect'||(mode==='edit'&&yaml===content)){close();return}if(mode==='rename'||mode==='remove'||mode==='reset'){close();return}if(yaml||name){setDiscard(true);return}if(state&&!state.initialized){void request({command:'dismiss'},null)}else close()}
 const custom=()=>{setError('');setAfterUnlock('editor');if(state?.locked)setError(t('configs.connectToUnlock'));else setMode(!state?.encrypted?'setup':'editor')}
 const inspect=async(e:ConfigEntry,editing=false)=>{
  setEntry(e);setContent(null);setYaml('');setReviewEdit(false);setError('');setMode(editing?'edit':'inspect');setBusy(true)
  try{const result=await client.configurations({command:'inspect',id:e.id,expect:e.revision});if(alive.current){setContent(result.yaml??'');setContentRevision(result.contentRevision??'');if(editing)setYaml(result.yaml??'')}}
  catch(err){if(alive.current)setError(err instanceof Error?err.message:t('configs.error'))}
  finally{if(alive.current)setBusy(false)}
 }
 const openInspector=(e:ConfigEntry,editing=false,locked=state?.locked)=>{
  setEntry(e)
  if(e.kind==='stored'&&locked){setError(t('configs.connectToUnlock'))}else return inspect(e,editing)
 }
 useImperativeHandle(ref,()=>({unlockTarget:(_target,resume)=>{if(busy||mode)return;resumeConnect.current=resume;setAfterUnlock(null);setError('');setMode('unlock')},openTarget:(target,action)=>{
  if(busy||mode)return
  setBusy(true);setError('')
  void client.configurations({command:'resolve',target:target.id}).then(async s=>{
   if(!alive.current)return
   setState(s);const e=s.entries.find(e=>e.id===s.entryId)
   if(!e){setError(t('configs.error'));return}
   setEntry(e)
   if(action==='inspect'||action==='edit')await openInspector(e,action==='edit',s.locked)
   else if(action==='rename'){setName(s.targetName??target.title);setOriginalName(s.targetName??target.title);setRenameTarget(target.id);setRenameRevision(s.targetRevision??'');setMode('rename')}
   else if(action==='remove')setMode('remove')
   else {await request({command:'reveal',id:e.id,expect:e.revision},null)}
  },err=>{if(alive.current){setError(err instanceof Error?err.message:t('configs.error'))}}).finally(()=>{if(alive.current)setBusy(false)})
 }}))
 const titleId=useId()
 const yamlDialog=mode==='inspect'||mode==='edit'
 const field='w-full rounded border border-line bg-app px-2 py-1.5 text-sm outline-none focus:border-accent'
 const buttonBase='inline-flex min-h-8 items-center justify-center rounded border px-3 py-1.5 text-sm focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent disabled:cursor-default disabled:opacity-40'
 const button=buttonBase+' border-line hover:bg-hover'
 const primary=buttonBase+' border-accent bg-accent text-accent-fg hover:opacity-90'
 const danger=buttonBase+' border-danger/50 bg-danger/10 text-danger hover:bg-danger/20'
 return <>
  <div className="px-2 pb-2 text-sm" onKeyDown={e=>e.stopPropagation()}>
   <button disabled={busy} className="inline-flex min-h-6 items-center gap-1 text-[12px] text-fg-muted hover:text-accent focus-visible:outline focus-visible:outline-accent" aria-haspopup="menu" aria-expanded={!!addMenu} onClick={event=>{setError('');const r=event.currentTarget.getBoundingClientRect();setAddMenu({x:r.left,y:r.bottom})}}>{t('configs.add')}<svg aria-hidden viewBox="0 0 12 12" className="h-3 w-3" fill="none" stroke="currentColor" strokeWidth="1.3"><path d="m3 4.5 3 3 3-3"/></svg></button>
   {!mode&&error&&<p role="alert" className="text-danger">{error}</p>}
  </div>
  {addMenu&&createPortal(<Menu at={addMenu} label={t('configs.add')} onClose={()=>setAddMenu(null)} items={[
   {id:'scan',label:t('configs.scan'),onSelect:()=>{setPaths([]);void request({command:'scan'},'scan')}},
   {id:'custom',label:t('configs.custom'),onSelect:()=>{void client.configurations({command:'status'}).then(s=>{setState(s);setAfterUnlock('editor');if(s.locked)setError(t('configs.connectToUnlock'));else setMode(!s.encrypted?'setup':'editor')},err=>setError(err instanceof Error?err.message:t('configs.error')))}},
   ...(state?.encrypted?[{id:'reset',label:t('configs.reset'),danger:true,separator:true,onSelect:()=>{resumeConnect.current=null;setPassword('');void request({command:'status'},'reset')}}]:[]),
  ]}/>,document.body)}
  {mode&&createPortal(<div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4" onMouseDown={e=>{if(e.target===e.currentTarget)cancel()}}>
   <div ref={box} role="dialog" aria-modal="true" aria-labelledby={titleId} tabIndex={-1}
    className={yamlDialog?"flex h-[calc(100dvh-32px)] w-[calc(100vw-32px)] flex-col rounded border border-line bg-panel p-3 text-fg shadow-2xl outline-none":"max-h-[88vh] w-[min(760px,96vw)] overflow-auto rounded-lg border border-line bg-panel p-5 text-fg shadow-2xl outline-none"}
    onKeyDown={e=>{
     if(e.defaultPrevented)return
     if(e.key==='Escape'){e.preventDefault();e.stopPropagation();cancel()}
     if(e.key==='Tab'){
      const els=Array.from(box.current?.querySelectorAll<HTMLElement>('button:not(:disabled),input:not(:disabled),textarea:not(:disabled),[contenteditable=true]')??[])
      const first=els[0],last=els.at(-1)
      if(e.shiftKey&&(document.activeElement===first||document.activeElement===box.current)){e.preventDefault();last?.focus()}
      else if(!e.shiftKey&&(document.activeElement===last||document.activeElement===box.current)){e.preventDefault();first?.focus()}
     }
    }}>
    <h2 id={titleId} className={yamlDialog?'mb-2 flex shrink-0 items-center justify-between gap-3 font-semibold':'mb-3 font-semibold'}>{yamlDialog?<><span className="min-w-0 truncate">{entry?.name||t('configs.yaml')}</span><span className="shrink-0 text-xs font-normal text-fg-muted">{t(mode==='inspect'?'configs.readOnly':'configs.editing')}</span></>:t(mode==='scan'?'configs.importTitle':mode==='setup'?'configs.setupTitle':mode==='unlock'?'configs.unlockTitle':mode==='rename'?'configs.rename':mode==='remove'?'configs.removeConfirm':mode==='reset'?'configs.reset':'configs.title')}</h2>
    <fieldset disabled={busy||discard} className={yamlDialog?"flex min-h-0 min-w-0 flex-1 flex-col gap-2":"min-w-0 space-y-3"}>
     {(mode==='inspect'||mode==='edit')&&entry&&<>
      {entry.path&&<p title={entry.path} className="shrink-0 truncate font-mono text-xs text-fg-muted">{entry.path}</p>}
      {content!==null&&<div inert={busy||discard||reviewEdit} className="min-h-0 min-w-0 flex-1 overflow-hidden rounded border border-line"><Suspense fallback={<p>{t('configs.loading')}</p>}><YamlView key={mode} autoFocus text={content} editable={mode==='edit'} onChange={text=>{setYaml(text);setReviewEdit(false)}} label={t('configs.yaml')}/></Suspense></div>}
     </>}
     {mode==='reset'&&<>
      <p className="text-sm">{t('configs.resetWarning')}</p>
      <ul className="space-y-1 text-sm">{state?.entries.filter(e=>e.kind==='stored').map(e=><li key={e.id}>{e.name}</li>)}</ul>
     </>}
     {mode==='rename'&&entry&&<>
      <p className="text-sm text-fg-muted">{t('configs.renameHint')}</p>
      <label className="block text-sm"><span className="mb-1 block px-2 text-xs text-fg-muted">{t(mode==='rename'?'configs.connectionName':'configs.name')}</span><input className={field} value={name} maxLength={128} onChange={e=>setName(e.target.value)}/></label>
     </>}
     {mode==='remove'&&entry&&<>
      <p className="break-all font-semibold">{entry.name}</p>
      {entry.path&&<p className="break-all font-mono text-xs text-fg-muted">{entry.path}</p>}
      <p className="text-sm text-fg-muted">{t(entry.kind==='linked'?'configs.removeLinked':'configs.removeStored')}</p>
      <p className="text-sm text-fg-muted">{t('configs.removeConnections')}</p>
     </>}
     {mode==='scan'&&<>
      <p className="text-sm text-fg-muted">{t('configs.importText')}</p>
      {!state?.candidates.length&&<p>{t('configs.empty')}</p>}
      <div className="max-h-[40vh] space-y-2 overflow-auto">{state?.candidates.map(c=><label key={c.path} className="flex items-start gap-3 rounded border border-line p-3">
       <span className="relative mt-1 h-3.5 w-3.5 shrink-0"><input type="checkbox" className="absolute inset-0 m-0 h-full w-full appearance-none rounded-sm border border-fg-subtle bg-transparent checked:border-accent checked:bg-accent focus-visible:outline focus-visible:outline-accent disabled:opacity-50" checked={c.selected||paths.includes(c.path)} disabled={c.selected||!!c.problem} onChange={e=>setPaths(e.target.checked?[...paths,c.path]:paths.filter(p=>p!==c.path))}/>
       {(c.selected||paths.includes(c.path))&&<svg aria-hidden="true" viewBox="0 0 10 10" className="pointer-events-none absolute inset-0 m-auto h-2.5 w-2.5 text-accent-fg" fill="none" stroke="currentColor" strokeWidth="1.8"><path d="M2 5.2 4.2 7.4 8 2.8"/></svg>}</span>
       <span className="min-w-0 text-sm"><span className="block break-all font-mono">{c.path}</span><span className="block text-fg-muted">{c.problem||c.contexts.join(', ')}{c.selected?' · '+t('configs.linked'):''}</span></span>
      </label>)}</div>
     </>}
     {(mode==='setup'||mode==='unlock')&&<>
      <p className="text-sm text-fg-muted">{t(mode==='setup'?'configs.setupText':'configs.unlockText')}</p>
      <label className="block text-sm"><span className="mb-1 block px-2 text-xs text-fg-muted">{t('configs.password')}</span><input className={field} type="password" autoComplete={mode==='setup'?'new-password':'current-password'} value={password} maxLength={1024} onChange={e=>setPassword(e.target.value)}/></label>
      {mode==='setup'&&<><label className="block text-sm"><span className="mb-1 block px-2 text-xs text-fg-muted">{t('configs.confirm')}</span><input className={field} type="password" autoComplete="new-password" value={repeat} maxLength={1024} onChange={e=>setRepeat(e.target.value)}/></label></>}
     </>}
     {mode==='editor'&&<>
      <p className="text-sm text-fg-muted">{t('configs.editorText')}</p>
      <label className="block text-sm"><span className="mb-1 block px-2 text-xs text-fg-muted">{t('configs.name')}</span><input className={field} value={name} maxLength={128} onChange={e=>setName(e.target.value)}/></label>
      <label className="block text-sm"><span className="mb-1 block px-2 text-xs text-fg-muted">{t('configs.yaml')}</span><textarea className={field+' h-[35vh] font-mono'} value={yaml} spellCheck={false} autoComplete="off" onChange={e=>setYaml(e.target.value)}/></label>
     </>}
    </fieldset>
    {error&&<p role="alert" className="mt-3 text-sm text-danger">{error}</p>}
    {busy&&<p role="status" className="mt-3 text-sm text-fg-muted">{t('configs.loading')}</p>}
    {reviewEdit&&entry&&<div className="mt-4 rounded border border-line bg-app p-3 text-sm"><p>{t(entry.kind==='linked'?'configs.reviewFile':'configs.reviewStored')}</p><p className="mt-2 break-all font-mono text-xs text-fg-muted">{entry.path||entry.name}</p></div>}
    {discard&&<p className="mt-4 text-sm">{t('configs.discard')}</p>}
    <div className={"config-dialog-footer flex shrink-0 items-center justify-between gap-3 border-t border-line "+(yamlDialog?"mt-3 pt-3":"mt-5 pt-4")}>
     {discard?<>
      <button className={button} onClick={()=>setDiscard(false)}>{t('configs.keep')}</button>
      <button className={danger} onClick={close}>{t('configs.discardYes')}</button>
     </>:<>
      <button ref={cancelRef} disabled={busy} className={button} onClick={cancel}>{t(reviewEdit?'configs.keep':mode==='inspect'?'configs.back':state&&!state.initialized?'configs.skip':'configs.cancel')}</button>
      <div className="config-dialog-primary flex items-center justify-end gap-2">
       {mode==='unlock'&&<button disabled={busy} className={button} onClick={()=>{resumeConnect.current=null;setPassword('');void request({command:'status'},'reset')}}>{t('configs.reset')}</button>}
       {mode==='reset'&&<button disabled={busy||!state?.resetRevision} className={danger} onClick={()=>{if(editsHeld()){setError(t('configs.unsaved'));return}void request({command:'reset',expect:state?.resetRevision},null)}}>{t('configs.resetConfirm')}</button>}
       {mode==='scan'&&<>
        <button disabled={busy} className={button} onClick={custom}>{t('configs.custom')}</button>
        <button className={primary} disabled={busy||!paths.length} onClick={()=>void request({command:'import',paths},null)}>{t('configs.import')}</button>
       </>}
       {(mode==='setup'||mode==='unlock')&&<button className={primary} disabled={busy||!password||(mode==='setup'&&password!==repeat)} onClick={()=>{const resume=resumeConnect.current;void request({command:mode,password},mode==='setup'?'editor':afterUnlock).then(ok=>{if(ok&&resume)void resume()})}}>{t(mode==='setup'?'configs.setup':'configs.unlock')}</button>}
       {mode==='inspect'&&entry&&content!==null&&<button disabled={busy} className={button} onClick={()=>{setYaml(content);setMode('edit')}}>{t('configs.edit')}</button>}
       {mode==='edit'&&entry&&content!==null&&<button className={primary} disabled={busy||!yaml.trim()||yaml===content} onClick={()=>{
        if(editsHeld()){setError(t('configs.unsaved'));return}
        if(!reviewEdit){setReviewEdit(true);return}
        void request({command:'update',id:entry.id,expect:entry.revision,contentRevision,yaml},null)
       }}>{t(reviewEdit?'configs.confirmSave':'configs.save')}</button>}
       {mode==='editor'&&<button className={primary} disabled={busy||!name.trim()||!yaml.trim()} onClick={()=>void request({command:'create',name,yaml},null)}>{t('configs.save')}</button>}
       {mode==='rename'&&entry&&<button className={primary} disabled={busy||!name.trim()||name.trim()===originalName} onClick={()=>void request({command:'rename-target',target:renameTarget,targetRevision:renameRevision,id:entry.id,expect:entry.revision,name},null).then(ok=>{if(ok)setName('')})}>{t('configs.rename')}</button>}
       {mode==='remove'&&entry&&<button disabled={busy} className={danger} onClick={()=>{if(editsHeld()){setError(t('configs.unsaved'));return}void request({command:'remove',id:entry.id,expect:entry.revision},null)}}>{t('configs.removeConfirm')}</button>}
      </div>
     </>}
    </div>
   </div>

  </div>,document.body)}
 </>
}
