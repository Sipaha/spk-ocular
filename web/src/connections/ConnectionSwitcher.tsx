import { useEffect, useId, useRef, useState, type RefObject, type KeyboardEvent } from 'react'
import { createPortal } from 'react-dom'
import { useStore as useModel } from 'zustand'
import type { Client } from '../api/client'
import type { Target } from '../api/types'
import { type Actions, matchesFilter, targetKey, useStore } from '../store'
import { providerText, t } from '../i18n'
import { Configurations, type ConfigurationActions } from '../configurations/Configurations'
import { Menu, type MenuItem } from '../actions/Menu'
import { targetMenuItems } from './targetMenu'
import { ProviderIcon, WarningIcon } from '../components/icons'
import { agents, pendingOf, useAgents } from '../agents/store'
import { TargetInfoButton } from '../components/TargetDetails'
import { appearanceModel } from './appearance'
import { ConnectionIcon } from './ConnectionIcon'
import { AppearanceDialog } from './AppearanceDialog'

export function ConnectionSwitcher({client,act,configRef,target}:{client:Client;act:Actions;configRef:RefObject<ConfigurationActions|null>;target:Target|null}){
 const view=useStore(s=>s.view),actions=useStore(s=>s.connectionActions),pending=useAgents(s=>s.pending)
 const model=appearanceModel(client),errors=useModel(model.store,s=>s.errors)
 const [open,setOpen]=useState(false),[query,setQuery]=useState(''),[cursor,setCursor]=useState(0),[keyboardCursor,setKeyboardCursor]=useState(false)
 const [menu,setMenu]=useState<{target:Target;at:{x:number;y:number};configs:ConfigurationActions|null}|null>(null),[editing,setEditing]=useState<Target|null>(null)
 const trigger=useRef<HTMLButtonElement>(null),box=useRef<HTMLElement>(null),input=useRef<HTMLInputElement>(null),id=useId()
 const all=view?.groups.flatMap(g=>g.targets)??[],filtered=all.filter(x=>matchesFilter(x,query)),active=filtered[Math.min(cursor,Math.max(0,filtered.length-1))]
 const failedTargets=all.filter(x=>errors[JSON.stringify([x.provider,x.id])])
 const issueCount=(view?.groups.reduce((n,g)=>n+g.problems.length+(g.error?1:0),0)??0)+failedTargets.length
 useEffect(()=>{for(const g of view?.groups??[])for(const x of g.targets)void model.ensure(x)},[view,model])
 const show=()=>{setKeyboardCursor(false);setOpen(true);setQuery('');setCursor(Math.max(0,all.findIndex(x=>!!target&&targetKey(x)===targetKey(target))))}
 const close=(back=true)=>{setOpen(false);setQuery('');setCursor(0);if(back)trigger.current?.focus()}
 useEffect(()=>{if(open)input.current?.focus()},[open])
 useEffect(()=>{if(open&&active&&(keyboardCursor||document.activeElement===input.current))box.current?.querySelector<HTMLElement>(`[id="${id}-${filtered.indexOf(active)}"]`)?.scrollIntoView({block:'nearest'})},[open,keyboardCursor,cursor,id,active,filtered])
 const addConfig=(at:{x:number;y:number})=>{close();configRef.current?.openAdd(at)}
 const choose=(x:Target)=>{close();void act.select(x)}
 const showMenu=(x:Target,at:{x:number;y:number})=>{setMenu({target:x,at,configs:configRef.current})}
 const rowKey=(e:KeyboardEvent<HTMLButtonElement>,x:Target)=>{
  if(e.key==='ContextMenu'||(e.shiftKey&&e.key==='F10')){e.preventDefault();e.stopPropagation();const r=e.currentTarget.getBoundingClientRect();showMenu(x,{x:r.left,y:r.bottom});return}
  if(['ArrowDown','ArrowUp','Home','End'].includes(e.key)){
   e.preventDefault();setKeyboardCursor(true);const next=e.key==='Home'?0:e.key==='End'?filtered.length-1:(filtered.indexOf(x)+(e.key==='ArrowDown'?1:-1)+filtered.length)%filtered.length
   setCursor(next);box.current?.querySelector<HTMLButtonElement>(`[id="${id}-${next}"] .connection-choice`)?.focus()
  }
 }
 const actual=menu?all.find(x=>targetKey(x)===targetKey(menu.target)):undefined
 const menuItems:MenuItem[]=actual?[
  {id:'appearance',label:t('appearance.title'),onSelect:()=>{close();setEditing(actual)}},
  ...targetMenuItems(actual,act,menu?.configs??null).map(item=>({...item,onSelect:()=>{close();item.onSelect()}})),
 ]:[]
 const status=(x:Target)=>x.connection?.state==='connecting'||actions[targetKey(x)]?'connecting':x.open?'connected':'disconnected'
 const statusLabel=(x:Target)=>t(status(x)==='connecting'?'connection.connecting':status(x)==='connected'?'connections.connected':'connection.disconnected')
 return <>
  <div className="connection-context" data-area="targets">
   <button ref={trigger} data-area-focus data-connection-switcher type="button" aria-label={t('connections.title')} aria-haspopup="dialog" aria-expanded={open} aria-describedby={`${id}-context`} className="connection-trigger" title={target?.title??t('connections.choose')} onClick={()=>{if(open)close();else show()}} onKeyDown={e=>{if(target&&(e.key==='ContextMenu'||(e.shiftKey&&e.key==='F10'))){rowKey(e,target);return}if(e.key==='ArrowDown'||e.key==='ArrowUp'){e.preventDefault();show()}}}>
    {target&&<ConnectionIcon client={client} target={target}/>}
    <span className="min-w-0 truncate">{target?.title??t('connections.choose')}</span>
    {target&&<span data-connection-state={status(target)} aria-label={statusLabel(target)} className="connection-state"/>}
    {issueCount>0&&<span className="text-warning" title={t('connections.problems',{n:issueCount})}><WarningIcon className="h-3.5 w-3.5"/></span>}
    <svg aria-hidden="true" className="h-3 w-3 shrink-0 text-fg-muted" viewBox="0 0 12 12" fill="none" stroke="currentColor"><path d="m3 4.5 3 3 3-3"/></svg>
   </button>
   {pending.length>0&&<button type="button" className="rounded px-1 text-xs text-warning" aria-label={`${t('agents.waitingHint')}: ${pending.length}`} onClick={()=>agents.bringBack()}>{pending.length}</button>}
   <span id={`${id}-context`} className="sr-only">{target?`${target.title}. ${statusLabel(target)}`:t('connections.choose')}{issueCount>0?`. ${t('connections.problems',{n:issueCount})}`:''}</span>
   {target&&<TargetInfoButton key={targetKey(target)} target={target} client={client}/>}
  </div>
  <Configurations client={client} ref={configRef} hideTrigger onChanged={()=>act.reload()}/>
  {open&&createPortal(<div className="fixed inset-0 z-40" onMouseDown={e=>{if(e.target===e.currentTarget)close()}}>
   <section ref={box} role="dialog" aria-modal="true" aria-label={t('connections.title')} data-connection-picker data-keyboard={keyboardCursor||undefined} className="connection-picker" onFocusCapture={e=>{if(e.target!==input.current&&!(e.target as HTMLElement).closest('[data-connection-row]'))setKeyboardCursor(false)}} onPointerMove={()=>{if(keyboardCursor)setKeyboardCursor(false)}} onKeyDown={e=>{
    if(menu)return
    if(e.key==='Escape'){e.preventDefault();e.stopPropagation();if(query){setQuery('');setCursor(0);input.current?.focus()}else close();return}
    if(e.target===input.current){
     if(['ArrowDown','ArrowUp','Home','End'].includes(e.key)){
      if((e.key==='Home'||e.key==='End')&&!e.ctrlKey)return
      e.preventDefault();setKeyboardCursor(true);setCursor(e.key==='Home'?0:e.key==='End'?filtered.length-1:(Math.min(cursor,filtered.length-1)+(e.key==='ArrowDown'?1:-1)+filtered.length)%Math.max(1,filtered.length))
     }
     if(e.key==='Enter'&&active){e.preventDefault();choose(active)}
    }
    if(e.key==='Tab'){
     setKeyboardCursor(true)
     const stops=Array.from(box.current?.querySelectorAll<HTMLElement>('input,button:not(:disabled)')??[]),first=stops[0],last=stops.at(-1)
     if(e.shiftKey&&document.activeElement===first){e.preventDefault();last?.focus()}
     if(!e.shiftKey&&document.activeElement===last){e.preventDefault();first?.focus()}
    }
   }}>
    <div className="connection-search"><input ref={input} data-target-filter role="combobox" aria-expanded="true" aria-controls={id} aria-activedescendant={active?`${id}-${filtered.indexOf(active)}`:undefined} aria-haspopup="grid" aria-label={t('connections.search')} placeholder={t('connections.search')} value={query} onChange={e=>{setQuery(e.target.value);setCursor(0)}} spellCheck={false} className="w-full min-w-0 bg-transparent outline-none"/></div>
    <div id={id} role="grid" aria-label={t('connections.title')} className="connection-picker-list">
     {view?.groups.map(g=>{
      const items=g.targets.filter(x=>matchesFilter(x,query));if(query.trim()&&!items.length&&!g.error&&!g.problems.length)return null;return <div role="rowgroup" aria-label={g.title} key={g.provider}>
       <div role="row"><div role="columnheader" className="connection-group"><ProviderIcon provider={g.provider} className="h-4 w-4"/><span>{g.title}</span><span className="connection-group-count">{items.length}</span>{g.provider==='kubernetes'&&<button type="button" className="connection-add" aria-label={t('configs.add')} title={t('configs.add')} aria-haspopup="menu" onClick={e=>{const r=e.currentTarget.getBoundingClientRect();addConfig({x:r.left,y:r.bottom+4})}}>＋</button>}</div></div>
       {(g.error||g.problems.length>0)&&<div role="row"><div role="gridcell" className="connection-problem"><div role="alert" aria-live="polite">{g.error?t('sidebar.providerError',{error:g.error}):t('sidebar.problems',{count:g.problems.length})}{g.problems.map(p=><p key={p.source} className="break-all text-xs">{p.source.split('/').pop()}: {p.message}</p>)}</div></div></div>}
       {!g.targets.length&&<div role="row"><div role="gridcell" className="px-3 py-2 text-xs text-fg-subtle">{providerText('sidebar.noTargets',g.provider)}</div></div>}
       {items.map(x=>{
        const selected=!!target&&targetKey(x)===targetKey(target),index=filtered.indexOf(x),waiting=pendingOf(pending,x.provider,x.id)
        return <div key={targetKey(x)} role="row" id={`${id}-${index}`} aria-selected={selected} data-connection-row data-cursor={keyboardCursor&&active===x||undefined} onPointerMove={()=>{if(cursor!==index)setCursor(index)}} onFocusCapture={()=>{if(cursor!==index)setCursor(index)}} className="connection-picker-row" onContextMenu={e=>{e.preventDefault();showMenu(x,{x:e.clientX,y:e.clientY})}}>
         <div role="gridcell" className="min-w-0"><button type="button" className="connection-choice" onKeyDown={e=>rowKey(e,x)} aria-label={[x.title,x.subtitle||g.title].join(' ')} aria-pressed={selected} title={[x.title,x.subtitle,statusLabel(x)].filter(Boolean).join('\n')} onClick={()=>choose(x)}>
          <ConnectionIcon client={client} target={x}/><span className="min-w-0 flex-1 text-left"><span className="flex items-center gap-1"><span className="truncate">{x.title}</span>{x.locked&&<svg aria-hidden="true" data-encrypted-lock className="h-3.5 w-3.5 shrink-0 text-fg-muted" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.4"><rect x="3.5" y="7" width="9" height="7" rx="1.5"/><path d="M5.5 7V4.5a2.5 2.5 0 0 1 5 0V7M8 10v2"/></svg>}</span><span className="block truncate text-xs text-fg-subtle">{x.subtitle||g.title}</span></span>
          <span aria-hidden="true" className="text-accent">{selected?'✓':''}</span><span aria-hidden="true" data-connection-state={status(x)} className="connection-state"/>
         </button></div>
         <div role="gridcell" className="flex items-center pr-1">
          {waiting>0&&<button type="button" className="rounded px-1 text-xs text-warning" aria-label={`${t('agents.waitingHint')}: ${waiting}`} onClick={()=>{close();agents.bringBack()}}>{waiting}</button>}
          <button type="button" className="connection-more" aria-label={t('connections.actions',{name:x.title})} aria-haspopup="menu" onClick={e=>{const r=e.currentTarget.getBoundingClientRect();showMenu(x,{x:r.left,y:r.bottom})}}>⋯</button>
         </div>
        </div>
       })}
      </div>
     })}
     {query&&!filtered.length&&<div role="row"><div role="gridcell" className="px-3 py-4 text-sm text-fg-muted">{t('sidebar.empty')}</div></div>}
    </div>
    {failedTargets.length>0&&<div role="alert" className="connection-problem">{t('appearance.failed')}<button type="button" className="ml-2 underline" onClick={()=>{for(const x of failedTargets)void model.ensure(x,true)}}>{t('appearance.retry')}</button></div>}
   </section>
  </div>,document.body)}
  {menu&&<Menu items={menuItems} at={menu.at} label={t('target.menu')} onClose={()=>setMenu(null)}/>}
  {editing&&<AppearanceDialog client={client} target={editing} onClose={()=>setEditing(null)}/>}
 </>
}
