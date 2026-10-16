import { openAppMenu } from './fixtures'
import { expect } from '@playwright/test'
import { test, scratchRoot } from './fixtures'
import { selectObject } from './synth'
import { join } from 'node:path'
import { languageNames, languages } from '../../web/src/languages'

test('container files: directories, highlighted editing, save, conflict and discard', async ({ page }) => {
 let contents='server:\n  port: 8080\n'; let version='original'; const requests: Record<string,unknown>[]=[]
 await page.route('**/api/Files',async route=>{
  const req=route.request().postDataJSON();requests.push(req)
  if(req.command==='list') return route.fulfill({json:{path:req.path,configRev:'fixture',text:'',entries:req.path==='/'?[{name:'etc',directory:true,symlink:false}]:[{name:'config.yaml',directory:false,symlink:false},{name:'.env',directory:false,symlink:false}]}})
  if(req.command==='read') return route.fulfill({json:{path:req.path,configRev:'fixture',text:contents,version}})
  if(req.expect!==version) return route.fulfill({status:409,json:{code:'conflict',detail:'File changed externally; reopen it before saving'}})
  contents=req.text; version='saved';return route.fulfill({json:{path:req.path,configRev:'fixture',text:contents,version}})
 })
 await selectObject(page,'api')
 await page.getByRole('button',{name:'Files',exact:true}).click()
 const dialog=page.getByRole('dialog',{name:'Files',exact:true})
 await dialog.getByRole('button',{name:'Expand /etc',exact:true}).click()
 await dialog.getByRole('treeitem',{name:'config.yaml',exact:true}).dblclick()
 const editor=dialog.locator('.cm-content')
 await expect(editor).toContainText('port: 8080')
 await expect(editor.locator('span').first()).toBeVisible()
 await editor.click();await page.keyboard.press('Control+End');await page.keyboard.type('enabled: true')
 await page.screenshot({path:join(scratchRoot,'container-files.png')})
 await editor.dispatchEvent('keydown',{key:'ы',code:'KeyS',ctrlKey:true,bubbles:true});await expect(dialog).toContainText('Saved to container')
 expect(contents).toContain('enabled: true');expect(requests.at(-1)?.configRev).toBe('fixture')
 await editor.click();await page.keyboard.type('\nnew: value')
 await editor.dispatchEvent('keydown',{key:'я',code:'KeyZ',ctrlKey:true,bubbles:true})
 await expect(editor).not.toContainText('new: value')
 await editor.dispatchEvent('keydown',{key:'н',code:'KeyY',ctrlKey:true,bubbles:true})
 await expect(editor).toContainText('new: value')
 await editor.dispatchEvent('keydown',{key:'а',code:'KeyF',ctrlKey:true,bubbles:true})
 await expect(dialog.locator('.cm-search')).toBeVisible()
 await dialog.locator('.cm-search input').first().dispatchEvent('keydown',{key:'Escape',code:'Escape',bubbles:true})
 await expect(dialog.locator('.cm-search')).toHaveCount(0)
 version='external'
 const geometry=await dialog.locator('[role=tree], [data-file-editor-panel]').evaluateAll(els=>els.map(el=>{const r=el.getBoundingClientRect();return [r.x,r.y,r.width,r.height]}))
 await page.keyboard.press('Control+s');await expect(dialog.getByRole('alert')).toContainText('changed externally')
 expect(await dialog.locator('[role=tree], [data-file-editor-panel]').evaluateAll(els=>els.map(el=>{const r=el.getBoundingClientRect();return [r.x,r.y,r.width,r.height]}))).toEqual(geometry)
 await page.screenshot({path:join(scratchRoot,'files-error-overlay.png')})
 await dialog.getByRole('button',{name:'Dismiss error',exact:true}).click()
 await expect(dialog.getByRole('alert')).toHaveCount(0)
 await expect(editor).toContainText('new: value')
 await dialog.getByRole('button',{name:'Close',exact:true}).click()
 const discard=page.getByRole('alertdialog')
 await expect(discard).toBeVisible()
 await discard.getByRole('button',{name:/keep editing/i}).click()
 await expect(editor).toContainText('new: value')
})

