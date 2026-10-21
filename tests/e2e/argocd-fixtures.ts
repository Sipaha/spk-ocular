import {expect,type Page} from '@playwright/test'
import {selectConnection,connectSelected} from './fixtures'
export async function english(page:Page){
 const response=await page.request.get('/'),html=await response.text(),token=html.match(/spk-ocular-api-token" content="([^"]+)"/)?.[1]
 const saved=await page.request.post('/api/SetLanguage',{headers:{Authorization:'Bearer '+token,Origin:new URL(response.url()).origin},data:{language:'en'}})
 expect(saved.ok()).toBeTruthy()
}
export async function argoPage(page:Page,name:string){
 const nav=page.getByRole('navigation',{name:'resources'}),region=nav.getByRole('region',{name:'Argo CD',exact:true})
 const heading=region.getByRole('button',{name:/^Argo CD \(/})
 if(await heading.getAttribute('aria-expanded')!=='true')await heading.click()
 if(name==='Repositories'||name==='Clusters'){
  const settings=region.getByRole('button',{name:/^Settings/})
  if(await settings.getAttribute('aria-expanded')!=='true')await settings.click()
 }
 await region.getByRole('button',{name,exact:true}).click({position:{x:8,y:14}})
 return page.locator('.argo-workspace')
}
export async function openArgo(page:Page){
 await page.goto('/');await selectConnection(page,/^demo\b/);await connectSelected(page)
 const workspace=await argoPage(page,'Applications')
 const zone=workspace.getByRole('textbox',{name:'Zone',exact:true});await zone.fill('blue');await zone.press('Enter')
 await expect(workspace.locator('[data-resync]')).toBeEnabled()
 return workspace
}
