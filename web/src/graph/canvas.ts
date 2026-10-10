import type { ClusterGraph, HealthState } from '../api/types'
import { fitCamera, NODE_H, NODE_W, routeEdge, zoomAt, type Camera, type Layout, type PlacedNode } from './model'

type Palette = {bg: string; panel: string; fg: string; muted: string; line: string; accent: string; route: string}
const healthColor: Record<HealthState, string> = {ok:'#69b38e',progressing:'#73a9df',warning:'#dfb266',error:'#e87986',terminating:'#ac92c7',unknown:'#7c899b'}
const GRID = 256
/** Imperative paint/camera loop: no React state or layout work during pan/zoom. */
export class GraphCanvas {
 camera: Camera = {x:0,y:0,zoom:1}
 lastFrameMs = 0
 private frame = 0
 private observer: ResizeObserver
 private width = 1
 private height = 1
 private layout: Layout = {nodes:[],regions:[],width:0,height:0}
 private graph: ClusterGraph | null = null
 private nodes = new Map<string, PlacedNode>()
 private grid = new Map<string, PlacedNode[]>()
 private selected: string | null = null
 private routes = false
 private filter = ''
 private hover: string | null = null
 private keyboard = -1
 private points = new Map<number, {x:number;y:number}>()
 private down: {x:number;y:number;camera:Camera;node:PlacedNode|null;dragged:boolean} | null = null
 private palette: Palette
 private cleanup: (() => void)[] = []
 private reduced = false

