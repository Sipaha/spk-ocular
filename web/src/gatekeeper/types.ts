import type {Ref} from '../api/types'
export interface GatekeeperAudit {
 ref:Ref;apiVersion:string;resourceVersion:string;capturedAt:number;auditTime?:string;auditTimeState:string;totalViolations?:number;returnedViolations:number;coverage:string;partial:boolean
 violations:{group:string;version:string;kind:string;namespace:string;name:string;message:string;enforcementAction:string;enforcementActions:string[]}[]
}
