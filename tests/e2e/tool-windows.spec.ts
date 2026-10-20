import {expect} from '@playwright/test'
import {test,scratchRoot,selectConnection} from './fixtures'
import {selectObject,expectScreen,since,stats,focusTerminal} from './synth'
import {join} from 'node:path'

test('Shell detaches and returns with its original PTY, screen, input and ordinary window geometry',async({page})=>{
 const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message))
 await selectObject(page,'api');const baseline=await stats(page)
 await page.keyboard.press('s');await expectScreen(page,'synthetic terminal on api')
 await focusTerminal(page);await page.keyboard.type('before-detach');await page.keyboard.press('Enter');await expectScreen(page,'you said: before-detach')
 const popupWait=page.waitForEvent('popup')
 await page.getByRole('button',{name:'Open in window',exact:true}).click();const popup=await popupWait;popup.on('pageerror',e=>errors.push(e.message))
 const screen=popup.locator('.xterm-screen')
 await expect(screen).toContainText('you said: before-detach')
 expect(await since(page,baseline)).toMatchObject({terminals:1,syn_execs:1})
 const geometry=await popup.evaluate(()=>({width:innerWidth,height:innerHeight}))
 expect(geometry.width).toBeLessThanOrEqual(960);expect(geometry.height).toBeLessThanOrEqual(600)
 await popup.locator('.xterm-helper-textarea').focus();await popup.keyboard.type('after-detach');await popup.keyboard.press('Enter')
 await expect(screen).toContainText('you said: after-detach')
 await popup.screenshot({path:join(scratchRoot,'shell-detached.png')})
 await popup.getByRole('button',{name:'Return to bottom panel',exact:true}).click()
 await expect.poll(()=>popup.isClosed()).toBe(true)
 await expectScreen(page,'you said: after-detach')
 expect(await since(page,baseline)).toMatchObject({terminals:1,syn_execs:1})
 await page.getByRole('button',{name:'Close tab',exact:true}).click()
 await expect.poll(async()=>since(page,baseline)).toMatchObject({terminals:0,syn_execs:0})
 expect(errors).toEqual([])
})

test('Files dock/window transfer keeps its directory, draft and captured save version',async({page,context})=>{
 let text='initial file\n',version='first';const calls:Record<string,unknown>[]=[]
 await context.route('**/api/Files',async route=>{
  const request=route.request().postDataJSON();calls.push(request)
  if(request.command==='list')return route.fulfill({json:{path:request.path,text:'',configRev:'captured',entries:[{name:'note.txt',directory:false,symlink:false}]}})
  if(request.command==='read')return route.fulfill({json:{path:request.path,text,version,configRev:'captured'}})
  expect(request.command).toBe('write');expect(request.expect).toBe(version);expect(request.configRev).toBe('captured')
  text=request.text;version='saved';return route.fulfill({json:{path:request.path,text,version,configRev:'captured'}})
 })
 await selectObject(page,'api');await page.getByRole('button',{name:'Files',exact:true}).click()
 const files=page.getByRole('region',{name:'Files',exact:true})
 await files.getByRole('treeitem',{name:'note.txt',exact:true}).dblclick()
 const editor=files.locator('.cm-content');await expect(editor).toContainText('initial file')
 await editor.click();await page.keyboard.press('Control+End');await page.keyboard.type('draft before transfer')
 const before=calls.length,popupWait=page.waitForEvent('popup')
 await files.getByRole('button',{name:'Open in window',exact:true}).click();const popup=await popupWait
 const remote=popup.getByRole('region',{name:'Files',exact:true})
 await expect(remote.locator('.cm-content')).toContainText('draft before transfer');expect(calls).toHaveLength(before)
 await remote.locator('.cm-content').click();await popup.keyboard.press('Control+z');await expect(remote.locator('.cm-content')).not.toContainText('draft before transfer');await popup.keyboard.press('Control+y');await expect(remote.locator('.cm-content')).toContainText('draft before transfer')
 await remote.locator('.cm-content').click();await popup.keyboard.press('Control+End');await popup.keyboard.type(' plus detached edit')
 await popup.screenshot({path:join(scratchRoot,'files-detached-draft.png')})
 await remote.getByRole('button',{name:'Return to bottom panel',exact:true}).click();await expect.poll(()=>popup.isClosed()).toBe(true)
 await expect(files.locator('.cm-content')).toContainText('plus detached edit');expect(calls).toHaveLength(before)
 await files.getByRole('button',{name:'Save',exact:true}).click();await expect(files).toContainText('Saved to container')
 expect(text).toContain('draft before transfer plus detached edit')
 await files.locator('.cm-content').click();await page.keyboard.type(' another draft')
 await page.locator('[data-tab-kind=files]').getByRole('button',{name:'Close tab',exact:true}).click()
 await expect(page.getByRole('alertdialog')).toBeVisible();await page.getByRole('button',{name:'Keep editing',exact:true}).click()
 await expect(files.locator('.cm-content')).toContainText('another draft');await expect(files.locator('.cm-content')).toBeFocused()
 const nextPopup=page.waitForEvent('popup');await files.getByRole('button',{name:'Open in window',exact:true}).click();const second=await nextPopup
 const remoteEditor=second.locator('.cm-content');await expect(remoteEditor).toContainText('another draft')
 await remoteEditor.click();await second.keyboard.press('Control+End');await second.keyboard.type(' retained after window close')
 await page.locator('[data-tab-kind=files]').getByRole('button',{name:'Close tab',exact:true}).click()
 await expect(page.getByRole('alertdialog')).toBeVisible();await page.getByRole('button',{name:'Keep editing',exact:true}).click()
 expect(second.isClosed()).toBe(false)
 await second.close({runBeforeUnload:true});await expect.poll(()=>second.isClosed()).toBe(true)
 await expect(files.locator('.cm-content')).toContainText('retained after window close')
 await page.screenshot({path:join(scratchRoot,'files-returned-draft.png')})
})

