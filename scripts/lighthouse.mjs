import { chromium } from '@playwright/test';
import lighthouse from 'lighthouse';
import net from 'node:net';
import { mkdirSync, writeFileSync } from 'node:fs';
import { createSiteServer } from './server.mjs';
const out=process.env.OCULAR_SITE_SCRATCH || '/home/spk/.spk/sawe/ss/SPK-Ocular/.agents/tmp/site';
mkdirSync(out,{recursive:true});
const server=createSiteServer();await new Promise(r=>server.listen(0,'127.0.0.1',r));
const port=await new Promise(r=>{const s=net.createServer().listen(0,'127.0.0.1',()=>{const p=s.address().port;s.close(()=>r(p));});});
const browser=await chromium.launch({args:[`--remote-debugging-port=${port}`]});
try{
 const result=await lighthouse(`http://127.0.0.1:${server.address().port}/spk-ocular/`,{port,output:'json',onlyCategories:['performance','accessibility','best-practices','seo'],logLevel:'error'});
 writeFileSync(out+'/lighthouse.json',result.report);
 const summary={scores:Object.fromEntries(Object.entries(result.lhr.categories).map(([k,v])=>[k,v.score])),metrics:Object.fromEntries(['largest-contentful-paint','cumulative-layout-shift','total-blocking-time'].map(k=>[k,result.lhr.audits[k].numericValue]))};
 writeFileSync(out+'/lighthouse-summary.json',JSON.stringify(summary,null,2));console.log(JSON.stringify(summary,null,2));
}finally{await browser.close();server.closeAllConnections();await new Promise(r=>server.close(r));}
