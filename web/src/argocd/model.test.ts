import { describe,it,expect } from 'vitest'
import { attention,resourceRef } from './model'
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