 constructor(private canvas: HTMLCanvasElement, private select: (node: PlacedNode|null) => void, private announce: (node: PlacedNode|null) => void, private clusterLabel: string) {
  const style = getComputedStyle(canvas)
  const color = (key:string, fallback:string) => style.getPropertyValue('--color-'+key).trim() || fallback
  this.palette = {bg:color('app','#10161f'),panel:color('navigation','#171e29'),fg:color('fg','#e8edf4'),muted:color('fg-muted','#a6b4c6'),line:color('line','#303c4c'),accent:color('accent','#9ebfeb'),route:'#5bc5c1'}
  this.reduced = matchMedia('(prefers-reduced-motion: reduce)').matches
  this.observer = new ResizeObserver(() => this.resize()); this.observer.observe(canvas)
  const listen = <K extends keyof HTMLElementEventMap>(type: K, handler:(event:HTMLElementEventMap[K])=>void, options?:AddEventListenerOptions) => {
   canvas.addEventListener(type, handler, options);this.cleanup.push(()=>canvas.removeEventListener(type,handler,options))
  }
  listen('wheel',e=>{e.preventDefault();const r=canvas.getBoundingClientRect();this.camera=zoomAt(this.camera,Math.exp(-Math.max(-120,Math.min(120,e.deltaY))*0.003),e.clientX-r.left,e.clientY-r.top);this.draw()}, {passive:false})
  listen('pointerdown',e=>{
   if (e.button!==0) return
   canvas.focus({preventScroll:true});canvas.setPointerCapture(e.pointerId)
   const p=this.point(e);this.points.set(e.pointerId,p)
   this.down={...p,camera:{...this.camera},node:this.hit(p.x,p.y),dragged:false}
  })
  listen('pointermove',e=>{
   const p=this.point(e)
   if (this.points.has(e.pointerId)) {
    const old=this.points.get(e.pointerId)!;this.points.set(e.pointerId,p)
    if (this.points.size===2) {
     const other=[...this.points.entries()].find(([id])=>id!==e.pointerId)![1]
     const before=Math.hypot(old.x-other.x,old.y-other.y),after=Math.hypot(p.x-other.x,p.y-other.y)
     if(before>2)this.camera=zoomAt(this.camera,after/before,(p.x+other.x)/2,(p.y+other.y)/2)
     this.camera.x+=(p.x-old.x)/2;this.camera.y+=(p.y-old.y)/2
     if(this.down)this.down.dragged=true
    } else if(this.down && (this.down.dragged || Math.hypot(p.x-this.down.x,p.y-this.down.y)>4)) {
     this.down.dragged=true
     this.camera={...this.down.camera,x:this.down.camera.x+p.x-this.down.x,y:this.down.camera.y+p.y-this.down.y}
    }
    this.draw()
   } else {
    const node=this.hit(p.x,p.y),id=node?.id??null
    if(id!==this.hover){this.hover=id;canvas.style.cursor=node?'pointer':'grab';canvas.title=node?node.kindTitle+' · '+(node.ref.title??node.ref.name):'';this.announce(node);this.draw()}
   }
  })
  const release=(e:PointerEvent,cancel=false)=>{
   const down=this.down
   this.points.delete(e.pointerId)
   if(canvas.hasPointerCapture(e.pointerId))canvas.releasePointerCapture(e.pointerId)
   if(!cancel && down && !down.dragged && !this.points.size)this.select(down.node)
   if(this.points.size===1){
    const point=[...this.points.values()][0]
    this.down={...point,camera:{...this.camera},node:null,dragged:true}
   } else this.down=null
  }
  listen('pointerup',e=>release(e))
  listen('pointercancel',e=>release(e,true))
  listen('lostpointercapture',e=>{if(this.points.has(e.pointerId)){this.points.delete(e.pointerId);this.down=null}})
  listen('pointerleave',()=>{if(!this.down){this.hover=null;this.announce(null);this.draw()}})
  listen('keydown',e=>{
   if(['+','=','-','0','Home','ArrowRight','ArrowLeft','ArrowUp','ArrowDown','Enter','Escape'].includes(e.key)){
    e.preventDefault();e.stopPropagation()
    if(e.key==='Home'||e.key==='0')this.fit()
    else if(e.key==='+'||e.key==='=')this.zoom(1.3)
    else if(e.key==='-')this.zoom(1/1.3)
    else if(e.key==='Escape')this.select(null)
    else if(e.key==='Enter' && this.keyboard>=0)this.select(this.layout.nodes[this.keyboard])
    else if(e.key.startsWith('Arrow') && this.layout.nodes.length) {
     this.keyboard=(this.keyboard+(e.key==='ArrowLeft'||e.key==='ArrowUp'?-1:1)+this.layout.nodes.length)%this.layout.nodes.length
     const node=this.layout.nodes[this.keyboard];this.hover=node.id;this.focus(node.id);this.announce(node)
    }
   }
  })
 }
 private point(e:PointerEvent){const r=this.canvas.getBoundingClientRect();return{x:e.clientX-r.left,y:e.clientY-r.top}}
 private resize(){
  const r=this.canvas.getBoundingClientRect();const first=this.width===1
  const oldWidth=this.width,oldHeight=this.height
  this.width=Math.max(1,r.width);this.height=Math.max(1,r.height)
  if(!first){this.camera.x+=(this.width-oldWidth)/2;this.camera.y+=(this.height-oldHeight)/2}
  const dpr=Math.min(2,devicePixelRatio||1)
  this.canvas.width=Math.round(this.width*dpr);this.canvas.height=Math.round(this.height*dpr)
  if(first&&this.layout.nodes.length)this.fit();else this.draw()
 }
 setGraph(graph:ClusterGraph,layout:Layout){
  const first=!this.graph;this.graph=graph;this.layout=layout;this.nodes=new Map(layout.nodes.map(n=>[n.id,n]));this.grid.clear()
  for(const n of layout.nodes){const key=Math.floor(n.x/GRID)+','+Math.floor(n.y/GRID);const list=this.grid.get(key)??[];list.push(n);this.grid.set(key,list)}
  if(first)this.fit();else this.draw()
 }
 setClusterLabel(label:string){this.clusterLabel=label;this.draw()}
 setOptions(selected:string|null,routes:boolean,filter:string){this.selected=selected;this.routes=routes;this.filter=filter.toLocaleLowerCase();this.draw()}
 fit(){this.camera=fitCamera(this.layout,this.width,this.height);this.draw()}
 zoom(factor:number){this.camera=zoomAt(this.camera,factor,this.width/2,this.height/2);this.draw()}
 focus(id:string){const n=this.nodes.get(id);if(n){this.camera={zoom:Math.max(0.9,this.camera.zoom),x:0,y:0};this.camera.x=this.width/2-(n.x+NODE_W/2)*this.camera.zoom;this.camera.y=this.height/2-(n.y+NODE_H/2)*this.camera.zoom;this.draw()}}
 hit(x:number,y:number):PlacedNode|null{
  x=(x-this.camera.x)/this.camera.zoom;y=(y-this.camera.y)/this.camera.zoom
  const gx=Math.floor(x/GRID),gy=Math.floor(y/GRID)
  for(let ix=gx-1;ix<=gx;ix++)for(let iy=gy-1;iy<=gy;iy++)for(const n of this.grid.get(ix+','+iy)??[])if(x>=n.x&&x<=n.x+NODE_W&&y>=n.y&&y<=n.y+NODE_H)return n
  return null
 }
 draw(){if(!this.frame)this.frame=requestAnimationFrame(()=>{this.frame=0;this.paint()})}
 private paint(){
  const start=performance.now(),ctx=this.canvas.getContext('2d')
  if(!ctx)return
  const {x,y,zoom}=this.camera,p=this.palette,dpr=Math.min(2,devicePixelRatio||1)
  ctx.setTransform(dpr,0,0,dpr,0,0);ctx.fillStyle=p.bg;ctx.fillRect(0,0,this.width,this.height)
  ctx.translate(x,y);ctx.scale(zoom,zoom)
  const left=-x/zoom,top=-y/zoom,right=left+this.width/zoom,bottom=top+this.height/zoom
  const visible=(x1:number,y1:number,x2:number,y2:number)=>x2>=left&&x1<=right&&y2>=top&&y1<=bottom
  for(const region of this.layout.regions) {
   if(!visible(region.x,region.y,region.x+region.width,region.y+region.height))continue
   ctx.fillStyle=p.panel;ctx.strokeStyle=p.line;ctx.lineWidth=1/zoom
   ctx.beginPath();if(typeof ctx.roundRect==='function')ctx.roundRect(region.x,region.y,region.width,region.height,4);else ctx.rect(region.x,region.y,region.width,region.height);ctx.fill();ctx.stroke()

  }
  const connected=new Set<string>()
  if(this.selected){connected.add(this.selected);for(const e of this.graph?.edges??[])if(e.source===this.selected||e.target===this.selected){connected.add(e.source);connected.add(e.target)}}
  const edgePath=(routes:boolean,highlight:boolean)=>{
   ctx.beginPath()
   for(const edge of this.graph?.edges??[]) {
    const isRoute=routeEdge(edge.type)
    if(routes && !isRoute)continue
    if(!routes && isRoute && this.routes)continue
    const selected=edge.source===this.selected||edge.target===this.selected
    if(highlight!==selected)continue
    if(zoom<.2 && this.layout.nodes.length>5000 && !selected && !routes)continue
    const a=this.nodes.get(edge.source),b=this.nodes.get(edge.target)
    if(!a||!b||!visible(Math.min(a.x,b.x),Math.min(a.y,b.y),Math.max(a.x,b.x)+NODE_W,Math.max(a.y,b.y)+NODE_H))continue
    const ax=a.x+NODE_W,ay=a.y+NODE_H/2,bx=b.x,by=b.y+NODE_H/2,dx=Math.max(40,Math.abs(ax-bx)*.45)
    ctx.moveTo(ax,ay);ctx.bezierCurveTo(ax+dx,ay,bx-dx,by,bx,by)
   }
   ctx.stroke()
  }
  ctx.lineWidth=1/zoom;ctx.strokeStyle=p.line;ctx.globalAlpha=this.selected?.35:.8;edgePath(false,false)
  if(this.routes){ctx.strokeStyle=p.route;ctx.globalAlpha=this.selected?.4:.8;ctx.lineWidth=1.5/zoom;ctx.setLineDash([8/zoom,6/zoom]);ctx.lineDashOffset=this.reduced?0:-(performance.now()/90)%14/zoom;edgePath(true,false);ctx.setLineDash([])}
  if(this.routes && zoom>.15){
   ctx.beginPath()
   for(const e of this.graph?.edges??[]){
    if(!routeEdge(e.type))continue
    const b=this.nodes.get(e.target)
    if(!b||!visible(b.x,b.y,b.x+NODE_W,b.y+NODE_H))continue
    const bx=b.x,by=b.y+NODE_H/2,size=5/zoom
    ctx.moveTo(bx,by);ctx.lineTo(bx-size*1.6,by-size);ctx.lineTo(bx-size*1.6,by+size);ctx.closePath()
   }
   ctx.fillStyle=p.route;ctx.globalAlpha=.9;ctx.fill()
  }
  ctx.globalAlpha=1;ctx.lineWidth=2/zoom;ctx.strokeStyle=p.accent;edgePath(false,true);if(this.routes){ctx.strokeStyle=p.route;edgePath(true,true)}
  const shown=this.layout.nodes.filter(n=>visible(n.x,n.y,n.x+NODE_W,n.y+NODE_H))
  for(const state of Object.keys(healthColor) as HealthState[]){
   ctx.beginPath()
   for(const n of shown)if(n.health===state){ctx.globalAlpha=1;ctx.rect(n.x,n.y,NODE_W,NODE_H)}
   ctx.fillStyle=p.bg;ctx.fill()
  }
  for(const n of shown){
   const active=n.id===this.selected||n.id===this.hover
   const matches=!this.filter||[n.ref.title??n.ref.name,n.kindTitle,n.ref.scope??''].some(s=>s.toLocaleLowerCase().includes(this.filter))
   ctx.globalAlpha=!matches?.15:(this.selected&&!connected.has(n.id)?.3:1)
   ctx.fillStyle=healthColor[n.health]??healthColor.unknown;ctx.fillRect(n.x,n.y,Math.max(3,1/zoom),NODE_H)
   if(active){ctx.strokeStyle=p.accent;ctx.lineWidth=2/zoom;ctx.strokeRect(n.x-2,n.y-2,NODE_W+4,NODE_H+4)}
   if(zoom>.42||active){
    ctx.fillStyle=p.muted;ctx.font='10px system-ui';ctx.fillText(n.kindTitle.slice(0,24),n.x+12,n.y+17)
    ctx.fillStyle=p.fg;ctx.font='13px system-ui';ctx.fillText((n.ref.title??n.ref.name).length>19?(n.ref.title??n.ref.name).slice(0,18)+'…':(n.ref.title??n.ref.name),n.x+12,n.y+35)
   }
  }
  ctx.globalAlpha=1
  for(const region of this.layout.regions){
   if(!visible(region.x,region.y,region.x+region.width,region.y+region.height))continue
   if(region.width*zoom>70){
    ctx.save();ctx.setTransform(dpr,0,0,dpr,0,0)
    ctx.fillStyle=p.muted;ctx.font='600 12px system-ui'
    const label=(region.name||this.clusterLabel)+'  ·  '+region.count
    const limit=Math.max(5,Math.floor((region.width*zoom-24)/7))
    ctx.fillText(label.length>limit?label.slice(0,limit-1)+'…':label,region.x*zoom+x+12,region.y*zoom+y+(zoom<.5?-8:22))
    ctx.restore()
   }
  }
  this.lastFrameMs=performance.now()-start
  this.canvas.dataset.cameraX=String(x);this.canvas.dataset.cameraY=String(y);this.canvas.dataset.zoom=String(zoom);this.canvas.dataset.frameMs=String(this.lastFrameMs)
  if(this.routes&&!this.reduced&&this.graph?.edges.some(e=>routeEdge(e.type)))this.frame=requestAnimationFrame(()=>{this.frame=0;this.paint()})
 }
 destroy(){this.observer.disconnect();this.cleanup.forEach(f=>f());if(this.frame)cancelAnimationFrame(this.frame)}
}
