import type {Ref} from '../api/types'
export interface CertificateTime {state:string;value?:string}
export interface CertificateStatus {
 ref:Ref;capturedAt:number;notBefore:CertificateTime;notAfter:CertificateTime;renewal:CertificateTime;validity:string;secretName:string
 conditions:{type:string;status:string;freshness:string;reason?:string}[]
 requestedNames:{type:string;value:string}[]
 issuer?:Ref;issuerState:string;consumers:Ref[];consumerState:string;truncated:boolean
}
