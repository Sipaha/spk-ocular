import {useId,useRef,useState,useEffect,type ReactNode} from 'react'
import {createPortal} from 'react-dom'
import {getLanguage,t} from '../i18n'
import {useStore} from '../store'
import {ModalCloseButton,useModalFocus} from '../components/modal'
import {openURL} from '../tunnels/open'
import {concept,concepts,documentation,type ConceptId} from './catalog'
import {explanation,learn} from './content'
import {useLearning} from './store'
import {Lesson} from './Lesson'
import {Course} from './Course'
import {pathText} from './pathContent'

export function ResourceHelpButton({provider,kind}:{provider:string;kind:string}) {
 const language=useStore(s=>s.info?.language)
 void language // Help labels follow live language changes, even in retained drawers.
 if(provider!=='kubernetes')return null
 return <button type="button" className="action-icon-button shrink-0 text-fg-muted" aria-label={`${learn('what')} ${concept(kind)?.[1]??kind}`} aria-haspopup="dialog" onClick={event=>{event.currentTarget.focus();useLearning.getState().open(kind)}}><span aria-hidden="true" className="flex h-4 w-4 items-center justify-center rounded-full border border-current text-[11px]">?</span></button>
}

/** No provider client: concept navigation cannot read objects or connect targets. */
export function ResourceExplanation({kind,onConcept}:{kind:string;onConcept:(id:ConceptId)=>void}) {
 const entry=concept(kind),[linkError,setLinkError]=useState(false)
 const mode=useStore(s=>s.info?.mode)
 if(!entry)return <section className="space-y-3"><h3 className="font-semibold break-all">{kind}</h3><p>{learn('fallback')}</p></section>
 const text=explanation(entry[0])
 return <article className="space-y-4" data-resource-explanation={kind}>
  <div><h3 className="text-lg font-semibold">{entry[1]}</h3><p className="mt-2 text-sm leading-relaxed">{text.purpose}</p></div>
  <section><h4 className="mb-2 text-xs font-semibold text-fg-subtle">{learn('related')}</h4><nav className="flex flex-wrap gap-2" aria-label={learn('related')}>{entry[4].split(',').map(id=><button key={id} className="rounded border border-line px-2 py-1 text-xs text-accent hover:bg-hover" onClick={()=>onConcept(id as ConceptId)}>{concept(id)?.[1]??id}</button>)}</nav></section>
  <details><summary className="cursor-pointer text-sm font-medium">{learn('fields')}</summary><ul className="mt-2 space-y-1">{entry[3].split(',').map(field=><li key={field} className="break-all font-mono text-xs text-fg-muted">{field}</li>)}</ul></details>
  <section className="border-l-2 border-line pl-3"><h4 className="mb-1 text-xs font-semibold text-fg-subtle">{learn('caution')}</h4><p className="text-sm leading-relaxed text-fg-muted">{text.caution}</p></section>
  <a className="inline-block text-xs text-accent underline-offset-4 hover:underline" href={documentation(kind)} target="_blank" rel="noopener noreferrer" onClick={event=>{if(mode==='desktop'){event.preventDefault();setLinkError(false);void openURL(documentation(kind),'desktop').catch(()=>setLinkError(true))}}}>{learn('docs')}</a>
  {linkError&&<p role="alert" className="text-xs text-danger">{learn('linkFailed')}</p>}
 </article>
}

