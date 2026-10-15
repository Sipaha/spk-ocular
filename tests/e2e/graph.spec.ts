import {layoutGraph,NODE_W,NODE_H} from '../../web/src/graph/model'
import { selectConnection, openAppMenu, connectionChoice } from './fixtures'
import { expect } from '@playwright/test'
import { test, connectSelected, scratchRoot } from './fixtures'
import { join } from 'node:path'
import { token } from './synth'

async function graphPage(page:import('@playwright/test').Page){
 await page.goto('/')
 await selectConnection(page,/^demo\b/)
 await connectSelected(page)
 const nav=page.getByRole('navigation',{name:'resources'})
 await nav.getByRole('textbox',{name:'Filter resources'}).fill('Cluster graph')
 await nav.getByRole('button',{name:'Cluster graph',exact:true}).click()
 await nav.getByRole('textbox',{name:'Filter resources'}).fill('')
 const graph=page.getByRole('region',{name:'Cluster graph'})
 await graph.getByRole('button',{name:'Load graph',exact:true}).click()
 await expect(graph.getByRole('button',{name:'Refresh',exact:true})).toBeEnabled()
 // The empty canvas can paint before the worker's layout is committed. Wait
 // for the layout effect and its scheduled frame before comparing the camera.
 await graph.locator('canvas').evaluate(()=>new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve()))))
 await expect(graph.locator('canvas')).toHaveAttribute('data-zoom',/.+/)
 return graph
}
test('graph selects standard details, preserves camera when route layer changes, and scopes namespaces',async({page})=>{
 let independentLayout:ReturnType<typeof layoutGraph>|undefined
 await page.route('**/api/ClusterGraph',async route=>{const response=await route.fetch(),snapshot=await response.json();independentLayout=layoutGraph(snapshot);await route.fulfill({response,json:snapshot})})
 const graph=await graphPage(page),canvas=graph.locator('canvas')
 await expect(graph).toContainText('4 resources')
 const before=await canvas.getAttribute('data-zoom')
 await graph.getByRole('checkbox',{name:'Route layer'}).check()
 await expect(graph).toContainText('Declared routes, not measured traffic')
 expect(await canvas.getAttribute('data-zoom')).toBe(before)
 const box=(await canvas.boundingBox())!
 await page.mouse.move(box.x+box.width/2,box.y+box.height/2);await page.mouse.wheel(0,-180)
 await expect.poll(()=>canvas.getAttribute('data-zoom')).not.toBe(before)
 const camera=()=>canvas.evaluate(el=>{const m=el.getContext('2d')!.getTransform();return{a:m.a,d:m.d,e:m.e,f:m.f}})
 const selectionCamera=await camera()
 const find=graph.getByRole('textbox',{name:'Find a resource'})
 await find.fill('alpha');await find.press('Enter')
 const details=graph.locator('[data-area="details"]')
 await expect(details.getByText('alpha',{exact:true}).first()).toBeVisible()
 await expect(details.getByRole('tab',{name:'YAML',exact:true})).toBeVisible()
 await canvas.evaluate(()=>new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve()))))
 expect(await camera()).toEqual(selectionCamera)
 await find.press('Enter');expect(await camera()).toEqual(selectionCamera)
 await canvas.focus();await page.keyboard.press('ArrowDown');await page.keyboard.press('Enter')
 await canvas.evaluate(()=>new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve()))))
 expect(await camera()).toEqual(selectionCamera)
 await find.fill('alpha');await find.press('Enter')
 await find.fill('')
 // Explicit Fit can move the camera; ordinary pointer selection cannot.
 await graph.getByRole('button',{name:'Fit graph',exact:true}).click()
 await canvas.evaluate(()=>new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve()))))
 const fitted=await camera(),bounds=(await canvas.boundingBox())!,dpr=await page.evaluate(()=>Math.min(2,devicePixelRatio||1))
 const alpha=independentLayout!.nodes.find(node=>(node.ref.title??node.ref.name)==='alpha')!
 expect(alpha).toBeTruthy()
 const point={x:bounds.x+(fitted.e+(alpha.x+NODE_W/2)*fitted.a)/dpr,y:bounds.y+(fitted.f+(alpha.y+NODE_H/2)*fitted.d)/dpr}
 await page.mouse.click(point.x,point.y)
 await canvas.evaluate(()=>new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve()))))
 expect(await camera()).toEqual(fitted)
 await page.mouse.move(point.x+4,point.y+3)
 await expect(canvas).not.toHaveAttribute('title')
 const hint=page.locator('.app-tooltip');await expect(hint).toContainText('alpha');await expect(hint).toHaveCSS('font-size','14px')
 const hintColors=await hint.evaluate(el=>{const probe=document.createElement('span');probe.style.background='var(--color-panel)';document.body.append(probe);const bg=getComputedStyle(el).backgroundColor,panel=getComputedStyle(probe).backgroundColor;probe.remove();return{bg,panel}})
 expect(hintColors.bg).toBe(hintColors.panel)
 await page.screenshot({path:join(scratchRoot,'graph-themed-node-tooltip.png')})
 await page.mouse.move(bounds.x+bounds.width-5,bounds.y+bounds.height-5)
 await page.screenshot({path:join(scratchRoot,'graph-details.png')})
 await graph.getByRole('checkbox',{name:'Route layer'}).uncheck()
 await expect(details.getByText('alpha',{exact:true}).first()).toBeVisible()
 const zone=graph.getByRole('textbox',{name:'Zone',exact:true})
 await zone.fill('blue, green');await zone.press('Enter')
 await expect(graph).toContainText('5 resources')
 await page.screenshot({path:join(scratchRoot,'graph-two-scopes.png')})
 const reply=await page.request.post('/api/ClusterGraph',{headers:{Authorization:'Bearer '+await token(page),Origin:new URL(page.url()).origin},data:{provider:'synthetic',target:'demo',scope:{mode:'some',names:[]}}})
 expect(reply.ok()).toBeTruthy()
 const empty=await reply.json();expect(empty.nodes.filter((n:{ref:{scope?:string}})=>n.ref.scope)).toEqual([])
})
for(const count of [10000,30000]) test(`canvas handles ${count} resources without a DOM node per resource or layout work on zoom`,async({page})=>{
 const nodes=Array.from({length:count},(_,i)=>({id:'uid-'+i,ref:{provider:'synthetic',target:'demo',kind:'services',scope:'ns-'+i%20,name:'service-'+i,uid:'uid-'+i},kindTitle:'Service',health:i%29===0?'warning' as const:'ok' as const}))
 const snapshot={nodes,edges:nodes.slice(20).map((n,i)=>({source:nodes[i].id,target:n.id,type:i%4===0?'selects':'owns'})),problems:[],truncated:false,discovery:'ready',capturedAt:Date.now()}
 const layout=layoutGraph(snapshot)
 await page.route('**/api/ClusterGraph',route=>route.fulfill({json:snapshot}))
 const graph=await graphPage(page),canvas=graph.locator('canvas')
 await expect(graph).toContainText(count+' resources')
 expect(await graph.locator('*').count()).toBeLessThan(150)
 // Observe actual drawing primitives, not a renderer-maintained node counter.
 // The independent layout supplies the geometry that must remain on screen.
 await canvas.evaluate((el,{nodeW,nodeH})=>{
  const ctx=el.getContext('2d')!,trace={strips:[] as [number,number][],matrix:null as {a:number;d:number;e:number;f:number}|null,width:el.width,height:el.height}
  for(const method of ['rect','fillRect'] as const){
   const original=ctx[method].bind(ctx)
   ctx[method]=(x:number,y:number,w:number,h:number)=>{
    if(method==='fillRect'&&x===0&&y===0&&h>nodeH){trace.strips=[];trace.matrix=null;const bounds=el.getBoundingClientRect(),dpr=Math.min(2,devicePixelRatio||1);trace.width=bounds.width*dpr;trace.height=bounds.height*dpr}
    if(h===nodeH&&w<nodeW){
     if(!trace.matrix){const m=ctx.getTransform();trace.matrix={a:m.a,d:m.d,e:m.e,f:m.f}}
     trace.strips.push([x,y])
    }
    return original(x,y,w,h)
   }
  }
  Object.assign(window,{graphPaintTrace:trace})
 },{nodeW:NODE_W,nodeH:NODE_H})
 const samples=[]
 for(let i=0;i<12;i++){
  const before=await canvas.getAttribute('data-zoom')
  await graph.getByRole('button',{name:'Zoom in',exact:true}).click()
  await expect.poll(()=>canvas.getAttribute('data-zoom')).not.toBe(before)
  await expect(canvas).toHaveAttribute('data-frame-ms',/.+/)
  samples.push(Number(await canvas.getAttribute('data-frame-ms')))
  const painted=await canvas.evaluate(()=>{
   const p=(window as unknown as {graphPaintTrace:{strips:[number,number][];matrix:{a:number;d:number;e:number;f:number};width:number;height:number}}).graphPaintTrace
   return {strips:p.strips,matrix:p.matrix,width:p.width,height:p.height}
  })
  const {a,d,e,f}=painted.matrix,left=-e/a,top=-f/d,right=(painted.width-e)/a,bottom=(painted.height-f)/d
  const expected=layout.nodes.filter(n=>n.x+NODE_W>=left&&n.x<=right&&n.y+NODE_H>=top&&n.y<=bottom).map(n=>`${n.x},${n.y}`).sort()
  expect(painted.strips.map(([x,y])=>`${x},${y}`).sort()).toEqual(expected)

 }
 samples.sort((a,b)=>a-b)
 console.log('graph zoom frame ms',JSON.stringify({count,samples,p90:samples[Math.floor(samples.length*.9)]}))
 expect(samples[Math.floor(samples.length*.9)]).toBeLessThan(32)
 await page.screenshot({path:join(scratchRoot,`graph-${count}.png`)})
 await graph.getByRole('checkbox',{name:'Route layer'}).check()
 await page.waitForTimeout(300)
 const routeMs=Number(await canvas.getAttribute('data-frame-ms'))
 expect(routeMs).toBeLessThan(32)
 console.log('Graph paint measurements:',{p90:samples[Math.floor(samples.length*.9)],routes:routeMs,nodes:nodes.length})
 await page.screenshot({path:join(scratchRoot,`graph-${count}-routes.png`)})
 await page.unrouteAll({behavior:'wait'})
})

