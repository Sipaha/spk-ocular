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

it('preserves external audit rule semantics and filters results without promoting unknown to pass',()=>{
 const audit:SecurityFinding={...finding,category:'audit',title:'Producer title',audit:{result:'unknown',nativeSeverity:'OTHER',category:'Reliability',description:'External explanation',messages:['plain <script> text'],remediation:'Review policy',scopeType:'JSONPath',scopeValue:'.spec.field',scannerName:'Scanner',scannerVersion:'',scannerVendor:'',apiVersion:'aquasecurity.github.io/v1alpha1',resourceVersion:'9',ownerUid:'old'}}
 expect(findingTitle(audit)).toBe('Producer title');expect(recommendation(audit)).toBe('')
 expect(filterFindings([audit,finding],'Reliability .spec.field policy','audit','','unknown')).toEqual([audit])
 expect(filterFindings([audit],'','','','pass')).toEqual([])
 expect(filterFindings([finding],'','','','unknown')).toEqual([])
 expect(filterFindings([audit],'plain <script>','','')).toEqual([audit])
})
