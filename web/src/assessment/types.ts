import type {Ref} from '../api/types'
export type AssessmentCheck='configuration'|'vpa'|'diagnosis'
export interface AssessmentRequest {provider:string;target:string;namespace:string;check:AssessmentCheck}
export interface AssessmentCounts {observed:number;review:number;unknown:number;excluded:number}
export interface AssessmentSnapshot {
 namespace:string;check:AssessmentCheck;capturedAt:number;partial:boolean
 resources:{ref:Ref;state:string;counts:AssessmentCounts;highlights:{field:string;sourceState?:string;freshness?:string;container?:string;code?:string;value?:string}[]}[]
 coverage:{kind:string;state:string}[]
}