test('file tree: compact icons, read-only location, double-click, footer type and folder refresh preserve the draft', async ({ page }) => {
 await page.route('**/api/ExecInfo', route=>route.fulfill({json:{defaultInstance:'pod',instances:[{id:'pod',title:'api-pod',ready:true,defaultChannel:'app',channels:[{id:'app',title:'app',running:true}]}]}}))
 let refreshed=false
 const fileCalls: { command: string; path: string }[]=[]
 await page.route('**/api/Files',async route=>{
  const request=route.request().postDataJSON();fileCalls.push(request)
  const entries=request.path==='/'?
   [{name:'app',directory:true,symlink:false},{name:'etc',directory:true,symlink:false},{name:'README.md',directory:false,symlink:false}]:
   request.path==='/etc'? [{name:'config.yaml',directory:false,symlink:false},...(refreshed?[{name:'new.conf',directory:false,symlink:false}]:[])]:
   [{name:'nested',directory:true,symlink:false}]
  await route.fulfill({json:{path:request.path,configRev:'fixture',entries:request.command==='list'?entries:undefined,text:request.command==='read'?'port: 8080\n':'',version:'original'}})
 })
 await selectObject(page,'api')
 const divider=(await page.getByRole('separator',{name:'Resize resource navigation',exact:true}).boundingBox())!
 await page.getByRole('button',{name:'Files',exact:true}).click()
 const dialog=page.getByRole('dialog',{name:'Files',exact:true})
 await expect(dialog.locator('[data-file-context]')).toContainText('api / api-pod / app')
 await expect(dialog.getByRole('button',{name:'Instance',exact:true})).toHaveCount(0)
 await expect(dialog.getByRole('button',{name:'Channel',exact:true})).toHaveCount(0)
 await expect(dialog.getByRole('button',{name:'↑',exact:true})).toHaveCount(0)
 await dialog.getByRole('button',{name:'Expand /etc',exact:true}).click()
 const file=dialog.getByRole('treeitem',{name:'config.yaml',exact:true})
 await expect(file).toHaveAttribute('aria-level','3')
 expect((await file.boundingBox())!.height).toBe(24)
 await expect(file.locator('svg')).toHaveCount(1)
 await file.click()
 await expect(dialog.locator('.cm-editor')).toHaveCount(0)
 expect(fileCalls.filter(call=>call.command==='read')).toHaveLength(0)
 await file.dblclick()
 const editor=dialog.locator('.cm-content')
 await expect(editor).toContainText('port: 8080')
 await expect(dialog.locator('[data-file-footer]').getByRole('button',{name:'Language',exact:true})).toContainText('YAML')
 await expect(dialog.locator('header').getByRole('button',{name:'Language',exact:true})).toHaveCount(0)
 await editor.click();await page.keyboard.press('Control+End');await page.keyboard.type('enabled: true')
 await dialog.getByRole('button',{name:'Expand /app',exact:true}).click()
 await expect(dialog.getByRole('treeitem',{name:'nested',exact:true})).toBeVisible()
 await expect(file).toBeVisible()
 refreshed=true
 await dialog.getByRole('treeitem',{name:'etc',exact:true}).click({button:'right'})
 await page.getByRole('menu',{name:'/etc',exact:true}).getByRole('menuitem',{name:'Refresh',exact:true}).click()
 await expect(dialog.getByRole('treeitem',{name:'new.conf',exact:true})).toBeVisible()
 await expect(editor).toContainText('enabled: true')
 await file.click({button:'right'})
 const fileMenu=page.getByRole('menu',{name:'/etc/config.yaml',exact:true})
 await expect(fileMenu.getByRole('menuitem',{name:'Refresh',exact:true})).toHaveCount(0)
 await expect(fileMenu.getByRole('menuitem',{name:'Download',exact:true})).toBeVisible()
 await page.screenshot({path:join(scratchRoot,'container-file-download-menu.png')})
 await fileMenu.getByRole('menuitem',{name:'Download',exact:true}).click()
 await expect(dialog.getByRole('alert')).toContainText('desktop application')
 await expect(editor).toContainText('enabled: true')
 await dialog.getByRole('treeitem',{name:'etc',exact:true}).click({button:'right'})
 await expect(page.getByRole('menu',{name:'/etc',exact:true}).getByRole('menuitem',{name:'Download',exact:true})).toBeVisible()
 await page.keyboard.press('Escape')
 await expect(page.getByRole('alertdialog')).toHaveCount(0)
 await dialog.getByRole('button',{name:'Collapse /etc',exact:true}).click()
 await expect(file).toHaveCount(0)
 await expect(editor).toContainText('enabled: true')
 await dialog.getByRole('button',{name:'Expand /etc',exact:true}).click()
 const point={x:divider.x+divider.width/2,y:(await dialog.boundingBox())!.y+160}
 expect(await page.evaluate(({x,y})=>!!document.elementFromPoint(x,y)?.closest('[data-file-overlay]'),point)).toBe(true)
 await page.mouse.move(point.x,point.y)
 await page.screenshot({path:join(scratchRoot,'container-file-tree.png')})
})

