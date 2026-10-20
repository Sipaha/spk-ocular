import {expect,type Page} from '@playwright/test'
import {test,openAppMenu,scratchRoot,selectConnection,connectSelected} from './fixtures'
import {join,resolve} from 'node:path'
import {readFileSync} from 'node:fs'
import {languages,languageNames} from '../../web/src/languages'
import {concepts} from '../../web/src/learning/catalog'
const learningContent=Object.fromEntries(languages.map(lang=>[lang,JSON.parse(readFileSync(resolve(import.meta.dirname,`../../web/src/learning/${lang}.json`),'utf8'))])) as Record<string,{ui:Record<string,string>}>
import {token} from './synth'

test.afterEach(async({page})=>{
 if(!page.url().startsWith('http'))return
 await page.request.post('/api/SetLanguage',{headers:{Authorization:`Bearer ${await token(page)}`,Origin:new URL(page.url()).origin},data:{language:'en'}})
})

test.beforeEach(async({page})=>{
 await page.goto('/')
 const reset=await page.request.post('/api/SetLanguage',{headers:{Authorization:`Bearer ${await token(page)}`,Origin:new URL(page.url()).origin},data:{language:'en'}})
 expect(reset.ok()).toBeTruthy();await page.reload()
 // Earlier specs may retain an explicitly connected synthetic target. Start the
 // offline cases on the fixture's unopened context, then dispose the old page
 // so pending workspace initialization cannot be mistaken for lesson traffic.
 await selectConnection(page,/^prod\b/);await page.reload()
 await expect(page.getByRole('button',{name:'Connect',exact:true})).toBeVisible()
})
const dialogForCourse=(page:Page)=>page.locator('[data-learning]')
async function library(page:Page){await openAppMenu(page);await page.getByRole('button',{name:'Kubernetes learning',exact:true}).click();const dialog=page.locator('[data-learning]');await expect(dialog).toBeVisible();return dialog}
async function lesson(page:Page){const dialog=await library(page);await dialog.getByRole('button',{name:'Why did the Pod come back?',exact:true}).click();return dialog.locator('[data-lesson]')}
async function replace(page:Page){const lab=await lesson(page);await lab.getByRole('gridcell',{name:'web-7db9-a',exact:true}).click();await lab.getByRole('button',{name:'Delete simulated Pod',exact:true}).click();await expect(lab).toHaveAttribute('data-phase','deleted');await expect(lab.getByRole('grid')).toHaveAttribute('aria-rowcount','3');await lab.getByRole('button',{name:'Advance simulated controller',exact:true}).click();await expect(lab).toHaveAttribute('data-phase','replaced');return lab}

test('offline help covers the built-in inventory and related concepts without connecting or fetching schemas',async({page,context})=>{
 const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message))
 const calls:string[]=[];page.on('request',r=>{if(/\/api\/(ConnectTarget|OpenView|GetResource|Prepare|Run|TouchRecent|GetEditSource|ListKinds)/.test(r.url()))calls.push(r.url().split('/').at(-1)!)})
 const dialog=await library(page)
 await context.setOffline(true)
 for(const entry of concepts){await dialog.locator('aside nav').getByRole('button',{name:entry[1],exact:true}).click();await expect(dialog.locator('[data-resource-explanation]')).toHaveAttribute('data-resource-explanation',entry[0]);await expect(dialog.locator('article h3')).toHaveText(entry[1]);await expect(dialog.getByRole('link')).toHaveAttribute('href',/^https:\/\/kubernetes.io\/docs\//)}
 await dialog.locator('aside nav').getByRole('button',{name:'ReplicaSet',exact:true}).click()
 await dialog.getByRole('navigation',{name:'Related concepts'}).getByRole('button',{name:'Deployment',exact:true}).click();await expect(dialog.locator('article h3')).toHaveText('Deployment')
 await dialog.getByText('Key fields',{exact:true}).click();await expect(dialog.getByText('spec.replicas',{exact:true})).toBeVisible()
 await page.screenshot({path:join(scratchRoot,'learning-help-en.png')})
 expect(calls).toEqual([]);expect(errors).toEqual([])
 await dialog.locator('article nav').getByRole('button',{name:'ReplicaSet',exact:true}).click()
 await expect(dialog).toBeFocused()
 await page.keyboard.press('Escape');await expect(dialog).toHaveCount(0);await expect(page.locator('[data-app-menu]')).toBeFocused()
 await context.setOffline(false)
})

