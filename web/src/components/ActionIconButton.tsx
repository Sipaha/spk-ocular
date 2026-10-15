import { useCallback, useEffect, useLayoutEffect, useId, useRef, useState, type ComponentProps } from 'react'
import { createPortal } from 'react-dom'
import { TOOLTIP_DELAY } from '../tooltips'

let visibleTooltip:{id:string;dismiss:()=>void}|null=null

/** Compact action with a stable accessible name and a descriptive hover/focus tooltip. */
export function ActionIconButton({label,description,children,className='',...props}: ComponentProps<'button'> & {label:string;description:string}) {
  const anchor=useRef<HTMLSpanElement>(null)
  const tooltip=useRef<HTMLSpanElement>(null)
  const id=useId()
  const showTimer=useRef<ReturnType<typeof setTimeout>|null>(null)
  const cancelShow=useCallback(()=>{if(showTimer.current!==null){clearTimeout(showTimer.current);showTimer.current=null}},[])
  const hideTimer=useRef<ReturnType<typeof setTimeout>|null>(null)
  const [tip,setTip]=useState<{left:number;top:number;above:boolean}|null>(null)
  const cancelHide=useCallback(()=>{if(hideTimer.current!==null){clearTimeout(hideTimer.current);hideTimer.current=null}},[])
  const hide=useCallback(()=>{cancelShow();cancelHide();setTip(null);if(visibleTooltip?.id===id)visibleTooltip=null},[cancelHide,cancelShow,id])
  const leave=()=>{cancelShow();cancelHide();if(anchor.current?.contains(document.activeElement))return;hideTimer.current=setTimeout(hide,100)}
  useEffect(()=>()=>{cancelShow();cancelHide();if(visibleTooltip?.id===id)visibleTooltip=null},[cancelHide,cancelShow,id])
  const show=()=>{
    cancelHide()
    if(showTimer.current!==null||tip)return
    visibleTooltip?.dismiss()
    visibleTooltip={id,dismiss:hide}
    showTimer.current=setTimeout(()=>{
    showTimer.current=null
    const rect=anchor.current?.getBoundingClientRect()
    if(!rect)return
    const width=Math.min(320,window.innerWidth-16)
    const above=window.innerHeight-rect.bottom<100
    setTip({left:Math.max(8,Math.min(rect.left,window.innerWidth-width-8)),top:above?rect.top-6:rect.bottom+6,above})
    },TOOLTIP_DELAY)
  }
  useLayoutEffect(()=>{
    if(!tip||!tooltip.current||!anchor.current)return
    const rect=anchor.current.getBoundingClientRect(),size=tooltip.current.getBoundingClientRect()
    const above=window.innerHeight-rect.bottom<size.height+14&&rect.top>size.height+14
    const left=Math.max(8,Math.min(rect.left+(rect.width-size.width)/2,window.innerWidth-size.width-8))
    const top=above?rect.top-6:rect.bottom+6
    if(left!==tip.left||top!==tip.top||above!==tip.above)setTip({left,top,above})
  },[tip])
  useEffect(()=>{
    if(!tip)return
    const escape=(event:KeyboardEvent)=>{if(event.key==='Escape'){event.preventDefault();event.stopPropagation();hide()}}
    document.addEventListener('keydown',escape,true)
    window.addEventListener('resize',hide)
    window.addEventListener('scroll',hide,true)
    return()=>{document.removeEventListener('keydown',escape,true);window.removeEventListener('resize',hide);window.removeEventListener('scroll',hide,true)}
  },[tip,hide])
  return <span ref={anchor} className="inline-flex shrink-0" onMouseEnter={show} onMouseLeave={leave} onFocus={show} onBlur={hide} onClickCapture={hide} onContextMenuCapture={hide} onKeyDownCapture={event=>{
    if(event.key==='Escape'&&tip){event.preventDefault();event.stopPropagation();hide()}
  }}>
    <button {...props} type={props.type??'button'} aria-label={label} aria-describedby={id} className={'action-icon-button '+className}>{children}</button>
    {!tip&&<span id={id} className="sr-only">{description}</span>}
    {tip&&createPortal(<span ref={tooltip} id={id} role="tooltip" onMouseEnter={cancelHide} onMouseLeave={leave} className="action-icon-tooltip" style={{left:tip.left,top:tip.top,transform:tip.above?'translateY(-100%)':undefined}}>{description}</span>,document.body)}
  </span>
}
