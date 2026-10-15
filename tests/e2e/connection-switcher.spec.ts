import {expect} from '@playwright/test'
import {test,openConnections,selectConnection,connectSelected,openAppMenu,scratchRoot} from './fixtures'
import {stats,token} from './synth'
import {join} from 'node:path'

test('header switcher preserves explicit connection, keyboard selection and workspace width',async({page})=>{
 await page.setViewportSize({width:1280,height:820});await page.goto('/')
 expect(await page.locator('[data-area=targets] aside').count()).toBe(0)
 await expect(page.getByRole('separator',{name:'Resize targets panel',exact:true})).toHaveCount(0)
 const before=await stats(page),picker=await openConnections(page)
 await expect(picker.getByRole('combobox')).toBeFocused()
 await expect(picker.locator('.connection-choice[title],.connection-group > [title]')).toHaveCount(0)
 await picker.locator('.connection-row-appearance').first().hover();await expect(page.locator('.app-tooltip')).toHaveText('Connection appearance')
 await picker.getByRole('combobox').hover()
 await picker.getByRole('combobox').fill('demo2')
 await expect(picker.locator('.connection-choice')).toHaveCount(1)
 await expect(picker).not.toContainText('✓')
 await expect(picker.locator('[data-connection-row][data-cursor=true]')).toHaveCount(1)
 await expect(picker.locator('[data-cursor=true] .connection-choice')).toContainText('demo2')
 expect((await stats(page)).syn_connects).toBe(before.syn_connects)
 await picker.getByRole('combobox').press('Enter')
 await expect(picker).toHaveCount(0)
 await expect(page.locator('[data-connection-switcher]')).toContainText('demo2')
 expect((await stats(page)).syn_connects).toBe(before.syn_connects)
 await connectSelected(page)
 await expect(page.getByRole('grid',{name:'resources'})).toBeVisible()
 const nav=(await page.getByRole('navigation',{name:'resources'}).boundingBox())!
 expect(nav.x).toBe(0)
 await page.screenshot({path:join(scratchRoot,'connection-workspace-1280.png')})
 await openConnections(page);await page.keyboard.press('Escape')
 await expect(page.locator('[data-connection-switcher]')).toBeFocused()
})

test('appearance has32icons and16preset pairs; persists through reload and target switches',async({page})=>{
 await page.goto('/');await selectConnection(page,/^demo\b/)
 const original=page.locator('[data-connection-appearance=header] [data-connection-icon]')
 await expect(original).toHaveAttribute('data-connection-icon',/./)
 const first=await original.getAttribute('data-connection-icon'),color=await original.getAttribute('data-connection-palette')
 await page.reload();await expect(original).toHaveAttribute('data-connection-icon',first!);await expect(original).toHaveAttribute('data-connection-palette',color!)
 const picker=await openConnections(page)
 await picker.getByRole('button',{name:'Actions for demo',exact:true}).click()
 await page.getByRole('menuitem',{name:'Connection appearance',exact:true}).click()
 const dialog=page.getByRole('dialog',{name:'Connection appearance',exact:true})
 await expect(dialog.locator('.appearance-icons button')).toHaveCount(32)
 await expect(dialog.locator('.appearance-palette button')).toHaveCount(16)
 await expect(dialog.locator('input[type=color]')).toHaveCount(0)
 const iconTile=dialog.locator('.appearance-icons .connection-avatar').first()
 expect((await iconTile.boundingBox())!.width).toBeGreaterThanOrEqual(35)
 await dialog.getByRole('button',{name:'Color 8',exact:true}).click()
 await page.keyboard.press('ArrowDown');await expect(dialog.getByRole('button',{name:'Color 16',exact:true})).toBeFocused()
 await dialog.getByRole('button',{name:'Icon 32',exact:true}).click()
 await dialog.getByRole('button',{name:'Color 3',exact:true}).click()
 await expect(dialog.getByRole('button',{name:'Icon 32',exact:true})).toHaveAttribute('aria-pressed','true')
 await expect(dialog.getByRole('button',{name:'Color 3',exact:true})).toHaveAttribute('aria-pressed','true')
 await page.screenshot({path:join(scratchRoot,'connection-appearance.png')})
 await dialog.getByRole('button',{name:'Cancel',exact:true}).click()
 await expect(original).toHaveAttribute('data-connection-icon',first!)
 await openConnections(page);await page.getByRole('button',{name:'Actions for demo',exact:true}).click();await page.getByRole('menuitem',{name:'Connection appearance',exact:true}).click()
 await dialog.getByRole('button',{name:'Icon 32',exact:true}).click();await dialog.getByRole('button',{name:'Color 3',exact:true}).click();await dialog.getByRole('button',{name:'Save',exact:true}).click()
 await expect(original).toHaveAttribute('data-connection-icon','cat');await expect(original).toHaveAttribute('data-connection-palette','green')
 await selectConnection(page,/^demo2\b/);await selectConnection(page,/^demo\b/);await page.reload()
 await expect(original).toHaveAttribute('data-connection-icon','cat');await expect(original).toHaveAttribute('data-connection-palette','green')
 await page.locator('[data-connection-appearance=header]').click()
 await dialog.getByRole('button',{name:'Color 16',exact:true}).click();await dialog.getByRole('button',{name:'Save',exact:true}).click()
 await page.reload();await expect(original).toHaveAttribute('data-connection-palette','sand')
 const state=await page.request.post('/api/GetTargetState',{headers:{Authorization:'Bearer '+await token(page),Origin:new URL(page.url()).origin},data:{provider:'synthetic',target:'demo'}})
 expect(state.ok()).toBe(true)
 await openAppMenu(page);await expect(page.locator('.language-menu')).toBeVisible()
})