test('first folder load, retained cache, explicit refresh, file loading and symlink navigation', async ({page}) => {
 let releaseFirst!:()=>void, releaseRefresh!:()=>void, releaseRead!:()=>void
 const first=new Promise<void>(resolve=>{releaseFirst=resolve})
 const refresh=new Promise<void>(resolve=>{releaseRefresh=resolve})
 const read=new Promise<void>(resolve=>{releaseRead=resolve})
 let lists=0,reads=0
 const calls:{command:string;path:string}[]=[]
 await page.route('**/api/Files',async route=>{
  const req=route.request().postDataJSON();calls.push(req)
  if(req.command==='resolve') return route.fulfill({json:{path:req.path==='/config-link'?'/etc/config.yaml':'/etc',configRev:'fixture',text:''}})
  if(req.command==='read') { if(++reads===1) await read;return route.fulfill({json:{path:req.path,configRev:'fixture',text:'port: 8080\n',version:'original'}}) }
  if(req.path==='/etc') { if(++lists===1) await first;else await refresh }
  const entries=req.path==='/'?[
   {name:'etc',directory:true,symlink:false},
   {name:'config-link',directory:false,symlink:true,target:'/etc/config.yaml'},
   {name:'etc-link',directory:true,symlink:true,target:'etc'},
  ]:[{name:'config.yaml',directory:false,symlink:false},...(lists>1?[{name:'new.conf',directory:false,symlink:false}]:[])]
  await route.fulfill({json:{path:req.path,configRev:'fixture',text:'',entries}})
 })
 await selectObject(page,'api')
 await page.getByRole('button',{name:'Files',exact:true}).click()
 const dialog=page.getByRole('dialog',{name:'Files',exact:true})
 await dialog.getByRole('button',{name:'Expand /etc',exact:true}).click()
 const folder=dialog.getByRole('treeitem',{name:'etc',exact:true})
 await expect(dialog.locator('[data-file-loading-child]')).toHaveCount(1)
 await expect(dialog.locator('[data-file-loading-child] [role=status]')).toBeVisible()
 await expect(folder.locator('[data-file-refresh-loading]')).toHaveCount(0)
 await page.screenshot({path:join(scratchRoot,'files-folder-first-loading.png')})
 releaseFirst()
 const file=dialog.getByRole('treeitem',{name:'config.yaml',exact:true})
 await expect(file).toBeVisible()
 await expect(dialog.locator('[data-file-loading-child]')).toHaveCount(0)
 await dialog.getByRole('button',{name:'Collapse /etc',exact:true}).click()
 await dialog.getByRole('button',{name:'Expand /etc',exact:true}).click()
 await expect(file).toBeVisible()
 expect(lists).toBe(1)
 await folder.click({button:'right'})
 await page.getByRole('menu',{name:'/etc',exact:true}).getByRole('menuitem',{name:'Refresh',exact:true}).click()
 await expect(folder.locator('[data-file-refresh-loading]')).toBeVisible()
 await expect(file).toBeVisible()
 await expect(dialog.locator('[data-file-loading-child]')).toHaveCount(0)
 await page.screenshot({path:join(scratchRoot,'files-folder-refresh-loading.png')})
 releaseRefresh()
 await expect(dialog.getByRole('treeitem',{name:'new.conf',exact:true})).toBeVisible()
 await file.dblclick()
 await expect(dialog.locator('[data-file-read-loading]')).toContainText('Loading /etc/config.yaml')
 await expect(dialog.locator('[data-file-editor-panel]')).toHaveAttribute('aria-busy','true')
 expect(await page.evaluate(()=>window.getSelection()?.toString())).toBe('')
 await page.screenshot({path:join(scratchRoot,'files-file-loading.png')})
 releaseRead()
 await expect(dialog.locator('.cm-content')).toContainText('port: 8080')
 await expect(dialog.locator('[data-file-read-loading]')).toHaveCount(0)
 const link=dialog.getByRole('treeitem',{name:'config-link',exact:true})
 await expect(link).toHaveAttribute('title','/config-link\nPoints to /etc/config.yaml')
 const arrow=link.locator('[data-file-link-indicator] svg')
 await expect(arrow).toBeVisible()
 expect((await arrow.boundingBox())!.width).toBe(14)
 expect((await arrow.boundingBox())!.height).toBe(14)
 await link.click({button:'right'})
 await page.getByRole('menu',{name:'/config-link',exact:true}).getByRole('menuitem',{name:'Go to target',exact:true}).click()
 await expect(file).toHaveAttribute('aria-selected','true')
 expect(calls.some(call=>call.command==='resolve'&&call.path==='/config-link')).toBe(true)
 const editor=dialog.locator('.cm-content')
 await editor.click();await page.keyboard.press('Control+End');await page.keyboard.type('draft: keep')
 const resolves=calls.filter(call=>call.command==='resolve').length
 await link.click({button:'right'})
 await page.getByRole('menu',{name:'/config-link',exact:true}).getByRole('menuitem',{name:'Go to target',exact:true}).click()
 const discard=page.getByRole('alertdialog')
 await expect(discard).toBeVisible()
 await discard.getByRole('button',{name:/keep editing/i}).click()
 await expect(editor).toContainText('draft: keep')
 expect(calls.filter(call=>call.command==='resolve')).toHaveLength(resolves)
 const dirLink=dialog.getByRole('treeitem',{name:'etc-link',exact:true})
 await expect(dirLink).toHaveAttribute('title','/etc-link\nPoints to etc')
 await dirLink.click({button:'right'})
 await page.getByRole('menu',{name:'/etc-link',exact:true}).getByRole('menuitem',{name:'Go to target',exact:true}).click()
 await expect(folder).toHaveAttribute('aria-selected','true')
 expect(lists).toBe(2)
 const sizes=await dialog.evaluate(el=>Object.fromEntries(['header','address','document-header'].map(key=>[key,el.querySelector(`[data-file-${key}]`)?.getBoundingClientRect().height])))
 expect(sizes).toEqual({header:35,address:35,'document-header':35})
 const gutters=await dialog.evaluate(el=>['header','address','document-header'].flatMap(key=>{
  const bar=el.querySelector<HTMLElement>(`[data-file-${key}]`)!
  const bounds=bar.getBoundingClientRect()
  const border=parseFloat(getComputedStyle(bar).borderBottomWidth)
  return [...bar.querySelectorAll('button,input')].map(control=>{
   const rect=control.getBoundingClientRect()
   return [rect.top-bounds.top,bounds.bottom-border-rect.bottom]
  })
 }))
 for(const gutter of gutters) expect(gutter).toEqual([4,4])
 const fonts=await file.evaluate(el=>getComputedStyle(el).fontFamily)
 expect(fonts).toBe(await dialog.locator('.cm-scroller').evaluate(el=>getComputedStyle(el).fontFamily))
 await page.screenshot({path:join(scratchRoot,'files-compact-link-navigation.png')})
})

