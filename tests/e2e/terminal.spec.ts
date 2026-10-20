import {selectConnection} from './fixtures'
import { expect, type Page } from '@playwright/test'
import { test, scratchRoot } from './fixtures'
import {join} from 'node:path'
import { activePanel, expectScreen, focusTerminal, screen, selectObject, since, stats } from './synth'
import { pickOption, selects } from './fixtures'

// The echo terminal of the synthetic provider (internal/providers/synthetic/live.go):
// typed lines are answered "you said: …", "flood N", "exit N", "size CxR" on resize.

async function openShell(page: Page, object = 'api') {
  await selectObject(page, object)
  await page.keyboard.press('s')
  // "size CxR" lines arrive asynchronously, so no prompt is waited for
  await expectScreen(page, `synthetic terminal on ${object === 'api' ? 'api' : 'worker-1'}`)
}

async function run(page: Page, line: string) {
  await page.keyboard.type(line)
  await page.keyboard.press('Enter')
}

const sizes = async (page: Page) => [...((await screen(page).textContent()) ?? '').matchAll(/size (\d+)x(\d+)/g)].map((m) => ({ cols: Number(m[1]), rows: Number(m[2]) }))

test('typing, output and the size the terminal opened with', async ({ page }) => {
  await openShell(page)
  // xterm resolves the shared CSS tokens; production CSS must keep all
  // sixteen ANSI colours, including ones referenced only at runtime.
  const colors = await page.evaluate(() => {
    const css = getComputedStyle(document.documentElement)
    return Array.from({ length: 16 }, (_, i) => css.getPropertyValue(`--color-ansi-${i}`).trim())
  })
  expect(colors.filter(Boolean)).toHaveLength(16)
  const appBackground = await page.locator('body').evaluate((el) => getComputedStyle(el).backgroundColor)
  await expect(activePanel(page).locator('.xterm-scrollable-element')).toHaveCSS('background-color', appBackground)
  await run(page, 'hello there')
  await expectScreen(page, 'you said: hello there')
  const [first] = await sizes(page)
  expect(first.cols).toBeGreaterThan(40)
  expect(first.rows).toBeGreaterThan(3)
  await expect(page.getByRole('tab', { name: /main · api/ })).toBeVisible()
})

test('changing the dock height resizes the terminal', async ({ page }) => {
  await openShell(page)
  const before = (await sizes(page)).at(-1)!
  // the dock height is remembered: grow a short dock, shrink a tall one
  const dy = before.rows < 20 ? -150 : 150
  const sep = page.getByRole('separator', { name: 'Resize the bottom panel' })
  const box = (await sep.boundingBox())!
  await page.mouse.move(box.x + box.width / 2, box.y + 1)
  await page.mouse.down()
  await page.mouse.move(box.x + box.width / 2, box.y + 1 + dy, { steps: 5 })
  await page.mouse.up()
  await expect.poll(async () => Math.abs((await sizes(page)).at(-1)!.rows - before.rows)).toBeGreaterThan(5)
  expect((await sizes(page)).at(-1)!.cols).toBe(before.cols)
  await page.mouse.move(10,10)
  await expect(sep).not.toBeFocused()
})

test('a flood stops at Ctrl+C and the terminal stays responsive', async ({ page }) => {
  await openShell(page)
  await run(page, 'flood 100000000')
  await expectScreen(page, /flood line \d+ of 100000000/)
  await page.keyboard.press('Control+c')
  await expectScreen(page, /\^C\s*\$/, 10_000)
  await run(page, 'still here')
  await expectScreen(page, 'you said: still here')
  expect(await screen(page).textContent()).not.toContain('flood done')
})

test('an exit code is shown; Reconnect starts the shell again', async ({ page }) => {
  await openShell(page)
  await run(page, 'exit 3')
  const alert = activePanel(page).getByRole('alert')
  await expect(alert).toContainText('process exited with code 3')
  await expect.poll(async () => (await stats(page)).terminals).toBe(0)
  await alert.getByRole('button', { name: 'Reconnect' }).click()
  await expectScreen(page, 'reconnected')
  await expect(alert).toHaveCount(0)
  // xterm exposes only visible rows; the old greeting can be in scrollback.
  // A greeting AFTER the reconnect marker proves the new shell has started.
  await expectScreen(page, /reconnected[\s\S]*synthetic terminal on api/)
  await run(page, 'again')
  await expectScreen(page, 'you said: again')
})