const localeLabels={en:['Connections','Connection appearance'],ru:['Подключения','Оформление подключения'],zh:['连接','连接外观'],es:['Conexiones','Apariencia de la conexión'],de:['Verbindungen','Verbindungsdarstellung'],fr:['Connexions','Apparence de la connexion'],pt:['Conexões','Aparência da conexão'],ja:['接続','接続の外観']}
for(const [language,labels] of Object.entries(localeLabels))test(`connection menus and appearance fit375/1280 and retain focus (${language})`,async({page})=>{
 await page.goto('/')
 const headers={Authorization:'Bearer '+await token(page),Origin:new URL(page.url()).origin}
 try{
  const changed=await page.request.post('/api/SetLanguage',{headers,data:{language}});expect(changed.ok()).toBe(true)
  await page.reload();await selectConnection(page,/^demo\b/)
  for(const width of [375,1280]){
   await page.setViewportSize({width,height:820})
   const picker=await openConnections(page)
   await expect(picker).toHaveAttribute('aria-label',labels[0])
   expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true)
   const bounds=(await picker.boundingBox())!;expect(bounds.x).toBeGreaterThanOrEqual(0);expect(bounds.x+bounds.width).toBeLessThanOrEqual(width)
   await page.screenshot({path:join(scratchRoot,`connection-picker-${language}-${width}.png`)})
   await picker.locator('[data-connection-row][aria-selected=true] .connection-more').click()
   await page.getByRole('menuitem').first().click()
   const dialog=page.locator('.appearance-dialog')
   await expect(dialog.getByRole('heading')).toHaveText(labels[1])
   expect(await dialog.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true)
   await expect(dialog.locator('[data-cancel]')).toBeFocused()
   await page.keyboard.press('Tab');await expect(dialog.getByRole('button').last()).toBeFocused()
   await page.keyboard.press('Tab');await expect(dialog.locator('.appearance-icons [aria-pressed=true]')).toBeFocused()
   await page.keyboard.press('ArrowRight');await expect(dialog.locator('.appearance-icons [aria-pressed=true]')).toBeFocused()
   await page.keyboard.press('Tab');await expect(dialog.locator('.appearance-palette [aria-pressed=true]')).toBeFocused()
   await page.screenshot({path:join(scratchRoot,`connection-appearance-${language}-${width}.png`)})
   await page.keyboard.press('Escape');await expect(page.locator('[data-connection-switcher]')).toBeFocused()
   await openAppMenu(page);await expect(page.locator('.app-utilities .language-menu')).toBeVisible();await page.keyboard.press('Escape')
  }
 }finally{await page.request.post('/api/SetLanguage',{headers,data:{language:''}})}
})

