import {expect} from '@playwright/test'
import {test,connectSelected,scratchRoot} from './fixtures'
import {join} from 'node:path'
import type {Page} from '@playwright/test'

async function timelinePage(page:Page) {
 await page.goto('/')
 await page.getByRole('option',{name:/^demo\b/}).click()
 await connectSelected(page)
 const nav=page.getByRole('navigation',{name:'resources'})
 await nav.getByRole('textbox',{name:'Filter resources'}).fill('Events timeline')
 await nav.getByRole('button',{name:'Events timeline',exact:true}).click()
 await nav.getByRole('textbox',{name:'Filter resources'}).fill('')
 const timeline=page.getByRole('region',{name:'Events timeline'})
 const zone=timeline.getByRole('textbox',{name:'Zone',exact:true})
 await zone.fill('blue');await zone.press('Enter')
 await expect(timeline.getByRole('button',{name:'Refresh',exact:true})).toBeEnabled()
 return timeline
}

test('timeline filters, opens standard properties, refreshes and supports keyboard inspection',async({page})=>{
 const timeline=await timelinePage(page)
 await expect(timeline).toContainText('4 / 4 event series')
 const grid=timeline.getByRole('grid',{name:'Resource events over time'})
 await grid.focus();await page.keyboard.press('ArrowDown')
 await expect(timeline.getByRole('button',{name:'Close event',exact:true})).toBeVisible()
 const search=timeline.getByRole('textbox',{name:'Find a workload, reason or message'})
 await search.fill('alpha')
 await expect(timeline).toContainText('1 / 4 event series')
 await grid.getByRole('button',{name:/BackOff · Crate\/alpha/}).click()
 await timeline.getByRole('button',{name:'Open resource · Crate/alpha',exact:true}).click()
 const details=timeline.locator('[data-area=details]')
 await expect(details.getByText('alpha',{exact:true}).first()).toBeVisible()
 await expect(details.getByRole('tab',{name:'YAML',exact:true})).toBeVisible()
 await page.screenshot({path:join(scratchRoot,'timeline-details.png')})
 await search.press('Escape')
 await expect(timeline).toContainText('4 / 4 event series')
 await timeline.getByRole('checkbox',{name:'Warnings only'}).check()
 await expect(timeline).toContainText('2 / 4 event series')
 await timeline.getByRole('button',{name:'Refresh',exact:true}).click()
 await expect(timeline.getByRole('button',{name:'Refresh',exact:true})).toBeEnabled()
 await expect(details.getByText('alpha',{exact:true}).first()).toBeVisible()
 const zone=timeline.getByRole('textbox',{name:'Zone',exact:true})
 await zone.fill('blue, green');await zone.press('Enter')
 await expect(timeline).toContainText('5 / 5 event series')
 await page.screenshot({path:join(scratchRoot,'timeline-two-scopes.png')})
})

test('timeline discloses incomplete coverage, historical ownership and aggregate intervals',async({page})=>{
 const now=Date.now(),ref={provider:'synthetic',target:'demo',kind:'crates',scope:'blue',name:'crate-7f3a',title:'alpha',uid:'old-uid'}
 await page.route('**/api/ClusterTimeline',route=>route.fulfill({json:{events:[{id:'historical',ref:{},subject:ref,subjectKind:'Crate',openable:true,type:'Warning',reason:'FailedScheduling',message:'Insufficient memory',count:7,firstAt:now-120000,lastAt:now-30000,timeFallback:false,messageTruncated:false}],resources:[{ref:{...ref,uid:'new-uid'},kindTitle:'Crate',ownerUid:'parent'},{ref:{...ref,name:'parent',uid:'parent'},kindTitle:'Deployment'}],problems:[{kind:'pods',scope:'blue',class:'forbidden'}],discovery:'partial',truncated:true,capturedAt:now}}))
 const timeline=await timelinePage(page)
 await expect(timeline).toContainText('Ownership unresolved')
 await expect(timeline).toContainText('Resource discovery is incomplete')
 await expect(timeline).toContainText('Timeline limit reached')
 await timeline.locator('summary').click()
 await expect(timeline).toContainText('pods / blue · forbidden')
 await timeline.getByRole('grid').getByRole('button',{name:/FailedScheduling/}).click()
 await expect(timeline).toContainText('7 occurrences (cumulative)')
 await expect(timeline).toContainText('Bars span first/last observations, not continuous failure duration.')
 await page.route('**/api/GetResource',route=>{expect(route.request().postDataJSON().uid).toBe('old-uid');return route.fulfill({status:404,json:{code:'not_found',detail:'object no longer exists'}})})
 await timeline.getByRole('button',{name:'Open resource · Crate/alpha',exact:true}).click()
 await expect(timeline.locator('[data-area=details]')).toContainText('no longer exists')
 await page.screenshot({path:join(scratchRoot,'timeline-partial-historical.png')})
 await page.unrouteAll({behavior:'wait'})
})

test('timeline virtualizes 10,000 event series and retains selection across all eight languages',async({page})=>{
 const now=Date.now(),ref={provider:'synthetic',target:'demo',kind:'crates',scope:'blue',name:'crate-7f3a',title:'alpha',uid:'crate-7f3a'}
 await page.route('**/api/ClusterTimeline',route=>route.fulfill({json:{events:Array.from({length:10000},(_,i)=>({id:'event-'+i,ref:{},subject:ref,subjectKind:'Crate',openable:true,type:i%3?'Normal':'Warning',reason:i%3?'Started':'BackOff',message:'Container observation '+i,count:1,firstAt:now-(10000-i)*1000,lastAt:now-(10000-i)*1000,timeFallback:false,messageTruncated:false})),resources:[{ref,kindTitle:'Crate'}],problems:[],discovery:'ready',truncated:false,capturedAt:now}}))
 const timeline=await timelinePage(page)
 await expect(timeline).toContainText('10000 / 10000 event series')
 expect(await timeline.getByRole('row').count()).toBeLessThan(60)
 const grid=timeline.getByRole('grid');await grid.focus();await page.keyboard.press('ArrowDown')
 await timeline.getByRole('button',{name:'Open resource · Crate/alpha',exact:true}).click()
 const names={en:'English',ru:'Русский',zh:'简体中文',es:'Español',de:'Deutsch',fr:'Français',pt:'Português (Brasil)',ja:'日本語'}
 for(const [code,name] of Object.entries(names)){
  await page.locator('.language-menu button').click();await page.getByRole('option',{name,exact:true}).click()
  await expect(page.locator('html')).toHaveAttribute('lang',code)
  await expect(page.locator('.timeline-workspace [data-area=details]').getByText('alpha',{exact:true}).first()).toBeVisible()
  await expect(page.locator('.timeline-workspace [role=row][aria-selected=true]')).toHaveCount(1)
  await page.screenshot({path:join(scratchRoot,'timeline-language-'+code+'.png')})
 }
 await page.locator('.language-menu button').click();await page.getByRole('option',{name:'English',exact:true}).click()
 await timeline.getByRole('button',{name:'Resource kind',exact:true}).click()
 await page.getByRole('option',{name:'Crate',exact:true}).click()
 await timeline.getByRole('button',{name:'Time range',exact:true}).click()
 await page.getByRole('option',{name:'Last 15 minutes',exact:true}).click()
 await expect(timeline).toContainText('900 / 10000 event series')
 await page.screenshot({path:join(scratchRoot,'timeline-filtered-10000.png')})
 await page.unrouteAll({behavior:'wait'})
})
