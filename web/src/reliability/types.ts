import type {Ref} from '../api/types'
export interface ReliabilityObservation {
 rule:string;version:number;source:Ref;resourceVersion:string;field:string;container?:string
 outcome:'observed'|'review'|'not-applicable'|'unknown';severity:string;code:string;value?:string;reference:string
}
export interface Reliability {
 ref:Ref;resourceVersion:string;capturedAt:number;observations:ReliabilityObservation[];pdbState:string;hpaState:string;partial:boolean
}
