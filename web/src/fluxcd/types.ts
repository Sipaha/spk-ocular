import type {Ref,ScopeSel} from '../api/types'
import type {ArgoSnapshot} from '../argocd/types'
export interface FluxRequest {provider:string;target:string;scope:ScopeSel}
export interface FluxLink {relation:string;group:string;kind:string;namespace:string;name:string}
export interface FluxResource {group:string;kind:string;version:string;namespace:string;name:string}
export interface FluxObject {
 ref:Ref;kind:string;section:string;state:string;message:string;generation:number;observedGeneration:number
 conditions:{type:string;status:string;reason:string;message:string;observedGeneration:number}[]
 url:string;path:string;revision:string;links:FluxLink[];inventory:FluxResource[];localDestination:boolean;createdAt:number;truncated:boolean
}
export interface FluxSnapshot {objects:FluxObject[];events:ArgoSnapshot['events'];coverage:ArgoSnapshot['coverage'];discovery:string;truncated:boolean;capturedAt:number}
export interface FluxTracking {group:string;kind:string;candidates:{kind:string;name:string;namespace:string;uid?:string}[]}
