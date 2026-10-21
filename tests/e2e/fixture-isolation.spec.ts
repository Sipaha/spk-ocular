import {expect} from '@playwright/test'
import {test} from './fixtures'
import {token} from './synth'

// These two cases intentionally share the server's persisted language. The
// common fixture must isolate the second case even if a preceding locale test
// exits before restoring its manual choice.
test('a locale scenario can leave a persisted manual choice',async({page})=>{
 await page.goto('/')
 const response=await page.request.post('/api/SetLanguage',{headers:{Authorization:'Bearer '+await token(page),Origin:new URL(page.url()).origin},data:{language:'pt'}})
 expect(response.ok()).toBeTruthy()
 await page.reload();await expect(page.locator('html')).toHaveAttribute('lang','pt')
})
test('a new case starts with its browser locale after a preceding manual choice',async({page})=>{
 await Promise.all([page.waitForResponse(response=>response.url().endsWith('/api/ListTargets')&&response.ok()),page.goto('/')])
 await expect(page.locator('html')).toHaveAttribute('lang','en')
})