test('failed first appearance write remains visible and retries the same assignment',async({page})=>{
 let failed=false
 await page.route('**/api/GetTargetState',async route=>{
  const request=route.request().postDataJSON()
  if(request.provider!=='synthetic'||request.target!=='demo')return route.continue()
  const response=await route.fetch(),body=await response.json();delete body.appearance
  await route.fulfill({response,json:body})
 })
 await page.route('**/api/SetTargetState',async route=>{
  const request=route.request().postDataJSON()
  if(!failed&&request.provider==='synthetic'&&request.target==='demo'&&request.key==='appearance'){failed=true;return route.fulfill({status:500,json:{code:'internal',detail:'synthetic UI preference write failure'}})}
  await route.continue()
 })
 await page.goto('/');await selectConnection(page,/^demo\b/)
 const picker=await openConnections(page),alert=picker.getByRole('alert')
 await expect(alert).toContainText('Could not load or save connection appearance')
 const icon=page.locator('[data-connection-appearance=header] [data-connection-icon]'),before={icon:await icon.getAttribute('data-connection-icon'),palette:await icon.getAttribute('data-connection-palette')}
 await alert.getByRole('button').click();await expect(alert).toHaveCount(0)
 await page.keyboard.press('Escape');await page.unroute('**/api/GetTargetState');await page.reload()
 await expect(icon).toHaveAttribute('data-connection-icon',before.icon!);await expect(icon).toHaveAttribute('data-connection-palette',before.palette!)
})

test('appearance waits for a slow stored value before enabling Save',async({page})=>{
 let release!:()=>void
 const held=new Promise<void>(resolve=>{release=resolve})
 await page.route('**/api/GetTargetState',async route=>{
  const request=route.request().postDataJSON()
  if(request.provider!=='synthetic'||request.target!=='demo')return route.continue()
  const response=await route.fetch(),body=await response.json()
  await held
  await route.fulfill({response,json:{...body,appearance:JSON.stringify({icon:'bird',palette:'violet'})}})
 })
 try{
  await page.goto('/');await selectConnection(page,/^demo\b/)
  const picker=await openConnections(page)
  await picker.getByRole('button',{name:'Actions for demo',exact:true}).click()
  await page.getByRole('menuitem',{name:'Connection appearance',exact:true}).click()
  const dialog=page.locator('.appearance-dialog')
  await expect(dialog.getByRole('status')).toBeVisible()
  await expect(dialog.getByRole('button',{name:'Save',exact:true})).toBeDisabled()
  await expect(dialog.locator('.appearance-icons button')).toHaveCount(0)
  release()
  await expect(dialog.getByRole('button',{name:'Icon 31',exact:true})).toHaveAttribute('aria-pressed','true')
  await expect(dialog.getByRole('button',{name:'Color 7',exact:true})).toHaveAttribute('aria-pressed','true')
  await dialog.getByRole('button',{name:'Save',exact:true}).click()
  const icon=page.locator('[data-connection-appearance=header] [data-connection-icon]')
  await expect(icon).toHaveAttribute('data-connection-icon','bird')
  await expect(icon).toHaveAttribute('data-connection-palette','violet')
  const response=await page.request.post('/api/GetTargetState',{headers:{Authorization:'Bearer '+await token(page),Origin:new URL(page.url()).origin},data:{provider:'synthetic',target:'demo'}})
  expect(JSON.parse((await response.json()).appearance)).toEqual({icon:'bird',palette:'violet'})
 }finally{release()}
})

