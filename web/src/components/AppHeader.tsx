import {lazy, Suspense} from 'react'
import {useLearning} from '../learning/store'
import {learn} from '../learning/content'
import { BaselineControl } from '../compare/BaselineControl'
import { useEffect, useRef, useState, type RefObject } from 'react'
import { createPortal } from 'react-dom'
import type { Client } from '../api/client'
import { LanguageMenu } from './LanguageMenu'
import type { Target } from '../api/types'
import type { ConfigurationActions } from '../configurations/Configurations'
import type { Actions } from '../store'
import { t } from '../i18n'
import { openPalette } from '../palette/store'
import { SearchIcon } from './icons'
import { DownloadsButton } from '../downloads/DownloadsButton'
import { ConnectionSwitcher } from '../connections/ConnectionSwitcher'

const KrrDialog = lazy(() => import('../krr/KrrDialog'))

/** Connection identity leads the workspace; app utilities stay in a quiet menu. */
export function AppHeader({target,onHelp,onAbout,client,act,configRef}:{target:Target|null;onHelp:()=>void;onAbout:()=>void;client:Client;act:Actions;configRef:RefObject<ConfigurationActions|null>}){
 const [open,setOpen]=useState(false),button=useRef<HTMLButtonElement>(null),menu=useRef<HTMLElement>(null)
 const [krrOpen,setKrrOpen]=useState(false)
 const close=()=>{setOpen(false);button.current?.focus()}
 useEffect(()=>{if(open)menu.current?.querySelector<HTMLElement>('button')?.focus()},[open])
 return <header className="app-header">
  <ConnectionSwitcher client={client} act={act} configRef={configRef} target={target}/>
  <button className="command-trigger" onClick={openPalette} aria-label={`${t('palette.label')} (Ctrl+K)`}><SearchIcon className="h-4 w-4 shrink-0"/><span className="truncate">{t('shell.search')}</span><kbd>Ctrl K</kbd></button>
  <BaselineControl/>
  <DownloadsButton/>
  <button ref={button} type="button" className="help-trigger" aria-label={t('connections.appMenu')} title={t('connections.appMenu')} aria-haspopup="dialog" aria-expanded={open} data-app-menu onClick={()=>setOpen(!open)}><svg aria-hidden="true" viewBox="0 0 20 20" className="h-4 w-4" fill="none" stroke="currentColor" strokeWidth="1.5"><path d="M4 5h12M4 10h12M4 15h12"/></svg></button>
  {open&&createPortal(<div className="fixed inset-0 z-40" onMouseDown={e=>{if(e.target===e.currentTarget)close()}}><section ref={menu} role="dialog" aria-modal="true" aria-label={t('connections.appMenu')} className="app-utilities" onKeyDown={e=>{
   if(e.defaultPrevented)return
   if(e.key==='Escape'){e.preventDefault();e.stopPropagation();close()}
   if(e.key==='Tab'){
    const stops=Array.from(menu.current?.querySelectorAll<HTMLElement>('button')??[]),first=stops[0],last=stops.at(-1)
    if(e.shiftKey&&document.activeElement===first){e.preventDefault();last?.focus()}
    if(!e.shiftKey&&document.activeElement===last){e.preventDefault();first?.focus()}
   }
  }}>
   <LanguageMenu client={client} onChanged={close}/>
   <button type="button" className="app-utility-action" onClick={()=>{close();useLearning.getState().open()}}>{learn('library')}</button>
   <button type="button" className="app-utility-action" onClick={()=>{close();setKrrOpen(true)}}>{t('krr.title')}</button>
   <button type="button" className="app-utility-action about-trigger" aria-label={t('about.title')} onClick={()=>{close();onAbout()}}>{t('about.open')}</button>
   <button type="button" className="app-utility-action" onClick={()=>{close();onHelp()}}>{t('keys.title')}<kbd>?</kbd></button>
  </section></div>,document.body)}
  {krrOpen&&createPortal(<Suspense fallback={null}><KrrDialog client={client} target={target} onClose={()=>{setKrrOpen(false);button.current?.focus()}}/></Suspense>,document.body)}
 </header>
}
