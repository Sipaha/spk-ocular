/** Focus rings follow Tab navigation, independently of browser input heuristics. */
export function installFocusMode(doc: Document) {
 const root=doc.documentElement
 const keyboard=(event:KeyboardEvent)=>{
  if(event.key==='Tab'&&!event.ctrlKey&&!event.altKey&&!event.metaKey&&!event.isComposing)root.setAttribute('data-tab-focus','true')
 }
 const pointer=()=>root.removeAttribute('data-tab-focus')
 doc.addEventListener('keydown',keyboard,true)
 doc.addEventListener('pointerdown',pointer,true)
 return ()=>{doc.removeEventListener('keydown',keyboard,true);doc.removeEventListener('pointerdown',pointer,true);pointer()}
}
