import { mkdirSync,readFileSync,writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { createSiteServer } from './server.mjs';
import path from 'node:path';
import { chromium, expect } from '@playwright/test';
import { API, REPO, RELEASES } from '../src/lib/releases.mjs';
const out=process.env.OCULAR_SITE_SCRATCH || '/home/spk/.spk/sawe/ss/SPK-Ocular/.agents/tmp/site';
mkdirSync(out,{recursive:true});
const axe=readFileSync(createRequire(import.meta.url).resolve('axe-core/axe.min.js'),'utf8');
const server=createSiteServer();
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
const port=server.address().port;
const root=`http://127.0.0.1:${port}/spk-ocular/`;
let browser;
const failures=[];
async function expectAnchorPosition(page, id) {
 // Wait for native smooth scrolling to finish before measuring its final position.
 await expect.poll(() => page.evaluate(id => {
  const section=document.getElementById(id);
  const contentTop=section.getBoundingClientRect().top+parseFloat(getComputedStyle(section).paddingTop);
  const gap=contentTop-document.querySelector('.header').getBoundingClientRect().bottom;
  return Math.abs(gap-28);
 }, id), {message: `${id}: section content should settle 28px below the sticky header`}).toBeLessThanOrEqual(2);
}

try{
 await expect.poll(async()=>{try{return(await fetch(root)).ok}catch{return false}},{timeout:20000}).toBe(true);
 browser=await chromium.launch();
 for(const lang of ['ru','en'])for(const theme of ['light','dark'])for(const width of [375,768,1440]){
  const context=await browser.newContext({viewport:{width,height:1000},colorScheme:theme,locale:lang==='ru'?'ru-RU':'en-US',reducedMotion:'reduce'});
  const page=await context.newPage();const errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  page.on('response',r=>{if(r.url().startsWith(root)&&r.status()>=400)errors.push(`${r.status()} ${r.url()}`)});
  await page.route(API,r=>r.fulfill({status:404,json:{message:'Not Found'}}));
  await page.goto(root+(lang==='en'?'en/':''),{waitUntil:'networkidle'});
  await expect(page.locator('html')).toHaveAttribute('data-theme',theme);
  await expect(page.locator('html')).toHaveAttribute('lang',lang);
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  expect(await page.locator('h1').evaluate(el=>el.clientHeight/parseFloat(getComputedStyle(el).lineHeight))).toBeLessThanOrEqual(width>=1024?2.1:3.1);
  await page.locator('img').evaluateAll(els=>els.forEach(el=>el.loading='eager'));
  await expect.poll(()=>page.locator('img').evaluateAll(els=>els.every(el=>el.complete&&el.naturalWidth>0))).toBe(true);
  expect(await page.locator('body').textContent()).not.toMatch(/[—–]/);
  await page.addScriptTag({content:axe});
  const accessibility=await page.evaluate(async()=>await window.axe.run(document,{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21aa']}}));
  if(accessibility.violations.length)failures.push({lang,theme,width,violations:accessibility.violations.map(v=>({id:v.id,nodes:v.nodes.map(n=>({html:n.html,summary:n.failureSummary}))}))});
  await expect(page.locator('#free')).toContainText(lang==='ru'?'Без ограничений по обороту':'No limits based on revenue');
  await expect(page.locator('#support')).toContainText(lang==='ru'?'добровольными':'voluntary');
  await expect(page.locator('#helm')).toContainText('Helm');
  await expect(page.locator('h1')).toContainText(lang==='ru'?'Kubernetes и Docker.':'Kubernetes & Docker.');
  await expect(page).toHaveTitle(/Kubernetes.*Docker/);
  await expect(page.locator('h1')).not.toContainText('Compose');
  await expect(page.locator('body')).toContainText(lang==='ru'?'Отдельные контейнеры':'standalone containers');
  await expect(page.locator('.header-actions a[href="#downloads"]')).toBeVisible();
  await expect(page.locator(width<=600?'.compact-download-text':'.header-actions [data-download-text]')).toBeVisible();
  const navigation=page.locator(width<=960?'.mobile-nav':'.desktop-nav');
  await expect(navigation).toBeVisible();
  await expect(navigation.getByRole('link',{name:'GitHub',exact:true})).toBeVisible();
  await expect(navigation.getByRole('link',{name:'GitHub',exact:true})).toHaveAttribute('href',REPO);
  for (const id of ['features','interface','free']) {
   await navigation.locator(`a[href="#${id}"]`).click();
   await expect(page).toHaveURL(new RegExp(`#${id}$`));
   await expectAnchorPosition(page,id);
   await expect(page.locator(`#${id} h2`).first()).toBeInViewport();
  }
  await page.locator('.header-actions a[href="#downloads"]').click();
  await expect(page.locator('#downloads h2')).toBeInViewport();
  await expectAnchorPosition(page,'downloads');
  await page.screenshot({path:path.join(out,`${lang}-${theme}-${width}-downloads-anchor.png`)});
  expect(await page.locator('a[href^="#"]').evaluateAll(links=>links.filter(link=>!document.getElementById(link.hash.slice(1))).map(link=>link.hash))).toEqual([]);
  expect(errors).toEqual([]);
  for (const image of await page.locator('main img:visible').all()) { await image.scrollIntoViewIfNeeded(); await image.evaluate(el=>el.decode()); }
  await page.evaluate(()=>window.scrollTo(0,0));
  await page.screenshot({path:path.join(out,`${lang}-${theme}-${width}.png`),fullPage:true});
  if(width===375)await page.screenshot({path:path.join(out,`${lang}-${theme}-mobile.png`)});
  if(width===1440)await page.screenshot({path:path.join(out,`${lang}-${theme}-hero.png`)});
  const tabs=page.getByRole('tab');await tabs.nth(0).focus();await page.keyboard.press('ArrowRight');
  await expect(tabs.nth(1)).toHaveAttribute('aria-selected','true');await expect(page.locator('#shot-1')).toBeVisible();await expect(page.locator('#shot-0')).toBeHidden();
  await page.keyboard.press('End');await expect(tabs.nth(3)).toHaveAttribute('aria-selected','true');
  await page.locator('.theme-toggle').click();await expect(page.locator('html')).toHaveAttribute('data-theme',theme==='dark'?'light':'dark');
  await page.reload();await expect(page.locator('html')).toHaveAttribute('data-theme',theme==='dark'?'light':'dark');
  await context.close();console.log(`PASS ${lang} ${theme} ${width}: layout, images, theme, keyboard tabs, axe`);
 }
 for (const width of [375,768,1440]) {
  const page=await browser.newPage({viewport:{width,height:1000},reducedMotion:'no-preference'});
  await page.route(API,r=>r.fulfill({status:404,json:{message:'Not Found'}}));
  await page.goto(root,{waitUntil:'networkidle'});
  const navigation=page.locator(width<=960?'.mobile-nav':'.desktop-nav');
  for (const id of ['features','interface','free']) {
   await navigation.locator(`a[href="#${id}"]`).click();
   await expectAnchorPosition(page,id);
  }
  await page.locator('.header-actions a[href="#downloads"]').click();
  await expectAnchorPosition(page,'downloads');
  await page.goto(root+'#downloads',{waitUntil:'networkidle'});
  await expectAnchorPosition(page,'downloads');
  await page.close();console.log(`PASS ${width}: smooth navigation and direct fragment link`);
 }
 for(const javaScriptEnabled of [false,true]){
  const page=await browser.newPage({javaScriptEnabled});
  await page.route(API,r=>r.abort());
  await page.goto(root,{waitUntil:'networkidle'});
  await expect(page.locator(`a[href="${RELEASES}"]`)).toBeVisible();
  if(!javaScriptEnabled){
   for(const id of [0,1,2,3])await expect(page.locator('#shot-'+id)).toBeVisible();
   await expect(page.locator('#free')).toContainText('Бесплатно');
   await page.locator('.header-actions a[href="#downloads"]').click();
   await expect(page.locator('#downloads h2')).toBeInViewport();
   await expectAnchorPosition(page,'downloads');
  }
  else await expect(page.locator('[data-release-status]')).toContainText('Не удалось');
  await page.close();console.log(`PASS no-JS / API-offline ${javaScriptEnabled}`);
 }
 const assets=[];
 for(const os of ['linux','windows','darwin'])for(const arch of ['amd64','arm64']){
  const formats=os==='linux'?['deb','rpm','tar.gz']:os==='windows'?['msi','zip']:['dmg','tar.gz'];
  for(const prefix of ['spk-ocular','spk-ocular-browser'])for(const ext of prefix.endsWith('browser')?[os==='windows'?'zip':'tar.gz']:formats){
   const name=`${prefix}_0.1.0_${os}_${arch}.${ext}`;
   for(const suffix of ['', '.sha256']) assets.push({name:name+suffix,browser_download_url:`${REPO}/releases/download/v0.1.0/${name}${suffix}`,size:1048576});
  }
 }
 for (const [ua,expectedOS,name] of [
  ['Mozilla/5.0 (Windows NT 10.0; Win64; x64)','windows','Windows'],
  ['Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)','darwin','macOS'],
  ['Mozilla/5.0 (X11; Linux x86_64)','linux','Linux'],
  ['Mozilla/5.0 (Linux; Android 14; Mobile)','',''],
 ]) for (const lang of ['ru','en']) {
  const page=await browser.newPage({userAgent:ua,viewport:{width:320,height:1000},reducedMotion:'reduce'});
  await page.route(API,r=>r.fulfill({json:{tag_name:'v0.1.0',assets}}));
  await page.goto(root+(lang==='en'?'en/':''),{waitUntil:'networkidle'});
  await expect(page.locator('select[name=os]')).toHaveValue(expectedOS);
  await expect(page.locator('.compact-download-text')).toBeVisible();
  await expect(page.locator('.language')).toBeVisible();
  await expect(page.locator('select[name=arch]')).toHaveValue('');
  const buttons=page.locator('[data-download-text]');
  for (const label of await buttons.allTextContents()) expect(label).toBe(name?`${lang==='ru'?'Скачать для':'Download for'} ${name}`:lang==='ru'?'Скачать':'Download');
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
  if (expectedOS) for(const href of await page.locator('.package a').evaluateAll(els=>els.map(el=>el.href)))expect(href).toContain(`_${expectedOS}_`);
  else await expect(page.locator('.package')).toHaveCount(20);
  await page.locator('select[name=os]').selectOption('darwin');
  await expect(buttons.first()).toHaveText(lang==='ru'?'Скачать для macOS':'Download for macOS');
  await page.locator('.header-actions a[href="#downloads"]').click();
  await expect(page.locator('select[name=os]')).toHaveValue('darwin');
  await expectAnchorPosition(page,'downloads');
  await page.close();console.log(`PASS ${lang} ${name||'mobile'}: automatic OS, button label, manual override and 320px layout`);
 }
 const page=await browser.newPage();await page.route(API,r=>r.fulfill({json:{tag_name:'v0.1.0',assets:[...assets].reverse()}}));
 await page.goto(root+'en/',{waitUntil:'networkidle'});
 await expect(page.locator('.package').first()).toContainText('Desktop app');
 await page.locator('select[name=os]').selectOption('');await expect(page.locator('.package')).toHaveCount(20);
 await page.locator('select[name=os]').selectOption('windows');await page.locator('select[name=arch]').selectOption('arm64');await expect(page.locator('.package')).toHaveCount(3);
 for(const href of await page.locator('.package a').evaluateAll(els=>els.map(el=>el.href)))expect(href).toContain('_windows_arm64.');
 await page.close();console.log('PASS all 20 assets, six platforms, architecture filters and checksums');
 writeFileSync(path.join(out,'accessibility.json'),JSON.stringify(failures,null,2));
 expect(failures).toEqual([]);
}finally{
 await browser?.close();
 server.closeAllConnections();
 await new Promise(resolve=>server.close(resolve));
}