test('graph language changes preserve canvas, route layer and selection in all eight locales',async({page})=>{
 await graphPage(page)
 const graph=page.locator('.graph-workspace'),canvas=graph.locator('canvas')
 await graph.getByRole('checkbox',{name:'Route layer'}).check()
 await graph.getByRole('textbox',{name:'Find a resource'}).fill('alpha')
 await graph.getByRole('textbox',{name:'Find a resource'}).press('Enter')
 const marker=await canvas.evaluate(el=>{el.dataset.identity='graph-identity';return el.dataset.identity})
 const names={en:'English',ru:'Русский',zh:'简体中文',es:'Español',de:'Deutsch',fr:'Français',pt:'Português (Brasil)',ja:'日本語'}
 for(const [code,name] of Object.entries(names)){
  await openAppMenu(page);await page.locator('.language-menu button').click()
  await page.getByRole('option',{name,exact:true}).click()
  await expect(page.locator('html')).toHaveAttribute('lang',code)
  await expect(canvas).toHaveAttribute('data-identity',marker)
  await expect(graph.locator('input[type=checkbox]')).toBeChecked()
  await expect(graph.locator('[data-area=details]').getByText('alpha',{exact:true}).first()).toBeVisible()
  await page.screenshot({path:join(scratchRoot,'graph-language-'+code+'.png')})
 }
 await openAppMenu(page);await page.locator('.language-menu button').click()
 await page.getByRole('option',{name:'English',exact:true}).click()
})