test('tree resize, edge scrollbar and compact centered footer preserve the draft', async ({page}) => {
 await page.route('**/api/Files',async route=>{
  const req=route.request().postDataJSON()
  await route.fulfill({json:{path:req.path,configRev:'fixture',text:req.command==='read'?'port: 8080\n':'',version:'original',entries:req.command==='list'?Array.from({length:200},(_,i)=>({name:`file-${String(i).padStart(3,'0')}.yaml`,directory:false,symlink:false})):undefined}})
 })
 await selectObject(page,'api')
 await page.getByRole('button',{name:'Files',exact:true}).click()
 const dialog=page.getByRole('dialog',{name:'Files',exact:true})
 const tree=dialog.getByRole('tree')
 await tree.getByRole('treeitem',{name:'file-000.yaml',exact:true}).dblclick()
 const editor=dialog.locator('.cm-content')
 await editor.click();await page.keyboard.press('Control+End');await page.keyboard.type('draft: keep')
 const separator=dialog.getByRole('separator',{name:'Resize file tree',exact:true})
 const before=(await tree.boundingBox())!
 const boundary=(await separator.boundingBox())!
 expect(boundary.x).toBeCloseTo(before.x+before.width,1)
 await page.mouse.move(boundary.x+boundary.width/2,boundary.y+100)
 await page.mouse.down();await page.mouse.move(boundary.x+boundary.width/2+100,boundary.y+100);await page.mouse.up()
 expect((await tree.boundingBox())!.width).toBeCloseTo(before.width+100,1)
 await expect(editor).toContainText('draft: keep')
 const after=(await separator.boundingBox())!
 await page.mouse.move(after.x+after.width/2,after.y+100)
 await page.mouse.down();await page.mouse.move(after.x+after.width/2-70,after.y+100)
 await page.keyboard.press('Escape');await page.mouse.up()
 await expect(dialog).toBeVisible()
 expect((await tree.boundingBox())!.width).toBeCloseTo(before.width+100,1)
 await separator.focus();await page.keyboard.press('ArrowLeft')
 expect((await tree.boundingBox())!.width).toBeCloseTo(before.width+80,1)
 await tree.focus();await page.keyboard.press('Alt+Shift+ArrowRight')
 expect((await tree.boundingBox())!.width).toBeCloseTo(before.width+100,1)
 await expect(tree).toBeFocused()
 await page.keyboard.press('Alt+Shift+ArrowLeft')
 expect((await tree.boundingBox())!.width).toBeCloseTo(before.width+80,1)
 await editor.click();await page.keyboard.press('Alt+Shift+ArrowLeft')
 expect((await tree.boundingBox())!.width).toBeCloseTo(before.width+80,1)
 await expect(editor).toContainText('draft: keep')

 const footer=dialog.locator('[data-file-footer]')
 const centered=await footer.evaluate(el=>{
  const rect=el.getBoundingClientRect(),border=parseFloat(getComputedStyle(el).borderTopWidth)
  return {height:rect.height,gaps:[...el.children].map(child=>{const r=child.getBoundingClientRect();return [r.top-rect.top-border,rect.bottom-r.bottom]})}
 })
 expect(centered.height).toBe(27)
 for(const [top,bottom] of centered.gaps) expect(top).toBeCloseTo(bottom,4)
 expect(centered.gaps).toEqual([[4,4],[2,2]])
 const scroll=await tree.evaluate(el=>({width:el.getBoundingClientRect().width-el.clientWidth-parseFloat(getComputedStyle(el).borderRightWidth),height:el.scrollHeight,viewport:el.clientHeight}))
 expect(scroll.height).toBeGreaterThan(scroll.viewport)
 expect(scroll.width).toBe(8)
 const bounds=(await tree.boundingBox())!
 const thumb={x:bounds.x+bounds.width-5,y:bounds.y+18}
 expect(await page.evaluate(({x,y})=>!document.elementFromPoint(x,y)?.closest('[role=separator]'),thumb)).toBe(true)
 await page.mouse.move(thumb.x,thumb.y)
 await page.screenshot({path:join(scratchRoot,'files-scrollbar-resize-footer.png')})
 await page.mouse.down();await page.mouse.move(thumb.x,thumb.y+140);await page.mouse.up()
 await expect.poll(()=>tree.evaluate(el=>el.scrollTop)).toBeGreaterThan(100)
 await expect(editor).toContainText('draft: keep')
})

test('empty folders keep a placeholder row and loaded folders retain counts while collapsed', async ({page}) => {
 let release!:()=>void
 const loaded=new Promise<void>(resolve=>{release=resolve})
 let reads=0
 await page.route('**/api/Files',async route=>{
  const req=route.request().postDataJSON()
  if(req.path==='/empty') {reads++;await loaded}
  await route.fulfill({json:{path:req.path,configRev:'fixture',text:'',entries:req.path==='/'?[{name:'empty',directory:true,symlink:false},{name:'neighbor',directory:true,symlink:false}]:req.path==='/neighbor'?[{name:'child.txt',directory:false,symlink:false}]:[]}})
 })
 await selectObject(page,'api');await page.getByRole('button',{name:'Files',exact:true}).click()
 const dialog=page.getByRole('dialog',{name:'Files',exact:true})
 const folder=dialog.getByRole('treeitem',{name:'empty',exact:true})
 await expect(folder.locator('[data-file-entry-count]')).toHaveCount(0)
 await dialog.getByRole('button',{name:'Expand /empty',exact:true}).click()
 const loading=dialog.locator('[data-file-loading-child]')
 await expect(loading).toBeVisible()
 const before=(await loading.boundingBox())!
 const loadingFont=await loading.locator('[role=status]').evaluate(el=>({size:getComputedStyle(el).fontSize,line:getComputedStyle(el).lineHeight,family:getComputedStyle(el).fontFamily}))
 const rowFont=await folder.evaluate(el=>({size:getComputedStyle(el).fontSize,line:getComputedStyle(el).lineHeight,family:getComputedStyle(el).fontFamily}))
 expect(loadingFont).toEqual(rowFont)
 release()
 const empty=dialog.locator('[data-file-empty-child]')
 await expect(empty).toHaveText('No elements')
 const after=(await empty.boundingBox())!
 expect(await empty.evaluate(el=>({size:getComputedStyle(el).fontSize,line:getComputedStyle(el).lineHeight,family:getComputedStyle(el).fontFamily}))).toEqual(rowFont)
 expect([after.y,after.height]).toEqual([before.y,before.height])
 await expect(folder.locator('[data-file-entry-count]')).toHaveText('0')
 await dialog.getByRole('button',{name:'Collapse /empty',exact:true}).click()
 await expect(empty).toHaveCount(0)
 await expect(folder.locator('[data-file-entry-count]')).toHaveText('0')
 await dialog.getByRole('button',{name:'Expand /empty',exact:true}).click()
 await expect(empty).toBeVisible()
 expect(reads).toBe(1)
 await dialog.getByRole('button',{name:'Expand /neighbor',exact:true}).click()
 const child=dialog.getByRole('treeitem',{name:'child.txt',exact:true})
 await expect(child).toBeVisible()
 expect((await empty.locator('span').boundingBox())!.x).toBe((await child.locator(':scope > svg').boundingBox())!.x)
 await page.screenshot({path:join(scratchRoot,'files-empty-folder-count.png')})
})

