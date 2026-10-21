import type { KindDescriptor, Ref, ScopeSel } from '../api/types'
import type { ArgoObject, ArgoResource } from './types'
export const attention = (o:ArgoObject) => o.kind==='Application' && (o.sync!=='Synced'||o.health!=='Healthy'||!!o.conditions.length||['Running','Terminating','Failed','Error'].includes(o.operation))
export const objectID = (o:ArgoObject) => JSON.stringify([o.ref.kind,o.ref.scope,o.ref.name,o.ref.uid])
/** Argo resource status has no UID. Resolve only explicitly local destinations,
 * discovered exact group/kind pairs and namespaces admitted by the current selection. */
export function resourceRef(o:ArgoObject,r:ArgoResource,kinds:KindDescriptor[],scope:ScopeSel):Ref|null {
 if(!o.localDestination||scope.mode==='none'||scope.mode==='some'&&!scope.names?.length)return null
 const kind=kinds.find(k=>k.singular===r.kind && (k.id.includes('/')?k.id.slice(0,k.id.lastIndexOf('/')):'')===r.group && !k.workspace)
 if(!kind || kind.scoped&&(!r.namespace||scope.mode==='one'&&scope.name!==r.namespace||scope.mode==='some'&&!scope.names?.includes(r.namespace)))return null
 if(!kind.scoped&&r.namespace)return null
 return {provider:o.ref.provider,target:o.ref.target,kind:kind.id,scope:kind.scoped?r.namespace:undefined,name:r.name}
}
