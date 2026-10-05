// Render social cards from the built page's real text, font and product image.
import { chromium } from '@playwright/test';
import { createSiteServer } from './server.mjs';
const server=createSiteServer();
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));
let browser;
try {
 browser=await chromium.launch();
 for(const lang of ['ru','en']) {
  const page=await browser.newPage({viewport:{width:1200,height:630},colorScheme:'dark',reducedMotion:'reduce'});
  await page.route('https://api.github.com/**',route=>route.abort());
  await page.goto(`http://127.0.0.1:${server.address().port}/spk-ocular/${lang==='en'?'en/':''}`,{waitUntil:'networkidle'});
  await page.evaluate(()=>{
   const brand=document.querySelector('.brand').cloneNode(true);
   const heading=document.querySelector('h1').cloneNode(true);
   const promise=document.querySelector('.hero-meta>a').cloneNode(true);
   const image=document.querySelector('.hero-visual img').cloneNode(true);
   const card=document.createElement('main');card.className='social-card';
   const text=document.createElement('div');text.className='social-copy';text.append(brand,heading,promise);
   card.append(text,image);document.body.replaceChildren(card);
  });
  await page.addStyleTag({content:`body{width:1200px;height:630px;overflow:hidden;background:#10161f}.social-card{display:grid;grid-template-columns:560px 1fr;gap:35px;padding:60px;height:630px;overflow:hidden}.social-copy{display:flex;flex-direction:column;justify-content:space-between;padding-block:12px 35px}.social-copy .brand{font-size:25px}.social-copy h1{font-size:51px;line-height:1.12;letter-spacing:-.05em}.social-copy>a:last-child{font-size:18px;max-width:410px;line-height:1.6}.social-copy>a:last-child span{display:none}.social-card>img{height:510px;width:816px;max-width:none;object-fit:cover;object-position:left top;border:1px solid #303c4c;border-radius:6px}`});
  await page.evaluate(async()=>{await document.fonts.ready;await Promise.all([...document.images].map(image=>image.decode()));});
  await page.screenshot({path:`public/media/og-${lang}.png`});await page.close();
  console.log(`Rendered social card: ${lang}`);
 }
}finally{await browser?.close();server.closeAllConnections();await new Promise(resolve=>server.close(resolve));}
