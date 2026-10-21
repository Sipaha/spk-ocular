import {revisionLink} from '../argocd/model'
import type {KindDescriptor,Ref,ScopeSel} from '../api/types'
import type {FluxLink,FluxObject,FluxResource,FluxTracking} from './types'
export const objectID=(o:FluxObject)=>JSON.stringify([o.ref.kind,o.ref.scope,o.ref.name,o.ref.uid])
export const fluxPage=(ref:Ref)=>'ocular.fluxcd.'+ref.kind.split('/')[1]
export function loadedLink(link:FluxLink,objects:FluxObject[],scope:ScopeSel,origin?:Ref):FluxObject|undefined {
 if(!contains(scope,link.namespace))return
 const matches=objects.filter(o=>(!origin||o.ref.provider===origin.provider&&o.ref.target===origin.target)&&o.ref.uid&&o.ref.name===link.name&&o.ref.scope===link.namespace&&o.kind===link.kind&&o.ref.kind.split('/')[0]===link.group)
 return matches.length===1?matches[0]:undefined
}
export function contains(scope:ScopeSel,namespace:string){return scope.mode==='all'||scope.mode==='one'&&scope.name===namespace||scope.mode==='some'&&(scope.names??[]).includes(namespace)}
export function inventoryRef(o:FluxObject,r:FluxResource,kinds:KindDescriptor[],scope:ScopeSel):Ref|undefined{
 if(!o.localDestination||!o.ref.uid||o.truncated||!r.name||!r.kind||!contains(scope,r.namespace))return
 const matches=kinds.filter(k=>!k.workspace&&k.singular===r.kind&&(k.id.includes('/')?k.id.split('/')[0]:'')===r.group&&(k.scoped?!!r.namespace:!r.namespace))
 if(matches.length!==1)return
 return {provider:o.ref.provider,target:o.ref.target,kind:matches[0].id,scope:r.namespace,name:r.name}
}
export function managerMatches(resource:{ref:Ref;fluxTracking?:FluxTracking},objects:FluxObject[]){
 const h=resource.fluxTracking;if(!h||!resource.ref.uid)return []
 return objects.filter(o=>{
  if(!o.ref.uid||!o.localDestination||o.truncated||o.ref.provider!==resource.ref.provider||o.ref.target!==resource.ref.target)return false
  const candidates=h.candidates.filter(c=>c.kind===o.kind&&c.name===o.ref.name&&c.namespace===o.ref.scope)
  if(!candidates.length)return false
  if(candidates.some(c=>c.uid))return candidates.some(c=>c.uid===o.ref.uid)
  // Kustomization status inventory has no UID: current metadata and exact
  // membership are both required. Helm labels alone remain unverified.
  return o.kind==='Kustomization'&&o.inventory.some(r=>r.group===h.group&&r.kind===h.kind&&r.name===resource.ref.name&&r.namespace===(resource.ref.scope??''))
 })
}

export function revisionURL(o:Pick<FluxObject,'kind'|'url'|'revision'>){
 const hash=o.kind==='GitRepository'?o.revision.match(/(?:^|@sha(?:1|256):)([a-fA-F0-9]{40}|[a-fA-F0-9]{64})$/)?.[1]:undefined
 const link=hash?revisionLink({repository:o.url,path:'',chart:'',targetRevision:'',observedRevision:hash}):undefined
 return link?.replace(/\/tree\/([a-fA-F0-9]{40}|[a-fA-F0-9]{64})$/,'/commit/$1')
}
