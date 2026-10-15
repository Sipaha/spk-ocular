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
 await picker.getByRole('combobox').fill('demo2')
 await expect(picker.locator('.connection-choice')).toHaveCount(1)
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

test('appearance has32icons and8preset pairs; persists through reload and target switches',async({page})=>{
 await page.goto('/');await selectConnection(page,/^demo\b/)
 const original=page.locator('[data-connection-switcher] [data-connection-icon]')
 await expect(original).toHaveAttribute('data-connection-icon',/./)
 const first=await original.getAttribute('data-connection-icon'),color=await original.getAttribute('data-connection-palette')
 await page.reload();await expect(original).toHaveAttribute('data-connection-icon',first!);await expect(original).toHaveAttribute('data-connection-palette',color!)
 const picker=await openConnections(page)
 await picker.getByRole('button',{name:'Actions for demo',exact:true}).click()
 await page.getByRole('menuitem',{name:'Connection appearance',exact:true}).click()
 const dialog=page.getByRole('dialog',{name:'Connection appearance',exact:true})
 await expect(dialog.locator('.appearance-icons button')).toHaveCount(32)
 await expect(dialog.locator('.appearance-palette button')).toHaveCount(8)
 await expect(dialog.locator('input[type=color]')).toHaveCount(0)
 await dialog.getByRole('button',{name:'Icon 32',exact:true}).click()
 await dialog.getByRole('button',{name:'Colour pair 3',exact:true}).click()
 await expect(dialog.getByRole('button',{name:'Icon 32',exact:true})).toHaveAttribute('aria-pressed','true')
 await expect(dialog.getByRole('button',{name:'Colour pair 3',exact:true})).toHaveAttribute('aria-pressed','true')
 await page.screenshot({path:join(scratchRoot,'connection-appearance.png')})
 await dialog.getByRole('button',{name:'Cancel',exact:true}).click()
 await expect(original).toHaveAttribute('data-connection-icon',first!)
 await openConnections(page);await page.getByRole('button',{name:'Actions for demo',exact:true}).click();await page.getByRole('menuitem',{name:'Connection appearance',exact:true}).click()
 await dialog.getByRole('button',{name:'Icon 32',exact:true}).click();await dialog.getByRole('button',{name:'Colour pair 3',exact:true}).click();await dialog.getByRole('button',{name:'Save',exact:true}).click()
 await expect(original).toHaveAttribute('data-connection-icon','cat');await expect(original).toHaveAttribute('data-connection-palette','green')
 await selectConnection(page,/^demo2\b/);await selectConnection(page,/^demo\b/);await page.reload()
 await expect(original).toHaveAttribute('data-connection-icon','cat');await expect(original).toHaveAttribute('data-connection-palette','green')
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
 const icon=page.locator('[data-connection-switcher] [data-connection-icon]'),before={icon:await icon.getAttribute('data-connection-icon'),palette:await icon.getAttribute('data-connection-palette')}
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
  await expect(dialog.getByRole('button',{name:'Colour pair 7',exact:true})).toHaveAttribute('aria-pressed','true')
  await dialog.getByRole('button',{name:'Save',exact:true}).click()
  const icon=page.locator('[data-connection-switcher] [data-connection-icon]')
  await expect(icon).toHaveAttribute('data-connection-icon','bird')
  await expect(icon).toHaveAttribute('data-connection-palette','violet')
  const response=await page.request.post('/api/GetTargetState',{headers:{Authorization:'Bearer '+await token(page),Origin:new URL(page.url()).origin},data:{provider:'synthetic',target:'demo'}})
  expect(JSON.parse((await response.json()).appearance)).toEqual({icon:'bird',palette:'violet'})
 }finally{release()}
})