test('detached Shell preserves alternate screen, ANSI, wide cells, output credits and interrupt',async({page})=>{
 await selectObject(page,'api');const base=await stats(page);await page.keyboard.press('s');await expectScreen(page,'synthetic terminal on api')
 await focusTerminal(page);await page.keyboard.type('screen-demo');await page.keyboard.press('Enter');await expectScreen(page,'ALT wide 界')
 const popupWait=page.waitForEvent('popup');await page.getByRole('button',{name:'Open in window',exact:true}).click();const popup=await popupWait
 await expect(popup.locator('.xterm-rows')).toContainText('ALT wide 界')
 const red=popup.locator('.xterm-rows span').filter({hasText:'ALT'}).first();await expect(red).toHaveClass(/xterm-fg-9/);await expect(red).toHaveClass(/xterm-bold/)
 const color=await red.evaluate(el=>getComputedStyle(el).color);expect(color).toBe(await page.locator('.xterm-rows span').filter({hasText:'ALT'}).first().evaluate(el=>getComputedStyle(el).color))
 await popup.locator('.xterm-helper-textarea').focus();await popup.keyboard.type('screen-normal');await popup.keyboard.press('Enter')
 await expect(popup.locator('.xterm-rows')).toContainText('synthetic terminal on api')
 await popup.keyboard.type('flood 100000000');await popup.keyboard.press('Enter');await expect(popup.locator('.xterm-rows')).toContainText('flood line')
 await popup.keyboard.press('Control+c');await expect(popup.locator('.xterm-rows')).toContainText('^C')
 await popup.getByRole('button',{name:'Return to bottom panel',exact:true}).click();await expect.poll(()=>popup.isClosed()).toBe(true)
 await expectScreen(page,'^C');expect(await since(page,base)).toMatchObject({terminals:1,syn_execs:1})
})


test('a detached explicit reconnect mirrors exit text and separators before returning to the dock',async({page})=>{
 await selectObject(page,'api');const base=await stats(page);await page.keyboard.press('s');await expectScreen(page,'synthetic terminal on api')
 const popupWait=page.waitForEvent('popup');await page.getByRole('button',{name:'Open in window',exact:true}).click();const popup=await popupWait
 await expect(popup.locator('.xterm-rows')).toContainText('synthetic terminal on api')
 await popup.locator('.xterm-helper-textarea').focus();await popup.keyboard.type('exit 7');await popup.keyboard.press('Enter')
 await expect(popup.getByRole('alert')).toContainText('process exited with code 7')
 await expect(popup.locator('.xterm-rows')).toContainText('[process exited with code 7]')
 await popup.getByRole('button',{name:'Reconnect',exact:true}).click()
 await expect(popup.locator('.xterm-rows')).toContainText('reconnected')
 await expect.poll(async()=>since(page,base)).toMatchObject({terminals:1,syn_execs:1})
 await popup.getByRole('button',{name:'Return to bottom panel',exact:true}).click();await expect.poll(()=>popup.isClosed()).toBe(true)
 await expectScreen(page,'reconnected');await expectScreen(page,'[process exited with code 7]')
})


