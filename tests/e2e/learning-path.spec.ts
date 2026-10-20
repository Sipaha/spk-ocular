import {expect,type Page,type Locator} from '@playwright/test'
import {test,openAppMenu,scratchRoot,selectConnection} from './fixtures'
import {token} from './synth'
import {join,resolve} from 'node:path'
import {readFileSync} from 'node:fs'
import {languages,languageNames,type Language} from '../../web/src/languages'
const pathContent=Object.fromEntries(languages.map(lang=>[lang,JSON.parse(readFileSync(resolve(import.meta.dirname,`../../web/src/learning/path-${lang}.json`),'utf8'))])) as Record<Language,{ui:Record<string,string>;topics:Record<string,string>;diagnoses:Record<string,string>}>
const learningContent=Object.fromEntries(languages.map(lang=>[lang,JSON.parse(readFileSync(resolve(import.meta.dirname,`../../web/src/learning/${lang}.json`),'utf8'))])) as Record<Language,{ui:Record<string,string>}>
import {topics,type Topic,type CaseId} from '../../web/src/learning/pathModel'

const en=pathContent.en
const plans:Record<Topic,Array<{id:CaseId;repair:string;evidence:[string,string][];result:string}>>={
 network:[{id:'selector',repair:'selector',evidence:[['service','YAML'],['pod','YAML'],['endpoints','Properties']],result:'endpoints'},{id:'port',repair:'port',evidence:[['service','YAML'],['pod','YAML'],['endpoints','Properties']],result:'endpoints'}],
 probes:[{id:'readiness',repair:'readiness',evidence:[['deployment','YAML'],['pod','Events'],['pod','Logs']],result:'pod'},{id:'startup',repair:'startup',evidence:[['deployment','YAML'],['pod','Events'],['pod','Logs']],result:'pod'}],
 runtime:[{id:'image',repair:'image',evidence:[['deployment','YAML'],['pod','Events'],['catalog','Properties']],result:'pod'},{id:'command',repair:'command',evidence:[['deployment','YAML'],['pod','Events'],['pod','Logs']],result:'pod'}],
 config:[{id:'environment',repair:'rollout',evidence:[['deployment','YAML'],['config','YAML'],['pod','Properties']],result:'pod'},{id:'secret',repair:'secret',evidence:[['deployment','YAML'],['secret','Properties'],['pod','Events']],result:'pod'}],
 resources:[{id:'requests',repair:'requests',evidence:[['deployment','YAML'],['pod','Events'],['node','Properties']],result:'pod'},{id:'memory',repair:'memory',evidence:[['deployment','YAML'],['pod','Properties'],['node','Properties']],result:'pod'}],
}
const objectName=(id:string,root:string,fixed=false)=>id==='pod'?root+(fixed?'-new':'-a'):id==='endpoints'?root+'-slice':id==='config'?root+'-config':id==='secret'?root+'-credentials':id==='catalog'?'approved-images':id==='node'?'lesson-node':root
async function visit(lab:Locator,id:string,view:string,root:string,fixed=false){
 await lab.getByRole('gridcell',{name:objectName(id,root,fixed),exact:true}).click()
 await lab.getByRole('tab',{name:view,exact:true}).click()
 await expect(lab.getByRole('tab',{name:view,exact:true})).toHaveAttribute('aria-selected','true')
 await expect(lab.getByRole('tab',{name:view,exact:true})).toHaveClass(/bg-active/)
 await expect(lab.locator('[role=tab][aria-selected=true]')).toHaveCount(1)
 await expect(lab.getByRole('tabpanel',{name:view,exact:true})).toBeVisible()
 if(view==='YAML')await expect(lab.getByRole('tabpanel',{name:view,exact:true})).toContainText('apiVersion:')
}
async function openCourse(page:Page){
 await openAppMenu(page);await page.getByRole('button',{name:'Kubernetes learning',exact:true}).click()
 const dialog=page.locator('[data-learning]');await dialog.getByRole('button',{name:en.ui.course,exact:true}).click();return dialog
}

test.beforeEach(async({page})=>{
 await page.goto('/')
 const result=await page.request.post('/api/SetLanguage',{headers:{Authorization:`Bearer ${await token(page)}`,Origin:new URL(page.url()).origin},data:{language:'en'}});expect(result.ok()).toBeTruthy()
 await selectConnection(page,/^prod\b/);await page.reload();await expect(page.getByRole('button',{name:'Connect',exact:true})).toBeVisible()
})
test.afterEach(async({page})=>{
 if(!page.url().startsWith('http'))return
 await page.request.post('/api/SetLanguage',{headers:{Authorization:`Bearer ${await token(page)}`,Origin:new URL(page.url()).origin},data:{language:'en'}})
})

