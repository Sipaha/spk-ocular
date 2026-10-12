import {beforeEach,describe,expect,it} from 'vitest'
import type {SecurityFinding} from '../api/types'
import {setLanguage} from '../i18n'
import {filterFindings,findingTitle,recommendation} from './model'
const finding:SecurityFinding={id:'p/root',ref:{provider:'kubernetes',target:'demo',kind:'pods',scope:'blue',name:'api',uid:'uid'},kindTitle:'Pod',category:'configuration',severity:'high',rule:'root',container:'main',evidence:'runAsUser'}
beforeEach(()=>setLanguage('en'))
describe('security filters and evidence presentation',()=>{
 it('combines terms, source and severity and searches localized rule names',()=>{
  expect(filterFindings([finding],'blue api non-root','configuration','high')).toEqual([finding])
  expect(filterFindings([finding],'api','cve','')).toEqual([])
  expect(filterFindings([finding],'api','configuration','critical')).toEqual([])
  setLanguage('ru');expect(filterFindings([finding],'запуск root','','')).toEqual([finding])
 })
 it('preserves external CVE text and does not apply configuration advice to scanner IDs',()=>{
  const cve={...finding,category:'cve' as const,rule:'root',title:'External title',package:'lib',image:'app:v1'}
  expect(findingTitle(cve)).toBe('External title');expect(recommendation(cve)).toBe('')
  expect(filterFindings([cve],'app:v1 lib','','')).toEqual([cve])
  expect(recommendation(finding)).toContain('runAsNonRoot: true')
 })
})