test('picker uses aligned spacing, a section Add action and coherent whole-row hover',async({page})=>{
 await page.goto('/');await selectConnection(page,/^demo\b/)
 const picker=await openConnections(page),search=picker.locator('.connection-search'),field=picker.getByRole('combobox')
 await expect(search.locator('svg')).toHaveCount(0)
 expect(await field.evaluate(el=>parseFloat(getComputedStyle(el).paddingLeft))).toBeGreaterThanOrEqual(8)
 expect(await field.evaluate(el=>parseFloat(getComputedStyle(el).paddingRight))).toBeGreaterThanOrEqual(8)
 const heading=picker.getByRole('rowgroup',{name:'Kubernetes',exact:true}).getByRole('columnheader')
 expect(await heading.evaluate(el=>parseFloat(getComputedStyle(el).fontSize))).toBe(14)
 const gaps=await picker.evaluate(el=>Array.from(el.querySelectorAll('[role=rowgroup]')).slice(1).flatMap(group=>{
  const previous=group.previousElementSibling?.querySelectorAll('[data-connection-row]'),before=previous?.[previous.length-1],after=group.querySelector('[data-connection-row]'),title=group.querySelector('.connection-group > span')
  if(!before||!after||!title)return []
  const text=title.getBoundingClientRect()
  return [{before:text.top-before.getBoundingClientRect().bottom,after:after.getBoundingClientRect().top-text.bottom}]
 }))
 expect(gaps.length).toBeGreaterThan(0)
 for(const gap of gaps)expect(Math.abs(gap.before-gap.after)).toBeLessThanOrEqual(1)
 console.log('connection heading gaps',JSON.stringify(gaps))
 await expect(heading.getByRole('button',{name:'Import kubeconfig files',exact:true})).toBeVisible()
 await expect(picker.locator('footer')).toHaveCount(0)
 const row=picker.locator('[data-connection-row]').filter({hasText:'demo2'})
 const style=()=>row.evaluate(el=>({background:getComputedStyle(el).backgroundColor,outline:getComputedStyle(el).outlineStyle}))
 await heading.hover();const idle=await style()
 await row.locator('.connection-choice').hover();const hovered=await style();expect(hovered.background).not.toBe(idle.background);expect(hovered.outline).toBe('none')
 await row.locator('.connection-more').hover();expect(await style()).toEqual(hovered)
 expect(await row.locator('.connection-more').evaluate(el=>getComputedStyle(el).backgroundColor)).toBe('rgba(0, 0, 0, 0)')
 await heading.hover();expect(await style()).toEqual(idle)
 await field.focus();await field.fill('demo2');await field.press('ArrowDown')
 await expect(row).toHaveAttribute('data-cursor','true')
 await search.hover();await expect(row).toHaveAttribute('data-cursor','true')
 await field.fill('');await heading.getByRole('button',{name:'Import kubeconfig files',exact:true}).click()
 await expect(picker).toHaveCount(0);await expect(page.getByRole('dialog',{name:'Choose configurations to add',exact:true})).toBeVisible()
 await page.keyboard.press('Escape');await openConnections(page)
 await page.screenshot({path:join(scratchRoot,'connection-picker-polished.png')})
})