test('multiple Pods require a dropdown choice for Files, Terminal and Logs', async ({page}) => {
 const execInfo={defaultInstance:'worker-1',instanceLabel:{key:'kubernetes.level.pod',text:'Pod'},instances:['worker-1','worker-2'].map(id=>({id,title:id,ready:true,defaultChannel:'main',channels:[{id:'main',title:'main',running:true}]}))}
 const fileCalls:Record<string,unknown>[]=[]
 const terminalCalls:Record<string,unknown>[]=[]
 const logCalls:{ref:{name:string;uid?:string}}[]=[]
 await page.route('**/api/ExecInfo',route=>route.fulfill({json:execInfo}))
 await page.route('**/api/Files',route=>{
  const req=route.request().postDataJSON();fileCalls.push(req)
  return route.fulfill({json:{path:req.path,configRev:'fixture',entries:[],text:''}})
 })
 await page.route('**/api/OpenTerminal',route=>{terminalCalls.push(route.request().postDataJSON());return route.continue()})
 await page.route('**/api/LogInfo',route=>{
  const ref=route.request().postDataJSON()
  return route.fulfill({json:{channels:[{id:'main',title:'main'}],defaultChannel:'main',aggregate:true,selectChannels:true,previous:false,instances:['worker-1','worker-2'].map(name=>({title:name,ref:{...ref,kind:'pods',name,uid:`uid-${name}`}}))}})
 })
 await page.route('**/api/OpenLogStream',route=>{
  logCalls.push(route.request().postDataJSON())
  return route.fulfill({status:404,json:{code:'not-found',detail:'Fixture records the selected source'}})
 })
 await selectObject(page,'workers')
 await page.getByRole('button',{name:'Files',exact:true}).click()
 const pods=page.getByRole('listbox',{name:'Pod',exact:true})
 await expect(pods.getByRole('option',{name:'worker-2',exact:true})).toBeVisible()
 expect(fileCalls).toHaveLength(0)
 await page.screenshot({path:join(scratchRoot,'files-pod-dropdown.png')})
 await page.keyboard.press('Escape')
 await expect(pods).toHaveCount(0)
 await expect(page.getByRole('dialog',{name:'Files',exact:true})).toHaveCount(0)
 await page.getByRole('button',{name:'Files',exact:true}).click()
 await pods.getByRole('option',{name:'worker-2',exact:true}).click()
 const dialog=page.getByRole('dialog',{name:'Files',exact:true})
 await expect(dialog.locator('[data-file-context]')).toHaveText('workers / worker-2 / main')
 await expect.poll(()=>fileCalls.length).toBe(1)
 expect(fileCalls[0].instance).toBe('worker-2');expect(fileCalls[0].channel).toBe('main')
 await dialog.getByRole('button',{name:'Close',exact:true}).click()
 await page.getByRole('button',{name:'Terminal',exact:true}).click()
 await expect(pods.getByRole('option',{name:'worker-2',exact:true})).toBeVisible()
 expect(terminalCalls).toHaveLength(0)
 await page.keyboard.press('ArrowDown');await page.keyboard.press('Enter')
 await expect.poll(()=>terminalCalls.length).toBe(1)
 expect(terminalCalls[0].instance).toBe('worker-2')
 await expect(page.locator('.xterm-rows')).toContainText('synthetic terminal on worker-2')
 await page.locator('[data-table-scroll]').first().focus()
 await page.keyboard.press('l')
 await expect(pods.getByRole('option',{name:'All Pods',exact:true})).toBeVisible()
 const choices=(await pods.boundingBox())!,tool=(await page.locator('[data-area="details"]').getByRole('button',{name:'Logs',exact:true}).boundingBox())!
 expect(choices.width).toBeLessThan(400)
 expect(Math.abs(choices.x-tool.x)).toBeLessThan(8)
 expect(logCalls).toHaveLength(0)
 await page.screenshot({path:join(scratchRoot,'logs-pod-dropdown.png')})
 await pods.getByRole('option',{name:'worker-2',exact:true}).click()
 await expect.poll(()=>logCalls.length).toBe(1)
 expect(logCalls[0].ref).toMatchObject({kind:'pods',name:'worker-2',uid:'uid-worker-2'})
 await page.getByRole('button',{name:'Logs',exact:true}).click()
 await pods.getByRole('option',{name:'All Pods',exact:true}).click()
 await expect.poll(()=>logCalls.length).toBe(2)
 expect(logCalls[1].ref.name).toBe('workers')
})

