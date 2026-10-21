import type {Ref,HealthState} from '../api/types'
export interface DiagnosisEvidence {
 ref:Ref;source:Ref;rule:string;field:string;state:HealthState;reason:string;message?:string;at?:number;related?:Ref;container?:string;previous?:boolean
}
export interface Diagnosis {ref:Ref;capturedAt:number;evidence:DiagnosisEvidence[];coverage:{source:string;state:string}[];truncated:boolean}
