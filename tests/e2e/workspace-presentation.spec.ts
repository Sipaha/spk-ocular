import {expect} from '@playwright/test'
import {test,connectSelected,scratchRoot,selectConnection} from './fixtures'
import {join} from 'node:path'

for(const [title,method,selector] of [
 ['Releases','Helm','.helm-workspace'],['Cluster graph','ClusterGraph','.graph-workspace'],
 ['Events timeline','ClusterTimeline','.timeline-workspace'],['Security','ClusterSecurity','.security-workspace'],
 ['RBAC explanation','RBACSnapshot','.rbac-workspace'],
])test(`${title} shares stable toolbar, delayed progress, cancel and explicit retry`,async({page})=>{
 await page.setViewportSize({width:1440,height:1000})
 let slow=false,calls=0,release:()=>void=()=>{}
 await page.route(`**/api/${method}`,async route=>{
  const response=await route.fetch(),body=await response.body()
  if(slow&&(method!=='Helm'||['list','catalog'].includes(route.request().postDataJSON().command))){calls++;await new Promise<void>(resolve=>{release=resolve})}
  try{await route.fulfill({response,body})}catch{/* Cancelled renderer requests cannot accept a late response. */}
 })
 await page.goto('/');await selectConnection(page,/^demo\b/);await connectSelected(page)
 const nav=page.getByRole('navigation',{name:'resources'}),filter=nav.getByRole('textbox').first()
 await filter.fill(title);await nav.getByRole('button',{name:title,exact:true}).click();await filter.fill('')
 const workspace=page.locator(selector)
 if(title==='Cluster graph')await workspace.getByRole('button',{name:'Load graph',exact:true}).click()
 const refresh=workspace.getByRole('button',{name:'Refresh',exact:true})
 await expect(refresh).toHaveAttribute('aria-busy','false')
 if(title==='Cluster graph')await expect(workspace.locator('canvas')).toHaveAttribute('data-zoom',/.+/)
 const toolbar=workspace.locator('.resource-list-toolbar'),before=await toolbar.boundingBox(),buttonBefore=await refresh.boundingBox()
 await expect(toolbar.getByRole('heading',{name:title,exact:true})).toHaveClass('sr-only')
 slow=true;await refresh.click();await expect.poll(()=>calls).toBe(1)
 await expect(refresh).toHaveAttribute('aria-busy','true');await expect(refresh).toBeDisabled()
 expect(await toolbar.boundingBox()).toEqual(before);expect(await refresh.boundingBox()).toEqual(buttonBefore)
 await expect(workspace.locator('[data-load-progress]')).toBeVisible()
 await page.screenshot({path:join(scratchRoot,`shared-progress-${method}.png`)})
 expect(await toolbar.boundingBox()).toEqual(before);expect(await refresh.boundingBox()).toEqual(buttonBefore)
 await workspace.getByRole('button',{name:'Cancel',exact:true}).click()
 await expect(workspace.locator('[data-load-cancelled]')).toBeVisible()
 release();slow=false
 await page.screenshot({path:join(scratchRoot,`shared-cancelled-${method}.png`)})
 await expect(workspace.locator('[data-load-cancelled]')).toBeVisible();expect(calls).toBe(1)
 await workspace.getByRole('button',{name:'Retry',exact:true}).click()
 await expect(workspace.locator('[data-load-cancelled]')).toHaveCount(0);await expect(refresh).toHaveAttribute('aria-busy','false')
 expect(await toolbar.boundingBox()).toEqual(before)
 await expect(page.locator('[data-location-status]')).toContainText(title)
})