test('concrete Pod tools open the running default directly and right-click retains container choice', async ({page}) => {
 const files:Record<string,unknown>[]=[]
 let execReads=0
 const podInfo={aggregate:false,defaultInstance:'api',instanceLabel:{key:'kubernetes.level.pod',text:'Pod'},channelLabel:{key:'kubernetes.level.container',text:'Container'},instances:[{id:'api',title:'api',ready:true,defaultChannel:'main',channels:[{id:'main',title:'main',running:true},{id:'sidecar',title:'sidecar',running:true}]}]}
 await page.route('**/api/ExecInfo',route=>{execReads++;return route.fulfill({json:podInfo})})
 await page.route('**/api/LogInfo',route=>route.fulfill({json:{aggregate:false,selectChannels:true,previous:true,defaultChannel:'main',channelLabel:{key:'kubernetes.level.container',text:'Container'},channels:[{id:'main',title:'main'},{id:'sidecar',title:'sidecar'}]}}))
 await page.route('**/api/Files',route=>{const req=route.request().postDataJSON();files.push(req);return route.fulfill({json:{path:req.path,configRev:'fixture',entries:[],text:''}})})
 await selectObject(page,'api')
 await expect.poll(()=>execReads).toBe(1)
 await expect(page.getByRole('button',{name:'Terminal…',exact:true})).toHaveCount(0)
 await page.getByRole('button',{name:'Files',exact:true}).click()
 const containerList=page.getByRole('listbox',{name:'Container',exact:true})
 await expect(containerList).toHaveCount(0)
 await expect(page.getByText('Loading',{exact:true})).toHaveCount(0)
 const inspector=page.getByRole('dialog',{name:'Files',exact:true})
 await expect(inspector.locator('[data-file-context]')).toContainText('main')
 await expect.poll(()=>files[0]?.channel).toBe('main')
 await inspector.getByRole('button',{name:'Close',exact:true}).click()
 for(const [button,title] of [['Terminal','Open a terminal'],['Files','Open files'],['Logs','Open logs']]) {
  await page.getByRole('button',{name:button,exact:true}).click({button:'right'})
  const setup=page.getByRole('dialog',{name:title,exact:true})
  await expect(setup.getByRole('button',{name:'Pod',exact:true})).toBeVisible()
  await expect(setup.getByRole('button',{name:'Container',exact:true})).toBeVisible()
  if(button==='Terminal') await expect(setup.getByRole('textbox',{name:'Command',exact:true})).toBeVisible()
  else await expect(setup.getByRole('textbox')).toHaveCount(0)
  await setup.getByRole('button',{name:'Container',exact:true}).click()
  await setup.getByRole('option',{name:'sidecar',exact:true}).click()
  await page.screenshot({path:join(scratchRoot,`tool-${button.toLowerCase()}-setup.png`)})
  if(button==='Files') {
   await setup.getByRole('button',{name:'Open',exact:true}).click()
   await expect(inspector.locator('[data-file-context]')).toContainText('sidecar')
   await expect.poll(()=>files.at(-1)?.channel).toBe('sidecar')
   await inspector.getByRole('button',{name:'Close',exact:true}).click()
  } else await setup.getByRole('button',{name:'Cancel',exact:true}).click()
 }
})