test('focus outlines follow Tab modality rather than mouse-focused text fields',async({page})=>{
 await page.goto('/');const picker=await openConnections(page),field=picker.getByRole('combobox')
 const outline=(selector:import('@playwright/test').Locator)=>selector.evaluate(el=>getComputedStyle(el).outlineStyle)
 await field.click();expect(await outline(field)).toBe('none')
 await expect(page.locator('html')).not.toHaveAttribute('data-tab-focus')
 const unselected=picker.locator('[data-connection-row][aria-selected=false]').first()
 await unselected.hover();await field.hover()
 const idleBackground=await unselected.evaluate(el=>getComputedStyle(el).backgroundColor)
 await page.keyboard.press('Tab')
 await expect(page.locator('html')).toHaveAttribute('data-tab-focus','true')
 const add=picker.getByRole('button',{name:'Import kubeconfig files',exact:true})
 await expect(add).toBeFocused();expect(await outline(add)).toBe('solid')
 await expect(picker.locator('[data-connection-row][data-cursor=true]')).toHaveCount(0)
 await expect.poll(()=>unselected.evaluate(el=>getComputedStyle(el).backgroundColor)).toBe(idleBackground)
 await field.click();expect(await outline(field)).toBe('none')
 await expect(page.locator('html')).not.toHaveAttribute('data-tab-focus')
 await page.keyboard.press('Shift+Tab');await expect(page.locator('html')).toHaveAttribute('data-tab-focus','true')
 await expect(picker.getByRole('button').last()).toBeFocused()
 await page.screenshot({path:join(scratchRoot,'connection-picker-tab-focus.png')})
 const focusedRow=picker.locator('[data-connection-row]').filter({has:page.locator(':focus')})
 expect(await outline(focusedRow)).toBe('solid')
 await picker.getByRole('columnheader').first().hover()
 await expect.poll(()=>outline(focusedRow)).toBe('none')
 await expect(picker.getByRole('button').last()).toBeFocused()
 await field.click();await page.screenshot({path:join(scratchRoot,'connection-picker-mouse-focus.png')})
 await page.keyboard.press('Escape')
 const menu=page.locator('[data-app-menu]');await menu.click()
 const language=page.locator('.language-menu button');expect(await outline(language)).toBe('none')
 expect(await language.evaluate(el=>getComputedStyle(el).borderColor)).toBe('rgb(48, 57, 70)')
 await page.keyboard.press('Tab');expect(await page.evaluate(()=>getComputedStyle(document.activeElement!).outlineStyle)).toBe('solid')
 await page.keyboard.press('Shift+Tab');await expect(language).toBeFocused()
 await expect.poll(()=>language.evaluate(el=>getComputedStyle(el).borderColor)).toBe('rgb(133, 175, 245)')
 await language.click();await page.keyboard.press('Escape')
 await expect.poll(()=>language.evaluate(el=>getComputedStyle(el).borderColor)).toBe('rgb(48, 57, 70)')
 expect(await outline(language)).toBe('none')
})


test('section Add remains reachable when the selected connection starts below the fold',async({page})=>{
 await page.route('**/api/ListTargets',async route=>{
  const response=await route.fetch(),body=await response.json()
  const group=body.groups.find((g:{provider:string})=>g.provider==='kubernetes'),first=group.targets[0]
  group.targets=Array.from({length:40},(_,i)=>({...first,id:'many-'+i,title:'Many '+i}))
  await route.fulfill({response,json:body})
 })
 await page.goto('/');await selectConnection(page,/^demo\b/)
 const picker=await openConnections(page),add=picker.getByRole('button',{name:'Import kubeconfig files',exact:true})
 await expect(picker.locator('[data-connection-row][aria-selected=true]')).toBeInViewport()
 await picker.getByRole('combobox').press('Tab');await expect(add).toBeFocused();await expect(add).toBeInViewport()
 await expect(picker.locator('[data-connection-row][data-cursor=true]')).toHaveCount(0)
 await page.screenshot({path:join(scratchRoot,'connection-picker-long-list.png')})
})