test('a custom command is not run again without asking', async ({ page }) => {
  await selectObject(page, 'workers')
  await page.keyboard.press('Shift+S')
  const dlg = page.getByRole('dialog', { name: 'Open a terminal' })
  await pickOption(selects(dlg).first(), /^worker-2/)
  await dlg.getByPlaceholder(/interactive shell/).fill('exit 5')
  await dlg.getByRole('button', { name: 'Open' }).click()
  const alert = activePanel(page).getByRole('alert')
  await expect(alert).toContainText('process exited with code 5')
  await alert.getByRole('button', { name: 'Reconnect' }).click()
  await expect(alert).toContainText('Run again:')
  await expect(alert.locator('code')).toHaveText('exit 5')
  await alert.getByRole('button', { name: 'Run' }).click()
  await expectScreen(page, 'reconnected')
  await expect(alert).toContainText('process exited with code 5')
  await expect(page.getByRole('tab', { name: /main · worker-2/ })).toBeVisible()
})

test('a terminal survives selecting another target and says whose it is', async ({ page }) => {
  await page.goto('/')
  const base = await stats(page)
  await openShell(page)
  await selectConnection(page,/^prod\b/)
  const tab = page.getByRole('tab', { name: /main · api/ })
  await expect(tab).toBeVisible()
  await expect(tab.getByText('demo', { exact: true })).toBeVisible() // the foreign-target badge
  await focusTerminal(page)
  await run(page, 'from prod')
  await expectScreen(page, 'you said: from prod')
  expect(await since(page, base)).toMatchObject({ terminals: 1, syn_execs: 1 })
})

test('closing the tab ends the command and frees the terminal', async ({ page }) => {
  await page.goto('/')
  // The last test's terminal hangs up when its page goes: count from after that.
  await expect.poll(async () => (await stats(page)).syn_execs).toBe(0)
  const base = await stats(page)
  await openShell(page)
  await expect.poll(async () => since(page, base)).toMatchObject({ terminals: 1, syn_execs: 1, syn_handles: 2 }) // the kept prototype + the running copy
  await page.getByRole('tab', { name: /main · api/ }).getByRole('button', { name: 'Close tab' }).click()
  await expect.poll(async () => since(page, base)).toMatchObject({ terminals: 0, syn_execs: 0, syn_handles: 0 })
})


test('physical Cyrillic keys send each character once, and resize sends only the final geometry',async({page})=>{
 await openShell(page)
 const textarea=activePanel(page).locator('textarea.xterm-helper-textarea')
 for(const [key,code] of [['п','KeyG'],['р','KeyH'],['и','KeyB'],['в','KeyD'],['е','KeyT'],['т','KeyN']]) {
  await textarea.dispatchEvent('keydown',{key,code,keyCode:0,bubbles:true,cancelable:true})
  await textarea.dispatchEvent('keypress',{key,code,charCode:key.charCodeAt(0),bubbles:true,cancelable:true})
 }
 await page.keyboard.press('Enter')
 await expectScreen(page,'you said: привет')
 await expect(screen(page)).not.toContainText('ппрпри')
 const before=await sizes(page)
 const sep=page.getByRole('separator',{name:'Resize the bottom panel',exact:true})
 const box=(await sep.boundingBox())!
 await page.mouse.move(box.x+box.width/2,box.y+1);await page.mouse.down()
 for(let i=1;i<=5;i++) {
  await page.mouse.move(box.x+box.width/2,box.y+1-i*20)
  await expect.poll(async()=>(await sizes(page)).length).toBe(before.length)
 }
 await page.mouse.up()
 await expect.poll(async()=>(await sizes(page)).length).toBe(before.length+1)
 await page.mouse.move(10,10);await expect(sep).not.toBeFocused()
})

