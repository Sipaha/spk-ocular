import {it,expect} from 'vitest'
import {kubernetesKindSources} from '../test/providerSource.mjs'
import {concepts,concept,documentation} from './catalog'
import {learningContent} from './content'
import {languages} from '../languages'
it('covers every described and placed built-in API kind in the provider inventory',()=>{
 const source=kubernetesKindSources['catalog.go']
 const placed=source.slice(source.indexOf('var placedKinds ='),source.indexOf('// navOrder'))
 const expected=[...placed.matchAll(/\{schema.GroupResource\{(?:Group: "([^"]+)", )?Resource: "([^"]+)"\}/g)].map(m=>(m[1]?m[1]+'/':'')+m[2])
 for(const file of ['kind_pods.go','kind_workloads.go','kind_cluster.go','kind_network.go']){
  const text=kubernetesKindSources[file]
  for(const m of text.matchAll(/ID: "([^"]+)", Title: "[^"]+", Group:/g))expected.push(m[1])
 }
 expect([...concepts.map(c=>c[0])].sort()).toEqual([...new Set(expected)].sort())
})
it('ships every purpose, warning and UI string in eight locales without placeholders or fallback copies',()=>{
 const en=learningContent.en
 for(const lang of languages){
  const local=learningContent[lang]
  expect(Object.keys(local.purposes).sort()).toEqual(concepts.map(c=>c[0]).sort())
  for(const group of ['ui','cautions','purposes'] as const){
   expect(Object.keys(local[group]).sort()).toEqual(Object.keys(en[group]).sort())
   for(const [key,text] of Object.entries(local[group])){expect(text.trim().length,lang+':'+key).toBeGreaterThan(1);if(lang!=='en'&&group!=='ui')expect(text,lang+':'+key).not.toBe(en[group][key as keyof typeof en[typeof group]])}
  }
 }
})
it('all related concepts resolve and documentation links stay on the official HTTPS origin',()=>{
 expect(new Set(concepts.map(c=>c[0])).size).toBe(concepts.length)
 for(const c of concepts){for(const id of c[4].split(','))expect(concept(id),id).toBeDefined();const url=new URL(documentation(c[0]));expect(url.origin).toBe('https://kubernetes.io');expect(url.pathname.startsWith('/docs/')).toBe(true);expect(c[3].split(',').length).toBeGreaterThan(0)}
 expect(concept('example.com/unknown')).toBeUndefined()
})
