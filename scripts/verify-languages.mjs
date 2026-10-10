import { chromium, expect } from '@playwright/test';
import { readFileSync, mkdirSync } from 'node:fs';
import { createRequire } from 'node:module';
import path from 'node:path';
import { createSiteServer } from './server.mjs';
import { languages, pagePath } from '../src/lib/languages.mjs';
import { API, REPO } from '../src/lib/releases.mjs';
const out=process.env.OCULAR_SITE_SCRATCH || '/home/spk/.spk/sawe/ss/SPK-Ocular/.agents/tmp/site';
mkdirSync(out,{recursive:true});
process.env.TMPDIR=path.join(out,'tmp');mkdirSync(process.env.TMPDIR,{recursive:true});
const axe=readFileSync(createRequire(import.meta.url).resolve('axe-core/axe.min.js'),'utf8');
const server=createSiteServer();await new Promise(r=>server.listen(0,'127.0.0.1',r));
const origin=`http://127.0.0.1:${server.address().port}`,base='/spk-ocular/';
const assets=[];
for(const [os,format] of [['linux','deb'],['windows','msi'],['darwin','dmg']]) {
 const name=`spk-ocular_0.1.0_${os}_amd64.${format}`;
 assets.push({name,browser_download_url:`${REPO}/releases/download/v0.1.0/${name}`,size:1024});
}
const dictionaries=Object.fromEntries(languages.filter(l=>!['ru','en'].includes(l.code)).map(l=>[l.code,JSON.parse(readFileSync(`src/i18n/locales/${l.code}.json`))]));
let browser;
try {
 browser=await chromium.launch();
 for(const {code:lang} of languages.filter(l=>!['ru','en'].includes(l.code))) for(const theme of ['light','dark']) for(const width of [320,375,768,1024,1440]) {
  const context=await browser.newContext({viewport:{width,height:1000},colorScheme:theme,reducedMotion:'reduce',userAgent:'Mozilla/5.0 (X11; Linux x86_64)'});
  const page=await context.newPage();const errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  page.on('response',r=>{if(r.url().startsWith(origin)&&r.status()>=400)errors.push(`${r.status()} ${r.url()}`)});
  await page.route(API,r=>r.fulfill({json:{tag_name:'v0.1.0',assets}}));
  await page.goto(origin+pagePath(lang,base),{waitUntil:'networkidle'});
  await expect(page.locator('html')).toHaveAttribute('lang',lang);
  const about='https://sipaha.github.io/about/';
  await expect(page.locator('.author-link')).toHaveAttribute('href',about);
  await expect(page.locator('.support-link')).toHaveAttribute('href',about+'#support');
  await expect(page.locator('.support-link')).toBeVisible();
  await expect(page.locator('html')).toHaveAttribute('data-theme',theme);
  await expect(page.locator('[rel=canonical]')).toHaveAttribute('href','https://sipaha.github.io'+pagePath(lang,base));
  await expect(page.locator('link[hreflang]')).toHaveCount(9);
  const d=dictionaries[lang];
  await expect(page.locator(`.${width<=960?'mobile-nav':'desktop-nav'} .header-support`)).toBeVisible();
  await expect(page.locator(`.${width<=960?'mobile-nav':'desktop-nav'} .header-support`)).toHaveAttribute('href',about+'#support');
  await expect(page.locator('.hero h1')).toHaveText(d.hero.join(''));
  for(const button of await page.locator('[data-download-label]').all()) {
   await expect(button).toHaveAttribute('aria-label',d.downloadFormatFor.replace('{format}','DEB').replace('{os}','Linux'));
   await expect(button).toHaveAttribute('href',`${REPO}/releases/download/v0.1.0/spk-ocular_0.1.0_linux_amd64.deb`);
  }
  await expect(page.locator('.screenshot-note')).toHaveText(d.screenshotNote);
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  expect(await page.locator('.header-inner').evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true);
  await page.locator('main img').evaluateAll(els=>els.forEach(el=>el.loading='eager'));
  await expect.poll(()=>page.locator('main img').evaluateAll(els=>els.every(el=>el.complete&&el.naturalWidth>0))).toBe(true);
  await page.locator('.hero-visual [data-image-preview]').click();
  await expect(page.locator('.image-preview')).toBeVisible();
  await expect(page.locator('.image-preview button')).toHaveAttribute('aria-label',d.closeImage);
  await page.locator('.image-preview img').evaluate(el=>el.decode());
  await page.addScriptTag({content:axe});
  const result=await page.evaluate(()=>window.axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));
  expect(result.violations.map(v=>({id:v.id,nodes:v.nodes.map(n=>n.target)}))).toEqual([]);
  await page.screenshot({path:path.join(out,`i18n-${lang}-${theme}-${width}-preview.png`)});
  await page.keyboard.press('Escape');
  await expect(page.locator('.hero-visual [data-image-preview]')).toBeFocused();
  await page.screenshot({path:path.join(out,`i18n-${lang}-${theme}-${width}.png`),fullPage:true});
  await page.locator('.language').click();
  await expect(page.locator('.language-options')).toBeVisible();
  expect(await page.locator('.language-options').evaluate(el=>{const r=el.getBoundingClientRect();return r.left>=0&&r.right<=innerWidth})).toBe(true);
  await page.screenshot({path:path.join(out,`i18n-${lang}-${theme}-${width}-menu.png`)});
  await page.keyboard.press('Escape');await expect(page.locator('.language-options')).toBeHidden();
  await page.locator(`.${width<=960?'mobile-nav':'desktop-nav'} a[href="#features"]`).click();
  expect(await page.evaluate(()=>{const el=document.getElementById('features');return Math.abs(el.getBoundingClientRect().top+parseFloat(getComputedStyle(el).paddingTop)-document.querySelector('.header').getBoundingClientRect().bottom-28)})).toBeLessThanOrEqual(2);
  await page.locator('select[name=os]').selectOption('windows');
  await expect(page.locator('.hero [data-download-label]')).toHaveAttribute('aria-label',d.downloadFormatFor.replace('{format}','MSI').replace('{os}','Windows'));
  await page.locator('.theme-toggle').click();await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-theme',theme==='light'?'dark':'light');
  expect(errors).toEqual([]);await context.close();console.log(`PASS language ${lang}/${theme}/${width}: copy, download format, layout, anchors, menu, theme, axe`);
 }
 for(const {code} of languages) {
  const context=await browser.newContext({viewport:{width:320,height:1000},userAgent:'Mozilla/5.0 (X11; Linux x86_64)'});
  const page=await context.newPage();const name='spk-ocular_0.1.0_linux_amd64.tar.gz';
  await page.route(API,r=>r.fulfill({json:{tag_name:'v0.1.0',assets:[{name,browser_download_url:`${REPO}/releases/download/v0.1.0/${name}`,size:1024}]}}));
  await page.goto(origin+pagePath(code,base),{waitUntil:'networkidle'});
  await expect(page.locator('.compact-download-text')).toContainText('TAR.GZ');
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  expect(await page.locator('.header-inner').evaluate(el=>el.scrollWidth<=el.clientWidth)).toBe(true);
  await page.screenshot({path:path.join(out,`i18n-${code}-320-targz.png`)});
  await context.close();
 }
 console.log('PASS all eight locales: 320px long TAR.GZ format fallback');
 for(const {code} of languages) {
  const context=await browser.newContext({locale:code==='zh'?'zh-CN':code,reducedMotion:'reduce'});
  await context.addInitScript(()=>Object.defineProperty(navigator,'webdriver',{get:()=>false}));
  const page=await context.newPage();await page.route(API,r=>r.fulfill({status:404,json:{}}));
  await page.goto(origin+base+'?ref=test#free');
  await expect(page).toHaveURL(origin+pagePath(code,base)+'?ref=test#free');
  await page.locator('.language').click();await page.locator('[data-language=de]').click();
  await expect(page).toHaveURL(origin+pagePath('de',base)+'?ref=test#free');
  await page.goto(origin+base);await expect(page).toHaveURL(origin+pagePath('de',base));
  await page.goto(origin+pagePath('ja',base));await expect(page.locator('html')).toHaveAttribute('lang','ja');
  await context.close();
 }
 for(const javaScriptEnabled of [true,false]) {
  const context=await browser.newContext({javaScriptEnabled,locale:'fr-FR',viewport:{width:375,height:1000}});
  if(javaScriptEnabled) await context.addInitScript(()=>{Object.defineProperty(navigator,'webdriver',{get:()=>false});Object.defineProperty(window,'localStorage',{get:()=>{throw Error('Disabled')}})});
  const page=await context.newPage();await page.route(API,r=>r.abort());
  await page.goto(origin+base);await expect(page.locator('html')).toHaveAttribute('lang',javaScriptEnabled?'fr':'ru');
  await page.locator('.language').click();await page.locator('[data-language=pt]').click();
  await expect(page.locator('html')).toHaveAttribute('lang','pt');
  await expect(page.locator('.download-fallback')).toBeVisible();
  await page.locator('.language').click();await page.locator('[data-language=ru]').click();
  await expect(page.locator('html')).toHaveAttribute('lang','ru');await page.reload();
  await expect(page.locator('html')).toHaveAttribute('lang','ru');await context.close();
 }
 const context=await browser.newContext({locale:'fa-IR'});await context.addInitScript(()=>Object.defineProperty(navigator,'webdriver',{get:()=>false}));
 const page=await context.newPage();await page.route(API,r=>r.abort());await page.goto(origin+base);await expect(page.locator('html')).toHaveAttribute('lang','en');await context.close();
 console.log('PASS language detection, ordered fallback, saved choice, explicit URLs, hash/query, storage denial and no-JS language navigation');
} finally {await browser?.close();server.closeAllConnections();await new Promise(r=>server.close(r));}
