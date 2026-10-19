import { useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import type { CompareSelection } from '../api/types'
import { ActionIconButton } from '../components/ActionIconButton'
import { ActionIcon } from '../components/icons'
import { ModalCloseButton, useModalFocus } from '../components/modal'
import { t } from '../i18n'
import { refTitle } from '../refs'
import { useStore } from '../store'
import { clearComparison, selectionAvailable, useComparison } from './store'

/** A permanent header slot: pinning, inspecting and clearing never move content. */
export function BaselineControl() {
 const baseline=useComparison(state=>state.baseline)
 const view=useStore(state=>state.view)
 const [opened,setOpened]=useState<{selection:CompareSelection|null}|null>(null)
 const open=opened!==null&&opened.selection===baseline
 const available=baseline&&selectionAvailable(baseline,view)
 const description=baseline?`${baseline.targetTitle} · ${baseline.ref.scope?baseline.ref.scope+'/':''}${refTitle(baseline.ref)} · ${available?t('compare.pickOther'):t('compare.connectionChanged')}`:t('compare.noBaseline',{pin:t('compare.pin'),compare:t('compare.with')})
 return <>
  <ActionIconButton label={t('compare.baseline')} description={description} aria-haspopup="dialog" aria-expanded={open} className={'h-7 w-7 '+(baseline?(available?'text-accent bg-active':'text-warning'):'text-fg-muted')} onClick={()=>setOpened(open?null:{selection:baseline})}><ActionIcon name="baseline"/></ActionIconButton>
  {open&&<BaselinePopover baseline={baseline} available={!!available} close={()=>setOpened(null)}/>}
 </>
}
function BaselinePopover({baseline,available,close}:{baseline:CompareSelection|null;available:boolean;close:()=>void}) {
 const box=useRef<HTMLElement>(null),closeButton=useRef<HTMLButtonElement>(null)
 const modalKey=useModalFocus(box,close,closeButton)
 return createPortal(<div className="fixed inset-0 z-40" onMouseDown={event=>{if(event.target===event.currentTarget)close()}}>
  <section ref={box} role="dialog" aria-modal="true" aria-label={t('compare.baseline')} data-comparison-baseline onKeyDown={modalKey} className="absolute right-3 top-12 w-[min(480px,calc(100vw-24px))] rounded border border-line bg-panel p-3 text-sm shadow-floating">
   <header className="mb-3 flex items-center gap-3"><strong className="min-w-0 flex-1">{t('compare.baseline')}</strong><ModalCloseButton ref={closeButton} label={t('drawer.close')} onClick={close}/></header>
   {baseline?<>
    <p className="break-all">{baseline.targetTitle} · {baseline.ref.scope?baseline.ref.scope+'/':''}{refTitle(baseline.ref)} · {baseline.ref.kind}</p>
    {!available&&<p className="mt-2 text-warning">{t('compare.connectionChanged')}</p>}
    <p className="mt-2 text-fg-muted">{t('compare.pickOther')}</p>
    <button type="button" className="mt-3 rounded border border-line px-2 py-1 text-xs hover:bg-hover" onClick={()=>{clearComparison();close()}}>{t('compare.clear')}</button>
   </>:<p className="text-fg-muted">{t('compare.noBaseline',{pin:t('compare.pin'),compare:t('compare.with')})}</p>}
  </section>
 </div>,document.body)
}