test('graph waits for explicit loading per session, retains warm opt-in and resets on reconnect and reload',async({page})=>{
 let calls=0
 const workers:string[]=[]
 page.on('worker',worker=>workers.push(worker.url()))
 await page.route('**/api/ClusterGraph',async route=>{calls++;await route.continue()})
 await page.goto('/');await selectConnection(page,/^demo\b/);await connectSelected(page)
 const nav=page.getByRole('navigation',{name:'resources'})
 const openGraph=async()=>{
  await nav.getByRole('textbox',{name:'Filter resources'}).fill('Cluster graph')
  await nav.getByRole('button',{name:'Cluster graph',exact:true}).click()
  await nav.getByRole('textbox',{name:'Filter resources'}).fill('')
 }
 await openGraph()
 const graph=page.locator('.graph-workspace'),load=graph.getByRole('button',{name:'Load graph',exact:true})
 await expect(load).toBeVisible();await expect(graph.locator('canvas')).toHaveCount(0)
 const labels={en:['English','Load graph'],ru:['Русский','Загрузить граф'],zh:['简体中文','加载关系图'],es:['Español','Cargar grafo'],de:['Deutsch','Graph laden'],fr:['Français','Charger le graphe'],pt:['Português (Brasil)','Carregar grafo'],ja:['日本語','グラフを読み込む']}
 for(const [code,[language,label]] of Object.entries(labels)){
  await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:language,exact:true}).click()
  await expect(graph.getByRole('button',{name:label,exact:true})).toBeVisible();expect(calls).toBe(0)
  await page.screenshot({path:join(scratchRoot,'graph-load-language-'+code+'.png')})
 }
 await openAppMenu(page);await page.locator('.language-menu button').click();await page.getByRole('option',{name:'English',exact:true}).click()
 // Scope changes before consent do not start the inventory or layout worker.
 const zone=graph.getByRole('textbox',{name:'Zone',exact:true})
 await zone.fill('blue, green');await zone.press('Enter')
 await expect(load).toBeVisible();expect(calls).toBe(0);expect(workers.filter(url=>url.includes('layout.worker'))).toEqual([])
 await page.screenshot({path:join(scratchRoot,'graph-explicit-load.png')})
 await load.click();await expect(graph).toContainText('5 resources');expect(calls).toBe(1)
 await nav.getByRole('button',{name:'Services',exact:true}).click();await openGraph()
 await expect(load).toHaveCount(0);await expect(graph).toContainText('5 resources');expect(calls).toBe(2)
 // Another warm target has its own opt-in, while returning to A keeps A's.
 await selectConnection(page,/^demo2\b/);await connectSelected(page);await openGraph()
 await expect(load).toBeVisible();expect(calls).toBe(2)
 await selectConnection(page,/^demo\b/);await connectSelected(page)
 await expect(load).toHaveCount(0);await expect(graph).toContainText('5 resources');expect(calls).toBe(3)
 const selected=await connectionChoice(page,/^demo\b/)
 await selected.click({button:'right'});await page.getByRole('menuitem',{name:'Disconnect',exact:true}).click()
 await page.keyboard.press('Escape');await expect(page.getByRole('button',{name:'Connect',exact:true})).toBeVisible()
 await connectSelected(page);await expect(load).toBeVisible();expect(calls).toBe(3)
 await load.click();await expect(graph).toContainText('5 resources');expect(calls).toBe(4)
 await page.reload();await expect(load).toBeVisible();expect(calls).toBe(4)
 await load.click();await expect(graph).toContainText('5 resources');expect(calls).toBe(5)
})