test('deployment revisions show retained history, read-only YAML and normalized comparisons beside related resources', async ({page}) => {
 const reads:string[]=[]
 await page.route('**/api/GetResource',async route=>{
  const req=route.request().postDataJSON()
  if(req.kind==='pods' && req.name.startsWith('api-very-long-pod')) return route.fulfill({json:{ref:{...req,title:undefined},health:{state:'ok'},facts:[],relations:[],yaml:'metadata: {}\n'}})
  if(req.kind==='apps/replicasets') {
   reads.push(req.uid)
   return route.fulfill({json:{ref:req,health:{state:'ok'},facts:[],relations:[],yaml:'metadata: {}\n',templateYAML:`spec:\n  containers:\n  - name: app\n    image: app:${req.uid==='rs1'?'v1':'v2'}\n`}})
  }
  const response=await route.fetch();const resource=await response.json()
  resource.revisionsAvailable=true
  resource.templateYAML='spec:\n  containers:\n  - name: app\n    image: app:v2\n'
  resource.revisions=[2,1].map(number=>({number,current:number===2,ref:{...resource.ref,kind:'apps/replicasets',name:`api-revision-${number}`,uid:`rs${number}`},created:'2026-10-01T10:00:00Z',ready:number===2?2:0,replicas:number===2?2:0,images:[`app:v${number}`],cause:number===1?'Initial deployment':''}))
  resource.relations=[{type:'owns',ref:{...resource.ref,kind:'apps/replicasets',name:'api-very-long-replicaset-name-123456789',uid:'rs2'}},{type:'owns',ref:{...resource.ref,kind:'pods',name:'api-very-long-pod-name-123456789',uid:'p1'}},{type:'uses',inert:true,ref:{kind:'unknown.example/resource',name:'unavailable'}}]
  await route.fulfill({json:resource})
 })
 await selectObject(page,'api')
 const revisions=page.getByRole('region',{name:'Deployment revisions',exact:true})
 await expect(revisions.getByText('Revision 1',{exact:true})).toBeVisible()
 await expect(revisions.getByText('Current',{exact:true})).toBeVisible()
 const table=revisions.getByRole('table',{name:'Deployment revisions',exact:true})
 await expect(table.getByRole('columnheader')).toHaveText(['Revision','Images / change','Ready','Created','Actions'])
 await expect(table.getByRole('row')).toHaveCount(3)
 expect(await table.evaluate(el=>el.parentElement!.scrollWidth===el.parentElement!.clientWidth)).toBeTruthy()
 for(const row of await table.locator('[data-deploy-revision]').all()) expect((await row.boundingBox())!.height).toBeLessThanOrEqual(48)
 await expect(table.getByText('Initial deployment',{exact:true})).toHaveAttribute('title','Initial deployment')
 await expect(table.getByText('2/2',{exact:true})).toHaveAttribute('title','2/2 ready')
 const relatedTables=page.locator('table').filter({has:page.locator('[data-related-resource]')})
 await expect(relatedTables).toHaveCount(2)
 await expect(relatedTables.first().getByRole('columnheader')).toHaveText(['Type','Name','Open'])
 await expect(relatedTables.last().getByRole('button')).toHaveCount(0)
 const assertRelatedAlignment=async()=>{
  const starts=await relatedTables.first().locator('[data-related-resource] td:nth-child(2)').evaluateAll(cells=>cells.map(cell=>cell.getBoundingClientRect().left))
  expect(starts).toHaveLength(2)
  expect(Math.max(...starts)-Math.min(...starts)).toBeLessThanOrEqual(0.5)
  const acrossGroups=await relatedTables.locator('th:nth-child(2)').evaluateAll(cells=>cells.map(cell=>cell.getBoundingClientRect().left))
  expect(Math.max(...acrossGroups)-Math.min(...acrossGroups)).toBeLessThanOrEqual(0.5)
  for(const row of await relatedTables.locator('[data-related-resource]').all()) expect((await row.boundingBox())!.height).toBeLessThanOrEqual(48)
 }
 await assertRelatedAlignment()
 await page.screenshot({path:join(scratchRoot,'related-and-deploy-revisions.png')})
 await revisions.locator('[data-deploy-revision]').filter({hasText:'Revision 2'}).getByRole('button',{name:'Compare',exact:true}).click()
 const latestComparison=page.getByRole('dialog',{name:'Deployment revisions',exact:true})
 await expect(latestComparison.getByRole('button',{name:'Compare against',exact:true})).toContainText('Revision 1')
 await expect(latestComparison.locator('[data-op="-"]')).toContainText('app:v1');await expect(latestComparison.locator('[data-op="+"]')).toContainText('app:v2')
 await latestComparison.getByRole('button',{name:'Compare against',exact:true}).click();await page.getByRole('option',{name:'Current Deployment',exact:true}).click()
 await expect(latestComparison).toContainText('Pod templates are identical.')
 await page.getByRole('dialog',{name:'Deployment revisions',exact:true}).getByRole('button',{name:'Close',exact:true}).click()
 const old=revisions.locator('[data-deploy-revision]').filter({hasText:'Revision 1'})
 await old.getByRole('button',{name:'View YAML',exact:true}).click()
 const dialog=page.getByRole('dialog',{name:'Deployment revisions',exact:true})
 await expect(dialog.locator('.cm-content')).toContainText('image: app:v1')
 await expect(dialog.locator('.cm-content')).toHaveAttribute('aria-readonly','true')
 const original=await dialog.locator('.cm-content').innerText()
 await dialog.locator('.cm-content').click();await page.keyboard.type('cannot change')
 expect(await dialog.locator('.cm-content').innerText()).toBe(original)
 await dialog.getByRole('button',{name:'Compare',exact:true}).click()
 await expect(dialog.locator('[data-op="-"]')).toContainText('app:v2')
 await expect(dialog.locator('[data-op="+"]')).toContainText('app:v1')
 await dialog.getByRole('button',{name:'Compare against',exact:true}).click()
 await dialog.getByRole('option',{name:'Revision 2',exact:true}).click()
 await expect.poll(()=>reads).toContain('rs2')
 await expect(dialog.locator('[data-op="-"]')).toContainText('app:v2')
 await page.screenshot({path:join(scratchRoot,'deploy-revision-compare.png')})
 await dialog.getByRole('button',{name:'Close',exact:true}).click()
 try {
 for(const language of languages) {
  await openAppMenu(page);await page.locator('.language-menu button').click()
  await page.getByRole('option',{name:languageNames[language],exact:true}).click()
  await expect(page.locator('html')).toHaveAttribute('lang',language)
  const translatedTable=page.locator('table').filter({has:page.locator('[data-deploy-revision]')})
  await expect(translatedTable.getByRole('columnheader')).toHaveCount(5)
  const headings={en:['Type','Name','Open'],ru:['Тип','Имя','Открыть'],zh:['类型','名称','打开'],es:['Tipo','Nombre','Abrir'],de:['Typ','Name','Öffnen'],fr:['Type','Nom','Ouvrir'],pt:['Tipo','Nome','Abrir'],ja:['種類','名前','開く']}
  await expect(relatedTables.first().getByRole('columnheader')).toHaveText(headings[language])
  for(const width of [1280,1000]) {
   await page.setViewportSize({width,height:900})
   await assertRelatedAlignment()
   for(const relatedTable of await relatedTables.all()) {
    expect(await relatedTable.evaluate(el=>el.parentElement!.scrollWidth===el.parentElement!.clientWidth)).toBeTruthy()
    await relatedTable.evaluate(el=>{el.parentElement!.scrollLeft=el.parentElement!.scrollWidth})
    const open=relatedTable.getByRole('button').first()
    if(await open.count()) await expect(open).toBeInViewport()
    await relatedTable.evaluate(el=>{el.parentElement!.scrollLeft=0})
   }
   if(language==='en'||language==='ru') await page.screenshot({path:join(scratchRoot,`relationships-${language}-${width}.png`)})
  }

  await expect(translatedTable.locator('[data-deploy-revision]').first().getByRole('button')).toHaveCount(2)
  if(language==='ru') {
   // Removing the240px targets column gives this table more room; still exercise real overflow.
   await page.setViewportSize({width:760,height:900})
   await page.screenshot({path:join(scratchRoot,'deploy-revisions-ru-compact.png')})
   expect(await translatedTable.evaluate(el=>el.parentElement!.scrollWidth>el.parentElement!.clientWidth)).toBeTruthy()
   await translatedTable.evaluate(el=>{el.parentElement!.scrollLeft=el.parentElement!.scrollWidth})
   await expect(translatedTable.locator('[data-deploy-revision]').first().getByRole('button').last()).toBeInViewport()
  }
 }
 } finally {
 await openAppMenu(page);await page.locator('.language-menu button').click()
 await page.getByRole('option',{name:languageNames.en,exact:true}).click()
 await page.setViewportSize({width:1280,height:900})
 }
 const related=page.getByRole('region',{name:'Related',exact:true})
 const link=related.getByRole('button',{name:'pods/api-very-long-pod-name-123456789',exact:true})
 await expect(link).toHaveAttribute('title','pods/api-very-long-pod-name-123456789')
 await link.focus()
 await page.keyboard.press('Enter')
 await expect(page.getByRole('dialog',{name:'pods api-very-long-pod-name-123456789',exact:true})).toBeVisible()
 await page.keyboard.press('Alt+ArrowLeft')
 await expect(page.getByRole('dialog',{name:'services api',exact:true})).toBeVisible()
})