test('connection icons edit appearance directly without selecting or connecting; header controls align',async({page})=>{
 await page.setViewportSize({width:1280,height:820});await page.goto('/');await selectConnection(page,/^demo\b/)
 const trigger=page.locator('[data-connection-switcher]'),headerIcon=page.locator('[data-connection-appearance=header]')
 const selected=await trigger.textContent(),before=await stats(page)
 await headerIcon.click()
 const dialog=page.getByRole('dialog',{name:'Connection appearance',exact:true})
 await expect(dialog).toBeVisible();await dialog.getByRole('button',{name:'Cancel',exact:true}).click();await expect(headerIcon).toBeFocused()
 const picker=await openConnections(page),kube=picker.getByRole('rowgroup',{name:'Kubernetes',exact:true}),heading=kube.getByRole('columnheader')
 const avatar=(await headerIcon.locator('.connection-avatar').boundingBox())!,menu=(await picker.boundingBox())!
 for(const dot of [trigger.locator('[data-connection-state]'),picker.locator('[data-connection-state]').first()]){const b=(await dot.boundingBox())!;expect(b.width).toBe(10);expect(b.height).toBe(10)}
 expect(Math.abs(avatar.x-menu.x)).toBeLessThanOrEqual(1)
 expect((await page.locator('.connection-identity').boundingBox())!.width).toBeGreaterThanOrEqual(200)
 const title=(await heading.locator('span').first().boundingBox())!,count=(await heading.locator('.connection-group-count').boundingBox())!,actions=(await heading.locator('.connection-group-actions').boundingBox())!,bounds=(await heading.boundingBox())!
 expect(count.x-title.x-title.width).toBeLessThanOrEqual(9);expect(count.x).toBeGreaterThan(title.x+title.width)
 expect(Math.abs(actions.x+actions.width-(bounds.x+bounds.width-8))).toBeLessThanOrEqual(1)
 await expect(heading.getByRole('button')).toHaveCount(2)
 const docker=picker.getByRole('rowgroup',{name:'Docker',exact:true}).getByRole('columnheader').locator('img')
 await expect(docker).toBeVisible();expect((await docker.boundingBox())!.width).toBeGreaterThanOrEqual(24);await expect(docker).toHaveCSS('opacity','0.6')
 const row=picker.locator('[data-connection-row]').filter({hasText:'demo2'}),tile=(await row.locator('.connection-avatar').boundingBox())!,text=(await row.locator('.connection-choice > span').first().boundingBox())!
 expect(tile.height).toBe(36);expect(Math.abs(tile.height-text.height)).toBeLessThanOrEqual(1)
 await row.getByRole('button',{name:'Connection appearance: demo2',exact:true}).click()
 await expect(dialog.getByText('demo2',{exact:true})).toBeVisible();expect(await trigger.textContent()).toBe(selected)
 expect((await stats(page)).syn_connects).toBe(before.syn_connects)
 await dialog.getByRole('button',{name:'Cancel',exact:true}).click();await expect(row.getByRole('button',{name:'Connection appearance: demo2',exact:true})).toBeFocused()
 await expect(picker).toBeVisible();await page.screenshot({path:join(scratchRoot,'connection-picker-header-actions.png')})
})


test('row appearance keeps the picker search, scroll and focus through Cancel and Save',async({page})=>{
 await page.route('**/api/ListTargets',async route=>{
  const response=await route.fetch(),body=await response.json(),group=body.groups.find((g:{provider:string})=>g.provider==='kubernetes'),first=group.targets[0]
  group.targets=Array.from({length:40},(_,i)=>({...first,id:'retained-'+i,title:'Retained '+i}))
  await route.fulfill({response,json:body})
 })
 await page.goto('/');await selectConnection(page,/^demo\b/);const before=await stats(page),picker=await openConnections(page),search=page.locator('[data-connection-picker] [role=combobox]'),list=page.locator('[data-connection-picker] .connection-picker-list')
 await search.fill(' ');const icon=picker.getByRole('button',{name:'Connection appearance: demo',exact:true});await icon.scrollIntoViewIfNeeded()
 const scroll=await list.evaluate(el=>el.scrollTop);expect(scroll).toBeGreaterThan(0)
 for(const action of ['Cancel','Save']){
  await icon.click();const dialog=page.getByRole('dialog',{name:'Connection appearance',exact:true})
  await expect(dialog).toBeVisible();await expect(page.locator('[data-connection-picker]')).toHaveAttribute('aria-hidden','true');await expect(page.locator('[data-connection-picker]').locator('..')).toHaveAttribute('inert','')
  await expect(search).toHaveValue(' ');expect(await list.evaluate(el=>el.scrollTop)).toBe(scroll)
  await dialog.getByRole('button',{name:'Color 16',exact:true}).click();await dialog.getByRole('button',{name:action,exact:true}).click()
  await expect(picker).toBeVisible();await expect(icon).toBeFocused();await expect(search).toHaveValue(' ');expect(await list.evaluate(el=>el.scrollTop)).toBe(scroll)
 }
 expect((await stats(page)).syn_connects).toBe(before.syn_connects);expect((await stats(page)).sessions).toBe(before.sessions)
 await expect(page.locator('[data-connection-switcher]')).toContainText('demo')
 await page.screenshot({path:join(scratchRoot,'connection-picker-retained-after-appearance.png')})
})

