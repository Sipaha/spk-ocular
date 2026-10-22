import {useEffect,useRef} from 'react'
import {createPortal} from 'react-dom'
import {useStore} from '../store'
import {dock} from '../dock/store'
import {t} from '../i18n'
import {ModalCloseButton,useModalFocus} from '../components/modal'
import {clearLogComposition,compositionAvailable,removeLogRoot,useLogComposition} from './composition'

export function CompositionTray(){
 const {composition,targetTitle,review}=useLogComposition()
 const view=useStore(s=>s.view)
 const available=!!composition&&compositionAvailable(composition,view)
 useEffect(()=>{if(composition&&!available)clearLogComposition()},[composition,available])
 if(!composition?.refs.length||!available)return null
 return <>
  <div role="region" aria-label={t('logs.composition.title')} className="flex shrink-0 flex-wrap items-center gap-2 border-t border-line bg-panel px-3 py-1 text-xs">
   <span>{t('logs.composition.count',{count:composition.refs.length})} · {targetTitle}</span>
   <button type="button" className="rounded border border-line px-2 py-1 hover:bg-hover" data-composition-review onClick={()=>useLogComposition.setState({review:true})}>{t('logs.composition.review')}</button>
   <button type="button" className="rounded px-2 py-1 hover:bg-hover" onClick={clearLogComposition}>{t('logs.composition.clear')}</button>
  </div>
  {review&&<Review/>}
 </>
}
function Review(){
 const {composition,targetTitle}=useLogComposition()
 const box=useRef<HTMLElement>(null)
 const close=()=>useLogComposition.setState({review:false})
 const key=useModalFocus(box,close)
 if(!composition)return null
 return createPortal(<div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-3">
  <section ref={box} role="dialog" aria-modal="true" aria-label={t('logs.composition.title')} tabIndex={-1} onKeyDown={key} className="flex max-h-[calc(100dvh-24px)] w-[min(720px,calc(100vw-24px))] flex-col border border-line bg-panel outline-none">
   <header className="flex items-center gap-3 border-b border-line p-3"><strong>{t('logs.composition.title')}</strong><span className="flex-1"/><ModalCloseButton label={t('drawer.close')} onClick={close}/></header>
   <div className="min-h-0 overflow-auto p-3 text-xs"><p className="mb-2 break-words">{targetTitle} · {composition.refs[0]?.target}</p><p>{t('logs.composition.hint')}</p><p className="mt-2 text-fg-muted">{t('logs.composition.memory')}</p>
    <ul className="mt-3 divide-y divide-line">{composition.refs.map((r,i)=><li key={`${r.scope}/${r.uid}`} className="flex items-center gap-2 py-2"><div className="min-w-0 flex-1 break-words"><p>{r.scope} · {r.kind}/{r.name}</p><p className="font-mono text-fg-muted">UID: {r.uid}</p></div><button type="button" className="rounded border border-line px-2 py-1 hover:bg-hover" onClick={()=>removeLogRoot(i)}>{t('logs.composition.remove')}</button></li>)}</ul>
   </div>
   <footer className="flex justify-end gap-2 border-t border-line p-3"><button type="button" className="rounded border border-line px-3 py-1 hover:bg-hover" onClick={clearLogComposition}>{t('logs.composition.clear')}</button><button type="button" disabled={composition.refs.length<2} className="rounded bg-accent px-3 py-1 text-accent-fg disabled:opacity-50" onClick={()=>{dock.openComposedLogs(targetTitle,composition);close()}}>{t('logs.composition.open')}</button></footer>
  </section>
 </div>,document.body)
}