test('Alt+Shift arrows resize a focused terminal panel without sending the chord to its program',async({page})=>{
 await page.setViewportSize({width:1440,height:1000})
 await openShell(page)
 await focusTerminal(page)
 const dock=page.locator('[data-area=dock]'),height=(await dock.boundingBox())!.height
 await page.keyboard.press('Alt+Shift+ArrowUp')
 await expect.poll(async()=>(await dock.boundingBox())!.height).toBe(height+20)
 await page.keyboard.press('Alt+Shift+ArrowDown')
 await expect.poll(async()=>(await dock.boundingBox())!.height).toBe(height)
 await run(page,'keyboard resize kept focus')
 await expectScreen(page,'you said: keyboard resize kept focus')
})

test('three repeated Cyrillic characters stay three across physical, text-input and composition paths',async({page})=>{
 await openShell(page)
 const input=activePanel(page).locator('textarea.xterm-helper-textarea')
 for(let i=0;i<3;i++){
  await input.dispatchEvent('keydown',{key:'ы',code:'KeyS',keyCode:0,bubbles:true,cancelable:true})
  await input.dispatchEvent('keypress',{key:'ы',code:'KeyS',charCode:1099,bubbles:true,cancelable:true})
  await input.dispatchEvent('keyup',{key:'ы',code:'KeyS',keyCode:0,bubbles:true,cancelable:true})
 }
 await page.keyboard.press('Enter')
 await expectScreen(page,'you said: ыыы')
 await expect(screen(page)).not.toContainText('you said: ыыыы')
 await page.keyboard.insertText('ы');await page.keyboard.insertText('ы');await page.keyboard.insertText('ы')
 await page.keyboard.press('Enter')
 await expect.poll(async()=>((await screen(page).textContent())?.match(/you said: ыыы/g)??[]).length).toBe(2)
 // Real xterm, including asynchronous composition delivery and residue cleanup.
 for(let i=0;i<3;i++){
  await input.dispatchEvent('compositionstart',{data:'',bubbles:true})
  await input.dispatchEvent('compositionupdate',{data:'ы',bubbles:true})
  await input.evaluate(el=>{const field=el as HTMLTextAreaElement;field.value+='ы';field.dispatchEvent(new InputEvent('input',{data:'ы',inputType:'insertCompositionText',isComposing:true,bubbles:true}))})
  await input.dispatchEvent('compositionend',{data:'ы',bubbles:true})
  await expect.poll(async()=>await input.inputValue()).toBe('')
  // Wait for xterm's zero-delay finalizer before the next independent commit.
  await page.evaluate(()=>new Promise(resolve=>setTimeout(resolve,0)))
 }
 await page.keyboard.press('Enter')
 await expect.poll(async()=>((await screen(page).textContent())?.match(/you said: ыыы/g)??[]).length).toBe(3)
 await expect(screen(page)).not.toContainText('you said: ыыыы')
 await page.screenshot({path: `${process.env.OCULAR_SCRATCH_DIR}/terminal-three-cyrillic-input-paths.png`})
})

test('an IME replacement cannot replay text already delivered by earlier keys',async({page})=>{
 await openShell(page)
 const input=activePanel(page).locator('textarea.xterm-helper-textarea')
 await page.keyboard.type('ABC')
 await expectScreen(page,'ABC')
 // xterm#6078: a 229 edit outside active composition replaces the trailing
 // helper-field character; unpatched xterm emits the whole retained buffer.
 await input.evaluate(el=>{
  const field=el as HTMLTextAreaElement
  field.dispatchEvent(new KeyboardEvent('keydown',{key:'Process',keyCode:229,bubbles:true,cancelable:true}))
  field.value=field.value.slice(0,-1)+'ы'
  field.dispatchEvent(new KeyboardEvent('keyup',{key:'Process',keyCode:229,bubbles:true}))
 })
 await page.evaluate(()=>new Promise(resolve=>setTimeout(resolve,0)))
 await page.keyboard.press('Enter')
 await expectScreen(page,'you said: ABCы')
 await expect(screen(page)).not.toContainText('you said: ABCABы')
})

