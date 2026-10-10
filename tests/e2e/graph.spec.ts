import { expect } from '@playwright/test'
import { test, connectSelected, scratchRoot } from './fixtures'
import { join } from 'node:path'
import { token } from './synth'

async function graphPage(page:import('@playwright/test').Page){
 await page.goto('/')
 await page.getByRole('option',{name:/^demo\b/}).click()
 await connectSelected(page)
 const nav=page.getByRole('navigation',{name:'resources'})
 await nav.getByRole('textbox',{name:'Filter resources'}).fill('Cluster graph')
 await nav.getByRole('button',{name:'Cluster graph',exact:true}).click()
 await nav.getByRole('textbox',{name:'Filter resources'}).fill('')
 const graph=page.getByRole('region',{name:'Cluster graph'})
 await expect(graph.getByRole('button',{name:'Refresh',exact:true})).toBeEnabled()
 // The empty canvas can paint before the worker's layout is committed. Wait
 // for the layout effect and its scheduled frame before comparing the camera.
 await graph.locator('canvas').evaluate(()=>new Promise<void>(resolve=>requestAnimationFrame(()=>requestAnimationFrame(()=>resolve()))))
 await expect(graph.locator('canvas')).toHaveAttribute('data-zoom',/.+/)
 return graph
}
test('graph selects standard details, preserves camera when route layer changes, and scopes namespaces',async({page})=>{
 const graph=await graphPage(page),canvas=graph.locator('canvas')
 await expect(graph).toContainText('4 resources')
 const before=await canvas.getAttribute('data-zoom')
 await graph.getByRole('checkbox',{name:'Route layer'}).check()
 await expect(graph).toContainText('Declared routes, not measured traffic')
 expect(await canvas.getAttribute('data-zoom')).toBe(before)
 const box=(await canvas.boundingBox())!
 await page.mouse.move(box.x+box.width/2,box.y+box.height/2);await page.mouse.wheel(0,-180)
 await expect.poll(()=>canvas.getAttribute('data-zoom')).not.toBe(before)
 const find=graph.getByRole('textbox',{name:'Find a resource'})
 await find.fill('alpha');await find.press('Enter')
 const details=graph.locator('[data-area="details"]')
 await expect(details.getByText('alpha',{exact:true}).first()).toBeVisible()
 await expect(details.getByRole('tab',{name:'YAML',exact:true})).toBeVisible()
 await find.fill('')
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
 const nodes=Array.from({length:count},(_,i)=>({id:'uid-'+i,ref:{provider:'synthetic',target:'demo',kind:'services',scope:'ns-'+i%20,name:'service-'+i,uid:'uid-'+i},kindTitle:'Service',health:i%29===0?'warning':'ok'}))
 await page.route('**/api/ClusterGraph',route=>route.fulfill({json:{nodes,edges:nodes.slice(20).map((n,i)=>({source:nodes[i].id,target:n.id,type:i%4===0?'selects':'owns'})),problems:[],truncated:false,discovery:'ready',capturedAt:Date.now()}}))
 const graph=await graphPage(page),canvas=graph.locator('canvas')
 await expect(graph).toContainText(count+' resources')
 expect(await graph.locator('*').count()).toBeLessThan(150)
 const samples=[]
 for(let i=0;i<12;i++){
  const before=await canvas.getAttribute('data-zoom')
  await graph.getByRole('button',{name:'Zoom in',exact:true}).click()
  await expect.poll(()=>canvas.getAttribute('data-zoom')).not.toBe(before)
  await expect(canvas).toHaveAttribute('data-frame-ms',/.+/)
  samples.push(Number(await canvas.getAttribute('data-frame-ms')))
 }
 samples.sort((a,b)=>a-b)
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
  await page.locator('.language-menu button').click()
  await page.getByRole('option',{name,exact:true}).click()
  await expect(page.locator('html')).toHaveAttribute('lang',code)
  await expect(canvas).toHaveAttribute('data-identity',marker)
  await expect(graph.locator('input[type=checkbox]')).toBeChecked()
  await expect(graph.locator('[data-area=details]').getByText('alpha',{exact:true}).first()).toBeVisible()
  await page.screenshot({path:join(scratchRoot,'graph-language-'+code+'.png')})
 }
 await page.locator('.language-menu button').click()
 await page.getByRole('option',{name:'English',exact:true}).click()
})
