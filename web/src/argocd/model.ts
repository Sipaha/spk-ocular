import type { KindDescriptor, Ref, ScopeSel } from '../api/types'
import type { ArgoObject, ArgoResource } from './types'
export const attention = (o:ArgoObject) => o.kind==='Application' && (o.sync!=='Synced'||o.health!=='Healthy'||!!o.conditions.length||['Running','Terminating','Failed','Error'].includes(o.operation))
export const objectID = (o:ArgoObject) => JSON.stringify([o.ref.kind,o.ref.scope,o.ref.name,o.ref.uid])
/** Argo resource status has no UID. Resolve only explicitly local destinations,
 * discovered exact group/kind pairs and namespaces admitted by the current selection. */
export function resourceRef(o:ArgoObject,r:ArgoResource,kinds:KindDescriptor[],scope:ScopeSel):Ref|null {
 if(!o.localDestination||scope.mode==='none'||scope.mode==='some'&&!scope.names?.length)return null
 const matching=kinds.filter(k=>k.singular===r.kind && (k.id.includes('/')?k.id.slice(0,k.id.lastIndexOf('/')):'')===r.group && !k.workspace)
 const kind=matching.length===1?matching[0]:undefined
 if(!kind || kind.scoped&&(!r.namespace||scope.mode==='one'&&scope.name!==r.namespace||scope.mode==='some'&&!scope.names?.includes(r.namespace)))return null
 if(!kind.scoped&&r.namespace)return null
 return {provider:o.ref.provider,target:o.ref.target,kind:kind.id,scope:kind.scoped?r.namespace:undefined,name:r.name}
}

/** Match both current self-tracking metadata and controller membership. A
 * generic instance label alone never produces a managing-Application link. */
export function managerMatches(resource:import('../api/types').Resource,objects:ArgoObject[]):ArgoObject[]{
 const tracking=resource.argoTracking
 if(!tracking||!resource.ref.uid||tracking.installation)return []
 return objects.filter(o=>o.kind==='Application'&&o.ref.uid&&o.ref.provider===resource.ref.provider&&o.ref.target===resource.ref.target&&o.localDestination&&(o.ref.name===tracking.application||`${o.ref.scope}_${o.ref.name}`===tracking.application)&&o.resources.some(r=>r.group===tracking.group&&r.kind===tracking.kind&&r.name===resource.ref.name&&r.namespace===(resource.ref.scope??'')))
}

export function repositoryLink(repository:string):string|null{
 if(repository.endsWith('…'))return null
 try {const url=new URL(repository);return ['https:','http:'].includes(url.protocol)&&!url.username&&!url.password&&!url.search&&!url.hash?url.href:null}catch{return null}
}
export function revisionLink(source:import('./types').ArgoSource):string|null{
 const link=repositoryLink(source.repository)
 if(!link||source.chart||!/^(?:[a-f0-9]{40}|[a-f0-9]{64})$/i.test(source.observedRevision))return null
 const url=new URL(link),parts=url.pathname.replace(/\.git$/,'').split('/').filter(Boolean)
 if(url.hostname!=='github.com'||url.protocol!=='https:'||parts.length!==2||!parts.every(p=>/^[\w.-]+$/.test(p)&&p!=='.'&&p!=='..'))return null
 return `https://github.com/${parts.join('/')}/tree/${source.observedRevision}`
}

export function eventCategory(event:{type:string;reason:string}):'failure'|'progress'|'success'|'information'{
 if(event.type==='Warning'||/fail|error/i.test(event.reason))return 'failure'
 if(/progress|start|running|pending/i.test(event.reason))return 'progress'
 if(/succeed|synced|healthy|completed/i.test(event.reason))return 'success'
 return 'information'
}
