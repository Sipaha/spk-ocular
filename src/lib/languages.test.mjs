import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import ts from 'typescript';
import { languages, pagePath, languageTarget } from './languages.mjs';
const codes = languages.map(l=>l.code);
const input = { codes, base:'/spk-ocular/',path:'/spk-ocular/',search:'?ref=demo',hash:'#downloads',stored:null,languages:['en-US'],userAgent:'Browser',webdriver:false };
const source = (await readFile(new URL('../i18n/copy.ts',import.meta.url),'utf8')).split('export type Copy')[0].replace(/^import .*;\n/gm,'');
const compiled = ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.ESNext}}).outputText;
const {en} = await import('data:text/javascript;base64,'+Buffer.from(compiled).toString('base64'));
const leaves = (value,path='') => typeof value==='string'?{[path]:value}:Object.assign({},...Object.entries(value).map(([key,item])=>leaves(item,path?`${path}.${key}`:key)));
test('all languages retain complete copy, technical versions and download placeholders',async()=>{
 const reference=leaves(en);
 for(const {code} of languages.filter(l=>!['ru','en'].includes(l.code))) {
  const translated=JSON.parse(await readFile(new URL(`../i18n/locales/${code}.json`,import.meta.url),'utf8'));
  const flat=leaves(translated);
  assert.deepEqual(Object.keys(flat).sort(),Object.keys(reference).sort(),code);
  for(const key of Object.keys(reference)) {
   assert(flat[key].trim(),`${code}.${key} is empty`);
   assert(!/TODO|undefined|\[object Object\]/.test(flat[key]));
   const tokens=text=>[...text.matchAll(/\{[a-z]+\}/g)].map(m=>m[0]).sort();
   assert.deepEqual(tokens(flat[key]),tokens(reference[key]),`${code}.${key} placeholders`);
   const versions=text=>[...text.matchAll(/\d+(?:\.\d+)*/g)].map(m=>m[0]).sort();
   assert.deepEqual(versions(flat[key]),versions(reference[key]),`${code}.${key} technical numbers`);
   if(key.endsWith('.image')||key==='license') assert.equal(flat[key],reference[key]);
  }
 }
});
test('saved choice, ordered preferences, unsupported locales and region variants resolve consistently',()=>{
 for(const code of codes) assert.equal(languageTarget({...input,languages:[code+'-XX']}),code==='ru'?null:`/spk-ocular/${code}/?ref=demo#downloads`);
 assert.equal(languageTarget({...input,stored:'ru',languages:['zh-CN']}),null);
 assert.equal(languageTarget({...input,stored:'pt',languages:['ru-RU']}),'/spk-ocular/pt/?ref=demo#downloads');
 assert.equal(languageTarget({...input,stored:'bad',languages:['fa-IR','de-DE']}),'/spk-ocular/de/?ref=demo#downloads');
 assert.equal(languageTarget({...input,languages:['fa-IR']}),'/spk-ocular/en/?ref=demo#downloads');
 assert.equal(languageTarget({...input,languages:['zh-Hant-TW','en']}),'/spk-ocular/en/?ref=demo#downloads');
});
test('explicit routes and crawlers do not lose their requested language',()=>{
 for(const code of codes.filter(c=>c!=='ru')) assert.equal(languageTarget({...input,path:pagePath(code,input.base),stored:'ru'}),null);
 assert.equal(languageTarget({...input,userAgent:'Googlebot'}),null);
 assert.equal(languageTarget({...input,webdriver:true}),null);
});

test('explicit Russian query overrides browser detection when storage is unavailable',()=>{
 assert.equal(languageTarget({...input,stored:null,languages:['zh-CN'],search:'?ref=test&lang=ru'}),null);
});
