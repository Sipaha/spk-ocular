import { describe,it,expect } from 'vitest'
import { attention,resourceRef,managerMatches,repositoryLink,revisionLink,eventCategory } from './model'
import type { ArgoObject,ArgoResource } from './types'
import type { KindDescriptor,ScopeSel } from '../api/types'
const app={ref:{provider:'kubernetes',target:'demo',kind:'argoproj.io/applications',name:'api',scope:'argo',uid:'app-uid'},kind:'Application',localDestination:true,sync:'Synced',health:'Healthy',operation:'Unknown',conditions:[],project:'',operationMessage:'',destination:'https://kubernetes.default.svc',destinationNamespace:'blue',sources:[],resources:[],generators:[],description:'',reconciledAt:0,createdAt:0,truncated:false} satisfies ArgoObject
const resource={group:'apps',version:'v1',kind:'Deployment',name:'api',namespace:'blue'} as ArgoResource
const kinds:KindDescriptor[]=[{id:'apps/deployments',singular:'Deployment',title:'Deployments',scoped:true,group:'Workloads',columns:[]}]
describe('reported resources',()=>{
 it('resolves discovery identity without inventing plural names or a UID',()=>{
  expect(resourceRef(app,resource,kinds,{mode:'one',name:'blue'})).toEqual({provider:'kubernetes',target:'demo',kind:'apps/deployments',name:'api',scope:'blue'})
 })
 it.each<ScopeSel>([{mode:'one',name:'other'},{mode:'some',names:[]},{mode:'some',names:['other']},{mode:'none'}])('does not read beyond selection %j',scope=>expect(resourceRef(app,resource,kinds,scope)).toBeNull())
 it('refuses unknown or remote destination, mismatching group/kind and virtual kinds',()=>{
  expect(resourceRef({...app,localDestination:false},resource,kinds,{mode:'all'})).toBeNull()
  expect(resourceRef(app,{...resource,group:'other'},kinds,{mode:'all'})).toBeNull()
  expect(resourceRef(app,{...resource,kind:'deployment'},kinds,{mode:'all'})).toBeNull()
  expect(resourceRef(app,resource,kinds.map(k=>({...k,workspace:'x'})),{mode:'all'})).toBeNull()
 })
 it('keeps cluster resources distinct and refuses a namespace on them',()=>{
  const r={...resource,group:'',kind:'Node',namespace:''},ks=[{...kinds[0],id:'nodes',singular:'Node',scoped:false}]
  expect(resourceRef(app,r,ks,{mode:'some',names:[]})).toBeNull()
  expect(resourceRef(app,r,ks,{mode:'one',name:'blue'})).toMatchObject({kind:'nodes',scope:undefined})
  expect(resourceRef(app,{...r,namespace:'blue'},ks,{mode:'all'})).toBeNull()
 })
})
it('keeps sync and health independent; absent operation alone is not a failure',()=>{
 expect(attention(app)).toBe(false)
 expect(attention({...app,health:'Unknown'})).toBe(true)
 expect(attention({...app,sync:'OutOfSync'})).toBe(true)
 expect(attention({...app,operation:'Running'})).toBe(true)
 expect(attention({...app,kind:'AppProject'})).toBe(false)
})

it('requires current identity, local controller membership and tracking metadata for reverse links',()=>{
 const resource={ref:{...app.ref,kind:'apps/deployments',name:'api',uid:'current-api',scope:'blue'},argoTracking:{application:'api',method:'annotation' as const,group:'apps',kind:'Deployment',installation:''},health:{state:'ok' as const},facts:[],yaml:'',relations:null}
 const managed={...app,resources:[{group:'apps',version:'v1',kind:'Deployment',name:'api',namespace:'blue',sync:'Synced',health:'Healthy',message:''}]}
 expect(managerMatches(resource,[managed])).toEqual([managed])
 expect(managerMatches({...resource,argoTracking:{...resource.argoTracking,method:'label'}},[{...managed,resources:[]}])).toEqual([])
 expect(managerMatches({...resource,argoTracking:{...resource.argoTracking,installation:'other-instance'}},[managed])).toEqual([])
 expect(managerMatches(resource,[{...managed,localDestination:false}])).toEqual([])
 expect(managerMatches({...resource,ref:{...resource.ref,uid:undefined}},[managed])).toEqual([])
 expect(managerMatches({...resource,argoTracking:{...resource.argoTracking,application:'argo_api'}},[{...managed,ref:{...managed.ref,scope:'argo'}}])).toHaveLength(1)
 expect(managerMatches(resource,[managed,{...managed,ref:{...managed.ref,scope:'other'}}])).toHaveLength(2)
})
it('opens only explicit safe repository URLs and known GitHub commit paths',()=>{
 expect(repositoryLink('javascript:alert(1)')).toBeNull()
 expect(repositoryLink('https://user:password@github.com/org/repo')).toBeNull()
 expect(repositoryLink('https://github.com/org/repo?token=hidden')).toBeNull()
 expect(repositoryLink('https://github.com/org/repo…')).toBeNull()
 const source={repository:'https://github.com/org/repo.git',observedRevision:'a'.repeat(40),path:'',chart:'',targetRevision:'main'}
 expect(revisionLink(source)).toBe('https://github.com/org/repo/tree/'+'a'.repeat(40))
 expect(revisionLink({...source,observedRevision:'main'})).toBeNull()
 expect(revisionLink({...source,repository:'https://other.example/org/repo'})).toBeNull()
 expect(revisionLink({...source,repository:'https://github.com/org/..'})).toBeNull()
})
it('activity categories preserve warning precedence and leave unknown reasons informational',()=>{
 expect(eventCategory({type:'Warning',reason:'OperationCompleted'})).toBe('failure')
 expect(eventCategory({type:'Normal',reason:'OperationStarted'})).toBe('progress')
 expect(eventCategory({type:'Normal',reason:'OperationCompleted'})).toBe('success')
 expect(eventCategory({type:'Normal',reason:'ApplicationUpdated'})).toBe('information')
})