test('IME preedit replacement, cancellation and Unicode paste keep committed input once',async({page})=>{
 await openShell(page)
 const input=activePanel(page).locator('textarea.xterm-helper-textarea')
 const cdp=await page.context().newCDPSession(page)
 await cdp.send('Input.imeSetComposition',{text:'に',selectionStart:1,selectionEnd:1})
 await cdp.send('Input.imeSetComposition',{text:'日本',selectionStart:2,selectionEnd:2})
 await cdp.send('Input.insertText',{text:'日本語'})
 await expect(input).toHaveValue('')
 await page.keyboard.press('Enter');await expectScreen(page,'you said: 日本語')
 await cdp.send('Input.imeSetComposition',{text:'取消',selectionStart:2,selectionEnd:2})
 await cdp.send('Input.imeSetComposition',{text:'',selectionStart:0,selectionEnd:0})
 await page.keyboard.type('hello');await page.keyboard.press('Backspace');await page.keyboard.type('p')
 await page.keyboard.press('Enter');await expectScreen(page,'you said: hellp')
 await expect(screen(page)).not.toContainText('you said: 取消')
 await page.evaluate(()=>navigator.clipboard.writeText('paste Ж🙂'))
 await page.keyboard.press('Control+Shift+V');await expectScreen(page,'paste Ж🙂')
 await page.keyboard.press('Enter');await expectScreen(page,'you said: paste Ж🙂')
 await cdp.detach()
})

test('instance details remain readable in quick and setup pickers after a narrow resize', async ({page})=>{
  await page.route('**/api/ExecInfo',async route=>{
    const response=await route.fetch(), info=await response.json()
    info.instances=info.instances.map((item:{title:string},index:number)=>({...item,details:[
      {text:index===0?'Previous revision 193 · Ready · Containers 1/1 · Running':'Current revision 194 · Not ready · Containers 0/1 · Running'},
    ]}))
    await route.fulfill({response,json:info})
  })
  await page.setViewportSize({width:1440,height:900})
  await selectObject(page,'workers')
  await page.getByRole('button',{name:'Terminal',exact:true}).click()
  const list=page.getByRole('listbox')
  await expect(list.getByRole('option',{name:'worker-2',exact:true})).toHaveAccessibleDescription(/Current revision 194/)
  const pod=list.getByRole('option',{name:'worker-2',exact:true})
  await expect(pod).not.toHaveAttribute('title');await expect(pod.locator('[title]')).toHaveCount(0)
  expect(await pod.getByText('worker-2',{exact:true}).evaluate(el=>{const probe=document.createElement('span');probe.style.color='var(--color-fg)';document.body.append(probe);const same=getComputedStyle(el).color===getComputedStyle(probe).color;probe.remove();return same})).toBe(true)
  await page.mouse.move(0,0);await page.screenshot({path:join(scratchRoot,'pod-picker-hierarchy.png')})

  for(const width of [375,768,1440]){
    // Close before resizing: ancestor scroll intentionally dismisses open menus.
    await page.keyboard.press('Escape')
    await page.setViewportSize({width,height:820})
    const button=page.getByRole('button',{name:'Terminal',exact:true})
    await button.scrollIntoViewIfNeeded()
    await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))))
    await button.click()
    await expect.poll(async()=>{const box=await list.boundingBox();return !!box&&box.x>=0&&box.x+box.width<=width}).toBe(true)
    await expect(list.getByRole('option',{name:'worker-2',exact:true}).getByText(/Current revision 194/)).toBeVisible()
  }
  await page.keyboard.press('Escape')
  await page.getByRole('button',{name:'Terminal',exact:true}).click({button:'right'})
  const dialog=page.getByRole('dialog',{name:'Open a terminal'})
  await selects(dialog).first().click()
  await expect(page.getByRole('option',{name:'worker-2',exact:true})).toHaveAccessibleDescription(/Current revision 194/)
  await page.getByRole('option',{name:'worker-2',exact:true}).click()
  await dialog.getByRole('button',{name:'Open',exact:true}).click()
  await expectScreen(page,'synthetic terminal on worker-2')
})