test('connection search recovers keyboard layout only when original text has no matches',async({page})=>{
 await page.route('**/api/ListTargets',async route=>{
  const response=await route.fetch(),body=await response.json(),group=body.groups.find((g:{provider:string})=>g.provider==='kubernetes'),first=group.targets[0]
  group.targets.push({...first,id:'original-layout',title:'тест',subtitle:'original text'},{...first,id:'mapped-layout',title:'ntcn',subtitle:'mapped text'})
  await route.fulfill({response,json:body})
 })
 await page.goto('/');await selectConnection(page,/^demo\b/);const before=await stats(page),picker=await openConnections(page),search=picker.getByRole('combobox')
 for(const [typed,expected] of [['вуьщ','demo'],['ВУЬЩ','DEMO'],['іефпштп','staging']]){
  await search.fill(typed);await expect(search).toHaveValue(typed)
  await expect(picker.getByRole('status')).toContainText(expected)
  await expect(picker.locator('.connection-choice')).not.toHaveCount(0)
 }
 await search.fill('тест');await expect(picker.locator('.connection-choice')).toHaveCount(1);await expect(picker.locator('.connection-choice')).toContainText('тест');await expect(picker.getByRole('status')).toHaveCount(0)
 expect((await stats(page)).syn_connects).toBe(before.syn_connects);expect((await stats(page)).sessions).toBe(before.sessions)
 await page.screenshot({path:join(scratchRoot,'connection-search-original-layout.png')})
})

test('connection context menu shares one pointer and keyboard highlight and keeps Remove last',async({page})=>{
 await page.route('**/api/ListTargets',async route=>{
  const response=await route.fetch(),body=await response.json(),group=body.groups.find((g:{provider:string})=>g.provider==='kubernetes')
  group.targets[0]={...group.targets[0],open:true,connection:{state:'connected'}}
  await route.fulfill({response,json:body})
 })
 await page.goto('/');await selectConnection(page,/^demo\b/);const picker=await openConnections(page)
 await picker.getByRole('rowgroup',{name:'Kubernetes',exact:true}).locator('.connection-more').first().click()
 const menu=page.getByRole('menu',{name:'Target',exact:true}),items=menu.getByRole('menuitem'),disconnect=menu.getByRole('menuitem',{name:'Disconnect',exact:true}),remove=menu.getByRole('menuitem',{name:'Remove',exact:true})
 await expect(items.last()).toHaveText('Remove')
 const labels=await items.allTextContents();expect(labels.indexOf('Disconnect')).toBeLessThan(labels.indexOf('Remove'))
 await disconnect.hover();await expect(disconnect).toHaveAttribute('data-active','true');await expect(menu.locator('[data-active=true]')).toHaveCount(1)
 const color=await disconnect.evaluate(el=>getComputedStyle(el).backgroundColor);expect(color).not.toBe('rgba(0, 0, 0, 0)')
 await page.keyboard.press('ArrowDown');await expect(remove).toBeFocused();await expect(remove).toHaveCSS('background-color',color);await expect(disconnect).toHaveCSS('background-color','rgba(0, 0, 0, 0)')
 await disconnect.hover();await expect(disconnect).toHaveCSS('background-color',color);await expect(remove).toHaveCSS('background-color','rgba(0, 0, 0, 0)')
 await page.screenshot({path:join(scratchRoot,'connection-context-menu-hover.png')})
 await picker.getByRole('rowgroup',{name:'Kubernetes',exact:true}).getByRole('columnheader').hover();await expect(menu.locator('[data-active=true]')).toHaveCount(0)
})
