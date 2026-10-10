import {describe,it,expect} from 'vitest'
import type {AccessAttributes,RBACGrant,RBACRule} from '../api/types'
import {ruleMatches,matchingGrants} from './model'
const rule=(over:Partial<RBACRule>={}):RBACRule=>({verbs:['get'],apiGroups:[''],resources:['pods'],resourceNames:[],nonResourceURLs:[],...over})
const grant=(over:Partial<RBACGrant>={}):RBACGrant=>({binding:{provider:'kubernetes',target:'t',kind:'rolebindings',name:'reader',scope:'blue'},bindingKind:'RoleBinding',role:{provider:'kubernetes',target:'t',kind:'roles',name:'reader',scope:'blue'},roleKind:'Role',subjectKind:'Group',subjectName:'dev',namespace:'blue',clusterWide:false,rules:[rule()],aggregated:false,...over})
const attrs=(over:Partial<AccessAttributes>={}):AccessAttributes=>({verb:'get',group:'',resource:'pods',subresource:'',namespace:'blue',name:'',...over})
describe('observed RBAC grant matching',()=>{
 it('keeps namespace bindings restricted even when they reference ClusterRoles',()=>{
  expect(ruleMatches(grant({roleKind:'ClusterRole'}),rule(),attrs())).toBe(true)
  expect(ruleMatches(grant({roleKind:'ClusterRole'}),rule(),attrs({namespace:'green'}))).toBe(false)
  expect(ruleMatches(grant(),rule(),attrs({namespace:''}))).toBe(false)
  expect(ruleMatches(grant({clusterWide:true}),rule(),attrs({namespace:''}))).toBe(true)
 })
 it('matches wildcard verbs/groups/resources and exact subresources, without broadening pods/*',()=>{
  expect(ruleMatches(grant(),rule({verbs:['*'],apiGroups:['*'],resources:['*']}),attrs({verb:'patch',group:'apps',resource:'deployments'}))).toBe(true)
  expect(ruleMatches(grant(),rule(),attrs({subresource:'log'}))).toBe(false)
  expect(ruleMatches(grant(),rule({resources:['pods/log']}),attrs({subresource:'log'}))).toBe(true)
  expect(ruleMatches(grant(),rule({resources:['*/scale']}),attrs({resource:'deployments',subresource:'scale'}))).toBe(true)
  expect(ruleMatches(grant(),rule({resources:['pods/*']}),attrs({subresource:'log'}))).toBe(false)
 })
 it('does not treat resourceNames as wildcards or permit top-level named create/deletecollection',()=>{
  const named=rule({verbs:['*'],resourceNames:['web']})
  expect(ruleMatches(grant(),named,attrs({name:'web'}))).toBe(true)
  expect(ruleMatches(grant(),named,attrs())).toBe(false)
  expect(ruleMatches(grant(),named,attrs({name:'web',verb:'create'}))).toBe(false)
  expect(ruleMatches(grant(),named,attrs({name:'web',verb:'deletecollection'}))).toBe(false)
  expect(ruleMatches(grant(),rule({resourceNames:['*']}),attrs({name:'web'}))).toBe(false)
  expect(ruleMatches(grant(),rule({verbs:['create'],resources:['pods/exec'],resourceNames:['web']}),attrs({verb:'create',subresource:'exec',name:'web'}))).toBe(true)
 })
 it('allows named list/watch declarations only with an explicit name and reports grants as evidence',()=>{
  const g=grant({rules:[rule({verbs:['list','watch'],resourceNames:['web']})],error:'limit'})
  expect(matchingGrants([g],attrs({verb:'list'}))).toEqual([])
  expect(matchingGrants([g],attrs({verb:'list',name:'web'}))).toEqual([g])
  expect(matchingGrants([grant({rules:[],error:'forbidden'})],attrs())).toEqual([])
 })
 it('never interprets non-resource URL rules as resource permissions',()=>{
  const nonResource=rule({resources:[],apiGroups:[],nonResourceURLs:['*']})
  expect(ruleMatches(grant({clusterWide:true}),nonResource,attrs())).toBe(false)
 })
})
