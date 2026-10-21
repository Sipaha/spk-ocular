import type { Ref, ScopeSel } from '../api/types'
export interface ArgoRequest {provider:string; target:string; scope:ScopeSel}
export interface ArgoSource {repository:string; path:string; chart:string; targetRevision:string; observedRevision:string}
export interface ArgoResource {group:string; version:string; kind:string; name:string; namespace:string; sync:string; health:string; message:string}
export interface ArgoObject {
 ref:Ref; kind:string; project:string; sync:string; health:string; operation:string; operationMessage:string
 destination:string; destinationNamespace:string; localDestination:boolean; sources:ArgoSource[]
 conditions:{type:string;message:string}[]; resources:ArgoResource[]; generators:string[]; description:string
 reconciledAt:number; createdAt:number; truncated:boolean
}
export interface ArgoSnapshot {
 objects:ArgoObject[]; servers:Ref[]; events:{id:string;ref:Ref;openable:boolean;type:string;reason:string;message:string;at:number;count:number}[]
 coverage:{kind:string;scope:string;state:string}[]; discovery:string; truncated:boolean; capturedAt:number
}