test('lesson observes deletion and new identity, navigates properties YAML events logs and requires owner evidence',async({page})=>{
 const forbidden:string[]=[];page.on('request',r=>{if(/\/api\/(ConnectTarget|OpenView|GetResource|Prepare|Run|TouchRecent|SetTargetState|GetEditSource)/.test(r.url()))forbidden.push(r.url())})
 const lab=await replace(page)
 await lab.getByRole('gridcell',{name:'web-7db9-c',exact:true}).click()
 await expect(lab.getByText('pod-new-0',{exact:true})).toBeVisible()
 await lab.getByRole('tab',{name:'YAML',exact:true}).click();await expect(lab.locator('.cm-content')).toContainText('ownerReferences')
 await lab.getByRole('tab',{name:'Events',exact:true}).click();await expect(lab.getByRole('tabpanel')).toContainText('SuccessfulCreate')
 await lab.getByRole('tab',{name:'Logs',exact:true}).click();await expect(lab.getByRole('tabpanel')).toContainText('app: ready to serve')
 await page.screenshot({path:join(scratchRoot,'learning-replacement-en.png')})
 await lab.getByRole('button',{name:'Try without hints',exact:true}).click();await expect(lab.getByRole('button',{name:'Show a hint',exact:true})).toHaveCount(0)
 await lab.getByRole('gridcell',{name:'orders',exact:true}).click();await lab.getByRole('radio',{name:learningContent.en.ui.answerController,exact:true}).check();await lab.getByRole('button',{name:'Check explanation',exact:true}).click();await expect(lab.getByRole('alert')).toContainText('First navigate')
 await lab.getByRole('gridcell',{name:'orders-7db9-a',exact:true}).click();await lab.locator('[data-lesson-owner]').click();await lab.locator('[data-lesson-owner]').click();await expect(lab.getByRole('heading',{name:'orders',exact:true})).toBeVisible()
 await lab.getByRole('radio',{name:learningContent.en.ui.answerRestart,exact:true}).check();await lab.getByRole('button',{name:'Check explanation',exact:true}).click();await expect(lab.getByRole('alert')).toContainText('does not explain')
 await lab.getByRole('radio',{name:learningContent.en.ui.answerController,exact:true}).check();await lab.getByRole('button',{name:'Check explanation',exact:true}).click();await expect(lab).toHaveAttribute('data-phase','complete');await expect(lab).toBeFocused()
 await dialogForCourse(page).getByRole('button',{name:'Learning path',exact:true}).click();await expect(page.locator('[data-course-progress]')).toHaveText('1 of 6 lessons complete');await dialogForCourse(page).getByRole('button',{name:'Why did the Pod come back?',exact:true}).click()
 await page.screenshot({path:join(scratchRoot,'learning-check-complete-en.png')})
 await lab.getByRole('button',{name:'Reset lesson',exact:true}).click();await expect(lab).toHaveAttribute('data-phase','observe');await expect(lab.getByRole('grid')).toHaveAttribute('aria-rowcount','4')
 await dialogForCourse(page).getByRole('button',{name:'Learning path',exact:true}).click();await expect(page.locator('[data-course-progress]')).toHaveText('0 of 6 lessons complete')
 expect(forbidden).toEqual([])
})

test('all eight lesson languages retain progress and fit narrow and wide windows',async({page})=>{
 const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message))
 const lab=await replace(page),dialog=page.locator('[data-learning]')
 for(const lang of languages){
  await dialog.locator('.language-menu button').click();await dialog.getByRole('option',{name:languageNames[lang],exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang',lang)
  await expect(lab).toHaveAttribute('data-phase','replaced');await expect(lab.getByRole('status')).toHaveText(learningContent[lang].ui.replaced)
  for(const width of [375,1440]){
   await page.setViewportSize({width,height:900})
   expect(await dialog.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true)
   const bounds=(await dialog.boundingBox())!;expect(bounds.x).toBeGreaterThanOrEqual(0);expect(bounds.x+bounds.width).toBeLessThanOrEqual(width)
   if(lang==='en'||lang==='ru')await page.screenshot({path:join(scratchRoot,`learning-lesson-${lang}-${width}.png`)})
  }
 }
 expect(errors).toEqual([])
})

test('help remains an overlay over a connected workspace and shortcuts cannot act on the background',async({page})=>{
 await selectConnection(page,/^demo\b/);await connectSelected(page)
 const grid=page.getByRole('grid',{name:'resources',exact:true}),before=await grid.elementHandle()
 const dialog=await library(page),calls:string[]=[]
 page.on('request',r=>{if(/\/api\/(Prepare|Run|ResyncView|RefreshKinds|ConnectTarget)/.test(r.url()))calls.push(r.url())})
 await page.keyboard.press('F5');await page.keyboard.press('Control+F5');await page.keyboard.press('Delete');await page.keyboard.press('Control+k')
 await expect(dialog).toBeVisible();expect(calls).toEqual([])
 await dialog.getByRole('button',{name:'Close',exact:true}).click()
 expect(await grid.evaluate((el,old)=>el===old,before)).toBe(true)
})