export function LearningDialog({languageMenu}:{languageMenu:ReactNode}) {
 const request=useLearning(s=>s.request),close=useLearning(s=>s.close)
 // Mounted only for an explicit request: no effects or content traffic while closed.
 if(!request)return null
 return <Library key={request.kind??'library'} initialKind={request.kind} onClose={close} languageMenu={languageMenu}/>
}
function Library({initialKind,onClose,languageMenu}:{initialKind?:string;onClose:()=>void;languageMenu:ReactNode}) {
 const [kind,setKind]=useState(initialKind??'apps/replicasets'),[query,setQuery]=useState(''),[view,setView]=useState<'reference'|'intro'|'path'>('reference'),[started,setStarted]=useState(false),[courseStarted,setCourseStarted]=useState(false),[introComplete,setIntroComplete]=useState(false)
 const box=useRef<HTMLElement>(null),title=useId(),modalKey=useModalFocus(box,onClose)
 const language=useStore(s=>s.info?.language)
 // Disabling the language trigger during an async preference write can leave
 // body focused. Preserve an editor focus, but recover the modal's lost focus.
 useEffect(()=>{if(document.activeElement===document.body)box.current?.focus()},[language])
 const matches=concepts.filter(c=>(c[0]+' '+c[1]+' '+explanation(c[0]).purpose).toLocaleLowerCase(getLanguage()).includes(query.toLocaleLowerCase(getLanguage())))
 return createPortal(<div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-2 sm:p-6" onMouseDown={e=>{if(e.target===e.currentTarget)onClose()}}>
  <section ref={box} tabIndex={-1} role="dialog" aria-modal="true" aria-labelledby={title} onKeyDown={modalKey} className="flex h-[min(880px,94dvh)] w-[min(1060px,100%)] min-w-0 flex-col overflow-hidden rounded-lg border border-line bg-panel text-fg shadow-2xl outline-none" data-learning>
   <header className="flex shrink-0 flex-wrap items-center gap-2 border-b border-line px-4 py-3"><div className="min-w-0 flex-1"><h2 id={title} className="font-semibold">{learn('library')}</h2><p className="text-xs text-fg-muted">{view!=='reference'?learn('lab'):learn('offline')}</p></div>{languageMenu}<ModalCloseButton onClick={onClose} label={t('action.close')}/></header>
   <nav className="flex shrink-0 flex-wrap gap-2 border-b border-line p-2" aria-label={learn('library')}>
    <button aria-pressed={view==='reference'} className={'rounded px-3 py-1 text-sm '+(view==='reference'?'bg-active text-accent':'hover:bg-hover')} onClick={()=>setView('reference')}>{learn('concepts')}</button>
    <button aria-pressed={view==='intro'} className={'rounded px-3 py-1 text-sm '+(view==='intro'?'bg-active text-accent':'hover:bg-hover')} onClick={()=>{setStarted(true);setView('intro')}}>{learn('lesson')}</button>
    <button aria-pressed={view==='path'} className={'rounded px-3 py-1 text-sm '+(view==='path'?'bg-active text-accent':'hover:bg-hover')} onClick={()=>{setCourseStarted(true);setView('path')}}>{pathText('course')}</button>
   </nav>
   <div hidden={view!=='reference'} className="min-h-0 flex-1 overflow-hidden">
    <div className="grid h-full min-h-0 grid-cols-1 grid-rows-[auto_minmax(0,1fr)] sm:grid-cols-[230px_minmax(0,1fr)] sm:grid-rows-1">
     <aside className="flex min-h-0 flex-col border-b border-line p-3 sm:border-r sm:border-b-0"><input className="w-full rounded border border-line bg-app px-2 py-1 text-sm" aria-label={learn('search')} placeholder={learn('search')} value={query} onChange={e=>setQuery(e.target.value)}/><nav aria-label={learn('concepts')} className="mt-2 max-h-40 min-h-0 overflow-auto sm:max-h-none sm:flex-1">{matches.map(c=><button key={c[0]} aria-current={kind===c[0]?'page':undefined} className={'block w-full truncate rounded px-2 py-1 text-left text-sm '+(kind===c[0]?'bg-selected text-accent':'hover:bg-hover')} onClick={()=>setKind(c[0])}>{c[1]}</button>)}{!matches.length&&<p role="status" className="p-2 text-sm text-fg-muted">{learn('noMatches')}</p>}</nav></aside>
     <div className="min-h-0 min-w-0 overflow-auto p-4 sm:p-6"><ResourceExplanation key={kind} kind={kind} onConcept={id=>{setKind(id);setQuery('');box.current?.focus()}}/></div>
    </div>
   </div>
   <div hidden={view!=='intro'} className="min-h-0 flex-1 overflow-auto">{started&&<Lesson onComplete={setIntroComplete}/>}</div>
   <div hidden={view!=='path'} className="min-h-0 flex-1 overflow-auto">{courseStarted&&<Course introComplete={introComplete} onIntro={()=>{box.current?.focus();setStarted(true);setView('intro')}}/>}</div>
  </section>
 </div>,document.body)
}
