import { useCallback, useEffect, useId, useRef, useState, type ComponentProps } from 'react'
import { createPortal } from 'react-dom'

let visibleTooltip:{id:string;dismiss:()=>void}|null=null

/** Compact action with a stable accessible name and a descriptive hover/focus tooltip. */
export function ActionIconButton({label,description,children,className='',...props}: ComponentProps<'button'> & {label:string;description:string}) {
  const anchor=useRef<HTMLSpanElement>(null)
  const id=useId()
  const hideTimer=useRef<ReturnType<typeof setTimeout>|null>(null)
  const [tip,setTip]=useState<{left:number;top:number;above:boolean}|null>(null)
  const cancelHide=useCallback(()=>{if(hideTimer.current!==null){clearTimeout(hideTimer.current);hideTimer.current=null}},[])
  const hide=useCallback(()=>{cancelHide();setTip(null);if(visibleTooltip?.id===id)visibleTooltip=null},[cancelHide,id])
  const leave=()=>{cancelHide();if(anchor.current?.contains(document.activeElement))return;hideTimer.current=setTimeout(hide,100)}
  useEffect(()=>()=>{cancelHide();if(visibleTooltip?.id===id)visibleTooltip=null},[cancelHide,id])
  const show=()=>{
    cancelHide()
    visibleTooltip?.dismiss()
    visibleTooltip={id,dismiss:hide}
    const rect=anchor.current?.getBoundingClientRect()
    if(!rect)return
    const width=Math.min(320,window.innerWidth-16)
    const above=window.innerHeight-rect.bottom<100
    setTip({left:Math.max(8,Math.min(rect.left,window.innerWidth-width-8)),top:above?rect.top-6:rect.bottom+6,above})
  }
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
    {tip&&createPortal(<span id={id} role="tooltip" onMouseEnter={cancelHide} onMouseLeave={leave} className="action-icon-tooltip" style={{left:tip.left,top:tip.top,transform:tip.above?'translateY(-100%)':undefined}}>{description}</span>,document.body)}
  </span>
}