test('a Files draft survives resource, kind and target navigation without discard or a new read',async({page})=>{
 const calls:Record<string,unknown>[]=[]
 await page.route('**/api/Files',async route=>{
  const request=route.request().postDataJSON();calls.push(request)
  await route.fulfill({json:{path:request.path,text:request.command==='read'?'retained file\n':'',version:'original',configRev:'original',entries:[{name:'note.txt',directory:false,symlink:false}]}})
 })
 await selectObject(page,'api');await page.getByRole('button',{name:'Files',exact:true}).click()
 const files=page.getByRole('region',{name:'Files',exact:true});await files.getByRole('treeitem',{name:'note.txt',exact:true}).dblclick()
 await expect(files.locator('.cm-content')).toContainText('retained file');await files.locator('.cm-content').click();await page.keyboard.press('Control+End');await page.keyboard.type('sticky draft')
 const before=calls.length
 await page.getByRole('grid',{name:'resources',exact:true}).getByRole('gridcell',{name:'workers',exact:true}).click()
 await expect(page.getByRole('alertdialog')).toHaveCount(0);await expect(page.locator('[data-area=details]').getByRole('heading',{name:'workers',exact:true})).toBeVisible()
 const crates=page.getByRole('navigation',{name:'resources',exact:true}).getByRole('button',{name:'Crates',exact:true});await crates.click();await expect(crates).toHaveAttribute('aria-current','page')
 await selectConnection(page,/^prod\b/)
 await expect(page.locator('[data-tab-kind=files]').getByText('demo',{exact:true})).toBeVisible()
 await expect(files.locator('.cm-content')).toContainText('sticky draft');expect(calls).toHaveLength(before)
 await expect(page.getByRole('alertdialog')).toHaveCount(0)
 await page.screenshot({path:join(scratchRoot,'files-foreign-target-draft.png')})
})

test('a detached Shell answers each terminal cursor query once',async({page})=>{
 let sent=0,queryEnd=0,acked=0;const reports:string[]=[]
 await page.routeWebSocket(/\/term\//,socket=>{
  const server=socket.connectToServer()
  socket.onMessage(message=>{
   if(typeof message==='string'){const control=JSON.parse(message);if(control.k==='ack')acked=control.n}
   else {const input=message.toString('utf8');if(/^\x1b\[\d+;\d+R$/.test(input))reports.push(input)}
   server.send(message)
  })
  server.onMessage(message=>{
   if(typeof message==='string'){socket.send(message);return}
   const data=Buffer.from(message),marker=Buffer.from('you said: QUERYMARK')
   const index=data.indexOf(marker)
   sent+=data.length
   if(!queryEnd&&index>=0){
    // Preserve byte count so the real stream's processing-credit invariant
    // remains intact; only this owned fixture's output becomes a cursor query.
    Buffer.from('\x1b[6n'+' '.repeat(marker.length-4)).copy(data,index);queryEnd=sent
   }
   socket.send(data)
  })
 })
 await selectObject(page,'api');await page.keyboard.press('s');await expectScreen(page,'synthetic terminal on api')
 const popupWait=page.waitForEvent('popup');await page.getByRole('button',{name:'Open in window',exact:true}).click();const popup=await popupWait
 await expect(popup.locator('.xterm-rows')).toContainText('synthetic terminal on api')
 await popup.locator('.xterm-helper-textarea').focus();await popup.keyboard.type('QUERYMARK');await popup.keyboard.press('Enter')
 await expect.poll(()=>queryEnd>0&&acked>=queryEnd).toBe(true)
 expect(reports).toHaveLength(1)
 await popup.getByRole('button',{name:'Return to bottom panel',exact:true}).click();await expect.poll(()=>popup.isClosed()).toBe(true)
 await focusTerminal(page);await page.keyboard.press('Control+c');await expectScreen(page,'^C')
 reports.length=0;queryEnd=0
 await page.keyboard.type('QUERYMARK');await page.keyboard.press('Enter')
 await expect.poll(()=>queryEnd>0&&acked>=queryEnd).toBe(true)
 expect(reports).toHaveLength(1)
})
