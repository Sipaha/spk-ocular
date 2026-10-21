import {describe,it,expect} from 'vitest'
import type {FluxObject,FluxTracking} from './types'
import {contains,inventoryRef,loadedLink,managerMatches,revisionURL} from './model'
import type {KindDescriptor} from '../api/types'
const ref={provider:'kubernetes',target:'a',kind:'kustomize.toolkit.fluxcd.io/kustomizations',scope:'ops',name:'app',uid:'manager-uid'}
const object={ref,kind:'Kustomization',section:'Workloads',state:'Ready',message:'',generation:2,observedGeneration:2,conditions:[],url:'',path:'',revision:'',links:[],inventory:[{group:'apps',kind:'Deployment',version:'v1',namespace:'blue',name:'web'}],localDestination:true,createdAt:0,truncated:false} satisfies FluxObject
const tracking:FluxTracking={group:'apps',kind:'Deployment',candidates:[{kind:'Kustomization',namespace:'ops',name:'app'}]}
const resource={ref:{provider:'kubernetes',target:'a',kind:'apps/deployments',scope:'blue',name:'web',uid:'resource-uid'},fluxTracking:tracking}
describe('Flux identities and navigation',()=>{
 it('does not treat empty namespace sets as all',()=>{
  expect(contains({mode:'some',names:[]},'blue')).toBe(false)
  expect(contains({mode:'some',names:['blue']},'other')).toBe(false)
  expect(contains({mode:'one',name:'blue'},'blue')).toBe(true)
 })
 it('links only a unique loaded source in the selected namespace',()=>{
  const link={relation:'dependency',group:'kustomize.toolkit.fluxcd.io',kind:'Kustomization',namespace:'ops',name:'app'}
  expect(loadedLink(link,[object],{mode:'one',name:'blue'})).toBeUndefined()
  expect(loadedLink(link,[object],{mode:'all'})).toBe(object)
  expect(loadedLink(link,[object,{...object,ref:{...ref,uid:'other'}}],{mode:'all'})).toBeUndefined()
 })
 it('requires local inventory, exact kind/group, current UIDs and matching target',()=>{
  expect(managerMatches(resource,[object])).toEqual([object])
  expect(managerMatches({...resource,ref:{...resource.ref,uid:''}},[object])).toEqual([])
  for(const o of [{...object,localDestination:false},{...object,truncated:true},{...object,inventory:[]},{...object,ref:{...ref,target:'b'}},{...object,ref:{...ref,uid:''}}])expect(managerMatches(resource,[o])).toEqual([])
  expect(managerMatches({...resource,fluxTracking:{...tracking,group:'wrong'}},[object])).toEqual([])
 })
 it('never guesses HelmRelease membership from labels and respects exact owner UIDs',()=>{
  const helm={...object,kind:'HelmRelease',ref:{...ref,kind:'helm.toolkit.fluxcd.io/helmreleases'}}
  const r={...resource,fluxTracking:{...tracking,candidates:[{kind:'HelmRelease',namespace:'ops',name:'app'}]}}
  expect(managerMatches(r,[helm])).toEqual([])
  expect(managerMatches({...r,fluxTracking:{...tracking,candidates:[{...r.fluxTracking.candidates[0],uid:'manager-uid'}]}},[helm])).toEqual([helm])
  expect(managerMatches({...r,fluxTracking:{...tracking,candidates:[{...r.fluxTracking.candidates[0],uid:'old-uid'}]}},[helm])).toEqual([])
 })
 it('resolves reported inventory only within local scope and an unambiguous served kind',()=>{
  const k={id:'apps/deployments',title:'Deployments',singular:'Deployment',group:'Workloads',scoped:true,columns:[]} satisfies KindDescriptor
  const r=object.inventory[0]
  expect(inventoryRef(object,r,[k],{mode:'one',name:'blue'})).toEqual({...resource.ref,uid:undefined})
  expect(inventoryRef(object,r,[k],{mode:'some',names:[]})).toBeUndefined()
  expect(inventoryRef({...object,localDestination:false},r,[k],{mode:'all'})).toBeUndefined()
  expect(inventoryRef(object,r,[k,{...k,id:'apps/deployments-copy'}],{mode:'all'})).toBeUndefined()
 })
})

it('links only a safe GitHub repository and resolved Git revision to a commit',()=>{
 const o={kind:'GitRepository',url:'https://github.com/example/repo.git',revision:'main@sha1:'+ 'a'.repeat(40)}
 expect(revisionURL(o)).toBe('https://github.com/example/repo/commit/'+'a'.repeat(40))
 expect(revisionURL({...o,url:'https://github.com/tree/'+'a'.repeat(40)})).toBe('https://github.com/tree/'+'a'.repeat(40)+'/commit/'+'a'.repeat(40))
 for(const v of [{...o,kind:'OCIRepository'},{...o,url:'https://github.com/user:pass@evil.invalid/repo'},{...o,revision:'main'},{...o,url:'https://git.example.test/example/repo'}])expect(revisionURL(v)).toBeUndefined()
})
