import { chromium, expect } from '@playwright/test';
import { mkdirSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { createSiteServer } from './server.mjs';
import { languages, pagePath } from '../src/lib/languages.mjs';
import { API, REPO } from '../src/lib/releases.mjs';

const out=process.env.OCULAR_SITE_SCRATCH || path.resolve('.agents/tmp/site');
mkdirSync(out,{recursive:true});
const measurement=JSON.parse(readFileSync('src/data/measurements.json','utf8'));
const server=createSiteServer();await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
const root=`http://127.0.0.1:${server.address().port}`;
const browser=await chromium.launch();
async function externalLinks(page) {
 const invalid=await page.locator('a[href^="https://"],a[href^="http://"]').evaluateAll(links=>links.filter(a=>a.target!=='_blank'||!a.relList.contains('noopener')||!a.relList.contains('noreferrer')).map(a=>a.href));
 expect(invalid).toEqual([]);
 const privateDocs=await page.locator('a[href]').evaluateAll(links=>links.filter(a=>/github\.com\/Sipaha\/(?:internal-docs|spk-ocular\/(?:blob|raw)\/[^/]+\/docs\/)/i.test(a.href)).map(a=>a.href));
 expect(privateDocs).toEqual([]);
}
try {
 for(const {code} of languages)for(const theme of ['light','dark'])for(const width of [375,1440]) {
  const context=await browser.newContext({viewport:{width,height:1800},colorScheme:theme,reducedMotion:'reduce'});
  const page=await context.newPage();await page.route(API,route=>route.fulfill({status:404,json:{message:'fixture'}}));
  await page.goto(root+pagePath(code,'/spk-ocular/'),{waitUntil:'networkidle'});
  const section=page.locator('#measurements');
  const number=n=>new Intl.NumberFormat(code,{maximumFractionDigits:0}).format(n);
  await expect(section.locator('h2')).toContainText(number(measurement.resources));
  await expect(section.locator('h2')).toContainText(number(measurement.sampledPeakPSSMiB));
  await expect(section.locator('.measurement-note')).toContainText(measurement.version);
  await expect(section.locator('.measurement-note')).toContainText('Chromium');
  await expect(section.locator('.measurement-conditions')).not.toHaveAttribute('open');
  expect(await section.innerText()).not.toMatch(/Pod LIST|NOT ACCEPTED|10[.,]08/);
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  await externalLinks(page);
  await page.evaluate(()=>{const section=document.querySelector('#measurements');window.scrollTo({top:scrollY+section.getBoundingClientRect().top-document.querySelector('.header').getBoundingClientRect().height-28,behavior:'instant'});});
  await section.screenshot({path:path.join(out,`memory-${code}-${theme}-${width}.png`)});
  if(code==='ru'&&theme==='light'&&width===1440) {
   await section.locator('summary').click();
   await expect(section.locator('.measurement-conditions')).toHaveAttribute('open','');
   await expect(section.locator('time')).toHaveAttribute('datetime',measurement.dateISO);
   await expect(section.locator('time')).toBeVisible();
   await expect(section.locator('a')).toHaveCount(0);
   await section.screenshot({path:path.join(out,'memory-ru-expanded.png')});
  }
  await context.close();console.log(`PASS memory ${code} ${theme} ${width}`);
 }
 for(const {code} of languages) {
  const context=await browser.newContext({javaScriptEnabled:false});const page=await context.newPage();
  await page.goto(root+pagePath(code,'/spk-ocular/'));await externalLinks(page);
  await expect(page.locator('#measurements h2')).toContainText('558');await context.close();
 }
 const context=await browser.newContext({userAgent:'Mozilla/5.0 (X11; Linux x86_64)'});
 const page=await context.newPage();
 const filename='spk-ocular_1.7.1_linux_amd64.deb';
 const url=REPO+'/releases/download/v1.7.1/'+filename;
 await page.route(API,route=>route.fulfill({json:{tag_name:'v1.7.1',assets:[{name:filename,browser_download_url:url,size:1024},{name:filename+'.sha256',browser_download_url:url+'.sha256',size:100}]}}));
 await page.goto(root+'/spk-ocular/en/',{waitUntil:'networkidle'});
 await expect(page.locator('.package a').first()).toHaveAttribute('target','_blank');
 await expect(page.locator('.package a').last()).toHaveAttribute('href',url+'.sha256');
 await externalLinks(page);await context.close();
 console.log('PASS generated package, checksum and primary-download new-tab contracts');
 console.log('PASS 32 measurement layouts, 8 no-JS outbound contracts and no internal documentation links');
} finally {await browser.close();await new Promise(resolve=>server.close(resolve));}