test('right-click setup cancels an earlier pending left-click without starting a tool',async({page})=>{
 for(const [tool,title,method] of [['Terminal','Open a terminal','ExecInfo'],['Files','Open files','ExecInfo'],['Logs','Open logs','LogInfo']]) {
  let release!:()=>void
  const pending=new Promise<void>(resolve=>{release=resolve})
  let reads=0,starts=0
  await page.unrouteAll({behavior:'wait'})
  await page.route(`**/api/${method}`,async route=>{
   reads++
   if(reads===1) await pending
   await route.fulfill({json:method==='ExecInfo'?{aggregate:true,defaultInstance:'api',instanceLabel:{key:'kubernetes.level.pod',text:'Pod'},channelLabel:{key:'kubernetes.level.container',text:'Container'},instances:[{id:'api',title:'api',ready:true,defaultChannel:'main',channels:[{id:'main',title:'main',running:true}]}]}:{aggregate:false,defaultChannel:'main',previous:true,channels:[{id:'main',title:'main'}]}})
  })
  for(const endpoint of ['Files','OpenTerminal','OpenLogStream']) await page.route(`**/api/${endpoint}`,route=>{starts++;return route.continue()})
  await selectObject(page,'api');await expect.poll(()=>reads).toBe(1)
  const button=page.getByRole('button',{name:tool,exact:true})
  await button.click();await button.click({button:'right'})
  const setup=page.getByRole('dialog',{name:title,exact:true})
  await expect(setup.getByRole('button',{name:'Open',exact:true})).toBeEnabled()
  const response=page.waitForResponse(res=>res.url().endsWith(`/api/${method}`))
  release();await (await response).finished()
  await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))))
  expect(starts).toBe(0)
  await expect(setup).toBeVisible()
  await setup.getByRole('button',{name:'Cancel',exact:true}).click()
 }
})

test('revision Compare defaults to the nearest lower retained number independent of order',async({page})=>{
 const templateReads:string[]=[]
 await page.route('**/api/GetResource',async route=>{
  const ref=route.request().postDataJSON()
  if(ref.kind==='apps/replicasets'){templateReads.push(ref.uid);const number=Number(String(ref.uid).replace('default-rs-',''));await route.fulfill({json:{ref,facts:[],health:{state:'ok'},yaml:'',templateYAML:`spec:\n  image: app:v${number}\n`}});return}
  const response=await route.fetch(),resource=await response.json();resource.revisionsAvailable=true;resource.templateYAML='spec:\n  image: app:v3\n'
  resource.revisions=[3,1,2].map(number=>({number,current:number===3,ref:{...resource.ref,kind:'apps/replicasets',name:`default-rs-${number}`,uid:`default-rs-${number}`},created:'2026-10-01T10:00:00Z',ready:0,replicas:0,images:[`app:v${number}`]}))
  await route.fulfill({response,json:resource})
 })
 await selectObject(page,'api');const revisions=page.getByRole('region',{name:'Deployment revisions',exact:true})
 for(const number of [3,2,1]){
  const row=revisions.locator('[data-deploy-revision]').filter({hasText:`Revision ${number}`})
  await row.getByRole('button',{name:number===3?'View YAML':'Compare',exact:true}).click()
  const dialog=page.getByRole('dialog',{name:'Deployment revisions',exact:true})
  if(number===3){await expect(dialog.locator('.cm-content')).toContainText('app:v3');expect(templateReads).toEqual(['default-rs-3']);await dialog.getByRole('button',{name:'Compare',exact:true}).click()}
  await expect(dialog.getByRole('button',{name:'Compare against',exact:true})).toContainText(number===1?'Current Deployment':`Revision ${number-1}`)
  await expect(dialog.locator('[data-op="-"]')).toContainText(`app:v${number===1?3:number-1}`);await expect(dialog.locator('[data-op="+"]')).toContainText(`app:v${number}`)
  if(number===3)await page.screenshot({path:join(scratchRoot,'deployment-revision-previous-default.png')})
  await dialog.getByRole('button',{name:'Close',exact:true}).click()
 }
})
