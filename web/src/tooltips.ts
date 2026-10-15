export const TOOLTIP_DELAY = 800

/** Theme-owned hints replace native platform tooltips while the anchor is active. */
export function installTooltips(doc: Document) {
 const win=doc.defaultView!
 let anchor:HTMLElement|null=null,original:string|null=null,timer:number|undefined,tip:HTMLDivElement|null=null,shownText:string|null=null,hoverPoint:{x:number;y:number}|null=null
 const tooltipID='ocular-hover-tooltip'
 const dismiss=()=>{
  win.clearTimeout(timer);timer=undefined;tip?.remove();tip=null
  if(anchor){const ids=(anchor.getAttribute('aria-describedby')??'').split(/\s+/).filter(id=>id&&id!==tooltipID);if(ids.length)anchor.setAttribute('aria-describedby',ids.join(' '));else anchor.removeAttribute('aria-describedby')}
 }
 const hide=()=>{
  dismiss()
  anchor?.removeAttribute('data-app-tooltip-active')
  if(anchor&&original!==null&&!anchor.hasAttribute('title')&&!anchor.hasAttribute('data-tooltip'))anchor.setAttribute('title',original)
  anchor=null;original=null;shownText=null;hoverPoint=null
 }
 const show=(target:EventTarget|null,point?:{x:number;y:number})=>{
  const el=target instanceof win.Element?target.closest<HTMLElement>('[title],[data-cell-tooltip],[data-app-tooltip-active],[data-tooltip]'):null
  const cell=el?.hasAttribute('data-cell-tooltip'),dynamic=el?.hasAttribute('data-tooltip')
  const text=cell?el?.dataset.cellTooltip:dynamic?el?.dataset.tooltip:el?.getAttribute('title')??(el===anchor?original:null)
  if(el===anchor&&text===shownText){if(dynamic&&point)hoverPoint=point;if(!dynamic||tip||timer!==undefined)return}
  hide()
  if(!el||!text)return
  anchor=el;shownText=text;hoverPoint=dynamic?point??null:null;el.setAttribute('data-app-tooltip-active','')
  if(!cell&&!dynamic){original=text;el.removeAttribute('title')}
  timer=win.setTimeout(()=>{
   timer=undefined
   // One hovered cell only, immediately before display; no resize observers.
   const clipped=cell?(el.querySelector<HTMLElement>('.truncate')??el):null
   if(!el.isConnected||(clipped&&clipped.scrollWidth<=clipped.clientWidth)){hide();return}
   tip=doc.createElement('div');tip.className='app-tooltip';tip.id=tooltipID;tip.setAttribute('role','tooltip');tip.textContent=text
   el.setAttribute('aria-describedby',[el.getAttribute('aria-describedby'),tooltipID].filter(Boolean).join(' '))
   doc.body.append(tip)
   const r=hoverPoint?{left:hoverPoint.x+12,top:hoverPoint.y-6,bottom:hoverPoint.y+10}:el.getBoundingClientRect(),b=tip.getBoundingClientRect(),margin=8
   tip.style.left=`${Math.max(margin,Math.min(r.left,win.innerWidth-b.width-margin))}px`
   tip.style.top=`${Math.max(margin,r.bottom+6+b.height<=win.innerHeight-margin?r.bottom+6:r.top-b.height-6)}px`
  },TOOLTIP_DELAY)
 }
 const enter=(event:Event)=>{if(event.type==='focusin'&&event.target instanceof win.HTMLElement&&event.target.hasAttribute('data-tooltip'))return;show(event.target,event instanceof win.MouseEvent?{x:event.clientX,y:event.clientY}:undefined)}
 const move=(event:MouseEvent)=>{if(event.target instanceof win.HTMLElement&&event.target.hasAttribute('data-tooltip')){if(event.buttons){dismiss();return}show(event.target,{x:event.clientX,y:event.clientY})}}
 const leave=(event:MouseEvent)=>{if(anchor&&!anchor.contains(event.relatedTarget as Node|null))hide()}
 const escape=(event:KeyboardEvent)=>{if(event.key==='Escape'){if(tip){event.preventDefault();event.stopPropagation()}dismiss()}else if(anchor?.hasAttribute('data-tooltip'))hide()}
 doc.addEventListener('mousemove',move,true);doc.addEventListener('wheel',dismiss,{capture:true,passive:true});doc.addEventListener('mouseover',enter,true);doc.addEventListener('mouseout',leave,true)
 doc.addEventListener('focusin',enter,true);doc.addEventListener('focusout',hide,true)
 doc.addEventListener('pointerdown',dismiss,true);doc.addEventListener('keydown',escape,true)
 win.addEventListener('scroll',dismiss,true);win.addEventListener('resize',dismiss)
 return ()=>{
  hide();doc.removeEventListener('mousemove',move,true);doc.removeEventListener('wheel',dismiss,true);doc.removeEventListener('mouseover',enter,true);doc.removeEventListener('mouseout',leave,true)
  doc.removeEventListener('focusin',enter,true);doc.removeEventListener('focusout',hide,true)
  doc.removeEventListener('pointerdown',dismiss,true);doc.removeEventListener('keydown',escape,true)
  win.removeEventListener('scroll',dismiss,true);win.removeEventListener('resize',dismiss)
 }
}