for(const topic of topics)test(`learning path ${topic}: evidence, repairs, observation and two independent checks stay local`,async({page,context})=>{
 const forbidden:string[]=[],errors:string[]=[];page.on('pageerror',e=>errors.push(e.message));page.on('request',r=>{if(/\/api\/(ConnectTarget|OpenView|GetResource|Prepare|Run|Files|OpenTerminal|ListKinds|ListScopes|GetEditSource|SetTargetState)/.test(r.url()))forbidden.push(r.url())})
 const dialog=await openCourse(page);await dialog.locator(`[data-course-topic=${topic}]`).getByRole('button').click()
 const lab=dialog.locator(`[data-task-lesson=${topic}]`)
 await context.setOffline(true)
 for(let step=0;step<4;step++){
  const plan=plans[topic][step%2],root=step>=2?'orders-1':'web-1'
  await expect(lab).toHaveAttribute('data-case',plan.id);await expect(lab).toHaveAttribute('data-phase',step>=2?'check':'practice')
  if(step>=2){await expect(lab.getByRole('button',{name:en.ui.hint,exact:true})).toHaveCount(0);await expect(lab.getByRole('button',{name:en.ui.more,exact:true})).toHaveCount(0)}
  await lab.getByRole('radio',{name:en.diagnoses[plan.id],exact:true}).check()
  await lab.locator(`input[name$="-repair"][value="${plan.repair}"]`).check()
  if(step===0){await lab.getByRole('button',{name:en.ui.apply,exact:true}).click();await expect(lab.getByRole('alert')).toHaveText(en.ui.evidenceError)}
  for(const [id,view]of plan.evidence){await visit(lab,id,view,root);if(step===0&&id===plan.evidence[0][0])await page.screenshot({animations:'disabled',path:join(scratchRoot,`course-${topic}-evidence.png`)})}
  await expect(lab.locator('[data-required-evidence] [data-inspected=true]')).toHaveCount(3)
  if(step===0){
   await lab.locator(`input[name$="-diagnosis"]:not([value="${plan.id}"])`).first().check();await lab.getByRole('button',{name:en.ui.apply,exact:true}).click();await expect(lab.getByRole('alert')).toHaveText(en.ui.diagnosisError)
   await lab.getByRole('radio',{name:en.diagnoses[plan.id],exact:true}).check();await lab.locator(`input[name$="-repair"]:not([value="${plan.repair}"])`).first().check();await lab.getByRole('button',{name:en.ui.apply,exact:true}).click();await expect(lab.getByRole('alert')).toHaveText(en.ui.repairError)
   await lab.locator(`input[name$="-repair"][value="${plan.repair}"]`).check()
  }
  await lab.getByRole('button',{name:en.ui.apply,exact:true}).click();await expect(lab).toHaveAttribute('data-phase','fixed');await expect(lab).toBeFocused()
  const next=lab.getByRole('button',{name:step===3?en.ui.finish:en.ui.next,exact:true});await next.click();await expect(lab.getByRole('alert')).toHaveText(en.ui.observe)
  await visit(lab,plan.result,'Properties',root,topic!=='network')
  if(step===0)await page.screenshot({animations:'disabled',path:join(scratchRoot,`course-${topic}-result.png`)})
  await next.click()
 }
 await expect(lab).toHaveAttribute('data-phase','complete');await expect(lab).toContainText(en.ui.complete)
 await expect(dialog.locator('[data-course-progress]')).toHaveText('1 of 6 lessons complete')
 await page.screenshot({animations:'disabled',path:join(scratchRoot,`course-${topic}-complete.png`)})
 await lab.getByRole('button',{name:'Reset lesson',exact:true}).click();await expect(lab).toHaveAttribute('data-phase','practice');await expect(dialog.locator('[data-course-progress]')).toHaveText('0 of 6 lessons complete')
 await context.setOffline(false);expect(forbidden).toEqual([]);expect(errors).toEqual([])
})

test('course locale switches, module navigation and help retain evidence; narrow layouts and closing remain bounded',async({page})=>{
 const dialog=await openCourse(page);await dialog.locator('[data-course-topic=network]').getByRole('button').click();const lab=dialog.locator('[data-task-lesson=network]')
 await visit(lab,'service','YAML','web-1')
 await lab.getByRole('button',{name:en.ui.more,exact:true}).click();await lab.getByRole('navigation',{name:'Related concepts',exact:true}).getByRole('button',{name:'Pod',exact:true}).click();await expect(lab).toBeFocused()
 await dialog.getByRole('button',{name:en.ui.overview,exact:true}).click();await dialog.locator('[data-course-topic=config]').getByRole('button').click();await dialog.getByRole('button',{name:en.ui.overview,exact:true}).click();await dialog.locator('[data-course-topic=network]').getByRole('button').click()
 await expect(lab.locator('[data-evidence="service:yaml"]')).toHaveAttribute('data-inspected','true')
 for(const language of languages){
  await dialog.locator('.language-menu button').click();await dialog.getByRole('option',{name:languageNames[language],exact:true}).click();await expect(page.locator('html')).toHaveAttribute('lang',language)
  await expect(lab.locator('[data-evidence="service:yaml"]')).toHaveAttribute('data-inspected','true')
  for(const width of [375,1440]){
   await page.setViewportSize({width,height:900});expect(await dialog.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true)
   const bounds=(await dialog.boundingBox())!;expect(bounds.x).toBeGreaterThanOrEqual(0);expect(bounds.x+bounds.width).toBeLessThanOrEqual(width)
   await expect(lab.getByRole('button',{name:pathContent[language].ui.apply,exact:true})).toBeVisible()
   await page.screenshot({animations:'disabled',path:join(scratchRoot,`course-${language}-${width}.png`)})
  }
 }
 await page.keyboard.press('Escape');await expect(dialog).toHaveCount(0);await expect(page.locator('[data-app-menu]')).toBeFocused()
 // Reopening is a new in-memory course; it must not imply saved certification.
 const language=languages.at(-1)!,ui=pathContent[language].ui
 await openAppMenu(page);await page.getByRole('button',{name:learningContent[language].ui.library,exact:true}).click();await dialog.getByRole('button',{name:ui.course,exact:true}).click();await dialog.locator('[data-course-topic=network]').getByRole('button').click()
 await expect(dialog.locator('[data-evidence="service:yaml"]')).toHaveAttribute('data-inspected','false')
})
